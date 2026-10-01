package arena

import (
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

func TestEmbeddedDefinitionsLoadAndValidate(t *testing.T) {
	defs, err := Definitions()
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if len(defs) != 5 {
		t.Fatalf("expected 5 challenges, got %d", len(defs))
	}
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			t.Errorf("challenge %s invalid: %v", d.Slug, err)
		}
		if d.Status != StatusActive {
			t.Errorf("challenge %s status = %s, want active", d.Slug, d.Status)
		}
	}
	if _, ok := BySlug("social-feed"); !ok {
		t.Error("expected social-feed challenge")
	}
	if _, ok := BySlug("nope"); ok {
		t.Error("unexpected challenge for unknown slug")
	}
}

// testArch builds a provider-backed client → api → cache → db architecture.
func testArch(t *testing.T) (sim.Architecture, []providers.ResolvedSpec) {
	t.Helper()
	api, err := providers.ResolveService("api", "aws", "lambda", providers.Config{MemoryMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := providers.ResolveService("cache", "aws", "elasticache", providers.Config{HitRatio: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	db, err := providers.ResolveService("db", "aws", "rds", providers.Config{StorageGB: 100})
	if err != nil {
		t.Fatal(err)
	}
	arch := sim.Architecture{
		Name: "arena-test",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			api.Spec, cache.Spec, db.Spec,
		},
		Links: []sim.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
	return arch, []providers.ResolvedSpec{api, cache, db}
}

func testDefinition() Definition {
	return Definition{
		Slug: "test-challenge", Version: 1, Name: "Test", Description: "d",
		Difficulty: DifficultyBeginner, Category: CategoryAPI, Status: StatusActive,
		Workload: Workload{
			TotalUsers: 10000, DAU: 1000, RequestsPerUserPerDay: 10,
			PeakMultiplier: 2, ReadWriteRatio: 4, PayloadBytes: 1024, DurationMS: 2000,
		},
		Constraints: Constraints{
			TargetThroughputRPS: 50, TargetP99MS: 500, TargetErrorRate: 0.05, MonthlyBudgetUSD: 100000,
		},
		Requirements: Requirements{RequiredKinds: []KindRequirement{{Kind: "cache", Min: 1}, {Kind: "database", Min: 1}}},
		Failures: []FailureScenario{{
			TargetKind: "cache", Type: string(sim.FailureCrash),
			StartMS: 500, DurationMS: 500, PassThrough: true,
		}},
		Scoring: Weights{Throughput: 1, Latency: 1, Reliability: 1, Cost: 1, FailureRecovery: 1},
		Seed:    7,
	}
}

func TestRunIsDeterministic(t *testing.T) {
	arch, resolved := testArch(t)
	def := testDefinition()

	first, err := Run(def, arch, resolved)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Run(def, arch, resolved)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.Metrics.Generated != second.Metrics.Generated ||
		first.Metrics.Completed != second.Metrics.Completed ||
		first.Metrics.P99MS != second.Metrics.P99MS {
		t.Fatalf("non-deterministic run: %+v vs %+v", first.Metrics, second.Metrics)
	}
	if first.Score.Score != second.Score.Score {
		t.Fatalf("non-deterministic score: %v vs %v", first.Score.Score, second.Score.Score)
	}
}

func TestRunProducesBaselineAndArtifacts(t *testing.T) {
	arch, resolved := testArch(t)
	out, err := Run(testDefinition(), arch, resolved)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Run == nil {
		t.Fatal("expected the challenge run result for persistence")
	}
	if out.BaselineMetrics == nil {
		t.Fatal("expected a baseline (no-failure) run for the failure metric")
	}
	if out.Cost == nil {
		t.Fatal("expected a monthly cost estimate for a provider-backed architecture")
	}
	if len(out.Capacity) != 3 {
		t.Fatalf("expected 3 capacity reports, got %d", len(out.Capacity))
	}
	if out.Score.Score < 0 || out.Score.Score > 100 {
		t.Fatalf("score out of range: %v", out.Score.Score)
	}
}

func TestCheckRequirementsRejectsMissingKind(t *testing.T) {
	def := testDefinition()
	// An architecture without a cache fails the cache requirement.
	arch := sim.Architecture{
		Name: "no-cache",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			{ID: "db", Kind: sim.KindDatabase},
		},
		Links: []sim.Link{{From: "client", To: "db"}},
	}
	if err := def.CheckRequirements(arch); err == nil {
		t.Fatal("expected a requirement failure for a missing cache")
	}
	if _, err := Run(def, arch, nil); err == nil {
		t.Fatal("Run must reject an architecture that does not meet requirements")
	}
}

func TestValidateDisplayName(t *testing.T) {
	good := []string{"Rahul", "Anirban", "Priya S", "  ok  "}
	for _, n := range good {
		if _, err := ValidateDisplayName(n); err != nil {
			t.Errorf("ValidateDisplayName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", " ", "a", "has\nnewline", "tab\there", string(make([]byte, 40))}
	for _, n := range bad {
		if _, err := ValidateDisplayName(n); err == nil {
			t.Errorf("ValidateDisplayName(%q) = nil, want error", n)
		}
	}
}
