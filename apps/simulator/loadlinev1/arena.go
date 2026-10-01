package loadlinev1

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/kekubhai/Loadline/apps/simulator/internal/arena"
	"github.com/kekubhai/Loadline/apps/simulator/internal/repositories"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
)

// ArenaService implements loadline.v1.ArenaService: LOADLINE's benchmark
// layer.
//
// It owns orchestration only. It loads a stored architecture version,
// converts it to the engine's domain types, asks the arena package to run
// the challenge's standardized benchmark on the SAME in-memory engine the
// other services use, and stores what came back. Metrics, score, and rank
// are always server-computed; the client supplies only a challenge slug, an
// architecture version id, and a display name.
//
// Challenge definitions (list/detail) are served from the embedded,
// version-controlled files, so browsing Arena works even without a
// database. Submissions and leaderboards require persistence.
type ArenaService struct {
	challenges    repositories.ChallengeRepository
	architectures repositories.ArchitectureRepository
	simulations   repositories.SimulationRepository
}

// compile-time check that the generated handler contract is met.
var _ lv1connect.ArenaServiceHandler = (*ArenaService)(nil)

// Submission abuse protection for the MVP: a simple per-(challenge, display
// name) cooldown backed by the submissions table. This is deliberately
// modest — production-scale abuse prevention needs stronger infrastructure
// (see the Arena specification), which is out of scope here.
const (
	submissionCooldown    = 30 * time.Second
	leaderboardDefaultLim = 50
	leaderboardMaxLimit   = 200
)

// NewArenaService wires the Connect service to its repositories. The
// challenge repository may be nil when no database is configured; list and
// detail still work, while submissions report a clear precondition error.
func NewArenaService(
	challenges repositories.ChallengeRepository,
	architectures repositories.ArchitectureRepository,
	simulations repositories.SimulationRepository,
) *ArenaService {
	return &ArenaService{
		challenges:    challenges,
		architectures: architectures,
		simulations:   simulations,
	}
}

// SyncChallenges upserts the embedded definitions into the challenges table
// so submissions can reference them by foreign key. It is idempotent and
// called once at boot. The files remain the source of truth; the table is a
// projection.
func SyncChallenges(ctx context.Context, repo repositories.ChallengeRepository) error {
	if repo == nil {
		return nil
	}
	defs, err := arena.Definitions()
	if err != nil {
		return err
	}
	for _, d := range defs {
		rec, err := definitionToRecord(d)
		if err != nil {
			return fmt.Errorf("arena: sync %s: %w", d.Slug, err)
		}
		if _, err := repo.UpsertChallenge(ctx, rec); err != nil {
			return fmt.Errorf("arena: sync %s: %w", d.Slug, err)
		}
	}
	return nil
}

// definitionToRecord renders a definition as a persistable challenge row.
func definitionToRecord(d arena.Definition) (workspace.Challenge, error) {
	definitionRaw, err := json.Marshal(d)
	if err != nil {
		return workspace.Challenge{}, err
	}
	summary := summaryToProto(d)
	workloadRaw, err := marshalMessage(summary.Workload)
	if err != nil {
		return workspace.Challenge{}, err
	}
	constraintsRaw, err := marshalMessage(summary.Constraints)
	if err != nil {
		return workspace.Challenge{}, err
	}
	scoringRaw, err := marshalMessage(weightsToProto(d.Scoring))
	if err != nil {
		return workspace.Challenge{}, err
	}
	failuresRaw, err := marshalMessage(&v1.FailureScenarioList{Failures: summary.Failures})
	if err != nil {
		return workspace.Challenge{}, err
	}
	reqRaw, err := marshalMessage(&v1.KindRequirementList{Requirements: requirementsToProto(d)})
	if err != nil {
		return workspace.Challenge{}, err
	}
	return workspace.Challenge{
		Slug:             d.Slug,
		Version:          d.Version,
		Definition:       definitionRaw,
		Name:             d.Name,
		Description:      d.Description,
		Difficulty:       string(d.Difficulty),
		Category:         string(d.Category),
		Status:           workspace.ChallengeStatus(d.Status),
		WorkloadConfig:   workloadRaw,
		Constraints:      constraintsRaw,
		ScoringConfig:    scoringRaw,
		FailureScenarios: failuresRaw,
		Requirements:     reqRaw,
		Seed:             d.Seed,
		DurationMS:       durationOf(d),
	}, nil
}

