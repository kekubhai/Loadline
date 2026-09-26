package providers

import (
	"math"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// --- Catalog loading ---

func TestCatalogCompleteness(t *testing.T) {
	cat := Catalog()
	want := map[string]bool{
		"aws/ec2": true, "aws/lambda": true, "aws/rds": true,
		"aws/elasticache": true, "aws/sqs": true, "aws/s3": true, "aws/cloudfront": true,
		"cloudflare/workers": true, "cloudflare/kv": true, "cloudflare/r2": true,
		"cloudflare/queues": true, "cloudflare/durable_objects": true,
		"gcp/cloud_run": true, "gcp/cloud_sql": true, "gcp/memorystore": true,
		"gcp/pub_sub": true, "gcp/cloud_storage": true, "gcp/cloud_cdn": true,
	}
	if len(cat) != len(want) {
		t.Fatalf("catalog has %d services, want %d", len(cat), len(want))
	}
	for key := range want {
		m, ok := cat[key]
		if !ok {
			t.Errorf("missing service %s", key)
			continue
		}
		if m.Capacity().Concurrency < 0 || m.Pricing().Currency != "USD" {
			t.Errorf("%s: malformed model", key)
		}
	}
}

func TestCatalogServiceModelsExposeAllFacets(t *testing.T) {
	for key, m := range Catalog() {
		if m.Provider() == "" || m.Service() == "" || m.Summary() == "" {
			t.Errorf("%s: identity incomplete", key)
		}
		cap := m.Capacity()
		if len(cap.Notes) == 0 {
			t.Errorf("%s: capacity assumptions undocumented", key)
		}
		if m.Latency().Default <= 0 {
			t.Errorf("%s: latency model missing default", key)
		}
		if m.Scaling().Kind == "" {
			t.Errorf("%s: scaling kind missing", key)
		}
		if len(m.Failure().Notes) == 0 {
			t.Errorf("%s: failure model undocumented", key)
		}
		if len(m.Pricing().Notes) == 0 {
			t.Errorf("%s: pricing assumptions undocumented", key)
		}
	}
}

// --- Spec building ---

func TestBuildSpecMapsKinds(t *testing.T) {
	cases := []struct {
		provider, service string
		kind              sim.ComponentKind
	}{
		{"aws", "lambda", sim.KindAPIServer},
		{"aws", "rds", sim.KindDatabase},
		{"aws", "elasticache", sim.KindCache},
		{"aws", "sqs", sim.KindQueue},
		{"aws", "s3", sim.KindObjectStorage},
		{"aws", "cloudfront", sim.KindNetwork},
		{"cloudflare", "workers", sim.KindAPIServer},
		{"cloudflare", "kv", sim.KindCache},
		{"cloudflare", "r2", sim.KindObjectStorage},
		{"cloudflare", "queues", sim.KindQueue},
		{"gcp", "cloud_run", sim.KindAPIServer},
		{"gcp", "cloud_sql", sim.KindDatabase},
		{"gcp", "memorystore", sim.KindCache},
		{"gcp", "pub_sub", sim.KindQueue},
		{"gcp", "cloud_storage", sim.KindObjectStorage},
		{"gcp", "cloud_cdn", sim.KindNetwork},
	}
	for _, tc := range cases {
		rs, err := ResolveService("x", tc.provider, tc.service, Config{})
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.provider, tc.service, err)
		}
		if rs.Spec.Kind != tc.kind {
			t.Errorf("%s/%s: kind = %s, want %s", tc.provider, tc.service, rs.Spec.Kind, tc.kind)
		}
	}
}

func TestBuildSpecUnknownService(t *testing.T) {
	if _, err := ResolveService("x", "aws", "fargate", Config{}); err == nil {
		t.Fatal("unknown service must error")
	}
}

func TestConfigOverrides(t *testing.T) {
	rs, err := ResolveService("api", "aws", "ec2", Config{Concurrency: 50, QueueLimit: 10, StorageGB: 42})
	if err != nil {
		t.Fatal(err)
	}
	if rs.Spec.Concurrency != 50 || rs.Spec.QueueLimit != 10 {
		t.Fatalf("config overrides not applied: %+v", rs.Spec)
	}
	// Default path: no overrides → catalog defaults.
	def, err := ResolveService("api", "aws", "ec2", Config{})
	if err != nil {
		t.Fatal(err)
	}
	if def.Spec.Concurrency != 200 || def.Spec.QueueLimit != 100 {
		t.Fatalf("defaults not used: %+v", def.Spec)
	}
	if !containsStr(rs.Assumptions[0], "c6i") {
		t.Fatal("assumption trail must include capacity notes")
	}
}

