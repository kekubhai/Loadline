package loadlinev1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"connectrpc.com/connect"

	"github.com/kekubhai/Loadline/apps/simulator/internal/arena"
	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/repositories"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
)

// newArenaServiceNoDB serves the Arena service with no persistence: challenge
// browsing must work, submissions must fail with a clear precondition.
func newArenaServiceNoDB(t *testing.T) lv1connect.ArenaServiceClient {
	t.Helper()
	path, handler := lv1connect.NewArenaServiceHandler(NewArenaService(nil, nil, nil))
	srv := httptest.NewServer(newMux(path, handler))
	t.Cleanup(srv.Close)
	return lv1connect.NewArenaServiceClient(srv.Client(), srv.URL)
}

func TestArenaListAndGetChallengeWithoutDB(t *testing.T) {
	c := newArenaServiceNoDB(t)
	ctx := context.Background()

	list, err := c.ListChallenges(ctx, connect.NewRequest(&v1.ListChallengesRequest{}))
	if err != nil {
		t.Fatalf("ListChallenges: %v", err)
	}
	if len(list.Msg.GetChallenges()) != 5 {
		t.Fatalf("expected 5 challenges, got %d", len(list.Msg.GetChallenges()))
	}

	got, err := c.GetChallenge(ctx, connect.NewRequest(&v1.GetChallengeRequest{Slug: "social-feed"}))
	if err != nil {
		t.Fatalf("GetChallenge: %v", err)
	}
	ch := got.Msg.GetChallenge()
	if ch.GetSeed() == 0 {
		t.Fatal("challenge must expose its benchmark seed")
	}
	if len(ch.GetRequirements()) == 0 {
		t.Fatal("challenge must expose its requirements")
	}
	if len(ch.GetSummary().GetFailures()) == 0 {
		t.Fatal("social-feed must expose its failure scenario")
	}
	if ch.GetSummary().GetConstraints().GetMonthlyBudgetUsd() <= 0 {
		t.Fatal("challenge must expose its budget target")
	}

	if _, err := c.GetChallenge(ctx, connect.NewRequest(&v1.GetChallengeRequest{Slug: "nope"})); err == nil {
		t.Fatal("expected not-found for an unknown challenge")
	} else if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound, got %v", connect.CodeOf(err))
	}
}

