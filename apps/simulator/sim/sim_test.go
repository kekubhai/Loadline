package sim

import (
	"math"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// --- Fixtures ---

func baseSpec() workload.Spec {
	return workload.Spec{
		TotalUsers:            1_000_000,
		DAU:                   100_000,
		RequestsPerUserPerDay: 20,
		PeakMultiplier:        5,
		ReadWriteRatio:        4,
		PayloadBytes:          4096,
	}
}

func client(id string) ComponentSpec { return ComponentSpec{ID: id, Kind: KindClient} }
func net(id string) ComponentSpec    { return ComponentSpec{ID: id, Kind: KindNetwork} }
func lb(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindLoadBalancer, Concurrency: 8, QueueLimit: 64, CapacityRPS: 20000, DefaultServiceTimeMillis: 0.5}
}
func api(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindAPIServer, Concurrency: 10, QueueLimit: 50, CapacityRPS: 1500, DefaultServiceTimeMillis: 3}
}
func cache(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindCache, Concurrency: 1, QueueLimit: 100, HitRatio: 0.8, DefaultServiceTimeMillis: 0.1}
}
func db(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindDatabase, Concurrency: 4, QueueLimit: 20, CapacityRPS: 300, DefaultServiceTimeMillis: 5}
}
func storage(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindObjectStorage, Concurrency: 2, QueueLimit: 10, DefaultServiceTimeMillis: 12}
}
func queueC(id string) ComponentSpec { return ComponentSpec{ID: id, Kind: KindQueue} }
func worker(id string) ComponentSpec {
	return ComponentSpec{ID: id, Kind: KindWorker, Concurrency: 4, QueueLimit: 40, DefaultServiceTimeMillis: 8}
}

