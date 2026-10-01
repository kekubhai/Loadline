// Package scoring turns measured simulation results into a deterministic,
// explainable 0–100 benchmark score.
//
// It is a pure package: it imports nothing from the engine, the database, or
// the transport layer, and it performs no I/O. Given the same Inputs and
// Weights it always returns the same Result. Every component score has a
// documented formula and a human-readable derivation, so a leaderboard row
// can always be explained from the numbers that produced it.
//
// Scoring is NOT a judgement of architecture quality. It measures how a
// design performed under one standardized benchmark. Different designs may
// trade off differently; the benchmark is the only authority.
package scoring

import (
	"fmt"
	"math"
)

// Weights are the per-dimension weights. They need not sum to 1 — Compute
// normalizes them — and a zero weight removes the dimension from the final
// score (it is still reported with its component score).
type Weights struct {
	Throughput      float64
	Latency         float64
	Reliability     float64
	Cost            float64
	FailureRecovery float64
}

// Inputs are the measured results and the challenge's targets. A target of 0
// means "not assessed" for that dimension.
type Inputs struct {
	// Targets (challenge constraints).
	TargetThroughputRPS float64
	TargetP95MS         float64
	TargetP99MS         float64
	TargetErrorRate     float64
	MonthlyBudgetUSD    float64

	// Measured, from the challenge run (workload + failures).
	AchievedRPS    float64
	P95MS          float64
	P99MS          float64
	ErrorRate      float64
	MonthlyCostUSD float64
	// CostKnown is false when the architecture has no provider-backed
	// components, so no monthly estimate exists. The budget cannot then be
	// verified, and the cost dimension scores 0 rather than guessing.
	CostKnown bool

	// Failure comparison: the challenge run (with the standardized failures)
	// versus a baseline run of the SAME architecture + seed WITHOUT failures.
	HasFailure        bool
	BaselineP99MS     float64
	FailureP99MS      float64
	BaselineErrorRate float64
	FailureErrorRate  float64
}

// ComponentScore is one dimension's normalized score and its derivation.
type ComponentScore struct {
	Name     string
	Score    float64 // 0..100
	Weight   float64 // normalized 0..1
	Weighted float64 // Score × Weight
	Detail   string
	Evidence []string
}

// Result is the final, explainable score.
type Result struct {
	Score      float64 // 0..100
	Components []ComponentScore
	Positives  []string
	Negatives  []string
}

// Compute calculates the weighted score. It clamps every component to
// [0,100] and the final score to [0,100].
func Compute(in Inputs, w Weights) Result {
	weights := normalize(w)
	components := []ComponentScore{
		throughputScore(in, weights.Throughput),
		latencyScore(in, weights.Latency),
		reliabilityScore(in, weights.Reliability),
		costScore(in, weights.Cost),
		failureRecoveryScore(in, weights.FailureRecovery),
	}
	res := Result{Components: components}
	var total float64
	for i := range components {
		components[i].Weighted = round1(components[i].Score * components[i].Weight)
		if components[i].Weight > 0 {
			total += components[i].Score * components[i].Weight
		}
		if components[i].Score >= 70 {
			for _, e := range components[i].Evidence {
				res.Positives = append(res.Positives, e)
			}
		} else if components[i].Weight > 0 {
			for _, e := range components[i].Evidence {
				res.Negatives = append(res.Negatives, e)
			}
		}
	}
	res.Score = round1(clamp(total, 0, 100))
	return res
}

// normalize scales the weights so they sum to 1. A non-positive total yields
// all-zero weights (the caller's definition is invalid; Validate rejects it
// before a benchmark ever runs).
func normalize(w Weights) Weights {
	total := w.Throughput + w.Latency + w.Reliability + w.Cost + w.FailureRecovery
	if total <= 0 {
		return Weights{}
	}
	return Weights{
		Throughput:      w.Throughput / total,
		Latency:         w.Latency / total,
		Reliability:     w.Reliability / total,
		Cost:            w.Cost / total,
		FailureRecovery: w.FailureRecovery / total,
	}
}

func throughputScore(in Inputs, weight float64) ComponentScore {
	c := ComponentScore{Name: "Throughput", Weight: weight}
	if in.TargetThroughputRPS <= 0 {
		c.Score = 100
		c.Detail = "not assessed"
		return c
	}
	ratio := in.AchievedRPS / in.TargetThroughputRPS
	c.Score = round1(clamp(ratio*100, 0, 100))
	c.Detail = "achieved RPS ÷ target RPS"
	c.Evidence = []string{
		sprintf("throughput %.0f RPS vs target %.0f RPS (%.0f%%)",
			in.AchievedRPS, in.TargetThroughputRPS, ratio*100),
	}
	return c
}