func TestArenaSubmitRequiresDatabase(t *testing.T) {
	c := newArenaServiceNoDB(t)
	_, err := c.SubmitChallenge(context.Background(), connect.NewRequest(&v1.SubmitChallengeRequest{
		ChallengeSlug: "social-feed", ArchitectureVersionId: "x", DisplayName: "Anirban",
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition without a database, got %v", err)
	}
}

// ── Database-backed round trip ───────────────────────────────────────────

type arenaTestEnv struct {
	arena      lv1connect.ArenaServiceClient
	workspace  lv1connect.WorkspaceServiceClient
	database   *db.Database
	challenges repositories.ChallengeRepository
}

// newArenaTestEnv spins up the real Workspace + Arena services over real
// repositories against a real PostgreSQL database. It SKIPS when no database
// is configured.
func newArenaTestEnv(t *testing.T) *arenaTestEnv {
	t.Helper()
	db.LoadDotEnv()
	url := os.Getenv(db.EnvTestDatabaseURL)
	if url == "" {
		url = os.Getenv(db.EnvDatabaseURL)
	}
	if url == "" {
		t.Skipf("set %s (or %s) to run arena persistence tests", db.EnvTestDatabaseURL, db.EnvDatabaseURL)
	}
	ctx := context.Background()
	database, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(database.Close)
	if err := db.Migrate(ctx, database.Pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	projects := repositories.NewProjectRepository(database)
	architectures := repositories.NewArchitectureRepository(database)
	workloads := repositories.NewWorkloadRepository(database)
	simulations := repositories.NewSimulationRepository(database)
	challenges := repositories.NewChallengeRepository(database)

	mux := http.NewServeMux()
	wsPath, wsHandler := lv1connect.NewWorkspaceServiceHandler(
		NewWorkspaceService(projects, architectures, workloads, simulations))
	mux.Handle(wsPath, wsHandler)
	arenaPath, arenaHandler := lv1connect.NewArenaServiceHandler(
		NewArenaService(challenges, architectures, simulations))
	mux.Handle(arenaPath, arenaHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &arenaTestEnv{
		arena:      lv1connect.NewArenaServiceClient(srv.Client(), srv.URL),
		workspace:  lv1connect.NewWorkspaceServiceClient(srv.Client(), srv.URL),
		database:   database,
		challenges: challenges,
	}
}

// seedTestChallenge inserts a lightweight challenge so the benchmark runs in
// milliseconds instead of the embedded challenges' 60s horizons.
func (e *arenaTestEnv) seedTestChallenge(t *testing.T) string {
	t.Helper()
	const slug = "arena-test"
	def := arena.Definition{
		Slug: slug, Version: 1, Name: "Arena Test", Description: "test benchmark",
		Difficulty: arena.DifficultyBeginner, Category: arena.CategoryAPI, Status: arena.StatusActive,
		Workload: arena.Workload{
			// Large enough to generate a few hundred requests in the short
			// 1.5s test horizon (≈230 peak RPS).
			TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 50,
			PeakMultiplier: 2, ReadWriteRatio: 4, PayloadBytes: 1024, DurationMS: 1500,
		},
		Constraints: arena.Constraints{
			TargetThroughputRPS: 5, TargetP99MS: 500, TargetErrorRate: 0.1, MonthlyBudgetUSD: 100000,
		},
		Requirements: arena.Requirements{RequiredKinds: []arena.KindRequirement{
			{Kind: "cache", Min: 1}, {Kind: "database", Min: 1},
		}},
		Failures: []arena.FailureScenario{{
			TargetKind: "cache", Type: "crash", StartMS: 400, DurationMS: 400, PassThrough: true,
		}},
		Scoring: arena.Weights{Throughput: 1, Latency: 1, Reliability: 1, Cost: 1, FailureRecovery: 1},
		Seed:    99,
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.challenges.UpsertChallenge(context.Background(), workspace.Challenge{
		Slug: slug, Version: 1, Name: def.Name, Description: def.Description,
		Difficulty: string(def.Difficulty), Category: string(def.Category),
		Status: workspace.ChallengeActive, Definition: raw,
		WorkloadConfig: json.RawMessage("{}"), Constraints: json.RawMessage("{}"),
		ScoringConfig: json.RawMessage("{}"), FailureScenarios: json.RawMessage("[]"),
		Requirements: json.RawMessage("{}"), Seed: def.Seed, DurationMS: def.Workload.DurationMS,
	})
	if err != nil {
		t.Fatalf("seed challenge: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.database.Pool.Exec(context.Background(), `DELETE FROM challenges WHERE slug = $1`, slug)
	})
	return slug
}

func TestArenaSubmitBenchmarkRoundTrip(t *testing.T) {
	e := newArenaTestEnv(t)
	ctx := context.Background()
	slug := e.seedTestChallenge(t)

	// Build a stored architecture (client → api → cache → db) to submit.
	project, err := e.workspace.CreateProject(ctx, connect.NewRequest(&v1.CreateProjectRequest{Name: "arena-test"}))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.workspace.DeleteProject(context.Background(),
			connect.NewRequest(&v1.DeleteProjectRequest{Id: project.Msg.GetProject().GetId()}))
	})
	arch, err := e.workspace.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId: project.Msg.GetProject().GetId(), Name: "web", Definition: testArchitecture(),
	}))
	if err != nil {
		t.Fatalf("CreateArchitecture: %v", err)
	}
	versionID := arch.Msg.GetVersion().GetId()

	submit := func(name string) *v1.Submission {
		t.Helper()
		resp, err := e.arena.SubmitChallenge(ctx, connect.NewRequest(&v1.SubmitChallengeRequest{
			ChallengeSlug: slug, ArchitectureVersionId: versionID, DisplayName: name,
		}))
		if err != nil {
			t.Fatalf("SubmitChallenge(%s): %v", name, err)
		}
		return resp.Msg.GetSubmission()
	}

	first := submit("Anirban")
	if first.GetId() == "" {
		t.Fatal("submission has no server-assigned id")
	}
	if first.GetStatus() != "completed" {
		t.Fatalf("status = %q (%s), want completed", first.GetStatus(), first.GetError())
	}
	if first.GetMetrics().GetGenerated() == 0 {
		t.Fatal("benchmark produced no traffic")
	}
	if first.GetPlan().GetPeakRps() <= 0 {
		t.Fatal("expected the derived workload plan")
	}
	if first.GetScore() <= 0 || first.GetScore() > 100 {
		t.Fatalf("score out of range: %v", first.GetScore())
	}
	if first.GetRank() != 1 {
		t.Fatalf("first submission rank = %d, want 1", first.GetRank())
	}
	if len(first.GetBreakdown().GetComponents()) == 0 {
		t.Fatal("expected an explainable score breakdown")
	}
	if first.GetSeed() != 99 {
		t.Fatalf("seed = %d, want the challenge seed 99", first.GetSeed())
	}

	// A second architect, same architecture → same score, later submission
	// → deterministic tie-break puts them second.
	second := submit("Priya")
	if second.GetRank() != 2 {
		t.Fatalf("second submission rank = %d, want 2 (tie-break by time)", second.GetRank())
	}

	// Re-submitting the same name within the cooldown is rejected.
	if _, err := e.arena.SubmitChallenge(ctx, connect.NewRequest(&v1.SubmitChallengeRequest{
		ChallengeSlug: slug, ArchitectureVersionId: versionID, DisplayName: "Anirban",
	})); err == nil || connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected ResourceExhausted for a cooldown resubmit, got %v", err)
	}

	// Read the first submission back; metrics and score are identical.
	got, err := e.arena.GetChallengeSubmission(ctx, connect.NewRequest(&v1.GetChallengeSubmissionRequest{Id: first.GetId()}))
	if err != nil {
		t.Fatalf("GetChallengeSubmission: %v", err)
	}
	if got.Msg.GetSubmission().GetScore() != first.GetScore() {
		t.Fatalf("stored score = %v, want %v", got.Msg.GetSubmission().GetScore(), first.GetScore())
	}

	// Leaderboard: score-ordered, ranks 1..n, only public data.
	lb, err := e.arena.GetChallengeLeaderboard(ctx, connect.NewRequest(&v1.GetChallengeLeaderboardRequest{Slug: slug}))
	if err != nil {
		t.Fatalf("GetChallengeLeaderboard: %v", err)
	}
	entries := lb.Msg.GetEntries()
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 leaderboard entries, got %d", len(entries))
	}
	if entries[0].GetRank() != 1 || entries[1].GetRank() != 2 {
		t.Fatalf("ranks = %d,%d want 1,2", entries[0].GetRank(), entries[1].GetRank())
	}
	if entries[0].GetScore() < entries[1].GetScore() {
		t.Fatal("leaderboard must be score-ordered descending")
	}
	if entries[0].GetDisplayName() == "" || entries[0].GetSubmissionId() == "" {
		t.Fatal("leaderboard entries must carry display name + id")
	}
}

