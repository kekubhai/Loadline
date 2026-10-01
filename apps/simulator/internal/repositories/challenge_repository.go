package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// ChallengeRepository is the persistence contract for Arena challenges and
// their submissions.
//
// Challenges are a projection of the embedded definition files (upserted at
// boot); submissions are append-only benchmark records. Rank is never
// stored — Leaderboard derives the authoritative ordering, and
// SubmissionRank derives one submission's place in it.
type ChallengeRepository interface {
	// UpsertChallenge inserts or refreshes a challenge version from its
	// canonical definition. It is idempotent on (slug, version).
	UpsertChallenge(ctx context.Context, c workspace.Challenge) (workspace.Challenge, error)
	// GetChallenge returns the newest version of a slug.
	GetChallenge(ctx context.Context, slug string) (workspace.Challenge, error)
	// GetChallengeVersion returns one specific version of a slug.
	GetChallengeVersion(ctx context.Context, slug string, version int) (workspace.Challenge, error)
	// ListChallenges returns every stored challenge, newest version first
	// per slug, ordered for display.
	ListChallenges(ctx context.Context) ([]workspace.Challenge, error)

	// CreateSubmission records a benchmarked architecture.
	CreateSubmission(ctx context.Context, s workspace.ChallengeSubmission) (workspace.ChallengeSubmission, error)
	GetSubmission(ctx context.Context, id string) (workspace.ChallengeSubmission, error)
	// Leaderboard returns completed submissions for one challenge version,
	// either score-ranked or most-recent, capped at limit.
	Leaderboard(ctx context.Context, challengeID string, version, limit int, recent bool) ([]workspace.ChallengeSubmission, error)
	// SubmissionRank returns a completed submission's 1-based position.
	SubmissionRank(ctx context.Context, s workspace.ChallengeSubmission) (int64, error)
	// CountRecentSubmissions counts one display name's submissions to a
	// challenge since a cutoff (the anti-abuse cooldown).
	CountRecentSubmissions(ctx context.Context, challengeID, displayName string, since time.Time) (int, error)
}

type postgresChallenges struct {
	db *db.Database
}

// NewChallengeRepository returns the PostgreSQL-backed Arena repository.
func NewChallengeRepository(database *db.Database) ChallengeRepository {
	return &postgresChallenges{db: database}
}

const challengeColumns = `id::text, slug, version, name, description, difficulty, category,
	status, definition, workload_config, constraints, scoring_config, failure_scenarios,
	requirements, seed, duration_ms, created_at, updated_at`

func scanChallenge(row interface{ Scan(...any) error }) (workspace.Challenge, error) {
	var c workspace.Challenge
	var status string
	var seed int64
	var definition, workload, constraints, scoring, failures, requirements []byte
	if err := row.Scan(&c.ID, &c.Slug, &c.Version, &c.Name, &c.Description,
		&c.Difficulty, &c.Category, &status, &definition, &workload, &constraints, &scoring,
		&failures, &requirements, &seed, &c.DurationMS, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return workspace.Challenge{}, err
	}
	c.Status = workspace.ChallengeStatus(status)
	c.Seed = uint64(seed)
	c.Definition = definition
	c.WorkloadConfig, c.Constraints, c.ScoringConfig = workload, constraints, scoring
	c.FailureScenarios, c.Requirements = failures, requirements
	return c, nil
}

