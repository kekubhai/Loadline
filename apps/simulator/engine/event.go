package engine

import "fmt"

// Event is the atomic unit of simulation work.
//
// Events are immutable once scheduled: the engine processes them in
// timestamp order, and components react by scheduling new events. An event
// carries identity and causal metadata — behavior lives in the handler
// attached at scheduling time, keeping the engine decoupled from any
// concrete component model.
type Event struct {
	// Timestamp is the simulated instant at which this event should execute.
	Timestamp Time

	// Type is a machine-readable category name (e.g. "request.arrive",
	// "request.complete", "failure.inject"). Used for logging, metrics
	// attribution, and debugging.
	Type string

	// ID is a unique, monotonically increasing sequence number assigned by
	// the scheduler at schedule time. It is the tie-breaker for the
	// mandatory deterministic rule: events with equal timestamps execute in
	// the order they were scheduled.
	ID uint64

	// RequestID identifies the (possibly retried) request this event belongs
	// to. Zero means the event is not tied to a request (e.g. failure
	// injection, scaling ticks).
	RequestID uint64

	// ComponentID names the simulated component that owns/executes the
	// event (e.g. "lb-1", "db-primary"). The engine treats this as opaque.
	ComponentID string

	// Cause is the ID of the event that directly caused this one (its
	// parent in the causal chain), or 0 if it is an exogenous event such as
	// workload arrival or failure injection. This makes causal chains
	// traceable: request timeout -> retry -> arrival.
	Cause uint64

	// Seq is the sequence number of this event within its request
	// (0 = arrival, 1 = first hop, ...). Opaque to the engine.
	Seq uint32

	// Handler is the callback executed when the event is processed. It
	// receives the simulation context and returns zero or more new events
	// to schedule. Handlers must not retain or mutate the event.
	Handler EventHandler

	// rngSeed is the per-event RNG stream seed, assigned by the scheduler
	// when the run is seeded. Unexported: simulation code derives no meaning
	// from it.
	rngSeed uint64
}

// EventHandler processes an event and returns the next events to schedule.
// Implementations must be deterministic and must only schedule events via
// the supplied Context.
type EventHandler func(ctx *Context) []Event

// String renders a compact one-line description, primarily for logging.
func (e *Event) String() string {
	req := "-"
	if e.RequestID != 0 {
		req = fmt.Sprintf("req%d", e.RequestID)
	}
	if e.ComponentID != "" {
		return fmt.Sprintf("#%d @%s %s[%s] %s", e.ID, e.Timestamp, e.ComponentID, req, e.Type)
	}
	return fmt.Sprintf("#%d @%s [%s] %s", e.ID, e.Timestamp, req, e.Type)
}

// Clone returns a shallow copy of the event (used internally by tests and
// by callers that reuse event structs).
func (e *Event) Clone() Event { c := *e; return c }
