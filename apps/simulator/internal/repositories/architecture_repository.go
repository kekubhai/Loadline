package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// ArchitectureRepository is the persistence contract for architectures and
// their immutable versions.
//
// Version numbers are assigned by the repository (1, 2, 3, …) inside the
// same transaction that inserts the row, so concurrent writers cannot
// produce two "version 2" rows for one architecture.
type ArchitectureRepository interface {
	Create(ctx context.Context, a workspace.Architecture) (workspace.Architecture, error)
	Get(ctx context.Context, id string) (workspace.Architecture, error)
	ListByProject(ctx context.Context, projectID string) ([]workspace.Architecture, error)
	Update(ctx context.Context, a workspace.Architecture) (workspace.Architecture, error)
	Delete(ctx context.Context, id string) error

	// CreateVersion appends a new immutable version and returns it with its
	// assigned version number.
	CreateVersion(ctx context.Context, architectureID string, definition json.RawMessage) (workspace.ArchitectureVersion, error)
	GetVersion(ctx context.Context, id string) (workspace.ArchitectureVersion, error)
	// ListVersions returns the versions of an architecture, oldest first.
	ListVersions(ctx context.Context, architectureID string) ([]workspace.ArchitectureVersion, error)

	// CreateWithVersion atomically creates an architecture and its first
	// version. Either both rows exist or neither does.
	CreateWithVersion(ctx context.Context, a workspace.Architecture, definition json.RawMessage) (workspace.Architecture, workspace.ArchitectureVersion, error)
}

type postgresArchitectures struct {
	db *db.Database
}

// NewArchitectureRepository returns the PostgreSQL-backed architecture
// repository.
func NewArchitectureRepository(database *db.Database) ArchitectureRepository {
	return &postgresArchitectures{db: database}
}

// architectureSelect lists architectures together with their version
// summary. The subquery aggregates versions once instead of counting per
// row; an architecture with no versions reports 0/0.
const architectureSelect = `
	SELECT a.id::text, a.project_id::text, a.name, a.description,
	       a.created_at, a.updated_at,
	       COALESCE(v.version_count, 0), COALESCE(v.latest_version, 0)
	FROM architectures a
	LEFT JOIN (
		SELECT architecture_id, COUNT(*) AS version_count, MAX(version) AS latest_version
		FROM architecture_versions
		GROUP BY architecture_id
	) v ON v.architecture_id = a.id`

func scanArchitecture(row interface{ Scan(...any) error }) (workspace.Architecture, error) {
	var a workspace.Architecture
	err := row.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Description,
		&a.CreatedAt, &a.UpdatedAt, &a.VersionCount, &a.LatestVersion)
	return a, err
}

func (r *postgresArchitectures) Create(ctx context.Context, a workspace.Architecture) (workspace.Architecture, error) {
	if err := a.Validate(); err != nil {
		return workspace.Architecture{}, err
	}
	if a.ID == "" {
		a.ID = workspace.NewID()
	}
	row := r.db.Pool.QueryRow(ctx, `
		INSERT INTO architectures (id, project_id, name, description)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		RETURNING created_at, updated_at`,
		a.ID, a.ProjectID, a.Name, a.Description)
	if err := row.Scan(&a.CreatedAt, &a.UpdatedAt); err != nil {
		return workspace.Architecture{}, wrapForeignKey(err, fmt.Errorf("repositories: create architecture: %w", err))
	}
	return a, nil
}

func (r *postgresArchitectures) Get(ctx context.Context, id string) (workspace.Architecture, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.Architecture{}, err
	}
	a, err := scanArchitecture(r.db.Pool.QueryRow(ctx,
		architectureSelect+` WHERE a.id = $1::uuid`, id))
	if err != nil {
		return workspace.Architecture{}, notFoundOrError(err, "architecture", id)
	}
	return a, nil
}

func (r *postgresArchitectures) ListByProject(ctx context.Context, projectID string) ([]workspace.Architecture, error) {
	if err := workspace.ValidateID(projectID); err != nil {
		return nil, err
	}
	rows, err := r.db.Pool.Query(ctx,
		architectureSelect+` WHERE a.project_id = $1::uuid ORDER BY a.created_at DESC, a.id DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repositories: list architectures: %w", err)
	}
	defer rows.Close()
	out := []workspace.Architecture{}
	for rows.Next() {
		a, err := scanArchitecture(rows)
		if err != nil {
			return nil, fmt.Errorf("repositories: scan architecture: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repositories: list architectures: %w", err)
	}
	return out, nil
}

func (r *postgresArchitectures) Update(ctx context.Context, a workspace.Architecture) (workspace.Architecture, error) {
	if err := a.Validate(); err != nil {
		return workspace.Architecture{}, err
	}
	if err := workspace.ValidateID(a.ID); err != nil {
		return workspace.Architecture{}, err
	}
	// Renaming never touches versions: the architecture's content is
	// immutable history, only its label changes.
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE architectures
		SET name = $2, description = $3, updated_at = now()
		WHERE id = $1::uuid
		RETURNING created_at, updated_at`,
		a.ID, a.Name, a.Description).Scan(&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return workspace.Architecture{}, notFoundOrError(err, "architecture", a.ID)
	}
	return a, nil
}

