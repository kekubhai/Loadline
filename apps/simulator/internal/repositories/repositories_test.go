package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
)

// These tests exercise real SQL against a real PostgreSQL database. They
// never run against production data by accident:
//
//   - LOADLINE_TEST_DATABASE_URL wins; DATABASE_URL is only a fallback;
//   - with neither set, every test SKIPS, so `go test ./...` stays green on
//     a machine with no database;
//   - each test creates its own project and deletes only that project, so
//     running against a shared database cannot destroy anything else.
func testDatabase(t *testing.T) *db.Database {
	t.Helper()
	// `go test` runs each package from its own directory, so the repository's
	// .env has to be located explicitly before the variables are read.
	db.LoadDotEnv()
	url := os.Getenv(db.EnvTestDatabaseURL)
	if url == "" {
		url = os.Getenv(db.EnvDatabaseURL)
	}
	if url == "" {
		t.Skipf("set %s (or %s) to run persistence tests", db.EnvTestDatabaseURL, db.EnvDatabaseURL)
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
	return database
}

// fixture is a project with one architecture, one version, and one workload,
// removed when the test ends.
type fixture struct {
	db            *db.Database
	projects      ProjectRepository
	architectures ArchitectureRepository
	workloads     WorkloadRepository
	simulations   SimulationRepository

	project      workspace.Project
	architecture workspace.Architecture
	version      workspace.ArchitectureVersion
	workload     workspace.Workload
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	database := testDatabase(t)
	ctx := context.Background()

	f := &fixture{
		db:            database,
		projects:      NewProjectRepository(database),
		architectures: NewArchitectureRepository(database),
		workloads:     NewWorkloadRepository(database),
		simulations:   NewSimulationRepository(database),
	}

	project, err := f.projects.Create(ctx, workspace.Project{Name: "test-project", Description: "persistence tests"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Deleting the project cascades to everything this fixture created, so
	// cleanup is one statement and cannot touch another test's rows.
	t.Cleanup(func() {
		if err := f.projects.Delete(context.Background(), project.ID); err != nil && !workspace.IsNotFound(err) {
			t.Logf("cleanup: delete project %s: %v", project.ID, err)
		}
	})
	f.project = project

	arch, version, err := f.architectures.CreateWithVersion(ctx, workspace.Architecture{
		ProjectID: project.ID,
		Name:      "web",
	}, json.RawMessage(`{"schemaVersion":"1","name":"web","components":[],"links":[]}`))
	if err != nil {
		t.Fatalf("create architecture with version: %v", err)
	}
	f.architecture, f.version = arch, version

	w, err := f.workloads.Create(ctx, workspace.Workload{
		ArchitectureVersionID: version.ID,
		Name:                  "peak",
		Configuration:         json.RawMessage(`{"totalUsers":1000,"dau":500}`),
	})
	if err != nil {
		t.Fatalf("create workload: %v", err)
	}
	f.workload = w
	return f
}

// TestMigrationIsIdempotent runs the migration twice: the ledger and the
// idempotent DDL must make the second run a no-op.
func TestMigrationIsIdempotent(t *testing.T) {
	database := testDatabase(t)
	if err := db.Migrate(context.Background(), database.Pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestProjectCRUD(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	repo := NewProjectRepository(database)

	created, err := repo.Create(ctx, workspace.Project{Name: "crud-project", Description: "one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), created.ID) })
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatalf("create returned an incomplete row: %+v", created)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "crud-project" || got.Description != "one" {
		t.Fatalf("get returned %+v", got)
	}

	got.Name = "renamed"
	got.Description = "two"
	updated, err := repo.Update(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "renamed" || updated.Description != "two" {
		t.Fatalf("update returned %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Fatalf("updated_at %v is before created_at %v", updated.UpdatedAt, updated.CreatedAt)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsProject(list, created.ID) {
		t.Fatalf("list did not include %s", created.ID)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); !workspace.IsNotFound(err) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
	if err := repo.Delete(ctx, created.ID); !workspace.IsNotFound(err) {
		t.Fatalf("expected not-found on second delete, got %v", err)
	}
}

func containsProject(list []workspace.Project, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}

// TestProjectValidationAndBadIDs checks that malformed input is rejected in
// Go, before it reaches a SQL cast.
func TestProjectValidationAndBadIDs(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	repo := NewProjectRepository(database)

	if _, err := repo.Create(ctx, workspace.Project{Name: "  "}); err == nil {
		t.Error("expected a blank name to be rejected")
	}
	if _, err := repo.Get(ctx, "not-a-uuid"); !workspace.IsInvalidID(err) {
		t.Errorf("expected an invalid-id error, got %v", err)
	}
	if _, err := repo.Get(ctx, "f47ac10b-58cc-4372-a567-0e02b2c3d479"); !workspace.IsNotFound(err) {
		t.Errorf("expected not-found for an unknown id, got %v", err)
	}
	// A SQL-injection attempt is just an invalid id: it never reaches a query.
	if _, err := repo.Get(ctx, "'; DROP TABLE projects; --"); !workspace.IsInvalidID(err) {
		t.Errorf("expected an invalid-id error for injection-shaped input, got %v", err)
	}
}

// TestArchitectureVersioning proves v1/v2/v3 coexist with server-assigned
// numbers, and that the architecture's version summary follows.
func TestArchitectureVersioning(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.version.Version != 1 {
		t.Fatalf("first version = %d, want 1", f.version.Version)
	}
	for want := 2; want <= 3; want++ {
		v, err := f.architectures.CreateVersion(ctx, f.architecture.ID, json.RawMessage(`{"schemaVersion":"1","name":"web"}`))
		if err != nil {
			t.Fatalf("create version %d: %v", want, err)
		}
		if v.Version != want {
			t.Fatalf("assigned version %d, want %d", v.Version, want)
		}
	}

	versions, err := f.architectures.ListVersions(ctx, f.architecture.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}
	for i, v := range versions {
		if v.Version != i+1 {
			t.Fatalf("versions are not ordered oldest-first: index %d has version %d", i, v.Version)
		}
	}

	got, err := f.architectures.Get(ctx, f.architecture.ID)
	if err != nil {
		t.Fatalf("get architecture: %v", err)
	}
	if got.VersionCount != 3 || got.LatestVersion != 3 {
		t.Fatalf("version summary = %d/%d, want 3/3", got.VersionCount, got.LatestVersion)
	}

	// Definition round-trips verbatim.
	v1, err := f.architectures.GetVersion(ctx, f.version.ID)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(v1.Definition, &decoded); err != nil {
		t.Fatalf("definition is not valid JSON: %v", err)
	}
	if decoded["name"] != "web" {
		t.Fatalf("definition round-trip lost data: %v", decoded)
	}

	// Renaming an architecture must not disturb its immutable history.
	renamed, err := f.architectures.Update(ctx, workspace.Architecture{
		ID: got.ID, ProjectID: got.ProjectID, Name: "renamed",
	})
	if err != nil {
		t.Fatalf("update architecture: %v", err)
	}
	if renamed.Name != "renamed" {
		t.Fatalf("update returned %+v", renamed)
	}
	versionsAfter, err := f.architectures.ListVersions(ctx, f.architecture.ID)
	if err != nil {
		t.Fatalf("list versions after rename: %v", err)
	}
	if len(versionsAfter) != 3 {
		t.Fatalf("rename changed the version count: %d", len(versionsAfter))
	}
}

// TestCreateVersionUnknownArchitectureIsNotFound checks the FK/lookup path.
func TestCreateVersionUnknownArchitectureIsNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.architectures.CreateVersion(context.Background(),
		"f47ac10b-58cc-4372-a567-0e02b2c3d479", json.RawMessage(`{"v":1}`))
	if !workspace.IsNotFound(err) {
		t.Fatalf("expected not-found, got %v", err)
	}
}

// TestCreateWithVersionRollsBackOnFailure is the atomicity test the workflow
// requires: the architecture insert succeeds inside the transaction, the
// version insert fails, and NOTHING is persisted.
func TestCreateWithVersionRollsBackOnFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A definition that is present but not valid JSON: validateDefinition
	// accepts it (it is non-empty) and PostgreSQL rejects the jsonb cast, so
	// the failure happens AFTER the architecture row was inserted.
	archID := workspace.NewID()
	_, _, err := f.architectures.CreateWithVersion(ctx, workspace.Architecture{
		ID:        archID,
		ProjectID: f.project.ID,
		Name:      "doomed",
	}, json.RawMessage(`this is not json`))
	if err == nil {
		t.Fatal("expected the invalid definition to fail the transaction")
	}

	if _, err := f.architectures.Get(ctx, archID); !workspace.IsNotFound(err) {
		t.Fatalf("partial write survived the rollback: architecture %s exists (%v)", archID, err)
	}
	// The project itself is untouched.
	if _, err := f.projects.Get(ctx, f.project.ID); err != nil {
		t.Fatalf("rolled-back transaction damaged the project: %v", err)
	}
}

// TestWithTxRollsBackOnError tests the transaction helper directly: a write
// followed by an error leaves no row behind.
func TestWithTxRollsBackOnError(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	repo := NewProjectRepository(database)

	id := workspace.NewID()
	sentinel := errors.New("intentional failure after the write")
	err := database.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO projects (id, name) VALUES ($1::uuid, $2)`, id, "rolled-back"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the sentinel error to surface, got %v", err)
	}
	if _, err := repo.Get(ctx, id); !workspace.IsNotFound(err) {
		t.Fatalf("partial write survived the rollback: project %s exists (%v)", id, err)
	}
}

// TestWorkloadCRUDAndRunHistorySurvivesDeletion covers workload CRUD and the
// ONDELETE SET NULL design: deleting a workload keeps the runs that used it.
func TestWorkloadCRUDAndRunHistorySurvivesDeletion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	got, err := f.workloads.Get(ctx, f.workload.ID)
	if err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if got.Name != "peak" || got.ArchitectureVersionID != f.version.ID {
		t.Fatalf("get returned %+v", got)
	}

	got.Name = "peak-v2"
	got.Configuration = json.RawMessage(`{"totalUsers":2000,"dau":1000}`)
	if _, err := f.workloads.Update(ctx, got); err != nil {
		t.Fatalf("update workload: %v", err)
	}

	list, err := f.workloads.ListByVersion(ctx, f.version.ID)
	if err != nil {
		t.Fatalf("list workloads: %v", err)
	}
	if len(list) != 1 || list[0].Name != "peak-v2" {
		t.Fatalf("list returned %+v", list)
	}

	run := newCompletedRun(t, f)
	if run.WorkloadID != f.workload.ID {
		t.Fatalf("run workload id = %q, want %q", run.WorkloadID, f.workload.ID)
	}

	// A workload attached to a version is only deletable once its runs stop
	// referencing it — and they must NOT be deleted along with it.
	if err := f.workloads.Delete(ctx, f.workload.ID); err != nil {
		t.Fatalf("delete workload: %v", err)
	}
	stillThere, err := f.simulations.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("run disappeared with its workload: %v", err)
	}
	if stillThere.WorkloadID != "" {
		t.Fatalf("expected the run's workload link to be cleared, got %q", stillThere.WorkloadID)
	}
}

// newCompletedRun persists a completed run with its result payload.
func newCompletedRun(t *testing.T, f *fixture) workspace.SimulationRun {
	t.Helper()
	run := workspace.SimulationRun{
		ID:                    workspace.NewID(),
		ArchitectureVersionID: f.version.ID,
		WorkloadID:            f.workload.ID,
		Status:                workspace.RunStatusCompleted,
		Seed:                  7,
		DurationMS:            5_000,
	}
	result := workspace.SimulationResult{
		Summary:     json.RawMessage(`{"eventsProcessed":1234,"stopReason":"horizon"}`),
		Plan:        json.RawMessage(`{"peakRps":1157.4}`),
		Metrics:     json.RawMessage(`{"generated":5800,"p99Ms":12.5}`),
		Capacity:    json.RawMessage(`[{"componentId":"db","maxSustainableRps":500}]`),
		Bottlenecks: json.RawMessage(`{"healthy":true,"summary":"nominal"}`),
		Failures:    json.RawMessage(`[]`),
		Cost:        json.RawMessage(`{"total":123.45,"currency":"USD"}`),
	}
	persisted, _, err := f.simulations.PersistRunWithResult(context.Background(), run, result)
	if err != nil {
		t.Fatalf("persist run with result: %v", err)
	}
	return persisted
}

// TestSimulationPersistenceRoundTrip stores a run + result and reads both
// back, checking the JSONB payloads survive verbatim.
func TestSimulationPersistenceRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	run := newCompletedRun(t, f)
	if run.CreatedAt.IsZero() {
		t.Fatal("persisted run has no created_at")
	}

	gotRun, err := f.simulations.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != workspace.RunStatusCompleted {
		t.Fatalf("status = %q, want completed", gotRun.Status)
	}
	// The seed is the whole point of the persistence: without it the run
	// could not be reproduced.
	if gotRun.Seed != 7 {
		t.Fatalf("seed = %d, want 7", gotRun.Seed)
	}
	if gotRun.DurationMS != 5_000 {
		t.Fatalf("duration = %v, want 5000", gotRun.DurationMS)
	}

	res, err := f.simulations.GetResultByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	assertJSONField(t, res.Metrics, "generated", float64(5800))
	assertJSONField(t, res.Plan, "peakRps", 1157.4)
	assertJSONField(t, res.Cost, "total", 123.45)
	assertJSONField(t, res.Summary, "stopReason", "horizon")
	assertJSONField(t, res.Bottlenecks, "summary", "nominal")
}

// TestPersistRunWithResultRollsBack proves the run and its result are atomic:
// a failing result insert leaves no run row.
func TestPersistRunWithResultRollsBack(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	runID := workspace.NewID()
	_, _, err := f.simulations.PersistRunWithResult(ctx, workspace.SimulationRun{
		ID: runID,
		// A version that does not exist: the run insert violates its foreign
		// key, so the whole transaction (run + result) rolls back.
		ArchitectureVersionID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Status:                workspace.RunStatusCompleted,
		Seed:                  1,
		DurationMS:            1_000,
	}, workspace.SimulationResult{Metrics: json.RawMessage(`{"generated":1}`)})
	if !workspace.IsNotFound(err) {
		t.Fatalf("expected a not-found foreign-key error, got %v", err)
	}
	if _, err := f.simulations.GetRun(ctx, runID); !workspace.IsNotFound(err) {
		t.Fatalf("partial write survived the rollback: run %s exists (%v)", runID, err)
	}
}

// TestRunListings covers per-version and per-project history, newest first.
func TestRunListings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first := newCompletedRun(t, f)
	second := newCompletedRun(t, f)

	byVersion, err := f.simulations.ListRunsByVersion(ctx, f.version.ID)
	if err != nil {
		t.Fatalf("list runs by version: %v", err)
	}
	if len(byVersion) != 2 {
		t.Fatalf("got %d runs for the version, want 2", len(byVersion))
	}
	// created_at may tie inside one test; the id tiebreak keeps ordering
	// deterministic, so just assert both runs are present.
	if !containsRun(byVersion, first.ID) || !containsRun(byVersion, second.ID) {
		t.Fatalf("version listing is missing a run: %+v", byVersion)
	}

	byProject, err := f.simulations.ListRunsByProject(ctx, f.project.ID)
	if err != nil {
		t.Fatalf("list runs by project: %v", err)
	}
	if len(byProject) < 2 {
		t.Fatalf("got %d runs for the project, want at least 2", len(byProject))
	}
}

func containsRun(runs []workspace.SimulationRun, id string) bool {
	for _, r := range runs {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestDeleteProjectCascades removes a whole workspace and checks nothing
// under it survives: the containment relationship is deliberate, and this
// test locks the behavior in.
func TestDeleteProjectCascades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	run := newCompletedRun(t, f)

	if err := f.projects.Delete(ctx, f.project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := f.architectures.Get(ctx, f.architecture.ID); !workspace.IsNotFound(err) {
		t.Fatalf("architecture survived the project delete (%v)", err)
	}
	if _, err := f.architectures.GetVersion(ctx, f.version.ID); !workspace.IsNotFound(err) {
		t.Fatalf("version survived the project delete (%v)", err)
	}
	if _, err := f.workloads.Get(ctx, f.workload.ID); !workspace.IsNotFound(err) {
		t.Fatalf("workload survived the project delete (%v)", err)
	}
	if _, err := f.simulations.GetRun(ctx, run.ID); !workspace.IsNotFound(err) {
		t.Fatalf("run survived the project delete (%v)", err)
	}
	if _, err := f.simulations.GetResultByRun(ctx, run.ID); !workspace.IsNotFound(err) {
		t.Fatalf("result survived the project delete (%v)", err)
	}
}

// assertJSONField decodes a stored JSONB payload and checks one field.
func assertJSONField(t *testing.T, raw json.RawMessage, field string, want any) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("stored payload is not a JSON object: %v (%s)", err, raw)
	}
	got, ok := decoded[field]
	if !ok {
		t.Fatalf("stored payload has no %q field: %s", field, raw)
	}
	if got != want {
		t.Fatalf("stored %s = %v (%T), want %v (%T)", field, got, got, want, want)
	}
}
