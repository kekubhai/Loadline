package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// ProjectRepository is the persistence contract for projects (workspaces).
type ProjectRepository interface {
	// Create inserts the project, assigning an id when it is empty and
	// returning the stored row (with server timestamps).
	Create(ctx context.Context, p workspace.Project) (workspace.Project, error)
	Get(ctx context.Context, id string) (workspace.Project, error)
	// List returns every project, newest first.
	List(ctx context.Context) ([]workspace.Project, error)
	// Update changes name/description and bumps updated_at.
	Update(ctx context.Context, p workspace.Project) (workspace.Project, error)
	// Delete removes the project and, by foreign key, the architectures
	// (and their versions, workloads, runs, and results) it contains.
	Delete(ctx context.Context, id string) error
}

type postgresProjects struct {
	db *db.Database
}

// NewProjectRepository returns the PostgreSQL-backed project repository.
func NewProjectRepository(database *db.Database) ProjectRepository {
	return &postgresProjects{db: database}
}

const projectColumns = `id::text, name, description, created_at, updated_at`

func (r *postgresProjects) Create(ctx context.Context, p workspace.Project) (workspace.Project, error) {
	if err := p.Validate(); err != nil {
		return workspace.Project{}, err
	}
	if p.ID == "" {
		p.ID = workspace.NewID()
	}
	row := r.db.Pool.QueryRow(ctx, `
		INSERT INTO projects (id, name, description)
		VALUES ($1::uuid, $2, $3)
		RETURNING created_at, updated_at`,
		p.ID, p.Name, p.Description)
	if err := row.Scan(&p.CreatedAt, &p.UpdatedAt); err != nil {
		return workspace.Project{}, fmt.Errorf("repositories: create project: %w", err)
	}
	return p, nil
}

func (r *postgresProjects) Get(ctx context.Context, id string) (workspace.Project, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.Project{}, err
	}
	var p workspace.Project
	err := r.db.Pool.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1::uuid`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return workspace.Project{}, notFoundOrError(err, "project", id)
	}
	return p, nil
}

func (r *postgresProjects) List(ctx context.Context) ([]workspace.Project, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+projectColumns+` FROM projects ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("repositories: list projects: %w", err)
	}
	defer rows.Close()
	out := []workspace.Project{}
	for rows.Next() {
		var p workspace.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repositories: scan project: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repositories: list projects: %w", err)
	}
	return out, nil
}

func (r *postgresProjects) Update(ctx context.Context, p workspace.Project) (workspace.Project, error) {
	if err := p.Validate(); err != nil {
		return workspace.Project{}, err
	}
	if err := workspace.ValidateID(p.ID); err != nil {
		return workspace.Project{}, err
	}
	// updated_at is set explicitly rather than by a trigger: the write is
	// the only place that can know the document changed, and keeping the
	// behavior in one visible statement beats a hidden side effect.
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE projects
		SET name = $2, description = $3, updated_at = now()
		WHERE id = $1::uuid
		RETURNING created_at, updated_at`,
		p.ID, p.Name, p.Description).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return workspace.Project{}, notFoundOrError(err, "project", p.ID)
	}
	return p, nil
}

func (r *postgresProjects) Delete(ctx context.Context, id string) error {
	if err := workspace.ValidateID(id); err != nil {
		return err
	}
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM projects WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("repositories: delete project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: project %s", workspace.ErrNotFound, id)
	}
	return nil
}

// notFoundOrError converts pgx's no-rows error into the package's sentinel
// and leaves every other error wrapped for the caller to log safely.
func notFoundOrError(err error, kind, id string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", workspace.ErrNotFound, kind, id)
	}
	return fmt.Errorf("repositories: get %s: %w", kind, err)
}
