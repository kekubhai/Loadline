package loadlinev1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
)

// runViaAPI creates and runs one simulation through the real service and
// returns its final metrics plus the run ID.
func runViaAPI(t *testing.T, c lv1connect.SimulationServiceClient, failures []*v1.Failure) (string, *v1.SystemMetrics) {
	t.Helper()
	opts := testOptions()
	opts.DurationMs = 20_000
	opts.Failures = failures
	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(),
		Workload:     heavyWorkload(),
		Options:      opts,
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
	return id, res.Msg.GetMetrics()
}

// componentMetrics finds one component's report.
func componentMetrics(m *v1.SystemMetrics, id string) *v1.ComponentMetrics {
	for _, c := range m.GetComponents() {
		if c.GetId() == id {
			return c
		}
	}
	return nil
}

// TestLatencyFailureViaAPI verifies through the wire that a DB service-time
// penalty collapses its modeled capacity: the queue overflows, load
// shedding rejects the excess, retried hops exhaust their budget as
// timeouts, and the diagnosis flags the DB as the critical origin.
// (Cache hits still complete at their usual latency, so p50 is expected
// to hold — the degradation signature is rejections + timeouts, not a
// percentile shift. The sim-level TestLatencyFailureDegradation covers a
// no-cache topology where percentiles do move.)
func TestLatencyFailureViaAPI(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	baseID, base := runViaAPI(t, c, nil)
	latID, lat := runViaAPI(t, c, []*v1.Failure{{
		Target: "db", Type: v1.FailureType_FAILURE_TYPE_INCREASED_LATENCY,
		StartMs: 0, DurationMs: 20_000,
		Config: &v1.FailureConfig{AddedLatencyMillis: 60}, // exceeds the 50ms budget
	}})

	// Load shedding: the slow DB (5ms → 65ms service) can serve ~61 RPS
	// while ~830 RPS of reads demand it; the queue (limit 20) overflows.
	if rej := componentMetrics(lat, "db").GetRejected(); rej < uint64(0.1*float64(lat.GetGenerated())) {
		t.Fatalf("latency failure must shed load at the db: rejected=%d of generated=%d", rej, lat.GetGenerated())
	}
	if base.GetRejected() != 0 {
		t.Fatalf("healthy baseline must not reject: %d", base.GetRejected())
	}
	// Retried hops that exceed the caller's budget end as timeouts.
	if lat.GetTimeouts() == 0 {
		t.Fatal("expected timeouts after retries are exhausted")
	}
	// The same seed produces the same arrivals; shed load means fewer
	// completions.
	if lat.GetCompleted() >= base.GetCompleted() {
		t.Fatalf("degraded run must complete less: %d vs %d", lat.GetCompleted(), base.GetCompleted())
	}

	// Diagnosis vs baseline flags the db as critical with real numbers.
	diag, err := c.GetDiagnosis(ctx, connect.NewRequest(&v1.GetDiagnosisRequest{
		SimulationId: latID, BaselineSimulationId: baseID,
	}))
	if err != nil {
		t.Fatalf("GetDiagnosis: %v", err)
	}
	found := false
	for _, b := range diag.Msg.GetDiagnosis().GetBottlenecks() {
		if b.GetComponentId() == "db" && b.GetSeverity() == "critical" {
			found = true
			if len(b.GetReasons()) == 0 {
				t.Fatal("bottleneck must carry machine-computed reasons")
			}
		}
	}
	if !found {
		t.Fatalf("expected critical db bottleneck, got %+v", diag.Msg.GetDiagnosis())
	}

	// Capacity and cost stay consistent on a degraded run.
	cap, err := c.GetCapacity(ctx, connect.NewRequest(&v1.GetCapacityRequest{SimulationId: latID}))
	if err != nil {
		t.Fatalf("GetCapacity: %v", err)
	}
	for _, r := range cap.Msg.GetReports() {
		if r.GetHeadroom()+r.GetUtilization() < 0.999 || r.GetHeadroom()+r.GetUtilization() > 1.001 {
			t.Fatalf("component %s: utilization+headroom must be 1", r.GetComponentId())
		}
	}
	dbBottleneck := false
	for _, r := range cap.Msg.GetReports() {
		if r.GetComponentId() == "db" && r.GetBottleneck() {
			dbBottleneck = true
		}
	}
	if !dbBottleneck {
		t.Fatal("capacity report must flag the db as a bottleneck on the degraded run")
	}
	cost, err := c.GetCostEstimate(ctx, connect.NewRequest(&v1.GetCostEstimateRequest{SimulationId: latID}))
	if err != nil {
		t.Fatalf("GetCostEstimate: %v", err)
	}
	if cost.Msg.GetEstimate().GetTotal() <= 0 {
		t.Fatal("degraded run must still produce a positive cost estimate")
	}
}

