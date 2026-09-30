package loadlinev1

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"connectrpc.com/connect"

	"github.com/kekubhai/Loadline/apps/simulator/internal/db"
	"github.com/kekubhai/Loadline/apps/simulator/internal/repositories"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
)

// newWorkspaceTestClient starts the real WorkspaceService over the real
// repositories against a real PostgreSQL database, and returns a generated
// Connect client (so serialization, routing, SQL, and the engine are all
// exercised end to end).
//
// The test SKIPS when no database is configured. LOADLINE_TEST_DATABASE_URL
// is preferred so the application's own DATABASE_URL is not touched by test
// data.
func newWorkspaceTestClient(t *testing.T) lv1connect.WorkspaceServiceClient {
	t.Helper()
	// `go test` runs each package from its own directory, so the repository's
	// .env has to be located explicitly before the variables are read.
	db.LoadDotEnv()
	url := os.Getenv(db.EnvTestDatabaseURL)
	if url == "" {
		url = os.Getenv(db.EnvDatabaseURL)
	}
	if url == "" {
		t.Skipf("set %s (or %s) to run workspace persistence tests", db.EnvTestDatabaseURL, db.EnvDatabaseURL)
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

	svc := NewWorkspaceService(
		repositories.NewProjectRepository(database),
		repositories.NewArchitectureRepository(database),
		repositories.NewWorkloadRepository(database),
		repositories.NewSimulationRepository(database),
	)
	path, handler := lv1connect.NewWorkspaceServiceHandler(svc)
	srv := httptest.NewServer(newMux(path, handler))
	t.Cleanup(srv.Close)
	return lv1connect.NewWorkspaceServiceClient(srv.Client(), srv.URL)
}

// workspaceFixture creates a project, an architecture with its first version,
// and a workload through the API, then removes the project when the test
// ends (which cascades to everything it contained).
type workspaceFixture struct {
	client   lv1connect.WorkspaceServiceClient
	project  *v1.Project
	arch     *v1.ArchitectureRecord
	version  *v1.ArchitectureVersion
	workload *v1.WorkloadRecord
}

func newWorkspaceFixture(t *testing.T) *workspaceFixture {
	t.Helper()
	c := newWorkspaceTestClient(t)
	ctx := context.Background()

	project, err := c.CreateProject(ctx, connect.NewRequest(&v1.CreateProjectRequest{
		Name: "workspace-test", Description: "persistence milestone",
	}))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	f := &workspaceFixture{client: c, project: project.Msg.GetProject()}
	t.Cleanup(func() {
		_, _ = c.DeleteProject(context.Background(), connect.NewRequest(&v1.DeleteProjectRequest{Id: f.project.GetId()}))
	})

	arch, err := c.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId:  f.project.GetId(),
		Name:       "web",
		Definition: testArchitecture(),
	}))
	if err != nil {
		t.Fatalf("CreateArchitecture: %v", err)
	}
	f.arch = arch.Msg.GetArchitecture()
	f.version = arch.Msg.GetVersion()
	if f.version.GetVersion() != 1 {
		t.Fatalf("first version = %d, want 1", f.version.GetVersion())
	}

	wl, err := c.CreateWorkload(ctx, connect.NewRequest(&v1.CreateWorkloadRequest{
		ArchitectureVersionId: f.version.GetId(),
		Name:                  "peak",
		Spec:                  testWorkload(),
	}))
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}
	f.workload = wl.Msg.GetWorkload()
	return f
}

