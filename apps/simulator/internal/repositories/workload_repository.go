package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// WorkloadRepository is the persistence contract for workload definitions.
//
// A workload belongs to exactly one architecture version. Editing a
// workload changes the load a *new* run is given; existing runs keep the
// results they measured, so they are unaffected.
type WorkloadRepository interface {
	Create(ctx context.Context, w workspace.Workload) (workspace.Workload, error)
	Get(ctx context.Context, id string) (workspace.Workload, error)
	// ListByVersion returns the workloads attached to an architecture
	// version, newest first.
	ListByVersion(ctx context.Context, architectureVersionID string) ([]workspace.Workload, error)
	Update(ctx context.Context, w workspace.Workload) (workspace.Workload, error)
	Delete(ctx context.Context, id string) error
}

type postgresWorkloads struct {
	db *db.Database
}

// NewWorkloadRepository returns the PostgreSQL-backed workload repository.
func NewWorkloadRepository(database *db.Database) WorkloadRepository {
	return &postgresWorkloads{db: database}
}

const workloadColumns = `id::text, architecture_version_id::text, name,
	configuration, created_at, updated_at`

func scanWorkload(row interface{ Scan(...any) error }) (workspace.Workload, error) {
	var w workspace.Workload
	var configuration []byte
	if err := row.Scan(&w.ID, &w.ArchitectureVersionID, &w.Name,
		&configuration, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return workspace.Workload{}, err
	}
	w.Configuration = configuration
	return w, nil
}

func (r *postgresWorkloads) Create(ctx context.Context, w workspace.Workload) (workspace.Workload, error) {
	if err := w.Validate(); err != nil {
		return workspace.Workload{}, err
	}
	if w.ID == "" {
		w.ID = workspace.NewID()
	}
	// The JSON payload is passed as text and cast to jsonb: pgx binds a
	// []byte parameter as bytea, which jsonb would reject.
	row := r.db.Pool.QueryRow(ctx, `
		INSERT INTO workloads (id, architecture_version_id, name, configuration)
		VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)
		RETURNING created_at, updated_at`,
		w.ID, w.ArchitectureVersionID, w.Name, string(w.Configuration))
	if err := row.Scan(&w.CreatedAt, &w.UpdatedAt); err != nil {
		return workspace.Workload{}, wrapForeignKey(err, fmt.Errorf("repositories: create workload: %w", err))
	}
	return w, nil
}

func (r *postgresWorkloads) Get(ctx context.Context, id string) (workspace.Workload, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.Workload{}, err
	}
	w, err := scanWorkload(r.db.Pool.QueryRow(ctx,
		`SELECT `+workloadColumns+` FROM workloads WHERE id = $1::uuid`, id))
	if err != nil {
		return workspace.Workload{}, notFoundOrError(err, "workload", id)
	}
	return w, nil
}

func (r *postgresWorkloads) ListByVersion(ctx context.Context, architectureVersionID string) ([]workspace.Workload, error) {
	if err := workspace.ValidateID(architectureVersionID); err != nil {
		return nil, err
	}
	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+workloadColumns+` FROM workloads
		 WHERE architecture_version_id = $1::uuid
		 ORDER BY created_at DESC, id DESC`, architectureVersionID)
	if err != nil {
		return nil, fmt.Errorf("repositories: list workloads: %w", err)
	}
	defer rows.Close()
	out := []workspace.Workload{}
	for rows.Next() {
		w, err := scanWorkload(rows)
		if err != nil {
			return nil, fmt.Errorf("repositories: scan workload: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repositories: list workloads: %w", err)
	}
	return out, nil
}

func (r *postgresWorkloads) Update(ctx context.Context, w workspace.Workload) (workspace.Workload, error) {
	if err := w.Validate(); err != nil {
		return workspace.Workload{}, err
	}
	if err := workspace.ValidateID(w.ID); err != nil {
		return workspace.Workload{}, err
	}
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE workloads
		SET name = $2, configuration = $3::jsonb, updated_at = now()
		WHERE id = $1::uuid
		RETURNING architecture_version_id::text, created_at, updated_at`,
		w.ID, w.Name, string(w.Configuration)).
		Scan(&w.ArchitectureVersionID, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return workspace.Workload{}, notFoundOrError(err, "workload", w.ID)
	}
	return w, nil
}

func (r *postgresWorkloads) Delete(ctx context.Context, id string) error {
	if err := workspace.ValidateID(id); err != nil {
		return err
	}
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM workloads WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("repositories: delete workload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: workload %s", workspace.ErrNotFound, id)
	}
	return nil
}

// jsonOrEmpty returns raw when set, else the given empty JSON value for the
// column's shape. Failed runs carry no metrics; storing "{}"/"[]" keeps the
// column meaningful without inventing numbers.
func jsonOrEmpty(raw json.RawMessage, empty string) string {
	if len(raw) == 0 {
		return empty
	}
	return string(raw)
}
