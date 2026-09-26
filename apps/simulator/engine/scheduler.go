package engine

import "fmt"

// Scheduler owns the pending-event set. It assigns globally unique,
// monotonically increasing IDs (the determinism tie-breaker), derives a
// private RNG stream per event, and feeds the priority queue.
type Scheduler struct {
	queue *PriorityQueue
	clock *Clock
	nextID uint64

	runSeed   uint64 // base seed for per-event RNG streams
	seeded    bool   // whether the run has a seed configured

	// trace, when non-nil, receives one line per scheduled and per
	// processed event. It is an optional debugging aid.
	trace func(line string)
}

// NewScheduler wires a scheduler to a queue and clock. Event IDs start at 1.
func NewScheduler(q *PriorityQueue, c *Clock) *Scheduler {
	return &Scheduler{queue: q, clock: c, nextID: 1}
}

// SetSeed enables per-event deterministic RNG streams derived from the run
// seed and each event's ID.
func (s *Scheduler) SetSeed(seed uint64) {
	s.runSeed = seed
	s.seeded = true
}

// SetTrace installs an optional trace sink (e.g. a logger). Passing nil
// disables tracing.
func (s *Scheduler) SetTrace(fn func(line string)) { s.trace = fn }

func (s *Scheduler) logf(e *Event, format string, args ...any) {
	if s.trace != nil {
		s.trace(fmt.Sprintf("@%s %s | %s", e.Timestamp, e, fmt.Sprintf(format, args...)))
	}
}

// Schedule enqueues an event at the Timestamp set on the template. The
// scheduler overrides identity fields: ID and per-event RNG seed. It
// returns the stored event; its ID can be captured for causal linking.
func (s *Scheduler) Schedule(template Event) *Event {
	return s.ScheduleRaw(template)
}

// ScheduleRaw finalizes identity fields and inserts into the queue.
func (s *Scheduler) ScheduleRaw(template Event) *Event {
	e := template // copy, so later caller mutations don't affect the queue
	e.ID = s.nextID
	s.nextID++
	if s.seeded {
		// Per-event RNG stream: mix the run seed with the event ID so each
		// event gets an independent, reproducible stream.
		e.rngSeed = mixSeed(s.runSeed, e.ID)
	}
	if s.trace != nil {
		s.trace(fmt.Sprintf("SCHED %s", e.String()))
	}
	s.queue.Push(&e)
	return &e
}

// NextEvent pops the earliest event, or returns nil when the queue is
// empty. This is the scheduler's contract with the runner.
func (s *Scheduler) NextEvent() *Event { return s.queue.Pop() }

// Pending returns how many events are waiting.
func (s *Scheduler) Pending() int { return s.queue.Len() }

// NextID returns the ID the next scheduled event will receive.
func (s *Scheduler) NextID() uint64 { return s.nextID }

// mixSeed deterministically folds a run seed and an event ID into a new
// 64-bit stream seed (a single SplitMix64 finalizer round).
func mixSeed(seed, id uint64) uint64 {
	z := seed ^ (id * 0x9e3779b97f4a7c15)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