func TestMemorySizingSlowsSmallerAllocations(t *testing.T) {
	small, err := ResolveService("fn", "aws", "lambda", Config{MemoryMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	big, err := ResolveService("fn", "aws", "lambda", Config{MemoryMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if small.Spec.DefaultServiceTimeMillis <= big.Spec.DefaultServiceTimeMillis {
		t.Fatalf("256MB (%.1fms) should be slower than 1024MB (%.1fms)",
			small.Spec.DefaultServiceTimeMillis, big.Spec.DefaultServiceTimeMillis)
	}
}

// --- Capacity estimation ---

func TestCapacityCeilingMath(t *testing.T) {
	// RDS: concurrency 8, 5ms service → ceiling = 8 / 0.005 = 1600 RPS.
	rs, err := ResolveService("db", "aws", "rds", Config{})
	if err != nil {
		t.Fatal(err)
	}
	ceil := modeledCeiling(rs)
	if math.Abs(ceil-1600) > 0.001 {
		t.Fatalf("rds ceiling = %f, want 1600", ceil)
	}
}

func TestCapacityHeadroomFields(t *testing.T) {
	res := fakeRunResult(t)
	specs := []ResolvedSpec{mustResolve(t, "db", "aws", "rds", Config{})}
	reports := EstimateCapacity(res, specs)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	cr := reports[0]
	if cr.CurrentRPS <= 0 {
		t.Fatal("current RPS must come from simulation output")
	}
	if cr.Headroom+cr.Utilization > 1.0001 || cr.Headroom+cr.Utilization < 0.9999 {
		t.Fatalf("headroom+utilization must be ~1: %f", cr.Headroom+cr.Utilization)
	}
	if len(cr.Assumptions) < 3 {
		t.Fatalf("assumptions must be explicit: %v", cr.Assumptions)
	}
}

// --- Pricing math ---

func TestPricingMathDeterministic(t *testing.T) {
	res := fakeRunResult(t)
	plan := mustPlan(t)
	specs := []ResolvedSpec{mustResolve(t, "db", "aws", "rds", Config{StorageGB: 100})}

	a := EstimateCost(res, specs, plan)
	b := EstimateCost(res, specs, plan)
	if a.Total != b.Total {
		t.Fatalf("cost estimates diverged: %f vs %f", a.Total, b.Total)
	}
	if !a.ESTIMATE || a.Currency != "USD" {
		t.Fatal("estimate must be marked ESTIMATE/USD")
	}

	// Hand-computed database storage line: 100 GB × $0.115.
	var dbCost float64
	for _, cc := range a.Components {
		for _, li := range cc.LineItems {
			if li.Category == CostDatabase {
				dbCost = li.MonthlyCost
			}
		}
	}
	if math.Abs(dbCost-100*0.115) > 1e-9 {
		t.Fatalf("db storage cost = %f, want %f", dbCost, 100*0.115)
	}

	// Category sum must equal total.
	var sum float64
	for _, cat := range allCategories {
		sum += a.ByCategory[cat]
	}
	if math.Abs(sum-a.Total) > 1e-9 {
		t.Fatalf("category sum %f != total %f", sum, a.Total)
	}
}

func TestFreeTierDeduction(t *testing.T) {
	// SQS: 1M free queue ops. Under 1M monthly ops → $0 after deduction.
	res := smallRunResult(t)
	plan := mustPlan(t)
	specs := []ResolvedSpec{mustResolve(t, "q", "aws", "sqs", Config{})}
	est := EstimateCost(res, specs, plan)
	for _, cc := range est.Components {
		for _, li := range cc.LineItems {
			if li.Unit == "operations" && li.Quantity < 1_000_000 && li.MonthlyCost != 0 {
				t.Fatalf("free tier not applied: %.1f ops cost $%f", li.Quantity, li.MonthlyCost)
			}
		}
	}
}

func TestEgressPricedFromPayload(t *testing.T) {
	res := fakeRunResult(t)
	plan := mustPlan(t)
	specs := []ResolvedSpec{mustResolve(t, "cdn", "aws", "cloudfront", Config{})}
	est := EstimateCost(res, specs, plan)
	found := false
	for _, cc := range est.Components {
		for _, li := range cc.LineItems {
			if li.Category == CostNetwork {
				found = true
				if li.Quantity <= 0 || li.MonthlyCost <= 0 {
					t.Fatal("egress must price from payload bytes")
				}
			}
		}
	}
	if !found {
		t.Fatal("CDN must produce a network line item")
	}
}

// --- Helpers ---

func mustResolve(t *testing.T, id, provider, service string, cfg Config) ResolvedSpec {
	t.Helper()
	rs, err := ResolveService(id, provider, service, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func mustPlan(t *testing.T) workload.Plan {
	t.Helper()
	p, err := workload.Derive(workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 5, ReadWriteRatio: 4, PayloadBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
} // fakeRunResult fabricates a small sim output for capacity/cost unit tests
// without running a full simulation. Fields mirror what the real snapshot
// fills in (computed averages included).
func fakeRunResult(t *testing.T) *sim.RunResult {
	t.Helper()
	return &sim.RunResult{
		Plan: mustPlan(t),
		Metrics: sim.Metrics{
			DurationMS: 60_000,
			Generated:  100_000, Completed: 100_000,
			Components: []sim.ComponentMetrics{
				{
					ID: "db", Kind: sim.KindDatabase,
					Arrived: 100_000, Completed: 100_000,
					ArrivalRPS: 100_000 / 60, Utilization: 0.6, AvgServiceMS: 5,
				},
				{
					ID: "cdn", Kind: sim.KindNetwork,
					Arrived: 100_000, Completed: 100_000,
					ArrivalRPS: 100_000 / 60, AvgServiceMS: 10,
				},
			},
		},
	}
}

// smallRunResult produces a light-load run (for free-tier tests).
func smallRunResult(t *testing.T) *sim.RunResult {
	t.Helper()
	return &sim.RunResult{
		Plan: mustPlan(t),
		Metrics: sim.Metrics{
			DurationMS: 60_000,
			Generated:  500, Completed: 500,
			Components: []sim.ComponentMetrics{{
				ID: "q", Kind: sim.KindQueue,
				Arrived: 500, Completed: 500, AvgServiceMS: 1,
			}},
		},
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