// recordToDefinition reconstructs the canonical benchmark from a stored
// challenge row, so a stored version is self-describing and pinned.
func recordToDefinition(rec workspace.Challenge) (arena.Definition, error) {
	var def arena.Definition
	if err := json.Unmarshal(rec.Definition, &def); err != nil {
		return arena.Definition{}, fmt.Errorf("challenge %s v%d: %w", rec.Slug, rec.Version, err)
	}
	return def, nil
}

// ── Challenges ───────────────────────────────────────────────────────────

func (s *ArenaService) ListChallenges(ctx context.Context, req *connect.Request[v1.ListChallengesRequest]) (*connect.Response[v1.ListChallengesResponse], error) {
	// With a database, the projection is authoritative; without one the
	// embedded definitions are served so Arena browsing still works.
	if s.challenges != nil {
		recs, err := s.challenges.ListChallenges(ctx)
		if err != nil {
			return nil, connectError("list challenges", err)
		}
		out := make([]*v1.ChallengeSummary, 0, len(recs))
		for _, rec := range recs {
			def, err := recordToDefinition(rec)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("stored challenge is unreadable"))
			}
			out = append(out, summaryToProto(def))
		}
		return connect.NewResponse(&v1.ListChallengesResponse{Challenges: out}), nil
	}
	defs, err := arena.Definitions()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("challenge definitions unavailable"))
	}
	out := make([]*v1.ChallengeSummary, 0, len(defs))
	for _, d := range defs {
		out = append(out, summaryToProto(d))
	}
	return connect.NewResponse(&v1.ListChallengesResponse{Challenges: out}), nil
}

func (s *ArenaService) GetChallenge(ctx context.Context, req *connect.Request[v1.GetChallengeRequest]) (*connect.Response[v1.GetChallengeResponse], error) {
	if s.challenges != nil {
		rec, err := s.challenges.GetChallenge(ctx, req.Msg.GetSlug())
		switch {
		case err == nil:
			def, err := recordToDefinition(rec)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("stored challenge is unreadable"))
			}
			return connect.NewResponse(&v1.GetChallengeResponse{Challenge: definitionToProto(def)}), nil
		case workspace.IsNotFound(err):
			// Fall back to the embedded definition below (e.g. sync has not
			// run yet); a stored-but-unreadable challenge is an error.
		default:
			return nil, connectError("get challenge", err)
		}
	}
	def, ok := arena.BySlug(req.Msg.GetSlug())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("challenge %q not found", req.Msg.GetSlug()))
	}
	return connect.NewResponse(&v1.GetChallengeResponse{Challenge: definitionToProto(def)}), nil
}

// ── Submission ───────────────────────────────────────────────────────────

