package sim

import (
	"fmt"
	"sort"
	"time"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// ComponentRuntime is a component's live state during a run.
type ComponentRuntime struct {
	spec ComponentSpec

	queue    []*Request
	inFlight int

	// failure is the active injected failure, or nil when healthy.
	failure *failState

	// Metrics (see ComponentMetrics for definitions).
	Arrived       uint64
	Completed     uint64
	Rejected      uint64
	Failed        uint64
	MaxQueueDepth int
	BusySumMS     float64
	WaitSumMS     float64
	WaitCount     uint64
}

// Request is the mutable state traveling through the architecture. One
// request is at exactly one place at any simulated instant (fan-out makes
// independent copies).
type Request struct {
	ID      uint64
	Read    bool // true = read op, false = write op
	Path    []string
	StartMS float64 // simulated ms when the client generated it
	EndMS   float64 // simulated ms when it completed or was rejected

	QueueEnterMS float64 // when it joined the current component's queue
	CacheHit     bool

	// Attempt is 1 on the first try; retries increment it.
	Attempt int
	// RetriesLeft counts remaining retries for this request.
	RetriesLeft int
	// Prev is the component that forwarded the request here (the caller).
	// When a hop fails, the CALLER — not the failing component — performs
	// the retry, matching real client behavior.
	Prev string
	// TimeoutMS is the end-to-end budget; exceeding it fails the request
	// as a timeout. Zero means no timeout.
	TimeoutMS float64
}

// FailureRecord summarizes one request-level failure for reporting.
// ComponentID is where the failure was detected (the serving component);
// CallerID is the upstream component that initiated the request and — for
// timeouts — whose budget was exceeded.
type FailureRecord struct {
	RequestID   uint64
	ComponentID string
	CallerID    string
	Kind        string // "timeout", "error", "dropped", "crash"
	Attempt     int
	AtMS        float64
	LatencyMS   float64
	Path        []string
}

// RetryPolicy describes retry/timeout behavior applied by the components
// named in Options.RetryOn.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// BackoffBaseMS is the mean exponential backoff before the first
	// retry; it doubles per subsequent attempt.
	BackoffBaseMS float64
	// TimeoutMS is the end-to-end request budget. Zero means none.
	TimeoutMS float64
}

// Options configures a simulation run.
type Options struct {
	// Seed drives all randomness: arrival gaps, read/write labels, cache
	// hits, failure rolls, backoff draws, per-event RNG streams.
	Seed uint64
	// DurationMS is the simulated horizon: arrivals are generated up to
	// this instant; events at or beyond it are left pending.
	DurationMS float64

	// Failures are validated, then injected at their start times.
	Failures []Failure

	// Retry configures the retry/timeout behavior of the components named
	// in RetryOn. Retries re-enter the same component the attempt failed
	// at (crash pass-through retries at the downstream target).
	Retry RetryPolicy
	// RetryOn lists component IDs that may retry failed requests they
	// forwarded. Empty disables retries everywhere.
	RetryOn []string

	// TrackWindows, when > 0, samples utilization/queue depth every N
	// simulated milliseconds so diagnosis can detect growing queues.
	TrackWindows int

	// Progress, when non-nil, is invoked with periodic snapshots of real
	// run state (same cadence as trend sampling). Purely observational:
	// it cannot influence the simulation. See ProgressHook.
	Progress ProgressHook
}

