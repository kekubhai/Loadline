package loadlinev1

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// newTestClient spins up an in-process Connect server over the real
// service and returns a generated client (exercises serialization and
// routing end to end, no network beyond loopback).
func newTestClient(t *testing.T) lv1connect.SimulationServiceClient {
	t.Helper()
	store := NewStore()
	path, handler := lv1connect.NewSimulationServiceHandler(NewService(store))
	mux := newMux(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Client base URL is the server root: procedure URLs already carry
	// the fully-qualified /loadline.v1.SimulationService/<RPC> path.
	return lv1connect.NewSimulationServiceClient(srv.Client(), srv.URL)
}

// testArchitecture is a small provider-backed architecture:
// client → Lambda (api) → ElastiCache (cache) → RDS (db).
func testArchitecture() *v1.Architecture {
	return &v1.Architecture{
		SchemaVersion: "1",
		Name:          "test-web",
		Components: []*v1.ComponentSpec{
			{Id: "client", Kind: v1.ComponentKind_COMPONENT_KIND_CLIENT},
			{Id: "api", Kind: v1.ComponentKind_COMPONENT_KIND_API_SERVER,
				Provider: "aws", Service: "lambda", Config: &v1.ProviderConfig{MemoryMb: 512}},
			{Id: "cache", Kind: v1.ComponentKind_COMPONENT_KIND_CACHE,
				Provider: "aws", Service: "elasticache", HitRatio: 0.8},
			{Id: "db", Kind: v1.ComponentKind_COMPONENT_KIND_DATABASE,
				Provider: "aws", Service: "rds", Config: &v1.ProviderConfig{StorageGb: 100}},
		},
		Links: []*v1.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
}

func testWorkload() *v1.WorkloadSpec {
	return &v1.WorkloadSpec{
		TotalUsers:            1_000_000,
		Dau:                   50_000,
		RequestsPerUserPerDay: 10,
		PeakMultiplier:        3,
		ReadWriteRatio:        4,
		PayloadBytes:          4096,
	}
}

// heavyWorkload produces ~2315 RPS peak — enough to push the RDS
// component past its modeled capacity once the cache stops filtering
// reads, so the cascade (saturation → queue overflow → load shedding)
// actually emerges in a short test window.
func heavyWorkload() *v1.WorkloadSpec {
	return &v1.WorkloadSpec{
		TotalUsers:            10_000_000,
		Dau:                   1_000_000,
		RequestsPerUserPerDay: 40,
		PeakMultiplier:        5,
		ReadWriteRatio:        4,
		PayloadBytes:          4096,
	}
}

func testOptions() *v1.SimulationOptions {
	return &v1.SimulationOptions{
		Seed:          7,
		DurationMs:    5_000, // short run: fast tests, enough requests for metrics
		RetryOn:       []string{"api", "cache"},
		MaxRetries:    2,
		BackoffBaseMs: 5,
		TimeoutMs:     50,
	}
}

// waitForCompleted polls until the run leaves RUNNING (or fails).
func waitForCompleted(t *testing.T, c lv1connect.SimulationServiceClient, id string) *v1.GetSimulationStatusResponse {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := c.GetSimulationStatus(context.Background(), connect.NewRequest(&v1.GetSimulationStatusRequest{SimulationId: id}))
		if err != nil {
			t.Fatalf("GetSimulationStatus: %v", err)
		}
		switch st.Msg.GetStatus() {
		case v1.RunStatus_RUN_STATUS_COMPLETED, v1.RunStatus_RUN_STATUS_FAILED:
			return st.Msg
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("simulation did not finish in time")
	return nil
}

// TestFullLifecycle covers create → run → stream → results → diagnosis →
// capacity → cost over the real service.
func TestFullLifecycle(t *testing.T) {
	c := newTestClient(t)

	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(),
		Workload:     testWorkload(),
		Options:      testOptions(),
	}))
	if err != nil {
		t.Fatalf("CreateSimulation: %v", err)
	}
	id := created.Msg.GetSimulation().GetId()
	if id == "" {
		t.Fatal("expected non-empty simulation ID")
	}

	// Results before running must fail cleanly.
	if _, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id})); err == nil {
		t.Fatal("expected GetResults before run to fail")
	}

	if _, err := c.RunSimulation(context.Background(), connect.NewRequest(&v1.RunSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("RunSimulation: %v", err)
	}
	// Double-run must be rejected.
	if _, err := c.RunSimulation(context.Background(), connect.NewRequest(&v1.RunSimulationRequest{SimulationId: id})); err == nil {
		t.Fatal("expected second RunSimulation to fail")
	}

	final := waitForCompleted(t, c, id)
	if final.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("run failed: %s", final.GetError())
	}

	res, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	m := res.Msg.GetMetrics()
	if m.GetGenerated() == 0 || m.GetCompleted() == 0 {
		t.Fatalf("expected real traffic, got generated=%d completed=%d", m.GetGenerated(), m.GetCompleted())
	}
	if len(m.GetComponents()) != 4 {
		t.Fatalf("expected 4 component reports, got %d", len(m.GetComponents()))
	}
	// Conservation: Generated = Completed + Rejected + Failed + InFlight.
	if m.GetGenerated() != m.GetCompleted()+m.GetRejected()+m.GetFailed()+m.GetInFlight() {
		t.Fatalf("conservation violated: %d != %d+%d+%d+%d",
			m.GetGenerated(), m.GetCompleted(), m.GetRejected(), m.GetFailed(), m.GetInFlight())
	}
	if res.Msg.GetPlan().GetPeakRps() <= 0 {
		t.Fatal("expected derived peak RPS in plan")
	}
	if res.Msg.GetSummary().GetEventsProcessed() == 0 {
		t.Fatal("expected processed events in summary")
	}

	// Diagnosis on a healthy short run should be produced (healthy or not,
	// the summary text must exist).
	diag, err := c.GetDiagnosis(context.Background(), connect.NewRequest(&v1.GetDiagnosisRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetDiagnosis: %v", err)
	}
	if diag.Msg.GetDiagnosis().GetSummary() == "" {
		t.Fatal("expected diagnosis summary")
	}

	// Capacity and cost need provider-backed components — this
	// architecture has three.
	cap, err := c.GetCapacity(context.Background(), connect.NewRequest(&v1.GetCapacityRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetCapacity: %v", err)
	}
	if len(cap.Msg.GetReports()) != 3 {
		t.Fatalf("expected 3 capacity reports, got %d", len(cap.Msg.GetReports()))
	}
	for _, r := range cap.Msg.GetReports() {
		if r.GetMaxSustainableRps() <= 0 {
			t.Fatalf("component %s: expected modeled ceiling > 0", r.GetComponentId())
		}
		if r.GetHeadroom()+r.GetUtilization() < 0.999 || r.GetHeadroom()+r.GetUtilization() > 1.001 {
			t.Fatalf("component %s: utilization+headroom must be 1", r.GetComponentId())
		}
		if len(r.GetAssumptions()) == 0 {
			t.Fatalf("component %s: assumptions must be recorded", r.GetComponentId())
		}
	}

	cost, err := c.GetCostEstimate(context.Background(), connect.NewRequest(&v1.GetCostEstimateRequest{SimulationId: id}))
	if err != nil {
		t.Fatalf("GetCostEstimate: %v", err)
	}
	if cost.Msg.GetEstimate().GetTotal() <= 0 {
		t.Fatal("expected positive total estimate")
	}
	if !contains(cost.Msg.GetEstimate().GetAssumptions(), "prices are LOCAL ESTIMATES for mid-tier plans, primary commercial regions; not live billing data") {
		t.Fatal("cost estimate must carry the not-live-billing assumption")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestCatalog lists the built-in catalog and checks determinism.
func TestCatalog(t *testing.T) {
	c := newTestClient(t)
	r1, err := c.ListCatalog(context.Background(), connect.NewRequest(&v1.ListCatalogRequest{}))
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	r2, err := c.ListCatalog(context.Background(), connect.NewRequest(&v1.ListCatalogRequest{}))
	if err != nil {
		t.Fatalf("ListCatalog 2: %v", err)
	}
	if len(r1.Msg.GetServices()) != 18 {
		t.Fatalf("expected 18 catalog services, got %d", len(r1.Msg.GetServices()))
	}
	for i := range r1.Msg.GetServices() {
		if r1.Msg.GetServices()[i].GetService() != r2.Msg.GetServices()[i].GetService() {
			t.Fatal("catalog order must be deterministic")
		}
		if r1.Msg.GetServices()[i].GetComponentKind() == "" {
			t.Fatalf("service %s: missing component kind", r1.Msg.GetServices()[i].GetService())
		}
	}
}

// TestStreamMetrics verifies the stream protocol: progress frames from
// real sampling windows, then a terminal status + results frame. The
// stream attaches BEFORE the run starts — the natural frontend order —
// so it observes every state change of the run.
func TestStreamMetrics(t *testing.T) {
	c := newTestClient(t)
	opts := testOptions()
	opts.DurationMs = 10_000 // 10 sampling windows → 10 progress frames
	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(),
		Workload:     testWorkload(),
		Options:      opts,
	}))
	if err != nil {
		t.Fatalf("CreateSimulation: %v", err)
	}
	id := created.Msg.GetSimulation().GetId()

	type streamResult struct {
		progressFrames int
		results        *v1.FinalResults
		status         v1.RunStatus
		err            error
	}
	streamCh := make(chan streamResult, 1)
	go func() {
		stream, err := c.StreamMetrics(context.Background(), connect.NewRequest(&v1.StreamMetricsRequest{SimulationId: id}))
		if err != nil {
			streamCh <- streamResult{err: err}
			return
		}
		out := streamResult{status: v1.RunStatus_RUN_STATUS_UNSPECIFIED}
		for stream.Receive() {
			switch ev := stream.Msg().GetEvent().(type) {
			case *v1.StreamMetricsResponse_Progress:
				out.progressFrames++
			case *v1.StreamMetricsResponse_Status:
				out.status = ev.Status.GetStatus()
			case *v1.StreamMetricsResponse_Results:
				out.results = ev.Results
			}
		}
		out.err = stream.Err()
		streamCh <- out
	}()

	// Give the stream a moment to attach, then trigger the run.
	time.Sleep(20 * time.Millisecond)
	if _, err := c.RunSimulation(context.Background(), connect.NewRequest(&v1.RunSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("RunSimulation: %v", err)
	}

	out := <-streamCh
	if out.err != nil {
		t.Fatalf("stream error: %v", out.err)
	}
	if out.status != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("stream ended with status %v", out.status)
	}
	if out.results == nil {
		t.Fatal("expected final results frame")
	}
	if out.results.GetMetrics().GetCompleted() == 0 {
		t.Fatal("expected completed requests in streamed results")
	}
	if out.progressFrames == 0 {
		t.Fatal("expected at least one progress frame from a 10s run")
	}
}

