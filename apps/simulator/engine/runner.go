package engine

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Runner executes a discrete-event simulation.
//
// The core loop is:
//
//	pop earliest event → advance clock to its timestamp → execute handler
//	→ handler schedules new events → repeat
//
// Simulated time advances only by jumping to event timestamps. The runner
// performs no wall-clock sleeps of its own except when a pacing tick is
// configured: in that mode it processes one event per tick and sleeps the
// per-tick interval between events, which makes long runs observable (and
// pausable/stoppable) from the outside. Tick pacing changes nothing about
// event ordering or results — the same schedule executes in the same order
// either way; only its wall-clock duration differs.
//
// Handler panics propagate to the caller — the engine does not swallow bugs.
type Runner struct {
	scheduler *Scheduler
	clock     *Clock

	// Horizon bounds simulated time: events at or beyond this instant are
	// never processed. Zero (the default) means unbounded.
	Horizon Time

	// MaxEvents bounds the number of processed events as a runaway guard.
	// Zero means unbounded.
	MaxEvents uint64

	// RecordHistory, when true, retains an EventRecord per processed event
	// in Results.History. Off by default: long runs would accumulate memory.
	RecordHistory bool

	// TraceOut, when non-nil, receives a human-readable line per processed
	// event.
	TraceOut io.Writer

	// Tick, when > 0, paces execution at one event per tick interval of
	// wall time. Zero (the default) runs events back-to-back as fast as
	// possible. Pacing is deterministic-safe: it never reorders or drops
	// events, so results are identical at any tick duration. Guarded by
	// tickMu; use SetTick/tickDelay for cross-goroutine access.
	Tick   time.Duration
	tickMu sync.Mutex

	// stopped is set by Stop: after the in-flight event finishes, the run
	// ends with StopReason "stopped" and the remaining queue is preserved.
	stopped bool
	stopMu  sync.Mutex

	// paused is set by Pause and cleared by Resume. While set, the run
	// loop idles without advancing simulated time.
	paused   bool
	pauseMu  sync.Mutex
	pauseNow chan struct{} // closed and replaced on each Resume wakeup

	// processedCount is a monotonically non-decreasing event counter
	// readable from another goroutine (progress telemetry).
	processedCount atomic.Uint64

	// finished is set when Run returns (any exit path). Control calls on
	// a finished runner are rejected: Pause reports false so a completed
	// run can never be re-marked PAUSED by a late control RPC.
	finished atomic.Bool
}

// NewRunner wires a runner to a scheduler and clock.
func NewRunner(s *Scheduler, c *Clock) *Runner {
	return &Runner{scheduler: s, clock: c, pauseNow: make(chan struct{})}
}

// SetTick re-targets the wall-clock pace of a live run (speed control).
// Zero restores as-fast-as-possible. Safe to call from another goroutine
// while the run loop executes; the new tick applies from the next event.
func (r *Runner) SetTick(d time.Duration) {
	r.tickMu.Lock()
	r.Tick = d
	r.tickMu.Unlock()
}

// tickDelay returns the current tick under lock (run-loop side).
func (r *Runner) tickDelay() time.Duration {
	r.tickMu.Lock()
	defer r.tickMu.Unlock()
	return r.Tick
}

// Pause suspends the run at the current simulated instant. The in-flight
// event completes; then the loop idles until Resume. Returns false when
// the run has already stopped.
func (r *Runner) Pause() bool {
	if r.finished.Load() {
		return false
	}
	r.stopMu.Lock()
	stopped := r.stopped
	r.stopMu.Unlock()
	if stopped {
		return false
	}
	r.pauseMu.Lock()
	r.paused = true
	r.pauseMu.Unlock()
	return true
}

// Resume lifts a pause and wakes the run loop.
func (r *Runner) Resume() {
	r.pauseMu.Lock()
	wasPaused := r.paused
	r.paused = false
	if wasPaused {
		close(r.pauseNow)
		r.pauseNow = make(chan struct{})
	}
	r.pauseMu.Unlock()
}

