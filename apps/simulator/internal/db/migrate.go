package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationFS embeds the SQL migrations so the binary always ships with a
// reproducible schema — no manual SQL against Neon, no files to copy.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationsDir is the directory inside migrationFS.
const migrationsDir = "migrations"

// migrationLockKey is an arbitrary but stable advisory-lock key. Every
// process that migrates takes it, so two servers starting at the same time
// cannot interleave DDL.
const migrationLockKey int64 = 8_267_411_902_113_001

// Migration is one embedded migration file.
type Migration struct {
	// Version is the numeric prefix of the file name (001 → 1).
	Version int
	// Name is the file name, e.g. "001_initial_schema.sql".
	Name string
	// SQL is the file body.
	SQL string
}

// Migrate applies every pending migration, in version order, and records
// each one in schema_migrations.
//
// The whole run happens in ONE transaction: PostgreSQL (and Neon) support
// transactional DDL, so either every pending migration lands or none does.
// A future migration needing a non-transactional statement (e.g. CREATE
// INDEX CONCURRENTLY) would have to be split out; none exists today.
//
// Re-running is safe: applied versions are skipped and each file is written
// idempotently.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := Migrations()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Serialize concurrent migrators. The lock is transaction-scoped, so it
	// is released by the commit or rollback below even if this process dies.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("db: acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    integer PRIMARY KEY,
			name       text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("db: apply migration %s: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
			m.Version, m.Name); err != nil {
			return fmt.Errorf("db: record migration %s: %w", m.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit migrations: %w", err)
	}
	return nil
}

// appliedVersions reads the set of already-applied migration versions.
func appliedVersions(ctx context.Context, tx pgx.Tx) (map[int]bool, error) {
	rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		out[v] = true
	}
	return out, rows.Err()
}

// Migrations returns the embedded migrations in ascending version order.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("db: read embedded migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := migrationFS.ReadFile(path.Join(migrationsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("db: read migration %s: %w", e.Name(), err)
		}
		version, err := migrationVersion(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: version, Name: e.Name(), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("db: duplicate migration version %d (%s and %s)",
				out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

// migrationVersion parses the numeric prefix of a migration file name.
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("db: migration %q must be named <version>_<name>.sql", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("db: migration %q has a non-numeric version prefix", name)
	}
	return v, nil
}
