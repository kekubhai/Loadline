package arena

import (
	"fmt"

	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
	"github.com/kekubhai/Loadline/apps/simulator/workload"

	"github.com/kekubhai/Loadline/apps/simulator/internal/arena/scoring"
)

// Outcome is everything one benchmark execution produced. Every field comes
// from the simulation engine or the provider models; nothing is fabricated.
type Outcome struct {
	// Seed is the challenge's fixed seed, echoed so it can be persisted and
	// the run reproduced.
	Seed uint64
	// Plan is the derived workload chain (users → DAU → requests/day → RPS).
	Plan workload.Plan
	// Run is the full challenge-run result (the same value as Metrics, kept
	// whole so the persistence layer can store it with the shared code
	// path every other run uses).
	Run *sim.RunResult
	// Metrics is the measured result of the challenge run (workload and
	// standardized failures applied).
	Metrics sim.Metrics
	// BaselineMetrics is the same architecture and seed WITHOUT the
	// standardized failures. It is nil only when the challenge has no
	// failure scenarios (in which case Metrics already is the healthy run).
	BaselineMetrics *sim.Metrics
	Diagnosis       sim.Diagnosis
	Capacity        []providers.CapacityReport
	Cost            *providers.CostEstimate
	Score           scoring.Result
}

// CheckRequirements reports whether the architecture satisfies the
// challenge's hard structural rules: every required kind is present at least
// the required number of times, and every failure scenario has a target
// component to act on. A submission that fails this is rejected, not scored.
func (d Definition) CheckRequirements(arch sim.Architecture) error {
	counts := map[sim.ComponentKind]int{}
	for _, c := range arch.Components {
		counts[c.Kind]++
	}
	for _, r := range d.Requirements.RequiredKinds {
		if counts[sim.ComponentKind(r.Kind)] < r.Min {
			return fmt.Errorf("challenge requires at least %d %s component(s); architecture has %d",
				r.Min, r.Kind, counts[sim.ComponentKind(r.Kind)])
		}
	}
	for i, f := range d.Failures {
		if resolveFailureTarget(arch, f.TargetKind) == "" {
			return fmt.Errorf("failure %d targets a %s component, but the architecture has none",
				i, f.TargetKind)
		}
	}
	return nil
}

// resolveFailureTarget returns the id of the architecture's first component
// of the given kind, or "" when there is none. "First" follows architecture
// declaration order, so the choice is deterministic.
func resolveFailureTarget(arch sim.Architecture, kind string) string {
	for _, c := range arch.Components {
		if string(c.Kind) == kind {
			return c.ID
		}
	}
	return ""
}

// options builds the engine options for a run, optionally applying the
// challenge's standardized failures. The workload, failures, retry policy,
// and seed all come from the definition — never from the client.
func (d Definition) options(arch sim.Architecture, withFailures bool) sim.Options {
	duration := d.Workload.DurationMS
	if duration <= 0 {
		duration = DefaultDurationMS
	}
	opts := sim.Options{
		Seed:       d.Seed,
		DurationMS: duration,
		Retry: sim.RetryPolicy{
			MaxRetries:    d.Run.MaxRetries,
			BackoffBaseMS: d.Run.BackoffBaseMS,
			TimeoutMS:     d.Run.TimeoutMS,
		},
		TrackWindows: int(d.Run.WindowMS),
	}
	if opts.TrackWindows <= 0 {
		opts.TrackWindows = int(DefaultWindowMS)
	}
	// Retry on every component: retries only trigger for requests a caller
	// forwarded and that failed, so listing all ids is safe and uniform.
	for _, c := range arch.Components {
		opts.RetryOn = append(opts.RetryOn, c.ID)
	}
	if !withFailures {
		return opts
	}
	for _, f := range d.Failures {
		opts.Failures = append(opts.Failures, sim.Failure{
			Target:     resolveFailureTarget(arch, f.TargetKind),
			Type:       sim.FailureType(f.Type),
			StartMS:    f.StartMS,
			DurationMS: f.DurationMS,
			Config: sim.FailureConfig{
				AddedLatencyMillis: f.AddedLatencyMS,
				ErrorRate:          f.ErrorRate,
				PacketLossRate:     f.PacketLossRate,
				PassThrough:        f.PassThrough,
			},
		})
	}
	return opts
}

