package db

import (
	"strings"
	"testing"
)

// TestMigrationsAreOrderedUniqueAndNonEmpty locks the invariants the runner
// relies on: at least one migration, strictly ascending versions, and every
// file carrying SQL.
func TestMigrationsAreOrderedUniqueAndNonEmpty(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if migrations[0].Name != "001_initial_schema.sql" {
		t.Fatalf("expected the initial schema first, got %q", migrations[0].Name)
	}
	for i, m := range migrations {
		if m.Version != i+1 {
			t.Fatalf("migration %d has version %d; versions must be consecutive from 1", i, m.Version)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("migration %s is empty", m.Name)
		}
	}
}

// TestInitialMigrationCreatesWorkspaceSchema checks the schema the milestone
// requires exists, and that it is written idempotently. This is a structural
// check on the SQL: the behavioral check (does it actually apply to
// PostgreSQL?) lives in the database-backed tests.
func TestInitialMigrationCreatesWorkspaceSchema(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	// Whitespace is collapsed so the checks below do not depend on the
	// column-alignment padding used for readability in the .sql file.
	initial := collapseSpaces(migrations[0].SQL)

	// Every table the workspace workflow needs. (The migration ledger itself,
	// schema_migrations, is created by the runner, not by this file.)
	for _, table := range []string{
		"projects",
		"architectures",
		"architecture_versions",
		"workloads",
		"simulation_runs",
		"simulation_results",
	} {
		if !strings.Contains(initial, "CREATE TABLE IF NOT EXISTS "+table+" ") {
			t.Errorf("initial migration does not create table %q idempotently", table)
		}
	}

	// Relational columns for identity/relationships/status/version/seed, and
	// JSONB for the structures the simulation model is still evolving.
	for _, fragment := range []string{
		"definition jsonb NOT NULL",
		"configuration jsonb NOT NULL",
		"seed bigint NOT NULL",
		"status text NOT NULL",
		"uuid PRIMARY KEY",
		"REFERENCES projects (id)",
		"REFERENCES architectures (id)",
		"REFERENCES architecture_versions (id)",
		"REFERENCES workloads (id) ON DELETE SET NULL",
		"simulation_run_id uuid NOT NULL UNIQUE",
	} {
		if !strings.Contains(initial, fragment) {
			t.Errorf("initial migration is missing %q", fragment)
		}
	}

	// Indexes for every foreign-key lookup the API performs.
	for _, index := range []string{
		"architectures_project_id_idx",
		"architecture_versions_architecture_version_key",
		"workloads_architecture_version_id_idx",
		"simulation_runs_architecture_version_id_idx",
		"simulation_runs_workload_id_idx",
		"simulation_results_simulation_run_id_idx",
	} {
		if !strings.Contains(initial, index) {
			t.Errorf("initial migration is missing index/constraint %q", index)
		}
	}

	// Deleting a workload must NOT delete the runs that used it: run history
	// outlives the load definition it was measured under.
	if !strings.Contains(initial, `workload_id uuid REFERENCES workloads (id) ON DELETE SET NULL`) {
		t.Error("simulation_runs.workload_id must be ON DELETE SET NULL so run history survives workload deletion")
	}
}

// collapseSpaces replaces every run of whitespace with a single space, so
// structural checks on the SQL do not depend on column-alignment padding.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestMigrationVersionParsing covers the naming contract and its errors.
func TestMigrationVersionParsing(t *testing.T) {
	ok, err := migrationVersion("007_add_indexes.sql")
	if err != nil || ok != 7 {
		t.Fatalf("migrationVersion(007_…): got (%d, %v)", ok, err)
	}
	for _, bad := range []string{"initial.sql", "abc_migration.sql", "0_zero.sql"} {
		if _, err := migrationVersion(bad); err == nil {
			t.Errorf("migrationVersion(%q): expected an error", bad)
		}
	}
}