func (s *ArenaService) SubmitChallenge(ctx context.Context, req *connect.Request[v1.SubmitChallengeRequest]) (*connect.Response[v1.SubmitChallengeResponse], error) {
	in := req.Msg
	if s.challenges == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("arena submissions require a configured database"))
	}
	name, err := arena.ValidateDisplayName(in.GetDisplayName())
	if err != nil {
		return nil, invalid(err.Error())
	}
	if in.GetArchitectureVersionId() == "" {
		return nil, invalid("architecture_version_id is required")
	}
	// The benchmark definition comes from the stored, version-pinned row
	// (a projection of the embedded files). The client cannot supply it.
	rec, err := s.challenges.GetChallenge(ctx, in.GetChallengeSlug())
	if err != nil {
		return nil, connectError("submit challenge", err)
	}
	def, err := recordToDefinition(rec)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("stored challenge is unreadable"))
	}
	if def.Status != arena.StatusActive {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("challenge %q is %s and does not accept submissions", def.Slug, def.Status))
	}

	// The architecture comes from storage; a submission can never benchmark
	// a document the server has not seen.
	version, err := s.architectures.GetVersion(ctx, in.GetArchitectureVersionId())
	if err != nil {
		return nil, connectError("submit challenge", err)
	}
	archProto := &v1.Architecture{}
	if err := protojson.Unmarshal(version.Definition, archProto); err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("stored architecture definition is unreadable"))
	}
	arch, resolved, err := protoArchToDomain(archProto)
	if err != nil {
		return nil, invalid(fmt.Sprintf("architecture: %v", err))
	}
	if err := def.CheckRequirements(arch); err != nil {
		return nil, invalid(err.Error())
	}

	// Cooldown (MVP anti-abuse).
	count, err := s.challenges.CountRecentSubmissions(ctx, rec.ID, name, time.Now().Add(-submissionCooldown))
	if err != nil {
		return nil, connectError("submit challenge", err)
	}
	if count > 0 {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("please wait %s before submitting again", submissionCooldown))
	}

	started := time.Now().UTC()
	outcome, runErr := arena.Run(def, arch, resolved)
	finished := time.Now().UTC()

	if runErr != nil {
		// The engine rejected the run. Record the failure honestly: a
		// failed submission carries its error and no metrics, and never
		// ranks.
		created, err := s.challenges.CreateSubmission(ctx, workspace.ChallengeSubmission{
			ChallengeID:           rec.ID,
			ChallengeSlug:         def.Slug,
			ChallengeVersion:      def.Version,
			DisplayName:           name,
			ArchitectureVersionID: version.ID,
			Status:                workspace.SubmissionFailed,
			Seed:                  def.Seed,
			Error:                 runErr.Error(),
		})
		if err != nil {
			return nil, connectError("persist submission", err)
		}
		sub, err := submissionToProto(created, 0)
		if err != nil {
			return nil, connectError("encode submission", err)
		}
		return connect.NewResponse(&v1.SubmitChallengeResponse{Submission: sub}), nil
	}

	// Persist the benchmark execution on the same simulation-run path every
	// other run uses, so its metrics are stored exactly once.
	runRecord := workspace.SimulationRun{
		ArchitectureVersionID: version.ID,
		Status:                workspace.RunStatusCompleted,
		Seed:                  outcome.Seed,
		DurationMS:            outcome.Metrics.DurationMS,
		StartedAt:             &started,
		FinishedAt:            &finished,
	}
	payload, err := buildRunOutput(outcome.Run, resolved).payload()
	if err != nil {
		return nil, connectError("persist benchmark result", err)
	}
	persistedRun, _, err := s.simulations.PersistRunWithResult(ctx, runRecord, payload)
	if err != nil {
		return nil, connectError("persist benchmark run", err)
	}

	metricsRaw, err := marshalMessage(metricsToProto(outcome.Metrics))
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	planRaw, err := marshalMessage(domainPlanToProto(outcome.Plan))
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	diagRaw, err := marshalMessage(diagnosisToProto(outcome.Diagnosis))
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	capacityRaw, err := marshalMessage(&v1.CapacityReportList{Reports: capacityToProto(outcome.Capacity)})
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	costRaw, err := marshalMessage(costToProto(outcome.Cost))
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	breakdownRaw, err := marshalMessage(scoreBreakdownToProto(outcome.Score))
	if err != nil {
		return nil, connectError("encode submission", err)
	}

	created, err := s.challenges.CreateSubmission(ctx, workspace.ChallengeSubmission{
		ChallengeID:           rec.ID,
		ChallengeSlug:         def.Slug,
		ChallengeVersion:      def.Version,
		DisplayName:           name,
		ArchitectureVersionID: version.ID,
		SimulationRunID:       persistedRun.ID,
		Status:                workspace.SubmissionCompleted,
		Score:                 outcome.Score.Score,
		Metrics:               metricsRaw,
		Plan:                  planRaw,
		Bottlenecks:           diagRaw,
		Capacity:              capacityRaw,
		Cost:                  costRaw,
		ScoreBreakdown:        breakdownRaw,
		Seed:                  outcome.Seed,
	})
	if err != nil {
		return nil, connectError("persist submission", err)
	}
	rank, err := s.challenges.SubmissionRank(ctx, created)
	if err != nil {
		return nil, connectError("rank submission", err)
	}
	sub, err := submissionToProto(created, rank)
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	return connect.NewResponse(&v1.SubmitChallengeResponse{Submission: sub}), nil
}