func basicArch() Architecture {
	// Client → API Server → Cache → Database (the required basic
	// architecture; cache misses and writes continue to the database).
	return Architecture{
		Name: "basic",
		Components: []ComponentSpec{
			client("client"), api("api"), cache("cache"), db("db"),
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
}

// Alias kept for readability at call sites.
func basicArchWithDB() Architecture { return basicArch() }

// --- Validation tests ---

func TestValidateBasicArch(t *testing.T) {
	arch := basicArch()
	if err := arch.Validate(); err != nil {
		t.Fatalf("basic arch should validate: %v", err)
	}
}

func TestValidateRequiresSingleClient(t *testing.T) {
	arch := basicArch()
	arch.Components = append(arch.Components, client("client2"))
	if err := arch.Validate(); err == nil {
		t.Fatal("two clients must fail validation")
	}
}

func TestValidateRejectsUnknownLink(t *testing.T) {
	arch := basicArch()
	arch.Links = append(arch.Links, Link{From: "api", To: "ghost"})
	if err := arch.Validate(); err == nil {
		t.Fatal("unknown link target must fail validation")
	}
}

func TestValidateRejectsCycle(t *testing.T) {
	arch := basicArch()
	arch.Links = append(arch.Links, Link{From: "db", To: "api"})
	if err := arch.Validate(); err == nil {
		t.Fatal("cycle must fail validation")
	}
}

func TestValidateQueueNeedsExactlyOneWorker(t *testing.T) {
	// Queue with no worker.
	arch := Architecture{
		Components: []ComponentSpec{client("c"), queueC("q")},
		Links:      []Link{{From: "c", To: "q"}},
	}
	if err := arch.Validate(); err == nil {
		t.Fatal("queue without worker must fail validation")
	}
	// Queue with two workers.
	arch2 := Architecture{
		Components: []ComponentSpec{client("c"), queueC("q"), worker("w1"), worker("w2")},
		Links:      []Link{{From: "c", To: "q"}, {From: "q", To: "w1"}, {From: "q", To: "w2"}},
	}
	if err := arch2.Validate(); err == nil {
		t.Fatal("queue with two workers must fail validation")
	}
}

func TestValidateWorkerNoOutgoing(t *testing.T) {
	arch := Architecture{
		Components: []ComponentSpec{client("c"), queueC("q"), worker("w")},
		Links: []Link{
			{From: "c", To: "q"},
			{From: "q", To: "w"},
			{From: "w", To: "api"},
		},
		// api doesn't exist; worker outgoing link must fail first.
	}
	if err := arch.Validate(); err == nil {
		t.Fatal("worker outgoing link must fail validation")
	}
}

// --- Request conservation ---

func TestRequestConservation(t *testing.T) {
	res, err := Simulate(basicArchWithDB(), baseSpec(), Options{Seed: 7, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Generated != m.Completed+m.Rejected+m.InFlight {
		t.Fatalf("conservation violated: gen=%d comp=%d rej=%d infl=%d",
			m.Generated, m.Completed, m.Rejected, m.InFlight)
	}
	if m.Generated == 0 {
		t.Fatal("no requests generated")
	}
}

// --- Capacity and rejection -----

func TestRejectionUnderOverload(t *testing.T) {
	// Tiny DB: 2 slots, 10ms service, queue limit 5. Saturate it with a
	// heavy workload.
	arch := Architecture{
		Name: "overload",
		Components: []ComponentSpec{
			client("client"), api("api"), db("db"),
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "db"},
		},
	}
	arch.Components[2] = ComponentSpec{ID: "db", Kind: KindDatabase, Concurrency: 2, QueueLimit: 5, DefaultServiceTimeMillis: 10}

	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 200_000, RequestsPerUserPerDay: 100,
		PeakMultiplier: 10, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 3, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.Rejected == 0 {
		t.Fatal("overloaded system must reject requests")
	}
	// Rejected requests are not completed.
	if res.Metrics.Completed+res.Metrics.Rejected > res.Metrics.Generated {
		t.Fatalf("completed+rejected exceeds generated: %+v", res.Metrics)
	}
}

func TestNoRejectionUnderLightLoad(t *testing.T) {
	// Peak ≈ 11.6 RPS — far below every component's capacity.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 2, ReadWriteRatio: 4,
	}
	res, err := Simulate(basicArchWithDB(), spec, Options{Seed: 11, DurationMS: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.Rejected != 0 {
		t.Fatalf("light load should not reject, got %d rejections", res.Metrics.Rejected)
	}
	if res.Metrics.Completed == 0 {
		t.Fatal("light load should complete requests")
	}
}

// --- Cache behavior ---

func TestCacheHitRatio(t *testing.T) {
	arch := basicArchWithDB()
	// Cache with 80% hit ratio; peak ≈ 17.4 RPS over 120s ≈ 2000 requests.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 100, // almost all reads
	}
	res, err := Simulate(arch, spec, Options{Seed: 5, DurationMS: 120_000})
	if err != nil {
		t.Fatal(err)
	}
	var cm ComponentMetrics
	for _, c := range res.Metrics.Components {
		if c.ID == "cache" {
			cm = c
		}
	}
	if cm.Arrived < 1000 {
		t.Fatalf("cache saw too few requests: %d", cm.Arrived)
	}
	dbHit := uint64(0)
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			dbHit = c.Arrived
		}
	}
	// Reads that miss go to the DB; writes never hit the cache short-circuit
	// and continue to DB as well. With ratio 100:1, writes are ~1%.
	// So DB arrivals ≈ misses + writes ≈ (0.2*reads + writes). Sanity band:
	// DB traffic must be far below cache arrivals.
	if dbHit >= cm.Arrived {
		t.Fatalf("db traffic (%d) should be well below cache traffic (%d)", dbHit, cm.Arrived)
	}
}

// --- Latency metrics ---

func TestLatencyMetrics(t *testing.T) {
	res, err := Simulate(basicArchWithDB(), baseSpec(), Options{Seed: 42, DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if m.Completed == 0 {
		t.Fatal("no completions to measure")
	}
	if m.P50MS <= 0 || m.P95MS < m.P50MS || m.P99MS < m.P95MS || m.MaxLatencyMS < m.P99MS {
		t.Fatalf("percentile ordering violated: p50=%f p95=%f p99=%f max=%f",
			m.P50MS, m.P95MS, m.P99MS, m.MaxLatencyMS)
	}
	if m.AvgLatencyMS <= 0 {
		t.Fatal("avg latency must be positive")
	}
	if math.IsNaN(m.AvgLatencyMS) || math.IsInf(m.AvgLatencyMS, 0) {
		t.Fatal("avg latency not finite")
	}
}

// --- Queue/worker flow ---

func TestQueueWorkerFlow(t *testing.T) {
	arch := Architecture{
		Name: "pipeline",
		Components: []ComponentSpec{
			client("client"), api("api"), queueC("q"), worker("w"),
		},
		Links: []Link{
			{From: "client", To: "api"},
			{From: "api", To: "q"},
			{From: "q", To: "w"},
		},
	}
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 1,
	}
	res, err := Simulate(arch, spec, Options{Seed: 9, DurationMS: 30_000})
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
	if qm.Arrived == 0 || wm.Arrived != qm.Completed {
		t.Fatalf("queue→worker handoff mismatch: queue arrived=%d completed=%d, worker arrived=%d",
			qm.Arrived, qm.Completed, wm.Arrived)
	}
}

// --- Saturation flag ---

func TestSaturationFlag(t *testing.T) {
	arch := basicArchWithDB()
	for i := range arch.Components {
		if arch.Components[i].ID == "db" {
			arch.Components[i].CapacityRPS = 1 // absurdly low → must flag
		}
	}
	spec := baseSpec()
	res, err := Simulate(arch, spec, Options{Seed: 2, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Metrics.Components {
		if c.ID == "db" {
			found = true
			if !c.Saturated {
				t.Fatalf("db arrivalRPS=%f should exceed capacity 1", c.ArrivalRPS)
			}
		}
	}
	if !found {
		t.Fatal("db metrics missing")
	}
}

// --- Determinism ---

func TestSimulateDeterministic(t *testing.T) {
	a, err := Simulate(basicArchWithDB(), baseSpec(), Options{Seed: 2024, DurationMS: 45_000})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Simulate(basicArchWithDB(), baseSpec(), Options{Seed: 2024, DurationMS: 45_000})
	if err != nil {
		t.Fatal(err)
	}
	if a.Metrics.Generated != b.Metrics.Generated ||
		a.Metrics.Completed != b.Metrics.Completed ||
		a.Metrics.Rejected != b.Metrics.Rejected ||
		a.Metrics.P99MS != b.Metrics.P99MS {
		t.Fatalf("identical runs diverged:\n%+v\n%+v", a.Metrics, b.Metrics)
	}
}

// --- Event engine invariants still hold under the sim layer ---

func TestSimEventsUseEngineTypes(t *testing.T) {
	res, err := Simulate(basicArchWithDB(), baseSpec(), Options{Seed: 1, DurationMS: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Events.Processed == 0 {
		t.Fatal("no engine events processed")
	}
	var _ engine.Time
}