// TestDeterminismAcrossAPI runs the identical seeded simulation twice
// through the full API lifecycle and requires identical final metrics —
// the API layer must not perturb engine determinism.
func TestDeterminismAcrossAPI(t *testing.T) {
	c := newTestClient(t)
	run := func() *v1.SystemMetrics {
		created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
			Architecture: testArchitecture(),
			Workload:     testWorkload(),
			Options:      testOptions(),
		}))
		if err != nil {
			t.Fatalf("CreateSimulation: %v", err)
		}
		id := created.Msg.GetSimulation().GetId()
		if _, err := c.RunSimulation(context.Background(), connect.NewRequest(&v1.RunSimulationRequest{SimulationId: id})); err != nil {
			t.Fatalf("RunSimulation: %v", err)
		}
		if st := waitForCompleted(t, c, id); st.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
			t.Fatalf("run failed: %s", st.GetError())
		}
		res, err := c.GetResults(context.Background(), connect.NewRequest(&v1.GetResultsRequest{SimulationId: id}))
		if err != nil {
			t.Fatalf("GetResults: %v", err)
		}
		return res.Msg.GetMetrics()
	}
	a, b := run(), run()
	if a.GetGenerated() != b.GetGenerated() || a.GetCompleted() != b.GetCompleted() ||
		a.GetP99Ms() != b.GetP99Ms() || a.GetP95Ms() != b.GetP95Ms() || a.GetAvgLatencyMs() != b.GetAvgLatencyMs() {
		t.Fatalf("same seed produced different metrics through the API:\n  run1 gen=%d compl=%d p95=%.4f p99=%.4f avg=%.4f\n  run2 gen=%d compl=%d p95=%.4f p99=%.4f avg=%.4f",
			a.GetGenerated(), a.GetCompleted(), a.GetP95Ms(), a.GetP99Ms(), a.GetAvgLatencyMs(),
			b.GetGenerated(), b.GetCompleted(), b.GetP95Ms(), b.GetP99Ms(), b.GetAvgLatencyMs())
	}
}

