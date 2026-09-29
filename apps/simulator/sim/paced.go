package sim

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// PacedRun is a simulation executing on its own goroutine at a wall-clock
// pace, with interactive control. It is the API layer's handle for
// run/pause/resume/stop/speed without the API owning simulation state.
type PacedRun struct {
	control SimRunControl // engine.Runner
	done    chan struct{} // closed when the goroutine finishes
	result  atomic.Pointer[RunResult]

	// Fatal error recovered inside the run goroutine (see Err). Guarded
	// by errMu because the writer is that goroutine and the reader is
	// whoever observes Done.
	errMu sync.Mutex
	err   error
}

// Control returns the run-control handle (Pause/Resume/Stop/Paused).
func (p *PacedRun) Control() SimRunControl { return p.control }

// Done returns a channel closed when the run finishes (completed,
// horizon-bounded, or stopped).
func (p *PacedRun) Done() <-chan struct{} { return p.done }

// WaitResult blocks until the run finishes and returns its result.
func (p *PacedRun) WaitResult() *RunResult {
	<-p.done
	return p.result.Load()
}

// StartPaced validates inputs exactly like Simulate, then executes the
// simulation on a background goroutine at a wall-clock pace:
//
//	wallDurationMS = 0 → tick = 0 → run as fast as possible
//	wallDurationMS > 0 → the full horizon takes that much wall time,
//	adjustable live via the runner's SetTick (the speed buttons).
//
// Progress snapshots flow through opts.Progress exactly as in Simulate.
// Event ordering, RNG consumption, and results are identical to Simulate
// for the same inputs and seed — pacing changes only the wall-clock
// duration, never the simulated behavior (the AGENTS.md rule: animation
// must never determine simulation behavior).
func StartPaced(arch Architecture, spec workload.Spec, opts Options, wallDurationMS float64) (*PacedRun, error) {
	// Full up-front validation + plan derivation: the caller gets the
	// same errors Simulate would produce, before any goroutine starts.
	if err := arch.Validate(); err != nil {
		return nil, fmt.Errorf("sim: invalid architecture: %w", err)
	}
	plan, err := workload.Derive(spec)
	if err != nil {
		return nil, fmt.Errorf("sim: invalid workload: %w", err)
	}
	if opts.DurationMS <= 0 {
		opts.DurationMS = 60_000
	}
	if opts.Seed == 0 {
		opts.Seed = 1
	}
	known := map[string]ComponentSpec{}
	for _, c := range arch.Components {
		known[c.ID] = c
	}
	if err := validateOptions(opts, plan, known); err != nil {
		return nil, err
	}
	for i, f := range opts.Failures {
		if err := f.validate(known); err != nil {
			return nil, fmt.Errorf("sim: failure %d: %w", i, err)
		}
	}
	if opts.TrackWindows <= 0 {
		opts.TrackWindows = 1000
	}

	clock := engine.NewClock()
	pq := engine.NewPriorityQueue()
	sched := engine.NewScheduler(pq, clock)
	sched.SetSeed(opts.Seed)
	runner := engine.NewRunner(sched, clock)
	runner.MaxEvents = maxEventBudget

	k := newKernel(arch, plan, opts)
	runner.Horizon = engine.Time(delay(opts.DurationMS))

	// Telemetry plumbing (mirrors Simulate).
	k.eventsProcessed = func() uint64 { return runner.Processed() }
	k.pendingEvents = func() int { return runner.Pending() }

	// Prime the arrival chain: the first arrival, then each arrival
	// handler schedules the next.
	firstGap := k.arrival.NextGapMillis()
	sched.Schedule(engine.Event{
		Timestamp: engine.Time(delay(firstGap)),
		Type:      "workload.arrive",
		Handler:   k.onArrival,
	})
	k.startSampler(sched)

	// Inject failures: each schedules its own start/stop events.
	for _, f := range opts.Failures {
		target, ftype := f.Target, f.Type
		sched.Schedule(engine.Event{
			Timestamp:   engine.Time(delay(f.StartMS)),
			Type:        "failure.start",
			ComponentID: f.Target,
			Handler: func(ctx *engine.Context) []engine.Event {
				k.rt(target).failure = &failState{failure: f}
				ctx.Logf("FAILURE %s ON %s", ftype, target)
				return nil
			},
		})
		sched.Schedule(engine.Event{
			Timestamp:   engine.Time(delay(f.StartMS + f.DurationMS)),
			Type:        "failure.stop",
			ComponentID: f.Target,
			Handler: func(ctx *engine.Context) []engine.Event {
				k.rt(target).failure = nil
				ctx.Logf("RECOVERY %s", target)
				return nil
			},
		})
	}

	// Wall-clock pacing: tick = wall duration ÷ estimated event budget.
	// The budget estimate only scales the initial tick; the API layer's
	// speed control re-targets it live anyway.
	const eventsPerSimMS = 2 // conservative: larger budget → smaller tick
	var tick time.Duration   // zero default: as fast as possible
	if wallDurationMS > 0 {
		budget := int64(opts.DurationMS * eventsPerSimMS)
		if budget < 1 {
			budget = 1
		}
		tickNS := int64(wallDurationMS * float64(time.Millisecond) / float64(budget))
		if tickNS < 1 {
			tickNS = 1
		}
		tick = time.Duration(tickNS)
	}
	runner.SetTick(tick)

	done := make(chan struct{})
	p := &PacedRun{control: runner, done: done}

	// The run executes on this goroutine, far from the caller's deferred
	// recover(). An engine panic here (negative delay, exhausted event
	// budget arithmetic, …) must fail THIS run, not the whole server, so
	// the goroutine recovers and publishes a result carrying the error.
	go func() {
		defer close(done)
		defer func() {
			if rec := recover(); rec != nil {
				p.setErr(fmt.Errorf("sim: engine panic: %v", rec))
			}
		}()
		events := runner.Run()
		p.result.Store(&RunResult{
			Plan:    plan,
			Events:  *events,
			Metrics: k.snapshot(durationOr(events, opts.DurationMS)),
		})
	}()

	return p, nil
}

// Err reports a fatal error captured while the paced goroutine was
// running (an engine panic recovered in place). It is safe to call after
// Done closes; before that it returns nil.
func (p *PacedRun) Err() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.err
}

// setErr records a fatal run error exactly once.
func (p *PacedRun) setErr(err error) {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.err == nil {
		p.err = err
	}
}

// SetTick re-targets the wall-clock pace of a live run (speed control).
// Implemented on *engine.Runner; see engine/runner.go.