func (r *postgresArchitectures) Delete(ctx context.Context, id string) error {
	if err := workspace.ValidateID(id); err != nil {
		return err
	}
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM architectures WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("repositories: delete architecture: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: architecture %s", workspace.ErrNotFound, id)
	}
	return nil
}

func (r *postgresArchitectures) CreateVersion(ctx context.Context, architectureID string, definition json.RawMessage) (workspace.ArchitectureVersion, error) {
	if err := workspace.ValidateID(architectureID); err != nil {
		return workspace.ArchitectureVersion{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return workspace.ArchitectureVersion{}, err
	}
	var out workspace.ArchitectureVersion
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		v, err := insertVersion(ctx, tx, architectureID, definition)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		return workspace.ArchitectureVersion{}, err
	}
	return out, nil
}

func (r *postgresArchitectures) CreateWithVersion(ctx context.Context, a workspace.Architecture, definition json.RawMessage) (workspace.Architecture, workspace.ArchitectureVersion, error) {
	if err := a.Validate(); err != nil {
		return workspace.Architecture{}, workspace.ArchitectureVersion{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return workspace.Architecture{}, workspace.ArchitectureVersion{}, err
	}
	if a.ID == "" {
		a.ID = workspace.NewID()
	}
	var version workspace.ArchitectureVersion
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO architectures (id, project_id, name, description)
			VALUES ($1::uuid, $2::uuid, $3, $4)
			RETURNING created_at, updated_at`,
			a.ID, a.ProjectID, a.Name, a.Description)
		if err := row.Scan(&a.CreatedAt, &a.UpdatedAt); err != nil {
			return wrapForeignKey(err, fmt.Errorf("repositories: create architecture: %w", err))
		}
		v, err := insertVersion(ctx, tx, a.ID, definition)
		if err != nil {
			return err
		}
		version = v
		return nil
	})
	if err != nil {
		return workspace.Architecture{}, workspace.ArchitectureVersion{}, err
	}
	a.VersionCount = 1
	a.LatestVersion = version.Version
	return a, version, nil
}

// insertVersion assigns the next version number and inserts it. It must run
// inside a transaction: the row lock on the architecture serializes
// concurrent writers so the (architecture_id, version) unique constraint
// cannot be violated by a lost update.
func insertVersion(ctx context.Context, q Querier, architectureID string, definition json.RawMessage) (workspace.ArchitectureVersion, error) {
	var locked string
	if err := q.QueryRow(ctx,
		`SELECT id::text FROM architectures WHERE id = $1::uuid FOR UPDATE`,
		architectureID).Scan(&locked); err != nil {
		return workspace.ArchitectureVersion{}, notFoundOrError(err, "architecture", architectureID)
	}
	var next int
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM architecture_versions
		WHERE architecture_id = $1::uuid`, architectureID).Scan(&next); err != nil {
		return workspace.ArchitectureVersion{}, fmt.Errorf("repositories: next architecture version: %w", err)
	}
	v := workspace.ArchitectureVersion{
		ID:             workspace.NewID(),
		ArchitectureID: architectureID,
		Version:        next,
		Definition:     definition,
	}
	err := q.QueryRow(ctx, `
		INSERT INTO architecture_versions (id, architecture_id, version, definition)
		VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)
		RETURNING created_at`,
		v.ID, v.ArchitectureID, v.Version, string(definition)).Scan(&v.CreatedAt)
	if err != nil {
		return workspace.ArchitectureVersion{}, fmt.Errorf("repositories: create architecture version: %w", err)
	}
	return v, nil
}

func (r *postgresArchitectures) GetVersion(ctx context.Context, id string) (workspace.ArchitectureVersion, error) {
	if err := workspace.ValidateID(id); err != nil {
		return workspace.ArchitectureVersion{}, err
	}
	var v workspace.ArchitectureVersion
	var definition []byte
	err := r.db.Pool.QueryRow(ctx, `
		SELECT id::text, architecture_id::text, version, definition, created_at
		FROM architecture_versions
		WHERE id = $1::uuid`, id).
		Scan(&v.ID, &v.ArchitectureID, &v.Version, &definition, &v.CreatedAt)
	if err != nil {
		return workspace.ArchitectureVersion{}, notFoundOrError(err, "architecture version", id)
	}
	v.Definition = definition
	return v, nil
}

func (r *postgresArchitectures) ListVersions(ctx context.Context, architectureID string) ([]workspace.ArchitectureVersion, error) {
	if err := workspace.ValidateID(architectureID); err != nil {
		return nil, err
	}
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id::text, architecture_id::text, version, definition, created_at
		FROM architecture_versions
		WHERE architecture_id = $1::uuid
		ORDER BY version ASC`, architectureID)
	if err != nil {
		return nil, fmt.Errorf("repositories: list architecture versions: %w", err)
	}
	defer rows.Close()
	out := []workspace.ArchitectureVersion{}
	for rows.Next() {
		var v workspace.ArchitectureVersion
		var definition []byte
		if err := rows.Scan(&v.ID, &v.ArchitectureID, &v.Version, &definition, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("repositories: scan architecture version: %w", err)
		}
		v.Definition = definition
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repositories: list architecture versions: %w", err)
	}
	return out, nil
}

// validateDefinition rejects an empty definition before it reaches the
// NOT NULL jsonb column.
func validateDefinition(definition json.RawMessage) error {
	if len(definition) == 0 {
		return fmt.Errorf("workspace: architecture version: definition is required")
	}
	return nil
}