// Paused reports whether the run is currently paused.
func (r *Runner) Paused() bool {
	r.pauseMu.Lock()
	defer r.pauseMu.Unlock()
	return r.paused
}

// Stop ends the run after the in-flight event finishes. The remaining
// queue is preserved and Results.StopReason reports "stopped". Safe to
// call from any goroutine; idempotent.
func (r *Runner) Stop() {
	r.stopMu.Lock()
	r.stopped = true
	r.stopMu.Unlock()
	// Also lift a pause so a paused-and-stopped run can terminate.
	r.Resume()
}

// waitWhilePaused blocks until Resume is called (or the runner is
// stopped). Returns false when the run was stopped while paused.
func (r *Runner) waitWhilePaused() bool {
	for {
		r.pauseMu.Lock()
		if !r.paused {
			r.pauseMu.Unlock()
			return true
		}
		wake := r.pauseNow
		r.pauseMu.Unlock()

		r.stopMu.Lock()
		stopped := r.stopped
		r.stopMu.Unlock()
		if stopped {
			return false
		}

		<-wake // closed by Resume
	}
}

// Run executes the simulation until the queue empties, a limit (horizon,
// max events) is reached, or the run is stopped, then returns the run's
// results.
func (r *Runner) Run() *Results {
	defer r.finished.Store(true)

	results := newResults()

	for {
		if !r.waitWhilePaused() {
			r.finishStopped(results)
			return results
		}

		e := r.scheduler.NextEvent()
		if e == nil {
			results.StopReason = StopQueueEmpty
			break
		}
		if r.Horizon != 0 && e.Timestamp >= r.Horizon {
			results.StopReason = StopHorizon
			r.scheduler.queue.Push(e) // never lose a pending event
			break
		}
		if r.MaxEvents != 0 && results.Processed >= r.MaxEvents {
			results.StopReason = StopMaxEvents
			r.scheduler.queue.Push(e) // never lose a pending event
			break
		}

		// Advance simulated time by jumping — never by sleeping.
		r.clock.Set(e.Timestamp)

		ctx := &Context{
			scheduler: r.scheduler,
			event:     e,
			clock:     r.clock,
		}
		if r.scheduler.seeded {
			ctx.rng = NewRNG(e.rngSeed)
		}

		results.record(e)
		r.processedCount.Store(results.Processed)
		if r.RecordHistory {
			results.recordHistory(e)
		}

		if e.Handler != nil {
			newEvents := e.Handler(ctx)
			for i := range newEvents {
				r.scheduler.ScheduleRaw(newEvents[i])
			}
		}

		if r.TraceOut != nil {
			fmt.Fprintf(r.TraceOut, "PROC  %s\n", e.String())
		}

		// A Stop issued during the handler takes effect now: one event
		// of grace, then end the run (the queue is preserved).
		r.stopMu.Lock()
		stopped := r.stopped
		r.stopMu.Unlock()
		if stopped {
			r.finishStopped(results)
			return results
		}

		if tick := r.tickDelay(); tick > 0 {
			time.Sleep(tick)
		}
	}

	results.Pending = r.scheduler.Pending()
	results.Scheduled = r.scheduler.nextID - 1
	return results
}

// finishStopped records the stopped state: partial counters, preserved
// queue, StopReason "stopped".
func (r *Runner) finishStopped(results *Results) {
	results.StopReason = StopStopped
	results.Pending = r.scheduler.Pending()
	results.Scheduled = r.scheduler.nextID - 1
}

// Processed reports how many events have been processed so far. During a
// paced run it is safe to call from another goroutine; the count is
// monotonically non-decreasing.
func (r *Runner) Processed() uint64 { return r.processedCount.Load() }

// Pending reports how many events are still queued. Called from another
// goroutine during a paced run.
func (r *Runner) Pending() int { return r.scheduler.Pending() }
