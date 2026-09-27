package engine

import "sort"

// StopReason explains why a simulation run ended.
type StopReason string

const (
	// StopQueueEmpty means every scheduled event was processed.
	StopQueueEmpty StopReason = "queue_empty"
	// StopHorizon means the run stopped because the next event lies beyond
	// the configured simulated-time horizon.
	StopHorizon StopReason = "horizon"
	// StopMaxEvents means the run hit the processed-event limit.
	StopMaxEvents StopReason = "max_events"
	// StopStopped means the run was stopped by an explicit control action
	// before its horizon; results are a partial view of the run.
	StopStopped StopReason = "stopped"
)

// EventRecord is an immutable summary of one processed event, retained when
// the runner is configured with RecordHistory. It is enough to audit
// execution order and causal chains without keeping full handler closures.
type EventRecord struct {
	ID          uint64
	Timestamp   Time
	Type        string
	RequestID   uint64
	ComponentID string
	Cause       uint64
}

// Results aggregates what happened during a run. All counters refer to
// simulated activity, never wall-clock time.
type Results struct {
	// Processed is the number of events whose handlers executed.
	Processed uint64

	// Scheduled is the total number of events ever enqueued, including
	// events still pending when the run stopped.
	Scheduled uint64

	// Pending is how many events remained queued when the run stopped.
	Pending int

	// FinalTime is the simulated instant of the last processed event.
	FinalTime Time

	// FirstTime / LastTime bound the simulated span of processed events.
	FirstTime Time
	LastTime  Time

	// StopReason states why the run ended.
	StopReason StopReason

	// ByType counts processed events per event Type. Deterministic
	// iteration order is available via SortedTypes.
	ByType map[string]uint64

	// History holds per-event records in processing order, only when the
	// runner was configured with RecordHistory.
	History []EventRecord
}

// SortedTypes returns event type counts ordered by type name, so reports
// and tests get a stable order.
func (r *Results) SortedTypes() []TypeCount {
	out := make([]TypeCount, 0, len(r.ByType))
	for t, n := range r.ByType {
		out = append(out, TypeCount{Type: t, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// TypeCount pairs an event type with its processed-event count.
type TypeCount struct {
	Type  string
	Count uint64
}

func newResults() *Results {
	return &Results{ByType: make(map[string]uint64), StopReason: StopQueueEmpty}
}

func (r *Results) record(e *Event) {
	if r.Processed == 0 {
		r.FirstTime = e.Timestamp
	}
	r.LastTime = e.Timestamp
	r.FinalTime = e.Timestamp
	r.Processed++
	r.ByType[e.Type]++
}

func (r *Results) recordHistory(e *Event) {
	r.History = append(r.History, EventRecord{
		ID:          e.ID,
		Timestamp:   e.Timestamp,
		Type:        e.Type,
		RequestID:   e.RequestID,
		ComponentID: e.ComponentID,
		Cause:       e.Cause,
	})
}