func (r *postgresChallenges) UpsertChallenge(ctx context.Context, c workspace.Challenge) (workspace.Challenge, error) {
	if err := c.Validate(); err != nil {
		return workspace.Challenge{}, err
	}
	if c.ID == "" {
		c.ID = workspace.NewID()
	}
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO challenges
			(id, slug, version, name, description, difficulty, category, status, definition,
			 workload_config, constraints, scoring_config, failure_scenarios,
			 requirements, seed, duration_ms)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb,
			$10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14::jsonb, $15, $16)
		ON CONFLICT (slug, version) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			difficulty = EXCLUDED.difficulty,
			category = EXCLUDED.category,
			status = EXCLUDED.status,
			definition = EXCLUDED.definition,
			workload_config = EXCLUDED.workload_config,
			constraints = EXCLUDED.constraints,
			scoring_config = EXCLUDED.scoring_config,
			failure_scenarios = EXCLUDED.failure_scenarios,
			requirements = EXCLUDED.requirements,
			seed = EXCLUDED.seed,
			duration_ms = EXCLUDED.duration_ms,
			updated_at = now()
		RETURNING id::text, created_at, updated_at`,
		c.ID, c.Slug, c.Version, c.Name, c.Description, c.Difficulty, c.Category, string(c.Status),
		jsonOrEmpty(c.Definition, "{}"),
		jsonOrEmpty(c.WorkloadConfig, "{}"),
		jsonOrEmpty(c.Constraints, "{}"),
		jsonOrEmpty(c.ScoringConfig, "{}"),
		jsonOrEmpty(c.FailureScenarios, "[]"),
		jsonOrEmpty(c.Requirements, "{}"),
		int64(c.Seed), c.DurationMS).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return workspace.Challenge{}, fmt.Errorf("repositories: upsert challenge: %w", err)
	}
	return c, nil
}

func (r *postgresChallenges) GetChallenge(ctx context.Context, slug string) (workspace.Challenge, error) {
	c, err := scanChallenge(r.db.Pool.QueryRow(ctx,
		`SELECT `+challengeColumns+` FROM challenges
		 WHERE slug = $1 ORDER BY version DESC LIMIT 1`, slug))
	if err != nil {
		return workspace.Challenge{}, notFoundOrError(err, "challenge", slug)
	}
	return c, nil
}

func (r *postgresChallenges) GetChallengeVersion(ctx context.Context, slug string, version int) (workspace.Challenge, error) {
	c, err := scanChallenge(r.db.Pool.QueryRow(ctx,
		`SELECT `+challengeColumns+` FROM challenges
		 WHERE slug = $1 AND version = $2`, slug, version))
	if err != nil {
		return workspace.Challenge{}, notFoundOrError(err, "challenge version", slug)
	}
	return c, nil
}

func (r *postgresChallenges) ListChallenges(ctx context.Context) ([]workspace.Challenge, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT `+challengeColumns+` FROM challenges c
		WHERE version = (SELECT MAX(version) FROM challenges WHERE slug = c.slug)
		ORDER BY CASE difficulty
			WHEN 'beginner' THEN 0 WHEN 'intermediate' THEN 1
			WHEN 'advanced' THEN 2 WHEN 'expert' THEN 3 ELSE 4 END, name, slug`)
	if err != nil {
		return nil, fmt.Errorf("repositories: list challenges: %w", err)
	}
	defer rows.Close()
	out := []workspace.Challenge{}
	for rows.Next() {
		c, err := scanChallenge(rows)
		if err != nil {
			return nil, fmt.Errorf("repositories: scan challenge: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const submissionColumns = `id::text, challenge_id::text, challenge_slug, challenge_version,
	display_name, architecture_version_id::text,
	COALESCE(simulation_run_id::text, '') AS simulation_run_id,
	status, score, metrics, plan, bottlenecks, capacity, cost, score_breakdown,
	seed, error, submitted_at`

func scanSubmission(row interface{ Scan(...any) error }) (workspace.ChallengeSubmission, error) {
	var s workspace.ChallengeSubmission
	var status string
	var seed int64
	var metrics, plan, bottlenecks, capacity, cost, breakdown []byte
	if err := row.Scan(&s.ID, &s.ChallengeID, &s.ChallengeSlug, &s.ChallengeVersion,
		&s.DisplayName, &s.ArchitectureVersionID, &s.SimulationRunID, &status, &s.Score,
		&metrics, &plan, &bottlenecks, &capacity, &cost, &breakdown,
		&seed, &s.Error, &s.SubmittedAt); err != nil {
		return workspace.ChallengeSubmission{}, err
	}
	s.Status = workspace.SubmissionStatus(status)
	s.Seed = uint64(seed)
	s.Metrics, s.Plan, s.Bottlenecks = metrics, plan, bottlenecks
	s.Capacity, s.Cost, s.ScoreBreakdown = capacity, cost, breakdown
	return s, nil
}

func (r *postgresChallenges) CreateSubmission(ctx context.Context, s workspace.ChallengeSubmission) (workspace.ChallengeSubmission, error) {
	if err := s.Validate(); err != nil {
		return workspace.ChallengeSubmission{}, err
	}
	if s.ID == "" {
		s.ID = workspace.NewID()
	}
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO challenge_submissions
			(id, challenge_id, challenge_slug, challenge_version, display_name,
			 architecture_version_id, simulation_run_id, status, score,
			 metrics, plan, bottlenecks, capacity, cost, score_breakdown, seed, error)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, NULLIF($7, '')::uuid, $8, $9,
			$10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb, $16, $17)
		RETURNING submitted_at`,
		s.ID, s.ChallengeID, s.ChallengeSlug, s.ChallengeVersion, s.DisplayName,
		s.ArchitectureVersionID, s.SimulationRunID, string(s.Status), s.Score,
		jsonOrEmpty(s.Metrics, "{}"),
		jsonOrEmpty(s.Plan, "{}"),
		jsonOrEmpty(s.Bottlenecks, "{}"),
		jsonOrEmpty(s.Capacity, "[]"),
		jsonOrEmpty(s.Cost, "{}"),
		jsonOrEmpty(s.ScoreBreakdown, "{}"),
		int64(s.Seed), s.Error).Scan(&s.SubmittedAt)
	if err != nil {
		return workspace.ChallengeSubmission{}, wrapForeignKey(err, fmt.Errorf("repositories: create submission: %w", err))
	}
	return s, nil
}

func (r *postgresChallenges) GetSubmission(ctx context.Context, id string) (workspace.ChallengeSubmission, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.ChallengeSubmission{}, err
	}
	s, err := scanSubmission(r.db.Pool.QueryRow(ctx,
		`SELECT `+submissionColumns+` FROM challenge_submissions WHERE id = $1::uuid`, id))
	if err != nil {
		return workspace.ChallengeSubmission{}, notFoundOrError(err, "submission", id)
	}
	return s, nil
}

