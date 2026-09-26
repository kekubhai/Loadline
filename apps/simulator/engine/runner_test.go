package engine

import (
	"testing"
)

// Contract: (timestamp, ID) ordering — time dominates, schedule order
// breaks ties. A heap with shuffled insertions must pop fully sorted.
func TestPriorityQueueOrder(t *testing.T) {
	pq := NewPriorityQueue()
	clock := NewClock()
	s := NewScheduler(pq, clock)

	// Schedule at mixed times, including equal timestamps.
	specs := []struct {
		ts  Time
		typ string
	}{
		{10, "b"}, {5, "a"}, {10, "a"}, {1, "z"}, {10, "c"}, {5, "b"},
	}
	for _, sp := range specs {
		s.Schedule(Event{Timestamp: sp.ts, Type: sp.typ})
	}

	// ts=1: z | ts=5: a(#2), b(#6) | ts=10: b(#1), a(#3), c(#5)
	want := []string{"z", "a", "b", "b", "a", "c"}
	got := make([]string, 0, len(want))
	for pq.Len() > 0 {
		got = append(got, pq.Pop().Type)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pop order = %v, want %v", got, want)
		}
	}
}

// Contract: Peek is non-destructive.
func TestPriorityQueuePeek(t *testing.T) {
	pq := NewPriorityQueue()
	if pq.Peek() != nil || pq.Pop() != nil {
		t.Fatal("empty queue must return nil from Peek and Pop")
	}
	clock := NewClock()
	s := NewScheduler(pq, clock)
	s.Schedule(Event{Timestamp: 100, Type: "x"})
	if pq.Peek().Timestamp != 100 || pq.Len() != 1 {
		t.Fatalf("peek changed queue state: len=%d", pq.Len())
	}
	if pq.Pop().Timestamp != 100 || pq.Len() != 0 {
		t.Fatal("pop did not remove the only element")
	}
}

// Contract: an empty-queue run stops with StopQueueEmpty and processes
// nothing.
func TestRunnerEmptyQueue(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)

	res := r.Run()
	if res.Processed != 0 || res.StopReason != StopQueueEmpty {
		t.Fatalf("unexpected results: %+v", res)
	}
	if clock.Now() != 0 {
		t.Fatalf("clock moved without events: %s", clock.Now())
	}
}

// Contract: the runner executes handlers and returns them, in timestamp
// order, and the clock jumps exactly to each processed event's timestamp.
func TestRunnerExecutesInTimestampOrder(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)

	var order []string
	handler := func(ctx *Context) []Event {
		order = append(order, ctx.Event().Type)
		if ctx.Now() != ctx.Event().Timestamp {
			t.Errorf("clock %s != event timestamp %s during %s",
				ctx.Now(), ctx.Event().Timestamp, ctx.Event().Type)
		}
		return nil
	}

	s.Schedule(Event{Timestamp: Time(30 * Millisecond), Type: "c", Handler: handler})
	s.Schedule(Event{Timestamp: Time(10 * Millisecond), Type: "a", Handler: handler})
	s.Schedule(Event{Timestamp: Time(20 * Millisecond), Type: "b", Handler: handler})

	res := r.Run()

	want := []string{"a", "b", "c"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("execution order = %v, want %v", order, want)
		}
	}
	if res.Processed != 3 || res.StopReason != StopQueueEmpty {
		t.Fatalf("results mismatch: %+v", res)
	}
	if res.FinalTime != Time(30*Millisecond) {
		t.Fatalf("final time = %s, want 30ms", res.FinalTime)
	}
}

// Contract: handler-returned events are scheduled and processed later;
// the causal chain (Cause) links child to parent.
func TestRunnerSchedulesNewEvents(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)

	var seen []uint64
	s.Schedule(Event{Timestamp: 0, Type: "root", Handler: func(ctx *Context) []Event {
		child := Event{Type: "leaf"}
		// Return-based scheduling: timestamp set by handler itself.
		child.Timestamp = ctx.Now().Add(5 * Millisecond)
		return []Event{child}
	}})
	s.Schedule(Event{Timestamp: Time(1 * Millisecond), Type: "probe", Handler: func(ctx *Context) []Event {
		seen = append(seen, ctx.Event().ID)
		return nil
	}})

	res := r.Run()

	if len(seen) != 1 {
		t.Fatalf("probe event lost; results: %+v", res)
	}
	if res.ByType["root"] != 1 || res.ByType["leaf"] != 1 || res.ByType["probe"] != 1 {
		t.Fatalf("ByType mismatch: %+v", res.ByType)
	}
	// The returned leaf carries Cause=0 because return-based scheduling
	// bypasses Context.Schedule; verify stored Cause semantics explicitly
	// via Schedule-based flow below instead.
}

