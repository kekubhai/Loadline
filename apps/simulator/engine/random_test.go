package engine

import "testing"

// Contract: identical seeds produce identical 64-bit streams.
func TestRNGDeterministic(t *testing.T) {
	a := NewRNG(42)
	b := NewRNG(42)
	for i := 0; i < 1000; i++ {
		if a.Next() != b.Next() {
			t.Fatalf("stream diverged at draw %d", i)
		}
	}
}

// Contract: different seeds produce different streams (overwhelmingly
// likely for SplitMix64; if this fails the generator is broken).
func TestRNGSeedsDiffer(t *testing.T) {
	a := NewRNG(1)
	b := NewRNG(2)
	diff := false
	for i := 0; i < 64; i++ {
		if a.Next() != b.Next() {
			diff = true
			break
		}
	}
	if !diff {
		t.Fatal("seeds 1 and 2 produced identical first 64 draws")
	}
}

// Contract: Float64 is uniform in [0, 1).
func TestRNGFloat64Range(t *testing.T) {
	r := NewRNG(7)
	for i := 0; i < 100000; i++ {
		f := r.Float64()
		if f < 0 || f >= 1 {
			t.Fatalf("Float64 out of [0,1): %f", f)
		}
	}
}

// Contract: Intn covers [0, n) without modulo bias beyond tolerance.
func TestRNGIntn(t *testing.T) {
	r := NewRNG(9)
	const n = 7
	const draws = 70000
	counts := make([]int, n)
	for i := 0; i < draws; i++ {
		v := r.Intn(n)
		if v < 0 || v >= n {
			t.Fatalf("Intn(%d) returned %d", n, v)
		}
		counts[v]++
	}
	want := draws / n
	for _, c := range counts {
		if c < want*90/100 || c > want*110/100 {
			t.Fatalf("Intn distribution skewed: counts=%v (want ~%d each)", counts, want)
		}
	}
	if !func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		r.Intn(0)
		return false
	}() {
		t.Fatal("Intn(0) should panic")
	}
}

// Contract: ExpFloat64 yields the specified mean and stays non-negative.
// Mean of an exponential with rate 1 is 1; variance is 1.
func TestRNGExpFloat64Mean(t *testing.T) {
	r := NewRNG(123)
	const draws = 200000
	var sum float64
	for i := 0; i < draws; i++ {
		v := r.ExpFloat64()
		if v < 0 {
			t.Fatalf("ExpFloat64 negative: %f", v)
		}
		sum += v
	}
	mean := sum / draws
	// Standard error of the mean for exponential is 1/sqrt(N) ≈ 0.0022.
	// Allow 5 sigma.
	if mean < 1-5*0.0022 || mean > 1+5*0.0022 {
		t.Fatalf("ExpFloat64 mean = %f, want ~1", mean)
	}
}

// Contract: per-event RNG streams are derived from (run seed, event ID) —
// the same event ID under the same seed always draws the same values, and
// different event IDs draw differently.
func TestPerEventRNGStreams(t *testing.T) {
	var drawsA, drawsB []float64

	build := func(collect *[]float64) (*Runner, func()) {
		clock := NewClock()
		pq := NewPriorityQueue()
		s := NewScheduler(pq, clock)
		s.SetSeed(777)
		r := NewRunner(s, clock)
		for id := uint64(1); id <= 3; id++ {
			s.Schedule(Event{Timestamp: Time(id), Type: "draw", Handler: func(ctx *Context) []Event {
				*collect = append(*collect, ctx.Rand().Float64())
				return nil
			}})
		}
		return r, func() {}
	}

	rA, _ := build(&drawsA)
	rA.Run()
	rB, _ := build(&drawsB)
	rB.Run()

	if len(drawsA) != 3 || len(drawsB) != 3 {
		t.Fatalf("expected 3 draws per run, got %d and %d", len(drawsA), len(drawsB))
	}
	for i := range drawsA {
		if drawsA[i] != drawsB[i] {
			t.Fatalf("draw %d diverged: %f vs %f", i, drawsA[i], drawsB[i])
		}
	}
	if drawsA[0] == drawsA[1] {
		t.Fatal("distinct event IDs produced identical draws — streams not independent")
	}
}

// Contract: Rand() without a configured seed panics loudly instead of
// silently producing unreproducible runs. The panic fires while the
// handler executes, so the run happens inside the recover guard.
func TestRandWithoutSeedPanics(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	s.Schedule(Event{Timestamp: 0, Type: "x", Handler: func(ctx *Context) []Event {
		_ = ctx.Rand()
		return nil
	}})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Rand without seed should panic")
			}
		}()
		NewRunner(s, clock).Run()
	}()
}
