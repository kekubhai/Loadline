package engine

import (
	"sync/atomic"
	"testing"
	"time"
)

// chainScheduler schedules n one-shot events at fixed steps and returns
// a counter the handlers increment.
func chainScheduler(n int, step Time) (*Scheduler, *Clock, *atomic.Uint64) {
	clock := NewClock()
	sched := NewScheduler(NewPriorityQueue(), clock)
	var count atomic.Uint64
	for i := 1; i <= n; i++ {
		sched.Schedule(Event{
			Timestamp: Time(i) * step,
			Type:      "test.tick",
			Handler: func(*Context) []Event {
				count.Add(1)
				return nil
			},
		})
	}
	return sched, clock, &count
}

// TestPauseBlocksUntilResume verifies that a paused run holds simulated
// time AND event progress steady until Resume, then finishes normally
// with identical results to an unpaced run.
func TestPauseBlocksUntilResume(t *testing.T) {
	sched, clock, count := chainScheduler(500, 1_000_000) // 500 × 1ms sim
	runner := NewRunner(sched, clock)
	runner.Tick = 100 * time.Microsecond // ~50ms wall for the whole run

	done := make(chan *Results)
	go func() { done <- runner.Run() }()

	// Let some events process, then pause.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && runner.Processed() < 50 {
		time.Sleep(500 * time.Microsecond)
	}
	if !runner.Pause() {
		t.Fatal("Pause returned false on an active run")
	}
	time.Sleep(80 * time.Millisecond) // let the loop park

	frozenProcessed := runner.Processed()
	frozenClock := clock.Now()
	time.Sleep(80 * time.Millisecond)
	if got := runner.Processed(); got != frozenProcessed {
		t.Fatalf("paused run kept processing events: %d → %d", frozenProcessed, got)
	}
	if got := clock.Now(); got != frozenClock {
		t.Fatalf("paused run advanced simulated time: %s → %s", frozenClock, got)
	}
	if !runner.Paused() {
		t.Fatal("Paused() reported false while paused")
	}

	runner.Resume()
	res := <-done
	if res.StopReason != StopQueueEmpty {
		t.Fatalf("stop reason = %q, want %q", res.StopReason, StopQueueEmpty)
	}
	if got := count.Load(); got != 500 {
		t.Fatalf("processed handlers = %d, want 500", got)
	}
	if res.FinalTime != 500_000_000 {
		t.Fatalf("final time = %s, want 500ms", res.FinalTime)
	}
}

// TestStopEndsWithStoppedReason verifies an explicit stop ends the run
// with the preserved queue and the "stopped" reason.
func TestStopEndsWithStoppedReason(t *testing.T) {
	clock := NewClock()
	sched := NewScheduler(NewPriorityQueue(), clock)

	// Self-perpetuating chain: every event schedules the next 1ms later.
	var self func(*Context) []Event
	self = func(ctx *Context) []Event {
		return []Event{{
			Timestamp: ctx.Now() + 1_000_000,
			Type:      "test.tick",
			Handler:   self,
		}}
	}
	sched.Schedule(Event{Timestamp: 1_000_000, Type: "test.tick", Handler: self})

	runner := NewRunner(sched, clock)
	runner.Horizon = Time(1) * 3_600_000_000_000 // 1h: only a stop can end this
	runner.Tick = 200 * time.Microsecond

	done := make(chan *Results)
	go func() { done <- runner.Run() }()

	time.Sleep(30 * time.Millisecond)
	runner.Stop()
	select {
	case res := <-done:
		if res.StopReason != StopStopped {
			t.Fatalf("stop reason = %q, want %q", res.StopReason, StopStopped)
		}
		if res.Processed == 0 {
			t.Fatal("stopped run processed no events")
		}
		if res.Pending == 0 {
			t.Fatal("stopped run preserved no pending events")
		}
		if res.FinalTime >= runner.Horizon {
			t.Fatalf("final time %s reached horizon %s before stop", res.FinalTime, runner.Horizon)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop within 5s")
	}
}

// TestStopWhilePaused verifies stop terminates a paused run (it must not
// wait forever for a resume).
func TestStopWhilePaused(t *testing.T) {
	sched, clock, _ := chainScheduler(10_000, 1_000_000)
	runner := NewRunner(sched, clock)
	runner.Tick = 100 * time.Microsecond

	done := make(chan *Results)
	go func() { done <- runner.Run() }()

	time.Sleep(10 * time.Millisecond)
	runner.Pause()
	time.Sleep(10 * time.Millisecond)
	runner.Stop()
	select {
	case res := <-done:
		if res.StopReason != StopStopped {
			t.Fatalf("stop reason = %q, want %q", res.StopReason, StopStopped)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("paused run did not stop within 5s")
	}
}

// TestTickPacingPreservesResults verifies pacing changes only the
// wall-clock duration, never the results.
func TestTickPacingPreservesResults(t *testing.T) {
	run := func(tick time.Duration) *Results {
		sched, clock, _ := chainScheduler(200, 1_000_000)
		runner := NewRunner(sched, clock)
		runner.Tick = tick
		return runner.Run()
	}
	fast, paced := run(0), run(50*time.Microsecond)
	if fast.Processed != paced.Processed || fast.FinalTime != paced.FinalTime ||
		fast.StopReason != paced.StopReason || fast.Scheduled != paced.Scheduled {
		t.Fatalf("pacing changed results: fast=%+v paced=%+v", fast, paced)
	}
}