func (s *ArenaService) GetChallengeSubmission(ctx context.Context, req *connect.Request[v1.GetChallengeSubmissionRequest]) (*connect.Response[v1.GetChallengeSubmissionResponse], error) {
	if s.challenges == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("arena submissions require a configured database"))
	}
	rec, err := s.challenges.GetSubmission(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connectError("get submission", err)
	}
	var rank int64
	if rec.Status == workspace.SubmissionCompleted {
		rank, err = s.challenges.SubmissionRank(ctx, rec)
		if err != nil {
			return nil, connectError("rank submission", err)
		}
	}
	sub, err := submissionToProto(rec, rank)
	if err != nil {
		return nil, connectError("encode submission", err)
	}
	return connect.NewResponse(&v1.GetChallengeSubmissionResponse{Submission: sub}), nil
}

// ── Leaderboard ──────────────────────────────────────────────────────────

func (s *ArenaService) GetChallengeLeaderboard(ctx context.Context, req *connect.Request[v1.GetChallengeLeaderboardRequest]) (*connect.Response[v1.GetChallengeLeaderboardResponse], error) {
	if s.challenges == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("arena leaderboards require a configured database"))
	}
	// Resolve the challenge from the stored projection (authoritative when
	// present), falling back to the embedded definitions.
	var def arena.Definition
	if crec, err := s.challenges.GetChallenge(ctx, req.Msg.GetSlug()); err == nil {
		def, err = recordToDefinition(crec)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("stored challenge is unreadable"))
		}
	} else if !workspace.IsNotFound(err) {
		return nil, connectError("get challenge", err)
	} else if embedded, ok := arena.BySlug(req.Msg.GetSlug()); ok {
		def = embedded
	} else {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("challenge %q not found", req.Msg.GetSlug()))
	}
	version := int(req.Msg.GetVersion())
	if version == 0 {
		version = def.Version
	}
	rec, err := s.challenges.GetChallengeVersion(ctx, def.Slug, version)
	if err != nil {
		return nil, connectError("get challenge version", err)
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = leaderboardDefaultLim
	}
	if limit > leaderboardMaxLimit {
		limit = leaderboardMaxLimit
	}

	rows, err := s.challenges.Leaderboard(ctx, rec.ID, version, limit, req.Msg.GetRecent())
	if err != nil {
		return nil, connectError("leaderboard", err)
	}
	entries := make([]*v1.LeaderboardEntry, 0, len(rows))
	for i, row := range rows {
		rank := int64(i + 1)
		if req.Msg.GetRecent() {
			// A recent listing is not score-ordered, so the displayed rank
			// is the row's true standing.
			rank, err = s.challenges.SubmissionRank(ctx, row)
			if err != nil {
				return nil, connectError("rank submission", err)
			}
		}
		entry, err := leaderboardEntry(row, rank)
		if err != nil {
			return nil, connectError("encode leaderboard entry", err)
		}
		entries = append(entries, entry)
	}
	return connect.NewResponse(&v1.GetChallengeLeaderboardResponse{
		Slug:    def.Slug,
		Version: int32(version),
		Entries: entries,
	}), nil
}

// leaderboardEntry renders one public row. Only benchmark data is exposed.
func leaderboardEntry(s workspace.ChallengeSubmission, rank int64) (*v1.LeaderboardEntry, error) {
	metrics, err := unmarshalMetrics(s.Metrics)
	if err != nil {
		return nil, err
	}
	cost, err := unmarshalCost(s.Cost)
	if err != nil {
		return nil, err
	}
	entry := &v1.LeaderboardEntry{
		Rank:         rank,
		SubmissionId: s.ID,
		DisplayName:  s.DisplayName,
		Score:        s.Score,
		SubmittedAt:  timestampFromTime(s.SubmittedAt),
	}
	if metrics != nil {
		entry.ThroughputRps = achievedRPS(metrics)
		entry.P95Ms = metrics.GetP95Ms()
		entry.P99Ms = metrics.GetP99Ms()
		entry.ErrorRate = metrics.GetErrorRate()
	}
	if cost != nil {
		entry.MonthlyCostUsd = cost.GetTotal()
		entry.CostKnown = true
	}
	return entry, nil
}

// achievedRPS mirrors arena's completion-rate definition for display.
func achievedRPS(m *v1.SystemMetrics) float64 {
	sec := m.GetDurationMs() / 1000
	if sec <= 0 {
		return 0
	}
	return float64(m.GetCompleted()) / sec
}