// ComponentMetrics is a point-in-time report for one component.
//
// Definitions:
//   - Arrived:      cumulative requests that entered the component.
//   - Completed:    cumulative requests that finished service here and
//     hopped onward or terminated with a response.
//   - Rejected:     cumulative requests refused at the door because the
//     concurrency slots were full and the queue was at its limit.
//     Rejected requests never occupy service time.
//   - Failed:       cumulative requests that failed while at this
//     component (timeout, injected error, crash, drop).
//   - QueueDepth:   requests currently waiting for a slot (instantaneous).
//   - MaxQueueDepth: high-water mark of QueueDepth (cumulative max).
//   - InFlight:     requests currently in service (instantaneous).
//   - Utilization:  BusySumMS / (concurrency × window) — average fraction
//     of the component's service capacity in use over the run window.
//   - AvgQueueWaitMS: mean time requests spent in this component's queue
//     before service.
//   - ThroughputRPS: Completed divided by the simulated window (average).
//   - ArrivalRPS:   Arrived divided by the simulated window (average).
//   - Saturated:    ArrivalRPS exceeded CapacityRPS (when CapacityRPS > 0).
//   - AvgServiceMS: mean service time actually executed (busy-ms over
//     completions) — real measured data, not the spec value.
//   - QueueTrend:   direction of occupancy over the run's sampled windows
//     ("growing", "draining", "stable"; "unknown" without samples).
type ComponentMetrics struct {
	ID             string
	Kind           ComponentKind
	Arrived        uint64
	Completed      uint64
	Rejected       uint64
	Failed         uint64
	QueueDepth     int
	MaxQueueDepth  int
	InFlight       int
	Utilization    float64
	AvgQueueWaitMS float64
	AvgServiceMS   float64
	ThroughputRPS  float64
	ArrivalRPS     float64
	CapacityRPS    float64
	QueueTrend     string
	Saturated      bool
}

// Metrics is the system-wide result of a run.
//
//   - Generated: requests the workload produced.
//   - Completed: requests that received a full response.
//   - Rejected:  requests refused at some component's capacity limit.
//   - Failed:    requests terminated by timeout, injected error, crash,
//     or drop (retries exhausted or no retry configured).
//   - InFlight:  Generated - Completed - Rejected - Failed (still pending
//     at horizon; large values mean the horizon is too short or the
//     system is hopelessly saturated).
//   - ErrorRate / TimeoutRate / DroppedRate are over Generated.
//   - Latency percentiles are nearest-rank over terminal outcomes:
//     completed requests plus requests that failed mid-path (timeout,
//     injected error, drop — at their failure latency, which is what the
//     caller actually experienced). Admission rejections are excluded
//     (they never entered service; they are tracked as a rate).
type Metrics struct {
	DurationMS   float64
	Generated    uint64
	Completed    uint64
	Rejected     uint64
	Failed       uint64
	Timeouts     uint64
	Dropped      uint64
	InFlight     uint64
	ErrorRate    float64
	TimeoutRate  float64
	AvgLatencyMS float64
	P50MS        float64
	P95MS        float64
	P99MS        float64
	MaxLatencyMS float64
	Components   []ComponentMetrics
	Failures     []FailureRecord
}

// RunResult bundles everything one run produced.
type RunResult struct {
	Plan    workload.Plan
	Metrics Metrics
	Events  engine.Results
}

// Kernel wires the architecture to the engine and owns all run state.
type Kernel struct {
	arch     Architecture
	plan     workload.Plan
	opts     Options
	runtimes map[string]*ComponentRuntime
	order    []string // architecture order; deterministic iteration
	clientID string
	outgoing map[string][]Link

	ids      *workload.IDAllocator
	splitter *workload.Splitter
	arrival  *workload.ArrivalClock
	failRNG  *engine.RNG // failure/backoff rolls, independent stream

	progress ProgressHook

	// Telemetry for the progress hook: wall-clock start of the run and
	// live engine counters, plumbed in by Simulate. Observational only —
	// the kernel never makes decisions from these.
	startedWall     time.Time
	eventsProcessed func() uint64
	pendingEvents   func() int

	retryOn map[string]bool

	latencies []float64
	generated uint64
	completed uint64
	rejected  uint64
	failed    uint64
	timeouts  uint64
	dropped   uint64

	failures []FailureRecord

	// window tracking for trend diagnosis
	windowMS   float64
	windows    []windowSample
	samplerDue float64
	sampledIDs []string // components tracked by the sampler
}

// windowSample is one point-in-time snapshot of a component's occupancy.
type windowSample struct {
	atMS      float64
	occupancy int // in service + queued
	inService int
}