// TestWorkspaceExecuteSimulationPersistsResult is the milestone's core
// workflow: build → save → run → save run → read it back.
func TestWorkspaceExecuteSimulationPersistsResult(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	resp, err := f.client.ExecuteSimulation(ctx, connect.NewRequest(&v1.ExecuteSimulationRequest{
		ArchitectureVersionId: f.version.GetId(),
		WorkloadId:            f.workload.GetId(),
		Options:               testOptions(),
	}))
	if err != nil {
		t.Fatalf("ExecuteSimulation: %v", err)
	}
	result := resp.Msg.GetResult()
	run := result.GetRun()

	if run.GetId() == "" {
		t.Fatal("run has no id")
	}
	if run.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("status = %v (%s), want COMPLETED", run.GetStatus(), run.GetError())
	}
	// The seed is mandatory: without it the stored run cannot be reproduced.
	if run.GetSeed() != 7 {
		t.Fatalf("seed = %d, want 7", run.GetSeed())
	}
	if run.GetDurationMs() <= 0 {
		t.Fatalf("duration = %v, want the measured horizon", run.GetDurationMs())
	}
	if run.GetWorkloadId() != f.workload.GetId() {
		t.Fatalf("run workload = %q, want %q", run.GetWorkloadId(), f.workload.GetId())
	}
	if run.GetCreatedAt() == nil || run.GetFinishedAt() == nil {
		t.Fatal("run must carry created_at and finished_at")
	}

	m := result.GetMetrics()
	if m.GetGenerated() == 0 || m.GetCompleted() == 0 {
		t.Fatalf("expected real traffic, got generated=%d completed=%d", m.GetGenerated(), m.GetCompleted())
	}
	if len(m.GetComponents()) != 4 {
		t.Fatalf("expected 4 component reports, got %d", len(m.GetComponents()))
	}
	if result.GetPlan().GetPeakRps() <= 0 {
		t.Fatal("expected the derived load plan")
	}
	if result.GetSummary().GetEventsProcessed() == 0 {
		t.Fatal("expected engine counters in the summary")
	}
	if result.GetDiagnosis().GetSummary() == "" {
		t.Fatal("expected the diagnosis to be stored with the run")
	}
	// testArchitecture() is provider-backed, so capacity and cost exist.
	if len(result.GetCapacity()) != 3 {
		t.Fatalf("expected 3 capacity reports, got %d", len(result.GetCapacity()))
	}
	if result.GetCost().GetTotal() <= 0 {
		t.Fatal("expected a positive cost estimate")
	}

	// Reading the run back returns the same measured numbers.
	got, err := f.client.GetSimulationRun(ctx, connect.NewRequest(&v1.GetSimulationRunRequest{Id: run.GetId()}))
	if err != nil {
		t.Fatalf("GetSimulationRun: %v", err)
	}
	stored := got.Msg.GetResult()
	if stored.GetRun().GetSeed() != run.GetSeed() {
		t.Fatalf("stored seed = %d, want %d", stored.GetRun().GetSeed(), run.GetSeed())
	}
	if stored.GetMetrics().GetGenerated() != m.GetGenerated() {
		t.Fatalf("stored generated = %d, want %d", stored.GetMetrics().GetGenerated(), m.GetGenerated())
	}
	if stored.GetMetrics().GetP99Ms() != m.GetP99Ms() {
		t.Fatalf("stored p99 = %v, want %v", stored.GetMetrics().GetP99Ms(), m.GetP99Ms())
	}
	if len(stored.GetMetrics().GetComponents()) != len(m.GetComponents()) {
		t.Fatalf("stored component metrics = %d, want %d", len(stored.GetMetrics().GetComponents()), len(m.GetComponents()))
	}
	if len(stored.GetCapacity()) != len(result.GetCapacity()) {
		t.Fatalf("stored capacity reports = %d, want %d", len(stored.GetCapacity()), len(result.GetCapacity()))
	}
	if stored.GetCost().GetTotal() != result.GetCost().GetTotal() {
		t.Fatalf("stored cost total = %v, want %v", stored.GetCost().GetTotal(), result.GetCost().GetTotal())
	}
	if stored.GetDiagnosis().GetSummary() != result.GetDiagnosis().GetSummary() {
		t.Fatal("stored diagnosis did not round-trip")
	}
	if stored.GetSummary().GetStopReason() != result.GetSummary().GetStopReason() {
		t.Fatal("stored run summary did not round-trip")
	}

	// History lists the run.
	list, err := f.client.ListSimulationRuns(ctx, connect.NewRequest(&v1.ListSimulationRunsRequest{
		ArchitectureVersionId: f.version.GetId(),
	}))
	if err != nil {
		t.Fatalf("ListSimulationRuns: %v", err)
	}
	if len(list.Msg.GetRuns()) != 1 || list.Msg.GetRuns()[0].GetId() != run.GetId() {
		t.Fatalf("history = %+v, want the one executed run", list.Msg.GetRuns())
	}
}

