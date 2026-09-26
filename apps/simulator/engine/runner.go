package engine

import (
	"fmt"
	"io"
)

// Runner executes a discrete-event simulation.
//
// The core loop is:
//
//	pop earliest event → advance clock to its timestamp → execute handler
//	→ handler schedules new events → repeat
//
// Simulated time advances only by jumping to event timestamps; the runner
// performs no wall-clock sleeps. Handler panics propagate to the caller —
// the engine does not swallow bugs.
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
}

// NewRunner wires a runner to a scheduler and clock.
func NewRunner(s *Scheduler, c *Clock) *Runner {
	return &Runner{scheduler: s, clock: c}
}

// Run executes the simulation until the queue empties or a limit (horizon,
// max events) is reached, then returns the run's results.
func (r *Runner) Run() *Results {
	results := newResults()

	for {
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
	}

	results.Pending = r.scheduler.Pending()
	results.Scheduled = r.scheduler.nextID - 1
	return results
}