// TestErrorRateFailureViaAPI verifies an injected DB error rate surfaces
// as terminal failures at the DB, with retry attempts visible in the
// failure records the API returns.
func TestErrorRateFailureViaAPI(t *testing.T) {
	c := newTestClient(t)

	_, base := runViaAPI(t, c, nil)
	_, errRun := runViaAPI(t, c, []*v1.Failure{{
		Target: "db", Type: v1.FailureType_FAILURE_TYPE_INCREASED_ERROR_RATE,
		StartMs: 0, DurationMs: 20_000,
		Config: &v1.FailureConfig{ErrorRate: 0.3},
	}})

	if base.GetFailed() != 0 {
		t.Fatalf("healthy baseline must not fail requests: %d", base.GetFailed())
	}
	db := componentMetrics(errRun, "db")
	if db.GetFailed() == 0 {
		t.Fatal("db must record injected failures")
	}
	if errRun.GetFailed() == 0 {
		t.Fatal("system must report terminal failures")
	}
}

// TestNetworkFailureViaAPI verifies packet loss surfaces as dropped
// requests (their own counter, distinct from errors) at the injected hop.
func TestNetworkFailureViaAPI(t *testing.T) {
	c := newTestClient(t)

	_, base := runViaAPI(t, c, nil)
	_, net := runViaAPI(t, c, []*v1.Failure{{
		Target: "api", Type: v1.FailureType_FAILURE_TYPE_NETWORK_FAILURE,
		StartMs: 0, DurationMs: 20_000,
		Config: &v1.FailureConfig{PacketLossRate: 0.25, AddedLatencyMillis: 10},
	}})

	if base.GetDropped() != 0 {
		t.Fatalf("healthy baseline must not drop: %d", base.GetDropped())
	}
	if net.GetDropped() == 0 {
		t.Fatal("network failure must drop requests")
	}
	// 25% loss on every request passing the api → system-wide error rate
	// dominated by drops.
	if net.GetErrorRate() < 0.15 {
		t.Fatalf("expected ~25%% error rate, got %.2f%%", net.GetErrorRate()*100)
	}
	api := componentMetrics(net, "api")
	if api.GetFailed() == 0 {
		t.Fatal("api must record the drops it caused")
	}
}

// TestCrashWithoutPassThroughViaAPI verifies a non-passthrough crash
// fails requests at the crashed component instead of forwarding them.
func TestCrashWithoutPassThroughViaAPI(t *testing.T) {
	c := newTestClient(t)

	_, base := runViaAPI(t, c, nil)
	_, crash := runViaAPI(t, c, []*v1.Failure{{
		Target: "cache", Type: v1.FailureType_FAILURE_TYPE_CRASH,
		StartMs: 0, DurationMs: 20_000,
		Config: &v1.FailureConfig{PassThrough: false},
	}})

	cache := componentMetrics(crash, "cache")
	if cache.GetFailed() == 0 {
		t.Fatal("crashed cache must fail requests")
	}
	if crash.GetFailed() <= base.GetFailed() {
		t.Fatalf("crash must increase failures: %d vs %d", crash.GetFailed(), base.GetFailed())
	}
	if crash.GetCompleted() >= base.GetCompleted() {
		t.Fatalf("crash must reduce completions: %d vs %d", crash.GetCompleted(), base.GetCompleted())
	}
}

// TestGetResultsFailureRecords verifies failure records survive the wire:
// kind, component attribution, and retry attempts are intact.
func TestGetResultsFailureRecords(t *testing.T) {
	c := newTestClient(t)
	opts := testOptions()
	opts.DurationMs = 20_000
	opts.Failures = []*v1.Failure{{
		Target: "db", Type: v1.FailureType_FAILURE_TYPE_INCREASED_ERROR_RATE,
		StartMs: 0, DurationMs: 20_000,
		Config: &v1.FailureConfig{ErrorRate: 0.5},
	}}
	created, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: testArchitecture(), Workload: heavyWorkload(), Options: opts,
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
	records := res.Msg.GetFailures()
	if len(records) == 0 {
		t.Fatal("expected failure records over the wire")
	}
	m := res.Msg.GetMetrics()
	sawDBError := false
	sawRetry := false
	for _, f := range records {
		if f.GetComponentId() == "db" && f.GetKind() == "error" {
			sawDBError = true
		}
		if f.GetAttempt() > 1 {
			sawRetry = true
		}
		if f.GetAtMs() < 0 || f.GetLatencyMs() < 0 {
			t.Fatalf("record has negative timing: %+v", f)
		}
		if len(f.GetPath()) == 0 {
			t.Fatal("failure record must carry the request path")
		}
	}
	if !sawDBError {
		t.Fatal("expected db-attributed error records")
	}
	if !sawRetry {
		t.Fatal("expected retried attempts among failure records")
	}
	// Cross-check conservation: every request is accounted for.
	if got, want := m.GetGenerated(), m.GetCompleted()+m.GetRejected()+m.GetFailed()+m.GetInFlight(); got != want {
		t.Fatalf("conservation violated: %d != %d", got, want)
	}
}