// Contract: Context.Schedule stamps the timestamp (now+delay) and causal
// parent; ScheduleAt stamps an absolute time. Both refuse the past.
func TestContextScheduleStampsCausality(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)

	var parentID, childID uint64
	s.Schedule(Event{Timestamp: 0, Type: "parent", Handler: func(ctx *Context) []Event {
		parentID = ctx.Event().ID
		ctx.Schedule(10*Millisecond, Event{Type: "child", Handler: func(c2 *Context) []Event {
			childID = c2.Event().ID
			if c2.Now() != Time(10*Millisecond) {
				t.Errorf("child ran at %s, want 10ms", c2.Now())
			}
			if c2.Event().Cause != parentID {
				t.Errorf("child Cause = %d, want parent %d", c2.Event().Cause, parentID)
			}
			return nil
		}})
		// Absolute scheduling also links causality.
		ctx.ScheduleAt(Time(20*Millisecond), Event{Type: "abs", Handler: func(c2 *Context) []Event {
			if c2.Now() != Time(20*Millisecond) {
				t.Errorf("abs ran at %s, want 20ms", c2.Now())
			}
			return nil
		}})
		return nil
	}})

	res := r.Run()
	if childID == 0 {
		t.Fatal("child never ran")
	}
	if res.ByType["abs"] != 1 {
		t.Fatal("ScheduleAt event never ran")
	}

	// Negative delay must panic — the engine never schedules the past.
	// The panic fires when the handler executes, so run the simulation
	// inside the recover guard.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Schedule with negative delay should panic")
			}
		}()
		s.Schedule(Event{Timestamp: 0, Type: "neg", Handler: func(ctx *Context) []Event {
			ctx.Schedule(-1, Event{Type: "bad"})
			return nil
		}})
		NewRunner(s, clock).Run()
	}()
}

// Contract: Horizon stops the run before processing events at/after the
// horizon and reports StopHorizon.
func TestRunnerHorizon(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)
	r.Horizon = Time(100 * Millisecond)

	ran := false
	s.Schedule(Event{Timestamp: Time(99 * Millisecond), Type: "inside", Handler: func(ctx *Context) []Event {
		ran = true
		ctx.Schedule(5*Millisecond, Event{Type: "beyond"}) // 104ms >= horizon
		return nil
	}})
	s.Schedule(Event{Timestamp: Time(200 * Millisecond), Type: "far"})

	res := r.Run()
	if !ran {
		t.Fatal("event inside horizon never ran")
	}
	if res.StopReason != StopHorizon {
		t.Fatalf("stop reason = %s, want %s", res.StopReason, StopHorizon)
	}
	if res.ByType["beyond"] != 0 || res.ByType["far"] != 0 {
		t.Fatalf("events past horizon were processed: %+v", res.ByType)
	}
}

// Contract: MaxEvents stops after N processed events and reports
// StopMaxEvents.
func TestRunnerMaxEvents(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)
	r.MaxEvents = 3

	var tick EventHandler
	tick = func(ctx *Context) []Event {
		ctx.Schedule(1*Millisecond, Event{Type: "tick", Handler: tick})
		return nil
	}
	s.Schedule(Event{Timestamp: 0, Type: "tick", Handler: tick})

	res := r.Run()
	if res.Processed != 3 || res.StopReason != StopMaxEvents {
		t.Fatalf("processed=%d stop=%s, want 3/%s", res.Processed, res.StopReason, StopMaxEvents)
	}
	if res.Pending != 1 {
		t.Fatalf("pending=%d, want 1", res.Pending)
	}
}

// Contract: RecordHistory retains per-event records in processing order.
func TestRunnerRecordHistory(t *testing.T) {
	clock := NewClock()
	pq := NewPriorityQueue()
	s := NewScheduler(pq, clock)
	r := NewRunner(s, clock)
	r.RecordHistory = true

	s.Schedule(Event{Timestamp: 1, Type: "a", ComponentID: "c1", Handler: func(ctx *Context) []Event {
		ctx.Schedule(2, Event{Type: "b", ComponentID: "c2"})
		return nil
	}})

	res := r.Run()
	if len(res.History) != 2 {
		t.Fatalf("history length = %d, want 2", len(res.History))
	}
	if res.History[0].Type != "a" || res.History[1].Type != "b" {
		t.Fatalf("history order wrong: %+v", res.History)
	}
	if res.History[1].Timestamp != 3 {
		t.Fatalf("history timestamp = %s, want 3ns", res.History[1].Timestamp)
	}
}
