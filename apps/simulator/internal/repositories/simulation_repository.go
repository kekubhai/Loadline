package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// SimulationRepository is the persistence contract for simulation runs and
// their result summaries.
type SimulationRepository interface {
	// CreateRun records a run (its inputs, seed, and lifecycle state).
	CreateRun(ctx context.Context, run workspace.SimulationRun) (workspace.SimulationRun, error)
	GetRun(ctx context.Context, id string) (workspace.SimulationRun, error)
	// ListRunsByVersion returns the runs of one architecture version,
	// newest first — the architecture's simulation history.
	ListRunsByVersion(ctx context.Context, architectureVersionID string) ([]workspace.SimulationRun, error)
	// ListRunsByProject returns every run under a project, newest first,
	// across all of its architectures and versions.
	ListRunsByProject(ctx context.Context, projectID string) ([]workspace.SimulationRun, error)
	// FinishRun marks a finished run (status, duration, error, timestamps).
	FinishRun(ctx context.Context, run workspace.SimulationRun) (workspace.SimulationRun, error)

	// SaveResult stores the result summary of a run.
	SaveResult(ctx context.Context, res workspace.SimulationResult) (workspace.SimulationResult, error)
	GetResultByRun(ctx context.Context, runID string) (workspace.SimulationResult, error)

	// PersistRunWithResult records the run and its result in ONE
	// transaction: a stored run with a half-written result is never
	// observable.
	PersistRunWithResult(ctx context.Context, run workspace.SimulationRun, res workspace.SimulationResult) (workspace.SimulationRun, workspace.SimulationResult, error)
}

type postgresSimulations struct {
	db *db.Database
}

// NewSimulationRepository returns the PostgreSQL-backed simulation
// repository.
func NewSimulationRepository(database *db.Database) SimulationRepository {
	return &postgresSimulations{db: database}
}

// workload_id is nullable (a deleted workload leaves the run in place), so
// it is coalesced to an empty string on read.
const runColumns = `id::text, architecture_version_id::text,
	COALESCE(workload_id::text, '') AS workload_id,
	status, seed, duration_ms, error, created_at, started_at, finished_at`

const resultColumns = `id::text, simulation_run_id::text, summary, plan, metrics,
	capacity, bottlenecks, failures, cost, created_at`

func scanRun(row interface{ Scan(...any) error }) (workspace.SimulationRun, error) {
	var r workspace.SimulationRun
	var status string
	var seed int64
	if err := row.Scan(&r.ID, &r.ArchitectureVersionID, &r.WorkloadID, &status,
		&seed, &r.DurationMS, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt); err != nil {
		return workspace.SimulationRun{}, err
	}
	r.Status = workspace.RunStatus(status)
	r.Seed = uint64(seed)
	return r, nil
}

func scanResult(row interface{ Scan(...any) error }) (workspace.SimulationResult, error) {
	var res workspace.SimulationResult
	var summary, plan, metrics, capacity, bottlenecks, failures, cost []byte
	if err := row.Scan(&res.ID, &res.SimulationRunID, &summary, &plan, &metrics,
		&capacity, &bottlenecks, &failures, &cost, &res.CreatedAt); err != nil {
		return workspace.SimulationResult{}, err
	}
	res.Summary, res.Plan, res.Metrics = summary, plan, metrics
	res.Capacity, res.Bottlenecks, res.Failures, res.Cost = capacity, bottlenecks, failures, cost
	return res, nil
}

func (r *postgresSimulations) CreateRun(ctx context.Context, run workspace.SimulationRun) (workspace.SimulationRun, error) {
	if err := run.Validate(); err != nil {
		return workspace.SimulationRun{}, err
	}
	if run.ID == "" {
		run.ID = workspace.NewID()
	}
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO simulation_runs
			(id, architecture_version_id, workload_id, status, seed, duration_ms, error, started_at, finished_at)
		VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9)
		RETURNING created_at`,
		run.ID, run.ArchitectureVersionID, run.WorkloadID, string(run.Status),
		int64(run.Seed), run.DurationMS, run.Error, run.StartedAt, run.FinishedAt).
		Scan(&run.CreatedAt)
	if err != nil {
		return workspace.SimulationRun{}, wrapForeignKey(err, fmt.Errorf("repositories: create simulation run: %w", err))
	}
	return run, nil
}

func (r *postgresSimulations) GetRun(ctx context.Context, id string) (workspace.SimulationRun, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.SimulationRun{}, err
	}
	run, err := scanRun(r.db.Pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM simulation_runs WHERE id = $1::uuid`, id))
	if err != nil {
		return workspace.SimulationRun{}, notFoundOrError(err, "simulation run", id)
	}
	return run, nil
}

func (r *postgresSimulations) ListRunsByVersion(ctx context.Context, architectureVersionID string) ([]workspace.SimulationRun, error) {
	if err := workspace.ValidateID(architectureVersionID); err != nil {
		return nil, err
	}
	return r.listRuns(ctx,
		`WHERE architecture_version_id = $1::uuid`, architectureVersionID)
}