// newKernel builds runtime state from the architecture. Callers must
// Validate the architecture first.
func newKernel(arch Architecture, plan workload.Plan, opts Options) *Kernel {
	k := &Kernel{
		arch:            arch,
		plan:            plan,
		opts:            opts,
		runtimes:        make(map[string]*ComponentRuntime, len(arch.Components)),
		outgoing:        make(map[string][]Link, len(arch.Components)),
		ids:             workload.NewIDAllocator(),
		splitter:        workload.NewSplitter(opts.Seed^0xA11CE, plan.ReadFraction),
		arrival:         workload.NewArrivalClock(opts.Seed^0xB0B, plan.MeanInterArrivalMillis),
		failRNG:         engine.NewRNG(opts.Seed ^ 0xF41A11),
		retryOn:         make(map[string]bool, len(opts.RetryOn)),
		windowMS:        float64(opts.TrackWindows),
		progress:        opts.Progress,
		startedWall:     time.Now(),
		eventsProcessed: func() uint64 { return 0 },
		pendingEvents:   func() int { return 0 },
	}
	for _, id := range opts.RetryOn {
		k.retryOn[id] = true
	}
	for _, c := range arch.Components {
		k.runtimes[c.ID] = &ComponentRuntime{spec: c}
		k.order = append(k.order, c.ID)
		if c.Kind == KindClient {
			k.clientID = c.ID
		}
	}
	for _, l := range arch.Links {
		k.outgoing[l.From] = append(k.outgoing[l.From], l)
	}
	for _, id := range k.order {
		if k.runtimes[id].spec.Concurrency > 0 {
			k.sampledIDs = append(k.sampledIDs, id)
		}
	}
	return k
}

func (k *Kernel) rt(id string) *ComponentRuntime { return k.runtimes[id] }

// Simulate validates the inputs, derives the load plan, runs the discrete
// event simulation, and reports metrics. It is fully deterministic:
// identical (architecture, workload, options) produce identical results.
func Simulate(arch Architecture, spec workload.Spec, opts Options) (*RunResult, error) {
	if err := arch.Validate(); err != nil {
		return nil, fmt.Errorf("sim: invalid architecture: %w", err)
	}
	plan, err := workload.Derive(spec)
	if err != nil {
		return nil, fmt.Errorf("sim: invalid workload: %w", err)
	}
	if opts.DurationMS <= 0 {
		opts.DurationMS = 60_000 // default: one simulated minute
	}
	if opts.Seed == 0 {
		opts.Seed = 1
	}
	known := map[string]ComponentSpec{}
	for _, c := range arch.Components {
		known[c.ID] = c
	}
	for i, f := range opts.Failures {
		if err := f.validate(known); err != nil {
			return nil, fmt.Errorf("sim: failure %d: %w", i, err)
		}
	}
	if opts.TrackWindows <= 0 {
		opts.TrackWindows = 1000 // default: 1s trend windows
	}

	clock := engine.NewClock()
	pq := engine.NewPriorityQueue()
	sched := engine.NewScheduler(pq, clock)
	sched.SetSeed(opts.Seed)
	runner := engine.NewRunner(sched, clock)

	k := newKernel(arch, plan, opts)
	runner.Horizon = engine.Time(delay(opts.DurationMS))

	// Prime the arrival chain: the first arrival, then each arrival
	// handler schedules the next.
	firstGap := k.arrival.NextGapMillis()
	sched.Schedule(engine.Event{
		Timestamp: engine.Time(delay(firstGap)),
		Type:      "workload.arrive",
		Handler:   k.onArrival,
	})

	// Start the periodic occupancy sampler for trend diagnosis.
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

	// Telemetry plumbing: let the kernel observe the engine's counters
	// for the progress hook. These closures read state after the tick
	// boundary of each event, so the values they report are stable.
	k.eventsProcessed = func() uint64 { return runner.Processed() }
	k.pendingEvents = func() int { return runner.Pending() }

	events := runner.Run()
	return &RunResult{
		Plan:   plan,
		Events: *events,
		// DurationMS reflects the simulated span actually measured: the
		// configured horizon normally, but a stopped run measures only
		// the span it processed so rates stay honest (requests ÷ elapsed
		// sim time, not requests ÷ horizon).
		Metrics: k.snapshot(durationOr(events, opts.DurationMS)),
	}, nil
}