// TestWorkspaceRunDeterminism proves the persisted seed reproduces the run
// exactly: the same stored version + workload + seed yields identical
// metrics, twice.
func TestWorkspaceRunDeterminism(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	execute := func() *v1.SimulationRunResult {
		resp, err := f.client.ExecuteSimulation(ctx, connect.NewRequest(&v1.ExecuteSimulationRequest{
			ArchitectureVersionId: f.version.GetId(),
			WorkloadId:            f.workload.GetId(),
			Options:               testOptions(),
		}))
		if err != nil {
			t.Fatalf("ExecuteSimulation: %v", err)
		}
		return resp.Msg.GetResult()
	}

	first := execute()
	// Read the first run back before the second runs: the payload must match
	// what the in-memory response reported.
	stored, err := f.client.GetSimulationRun(ctx, connect.NewRequest(&v1.GetSimulationRunRequest{Id: first.GetRun().GetId()}))
	if err != nil {
		t.Fatalf("GetSimulationRun: %v", err)
	}
	second := execute()

	a, b := first.GetMetrics(), second.GetMetrics()
	if a.GetGenerated() != b.GetGenerated() || a.GetCompleted() != b.GetCompleted() ||
		a.GetP95Ms() != b.GetP95Ms() || a.GetP99Ms() != b.GetP99Ms() || a.GetAvgLatencyMs() != b.GetAvgLatencyMs() {
		t.Fatalf("same seed produced different metrics:\n  run1 gen=%d compl=%d p95=%.4f p99=%.4f avg=%.4f\n  run2 gen=%d compl=%d p95=%.4f p99=%.4f avg=%.4f",
			a.GetGenerated(), a.GetCompleted(), a.GetP95Ms(), a.GetP99Ms(), a.GetAvgLatencyMs(),
			b.GetGenerated(), b.GetCompleted(), b.GetP95Ms(), b.GetP99Ms(), b.GetAvgLatencyMs())
	}
	// The replay of the first run must equal the live result too.
	s := stored.Msg.GetResult().GetMetrics()
	if s.GetGenerated() != a.GetGenerated() || s.GetP99Ms() != a.GetP99Ms() {
		t.Fatalf("stored run does not reproduce the live result: stored gen=%d p99=%.4f, live gen=%d p99=%.4f",
			s.GetGenerated(), s.GetP99Ms(), a.GetGenerated(), a.GetP99Ms())
	}

	list, err := f.client.ListSimulationRuns(ctx, connect.NewRequest(&v1.ListSimulationRunsRequest{
		ArchitectureVersionId: f.version.GetId(),
	}))
	if err != nil {
		t.Fatalf("ListSimulationRuns: %v", err)
	}
	if len(list.Msg.GetRuns()) != 2 {
		t.Fatalf("expected 2 stored runs, got %d", len(list.Msg.GetRuns()))
	}
}

// TestWorkspaceArchitectureVersioning exercises the append-only version chain
// through the API, and confirms a re-run after a modification uses the new
// version's definition.
func TestWorkspaceArchitectureVersioning(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	// v2 adds a load balancer in front of the API server.
	v2Arch := testArchitecture()
	v2Arch.Components = append([]*v1.ComponentSpec{
		{Id: "lb", Kind: v1.ComponentKind_COMPONENT_KIND_LOAD_BALANCER, FanOut: 1},
	}, v2Arch.Components...)
	v2Arch.Links = append([]*v1.Link{{From: "client", To: "lb"}, {From: "lb", To: "api"}}, v2Arch.Links[1:]...)

	created, err := f.client.CreateArchitectureVersion(ctx, connect.NewRequest(&v1.CreateArchitectureVersionRequest{
		ArchitectureId: f.arch.GetId(),
		Definition:     v2Arch,
	}))
	if err != nil {
		t.Fatalf("CreateArchitectureVersion: %v", err)
	}
	if created.Msg.GetVersion().GetVersion() != 2 {
		t.Fatalf("assigned v%d, want v2", created.Msg.GetVersion().GetVersion())
	}

	versions, err := f.client.ListArchitectureVersions(ctx, connect.NewRequest(&v1.ListArchitectureVersionsRequest{
		ArchitectureId: f.arch.GetId(),
	}))
	if err != nil {
		t.Fatalf("ListArchitectureVersions: %v", err)
	}
	if len(versions.Msg.GetVersions()) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions.Msg.GetVersions()))
	}
	for i, v := range versions.Msg.GetVersions() {
		if int(v.GetVersion()) != i+1 {
			t.Fatalf("versions out of order at %d: v%d", i, v.GetVersion())
		}
		if v.GetDefinition() == nil || len(v.GetDefinition().GetComponents()) == 0 {
			t.Fatalf("v%d lost its definition", v.GetVersion())
		}
	}
	// v1 keeps its original component count; v2 has the extra load balancer.
	if n := len(versions.Msg.GetVersions()[0].GetDefinition().GetComponents()); n != 4 {
		t.Fatalf("v1 has %d components, want 4 (history must be immutable)", n)
	}
	if n := len(versions.Msg.GetVersions()[1].GetDefinition().GetComponents()); n != 5 {
		t.Fatalf("v2 has %d components, want 5", n)
	}

	// Renaming the architecture leaves the version chain alone.
	renamed, err := f.client.UpdateArchitecture(ctx, connect.NewRequest(&v1.UpdateArchitectureRequest{
		Id: f.arch.GetId(), Name: "renamed", Description: "still the same history",
	}))
	if err != nil {
		t.Fatalf("UpdateArchitecture: %v", err)
	}
	if renamed.Msg.GetArchitecture().GetLatestVersion() != 2 || renamed.Msg.GetArchitecture().GetVersionCount() != 2 {
		t.Fatalf("rename changed the version summary: %+v", renamed.Msg.GetArchitecture())
	}
}