func (r *postgresSimulations) ListRunsByProject(ctx context.Context, projectID string) ([]workspace.SimulationRun, error) {
	if err := workspace.ValidateID(projectID); err != nil {
		return nil, err
	}
	return r.listRuns(ctx, `
		WHERE architecture_version_id IN (
			SELECT v.id FROM architecture_versions v
			JOIN architectures a ON a.id = v.architecture_id
			WHERE a.project_id = $1::uuid
		)`, projectID)
}

func (r *postgresSimulations) listRuns(ctx context.Context, where string, arg string) ([]workspace.SimulationRun, error) {
	// `where` is a fixed literal chosen by the caller above; the caller's
	// value is always bound as $1. No user input is ever concatenated.
	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+runColumns+` FROM simulation_runs `+where+`
		 ORDER BY created_at DESC, id DESC`, arg)
	if err != nil {
		return nil, fmt.Errorf("repositories: list simulation runs: %w", err)
	}
	defer rows.Close()
	out := []workspace.SimulationRun{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("repositories: scan simulation run: %w", err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repositories: list simulation runs: %w", err)
	}
	return out, nil
}

func (r *postgresSimulations) FinishRun(ctx context.Context, run workspace.SimulationRun) (workspace.SimulationRun, error) {
	if err := run.Validate(); err != nil {
		return workspace.SimulationRun{}, err
	}
	if err := workspace.ValidateID(run.ID); err != nil {
		return workspace.SimulationRun{}, err
	}
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE simulation_runs
		SET status = $2, duration_ms = $3, error = $4, finished_at = now()
		WHERE id = $1::uuid
		RETURNING created_at, started_at, finished_at`,
		run.ID, string(run.Status), run.DurationMS, run.Error).
		Scan(&run.CreatedAt, &run.StartedAt, &run.FinishedAt)
	if err != nil {
		return workspace.SimulationRun{}, notFoundOrError(err, "simulation run", run.ID)
	}
	return run, nil
}

func (r *postgresSimulations) SaveResult(ctx context.Context, res workspace.SimulationResult) (workspace.SimulationResult, error) {
	if err := workspace.ValidateID(res.SimulationRunID); err != nil {
		return workspace.SimulationResult{}, err
	}
	if err := insertResult(ctx, r.db.Pool, &res); err != nil {
		return workspace.SimulationResult{}, err
	}
	return res, nil
}

func (r *postgresSimulations) GetResultByRun(ctx context.Context, runID string) (workspace.SimulationResult, error) {
	if err := workspace.ValidateID(runID); err != nil {
		return workspace.SimulationResult{}, err
	}
	res, err := scanResult(r.db.Pool.QueryRow(ctx,
		`SELECT `+resultColumns+` FROM simulation_results WHERE simulation_run_id = $1::uuid`, runID))
	if err != nil {
		return workspace.SimulationResult{}, notFoundOrError(err, "simulation result", runID)
	}
	return res, nil
}

func (r *postgresSimulations) PersistRunWithResult(ctx context.Context, run workspace.SimulationRun, res workspace.SimulationResult) (workspace.SimulationRun, workspace.SimulationResult, error) {
	if err := run.Validate(); err != nil {
		return workspace.SimulationRun{}, workspace.SimulationResult{}, err
	}
	var outRun workspace.SimulationRun
	var outRes workspace.SimulationResult
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if run.ID == "" {
			run.ID = workspace.NewID()
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO simulation_runs
				(id, architecture_version_id, workload_id, status, seed, duration_ms, error, started_at, finished_at)
			VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9)
			RETURNING created_at`,
			run.ID, run.ArchitectureVersionID, run.WorkloadID, string(run.Status),
			int64(run.Seed), run.DurationMS, run.Error, run.StartedAt, run.FinishedAt).
			Scan(&run.CreatedAt); err != nil {
			return wrapForeignKey(err, fmt.Errorf("repositories: create simulation run: %w", err))
		}
		res.SimulationRunID = run.ID
		if err := insertResult(ctx, tx, &res); err != nil {
			return err
		}
		outRun, outRes = run, res
		return nil
	})
	if err != nil {
		return workspace.SimulationRun{}, workspace.SimulationResult{}, err
	}
	return outRun, outRes, nil
}

// insertResult writes the result row, assigning an id when the caller did
// not supply one. Structured payloads default to the empty JSON value for
// their shape so a failed run stores no invented metrics.
func insertResult(ctx context.Context, q Querier, res *workspace.SimulationResult) error {
	if res.ID == "" {
		res.ID = workspace.NewID()
	}
	err := q.QueryRow(ctx, `
		INSERT INTO simulation_results
			(id, simulation_run_id, summary, plan, metrics, capacity, bottlenecks, failures, cost)
		VALUES ($1::uuid, $2::uuid, $3::jsonb, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb)
		RETURNING created_at`,
		res.ID, res.SimulationRunID,
		jsonOrEmpty(res.Summary, "{}"),
		jsonOrEmpty(res.Plan, "{}"),
		jsonOrEmpty(res.Metrics, "{}"),
		jsonOrEmpty(res.Capacity, "[]"),
		jsonOrEmpty(res.Bottlenecks, "{}"),
		jsonOrEmpty(res.Failures, "[]"),
		jsonOrEmpty(res.Cost, "{}")).
		Scan(&res.CreatedAt)
	if err != nil {
		return wrapForeignKey(err, fmt.Errorf("repositories: create simulation result: %w", err))
	}
	return nil
}