func (r *postgresChallenges) Leaderboard(ctx context.Context, challengeID string, version, limit int, recent bool) ([]workspace.ChallengeSubmission, error) {
	if err := workspace.ValidateID(challengeID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	// `order` is a fixed literal selected here; no user input is concatenated.
	order := `score DESC, submitted_at ASC, id ASC`
	if recent {
		order = `submitted_at DESC, id ASC`
	}
	rows, err := r.db.Pool.Query(ctx, `
		SELECT `+submissionColumns+` FROM challenge_submissions
		WHERE challenge_id = $1::uuid AND challenge_version = $2 AND status = 'completed'
		ORDER BY `+order+` LIMIT $3`, challengeID, version, limit)
	if err != nil {
		return nil, fmt.Errorf("repositories: leaderboard: %w", err)
	}
	defer rows.Close()
	out := []workspace.ChallengeSubmission{}
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, fmt.Errorf("repositories: scan submission: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *postgresChallenges) SubmissionRank(ctx context.Context, s workspace.ChallengeSubmission) (int64, error) {
	if err := workspace.ValidateID(s.ChallengeID); err != nil {
		return 0, err
	}
	// Rank = number of completed submissions ordered strictly ahead of this
	// one, + 1. The ordering matches Leaderboard exactly.
	var ahead int64
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM challenge_submissions
		WHERE challenge_id = $1::uuid AND challenge_version = $2 AND status = 'completed'
		  AND (score > $3
		       OR (score = $3 AND submitted_at < $4)
		       OR (score = $3 AND submitted_at = $4 AND id::text < $5))`,
		s.ChallengeID, s.ChallengeVersion, s.Score, s.SubmittedAt, s.ID).Scan(&ahead)
	if err != nil {
		return 0, fmt.Errorf("repositories: submission rank: %w", err)
	}
	return ahead + 1, nil
}

func (r *postgresChallenges) CountRecentSubmissions(ctx context.Context, challengeID, displayName string, since time.Time) (int, error) {
	if err := workspace.ValidateID(challengeID); err != nil {
		return 0, err
	}
	var n int
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM challenge_submissions
		WHERE challenge_id = $1::uuid AND display_name = $2 AND submitted_at >= $3`,
		challengeID, displayName, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repositories: count recent submissions: %w", err)
	}
	return n, nil
}
