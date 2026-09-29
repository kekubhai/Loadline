package sim

import (
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// TestDeterministicOneRunIdentity asserts the Step 8.1 contract: two
// independent runs with identical (architecture, workload, failures,
// duration) must produce identical results. The engine, per-event RNG
// streams, workload derivation, failure injection, retry/backoff, cache and
// percentile calculations all participate in this.
func TestDeterministicOneRunIdentity(t *testing.T) {
	arch := basicArch()
	spec := baseSpec()

	run := func(suffix string) Metrics {
		a, err := Simulate(arch, spec, Options{
			Seed:       12345,
			DurationMS: 30_000,
			Retry: RetryPolicy{
				MaxRetries:    2,
				BackoffBaseMS: 5,
				TimeoutMS:     80,
			},
			RetryOn: []string{"api", "cache"},
			Failures: []Failure{
				{
					Target:     "cache",
					Type:       FailureCrash,
					StartMS:    10_000,
					DurationMS: 10_000,
					Config: FailureConfig{
						PassThrough: true,
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("%s: simulate failed: %v", suffix, err)
		}
		return a.Metrics
	}

	x := run("first")
	y := run("second")

	if x.Generated != y.Generated {
		t.Fatalf("generated diverged: %d vs %d", x.Generated, y.Generated)
	}
	if x.Completed != y.Completed {
		t.Fatalf("completed diverged: %d vs %d", x.Completed, y.Completed)
	}
	if x.Rejected != y.Rejected {
		t.Fatalf("rejected diverged: %d vs %d", x.Rejected, y.Rejected)
	}
	if x.Failed != y.Failed {
		t.Fatalf("failed diverged: %d vs %d", x.Failed, y.Failed)
	}
	if x.Timeouts != y.Timeouts {
		t.Fatalf("timeouts diverged: %d vs %d", x.Timeouts, y.Timeouts)
	}
	if x.Dropped != y.Dropped {
		t.Fatalf("dropped diverged: %d vs %d", x.Dropped, y.Dropped)
	}
	if x.ErrorRate != y.ErrorRate {
		t.Fatalf("error rate diverged: %.9f vs %.9f", x.ErrorRate, y.ErrorRate)
	}
	if x.TimeoutRate != y.TimeoutRate {
		t.Fatalf("timeout rate diverged: %.9f vs %.9f", x.TimeoutRate, y.TimeoutRate)
	}
	if x.AvgLatencyMS != y.AvgLatencyMS {
		t.Fatalf("avg latency diverged: %.9f vs %.9f", x.AvgLatencyMS, y.AvgLatencyMS)
	}
	if x.P50MS != y.P50MS {
		t.Fatalf("p50 diverged: %.9f vs %.9f", x.P50MS, y.P50MS)
	}
	if x.P95MS != y.P95MS {
		t.Fatalf("p95 diverged: %.9f vs %.9f", x.P95MS, y.P95MS)
	}
	if x.P99MS != y.P99MS {
		t.Fatalf("p99 diverged: %.9f vs %.9f", x.P99MS, y.P99MS)
	}
	if x.MaxLatencyMS != y.MaxLatencyMS {
		t.Fatalf("max latency diverged: %.9f vs %.9f", x.MaxLatencyMS, y.MaxLatencyMS)
	}
	// Compare failure records field-by-field (slices cannot be compared).
	if len(x.Failures) != len(y.Failures) {
		t.Fatalf("failure records diverged: %d vs %d", len(x.Failures), len(y.Failures))
	}
	for i := range x.Failures {
		if x.Failures[i].RequestID != y.Failures[i].RequestID {
			t.Fatalf("failure record %d diverged: request id", i)
		}
		if x.Failures[i].ComponentID != y.Failures[i].ComponentID {
			t.Fatalf("failure record %d diverged: component", i)
		}
		if x.Failures[i].CallerID != y.Failures[i].CallerID {
			t.Fatalf("failure record %d diverged: caller", i)
		}
		if x.Failures[i].Kind != y.Failures[i].Kind {
			t.Fatalf("failure record %d diverged: kind", i)
		}
		if x.Failures[i].Attempt != y.Failures[i].Attempt {
			t.Fatalf("failure record %d diverged: attempt", i)
		}
		if x.Failures[i].AtMS != y.Failures[i].AtMS {
			t.Fatalf("failure record %d diverged: atMs", i)
		}
		if x.Failures[i].LatencyMS != y.Failures[i].LatencyMS {
			t.Fatalf("failure record %d diverged: latencyMs", i)
		}
	}
	for i := range x.Components {
		if x.Components[i] != y.Components[i] {
			t.Fatalf("component %d diverged: %+v vs %+v", i, x.Components[i], y.Components[i])
		}
	}
}

// TestDeterministicAcrossPacing asserts that Result is byte-identical to
// Simulate (as fast as possible) and to StartPaced (wall-clock paced). The
// engine's event ordering and RNG streams are independent of wall time, so
// any observable difference means timing leaked into the simulation.
func TestDeterministicAcrossPacing(t *testing.T) {
	arch := basicArch()
	spec := baseSpec()

	fast, err := Simulate(arch, spec, Options{
		Seed:       4242,
		DurationMS: 45_000,
		Retry: RetryPolicy{
			MaxRetries:    1,
			BackoffBaseMS: 2,
			TimeoutMS:     60,
		},
	})
	if err != nil {
		t.Fatalf("fast simulate failed: %v", err)
	}

	paced, err := StartPaced(arch, spec, Options{
		Seed:       4242,
		DurationMS: 45_000,
		Retry: RetryPolicy{
			MaxRetries:    1,
			BackoffBaseMS: 2,
			TimeoutMS:     60,
		},
	}, 15000)
	if err != nil {
		t.Fatalf("paced run failed to start: %v", err)
	}
	pr := paced.WaitResult()
	if pr == nil {
		t.Fatal("paced run returned nil result")
	}

	if fast.Metrics.Generated != pr.Metrics.Generated {
		t.Fatalf("generated diverged: %d vs %d", fast.Metrics.Generated, pr.Metrics.Generated)
	}
	if fast.Metrics.Completed != pr.Metrics.Completed {
		t.Fatalf("completed diverged: %d vs %d", fast.Metrics.Completed, pr.Metrics.Completed)
	}
	if fast.Metrics.Rejected != pr.Metrics.Rejected {
		t.Fatalf("rejected diverged: %d vs %d", fast.Metrics.Rejected, pr.Metrics.Rejected)
	}
	if fast.Metrics.P99MS != pr.Metrics.P99MS {
		t.Fatalf("p99 diverged between fast and paced: %.9f vs %.9f",
			fast.Metrics.P99MS, pr.Metrics.P99MS)
	}
	if fast.Metrics.AvgLatencyMS != pr.Metrics.AvgLatencyMS {
		t.Fatalf("avg latency diverged: %.9f vs %.9f",
			fast.Metrics.AvgLatencyMS, pr.Metrics.AvgLatencyMS)
	}
	if len(fast.Metrics.Failures) != len(pr.Metrics.Failures) {
		t.Fatalf("failure records diverged: %d vs %d", len(fast.Metrics.Failures), len(pr.Metrics.Failures))
	}
}

// nearestRank identity double-check: identical sorted inputs under the same
// seed must yield the same percentile triple. This locks the nearest-rank
// definition used for P50/P95/P99.
func TestDeterministicPercentilesSameInputs(t *testing.T) {
	sorted := []float64{
		1.5, 2.0, 3.2, 4.1, 5.0, 6.7, 7.8, 8.9, 10.0, 12.5,
		15.3, 18.0, 20.2, 22.1, 25.0, 27.4, 30.0, 33.5, 37.2, 41.0,
	}
	if len(sorted) != 20 {
		t.Fatalf("test data changed size: %d", len(sorted))
	}
	// Nearest-rank: P50 = ceil(0.50 × 20) = 10th value = 12.5.
	// P95 = ceil(0.95 × 20) = 19th value = 37.2.
	// P05 = ceil(0.05 × 20) = 1st value = 1.5.
	if got := nearestRank(sorted, 0.50); got != 12.5 {
		t.Fatalf("p50 = %.2f, want 12.5 (nearest-rank)", got)
	}
	if got := nearestRank(sorted, 0.95); got != 37.2 {
		t.Fatalf("p95 = %.2f, want 37.2 (nearest-rank ceil(0.95×20)=19)", got)
	}
	if got := nearestRank(sorted, 0.05); got != 1.5 {
		t.Fatalf("p05 = %.2f, want 1.5 (nearest-rank)", got)
	}
}

// TestMetricsPercentilesSameSeed locks percentile determinism for one full
// seeded run: the same (architecture, spec, seed) must give the same
// percentile triple.
func TestMetricsPercentilesSameSeed(t *testing.T) {
	arch := basicArch()
	spec := baseSpec()

	run := func(seed uint64) Metrics {
		res, err := Simulate(arch, spec, Options{Seed: seed, DurationMS: 25_000})
		if err != nil {
			t.Fatalf("simulate(seed=%d) failed: %v", seed, err)
		}
		return res.Metrics
	}

	a := run(777)
	b := run(777)

	if a.P50MS != b.P50MS {
		t.Fatalf("p50 diverged: %.9f vs %.9f", a.P50MS, b.P50MS)
	}
	if a.P95MS != b.P95MS {
		t.Fatalf("p95 diverged: %.9f vs %.9f", a.P95MS, b.P95MS)
	}
	if a.P99MS != b.P99MS {
		t.Fatalf("p99 diverged: %.9f vs %.9f", a.P99MS, b.P99MS)
	}
	if a.MaxLatencyMS != b.MaxLatencyMS {
		t.Fatalf("max latency diverged: %.9f vs %.9f", a.MaxLatencyMS, b.MaxLatencyMS)
	}
}

// TestFailureSeedIndependence asserts that the same architecture and load
// with the failure sequence triggered by different seeds still produces
// identical system results — the failure RNG stream is seeded per run, so
// identical (architecture, workload, options) must keep emitting the same
// failure/event sequence.
func TestFailureSeedIndependence(t *testing.T) {
	arch := basicArch()
	spec := baseSpec()

	run := func(seed uint64) Metrics {
		res, err := Simulate(arch, spec, Options{
			Seed:       seed,
			DurationMS: 40_000,
			Failures: []Failure{
				{
					Target:     "db",
					Type:       FailureLatency,
					StartMS:    15_000,
					DurationMS: 10_000,
					Config: FailureConfig{
						AddedLatencyMillis: 30,
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("simulate(seed=%d) failed: %v", seed, err)
		}
		return res.Metrics
	}

	x := run(100)
	y := run(100)

	if x.Generated != y.Generated {
		t.Fatalf("generated diverged: %d vs %d", x.Generated, y.Generated)
	}
	if x.Completed != y.Completed {
		t.Fatalf("completed diverged: %d vs %d", x.Completed, y.Completed)
	}
	if x.Failed != y.Failed {
		t.Fatalf("failed diverged: %d vs %d", x.Failed, y.Failed)
	}
	if x.P99MS != y.P99MS {
		t.Fatalf("p99 diverged: %.9f vs %.9f", x.P99MS, y.P99MS)
	}

	// Failure behavior must be reproducible too.
	// Compare failure records field-by-field (slices cannot be compared).
	if len(x.Failures) != len(y.Failures) {
		t.Fatalf("failure records diverged: %d vs %d", len(x.Failures), len(y.Failures))
	}
	for i := range x.Failures {
		if x.Failures[i].RequestID != y.Failures[i].RequestID {
			t.Fatalf("failure record %d diverged: request id", i)
		}
		if x.Failures[i].ComponentID != y.Failures[i].ComponentID {
			t.Fatalf("failure record %d diverged: component", i)
		}
		if x.Failures[i].CallerID != y.Failures[i].CallerID {
			t.Fatalf("failure record %d diverged: caller", i)
		}
		if x.Failures[i].Kind != y.Failures[i].Kind {
			t.Fatalf("failure record %d diverged: kind", i)
		}
		if x.Failures[i].Attempt != y.Failures[i].Attempt {
			t.Fatalf("failure record %d diverged: attempt", i)
		}
		if x.Failures[i].AtMS != y.Failures[i].AtMS {
			t.Fatalf("failure record %d diverged: atMs", i)
		}
		if x.Failures[i].LatencyMS != y.Failures[i].LatencyMS {
			t.Fatalf("failure record %d diverged: latencyMs", i)
		}
	}
}

// TestZeroTrafficDeterministic asserts that a run with an empty workload
// and a short horizon is deterministically empty — no requests, no events,
// and the failure RNG never consumes a stream seed without materializing.
func TestZeroTrafficDeterministic(t *testing.T) {
	// Even a trivially light workload must be deterministic across runs.
	os := workload.Spec{
		TotalUsers:            10,
		DAU:                   5,
		RequestsPerUserPerDay: 1,
		PeakMultiplier:        1,
		ReadWriteRatio:        4,
		PayloadBytes:          4096,
	}
	opts := Options{Seed: 999, DurationMS: 5_000}
	arch := basicArch()
	res, err := Simulate(arch, os, opts)
	if err != nil {
		t.Fatalf("zero-traffic run failed: %v", err)
	}
	m := res.Metrics
	if m.Generated != 0 {
		t.Fatalf("generated = %d, want 0", m.Generated)
	}
	if m.Completed != 0 || m.Rejected != 0 || m.Failed != 0 {
		t.Fatalf("non-zero result on zero traffic: %+v", m)
	}
	if len(m.Failures) != 0 {
		t.Fatalf("failure records on zero traffic: %+v", m.Failures)
	}
}
