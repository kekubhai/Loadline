package loadlinev1

import (
	"fmt"
	"sync"

	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	"github.com/kekubhai/Loadline/apps/simulator/providers"
	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

// Run is one stored simulation: its inputs, lifecycle state, and — once
// finished — its outputs. All access is mutex-guarded; the run executes
// on a background goroutine while RPCs poll or subscribe to it.
type Run struct {
	ID  string
	mu  sync.RWMutex
	Sim *v1.Simulation

	Status   v1.RunStatus
	Err      string               // set when Status == FAILED
	Progress *v1.ProgressSnapshot // latest mid-run snapshot
	Started  bool
	Result   *sim.RunResult
	Resolved []providers.ResolvedSpec   // provider-backed specs (capacity/cost)
	subs     map[chan struct{}]struct{} // wake channels for stream waiters
	version  uint64                     // bumped on every state change
}

// Store holds all runs of this process. V1 is deliberately in-memory:
// simulations are ephemeral what-if analyses, not durable documents.
type Store struct {
	mu    sync.RWMutex
	runs  map[string]*Run
	order []string // creation order → deterministic listing/debug
	seq   int
}

// NewStore creates an empty run store.
func NewStore() *Store {
	return &Store{runs: map[string]*Run{}}
}

// Create registers a new simulation and returns it with its assigned ID.
func (s *Store) Create(newSim *v1.Simulation) (*Run, error) {
	if newSim == nil {
		return nil, fmt.Errorf("simulation is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("sim-%d", s.seq)
	newSim.Id = id
	r := &Run{
		ID:     id,
		Sim:    newSim,
		Status: v1.RunStatus_RUN_STATUS_PENDING,
		subs:   map[chan struct{}]struct{}{},
	}
	s.runs[id] = r
	s.order = append(s.order, id)
	return r, nil
}

// Get fetches a run by ID.
func (s *Store) Get(id string) (*Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, fmt.Errorf("simulation %q not found", id)
	}
	return r, nil
}

// SetStatus transitions status, records the error, and wakes all
// subscribers.
func (r *Run) SetStatus(st v1.RunStatus, errMsg string) {
	r.mu.Lock()
	r.Status = st
	r.Err = errMsg
	r.version++
	subs := r.snapshotSubsLocked()
	r.mu.Unlock()
	r.notifyAll(subs)
}

// SetProgress stores the latest progress snapshot and wakes subscribers.
func (r *Run) SetProgress(pb *v1.ProgressSnapshot) {
	r.mu.Lock()
	r.Progress = pb
	r.version++
	subs := r.snapshotSubsLocked()
	r.mu.Unlock()
	r.notifyAll(subs)
}

// SetResult attaches final outputs and wakes subscribers.
func (r *Run) SetResult(res *sim.RunResult, resolved []providers.ResolvedSpec) {
	r.mu.Lock()
	r.Result = res
	r.Resolved = resolved
	r.version++
	subs := r.snapshotSubsLocked()
	r.mu.Unlock()
	r.notifyAll(subs)
}

// State returns the current version and observable state atomically.
// Stream waiters use the version to detect changes that landed between
// their state read and their subscribe (the missed-wakeup window).
func (r *Run) State() (version uint64, status v1.RunStatus, errMsg string, progress *v1.ProgressSnapshot) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version, r.Status, r.Err, r.Progress
}

// subscribe registers a wake channel for the next state change. The
// caller passes the version it last observed; if the run has advanced
// since, the subscription is registered and `changed` reports true so
// the caller re-reads state instead of waiting on a wakeup that already
// happened (missed-wakeup guard).
func (r *Run) subscribe(seenVersion uint64) (wake <-chan struct{}, cancel func(), changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan struct{})
	if r.version != seenVersion {
		r.subs[ch] = struct{}{} // still register; cancel() cleans up
		return ch, func() { r.unsubscribe(ch) }, true
	}
	r.subs[ch] = struct{}{}
	return ch, func() { r.unsubscribe(ch) }, false
}

func (r *Run) unsubscribe(ch chan struct{}) {
	r.mu.Lock()
	delete(r.subs, ch)
	r.mu.Unlock()
}

// snapshotSubsLocked drains the subscriber set and returns the wake
// channels (caller must hold mu). Draining — rather than copying — is
// essential: each channel is closed exactly once by notifyAll, and any
// notification racing with a slow subscriber cannot double-close.
func (r *Run) snapshotSubsLocked() []chan struct{} {
	out := make([]chan struct{}, 0, len(r.subs))
	for ch := range r.subs {
		out = append(out, ch)
		delete(r.subs, ch)
	}
	return out
}

// notifyAll closes wake channels outside the lock.
func (r *Run) notifyAll(subs []chan struct{}) {
	for _, ch := range subs {
		close(ch)
	}
}

// ErrPendingRun is returned when results are requested before the run
// has finished.
var ErrNotCompleted = fmt.Errorf("simulation has not completed; results are unavailable until status is COMPLETED")

// ResultsFor returns the finished result or an error carrying the run's
// current state.
func (r *Run) ResultsFor() (*sim.RunResult, []providers.ResolvedSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	switch r.Status {
	case v1.RunStatus_RUN_STATUS_COMPLETED:
		return r.Result, r.Resolved, nil
	case v1.RunStatus_RUN_STATUS_FAILED:
		return nil, nil, fmt.Errorf("simulation failed: %s", r.Err)
	case v1.RunStatus_RUN_STATUS_RUNNING:
		return nil, nil, fmt.Errorf("simulation is still running")
	default:
		return nil, nil, ErrNotCompleted
	}
}
