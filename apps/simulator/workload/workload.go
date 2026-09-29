// Package workload turns human-scale assumptions into a derived load plan.
//
// The derivation is a pure function: no RNG, no engine, no I/O. Every
// intermediate value is kept in the Plan so reports and tests can inspect
// exactly how "1M users" became "peak RPS" — no hidden jumps.
package workload

import (
	"fmt"
	"math"
)

// Spec is the user-facing workload definition.
type Spec struct {
	// TotalUsers is the registered/total user population.
	TotalUsers int64
	// DAU is the daily active user count (<= TotalUsers).
	DAU int64
	// RequestsPerUserPerDay is the average number of requests one active
	// user issues per day.
	RequestsPerUserPerDay float64
	// PeakMultiplier scales average RPS up to the peak the architecture
	// must survive. >= 1.
	PeakMultiplier float64
	// ReadWriteRatio is reads per write, e.g. 4 means 80% read / 20% write.
	// Must be > 0.
	ReadWriteRatio float64
	// PayloadBytes is the modeled request/response payload size.
	PayloadBytes int64
}

// Plan is the derived, inspectable load plan. All values are deterministic
// functions of the Spec.
type Plan struct {
	TotalUsers            int64
	DAU                   int64
	DAUFraction           float64
	RequestsPerUserPerDay float64
	RequestsPerDay        float64
	AverageRPS            float64
	PeakMultiplier        float64
	PeakRPS               float64
	ReadFraction          float64
	WriteFraction         float64
	PayloadBytes          int64

	// MeanInterArrivalMillis is the mean gap between consecutive request
	// arrivals at peak load: 1000 / PeakRPS. The Poisson arrival process in
	// the simulation draws exponential gaps with this mean.
	MeanInterArrivalMillis float64
}

// SecondsPerDay is the divisor for requests/day → average RPS.
const SecondsPerDay = 86400.0

// finite reports whether v is neither NaN nor ±Inf. Range comparisons
// are false for NaN, so every numeric field needs this check or it
// would sail through validation and poison the derived plan.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Validate checks the spec's basic sanity.
func (s Spec) Validate() error {
	if s.TotalUsers <= 0 {
		return fmt.Errorf("workload: TotalUsers must be > 0, got %d", s.TotalUsers)
	}
	if s.DAU <= 0 {
		return fmt.Errorf("workload: DAU must be > 0, got %d", s.DAU)
	}
	if s.DAU > s.TotalUsers {
		return fmt.Errorf("workload: DAU (%d) cannot exceed TotalUsers (%d)", s.DAU, s.TotalUsers)
	}
	if !finite(s.RequestsPerUserPerDay) {
		return fmt.Errorf("workload: RequestsPerUserPerDay must be a finite number, got %v", s.RequestsPerUserPerDay)
	}
	if s.RequestsPerUserPerDay <= 0 {
		return fmt.Errorf("workload: RequestsPerUserPerDay must be > 0, got %f", s.RequestsPerUserPerDay)
	}
	if !finite(s.PeakMultiplier) {
		return fmt.Errorf("workload: PeakMultiplier must be a finite number, got %v", s.PeakMultiplier)
	}
	if s.PeakMultiplier < 1 {
		return fmt.Errorf("workload: PeakMultiplier must be >= 1, got %f", s.PeakMultiplier)
	}
	if !finite(s.ReadWriteRatio) {
		return fmt.Errorf("workload: ReadWriteRatio must be a finite number, got %v", s.ReadWriteRatio)
	}
	if s.ReadWriteRatio <= 0 {
		return fmt.Errorf("workload: ReadWriteRatio must be > 0, got %f", s.ReadWriteRatio)
	}
	if s.PayloadBytes < 0 {
		return fmt.Errorf("workload: PayloadBytes cannot be negative, got %d", s.PayloadBytes)
	}
	return nil
}

// Derive computes the load plan from the spec. It returns an error if the
// spec is invalid; it never invents values.
func Derive(spec Spec) (Plan, error) {
	if err := spec.Validate(); err != nil {
		return Plan{}, err
	}
	p := Plan{
		TotalUsers:            spec.TotalUsers,
		DAU:                   spec.DAU,
		DAUFraction:           float64(spec.DAU) / float64(spec.TotalUsers),
		RequestsPerUserPerDay: spec.RequestsPerUserPerDay,
		PeakMultiplier:        spec.PeakMultiplier,
		PayloadBytes:          spec.PayloadBytes,
	}
	p.RequestsPerDay = float64(spec.DAU) * spec.RequestsPerUserPerDay
	p.AverageRPS = p.RequestsPerDay / SecondsPerDay
	p.PeakRPS = p.AverageRPS * spec.PeakMultiplier
	if !finite(p.PeakRPS) {
		return Plan{}, fmt.Errorf(
			"user workload: derived peak RPS is not finite (%v) — the user count or requests per user per day overflows float64", p.PeakRPS)
	}
	p.ReadFraction = spec.ReadWriteRatio / (spec.ReadWriteRatio + 1)
	p.WriteFraction = 1 - p.ReadFraction
	p.MeanInterArrivalMillis = 1000.0 / p.PeakRPS
	return p, nil
}
