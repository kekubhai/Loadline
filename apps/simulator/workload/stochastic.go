package workload

import "github.com/kekubhai/Loadline/apps/simulator/engine"

// Splitter labels requests read or write according to the plan's read/write
// ratio, deterministically.
type Splitter struct {
	readFrac float64
	rng      *engine.RNG
}

// NewSplitter returns a deterministic read/write splitter. It takes the
// seed first and an optional read fraction (default 0.8).
func NewSplitter(seed uint64, readFraction ...float64) *Splitter {
	frac := 0.8
	if len(readFraction) > 0 {
		frac = readFraction[0]
	}
	return &Splitter{readFrac: frac, rng: engine.NewRNG(seed)}
}

// IsRead reports whether the next request is a read.
func (s *Splitter) IsRead() bool { return s.rng.Float64() < s.readFrac }

type ArrivalClock struct {
	rng           *engine.RNG
	meanGapMillis float64
}

// NewArrivalClock returns a generator of exponential inter-arrival gaps
// with the given mean, in milliseconds.
func NewArrivalClock(seed uint64, meanGapMillis float64) *ArrivalClock {
	return &ArrivalClock{rng: engine.NewRNG(seed), meanGapMillis: meanGapMillis}
}

// NextGapMillis returns the next simulated gap between arrivals.
func (a *ArrivalClock) NextGapMillis() float64 {
	return a.meanGapMillis * a.rng.ExpFloat64()
}

// IDAllocator hands out unique, monotonically increasing request IDs.
type IDAllocator struct {
	next uint64
}

// NewIDAllocator starts IDs at 1.
func NewIDAllocator() *IDAllocator { return &IDAllocator{next: 1} }

// Next returns the next request ID.
func (a *IDAllocator) Next() uint64 {
	id := a.next
	a.next++
	return id
}