func latencyScore(in Inputs, weight float64) ComponentScore {
	c := ComponentScore{Name: "Latency", Weight: weight}
	assessed := false
	worst := 100.0
	if in.TargetP95MS > 0 && in.P95MS > 0 {
		assessed = true
		s := round1(clamp(in.TargetP95MS/in.P95MS*100, 0, 100))
		if s < worst {
			worst = s
		}
		if in.P95MS > in.TargetP95MS {
			c.Evidence = append(c.Evidence, sprintf("p95 %.1fms over target %.1fms", in.P95MS, in.TargetP95MS))
		} else {
			c.Evidence = append(c.Evidence, sprintf("p95 %.1fms at or under target %.1fms", in.P95MS, in.TargetP95MS))
		}
	}
	if in.TargetP99MS > 0 && in.P99MS > 0 {
		assessed = true
		s := round1(clamp(in.TargetP99MS/in.P99MS*100, 0, 100))
		if s < worst {
			worst = s
		}
		if in.P99MS > in.TargetP99MS {
			c.Evidence = append(c.Evidence, sprintf("p99 %.1fms over target %.1fms", in.P99MS, in.TargetP99MS))
		} else {
			c.Evidence = append(c.Evidence, sprintf("p99 %.1fms at or under target %.1fms", in.P99MS, in.TargetP99MS))
		}
	}
	if !assessed {
		c.Score = 100
		c.Detail = "not assessed"
		return c
	}
	c.Score = worst
	c.Detail = "target ÷ measured (worst of p95/p99)"
	return c
}

func reliabilityScore(in Inputs, weight float64) ComponentScore {
	c := ComponentScore{Name: "Reliability", Weight: weight}
	if in.TargetErrorRate <= 0 {
		c.Score = round1(clamp((1-in.ErrorRate)*100, 0, 100))
		c.Detail = "1 − error rate (no error target set)"
		c.Evidence = []string{sprintf("error rate %.2f%% (no target; scored as 1 − error rate)", in.ErrorRate*100)}
		return c
	}
	if in.ErrorRate <= in.TargetErrorRate {
		c.Score = 100
		c.Detail = "target ÷ measured error rate"
		c.Evidence = []string{sprintf("error rate %.2f%% within target %.2f%%", in.ErrorRate*100, in.TargetErrorRate*100)}
		return c
	}
	c.Score = round1(clamp(in.TargetErrorRate/in.ErrorRate*100, 0, 100))
	c.Detail = "target ÷ measured error rate"
	c.Evidence = []string{sprintf("error rate %.2f%% over target %.2f%%", in.ErrorRate*100, in.TargetErrorRate*100)}
	return c
}

func costScore(in Inputs, weight float64) ComponentScore {
	c := ComponentScore{Name: "Cost", Weight: weight}
	if in.MonthlyBudgetUSD <= 0 {
		c.Score = 100
		c.Detail = "not assessed"
		return c
	}
	if !in.CostKnown {
		c.Score = 0
		c.Detail = "cost could not be estimated"
		c.Evidence = []string{"no monthly cost estimate (no provider-backed components); budget cannot be verified"}
		return c
	}
	if in.MonthlyCostUSD <= in.MonthlyBudgetUSD {
		c.Score = 100
		c.Detail = "budget ÷ estimate"
		c.Evidence = []string{sprintf("monthly cost $%.0f within budget $%.0f", in.MonthlyCostUSD, in.MonthlyBudgetUSD)}
		return c
	}
	c.Score = round1(clamp(in.MonthlyBudgetUSD/in.MonthlyCostUSD*100, 0, 100))
	c.Detail = "budget ÷ estimate"
	c.Evidence = []string{sprintf("monthly cost $%.0f over budget $%.0f", in.MonthlyCostUSD, in.MonthlyBudgetUSD)}
	return c
}

func failureRecoveryScore(in Inputs, weight float64) ComponentScore {
	c := ComponentScore{Name: "Failure recovery", Weight: weight}
	if !in.HasFailure {
		c.Score = 100
		c.Detail = "no failure scenario"
		return c
	}
	latencyPenalty := 0.0
	if in.BaselineP99MS > 0 {
		latencyPenalty = clamp((in.FailureP99MS-in.BaselineP99MS)/in.BaselineP99MS, 0, 1)
	}
	errorPenalty := clamp(in.FailureErrorRate-in.BaselineErrorRate, 0, 1)
	c.Score = round1(clamp(100*(1-0.5*latencyPenalty-0.5*errorPenalty), 0, 100))
	c.Detail = "degradation vs same architecture without failures"
	c.Evidence = []string{
		sprintf("p99 moved %.1fms → %.1fms during the failure (+%.0f%%)",
			in.BaselineP99MS, in.FailureP99MS, latencyPenalty*100),
		sprintf("error rate moved %.2f%% → %.2f%% during the failure",
			in.BaselineErrorRate*100, in.FailureErrorRate*100),
	}
	return c
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Max(lo, math.Min(hi, v))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// sprintf keeps the derivation strings in one place.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
