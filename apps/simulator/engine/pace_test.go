package engine

import (
	"testing"
	"time"
)

// TestPaceFollowsSimulatedTimeNotEventCount is the regression for runs
// that took minutes at "1×": a dense schedule (20k events in 400ms of
// simulated time, ~50 events per simulated ms) at real-time pace must
// take ~400ms of wall time, not events × per-event sleep.
func TestPaceFollowsSimulatedTimeNotEventCount(t *testing.T) {
	sched, clock, count := chainScheduler(20_000, 20_000) // 20µs apart → 400ms sim
	runner := NewRunner(sched, clock)
	runner.SetPace(1.0) // real time

	start := time.Now()
	res := runner.Run()
	wall := time.Since(start)

	if got := count.Load(); got != 20_000 {
		t.Fatalf("processed %d events, want 20000", got)
	}
	if res.StopReason != StopQueueEmpty {
		t.Fatalf("stop reason = %q", res.StopReason)
	}
	if wall < 300*time.Millisecond || wall > 1500*time.Millisecond {
		t.Fatalf("400ms of simulated time took %s of wall time at 1× pace", wall)
	}
}

// TestPaceSpeedUp: the same schedule at 50× must finish far faster.
func TestPaceSpeedUp(t *testing.T) {
	sched, clock, _ := chainScheduler(20_000, 20_000)
	runner := NewRunner(sched, clock)
	runner.SetPace(1.0 / 50)

	start := time.Now()
	runner.Run()
	if wall := time.Since(start); wall > 300*time.Millisecond {
		t.Fatalf("400ms of simulated time took %s at 50×; want ≈8ms", wall)
	}
}

// TestPacePreservesResults: pacing changes only wall-clock duration.
func TestPacePreservesResults(t *testing.T) {
	run := func(pace float64) *Results {
		sched, clock, _ := chainScheduler(300, 100_000) // 30ms sim
		runner := NewRunner(sched, clock)
		runner.SetPace(pace)
		return runner.Run()
	}
	fast, paced := run(0), run(1.0)
	if fast.Processed != paced.Processed || fast.FinalTime != paced.FinalTime ||
		fast.StopReason != paced.StopReason || fast.Scheduled != paced.Scheduled {
		t.Fatalf("pacing changed results: fast=%+v paced=%+v", fast, paced)
	}
}

// TestPacedSparseRunStopsPromptly: one event per simulated second at
// real time means multi-second waits; Stop must not wait them out, and
// the interrupted event must stay queued.
func TestPacedSparseRunStopsPromptly(t *testing.T) {
	sched, clock, count := chainScheduler(10, 1_000_000_000) // 1 event / sim-second
	runner := NewRunner(sched, clock)
	runner.SetPace(1.0)

	done := make(chan *Results)
	go func() { done <- runner.Run() }()
	time.Sleep(50 * time.Millisecond) // first event anchors; second waits ~1s
	stopAt := time.Now()
	runner.Stop()
	select {
	case res := <-done:
		if d := time.Since(stopAt); d > 200*time.Millisecond {
			t.Fatalf("stop took %s to land", d)
		}
		if res.StopReason != StopStopped {
			t.Fatalf("stop reason = %q", res.StopReason)
		}
		if got := count.Load(); got != 1 {
			t.Fatalf("handlers run = %d, want 1 (interrupted event must not execute)", got)
		}
		if res.Pending != 9 {
			t.Fatalf("pending = %d, want 9 (interrupted event put back)", res.Pending)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paced run did not stop")
	}
}

// TestPacedPauseResumeDoesNotBurst: wall time spent paused must not be
// "caught up" by racing through events after resume.
func TestPacedPauseResumeDoesNotBurst(t *testing.T) {
	sched, clock, count := chainScheduler(40, 10_000_000) // 10ms apart → 400ms sim
	runner := NewRunner(sched, clock)
	runner.SetPace(1.0)

	done := make(chan *Results)
	go func() { done <- runner.Run() }()
	time.Sleep(60 * time.Millisecond)
	if !runner.Pause() {
		t.Fatal("pause failed")
	}
	time.Sleep(300 * time.Millisecond)
	before := count.Load()
	runner.Resume()
	time.Sleep(30 * time.Millisecond)
	if burst := count.Load() - before; burst > 10 {
		t.Fatalf("resume burst %d events in 30ms; pacing should allow ~3", burst)
	}
	if res := <-done; res.StopReason != StopQueueEmpty || count.Load() != 40 {
		t.Fatalf("run did not complete after resume: %+v", res)
	}
}