func TestArenaSubmitValidation(t *testing.T) {
	e := newArenaTestEnv(t)
	ctx := context.Background()
	slug := e.seedTestChallenge(t)

	project, err := e.workspace.CreateProject(ctx, connect.NewRequest(&v1.CreateProjectRequest{Name: "arena-validation"}))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.workspace.DeleteProject(context.Background(),
			connect.NewRequest(&v1.DeleteProjectRequest{Id: project.Msg.GetProject().GetId()}))
	})
	arch, err := e.workspace.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId: project.Msg.GetProject().GetId(), Name: "web", Definition: testArchitecture(),
	}))
	if err != nil {
		t.Fatalf("CreateArchitecture: %v", err)
	}
	versionID := arch.Msg.GetVersion().GetId()

	// Invalid display name.
	if _, err := e.arena.SubmitChallenge(ctx, connect.NewRequest(&v1.SubmitChallengeRequest{
		ChallengeSlug: slug, ArchitectureVersionId: versionID, DisplayName: "a",
	})); err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument for a bad name, got %v", err)
	}

	// Unknown challenge.
	if _, err := e.arena.SubmitChallenge(ctx, connect.NewRequest(&v1.SubmitChallengeRequest{
		ChallengeSlug: "does-not-exist", ArchitectureVersionId: versionID, DisplayName: "Anirban",
	})); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound for an unknown challenge, got %v", err)
	}

	// A missing required kind (no cache) fails the challenge requirements.
	noCache := &v1.Architecture{
		SchemaVersion: "1", Name: "no-cache",
		Components: []*v1.ComponentSpec{
			{Id: "client", Kind: v1.ComponentKind_COMPONENT_KIND_CLIENT},
			{Id: "db", Kind: v1.ComponentKind_COMPONENT_KIND_DATABASE,
				Provider: "aws", Service: "rds", Config: &v1.ProviderConfig{StorageGb: 10}},
		},
		Links: []*v1.Link{{From: "client", To: "db"}},
	}
	a2, err := e.workspace.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId: project.Msg.GetProject().GetId(), Name: "no-cache", Definition: noCache,
	}))
	if err != nil {
		t.Fatalf("CreateArchitecture(no-cache): %v", err)
	}
	if _, err := e.arena.SubmitChallenge(ctx, connect.NewRequest(&v1.SubmitChallengeRequest{
		ChallengeSlug: slug, ArchitectureVersionId: a2.Msg.GetVersion().GetId(), DisplayName: "Anirban",
	})); err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument for a missing required kind, got %v", err)
	}
}

// guard: workspace.IsNotFound must classify the repository's not-found error.
func TestWorkspaceIsNotFoundClassifiesArenaRepo(t *testing.T) {
	e := newArenaTestEnv(t)
	_, err := e.challenges.GetChallenge(context.Background(), "no-such-challenge")
	if err == nil || !workspace.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}
