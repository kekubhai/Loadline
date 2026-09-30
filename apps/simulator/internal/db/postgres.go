// Package db owns LOADLINE's PostgreSQL persistence plumbing: connection
// pooling, health checks, and schema migrations.
//
// Dependency direction (enforced by imports): this package imports nothing
// from the simulator. The simulation engine (engine, sim, workload) never
// imports this package — a simulation stays a pure in-memory
// (architecture + workload + seed) → result computation with no database,
// HTTP, or cloud-provider dependency. PostgreSQL stores what the engine
// already produced; it never participates in producing it.
//
// PostgreSQL is the source of truth for LOADLINE workspace documents:
// projects, architectures, architecture versions, workloads, simulation
// runs, and their result summaries.
package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// EnvDatabaseURL is the only place connection details come from. Credentials
// are never hardcoded, logged, or returned to clients.
const EnvDatabaseURL = "DATABASE_URL"

// EnvTestDatabaseURL lets the persistence tests point at a disposable
// database (a Neon branch) instead of the one the application uses.
const EnvTestDatabaseURL = "LOADLINE_TEST_DATABASE_URL"

// Pool sizing. LOADLINE's API is a small number of long-lived requests
// against a managed PostgreSQL (Neon), so a modest pool is right:
// enough concurrency to overlap queries, few enough connections that a
// serverless Postgres proxy is not overwhelmed.
const (
	defaultMaxConns          = 8
	defaultMinConns          = 0
	defaultMaxConnLifetime   = 30 * time.Minute
	defaultMaxConnIdleTime   = 5 * time.Minute
	defaultHealthCheckPeriod = time.Minute
)

// Database is the handle the API layer holds: one pooled connection set
// per process, shared by every repository.
type Database struct {
	// Pool is exposed so repositories can run queries directly. Prefer
	// WithTx for multi-write workflows.
	Pool *pgxpool.Pool
}

// Open parses the connection string, creates the pool, and verifies the
// database is reachable before returning. A failure here is a startup
// failure with a clear reason — never a surprise on the first request.
func Open(ctx context.Context, databaseURL string) (*Database, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("db: %s is empty; set your connection string in .env or the environment", EnvDatabaseURL)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// The parse error can embed the connection string (and therefore
		// credentials), so it is deliberately not wrapped into the message
		// that callers log.
		return nil, fmt.Errorf("db: %s is not a valid PostgreSQL connection string", EnvDatabaseURL)
	}
	cfg.MaxConns = defaultMaxConns
	cfg.MinConns = defaultMinConns
	cfg.MaxConnLifetime = defaultMaxConnLifetime
	cfg.MaxConnIdleTime = defaultMaxConnIdleTime
	cfg.HealthCheckPeriod = defaultHealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping PostgreSQL: %w", err)
	}
	return &Database{Pool: pool}, nil
}

// OpenFromEnv loads .env (local development only; real environment
// variables always win), then opens the database named by DATABASE_URL.
func OpenFromEnv(ctx context.Context) (*Database, error) {
	LoadDotEnv()
	return Open(ctx, os.Getenv(EnvDatabaseURL))
}

// dotEnvMaxDepth bounds the upward search for a .env file.
const dotEnvMaxDepth = 8

// repoMarkers identify the root of this repository: the upward search for
// .env stops here so an unrelated .env in a parent directory is never
// picked up.
var repoMarkers = []string{".git", "go.work", "pnpm-workspace.yaml"}

// LoadDotEnv loads the nearest .env file, starting in the working directory
// and walking up towards the repository root.
//
// The search matters because the same file has to be found from very
// different working directories: the server is normally started from the
// repository root, while `go test` runs each package from its own directory
// several levels below it. Without the walk, a configured DATABASE_URL would
// be invisible to the persistence tests.
//
// A missing file is not an error: deployments set real environment
// variables. godotenv does not overwrite variables that are already set, so
// an explicit environment always wins over the file.
func LoadDotEnv() {
	path, ok := findDotEnv()
	if !ok {
		return
	}
	if err := godotenv.Load(path); err != nil {
		// Malformed file: report it, but keep going — the environment may
		// still supply DATABASE_URL.
		fmt.Fprintf(os.Stderr, "db: could not load %s: %v\n", path, err)
	}
}

// findDotEnv returns the nearest .env file at or above the working
// directory, stopping at the repository root.
func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for i := 0; i < dotEnvMaxDepth; i++ {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		if isRepoRoot(dir) {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir { // filesystem root
			return "", false
		}
		dir = parent
	}
	return "", false
}

// isRepoRoot reports whether dir contains a repository marker.
func isRepoRoot(dir string) bool {
	for _, marker := range repoMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// Ping reports whether PostgreSQL is reachable. It is used by the health
// endpoint and by tests.
func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("db: not connected")
	}
	return d.Pool.Ping(ctx)
}

// Close releases every pooled connection. Call it during graceful shutdown.
func (d *Database) Close() {
	if d == nil || d.Pool == nil {
		return
	}
	d.Pool.Close()
}

// WithTx runs fn inside a transaction: commit on success, rollback on any
// error or panic. Use it for atomic multi-write workflows (create an
// architecture together with its first version; persist a simulation run
// together with its result). Single statements do not need it — every
// statement is already atomic on its own.
func (d *Database) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin transaction: %w", err)
	}
	defer func() {
		// A rollback after a successful commit returns ErrTxClosed and is
		// a no-op; ignoring it keeps the success path clean. The rollback
		// uses a detached context so a cancelled request context cannot
		// leave the transaction open.
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit transaction: %w", err)
	}
	return nil
}
