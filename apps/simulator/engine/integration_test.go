package engine

import (
	"testing"
	"time"
)

// Integration: a multi-hop causal chain (arrival → service → response →
// retry) must preserve event ordering, causal links, and reproducibility
// across two identical runs — the property the whole platform depends on.
func TestCausalChainDeterminism(t *testing.T) {
	trace := func() []string {
		clock := NewClock()
		pq := NewPriorityQueue()
		s := NewScheduler(pq, clock)
		s.SetSeed(2024)
		r := NewRunner(s, clock)
		r.RecordHistory = true

		s.Schedule(Event{Timestamp: 0, Type: "request.arrive", ComponentID: "lb", RequestID: 1,
			Handler: func(ctx *Context) []Event {
				ctx.Schedule(2*Millisecond, Event{Type: "request.service", ComponentID: "api", RequestID: 1,
					Handler: func(ctx *Context) []Event {
						// Random failure roll decides retry vs success.
						if ctx.Rand().Float64() < 0.5 {
							ctx.Schedule(1*Millisecond, Event{Type: "request.retry", ComponentID: "lb", RequestID: 1,
								Handler: func(ctx *Context) []Event {
									ctx.Schedule(2*Millisecond, Event{Type: "request.service", ComponentID: "api", RequestID: 1, Handler: nil})
									return nil
								}})
						} else {
							ctx.Schedule(1*Millisecond, Event{Type: "request.response", ComponentID: "api", RequestID: 1, Handler: nil})
						}
						return nil
					}})
				return nil
			}})

		res := r.Run()
		if res.StopReason != StopQueueEmpty {
			t.Fatalf("unexpected stop: %s", res.StopReason)
		}
		lines := make([]string, 0, len(res.History))
		for _, h := range res.History {
			lines = append(lines, h.Type)
		}
		return lines
	}

	first := trace()
	second := trace()

	if len(first) == 0 {
		t.Fatal("no events processed")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("traces diverged at %d: %v vs %v", i, first[i], second[i])
		}
	}
}

// Contract: Results counters are internally consistent.
func TestResultsConsistency(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	s.SetSeed(1)
	r := NewRunner(s, clock)

	s.Schedule(Event{Timestamp: 0, Type: "boom", Handler: func(ctx *Context) []Event {
		ctx.Schedule(1, Event{Type: "after"})
		return nil
	}})
	s.Schedule(Event{Timestamp: 5, Type: "pending"})

	res := r.Run()
	// Everything drains: boom → after → pending all process.
	if res.Scheduled != 3 {
		t.Fatalf("scheduled = %d, want 3", res.Scheduled)
	}
	if res.Processed != 3 {
		t.Fatalf("processed = %d, want 3", res.Processed)
	}
	if res.Pending != 0 {
		t.Fatalf("pending = %d, want 0", res.Pending)
	}
	if res.ByType["boom"] != 1 || res.ByType["after"] != 1 || res.ByType["pending"] != 1 {
		t.Fatalf("ByType wrong: %+v", res.ByType)
	}
	if res.FirstTime != 0 || res.LastTime != Time(5) {
		t.Fatalf("time bounds wrong: first=%s last=%s", res.FirstTime, res.LastTime)
	}
}

// Guard against accidental wall-clock coupling: a 200k-event run must
// complete far faster than a real-time interpretation of its simulated
// span would allow.
func TestSimulatedTimeIsNotWallClock(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)

	var chain EventHandler
	chain = func(ctx *Context) []Event {
		if ctx.Now().Add(1*Millisecond) <= Time(200*Second) {
			ctx.Schedule(1*Millisecond, Event{Type: "chain", Handler: chain})
		}
		return nil
	}
	s.Schedule(Event{Timestamp: 0, Type: "chain", Handler: chain})

	start := time.Now()
	res := r.Run()
	elapsed := time.Since(start)

	if res.FinalTime != Time(200*Second) {
		t.Fatalf("final sim time = %s, want 200s", res.FinalTime)
	}
	// 200 simulated seconds must not take 200 real seconds.
	if elapsed > 30*time.Second {
		t.Fatalf("run took %s — sim time appears coupled to wall clock", elapsed)
	}
}