// TestValidationErrors checks structured rejection of bad inputs.
func TestValidationErrors(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Unknown provider service.
	arch := testArchitecture()
	arch.Components[1].Service = "not_a_service"
	if _, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: arch, Workload: testWorkload(),
	})); err == nil {
		t.Fatal("expected unknown service rejection")
	}

	// Unknown failure target.
	opts := testOptions()
	opts.Failures = []*v1.Failure{{Target: "nope", Type: v1.FailureType_FAILURE_TYPE_CRASH, StartMs: 100, DurationMs: 100}}
	if _, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(), Workload: testWorkload(), Options: opts,
	})); err == nil {
		t.Fatal("expected unknown failure target rejection")
	}

	// Invalid workload (peak < 1).
	wl := testWorkload()
	wl.PeakMultiplier = 0.5
	if _, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(), Workload: wl,
	})); err == nil {
		t.Fatal("expected invalid workload rejection")
	}

	// Not-found IDs.
	if _, err := c.GetResults(ctx, connect.NewRequest(&v1.GetResultsRequest{SimulationId: "sim-404"})); err == nil {
		t.Fatal("expected not-found rejection")
	}
	if _, err := c.RunSimulation(ctx, connect.NewRequest(&v1.RunSimulationRequest{SimulationId: "sim-404"})); err == nil {
		t.Fatal("expected not-found rejection")
	}

	// Cycle detection via the engine at run time (architecture-level).
	cyclic := testArchitecture()
	cyclic.Links = []*v1.Link{
		{From: "client", To: "api"},
		{From: "api", To: "cache"},
		{From: "cache", To: "db"},
		{From: "db", To: "api"},
	}
	created, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: cyclic, Workload: testWorkload(), Options: testOptions(),
	}))
	if err != nil {
		t.Fatalf("CreateSimulation (cycle): %v", err)
	}
	if _, err := c.RunSimulation(ctx, connect.NewRequest(&v1.RunSimulationRequest{SimulationId: created.Msg.GetSimulation().GetId()})); err != nil {
		t.Fatalf("RunSimulation (cycle): %v", err)
	}
	st := waitForCompleted(t, c, created.Msg.GetSimulation().GetId())
	if st.GetStatus() != v1.RunStatus_RUN_STATUS_FAILED {
		t.Fatal("expected cyclic architecture to fail the run")
	}
}