// durationOr returns the simulated span the run actually covered: the
// final event timestamp when the run stopped early, else the configured
// duration. Floor of 1ms keeps rates finite.
func durationOr(events *engine.Results, configured float64) float64 {
	if events != nil && events.StopReason == engine.StopStopped && events.FinalTime > 0 {
		ms := float64(events.FinalTime) / 1e6
		if ms > 1 {
			return ms
		}
		return 1
	}
	return configured
}

// ProgressHook receives periodic mid-run snapshots of real kernel state
// while the simulation executes. It is called synchronously on the
// sampler's scheduled events, so it must be fast and must not mutate the
// kernel. It exists so an API layer can stream progress WITHOUT owning
// simulation state; the engine and kernel stay transport-agnostic.
// Snapshot values are the same quantities the sampler records for trend
// diagnosis: instantaneous queue depth, in-flight slots, cumulative
// arrivals/completions, and utilization over the elapsed window.
type ProgressHook func(Snapshot)

// SimRunControl exposes control over an in-flight run: pause at the
// current simulated instant, resume, or stop early. Implemented by
// engine.Runner; StartPaced returns it so an API layer can drive a run
// interactively without importing the engine.
type SimRunControl interface {
	Pause() bool
	Resume()
	Stop()
	Paused() bool
}

// Snapshot is one point-in-time view of the run (all values measured,
// none derived after the fact).
type Snapshot struct {
	// SimTimeMS is the current simulated time.
	SimTimeMS float64
	// DurationMS is the configured horizon.
	DurationMS float64
	// System counters (cumulative, matching Metrics definitions).
	Generated uint64
	Completed uint64
	Rejected  uint64
	Failed    uint64
	// InFlight: Generated − Completed − Rejected − Failed.
	InFlight int64
	// Components: one entry per constrained component, in architecture
	// order (deterministic).
	Components []ComponentOccupancy
	// WallElapsedMS is real wall-clock time since the run started. Read
	// alongside SimTimeMS it makes the pacing mode visible; it is never
	// part of any metric (sim time drives all of those).
	WallElapsedMS float64
	// EventsProcessed / EventsPending are the engine's live counters.
	EventsProcessed uint64
	EventsPending   int64
}

// ComponentOccupancy is one component's instantaneous occupancy.
type ComponentOccupancy struct {
	ID          string
	QueueDepth  int
	InFlight    int
	Arrived     uint64
	Completed   uint64
	Utilization float64
}

