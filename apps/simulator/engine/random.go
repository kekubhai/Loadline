package engine

import "math"

// RNG is a deterministic pseudo-random number generator for simulation
// logic (jitter, failure rolls, workload mixes, ...).
//
// It implements SplitMix64 (Steele, Lea & Flood, 2014 — the reference PRNG
// for Java's SplittableRandom seeding): tiny, fast, and fully specified, so
// identical seeds produce identical streams on every platform and Go
// release. The engine never touches global rand state, which keeps
// simulations reproducible.
type RNG struct {
	state uint64
}

// NewRNG returns a generator seeded with the given seed. The same seed
// always yields the same sequence of values.
func NewRNG(seed uint64) *RNG {
	return &RNG{state: seed}
}

// Next advances the generator and returns the raw 64-bit value.
func (r *RNG) Next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Float64 returns a uniform value in [0, 1).
func (r *RNG) Float64() float64 {
	// 53 random mantissa bits: uniform over all representable doubles in [0,1).
	return float64(r.Next()>>11) / float64(1<<53)
}

// Intn returns a uniform value in [0, n). Panics if n <= 0.
func (r *RNG) Intn(n int) int {
	if n <= 0 {
		panic("engine: RNG.Intn requires n > 0")
	}
	// Rejection sampling on the high bits avoids modulo bias.
	limit := uint64(1)<<63 - 1
	bound := uint64(n)
	threshold := (limit + 1 - bound) % bound // largest multiple-of-bound region start
	for {
		v := r.Next() >> 1 // 63-bit value
		if v >= threshold {
			return int(v % bound)
		}
	}
}

// ExpFloat64 returns an exponentially distributed value with rate λ = 1
// (mean 1). Use it for inter-arrival times in Poisson processes: multiply
// by the desired mean.
func (r *RNG) ExpFloat64() float64 {
	// log(1 - u) with u in [0,1); 1-u avoids log(0).
	// math.Log is part of the stdlib contract: same input, same output.
	u := 1 - r.Float64()
	return -math.Log(u)
}