// TestWorkspaceWorkloadCRUDThroughAPI covers the workload lifecycle.
func TestWorkspaceWorkloadCRUDThroughAPI(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	updated, err := f.client.UpdateWorkload(ctx, connect.NewRequest(&v1.UpdateWorkloadRequest{
		Id:   f.workload.GetId(),
		Name: "peak-v2",
		Spec: &v1.WorkloadSpec{TotalUsers: 2_000_000, Dau: 100_000, RequestsPerUserPerDay: 20, PeakMultiplier: 4, ReadWriteRatio: 4, PayloadBytes: 4096},
	}))
	if err != nil {
		t.Fatalf("UpdateWorkload: %v", err)
	}
	if updated.Msg.GetWorkload().GetSpec().GetDau() != 100_000 {
		t.Fatalf("update did not persist the spec: %+v", updated.Msg.GetWorkload())
	}

	list, err := f.client.ListWorkloads(ctx, connect.NewRequest(&v1.ListWorkloadsRequest{
		ArchitectureVersionId: f.version.GetId(),
	}))
	if err != nil {
		t.Fatalf("ListWorkloads: %v", err)
	}
	if len(list.Msg.GetWorkloads()) != 1 || list.Msg.GetWorkloads()[0].GetName() != "peak-v2" {
		t.Fatalf("list = %+v", list.Msg.GetWorkloads())
	}

	if _, err := f.client.DeleteWorkload(ctx, connect.NewRequest(&v1.DeleteWorkloadRequest{Id: f.workload.GetId()})); err != nil {
		t.Fatalf("DeleteWorkload: %v", err)
	}
	if _, err := f.client.GetWorkload(ctx, connect.NewRequest(&v1.GetWorkloadRequest{Id: f.workload.GetId()})); !isNotFound(err) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
}

// TestWorkspaceValidationErrors checks that bad input is rejected with the
// right status and that nothing is persisted along the way.
func TestWorkspaceValidationErrors(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	// Missing required fields.
	if _, err := f.client.CreateProject(ctx, connect.NewRequest(&v1.CreateProjectRequest{})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument for a project without a name, got %v", err)
	}
	if _, err := f.client.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId: f.project.GetId(), Name: "no-definition",
	})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument for a missing definition, got %v", err)
	}
	// A workload the engine cannot derive a plan from must be rejected before
	// it is stored.
	if _, err := f.client.CreateWorkload(ctx, connect.NewRequest(&v1.CreateWorkloadRequest{
		ArchitectureVersionId: f.version.GetId(),
		Name:                  "impossible",
		Spec:                  &v1.WorkloadSpec{TotalUsers: 10, Dau: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 0.5, ReadWriteRatio: 4},
	})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument for an underivable workload, got %v", err)
	}
	// Unknown ids are not found, and malformed ids are invalid arguments —
	// never a database error surfaced to the client.
	if _, err := f.client.GetProject(ctx, connect.NewRequest(&v1.GetProjectRequest{Id: "f47ac10b-58cc-4372-a567-0e02b2c3d479"})); !isNotFound(err) {
		t.Errorf("expected not-found for an unknown project, got %v", err)
	}
	if _, err := f.client.GetProject(ctx, connect.NewRequest(&v1.GetProjectRequest{Id: "'; DROP TABLE projects; --"})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument for a malformed id, got %v", err)
	}
	// Run history needs exactly one selector.
	if _, err := f.client.ListSimulationRuns(ctx, connect.NewRequest(&v1.ListSimulationRunsRequest{})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument with no selector, got %v", err)
	}
	if _, err := f.client.ListSimulationRuns(ctx, connect.NewRequest(&v1.ListSimulationRunsRequest{
		ArchitectureVersionId: f.version.GetId(), ProjectId: f.project.GetId(),
	})); !isInvalidArgument(err) {
		t.Errorf("expected invalid-argument with two selectors, got %v", err)
	}
}

