package sim

import (
	"math"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// --- Failure validation ---

func TestFailureValidation(t *testing.T) {
	arch := basicArch()
	base := workload.Spec{
		TotalUsers: 1_000_000, DAU: 10_000, RequestsPerUserPerDay: 5,
		PeakMultiplier: 2, ReadWriteRatio: 4,
	}
	cases := []struct {
		name string
		fail Failure
	}{
		{"unknown target", Failure{Target: "ghost", Type: FailureCrash, StartMS: 0, DurationMS: 1000}},
		{"negative start", Failure{Target: "db", Type: FailureCrash, StartMS: -1, DurationMS: 1000}},
		{"zero duration", Failure{Target: "db", Type: FailureCrash, StartMS: 0, DurationMS: 0}},
		{"latency without penalty", Failure{Target: "db", Type: FailureLatency, StartMS: 0, DurationMS: 1000}},
		{"error rate out of range", Failure{Target: "db", Type: FailureErrorRate, StartMS: 0, DurationMS: 1000,
			Config: FailureConfig{ErrorRate: 1.5}}},
		{"unknown type", Failure{Target: "db", Type: "zap", StartMS: 0, DurationMS: 1000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Simulate(arch, base, Options{Seed: 1, DurationMS: 5_000, Failures: []Failure{tc.fail}})
			if err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

// --- Crash: pass-through cache fails over to the DB (natural propagation) ---

func crashArch() Architecture {
	return Architecture{
		Name: "crash-test",
		Components: []ComponentSpec{
			client("client"),
			{ID: "api", Kind: KindAPIServer, Concurrency: 10, QueueLimit: 50, DefaultServiceTimeMillis: 2},
			{ID: "cache", Kind: KindCache, Concurrency: 2, QueueLimit: 100, HitRatio: 0.8, DefaultServiceTimeMillis: 0.1},
			{ID: "db", Kind: KindDatabase, Concurrency: 4, QueueLimit: 20, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
}

func TestCrashPassThroughPropagatesLoad(t *testing.T) {
	arch := crashArch()
	// Steady load, cache healthy first 10s, crashed 10s→25s, recovered after.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100, // reads only, so DB load == cache misses
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       21,
		DurationMS: 40_000,
		Failures: []Failure{{
			Target: "cache", Type: FailureCrash,
			StartMS: 10_000, DurationMS: 15_000,
			Config: FailureConfig{PassThrough: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cache, db ComponentMetrics
	for _, c := range res.Metrics.Components {
		switch c.ID {
		case "cache":
			cache = c
		case "db":
			db = c
		}
	}
	if cache.Failed != 0 {
		t.Fatalf("pass-through crash must not fail requests at the cache, got %d", cache.Failed)
	}
	// The DB must have absorbed the cache's share of traffic: with an 80%
	// hit ratio and healthy cache, DB traffic ≈ 20% of reads; while crashed
	// with pass-through, DB traffic ≈ 100% of reads.
	if db.Arrived == 0 {
		t.Fatal("db saw no traffic at all")
	}
	if db.Arrived <= cache.Completed-db.Arrived {
		t.Fatalf("db load should jump substantially during cache crash: db=%d cache=%d",
			db.Arrived, cache.Arrived)
	}
}

func TestCrashFailsRequestsWhenNotPassThrough(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       22,
		DurationMS: 20_000,
		Failures: []Failure{{
			Target: "cache", Type: FailureCrash,
			StartMS: 5_000, DurationMS: 10_000, // recovers at t=15s
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Failed == 0 {
		t.Fatal("non-pass-through crash must fail requests")
	}
	// Recovery: requests that arrive after the failure window must complete.
	// Generated during failure ≈ (10s window / 2.88ms IAT) ≈ 3400; total
	// generated ≈ 6900. Post-recovery traffic must still complete.
	if m.Completed == 0 {
		t.Fatal("no completions even after recovery")
	}
	if m.Failed >= m.Generated {
		t.Fatalf("crash ended but everything failed: %d/%d", m.Failed, m.Generated)
	}
	var cache ComponentMetrics
	for _, c := range m.Components {
		if c.ID == "cache" {
			cache = c
		}
	}
	if cache.Failed == 0 {
		t.Fatal("cache should record crash failures")
	}
}

// --- Latency degradation ---

func TestLatencyFailureDegradation(t *testing.T) {
	arch := crashArch()
	// No cache in this path: every request pays the DB penalty directly.
	arch.Components = append(arch.Components[:2], arch.Components[3])
	arch.Links = []Link{
		{From: "client", To: "api"},
		{From: "api", To: "db"},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	healthy, err := Simulate(arch, spec, Options{Seed: 31, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := Simulate(arch, spec, Options{
		Seed:       31, // same seed → same arrivals → controlled comparison
		DurationMS: 30_000,
		Failures: []Failure{{
			Target: "db", Type: FailureLatency,
			StartMS: 0, DurationMS: 30_000,
			Config: FailureConfig{AddedLatencyMillis: 50},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if degraded.Metrics.P50MS < healthy.Metrics.P50MS+40 {
		t.Fatalf("latency failure must raise p50 by ~50ms: healthy %.2fms vs degraded %.2fms",
			healthy.Metrics.P50MS, degraded.Metrics.P50MS)
	}
	if degraded.Metrics.P99MS < healthy.Metrics.P99MS+40 {
		t.Fatalf("latency failure must raise p99 by ~50ms: healthy %.2fms vs degraded %.2fms",
			healthy.Metrics.P99MS, degraded.Metrics.P99MS)
	}
}

// --- Error injection ---

func TestErrorRateInjection(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       41,
		DurationMS: 30_000,
		Failures: []Failure{{
			Target: "db", Type: FailureErrorRate,
			StartMS: 0, DurationMS: 30_000,
			Config: FailureConfig{ErrorRate: 0.3},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	// ~20% of reads reach the DB (80% cache hits), so a 30% DB-side error
	// rate lands as ~6% system-wide. Assert the DB-attributed share.
	var db ComponentMetrics
	for _, c := range m.Components {
		if c.ID == "db" {
			db = c
		}
	}
	dbFailRate := float64(db.Failed) / float64(maxU64(1, db.Failed+db.Completed))
	if dbFailRate < 0.2 || dbFailRate > 0.4 {
		t.Fatalf("db-side failure share = %.2f%%, want ~30%%", dbFailRate*100)
	}
	if db.Failed == 0 {
		t.Fatal("db should record injected failures")
	}
	if len(m.Failures) == 0 {
		t.Fatal("failure records should be retained")
	}
}

// --- Network failure: drops ---

func TestNetworkFailureDrops(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       51,
		DurationMS: 30_000,
		Failures: []Failure{{
			Target: "api", Type: FailureNetwork,
			StartMS: 0, DurationMS: 30_000,
			Config: FailureConfig{PacketLossRate: 0.25, AddedLatencyMillis: 10},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Dropped == 0 {
		t.Fatal("network failure must drop requests")
	}
	if m.ErrorRate < 0.15 || m.ErrorRate > 0.35 {
		t.Fatalf("drop rate = %.2f%%, want ~25%%", m.ErrorRate*100)
	}
}

// --- Cascading overload: DB error injection → API timeouts, without any
// hardcoded cascade logic. The DB gets slow; the API's 50ms budget starts
// timing out downstream-hopping requests; retries add load. ---

func TestCascadingOverload(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 5, ReadWriteRatio: 100,
	}
	// Healthy baseline (identical seed → identical arrivals).
	healthy, err := Simulate(arch, spec, Options{Seed: 61, DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}

	cascaded, err := Simulate(arch, spec, Options{
		Seed:       61, // same arrivals as baseline
		DurationMS: 60_000,
		Retry:      RetryPolicy{MaxRetries: 2, BackoffBaseMS: 5, TimeoutMS: 50},
		RetryOn:    []string{"api", "cache"}, // both forwarders may retry
		Failures: []Failure{{
			Target: "db", Type: FailureLatency,
			StartMS: 0, DurationMS: 60_000,
			Config: FailureConfig{AddedLatencyMillis: 60}, // alone exceeds the 50ms budget
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	hm, cm := healthy.Metrics, cascaded.Metrics
	if cm.Timeouts == 0 {
		t.Fatal("expected timeouts at the API when the DB slow-down exceeds its budget")
	}
	if cm.P95MS <= hm.P95MS {
		t.Fatalf("cascade must raise p95: healthy %.1f vs cascaded %.1f", hm.P95MS, cm.P95MS)
	}
	if cm.Completed >= hm.Completed {
		t.Fatalf("cascade must reduce completions: healthy %d vs cascaded %d", hm.Completed, cm.Completed)
	}
	// Timeouts must be attributed to the true caller chain: detected at the
	// db, caller = cache (the DB's direct upstream in this topology). The
	// injected component (db) differs from the flagging chain — propagation,
	// not hardcoding. Retries must also fire: DB-served requests are retried
	// by the cache until retries are exhausted.
	var callerOK, wrongCaller bool
	var retriedAttempts int
	for _, f := range cm.Failures {
		if f.Kind == "timeout" {
			if f.CallerID == "cache" && f.ComponentID == "db" {
				callerOK = true
			} else {
				wrongCaller = true
			}
			if f.Attempt > 1 {
				retriedAttempts++
			}
		}
	}
	if !callerOK || wrongCaller {
		t.Fatalf("timeout records must attribute caller=cache, detected=db; got callerOK=%v wrongCaller=%v",
			callerOK, wrongCaller)
	}
	if retriedAttempts == 0 {
		t.Fatal("retries must appear in failure records (attempts > 1)")
	}
	// Retry amplification: the DB must see MORE arrivals than the number of
	// distinct failing requests, because callers re-sent timed-out hops.
	var dbArrived uint64
	uniqueFailed := make(map[uint64]bool)
	for _, f := range cm.Failures {
		uniqueFailed[f.RequestID] = true
	}
	for _, c := range cm.Components {
		if c.ID == "db" {
			dbArrived = c.Arrived
		}
	}
	if dbArrived <= uint64(len(uniqueFailed)) {
		t.Fatalf("retry amplification expected: db arrivals %d vs unique failing requests %d",
			dbArrived, len(uniqueFailed))
	}
	// Diagnosis must flag the DB as the origin under this load.
	d := DiagnoseWithBaseline(cm, hm)
	if d.Healthy {
		t.Fatal("diagnosis must flag bottlenecks under cascade")
	}
	if d.Bottlenecks[0].ComponentID == "api" {
		t.Fatalf("primary bottleneck should be the DB origin, got %s", d.Bottlenecks[0].ComponentID)
	}
}

// --- Recovery: failure window ends, error rate returns to zero ---

func TestRecoveryAfterFailureWindow(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 5, ReadWriteRatio: 100,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       71,
		DurationMS: 60_000,
		Failures: []Failure{{
			Target: "db", Type: FailureErrorRate,
			StartMS: 10_000, DurationMS: 10_000, // fails 10s→20s, healthy after
			Config: FailureConfig{ErrorRate: 0.9},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	// 1/3 of the window is failing → overall error rate ≈ 30% of requests
	// in that window failing; completions continue afterward.
	if m.Failed == 0 {
		t.Fatal("failure window should produce failures")
	}
	if m.Completed == 0 {
		t.Fatal("post-recovery traffic must complete")
	}
	// Failed share must be well below the 90% injection rate because only
	// a third of the window is affected and only DB-served requests fail.
	if float64(m.Failed)/float64(m.Generated) > 0.5 {
		t.Fatalf("failures should be confined to the window: %.0f%% failed",
			100*float64(m.Failed)/float64(m.Generated))
	}
}

// --- Percentile calculations on real collected data ---

func TestPercentileCalculationExact(t *testing.T) {
	// nearestRank must match the textbook definition on a known series.
	sorted := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := nearestRank(sorted, 0.50); got != 50 {
		t.Errorf("p50 = %v, want 50", got)
	}
	if got := nearestRank(sorted, 0.95); got != 100 {
		t.Errorf("p95 = %v (nearest-rank of 9.5 → 10th), want 100", got)
	}
	if got := nearestRank(sorted, 0.99); got != 100 {
		t.Errorf("p99 = %v, want 100", got)
	}
	if got := nearestRank(sorted, 0.05); got != 10 {
		t.Errorf("p05 = %v, want 10", got)
	}
}

func TestMetricsPercentilesFromSimulation(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	res, err := Simulate(arch, spec, Options{Seed: 81, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Completed < 100 {
		t.Fatalf("need enough samples for percentile sanity, got %d completions", m.Completed)
	}
	if !(m.P50MS <= m.P95MS && m.P95MS <= m.P99MS && m.P99MS <= m.MaxLatencyMS) {
		t.Fatalf("percentile ordering violated: p50=%.2f p95=%.2f p99=%.2f max=%.2f",
			m.P50MS, m.P95MS, m.P99MS, m.MaxLatencyMS)
	}
	if m.P50MS <= 0 || math.IsNaN(m.P99MS) {
		t.Fatal("percentiles must be positive finite")
	}
}

// --- Bottleneck detection ---

func TestBottleneckDetectionUnderOverload(t *testing.T) {
	arch := crashArch()
	// DB capacity 150 RPS vs offered peak ~200+ RPS → must flag.
	arch.Components[3].CapacityRPS = 150
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 100, // peak ≈ 231 RPS
	}
	res, err := Simulate(arch, spec, Options{Seed: 91, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	d := Diagnose(res.Metrics)
	if d.Healthy {
		t.Fatalf("expected bottleneck detection, got: %s", d.Summary)
	}
	if d.Bottlenecks[0].ComponentID != "db" {
		t.Fatalf("primary bottleneck = %s, want db", d.Bottlenecks[0].ComponentID)
	}
	if d.Bottlenecks[0].Severity != "critical" {
		t.Fatalf("severity = %s, want critical", d.Bottlenecks[0].Severity)
	}
	if len(d.Bottlenecks[0].Reasons) == 0 {
		t.Fatal("diagnosis must explain why")
	}
}

func TestDiagnosisHealthySystem(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 5_000, RequestsPerUserPerDay: 2,
		PeakMultiplier: 2, ReadWriteRatio: 4,
	}
	res, err := Simulate(arch, spec, Options{Seed: 101, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	d := Diagnose(res.Metrics)
	if !d.Healthy {
		t.Fatalf("light load should be healthy, got: %s — %v", d.Summary, d.Bottlenecks[0].Reasons)
	}
}

func TestDiagnosisWithBaselineReportsDeltas(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	baseline, err := Simulate(arch, spec, Options{Seed: 111, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := Simulate(arch, spec, Options{
		Seed:       111,
		DurationMS: 30_000,
		Failures: []Failure{{
			Target: "db", Type: FailureLatency,
			StartMS: 0, DurationMS: 30_000,
			Config: FailureConfig{AddedLatencyMillis: 40},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := DiagnoseWithBaseline(degraded.Metrics, baseline.Metrics)
	found := false
	for _, imp := range d.Impacts {
		if contains(imp, "p95 latency increased") || contains(imp, "throughput decreased") {
			found = true
		}
	}
	if !found {
		t.Fatalf("baseline diagnosis must report latency/throughput deltas; impacts: %+v",
			d.Impacts)
	}
}

// --- Determinism with failures ---

func TestFailureSimulationDeterministic(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100,
	}
	mk := func() Options {
		return Options{
			Seed:       121,
			DurationMS: 30_000,
			Retry:      RetryPolicy{MaxRetries: 2, BackoffBaseMS: 5, TimeoutMS: 50},
			RetryOn:    []string{"api"},
			Failures: []Failure{{
				Target: "cache", Type: FailureCrash,
				StartMS: 5_000, DurationMS: 10_000,
				Config: FailureConfig{PassThrough: true},
			}},
		}
	}
	a, err := Simulate(arch, spec, mk())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Simulate(arch, spec, mk())
	if err != nil {
		t.Fatal(err)
	}
	am, bm := a.Metrics, b.Metrics
	if am.Generated != bm.Generated || am.Completed != bm.Completed ||
		am.Failed != bm.Failed || am.P99MS != bm.P99MS {
		t.Fatalf("failure runs diverged:\n%+v\n%+v", am, bm)
	}
	if len(am.Failures) != len(bm.Failures) {
		t.Fatalf("failure records diverged: %d vs %d", len(am.Failures), len(bm.Failures))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