// emitSnapshot builds the current snapshot for the progress hook.
func (k *Kernel) emitSnapshot(nowMS float64) {
	if k.progress == nil {
		return
	}
	snap := Snapshot{
		SimTimeMS:  nowMS,
		DurationMS: k.opts.DurationMS,
		Generated:  k.generated,
		Completed:  k.completed,
		Rejected:   k.rejected,
		Failed:     k.failed,
		InFlight:   int64(k.generated) - int64(k.completed) - int64(k.rejected) - int64(k.failed),
		Components: make([]ComponentOccupancy, 0, len(k.sampledIDs)),
		// Telemetry: real counters from the kernel and its runner — the
		// wall-clock origin the sampler chain started at (pause-aware,
		// because the runner idles inside ticks while paused) and the
		// engine's processed/pending event counts. Pacing therefore never
		// leaks into displayed metrics.
		WallElapsedMS:   float64(time.Since(k.startedWall)) / float64(time.Millisecond),
		EventsProcessed: k.eventsProcessed(),
		EventsPending:   int64(k.pendingEvents()),
	}
	for _, id := range k.sampledIDs {
		rt := k.rt(id)
		util := 0.0
		if rt.spec.Concurrency > 0 {
			util = rt.BusySumMS / (float64(rt.spec.Concurrency) * maxFloat64(nowMS, 1))
		}
		snap.Components = append(snap.Components, ComponentOccupancy{
			ID:          id,
			QueueDepth:  len(rt.queue),
			InFlight:    rt.inFlight,
			Arrived:     rt.Arrived,
			Completed:   rt.Completed,
			Utilization: util,
		})
	}
	k.progress(snap)
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// startSampler begins the periodic occupancy sampling used by trend
// diagnosis. Each sample schedules the next; the horizon ends the chain.
func (k *Kernel) startSampler(sched *engine.Scheduler) {
	sched.Schedule(engine.Event{
		Timestamp: engine.Time(delay(k.windowMS)),
		Type:      "metrics.sample",
		Handler:   k.sampleWindow,
	})
}

// onArrival generates one request and keeps the Poisson arrival process
// going.
func (k *Kernel) onArrival(ctx *engine.Context) []engine.Event {
	req := &Request{
		ID:          k.ids.Next(),
		Read:        k.splitter.IsRead(),
		StartMS:     nowMillis(ctx),
		Attempt:     1,
		RetriesLeft: k.opts.Retry.MaxRetries,
		// End-to-end budget: applied to every request when configured.
		TimeoutMS: k.opts.Retry.TimeoutMS,
	}
	k.generated++
	ctx.Logf("generated request %d (read=%v)", req.ID, req.Read)
	k.dispatch(ctx, k.clientID, req)

	gap := k.arrival.NextGapMillis()
	ctx.Schedule(delay(gap), engine.Event{Type: "workload.arrive", Handler: k.onArrival})
	return nil
}

// dispatch admits a request into a component: it either takes a service
// slot, joins the queue, or is rejected when both are full. Crash and
// network-failure state short-circuit admission.
func (k *Kernel) dispatch(ctx *engine.Context, compID string, r *Request) {
	rt := k.rt(compID)
	rt.Arrived++
	r.Path = append(r.Path, compID)

	// Failure admission behavior.
	if fs := rt.failure; fs.active() {
		switch fs.failure.Type {
		case FailureCrash:
			if fs.failure.Config.PassThrough {
				// Failed component forwards untouched (cache → origin).
				ctx.Logf("request %d bypasses crashed %s (pass-through)", r.ID, compID)
				k.hop(ctx, compID, r)
				return
			}
			rt.Failed++
			ctx.Logf("request %d CRASH-FAIL at %s", r.ID, compID)
			k.failRequest(ctx, compID, r, "crash")
			return
		case FailureNetwork:
			if fs.failure.Config.PacketLossRate > 0 &&
				k.failRNG.Float64() < fs.failure.Config.PacketLossRate {
				rt.Failed++
				ctx.Logf("request %d DROPPED at %s (packet loss)", r.ID, compID)
				k.failRequest(ctx, compID, r, "dropped")
				return
			}
		}
	}

	// Unconstrained components (client, network) are pure latency hops.
	if rt.spec.Concurrency <= 0 {
		k.beginService(ctx, compID, r)
		return
	}

	// Capacity limit: all slots busy AND queue at its limit → reject.
	if rt.spec.QueueLimit > 0 && rt.inFlight >= rt.spec.Concurrency && len(rt.queue) >= rt.spec.QueueLimit {
		rt.Rejected++
		ctx.Logf("request %d REJECTED at %s (inFlight=%d queue=%d)",
			r.ID, compID, rt.inFlight, len(rt.queue))
		k.rejectRequest(ctx, r)
		return
	}

	r.QueueEnterMS = nowMillis(ctx)
	rt.queue = append(rt.queue, r)
	if len(rt.queue) > rt.MaxQueueDepth {
		rt.MaxQueueDepth = len(rt.queue)
	}
	k.pump(ctx, compID)
}

// pump starts service for queued requests while slots are free.
func (k *Kernel) pump(ctx *engine.Context, compID string) {
	rt := k.rt(compID)
	for rt.inFlight < rt.spec.Concurrency && len(rt.queue) > 0 {
		r := rt.queue[0]
		rt.queue = rt.queue[1:]
		k.beginService(ctx, compID, r)
	}
}

// beginService occupies a slot and schedules this request's completion.
func (k *Kernel) beginService(ctx *engine.Context, compID string, r *Request) {
	rt := k.rt(compID)
	rt.inFlight++

	if r.QueueEnterMS > 0 {
		wait := nowMillis(ctx) - r.QueueEnterMS
		if wait < 0 {
			wait = 0
		}
		rt.WaitSumMS += wait
		rt.WaitCount++
		r.QueueEnterMS = 0
	}

	serviceMS := rt.spec.ServiceTime(opFor(rt.spec, r))
	if fs := rt.failure; fs.active() {
		switch fs.failure.Type {
		case FailureLatency, FailureNetwork:
			serviceMS += fs.failure.Config.AddedLatencyMillis
		}
	}
	ctx.Logf("request %d begins %s at %s for %.3fms", r.ID, opFor(rt.spec, r), compID, serviceMS)

	svcMS := serviceMS
	startMS := nowMillis(ctx)
	comp := compID
	req := r

	ctx.Schedule(delay(serviceMS), engine.Event{
		Type:        "component.finish",
		ComponentID: compID,
		RequestID:   r.ID,
		Handler: func(ctx *engine.Context) []engine.Event {
			k.finishService(ctx, comp, req, svcMS, startMS)
			return nil
		},
	})
}

// finishService releases the slot, applies error injections, handles the
// cache hit path, and moves the request onward.
func (k *Kernel) finishService(ctx *engine.Context, compID string, r *Request, serviceMS, startMS float64) {
	rt := k.rt(compID)
	rt.inFlight--
	rt.Completed++
	rt.BusySumMS += serviceMS

	// End-to-end timeout check: the budget applies at every hop exit.
	if r.TimeoutMS > 0 && nowMillis(ctx)-r.StartMS > r.TimeoutMS {
		rt.Failed++
		ctx.Logf("request %d TIMEOUT after %s hop (%.1fms elapsed > %.1fms budget)",
			r.ID, compID, nowMillis(ctx)-r.StartMS, r.TimeoutMS)
		k.failRequest(ctx, compID, r, "timeout")
		k.pump(ctx, compID)
		return
	}

	// Injected error rate: a fraction of serviced requests fail here.
	if fs := rt.failure; fs.active() && fs.failure.Type == FailureErrorRate {
		if k.failRNG.Float64() < fs.failure.Config.ErrorRate {
			rt.Failed++
			ctx.Logf("request %d ERROR at %s (injected error rate)", r.ID, compID)
			k.failRequest(ctx, compID, r, "error")
			k.pump(ctx, compID)
			return
		}
	}

	// Cache behavior: a read that hits is served here and terminates with
	// a response; misses and writes continue downstream.
	if rt.spec.Kind == KindCache && r.Read && !r.CacheHit {
		if ctx.Rand().Float64() < rt.spec.HitRatio {
			r.CacheHit = true
			ctx.Logf("request %d CACHE HIT at %s", r.ID, compID)
			k.completeRequest(ctx, r)
			k.pump(ctx, compID)
			return
		}
		ctx.Logf("request %d cache miss at %s", r.ID, compID)
	}

	k.pump(ctx, compID)
	k.hop(ctx, compID, r)
}

// route returns the components a request should visit next.
func (k *Kernel) route(fromID string, r *Request) []string {
	var targets []string
	for _, l := range k.outgoing[fromID] {
		switch l.Condition {
		case "":
			targets = append(targets, l.To)
		case "read":
			if r.Read {
				targets = append(targets, l.To)
			}
		case "write":
			if !r.Read {
				targets = append(targets, l.To)
			}
		}
	}
	return targets
}

// hop moves a request to its next component(s), or terminates it with a
// response when no route remains.
func (k *Kernel) hop(ctx *engine.Context, fromID string, r *Request) bool {
	targets := k.route(fromID, r)
	if len(targets) == 0 {
		k.completeRequest(ctx, r)
		return true
	}
	for i, t := range targets {
		if i == 0 {
			r.Prev = fromID
			k.dispatch(ctx, t, r)
			continue
		}
		clone := *r // fan-out: independent legs, same logical request
		clone.Prev = fromID
		clone.Path = append([]string(nil), r.Path...)
		k.dispatch(ctx, t, &clone)
	}
	return false
}

// completeRequest records a successful end-to-end response.
func (k *Kernel) completeRequest(ctx *engine.Context, r *Request) {
	r.EndMS = nowMillis(ctx)
	k.latencies = append(k.latencies, r.EndMS-r.StartMS)
	k.completed++
	ctx.Logf("request %d completed in %.3fms via %v", r.ID, r.EndMS-r.StartMS, r.Path)
}

// rejectRequest terminates a request that could not be admitted.
func (k *Kernel) rejectRequest(ctx *engine.Context, r *Request) {
	r.EndMS = nowMillis(ctx)
	k.rejected++
}

// failRequest terminates a request after a failure. If the failing
// component is in RetryOn and retries remain, a retry is scheduled
// instead (backoff, then re-dispatch at the failing component); the
// failure is recorded per attempt but does not count as terminal yet.
func (k *Kernel) failRequest(ctx *engine.Context, compID string, r *Request, kind string) {
	k.failures = append(k.failures, FailureRecord{
		RequestID:   r.ID,
		ComponentID: compID,
		CallerID:    r.Prev,
		Kind:        kind,
		Attempt:     r.Attempt,
		AtMS:        nowMillis(ctx),
		LatencyMS:   nowMillis(ctx) - r.StartMS,
		Path:        append([]string(nil), r.Path...),
	})

	if k.retryOn[r.Prev] && r.RetriesLeft > 0 {
		r.RetriesLeft--
		r.Attempt++
		backoff := k.opts.Retry.BackoffBaseMS * float64(int(1)<<(r.Attempt-2)) // doubles per attempt
		if backoff > 0 {
			backoff *= 0.5 + k.failRNG.ExpFloat64() // jittered exponential, mean backoff
		}
		ctx.Logf("request %d will RETRY at %s in %.2fms (attempt %d, %d left)",
			r.ID, compID, backoff, r.Attempt, r.RetriesLeft)
		next := *r
		next.Path = append([]string(nil), r.Path...)
		ctx.Schedule(delay(backoff), engine.Event{
			Type:        "request.retry",
			ComponentID: compID,
			RequestID:   r.ID,
			Handler: func(ctx *engine.Context) []engine.Event {
				k.dispatch(ctx, compID, &next)
				return nil
			},
		})
		return
	}

	// Terminal failure: the caller experienced latency up to the failure
	// point, so it enters the latency distribution (see Metrics docs).
	r.EndMS = nowMillis(ctx)
	k.latencies = append(k.latencies, r.EndMS-r.StartMS)
	k.failed++
	switch kind {
	case "timeout":
		k.timeouts++
	case "dropped":
		k.dropped++
	}
}

// opFor maps (component kind, request class) to the priced operation.
func opFor(spec ComponentSpec, r *Request) Op {
	switch spec.Kind {
	case KindCache, KindDatabase:
		if r.Read {
			return OpRead
		}
		return OpWrite
	case KindObjectStorage:
		if r.Read {
			return OpGet
		}
		return OpPut
	case KindQueue:
		return OpEnqueue
	case KindWorker:
		return OpCompute
	case KindNetwork:
		return OpTransit
	case KindClient:
		return OpComplete
	default: // api_server, load_balancer
		return OpCompute
	}
}

// sampleWindow records occupancy snapshots for trend diagnosis.
func (k *Kernel) sampleWindow(ctx *engine.Context) []engine.Event {
	at := nowMillis(ctx)
	for _, id := range k.order {
		rt := k.rt(id)
		if rt.spec.Concurrency <= 0 {
			continue
		}
		k.windows = append(k.windows, windowSample{
			atMS:      at,
			occupancy: rt.inFlight + len(rt.queue),
			inService: rt.inFlight,
		})
	}
	k.samplerDue = at + k.windowMS
	k.emitSnapshot(at) // observe-only; cannot affect event scheduling
	ctx.Schedule(delay(k.windowMS), engine.Event{Type: "metrics.sample", Handler: k.sampleWindow})
	return nil
}

// snapshot builds the immutable metrics report.
func (k *Kernel) snapshot(durationMS float64) Metrics {
	m := Metrics{
		DurationMS: durationMS,
		Generated:  k.generated,
		Completed:  k.completed,
		Rejected:   k.rejected,
		Failed:     k.failed,
		Timeouts:   k.timeouts,
		Dropped:    k.dropped,
		InFlight:   k.generated - k.completed - k.rejected - k.failed,
		Failures:   k.failures,
	}
	if k.generated > 0 {
		m.ErrorRate = float64(k.failed) / float64(k.generated)
		m.TimeoutRate = float64(k.timeouts) / float64(k.generated)
	}
	if len(k.latencies) > 0 {
		sorted := append([]float64(nil), k.latencies...)
		sort.Float64s(sorted)
		var sum float64
		for _, v := range sorted {
			sum += v
		}
		m.AvgLatencyMS = sum / float64(len(sorted))
		m.P50MS = nearestRank(sorted, 0.50)
		m.P95MS = nearestRank(sorted, 0.95)
		m.P99MS = nearestRank(sorted, 0.99)
		m.MaxLatencyMS = sorted[len(sorted)-1]
	}
	windowSec := durationMS / 1000
	for _, id := range k.order {
		rt := k.runtimes[id]
		cm := ComponentMetrics{
			ID:            id,
			Kind:          rt.spec.Kind,
			Arrived:       rt.Arrived,
			Completed:     rt.Completed,
			Rejected:      rt.Rejected,
			Failed:        rt.Failed,
			QueueDepth:    len(rt.queue),
			MaxQueueDepth: rt.MaxQueueDepth,
			InFlight:      rt.inFlight,
			ThroughputRPS: float64(rt.Completed) / windowSec,
			ArrivalRPS:    float64(rt.Arrived) / windowSec,
		}
		if rt.WaitCount > 0 {
			cm.AvgQueueWaitMS = rt.WaitSumMS / float64(rt.WaitCount)
		}
		if rt.spec.Concurrency > 0 {
			cm.Utilization = rt.BusySumMS / (float64(rt.spec.Concurrency) * durationMS)
		}
		if rt.spec.CapacityRPS > 0 {
			cm.CapacityRPS = rt.spec.CapacityRPS
			if cm.ArrivalRPS > rt.spec.CapacityRPS {
				cm.Saturated = true
			}
		}
		if rt.Completed > 0 {
			cm.AvgServiceMS = rt.BusySumMS / float64(rt.Completed)
		}
		cm.QueueTrend = k.queueTrend(id)
		m.Components = append(m.Components, cm)
	}
	return m
}

// queueTrend classifies a component's occupancy trajectory from sampled
// windows: compare the mean of the last third of samples against the
// first third. Requires at least 6 samples of that component.
func (k *Kernel) queueTrend(id string) string {
	idx := -1
	for i, sid := range k.sampledIDs {
		if sid == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "unknown"
	}
	n := len(k.sampledIDs)
	var series []float64
	for i := idx; i < len(k.windows); i += n {
		series = append(series, float64(k.windows[i].occupancy))
	}
	if len(series) < 6 {
		return "unknown"
	}
	third := len(series) / 3
	var first, last float64
	for i := 0; i < third; i++ {
		first += series[i]
	}
	for i := len(series) - third; i < len(series); i++ {
		last += series[i]
	}
	first /= float64(third)
	last /= float64(third)
	switch {
	case last > first+2 && last > 1.5*first:
		return "growing"
	case first > 2 && last < first/1.5:
		return "draining"
	default:
		return "stable"
	}
}

// nearestRank returns the nearest-rank percentile of a sorted slice.
func nearestRank(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(p*float64(n) + 0.5)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}
