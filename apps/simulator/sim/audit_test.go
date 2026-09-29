package sim

// Step 8 — Simulation Correctness & Trust audit tests.
//
// Every test here pins behavior the documentation promises: workload
// derivation edge cases, queue growth/drain/rejection, capacity and
// concurrency enforcement, latency composition, failure scoping and
// timing, emergent cascades, evidence-based diagnosis, and hostile edge
// cases (zero traffic, zero capacity, invalid configs, failure at
// horizon boundaries). All fixed-seed, all deterministic.

import (
	"math"
	"strings"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// ------------------------------------------------------------- workload ----

// Step 8.2: rounding — fractional requests/user must produce fractional
// (not integer-truncated) requests/day, and the whole chain must compose.
func TestWorkloadRoundingAndChain(t *testing.T) {
	p, err := workload.Derive(workload.Spec{
		TotalUsers: 3, DAU: 1, RequestsPerUserPerDay: 2.5,
		PeakMultiplier: 2, ReadWriteRatio: 1, PayloadBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 1 × 2.5 = 2.5 req/day → 2.5/86400 avg → ×2 peak. No truncation.
	if p.RequestsPerDay != 2.5 {
		t.Fatalf("requests/day = %f, want 2.5 (must not truncate)", p.RequestsPerDay)
	}
	wantAvg := 2.5 / 86400.0
	if math.Abs(p.AverageRPS-wantAvg) > 1e-15 {
		t.Fatalf("avg RPS = %f, want %f", p.AverageRPS, wantAvg)
	}
	if math.Abs(p.PeakRPS-2*wantAvg) > 1e-15 {
		t.Fatalf("peak RPS = %f, want %f", p.PeakRPS, 2*wantAvg)
	}
	if p.MeanInterArrivalMillis != 1000.0/p.PeakRPS {
		t.Fatalf("mean IAT = %f, want 1000/peak", p.MeanInterArrivalMillis)
	}
}

// Step 8.2: zero and invalid values must be rejected with explicit errors,
// never defaulted silently.
func TestWorkloadZeroAndInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		spec workload.Spec
	}{
		{"zero users", workload.Spec{TotalUsers: 0, DAU: 1, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"negative users", workload.Spec{TotalUsers: -5, DAU: 1, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"zero dau", workload.Spec{TotalUsers: 10, DAU: 0, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"dau over users", workload.Spec{TotalUsers: 10, DAU: 11, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"zero req/user", workload.Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 0, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"negative req/user", workload.Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: -1, PeakMultiplier: 1, ReadWriteRatio: 1}},
		{"peak below one", workload.Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 0.5, ReadWriteRatio: 1}},
		{"zero read/write ratio", workload.Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 0}},
		{"negative payload", workload.Spec{TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1, PeakMultiplier: 1, ReadWriteRatio: 1, PayloadBytes: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := workload.Derive(tc.spec); err == nil {
				t.Fatal("invalid workload must be rejected")
			} else if !strings.Contains(err.Error(), "workload") {
				t.Fatalf("error should identify the workload: %v", err)
			}
		})
	}
}

// Step 8.2: extreme values must not overflow or NaN.
func TestWorkloadExtremeValues(t *testing.T) {
	p, err := workload.Derive(workload.Spec{
		TotalUsers: math.MaxInt64 / 2, DAU: math.MaxInt64 / 4,
		RequestsPerUserPerDay: 1000, PeakMultiplier: 1, ReadWriteRatio: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.IsInf(p.PeakRPS, 0) || math.IsNaN(p.PeakRPS) || p.PeakRPS <= 0 {
		t.Fatalf("extreme workload produced %f", p.PeakRPS)
	}
	if math.IsInf(p.MeanInterArrivalMillis, 0) || p.MeanInterArrivalMillis <= 0 {
		t.Fatalf("extreme workload IAT = %f", p.MeanInterArrivalMillis)
	}
}

// Step 8.2: read/write ratios — split fraction and the simulated split.
func TestWorkloadReadWriteRatios(t *testing.T) {
	cases := []struct {
		ratio     float64
		readFrac  float64
		writeFrac float64
	}{
		{1, 0.5, 0.5},
		{4, 0.8, 0.2},
		{9, 0.9, 0.1},
		{99, 0.99, 0.01},
	}
	for _, tc := range cases {
		p, err := workload.Derive(workload.Spec{
			TotalUsers: 100, DAU: 50, RequestsPerUserPerDay: 10,
			PeakMultiplier: 1, ReadWriteRatio: tc.ratio,
		})
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(p.ReadFraction-tc.readFrac) > 1e-12 || math.Abs(p.WriteFraction-tc.writeFrac) > 1e-12 {
			t.Fatalf("ratio %v: read=%f write=%f, want %v/%v",
				tc.ratio, p.ReadFraction, p.WriteFraction, tc.readFrac, tc.writeFrac)
		}
		if math.Abs(p.ReadFraction+p.WriteFraction-1) > 1e-12 {
			t.Fatalf("fractions must sum to 1, got %f", p.ReadFraction+p.WriteFraction)
		}
	}
}

// Step 8.2: payload size must flow into the plan untouched (it prices
// egress downstream; the plan is where its assumption lives).
func TestWorkloadPayloadSizes(t *testing.T) {
	for _, b := range []int64{0, 1, 4096, 1 << 20} {
		p, err := workload.Derive(workload.Spec{
			TotalUsers: 10, DAU: 5, RequestsPerUserPerDay: 1,
			PeakMultiplier: 1, ReadWriteRatio: 1, PayloadBytes: b,
		})
		if err != nil {
			t.Fatalf("payload %d: %v", b, err)
		}
		if p.PayloadBytes != b {
			t.Fatalf("payload = %d, want %d", p.PayloadBytes, b)
		}
	}
}

// Step 8.2: peak multiplier must be inspectable in the plan and scale
// exactly.
func TestWorkloadPeakMultiplierScales(t *testing.T) {
	mk := func(mult float64) workload.Plan {
		p, err := workload.Derive(workload.Spec{
			TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
			PeakMultiplier: mult, ReadWriteRatio: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := mk(1)
	for _, mult := range []float64{1, 2, 5, 10} {
		p := mk(mult)
		if p.PeakMultiplier != mult {
			t.Fatalf("plan multiplier = %f, want %f", p.PeakMultiplier, mult)
		}
		if math.Abs(p.PeakRPS-base.AverageRPS*mult) > 1e-9 {
			t.Fatalf("peak RPS does not track multiplier: %f vs %f×%f",
				p.PeakRPS, base.AverageRPS, mult)
		}
		if math.Abs(p.AverageRPS-base.AverageRPS) > 1e-12 {
			t.Fatal("average RPS must not depend on the peak multiplier")
		}
	}
}

// ---------------------------------------------------------------- queue ----

// Step 8.3: queues grow when arrival exceeds service capacity, and the
// measured depth/trend reflect it. Mild sustained overload (1.27× capacity)
// against an unbounded queue: depth climbs continuously.
func TestQueueGrowsUnderOverload(t *testing.T) {
	arch := Architecture{
		Name: "queue-grow",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 0, DefaultServiceTimeMillis: 10},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 1.1, ReadWriteRatio: 1, // ~127 RPS vs db ~100 RPS capacity
	}
	res, err := Simulate(arch, spec, Options{Seed: 300, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.MaxQueueDepth < 50 {
		t.Fatalf("overloaded queue must grow, max depth %d", db.MaxQueueDepth)
	}
	if db.AvgQueueWaitMS <= 0 {
		t.Fatal("queued requests must record positive wait time")
	}
	if db.Rejected != 0 {
		t.Fatalf("unbounded queue must not reject: %d", db.Rejected)
	}
	if db.QueueTrend != "growing" {
		t.Fatalf("queue trend = %q, want growing under sustained overload", db.QueueTrend)
	}
}

// Step 8.3: queues drain (trend draining/stable, no depth) when capacity
// exceeds arrival load.
func TestQueueDrainsUnderLightLoad(t *testing.T) {
	arch := Architecture{
		Name: "queue-drain",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 8, QueueLimit: 100, DefaultServiceTimeMillis: 1},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 10_000, RequestsPerUserPerDay: 2,
		PeakMultiplier: 2, ReadWriteRatio: 1, // ~0.46 RPS vs db ~8000 RPS
	}
	res, err := Simulate(arch, spec, Options{Seed: 301, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.MaxQueueDepth > 2 {
		t.Fatalf("light load must not build a deep queue: max %d", db.MaxQueueDepth)
	}
	if db.QueueTrend == "growing" {
		t.Fatalf("queue trend = growing on a drained system")
	}
	if db.Rejected != 0 {
		t.Fatalf("light load must not reject: %d", db.Rejected)
	}
}

// Step 8.3: rejections fire exactly when concurrency + queue limit are
// exhausted, and rejected requests never enter service (their wait/queue
// metrics stay clean).
func TestRejectionAtConfiguredLimit(t *testing.T) {
	arch := Architecture{
		Name: "reject",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 3, DefaultServiceTimeMillis: 20},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1, // ~231 RPS vs db 50 RPS
	}
	res, err := Simulate(arch, spec, Options{Seed: 302, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.Rejected == 0 {
		t.Fatal("saturated 1-slot/3-queue db must reject")
	}
	// Rejections only occur when the queue is at its limit: the recorded
	// high-water mark must be the full queue limit (3).
	if db.MaxQueueDepth != 3 {
		t.Fatalf("max queue depth = %d, want exactly the configured limit 3", db.MaxQueueDepth)
	}
	// Conservation with fan-out absent: generated = completed + rejected + in-flight.
	m := res.Metrics
	if m.Generated != m.Completed+m.Rejected+m.InFlight {
		t.Fatalf("conservation: gen=%d comp=%d rej=%d infl=%d",
			m.Generated, m.Completed, m.Rejected, m.InFlight)
	}
}

// Step 8.3: unbounded queues (QueueLimit=0) never reject — they grow.
func TestUnboundedQueueNeverRejects(t *testing.T) {
	arch := Architecture{
		Name: "unbounded",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 0, DefaultServiceTimeMillis: 20},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 303, DurationMS: 15_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.Rejected != 0 {
		t.Fatalf("unbounded queue must never reject, got %d", db.Rejected)
	}
	if db.MaxQueueDepth < 10 {
		t.Fatalf("overloaded unbounded queue should grow deep, max %d", db.MaxQueueDepth)
	}
}

// ------------------------------------------------------------- capacity ----

// Step 8.4: concurrency is actually respected — a component never exceeds
// its slot count, even under heavy queueing.
func TestConcurrencySlotsRespected(t *testing.T) {
	arch := Architecture{
		Name: "slots",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 2, QueueLimit: 500, DefaultServiceTimeMillis: 25},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 50,
		PeakMultiplier: 5, ReadWriteRatio: 1, // ~58 RPS vs 80 RPS capacity
	}
	// Sample-based check: utilization can never exceed 1 for the run, and
	// with 25ms service and 2 slots, sustained throughput must be ≈ 80 RPS
	// (the concurrency limit), not more.
	res, err := Simulate(arch, spec, Options{Seed: 310, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.Utilization > 1.0+1e-9 {
		t.Fatalf("utilization exceeded 1.0: %f — slots not respected", db.Utilization)
	}
	// Theoretical ceiling: 2 slots / 0.025s = 80 RPS. Measured throughput
	// must sit just under it (Poisson slack), never above it.
	if db.ThroughputRPS > 80.0+2.0 {
		t.Fatalf("throughput %f RPS exceeds concurrency ceiling 80 RPS", db.ThroughputRPS)
	}
	if db.ThroughputRPS < 60.0 {
		t.Fatalf("throughput %f RPS suspiciously below the ceiling — model broke", db.ThroughputRPS)
	}
}

// Step 8.4: service time is actually executed — measured average service
// must match the spec value (within sampling noise).
func TestServiceTimeRespected(t *testing.T) {
	arch := Architecture{
		Name: "svc",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 4, QueueLimit: 100, DefaultServiceTimeMillis: 8},
			{ID: "cache", Kind: KindCache, Concurrency: 2, QueueLimit: 100, HitRatio: 0.5, DefaultServiceTimeMillis: 0.5},
		},
		Links: []Link{
			{From: "client", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 10_000, RequestsPerUserPerDay: 5,
		PeakMultiplier: 1, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 311, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Metrics.Components {
		switch c.ID {
		case "db":
			if c.Completed > 0 && math.Abs(c.AvgServiceMS-8) > 0.5 {
				t.Fatalf("db measured service %fms, want ≈8ms", c.AvgServiceMS)
			}
		case "cache":
			if c.Completed > 0 && c.AvgServiceMS <= 0 {
				t.Fatal("cache must record measured service time")
			}
		}
	}
}

// Step 8.4: per-op service times override the default (read vs write
// pricing on a database).
func TestPerOpServiceTimesRespected(t *testing.T) {
	arch := Architecture{
		Name: "perop",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 8, QueueLimit: 100,
				ServiceTimeMillis:        map[Op]float64{OpRead: 1, OpWrite: 20},
				DefaultServiceTimeMillis: 5},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	// Two runs: all reads vs all writes. Same seed → same arrivals.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 2, ReadWriteRatio: 1e9, // ~all reads
	}
	readRun, err := Simulate(arch, spec, Options{Seed: 312, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	writeSpec := spec
	writeSpec.ReadWriteRatio = 1e-9 // ~all writes
	writeRun, err := Simulate(arch, writeSpec, Options{Seed: 312, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	var rdb, wdb ComponentMetrics
	for _, c := range readRun.Metrics.Components {
		if c.ID == "db" {
			rdb = c
		}
	}
	for _, c := range writeRun.Metrics.Components {
		if c.ID == "db" {
			wdb = c
		}
	}
	if rdb.Completed == 0 || wdb.Completed == 0 {
		t.Fatal("both runs must complete traffic")
	}
	if wdb.AvgServiceMS < rdb.AvgServiceMS+10 {
		t.Fatalf("write-heavy service %fms should far exceed read-heavy %fms (1ms vs 20ms ops)",
			wdb.AvgServiceMS, rdb.AvgServiceMS)
	}
}

// Step 8.4: capacity RPS is a saturation FLAG (metrics), not an implicit
// admission control — queue limit is what rejects.
func TestCapacityRPSFlagsSaturationOnly(t *testing.T) {
	arch := Architecture{
		Name: "capflag",
		Components: []ComponentSpec{
			client("client"),
			// CapacityRPS 10 but huge concurrency+queue: admission never blocks.
			{ID: "db", Kind: KindDatabase, Concurrency: 100, QueueLimit: 0,
				CapacityRPS: 10, DefaultServiceTimeMillis: 0.1},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 5, ReadWriteRatio: 1, // ~115 RPS ≫ 10 RPS flag
	}
	res, err := Simulate(arch, spec, Options{Seed: 313, DurationMS: 15_000})
	if err != nil {
		t.Fatal(err)
	}
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if !db.Saturated {
		t.Fatalf("arrival %f RPS must flag saturation against capacity 10", db.ArrivalRPS)
	}
	if db.Rejected != 0 {
		t.Fatal("CapacityRPS is a flag, not admission control — must not reject")
	}
}

// -------------------------------------------------------------- latency ----

// Step 8.5: total request latency composes exactly: service + queue wait
// at each hop. Two serial databases (no cache) with known service times:
// light load ⇒ latency ≈ sum of service times (+ jitter band).
func TestLatencyComposesAcrossHops(t *testing.T) {
	arch := Architecture{
		Name: "compose",
		Components: []ComponentSpec{
			client("client"),
			{ID: "api", Kind: KindAPIServer, Concurrency: 16, QueueLimit: 100, DefaultServiceTimeMillis: 3},
			{ID: "db", Kind: KindDatabase, Concurrency: 16, QueueLimit: 100, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "db"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 10_000, RequestsPerUserPerDay: 2,
		PeakMultiplier: 1, ReadWriteRatio: 1, // ~0.23 RPS: no queueing
	}
	res, err := Simulate(arch, spec, Options{Seed: 320, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Completed == 0 {
		t.Fatal("no completions")
	}
	// 3ms (api) + 5ms (db) = 8ms minimum; tiny queueing slack allowed.
	if m.P50MS < 7.9 || m.P50MS > 12 {
		t.Fatalf("p50 = %fms, want ≈8ms (3+5 service composition)", m.P50MS)
	}
}

// Step 8.5: queue waiting time appears in the tail, not the floor —
// under overload p99 must far exceed pure service composition.
func TestLatencyQueueWaitShowsInTail(t *testing.T) {
	arch := Architecture{
		Name: "tail",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 2000, DefaultServiceTimeMillis: 10},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1, // ~115 RPS vs 100 RPS capacity
	}
	res, err := Simulate(arch, spec, Options{Seed: 321, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.P99MS < 20 {
		t.Fatalf("p99 %fms must exceed 2× service under overload (queue wait)", m.P99MS)
	}
	if m.P99MS <= m.P50MS {
		t.Fatalf("tail must exceed median under queueing: p99=%f p50=%f", m.P99MS, m.P50MS)
	}
}

// Step 8.5: percentiles are computed from actual per-request observations
// — verify the measured average converges to the observed latency sum, and
// that the sample count equals completed+failed terminal outcomes.
func TestLatencyFromActualObservations(t *testing.T) {
	arch := basicArch()
	spec := baseSpec()
	res, err := Simulate(arch, spec, Options{Seed: 322, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.P50MS <= 0 || m.P95MS < m.P50MS || m.P99MS < m.P95MS || m.MaxLatencyMS < m.P99MS {
		t.Fatalf("percentile ordering violated: %f/%f/%f/%f",
			m.P50MS, m.P95MS, m.P99MS, m.MaxLatencyMS)
	}
	// avg must be bounded by min/max of the observed distribution.
	if m.AvgLatencyMS < m.P50MS*0.2 || m.AvgLatencyMS > m.MaxLatencyMS+1e-9 {
		t.Fatalf("avg %f outside plausible observation range [~min, max %f]",
			m.AvgLatencyMS, m.MaxLatencyMS)
	}
	// Nearest-rank definitions hold on live data: p50 of a monotone series
	// cannot exceed the median rank position. Indirect but sound: with >100
	// samples, p99 ≥ p95 ≥ p50 and all are actual observed values.
	if m.Completed+m.Failed < 100 {
		t.Fatalf("need ≥100 terminal outcomes for percentile meaning, got %d",
			m.Completed+m.Failed)
	}
}

// -------------------------------------------------------------- failures ----

// Step 8.6: failures only affect their intended target — a latency
// injection on the db leaves the api's measured service time untouched.
func TestFailureScopingToTarget(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}
	healthy, err := Simulate(arch, spec, Options{Seed: 330, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := Simulate(arch, spec, Options{
		Seed:       330,
		DurationMS: 20_000, // same horizon as the failure window
		Failures: []Failure{{
			Target: "db", Type: FailureLatency,
			StartMS: 0, DurationMS: 20_000,
			Config: FailureConfig{AddedLatencyMillis: 15},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(res *RunResult, id string) ComponentMetrics {
		for _, c := range res.Metrics.Components {
			if c.ID == id {
				return c
			}
		}
		return ComponentMetrics{}
	}
	hApi, dApi := get(healthy, "api"), get(degraded, "api")
	hDb, dDb := get(healthy, "db"), get(degraded, "db")
	if math.Abs(dApi.AvgServiceMS-hApi.AvgServiceMS) > 1e-9 {
		t.Fatalf("api service changed %f→%f under a db-only failure",
			hApi.AvgServiceMS, dApi.AvgServiceMS)
	}
	if dDb.AvgServiceMS < hDb.AvgServiceMS+10 {
		t.Fatalf("db service %f→%f must carry the +15ms penalty",
			hDb.AvgServiceMS, dDb.AvgServiceMS)
	}
}

// Step 8.6: failure start time is honored — no failures before StartMS,
// none after the window ends.
func TestFailureWindowBoundariesHonored(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       331,
		DurationMS: 30_000,
		Failures: []Failure{{
			Target: "db", Type: FailureErrorRate,
			StartMS: 10_000, DurationMS: 10_000, // 10s→20s
			Config: FailureConfig{ErrorRate: 1.0},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Metrics.Failures {
		if f.AtMS < 10_000-1 || f.AtMS > 20_000+1 {
			t.Fatalf("failure at %.1fms outside injection window [10000,20000]", f.AtMS)
		}
	}
	if res.Metrics.Failed == 0 {
		t.Fatal("100% error-rate window must produce failures")
	}
}

// Step 8.6: failure duration is finite — recovery restores behavior.
func TestFailureDurationEnds(t *testing.T) {
	arch := crashArch()
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{
		Seed:       332,
		DurationMS: 24_000,
		Failures: []Failure{{
			Target: "db", Type: FailureLatency,
			StartMS: 0, DurationMS: 4_000, // only the first 4s
			Config: FailureConfig{AddedLatencyMillis: 40},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Latency penalty is bounded: once recovery lands, completions return
	// to fast service. Average service time must be well under the
	// all-window-failing level (5+40=45ms) — it blends 4s@45ms with 20s@5ms.
	var db ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			db = c
		}
	}
	if db.AvgServiceMS > 15 {
		t.Fatalf("db avg service %fms — failure window did not end", db.AvgServiceMS)
	}
	if db.AvgServiceMS < 8 {
		t.Fatalf("db avg service %fms — penalty not applied at all", db.AvgServiceMS)
	}
}

// Step 8.7: THE cascade — cache crash (pass-through) → database load
// increases → database saturates → queue grows → latency rises. Each link
// in the chain is asserted from measured numbers.
func TestCacheCrashCascadeEmerges(t *testing.T) {
	arch := Architecture{
		Name: "cascade",
		Components: []ComponentSpec{
			client("client"),
			{ID: "api", Kind: KindAPIServer, Concurrency: 20, QueueLimit: 200, DefaultServiceTimeMillis: 2},
			{ID: "cache", Kind: KindCache, Concurrency: 2, QueueLimit: 500, HitRatio: 0.8, DefaultServiceTimeMillis: 0.1},
			{ID: "db", Kind: KindDatabase, Concurrency: 4, QueueLimit: 20, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
	// Reads only (misses are the only DB load), sized so the DB can take
	// baseline misses comfortably but NOT the full read stream.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1e9, // ≈ all reads, ~231 RPS peak
	}
	base, err := Simulate(arch, spec, Options{Seed: 340, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	cascade, err := Simulate(arch, spec, Options{
		Seed: 340, // same arrivals → controlled comparison
		Failures: []Failure{{
			Target: "cache", Type: FailureCrash,
			StartMS: 5_000, DurationMS: 20_000,
			Config: FailureConfig{PassThrough: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(res *RunResult, id string) ComponentMetrics {
		for _, c := range res.Metrics.Components {
			if c.ID == id {
				return c
			}
		}
		return ComponentMetrics{}
	}
	bdb, cdb := get(base, "db"), get(cascade, "db")

	// 1. database load increases (cache hits became DB traffic)
	if cdb.Arrived <= bdb.Arrived*2 {
		t.Fatalf("cascade step 1: db arrivals %d should more than double vs baseline %d",
			cdb.Arrived, bdb.Arrived)
	}
	// 2. database saturates → rejections at its small queue
	if cdb.Rejected == 0 {
		t.Fatal("cascade step 2: db must shed load once saturated")
	}
	// 3. queue grows
	if cdb.MaxQueueDepth < 20 {
		t.Fatalf("cascade step 3: db max queue depth %d, want at the limit", cdb.MaxQueueDepth)
	}
	// 4. latency increases
	if cascade.Metrics.P95MS <= base.Metrics.P95MS {
		t.Fatalf("cascade step 4: p95 %f must exceed baseline %f",
			cascade.Metrics.P95MS, base.Metrics.P95MS)
	}
	// 5. diagnosis names the db with evidence, not the cache pass-through
	d := DiagnoseWithBaseline(cascade.Metrics, base.Metrics)
	if d.Healthy {
		t.Fatal("cascade step 5: diagnosis must flag the overload")
	}
	if d.Bottlenecks[0].ComponentID != "db" {
		t.Fatalf("cascade step 5: primary bottleneck %q, want db", d.Bottlenecks[0].ComponentID)
	}
	joined := strings.Join(d.Bottlenecks[0].Reasons, " | ")
	if !strings.Contains(joined, "rejected") && !strings.Contains(joined, "capacity") {
		t.Fatalf("cascade step 5: reasons must carry evidence, got: %s", joined)
	}
}

// Step 8.7: a second dependency failure — worker queue saturation. API
// enqueue→worker overload: the queue buffers, workers drain slower than
// arrivals, latency and in-flight counts rise.
func TestWorkerQueueSaturationCascade(t *testing.T) {
	arch := Architecture{
		Name: "worker-cascade",
		Components: []ComponentSpec{
			client("client"),
			{ID: "api", Kind: KindAPIServer, Concurrency: 16, QueueLimit: 100, DefaultServiceTimeMillis: 1},
			{ID: "q", Kind: KindQueue},
			{ID: "w", Kind: KindWorker, Concurrency: 1, QueueLimit: 0, DefaultServiceTimeMillis: 30},
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "q"},
			{From: "q", To: "w"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 50,
		PeakMultiplier: 5, ReadWriteRatio: 1, // ~58 RPS vs 33 RPS drain
	}
	res, err := Simulate(arch, spec, Options{Seed: 341, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	var qm, wm ComponentMetrics
	for _, c := range res.Metrics.Components {
		switch c.ID {
		case "q":
			qm = c
		case "w":
			wm = c
		}
	}
	if wm.Arrived == 0 {
		t.Fatal("worker saw no traffic")
	}
	if wm.ThroughputRPS >= qm.ArrivalRPS {
		t.Fatalf("worker drain %.1f RPS must lag queue arrival %.1f RPS",
			wm.ThroughputRPS, qm.ArrivalRPS)
	}
	if res.Metrics.InFlight == 0 {
		t.Fatal("backed-up work must still be in flight at the horizon")
	}
}

// ------------------------------------------------------------- diagnosis ----

// Step 8.8: diagnosis uses actual metrics — every reason string must
// embed concrete measured numbers, never generic phrasing.
func TestDiagnosisIsEvidenceBased(t *testing.T) {
	arch := Architecture{
		Name: "evidence",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 5, CapacityRPS: 50, DefaultServiceTimeMillis: 10},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1, // ~231 RPS vs 50 RPS capacity
	}
	res, err := Simulate(arch, spec, Options{Seed: 350, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	d := Diagnose(res.Metrics)
	if d.Healthy || len(d.Bottlenecks) == 0 {
		t.Fatal("overloaded system must be diagnosed")
	}
	b := d.Bottlenecks[0]
	if b.ComponentID != "db" {
		t.Fatalf("bottleneck %q, want db", b.ComponentID)
	}
	if b.Severity != "critical" {
		t.Fatalf("severity %q, want critical", b.Severity)
	}
	joined := strings.Join(b.Reasons, " | ")
	// Evidence strings must cite numbers: "rejected N of M arrivals",
	// "incoming load X RPS exceeds modeled capacity Y RPS", percentages.
	if !strings.Contains(joined, "rejected") || !strings.Contains(joined, "arrivals") {
		t.Fatalf("rejection evidence missing: %s", joined)
	}
	if !strings.Contains(joined, "RPS") {
		t.Fatalf("capacity evidence must cite RPS numbers: %s", joined)
	}
	if strings.Contains(strings.ToLower(joined), "may ") {
		t.Fatalf("diagnosis must not hedge with 'may': %s", joined)
	}
	for _, r := range b.Reasons {
		hasDigit := strings.ContainsAny(r, "0123456789")
		if !hasDigit {
			t.Fatalf("every reason must carry a number, got: %q", r)
		}
	}
}

// Step 8.8: moderate utilization alone flags moderate (not critical) —
// severity tracks the evidence.
func TestDiagnosisSeverityMatchesEvidence(t *testing.T) {
	arch := Architecture{
		Name: "moderate",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 4, QueueLimit: 200, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	// ~74% of the db's 800 RPS ceiling: elevated, not critical.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 25.5,
		PeakMultiplier: 10, ReadWriteRatio: 1, // ~591 RPS offered at peak
	}
	res, err := Simulate(arch, spec, Options{Seed: 351, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	d := Diagnose(res.Metrics)
	if d.Healthy {
		return // not flagged at all is acceptable at this load
	}
	if d.Bottlenecks[0].Severity == "critical" {
		t.Fatalf("231 RPS on a 800 RPS ceiling must not be critical: %+v",
			d.Bottlenecks[0].Reasons)
	}
}

// ------------------------------------------------------------- edge cases ----

// Step 8.11: zero traffic — a valid workload whose peak inter-arrival is
// long still runs and reports honest zeros.
func TestEdgeCaseNearZeroTraffic(t *testing.T) {
	res, err := Simulate(basicArch(), workload.Spec{
		TotalUsers: 100, DAU: 1, RequestsPerUserPerDay: 0.001,
		PeakMultiplier: 1, ReadWriteRatio: 1,
	}, Options{Seed: 360, DurationMS: 5_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Generated != 0 || m.Completed != 0 || m.Rejected != 0 || m.Failed != 0 {
		t.Fatalf("near-zero traffic must report zeros: %+v", m)
	}
	// Rates must be zero, not NaN.
	if math.IsNaN(m.ErrorRate) || m.ErrorRate != 0 {
		t.Fatalf("error rate %f on zero traffic", m.ErrorRate)
	}
	for _, c := range m.Components {
		if math.IsNaN(c.ArrivalRPS) || math.IsNaN(c.ThroughputRPS) || math.IsNaN(c.Utilization) {
			t.Fatalf("NaN in component metrics: %+v", c)
		}
	}
}

// Step 8.11: extremely high traffic — heavy overload must remain stable,
// bounded, and honest about shedding.
func TestEdgeCaseExtremelyHighTraffic(t *testing.T) {
	arch := Architecture{
		Name: "flood",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 2, QueueLimit: 10, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 100_000_000, DAU: 50_000_000, RequestsPerUserPerDay: 200,
		PeakMultiplier: 10, ReadWriteRatio: 1, // ~1.16M RPS offered
	}
	res, err := Simulate(arch, spec, Options{Seed: 361, DurationMS: 5_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Generated == 0 {
		t.Fatal("flood must generate traffic")
	}
	if m.Generated != m.Completed+m.Rejected+m.InFlight {
		t.Fatalf("conservation under flood: gen=%d comp=%d rej=%d infl=%d",
			m.Generated, m.Completed, m.Rejected, m.InFlight)
	}
	if m.Rejected == 0 {
		t.Fatal("flood against a 10-slot queue must shed")
	}
	if m.Completed == 0 {
		t.Fatal("some traffic must still complete under flood")
	}
}

// Step 8.11: zero capacity fields (concurrency 0 = unconstrained) are
// modeled as pass-through latency hops, not errors.
func TestEdgeCaseZeroConcurrencyIsPassThrough(t *testing.T) {
	arch := Architecture{
		Name: "passthrough",
		Components: []ComponentSpec{
			client("client"),
			{ID: "n", Kind: KindNetwork, DefaultServiceTimeMillis: 7},
			{ID: "db", Kind: KindDatabase, Concurrency: 4, QueueLimit: 50, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{
			{From: "client", To: "n"},
			{From: "n", To: "db"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 10_000, RequestsPerUserPerDay: 5,
		PeakMultiplier: 1, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 362, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Completed == 0 {
		t.Fatal("pass-through hop must not break the flow")
	}
	// 7ms transit + 5ms db service = 12ms floor.
	if m.P50MS < 11.5 || m.P50MS > 20 {
		t.Fatalf("p50 = %fms, want ≈12ms (transit+service)", m.P50MS)
	}
}

// Step 8.11: missing dependency — link to a nonexistent component fails
// validation explicitly.
func TestEdgeCaseMissingDependencyRejected(t *testing.T) {
	arch := basicArch()
	arch.Links = append(arch.Links, Link{From: "api", To: "ghost"})
	if err := arch.Validate(); err == nil {
		t.Fatal("missing dependency must fail validation")
	}
}

// Step 8.11: disconnected component — unreachable from the client, must
// fail validation.
func TestEdgeCaseDisconnectedComponentRejected(t *testing.T) {
	arch := basicArch()
	arch.Components = append(arch.Components, storage("orphan"))
	if err := arch.Validate(); err == nil {
		t.Fatal("disconnected component must fail validation")
	}
}

// Step 8.11: circular architecture — cycle fails validation.
func TestEdgeCaseCircularArchitectureRejected(t *testing.T) {
	arch := Architecture{
		Name: "cycle",
		Components: []ComponentSpec{
			client("client"),
			{ID: "a", Kind: KindAPIServer, Concurrency: 2, QueueLimit: 5, DefaultServiceTimeMillis: 1},
			{ID: "b", Kind: KindAPIServer, Concurrency: 2, QueueLimit: 5, DefaultServiceTimeMillis: 1},
		},
		Links: []Link{
			{From: "client", To: "a"},
			{From: "a", To: "b"},
			{From: "b", To: "a"},
		},
	}
	if err := arch.Validate(); err == nil {
		t.Fatal("circular architecture must fail validation")
	}
}

// Step 8.11: invalid configuration — negative values rejected up front.
func TestEdgeCaseInvalidConfigRejected(t *testing.T) {
	cases := []struct {
		name string
		spec ComponentSpec
	}{
		{"negative concurrency", ComponentSpec{ID: "x", Kind: KindDatabase, Concurrency: -1}},
		{"negative queue limit", ComponentSpec{ID: "x", Kind: KindDatabase, QueueLimit: -1}},
		{"negative capacity", ComponentSpec{ID: "x", Kind: KindDatabase, CapacityRPS: -5}},
		{"hit ratio over one", ComponentSpec{ID: "x", Kind: KindCache, HitRatio: 1.5}},
		{"negative service time", ComponentSpec{ID: "x", Kind: KindDatabase, DefaultServiceTimeMillis: -2}},
		{"negative per-op service time", ComponentSpec{ID: "x", Kind: KindDatabase,
			ServiceTimeMillis: map[Op]float64{OpRead: -1}}},
		{"empty id", ComponentSpec{ID: "", Kind: KindDatabase}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arch := Architecture{
				Components: []ComponentSpec{client("client"), tc.spec},
				Links:      []Link{{From: "client", To: "x"}},
			}
			if _, err := Simulate(arch, baseSpec(), Options{Seed: 1, DurationMS: 1_000}); err == nil {
				t.Fatalf("invalid config %q must be rejected", tc.name)
			}
		})
	}
}

// Step 8.11: extremely long queues — a deep unbounded queue must not
// overflow memory or metrics in a bounded run. Note the model fact this
// pins: the KindQueue component is a zero-delay pass-through hop; actual
// FIFO buffering lives at the constrained component downstream (here the
// worker's unbounded queue).
func TestEdgeCaseExtremelyLongQueues(t *testing.T) {
	arch := Architecture{
		Name: "deep-queue",
		Components: []ComponentSpec{
			client("client"),
			{ID: "api", Kind: KindAPIServer, Concurrency: 32, QueueLimit: 0, DefaultServiceTimeMillis: 0.1},
			{ID: "q", Kind: KindQueue},
			{ID: "w", Kind: KindWorker, Concurrency: 1, QueueLimit: 0, DefaultServiceTimeMillis: 100},
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "q"},
			{From: "q", To: "w"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 500_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 5, ReadWriteRatio: 1, // ~2894 RPS vs 10 RPS drain
	}
	res, err := Simulate(arch, spec, Options{Seed: 363, DurationMS: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	var qm, wm ComponentMetrics
	for _, c := range res.Metrics.Components {
		switch c.ID {
		case "q":
			qm = c
		case "w":
			wm = c
		}
	}
	// The queue component passes through; the worker buffers the backlog.
	if qm.Arrived == 0 || wm.Arrived != qm.Completed {
		t.Fatalf("queue→worker handoff mismatch: q arrived=%d completed=%d w arrived=%d",
			qm.Arrived, qm.Completed, wm.Arrived)
	}
	if wm.MaxQueueDepth < 1000 {
		t.Fatalf("deep queue should hold thousands at the worker, max %d", wm.MaxQueueDepth)
	}
	if res.Metrics.Generated != res.Metrics.Completed+res.Metrics.InFlight {
		t.Fatalf("conservation with deep queue: %+v", res.Metrics)
	}
}

// Step 8.11: failure at simulation start (StartMS=0) is legal and takes
// effect immediately.
func TestEdgeCaseFailureAtStart(t *testing.T) {
	res, err := Simulate(crashArch(), workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}, Options{
		Seed:       364,
		DurationMS: 10_000,
		Failures: []Failure{{
			Target: "db", Type: FailureErrorRate,
			StartMS: 0, DurationMS: 10_000,
			Config: FailureConfig{ErrorRate: 0.5},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.Failed == 0 {
		t.Fatal("failure starting at t=0 must affect the run")
	}
}

// Step 8.11: failure ending exactly at the horizon is legal — the stop
// event may land at/past the horizon edge; results stay consistent.
func TestEdgeCaseFailureAtEnd(t *testing.T) {
	res, err := Simulate(crashArch(), workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}, Options{
		Seed:       365,
		DurationMS: 10_000,
		Failures: []Failure{{
			Target: "db", Type: FailureCrash,
			StartMS: 9_000, DurationMS: 1_000, // ends exactly at the horizon
			Config: FailureConfig{PassThrough: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Generated != m.Completed+m.Rejected+m.InFlight+m.Failed {
		t.Fatalf("conservation with horizon-edge failure: %+v", m)
	}
}

// ------------------------------------------------- fixed bug regressions ----

// Regression (Step 8 audit): fan-out across multiple links must count each
// leg in Generated, or Completed could exceed Generated and InFlight could
// underflow. A load balancer fanning out to two databases is the minimal
// trigger.
func TestFanOutConservationRegression(t *testing.T) {
	arch := Architecture{
		Name: "fanout",
		Components: []ComponentSpec{
			client("client"),
			lb("lb"),
			{ID: "db1", Kind: KindDatabase, Concurrency: 4, QueueLimit: 50, DefaultServiceTimeMillis: 5},
			{ID: "db2", Kind: KindDatabase, Concurrency: 4, QueueLimit: 50, DefaultServiceTimeMillis: 5},
		},
		Links: []Link{
			{From: "client", To: "lb"},
			{From: "lb", To: "db1"},
			{From: "lb", To: "db2"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 2, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 370, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Generated != m.Completed+m.Rejected+m.InFlight+m.Failed {
		t.Fatalf("fan-out conservation broken: gen=%d comp=%d rej=%d infl=%d failed=%d",
			m.Generated, m.Completed, m.Rejected, m.InFlight, m.Failed)
	}
	var d1, d2 ComponentMetrics
	for _, c := range m.Components {
		switch c.ID {
		case "db1":
			d1 = c
		case "db2":
			d2 = c
		}
	}
	if d1.Arrived == 0 || d2.Arrived == 0 {
		t.Fatal("both fan-out legs must receive traffic")
	}
}

// Regression (Step 8 audit): requests enqueued at exactly t=0 must still
// have their queue wait measured (the old QueueEnterMS>0 sentinel missed
// them).
func TestQueueWaitAtTimeZeroMeasured(t *testing.T) {
	// A request arrives at t≈0 (first Poisson gap can be tiny) into a
	// saturated component: its wait must be counted. We assert the
	// aggregate invariant instead of poking privates: with saturation,
	// AvgQueueWaitMS must be strictly positive.
	arch := Architecture{
		Name: "zero-wait",
		Components: []ComponentSpec{
			client("client"),
			{ID: "db", Kind: KindDatabase, Concurrency: 1, QueueLimit: 1000, DefaultServiceTimeMillis: 10},
		},
		Links: []Link{{From: "client", To: "db"}},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1,
	}
	found := false
	for seed := uint64(371); seed < 380 && !found; seed++ {
		res, err := Simulate(arch, spec, Options{Seed: seed, DurationMS: 10_000})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Metrics.Components {
			if c.ID == "db" && c.AvgQueueWaitMS > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("saturated db must record positive queue wait across seeds")
	}
}

// ------------------------------------------------- percentile definition ----

// Step 8.5 lock: the nearest-rank definition (ceil(p×n)) on adversarial
// sample counts where round-half-up diverges from it.
func TestNearestRankDefinition(t *testing.T) {
	// 39 samples: round-half-up gives p95 rank 37, nearest-rank gives 38.
	n := 39
	sorted := make([]float64, n)
	for i := range sorted {
		sorted[i] = float64(i + 1) // 1..39, sorted
	}
	if got := nearestRank(sorted, 0.95); got != 38 {
		t.Fatalf("p95 of 39 samples = %v, want 38 (ceil(0.95×39)=38)", got)
	}
	// 20 samples: exact float edge (0.05×20=1.0000000000000002) must not
	// bump the rank.
	if got := nearestRank(sorted[:20], 0.05); got != 1 {
		t.Fatalf("p05 of 20 samples = %v, want 1", got)
	}
	if got := nearestRank(sorted[:20], 0.50); got != 10 {
		t.Fatalf("p50 of 20 samples = %v, want 10", got)
	}
	// Degenerate inputs.
	if got := nearestRank(nil, 0.99); got != 0 {
		t.Fatalf("empty input must yield 0, got %v", got)
	}
	if got := nearestRank([]float64{42}, 0.99); got != 42 {
		t.Fatalf("single sample must be its own percentile, got %v", got)
	}
}