// Run executes the benchmark on an already-resolved architecture. The
// caller (the ArenaService) performs proto → domain conversion so this
// package never touches transport types.
//
// Determinism: the seed is fixed by the challenge, so the same
// (architecture, challenge) always produces the same result.
func Run(d Definition, arch sim.Architecture, resolved []providers.ResolvedSpec) (*Outcome, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if err := arch.Validate(); err != nil {
		return nil, fmt.Errorf("architecture: %w", err)
	}
	if err := d.CheckRequirements(arch); err != nil {
		return nil, err
	}

	spec := d.Workload.Spec()
	plan, err := workload.Derive(spec)
	if err != nil {
		return nil, fmt.Errorf("workload: %w", err)
	}

	baseline, err := sim.Simulate(arch, spec, d.options(arch, false))
	if err != nil {
		return nil, fmt.Errorf("baseline simulation: %w", err)
	}
	challenge := baseline
	if len(d.Failures) > 0 {
		challenge, err = sim.Simulate(arch, spec, d.options(arch, true))
		if err != nil {
			return nil, fmt.Errorf("challenge simulation: %w", err)
		}
	}

	out := &Outcome{Seed: d.Seed, Run: challenge, Plan: baseline.Plan, Metrics: challenge.Metrics}
	if len(d.Failures) > 0 {
		out.BaselineMetrics = &baseline.Metrics
		out.Diagnosis = sim.DiagnoseWithBaseline(challenge.Metrics, baseline.Metrics)
	} else {
		out.Diagnosis = sim.Diagnose(challenge.Metrics)
	}

	if len(resolved) > 0 {
		out.Capacity = providers.EstimateCapacity(challenge, resolved)
		providers.SortCapacity(out.Capacity)
		out.Cost = providers.EstimateCost(challenge, resolved, plan)
	}

	out.Score = score(d, out)
	return out, nil
}

// score assembles the scoring inputs from measured results and the
// challenge's targets, then delegates to the pure scoring package.
func score(d Definition, out *Outcome) scoring.Result {
	in := scoring.Inputs{
		TargetThroughputRPS: d.Constraints.TargetThroughputRPS,
		TargetP95MS:         d.Constraints.TargetP95MS,
		TargetP99MS:         d.Constraints.TargetP99MS,
		TargetErrorRate:     d.Constraints.TargetErrorRate,
		MonthlyBudgetUSD:    d.Constraints.MonthlyBudgetUSD,
		AchievedRPS:         achievedRPS(out.Metrics),
		P95MS:               out.Metrics.P95MS,
		P99MS:               out.Metrics.P99MS,
		ErrorRate:           out.Metrics.ErrorRate,
	}
	if out.Cost != nil {
		in.MonthlyCostUSD = out.Cost.Total
		in.CostKnown = true
	}
	if out.BaselineMetrics != nil {
		in.HasFailure = true
		in.BaselineP99MS = out.BaselineMetrics.P99MS
		in.FailureP99MS = out.Metrics.P99MS
		in.BaselineErrorRate = out.BaselineMetrics.ErrorRate
		in.FailureErrorRate = out.Metrics.ErrorRate
	}
	return scoring.Compute(in, scoring.Weights{
		Throughput:      d.Scoring.Throughput,
		Latency:         d.Scoring.Latency,
		Reliability:     d.Scoring.Reliability,
		Cost:            d.Scoring.Cost,
		FailureRecovery: d.Scoring.FailureRecovery,
	})
}

// achievedRPS is the system's measured completion rate over the run window.
func achievedRPS(m sim.Metrics) float64 {
	sec := m.DurationMS / 1000
	if sec <= 0 {
		return 0
	}
	return float64(m.Completed) / sec
}
