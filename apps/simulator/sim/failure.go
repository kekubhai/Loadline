package sim

import (
	"fmt"
	"math"
)

// finite reports whether v is neither NaN nor ±Inf. Every numeric check
// in this package must go through it: range comparisons are false for
// NaN, which would otherwise let poisoned values pass validation.
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// FailureType enumerates the V1 failure injections.
type FailureType string

const (
	// FailureCrash makes the target component unable to serve: arriving
	// requests either fail at the component (default) or pass through it
	// untouched (PassThrough), e.g. a cache whose callers fall back to the
	// origin. Which of the two happens is configuration, not hardcoded
	// behavior.
	FailureCrash FailureType = "crash"
	// FailureLatency adds a fixed service-time penalty to every request the
	// component serves while the failure is active.
	FailureLatency FailureType = "increased_latency"
	// FailureErrorRate makes a fraction of requests that finish service at
	// the component fail.
	FailureErrorRate FailureType = "increased_error_rate"
	// FailureNetwork inflates transit time through the component and drops
	// a fraction of requests (packet loss) before service.
	FailureNetwork FailureType = "network_failure"
)

// FailureConfig carries the parameters of one injection. Only the fields
// relevant to the failure's type are consumed; irrelevant ones are ignored.
type FailureConfig struct {
	// AddedLatencyMillis (FailureLatency, FailureNetwork): service-time
	// penalty while active.
	AddedLatencyMillis float64
	// ErrorRate (FailureErrorRate): fraction 0..1 of serviced requests that
	// fail.
	ErrorRate float64
	// PacketLossRate (FailureNetwork): fraction 0..1 of arriving requests
	// dropped before service.
	PacketLossRate float64
	// PassThrough (FailureCrash): when true, the failed component forwards
	// requests downstream instead of failing them (cache → origin fallback).
	// When false, requests fail at the component.
	PassThrough bool
}

// Failure is one injection: what, where, when, and how.
type Failure struct {
	// Target component ID (must exist in the architecture).
	Target string
	// Type of failure.
	Type FailureType
	// StartMS is the simulated instant the failure begins (>= 0).
	StartMS float64
	// DurationMS is how long it lasts (> 0).
	DurationMS float64
	// Config holds the type-specific parameters.
	Config FailureConfig
}

func (f Failure) validate(known map[string]ComponentSpec) error {
	if _, ok := known[f.Target]; !ok {
		return fmt.Errorf("failure: unknown target component %q", f.Target)
	}
	// NaN slips through every range comparison below (all of them are
	// false for NaN), so reject non-finite parameters explicitly first:
	// a NaN injected at run time would poison service times and metrics.
	if !finite(f.StartMS) || !finite(f.DurationMS) {
		return fmt.Errorf("failure on %s: StartMS and DurationMS must be finite, got StartMS=%v DurationMS=%v",
			f.Target, f.StartMS, f.DurationMS)
	}
	if f.StartMS < 0 {
		return fmt.Errorf("failure on %s: StartMS cannot be negative", f.Target)
	}
	if f.DurationMS <= 0 {
		return fmt.Errorf("failure on %s: DurationMS must be > 0", f.Target)
	}
	switch f.Type {
	case FailureCrash:
		// no extra config required
	case FailureLatency:
		if !finite(f.Config.AddedLatencyMillis) || f.Config.AddedLatencyMillis <= 0 {
			return fmt.Errorf("failure on %s: AddedLatencyMillis must be a finite value > 0 for increased_latency, got %v",
				f.Target, f.Config.AddedLatencyMillis)
		}
	case FailureErrorRate:
		if !finite(f.Config.ErrorRate) || f.Config.ErrorRate < 0 || f.Config.ErrorRate > 1 {
			return fmt.Errorf("failure on %s: ErrorRate must be a finite value in [0,1], got %v", f.Target, f.Config.ErrorRate)
		}
	case FailureNetwork:
		if !finite(f.Config.AddedLatencyMillis) || !finite(f.Config.PacketLossRate) ||
			f.Config.AddedLatencyMillis < 0 || f.Config.PacketLossRate < 0 || f.Config.PacketLossRate > 1 {
			return fmt.Errorf("failure on %s: network failure needs finite AddedLatencyMillis >= 0 and PacketLossRate in [0,1], got AddedLatencyMillis=%v PacketLossRate=%v",
				f.Target, f.Config.AddedLatencyMillis, f.Config.PacketLossRate)
		}
	default:
		return fmt.Errorf("failure on %s: unknown type %q", f.Target, f.Type)
	}
	return nil
}

// failState is the active failure on a component runtime during a run.
type failState struct {
	failure Failure
}

func (fs *failState) active() bool { return fs != nil }
