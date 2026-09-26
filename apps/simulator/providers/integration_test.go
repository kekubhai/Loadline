package providers

import (
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// End-to-end: resolve catalog services → build a generic architecture →
// simulate → estimate capacity and cost from the run's own outputs. This
// is the full Step 4 pipeline without any UI.
func TestProviderPipelineEndToEnd(t *testing.T) {
	// 1. Resolve services from the catalog with typed config.
	cacheRS, err := ResolveService("cache", "aws", "elasticache", Config{HitRatio: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	apiRS, err := ResolveService("api", "aws", "lambda", Config{MemoryMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	dbRS, err := ResolveService("db", "aws", "rds", Config{StorageGB: 100})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Assemble the generic architecture (client is generic; the
	//    simulator remains provider-agnostic).
	arch := sim.Architecture{
		Name: "aws-web",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			apiRS.Spec, cacheRS.Spec, dbRS.Spec,
		},
		Links: []sim.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
	if err := arch.Validate(); err != nil {
		t.Fatalf("provider-built architecture must validate: %v", err)
	}

	// 3. Simulate with a moderate workload.
	spec := workload.Spec{
		TotalUsers: 1_000_000, DAU: 50_000, RequestsPerUserPerDay: 10,
		PeakMultiplier: 3, ReadWriteRatio: 4, PayloadBytes: 4096,
	}
	res, err := sim.Simulate(arch, spec, sim.Options{Seed: 7, DurationMS: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.Completed == 0 {
		t.Fatal("pipeline run must complete requests")
	}

	// 4. Capacity from real outputs.
	reports := EstimateCapacity(res, []ResolvedSpec{apiRS, cacheRS, dbRS})
	if len(reports) != 3 {
		t.Fatalf("expected 3 capacity reports, got %d", len(reports))
	}
	for _, cr := range reports {
		if cr.CurrentRPS <= 0 {
			t.Errorf("%s: capacity report missing measured RPS", cr.ComponentID)
		}
		if len(cr.Assumptions) == 0 {
			t.Errorf("%s: capacity assumptions missing", cr.ComponentID)
		}
	}

	// 5. Cost from real outputs; all categories accounted.
	plan := res.Plan
	est := EstimateCost(res, []ResolvedSpec{apiRS, cacheRS, dbRS}, plan)
	if est.Total <= 0 {
		t.Fatal("estimate total must be positive")
	}
	if !est.ESTIMATE {
		t.Fatal("estimate must be marked")
	}
	hasCompute := est.ByCategory[CostCompute] > 0
	hasRequests := est.ByCategory[CostRequests] > 0
	hasDatabase := est.ByCategory[CostDatabase] > 0
	if !hasCompute || !hasRequests || !hasDatabase {
		t.Fatalf("expected compute+requests+database costs, got %+v", est.ByCategory)
	}
	if len(est.Assumptions) < 3 {
		t.Fatalf("cost assumptions must be explicit: %v", est.Assumptions)
	}

	// 6. Determinism: same inputs → identical estimate.
	est2 := EstimateCost(res, []ResolvedSpec{apiRS, cacheRS, dbRS}, plan)
	if est.Total != est2.Total {
		t.Fatalf("cost not deterministic: %f vs %f", est.Total, est2.Total)
	}
}