// TestWorkspaceExecuteSimulationRejectsMismatchedWorkload guards the
// referential integrity of a stored run: a workload from another version must
// not be runnable against this one.
func TestWorkspaceExecuteSimulationRejectsMismatchedWorkload(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	// A second architecture (same project) with its own version and workload.
	other, err := f.client.CreateArchitecture(ctx, connect.NewRequest(&v1.CreateArchitectureRequest{
		ProjectId: f.project.GetId(), Name: "other", Definition: testArchitecture(),
	}))
	if err != nil {
		t.Fatalf("CreateArchitecture: %v", err)
	}
	otherWorkload, err := f.client.CreateWorkload(ctx, connect.NewRequest(&v1.CreateWorkloadRequest{
		ArchitectureVersionId: other.Msg.GetVersion().GetId(), Name: "other-peak", Spec: testWorkload(),
	}))
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}

	if _, err := f.client.ExecuteSimulation(ctx, connect.NewRequest(&v1.ExecuteSimulationRequest{
		ArchitectureVersionId: f.version.GetId(),
		WorkloadId:            otherWorkload.Msg.GetWorkload().GetId(),
		Options:               testOptions(),
	})); !isInvalidArgument(err) {
		t.Fatalf("expected invalid-argument for a mismatched workload, got %v", err)
	}
}

// TestWorkspaceExecuteSimulationPersistsFailure checks that a run the engine
// rejects is recorded honestly: status FAILED with the reason, and no
// fabricated metrics.
func TestWorkspaceExecuteSimulationPersistsFailure(t *testing.T) {
	f := newWorkspaceFixture(t)
	ctx := context.Background()

	// A cyclic architecture: the engine rejects it at execution time.
	cyclic := testArchitecture()
	cyclic.Links = append(cyclic.Links, &v1.Link{From: "db", To: "api"})
	bad, err := f.client.CreateArchitectureVersion(ctx, connect.NewRequest(&v1.CreateArchitectureVersionRequest{
		ArchitectureId: f.arch.GetId(), Definition: cyclic,
	}))
	if err != nil {
		t.Fatalf("CreateArchitectureVersion: %v", err)
	}
	badWorkload, err := f.client.CreateWorkload(ctx, connect.NewRequest(&v1.CreateWorkloadRequest{
		ArchitectureVersionId: bad.Msg.GetVersion().GetId(), Name: "cyclic", Spec: testWorkload(),
	}))
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}

	resp, err := f.client.ExecuteSimulation(ctx, connect.NewRequest(&v1.ExecuteSimulationRequest{
		ArchitectureVersionId: bad.Msg.GetVersion().GetId(),
		WorkloadId:            badWorkload.Msg.GetWorkload().GetId(),
		Options:               testOptions(),
	}))
	if err != nil {
		t.Fatalf("ExecuteSimulation: %v", err)
	}
	result := resp.Msg.GetResult()
	if result.GetRun().GetStatus() != v1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", result.GetRun().GetStatus())
	}
	if result.GetRun().GetError() == "" {
		t.Fatal("a failed run must record why it failed")
	}
	if result.GetMetrics() != nil {
		t.Fatal("a failed run must not carry metrics")
	}
	// The failure is durable: reading it back reports the same thing.
	stored, err := f.client.GetSimulationRun(ctx, connect.NewRequest(&v1.GetSimulationRunRequest{Id: result.GetRun().GetId()}))
	if err != nil {
		t.Fatalf("GetSimulationRun: %v", err)
	}
	if stored.Msg.GetResult().GetRun().GetStatus() != v1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("stored status = %v, want FAILED", stored.Msg.GetResult().GetRun().GetStatus())
	}
	if stored.Msg.GetResult().GetMetrics() != nil {
		t.Fatal("stored failed run must not carry metrics")
	}
}

// isInvalidArgument reports whether err is a Connect invalid-argument error.
func isInvalidArgument(err error) bool {
	return connect.CodeOf(err) == connect.CodeInvalidArgument
}

// isNotFound reports whether err is a Connect not-found error.
func isNotFound(err error) bool {
	return connect.CodeOf(err) == connect.CodeNotFound
}