// TestFailureInjectionEndToEnd runs the cache-crash cascade through the
// API under heavy load and verifies the diagnosis reports the database
// bottleneck with real numbers.
func TestFailureInjectionEndToEnd(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	opts := testOptions()
	opts.DurationMs = 10_000

	// Baseline: same seed, no failures.
	base, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(), Workload: heavyWorkload(), Options: opts,
	}))
	if err != nil {
		t.Fatalf("CreateSimulation baseline: %v", err)
	}
	baseID := base.Msg.GetSimulation().GetId()
	if _, err := c.RunSimulation(ctx, connect.NewRequest(&v1.RunSimulationRequest{SimulationId: baseID})); err != nil {
		t.Fatalf("RunSimulation baseline: %v", err)
	}
	if st := waitForCompleted(t, c, baseID); st.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("baseline failed: %s", st.GetError())
	}

	// Cache crash 1s→6s with pass-through (cache → origin fallback):
	// all reads hit the database for 5 simulated seconds.
	opts.Failures = []*v1.Failure{{
		Target: "cache", Type: v1.FailureType_FAILURE_TYPE_CRASH,
		StartMs: 1_000, DurationMs: 5_000,
		Config: &v1.FailureConfig{PassThrough: true},
	}}
	crash, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(), Workload: heavyWorkload(), Options: opts,
	}))
	if err != nil {
		t.Fatalf("CreateSimulation crash: %v", err)
	}
	crashID := crash.Msg.GetSimulation().GetId()
	if _, err := c.RunSimulation(ctx, connect.NewRequest(&v1.RunSimulationRequest{SimulationId: crashID})); err != nil {
		t.Fatalf("RunSimulation crash: %v", err)
	}
	if st := waitForCompleted(t, c, crashID); st.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("crash run failed: %s", st.GetError())
	}

	diag, err := c.GetDiagnosis(ctx, connect.NewRequest(&v1.GetDiagnosisRequest{
		SimulationId: crashID, BaselineSimulationId: baseID,
	}))
	if err != nil {
		t.Fatalf("GetDiagnosis: %v", err)
	}
	if diag.Msg.GetDiagnosis().GetHealthy() {
		t.Fatal("expected bottlenecks after cache-crash injection")
	}
	var dbFound bool
	for _, b := range diag.Msg.GetDiagnosis().GetBottlenecks() {
		if b.GetComponentId() == "db" && b.GetSeverity() == "critical" {
			dbFound = true
			if len(b.GetReasons()) == 0 {
				t.Fatal("bottleneck must carry machine-computed reasons")
			}
		}
	}
	if !dbFound {
		t.Fatalf("expected critical db bottleneck, got: %+v", diag.Msg.GetDiagnosis())
	}

	// Same seed → identical arrivals; the crash must measurably change DB load.
	resB, err := c.GetResults(ctx, connect.NewRequest(&v1.GetResultsRequest{SimulationId: baseID}))
	if err != nil {
		t.Fatalf("GetResults base: %v", err)
	}
	resC, err := c.GetResults(ctx, connect.NewRequest(&v1.GetResultsRequest{SimulationId: crashID}))
	if err != nil {
		t.Fatalf("GetResults crash: %v", err)
	}
	dbArrivals := func(m *v1.SystemMetrics) uint64 {
		for _, c := range m.GetComponents() {
			if c.GetId() == "db" {
				return c.GetArrived()
			}
		}
		return 0
	}
	if dbArrivals(resC.Msg.GetMetrics()) <= dbArrivals(resB.Msg.GetMetrics()) {
		t.Fatalf("crash must push more traffic to db: base=%d crash=%d",
			dbArrivals(resB.Msg.GetMetrics()), dbArrivals(resC.Msg.GetMetrics()))
	}
}

// TestProgressHookDeterminism guards the invariant that enabling the
// progress hook (as the API layer does) does not perturb the simulation.
func TestProgressHookDeterminism(t *testing.T) {
	arch, resolved, err := protoArchToDomain(testArchitecture())
	if err != nil {
		t.Fatalf("protoArchToDomain: %v", err)
	}
	wl := protoWorkloadToDomain(testWorkload())

	run := func(withHook bool) *sim.Metrics {
		opts := protoOptionsToDomain(testOptions())
		if withHook {
			opts.Progress = func(snap sim.Snapshot) { /* consume, ignore */ }
		}
		res, err := sim.Simulate(arch, wl, opts)
		if err != nil {
			t.Fatalf("Simulate: %v", err)
		}
		return &res.Metrics
	}
	a, b := run(false), run(true)
	if a.Generated != b.Generated || a.Completed != b.Completed || a.P99MS != b.P99MS {
		t.Fatalf("progress hook perturbed the simulation: %+v vs %+v", a, b)
	}
	_ = resolved
}
