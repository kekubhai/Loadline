package scoring

import (
	"math"
	"testing"
)

// evenWeights exercises every dimension equally.
var evenWeights = Weights{
	Throughput: 1, Latency: 1, Reliability: 1, Cost: 1, FailureRecovery: 1,
}

func TestComputePerfectAtTargets(t *testing.T) {
	in := Inputs{
		TargetThroughputRPS: 1000, AchievedRPS: 1000,
		TargetP95MS: 100, P95MS: 100,
		TargetP99MS: 200, P99MS: 200,
		TargetErrorRate: 0.01, ErrorRate: 0.01,
		MonthlyBudgetUSD: 1000, MonthlyCostUSD: 1000, CostKnown: true,
	}
	res := Compute(in, evenWeights)
	if res.Score != 100 {
		t.Fatalf("expected 100 at every target, got %v (%+v)", res.Score, res.Components)
	}
	for _, c := range res.Components {
		if c.Score != 100 {
			t.Errorf("component %s = %v, want 100", c.Name, c.Score)
		}
	}
}

func TestComputePoor(t *testing.T) {
	// Half the throughput, double the latency, 10x the errors, double budget.
	in := Inputs{
		TargetThroughputRPS: 1000, AchievedRPS: 500,
		TargetP95MS: 100, P95MS: 100,
		TargetP99MS: 200, P99MS: 400,
		TargetErrorRate: 0.01, ErrorRate: 0.10,
		MonthlyBudgetUSD: 1000, MonthlyCostUSD: 2000, CostKnown: true,
	}
	// Four assessed dimensions (failure recovery is excluded).
	res := Compute(in, Weights{Throughput: 1, Latency: 1, Reliability: 1, Cost: 1})
	// 50, 50, 10, 50 → mean 40.
	if res.Score != 40 {
		t.Fatalf("expected 40, got %v (%+v)", res.Score, res.Components)
	}
	if len(res.Negatives) == 0 {
		t.Error("expected negative evidence")
	}
}

func TestComputeClampsAtZeroAndHundred(t *testing.T) {
	// Absurdly bad numbers must clamp to 0, never go negative.
	bad := Inputs{
		TargetThroughputRPS: 1e6, AchievedRPS: 1,
		TargetP99MS: 1, P99MS: 1e6,
		TargetErrorRate: 0.001, ErrorRate: 1.0,
		MonthlyBudgetUSD: 1, MonthlyCostUSD: 1e9, CostKnown: true,
		HasFailure: true, BaselineP99MS: 1, FailureP99MS: 1e6,
		BaselineErrorRate: 0, FailureErrorRate: 1,
	}
	if got := Compute(bad, evenWeights).Score; got != 0 {
		t.Fatalf("expected 0, got %v", got)
	}
	// Overshooting a target must cap at 100, never exceed it.
	great := Inputs{
		TargetThroughputRPS: 100, AchievedRPS: 1e6,
		TargetP99MS: 1000, P99MS: 1,
		TargetErrorRate: 0.10, ErrorRate: 0,
		MonthlyBudgetUSD: 1e6, MonthlyCostUSD: 1, CostKnown: true,
		HasFailure: true, BaselineP99MS: 1000, FailureP99MS: 1000,
		BaselineErrorRate: 0, FailureErrorRate: 0,
	}
	if got := Compute(great, evenWeights).Score; got != 100 {
		t.Fatalf("expected 100, got %v", got)
	}
}

func TestCostUnknownScoresZero(t *testing.T) {
	in := Inputs{MonthlyBudgetUSD: 1000, CostKnown: false}
	c := costScore(in, 1)
	if c.Score != 0 {
		t.Fatalf("expected 0 for unknowable cost, got %v", c.Score)
	}
}

func TestZeroWeightRemovesDimension(t *testing.T) {
	in := Inputs{
		TargetThroughputRPS: 1000, AchievedRPS: 1, // terrible throughput
		MonthlyBudgetUSD: 1000, MonthlyCostUSD: 1000, CostKnown: true,
	}
	// Only cost is weighted; the poor throughput must not matter.
	res := Compute(in, Weights{Cost: 1})
	if res.Score != 100 {
		t.Fatalf("expected 100 with only cost weighted, got %v", res.Score)
	}
}

func TestWeightsAreNormalized(t *testing.T) {
	in := Inputs{TargetThroughputRPS: 100, AchievedRPS: 100}
	// Weights 3:1 must produce the same score as 0.75:0.25 when both
	// dimensions hit their target.
	a := Compute(in, Weights{Throughput: 3, Cost: 1})
	b := Compute(in, Weights{Throughput: 0.75, Cost: 0.25})
	if a.Score != b.Score {
		t.Fatalf("normalization mismatch: %v vs %v", a.Score, b.Score)
	}
}

func TestFailureRecoveryDegrades(t *testing.T) {
	clean := Inputs{
		HasFailure:    true,
		BaselineP99MS: 100, FailureP99MS: 100,
		BaselineErrorRate: 0, FailureErrorRate: 0,
	}
	if got := failureRecoveryScore(clean, 1).Score; got != 100 {
		t.Fatalf("no degradation should score 100, got %v", got)
	}
	worse := Inputs{
		HasFailure:    true,
		BaselineP99MS: 100, FailureP99MS: 200, // +100%
		BaselineErrorRate: 0, FailureErrorRate: 0.2,
	}
	got := failureRecoveryScore(worse, 1).Score
	// 0.5×1.0 latency penalty + 0.5×0.2 error penalty → 100×(1-0.5-0.1)=40.
	if got != 40 {
		t.Fatalf("expected 40, got %v", got)
	}
}

func TestComputeIsDeterministic(t *testing.T) {
	in := Inputs{
		TargetThroughputRPS: 5000, AchievedRPS: 4200,
		TargetP99MS: 300, P99MS: 412,
		TargetErrorRate: 0.01, ErrorRate: 0.004,
		MonthlyBudgetUSD: 5000, MonthlyCostUSD: 6100, CostKnown: true,
		HasFailure: true, BaselineP99MS: 300, FailureP99MS: 512,
		BaselineErrorRate: 0.002, FailureErrorRate: 0.02,
	}
	first := Compute(in, Weights{Throughput: 0.3, Latency: 0.25, Reliability: 0.2, Cost: 0.1, FailureRecovery: 0.15})
	for i := 0; i < 100; i++ {
		if got := Compute(in, Weights{Throughput: 0.3, Latency: 0.25, Reliability: 0.2, Cost: 0.1, FailureRecovery: 0.15}); got.Score != first.Score {
			t.Fatalf("non-deterministic: %v vs %v", got.Score, first.Score)
		}
	}
	if math.IsNaN(first.Score) || math.IsInf(first.Score, 0) {
		t.Fatalf("non-finite score: %v", first.Score)
	}
	if first.Score < 0 || first.Score > 100 {
		t.Fatalf("score out of range: %v", first.Score)
	}
}
