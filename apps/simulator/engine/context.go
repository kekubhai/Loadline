package engine

import "fmt"

// Context is what an event handler sees while executing. It is the only
// sanctioned way to read simulated time, draw random values, and schedule
// new events during a run.
//
// Time and RNG access are deliberately restricted: handlers cannot mutate
// the clock and cannot touch shared randomness except through their own
// per-event stream, which keeps runs reproducible.
type Context struct {
	scheduler *Scheduler
	event     *Event
	clock     *Clock
	rng       *RNG // per-event stream; nil if no seed configured
}

// Now returns the current simulated instant.
func (c *Context) Now() Time { return c.clock.Now() }

// Event returns the event currently being processed. Handlers must not
// mutate it.
func (c *Context) Event() *Event { return c.event }

// Schedule enqueues a new event at now+delay. The scheduler assigns the
// event its ID and causal parent (the event being processed). Panics if
// delay is negative — the engine never schedules into the past.
func (c *Context) Schedule(delay Duration, template Event) *Event {
	if delay < 0 {
		panic(fmt.Sprintf("engine: Schedule with negative delay %s at %s", delay, c.clock.Now()))
	}
	template.Timestamp = c.clock.Now().Add(delay)
	template.Cause = c.event.ID
	return c.scheduler.ScheduleRaw(template)
}

// ScheduleAt enqueues a new event at an absolute simulated instant. It
// panics if the instant is earlier than current time.
func (c *Context) ScheduleAt(t Time, template Event) *Event {
	if t < c.clock.Now() {
		panic(fmt.Sprintf("engine: ScheduleAt(%s) is before current time %s", t, c.clock.Now()))
	}
	template.Timestamp = t
	template.Cause = c.event.ID
	return c.scheduler.ScheduleRaw(template)
}

// Rand returns the event's private deterministic random stream. Two events
// with identical (seed, ID) receive identical streams — reproducibility by
// construction. Panics if the simulation was run without a seed.
func (c *Context) Rand() *RNG {
	if c.rng == nil {
		panic("engine: Rand() called but simulation has no seed configured")
	}
	return c.rng
}

// Logf appends a line to the run's event log (if one is configured).
func (c *Context) Logf(format string, args ...any) {
	c.scheduler.logf(c.event, format, args...)
}
