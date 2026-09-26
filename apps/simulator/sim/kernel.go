package sim

import (
	"fmt"
	"sort"

	"github.com/kekubhai/Loadline/apps/simulator/engine"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// Request is the mutable state traveling through the architecture. One
// request is at exactly one place at any simulated instant (fan-out makes
// independent copies).
type Request struct {
	ID           uint64
	Read         bool   // true = read op, false = write op
	Next         string // component the request is heading to
	Path         []string
	StartMS      float64 // simulated ms when the client generated it
	EndMS        float64 // simulated ms when it completed or was rejected
	QueueEnterMS float64 // when it joined the current component's queue
	CacheHit     bool
}

// ComponentRuntime is a component's live state during a run.
type ComponentRuntime struct {
	spec ComponentSpec

	queue    []*Request
	inFlight int

	// Metrics (see ComponentMetrics for definitions).
	Arrived       uint64
	Completed     uint64
	Rejected      uint64
	MaxQueueDepth int
	BusySumMS     float64
	WaitSumMS     float64
	WaitCount     uint64
}

// Options configures a simulation run.
type Options struct {
	// Seed drives all randomness: arrival gaps, read/write labels, cache
	// hits, per-event RNG streams.
	Seed uint64
	// DurationMS is the simulated horizon: arrivals are generated up to
	// this instant; events at or beyond it are left pending.
	DurationMS float64
}

// ComponentMetrics is a point-in-time report for one component.
//
// Definitions:
//   - Arrived:      cumulative requests that entered the component.
//   - Completed:    cumulative requests that finished service here and
//     hopped onward or terminated with a response.
//   - Rejected:     cumulative requests refused at the door because the
//     concurrency slots were full and the queue was at its
//     limit. Rejected requests never occupy service time.
//   - QueueDepth:   requests currently waiting for a slot (instantaneous).
//   - MaxQueueDepth: high-water mark of QueueDepth (cumulative max).
//   - InFlight:     requests currently in service (instantaneous).
//   - Utilization:  BusySumMS / window — the fraction of the simulated
//     window the component's concurrency slots were busy
//     (0..1, can exceed 1 only if Concurrency is 0, in which
//     case it is not reported).
//   - AvgQueueWaitMS: mean time requests spent in this component's queue
//     before service (0 if none waited).
//   - ArrivalRPS:   Arrived divided by the simulated window (average, not
//     instantaneous).
//   - Saturated:    ArrivalRPS exceeded CapacityRPS (when CapacityRPS > 0).
type ComponentMetrics struct {
	ID             string
	Kind           ComponentKind
	Arrived        uint64
	Completed      uint64
	Rejected       uint64
	QueueDepth     int
	MaxQueueDepth  int
	InFlight       int
	Utilization    float64
	AvgQueueWaitMS float64
	ArrivalRPS     float64
	Saturated      bool
}

// Metrics is the system-wide result of a run.
//
//   - Generated: requests the workload produced.
//   - Completed: requests that received a full response.
//   - Rejected:  requests refused at some component's capacity limit.
//   - InFlight:  Generated - Completed - Rejected (still pending at
//     horizon; large values mean the horizon is too short or
//     the system ishopelessly saturated).
//   - Latency percentiles are nearest-rank over completed requests only.
type Metrics struct {
	DurationMS   float64
	Generated    uint64
	Completed    uint64
	Rejected     uint64
	InFlight     uint64
	AvgLatencyMS float64
	P50MS        float64
	P95MS        float64
	P99MS        float64
	MaxLatencyMS float64
	Components   []ComponentMetrics
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

	latencies []float64
	generated uint64
	completed uint64
	rejected  uint64
}

// newKernel builds runtime state from the architecture. Callers must
// Validate the architecture first.
func newKernel(arch Architecture, plan workload.Plan, opts Options) *Kernel {
	k := &Kernel{
		arch:     arch,
		plan:     plan,
		opts:     opts,
		runtimes: make(map[string]*ComponentRuntime, len(arch.Components)),
		outgoing: make(map[string][]Link, len(arch.Components)),
		ids:      workload.NewIDAllocator(),
		splitter: workload.NewSplitter(opts.Seed^0xA11CE, plan.ReadFraction),
		arrival:  workload.NewArrivalClock(opts.Seed^0xB0B, plan.MeanInterArrivalMillis),
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

	events := runner.Run()
	return &RunResult{
		Plan:    plan,
		Events:  *events,
		Metrics: k.snapshot(opts.DurationMS),
	}, nil
}

// onArrival generates one request and keeps the Poisson arrival process
// going.
func (k *Kernel) onArrival(ctx *engine.Context) []engine.Event {
	req := &Request{
		ID:      k.ids.Next(),
		Read:    k.splitter.IsRead(),
		StartMS: nowMillis(ctx),
	}
	k.generated++
	ctx.Logf("generated request %d (read=%v)", req.ID, req.Read)
	k.dispatch(ctx, k.clientID, req)

	gap := k.arrival.NextGapMillis()
	ctx.Schedule(delay(gap), engine.Event{Type: "workload.arrive", Handler: k.onArrival})
	return nil
}

// dispatch admits a request into a component: it either takes a service
// slot, joins the queue, or is rejected when both are full.
func (k *Kernel) dispatch(ctx *engine.Context, compID string, r *Request) {
	rt := k.rt(compID)
	rt.Arrived++
	r.Path = append(r.Path, compID)

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
	ctx.Logf("request %d begins %s at %s for %.3fms", r.ID, opFor(rt.spec, r), compID, serviceMS)

	// Capture per-request values for the completion event.
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

// finishService releases the slot, records metrics, and moves the request
// onward — with cache-hit routing handled here.
func (k *Kernel) finishService(ctx *engine.Context, compID string, r *Request, serviceMS, startMS float64) {
	rt := k.rt(compID)
	rt.inFlight--
	rt.Completed++
	rt.BusySumMS += serviceMS

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

	// Free the slot for whoever is waiting before the request hops on.
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
// response when no route remains. Returns true if the request terminated.
func (k *Kernel) hop(ctx *engine.Context, fromID string, r *Request) bool {
	targets := k.route(fromID, r)
	if len(targets) == 0 {
		k.completeRequest(ctx, r)
		return true
	}
	for i, t := range targets {
		if i == 0 {
			k.dispatch(ctx, t, r)
			continue
		}
		clone := *r // fan-out: independent legs, same logical request
		clone.Path = append([]string(nil), r.Path...)
		k.dispatch(ctx, t, &clone)
	}
	return false
}

// completeRequest records a successful end-to-end response.
func (k *Kernel) completeRequest(ctx *engine.Context, r *Request) {
	r.EndMS = nowMillis(ctx)
	lat := r.EndMS - r.StartMS
	k.latencies = append(k.latencies, lat)
	k.completed++
	ctx.Logf("request %d completed in %.3fms via %v", r.ID, lat, r.Path)
}

// rejectRequest terminates a request that could not be admitted. The
// request does not occupy further service time; its partial path remains
// recorded in the components it did reach.
func (k *Kernel) rejectRequest(ctx *engine.Context, r *Request) {
	r.EndMS = nowMillis(ctx)
	k.rejected++
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

// snapshot builds the immutable metrics report.
func (k *Kernel) snapshot(durationMS float64) Metrics {
	m := Metrics{
		DurationMS: durationMS,
		Generated:  k.generated,
		Completed:  k.completed,
		Rejected:   k.rejected,
		InFlight:   k.generated - k.completed - k.rejected,
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
			QueueDepth:    len(rt.queue),
			MaxQueueDepth: rt.MaxQueueDepth,
			InFlight:      rt.inFlight,
			ArrivalRPS:    float64(rt.Arrived) / windowSec,
		}
		if rt.WaitCount > 0 {
			cm.AvgQueueWaitMS = rt.WaitSumMS / float64(rt.WaitCount)
		}
		if rt.spec.Concurrency > 0 {
			cm.Utilization = rt.BusySumMS / (float64(rt.spec.Concurrency) * durationMS)
		}
		if rt.spec.CapacityRPS > 0 && cm.ArrivalRPS > rt.spec.CapacityRPS {
			cm.Saturated = true
		}
		m.Components = append(m.Components, cm)
	}
	return m
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
