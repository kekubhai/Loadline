# LOADLINE Development Timeline

## Step 1 — Simulation Engine
Status: COMPLETE

### Built

A self-contained Go discrete-event simulation engine at
`apps/simulator/engine/` (module
`github.com/kekubhai/Loadline/apps/simulator`). It imports only the Go
standard library — no Next.js, HTTP, database, Redis, WebSockets, or cloud
providers.

Core flow:

```text
Event
 ↓
Priority Queue
 ↓
Scheduler
 ↓
Component execution (event handler)
 ↓
New Events
 ↓
Repeat
```

Components:

- **Simulation clock** (`clock.go`, `time.go`) — `Time` and `Duration` are
  distinct int64 types in nanoseconds. The clock only advances when the
  runner jumps it to an event's timestamp; no wall-clock involvement
  anywhere in the engine.
- **Event model** (`event.go`) — immutable record with timestamp, type,
  unique ID, request ID, component ID, causal parent (`Cause`), and the
  handler to execute.
- **Priority queue** (`queue.go`) — binary min-heap ordering strictly by
  `(timestamp, ID)`: earlier events first, equal timestamps in schedule
  order. O(log n) push/pop.
- **Event scheduler** (`scheduler.go`) — assigns monotonically increasing
  event IDs (the determinism tie-breaker), derives a per-event RNG stream
  seed from the run seed, and feeds the queue.
- **Simulation runner** (`runner.go`) — the event loop: pop earliest →
  jump clock → execute handler → schedule returned new events → repeat.
  Supports a simulated-time `Horizon`, a `MaxEvents` runaway guard, and
  optional event-history recording. Events that trigger a stop are pushed
  back, never dropped.
- **Deterministic randomness** (`random.go`) — SplitMix64 `RNG` plus a
  private per-event stream derived from `(run seed, event ID)` via
  `Context.Rand()`. Running without a seed makes `Rand()` panic loudly
  instead of silently breaking reproducibility.
- **Simulation results** (`results.go`) — processed/scheduled/pending
  counts, per-event-type counters, first/last/final simulated time, stop
  reason (`queue_empty`, `horizon`, `max_events`), and optional ordered
  per-event history for auditing causal chains.
- **Handler context** (`context.go`) — `Schedule` (relative delay),
  `ScheduleAt` (absolute time), `Now`, `Event`, `Rand`, `Logf`.
  Scheduling into the past panics; `Schedule`/`ScheduleAt` stamp the
  causal parent automatically.

Executable demo (`apps/simulator/main.go`): runs a 5-request
arrival→service→completion chain twice with seed 42 inside one process,
prints both traces, and verifies they are identical — proving
deterministic, timestamp-ordered execution.

### Tests

19 tests in `apps/simulator/engine/` (all fixed-seed, no wall-clock
assertions except the decoupling guard):

- `runner_test.go` — queue `(timestamp, ID)` ordering with ties, peek
  non-destructiveness, timestamp-order execution with clock-jump checks,
  handler-scheduled child events, causality stamping via
  `Schedule`/`ScheduleAt`, negative-delay panic, horizon stop, max-events
  stop, history recording.
- `random_test.go` — identical seeds → identical streams, different seeds
  → different streams, `Float64` range, `Intn` coverage/unbiasedness,
  `ExpFloat64` mean, per-event stream reproducibility and independence,
  unseeded `Rand()` panic.
- `integration_test.go` — multi-hop causal chain
  (arrive→service→retry/response) reproducible across two runs, results
  counter consistency, and a 200k-event / 200-simulated-second run
  completing in milliseconds (sim time is not wall time).

```
go test ./...   →  ok  github.com/kekubhai/Loadline/apps/simulator/engine  (19/19 PASS)
go vet ./...    →  clean
gofmt           →  clean
```

### How to Run

```bash
cd apps/simulator

# Run all engine tests
go test ./... -count=1

# Run the deterministic-execution demo (two seeded runs, compared)
go run .
```

The demo prints both traces and ends with
`determinism: OK — identical seed produced identical traces`.

### Current Capabilities

- Discrete-event simulation with strict `(timestamp, schedule-order)`
  execution.
- Fully deterministic runs given the same events and seed — reproducible
  across processes and runs.
- Simulated time fully decoupled from wall-clock time (integer ns, jump
  advancement, no sleeps).
- Causal-chain tracking: every scheduled event records the ID of the event
  that caused it.
- Per-event independent deterministic RNG streams.
- Run bounds: simulated-time horizon and processed-event ceiling; results
  report why a run stopped.
- Per-type event counters and optional ordered event history.

### Limitations

- No component models yet (no CDN, load balancer, cache, queue, database
  behaviors) — events are scheduled directly by handlers, so there is no
  notion of capacity, utilization, or queueing depth.
- No workload model: arrivals in the demo are hard-coded, not derived from
  DAU → RPS assumptions.
- No metrics engine: results are event counts and time bounds, not
  throughput/latency percentiles/error rates.
- No cost engine, no architecture schema, no provider catalog.
- Single-threaded by design; no real-time or variable-speed execution
  mode.
- No persistence or API/WebSocket layer; the engine is Go-library-only.

### Next Step

Step 2 — Components and Workload Modeling ✅ (see below)

---

## Step 2 — Components and Workload Modeling
Status: COMPLETE

### What Was Built

A generic component/workload simulation layer on top of the Step 1 engine
(two new packages, still Go-stdlib-only, still provider-free):

**`workload` package** — turns human-scale assumptions into a derived load
plan as a pure, inspectable function:

```text
Users → DAU → Requests/day → Average RPS → Peak RPS
```

Every intermediate value is kept on the `Plan` struct (DAU fraction,
read/write split, mean inter-arrival time). Invalid specs (DAU > users,
peak < 1x, etc.) are rejected with explanatory errors — no hidden jumps,
no invented numbers.

**`sim` package** — the component and request-flow layer:

- `ComponentSpec` — per-node assumptions: per-op service times,
  concurrency slots, queue limit, capacity RPS, cache hit ratio.
- `Kernel` — implements the request lifecycle on the engine: dispatch
  (admit / queue / reject), queueing with FIFO order, service with
  concurrency slots, completion hop, and routing by link conditions
  (read/write/all). Cache reads short-circuit on hits; misses and writes
  continue downstream.
- Rejection policy: when all concurrency slots are busy AND the queue is
  at its limit, the request is refused at the door (it never occupies
  service time).
- `Metrics` — see below.
- `Simulate(arch, workload, options)` — validates, derives, runs, and
  reports; identical inputs + seed give byte-identical metrics.

### Components Supported

All nine generic kinds, each with capacity (concurrency + queue limit),
service time per op, queueing, arrivals, completions, and rejections:

| Kind | Role in a request flow |
|---|---|
| `client` | generates traffic, receives responses |
| `load_balancer` | routes with configurable concurrency + queue |
| `api_server` | computes; forwards reads/writes downstream |
| `cache` | serves reads on hit (HitRatio), forwards misses + writes |
| `queue` | unbounded buffer; must link to exactly one worker |
| `worker` | async compute; drains a queue; terminal |
| `database` | priced reads/writes; typical bottleneck |
| `object_storage` | priced get/put |
| `network` | pure latency transit hop |

Architecture validation rules: exactly one client, no unknown link IDs,
no self-links, no dependency cycles, all nodes reachable from the client,
queues link to exactly one worker, workers link only from queues.

### Workload Model

`workload.Spec` → `workload.Plan`:

```text
TotalUsers, DAU, RequestsPerUserPerDay, PeakMultiplier,
ReadWriteRatio, PayloadBytes
   ↓ Derive()
RequestsPerDay = DAU × RequestsPerUserPerDay
AverageRPS     = RequestsPerDay ÷ 86400
PeakRPS        = AverageRPS × PeakMultiplier
ReadFraction   = ratio ÷ (ratio + 1)
MeanInterArrivalMillis = 1000 ÷ PeakRPS
```

Arrivals are a deterministic Poisson process: exponential gaps drawn from
the seeded per-event RNG streams, labeled read/write by the split ratio.

### Metrics

Per component (all definitions documented on the struct):
Arrived, Completed, Rejected, QueueDepth (instantaneous), MaxQueueDepth
(high-water), InFlight, Utilization (busy-ms ÷ concurrency × window),
AvgQueueWaitMS, ArrivalRPS, Saturated flag (ArrivalRPS > CapacityRPS).

System-wide: Generated, Completed, Rejected, InFlight (conservation:
Generated = Completed + Rejected + InFlight), Avg/P50/P95/P99/Max latency
over completed requests (nearest-rank percentiles), window duration.

### Example Architecture

The demo (`apps/simulator/main.go`) runs Client → API Server → Cache →
Database with the 1M-user / 100k-DAU / 20-req-per-user / 5x-peak workload,
one simulated minute, seed 42. Sample output:

```text
users 1000000 → DAU 100000 (10.0%) → 2000000 req/day → avg 23.1 RPS → peak 115.7 RPS (x5.0)
window 60s: generated 6837, completed 6836, rejected 0, in-flight 1
latency ms: avg 4.95  p50 3.10  p95 8.10  p99 8.10  max 9.77
api    arrived 6837  completed 6836  util 3.4%
cache  arrived 6836  completed 6836  util 0.6%
db     arrived 2535  completed 2535  util 5.3%   ← cache filters ~63% of traffic
```

### How to Run

```bash
cd apps/simulator

go test ./... -count=1   # all 40 tests (engine 19, workload 8, sim 13)
go run .                 # demo: plan + metrics + determinism check
```

### Tests

40 tests total, all passing, all deterministic (fixed seeds):

- engine (19) — unchanged from Step 1, still green.
- workload (8) — derivation chain values, 1-RPS anchor point, seven
  invalid-spec rejections, read/write split ratio over 100k draws,
  arrival-gap mean matches the plan, ID uniqueness.
- sim (13) — architecture validation (single client, unknown links,
  cycles, queue/worker pairing, worker terminals), request conservation,
  rejection under overload, zero rejections under light load, DB traffic
  drops with an 80%-hit cache, percentile ordering, queue→worker handoff
  equality, saturation flag, same-seed determinism.

`go vet` and `gofmt` clean.

### Current Limitations

- Latency percentiles are computed over completed requests only; no
  per-component latency attribution yet (which hop added what).
- Utilization is a window average, not a time series; no per-percentile
  queue-depth distribution.
- Cache hits are probabilistic per request; no key-space, TTL, or
  eviction modeling.
- No retries or timeouts yet, so failure cascades (Step 3+) cannot be
  expressed.
- Queue concurrency is a single FIFO line; no priority classes.
- Links are topological only — no bandwidth/egress pricing or payload
  transfer time on links yet.
- No fan-out replica copies for load balancers (FanOut field exists but
  routes to a single target), no multi-AZ or availability modeling.
- No cost engine, provider catalog, frontend, failure injection, or
  comparison UI — deliberately out of scope for this step.

### Next Step

Step 3 — Metrics, Bottleneck Diagnosis, and Failure Injection ✅ (see below)

---

## Step 3 — Failure Simulation, Advanced Metrics, and Bottleneck Diagnosis
Status: COMPLETE

### What Was Built

Three extensions to the `sim` package, still stdlib-only and
provider-free. All behavior emerges from component interactions on the
Step 1 engine — nothing about cascades is hardcoded.

**Failure injection** (`failure.go`) — four types, each defined by
`Target`, `StartMS`, `DurationMS`, and a `FailureConfig`:

| Type | Effect while active |
|---|---|
| `crash` | component cannot serve: requests fail at it, or forward untouched when `PassThrough` is set (cache → origin fallback) |
| `increased_latency` | fixed service-time penalty per request served |
| `increased_error_rate` | a fraction of serviced requests fail |
| `network_failure` | transit latency inflation plus packet loss (drops) |

Failures are scheduled as engine events (`failure.start` / `failure.stop`);
recovery is just the stop event clearing the failure state. Invalid specs
(unknown target, zero duration, out-of-range rates) are rejected before
the run starts.

**Timeouts and retries** (kernel) — the propagation enablers:

- `RetryPolicy{MaxRetries, BackoffBaseMS, TimeoutMS}` applied by the
  components listed in `Options.RetryOn`.
- The **caller** performs the retry (request carries `Prev`), matching
  real client behavior; backoff doubles per attempt with jittered
  exponential draws from a dedicated seeded RNG stream.
- `TimeoutMS` is an end-to-end budget checked at every hop exit; exceeded
  requests fail as timeouts and burn a retry if the caller can retry.
- Retries re-enter the failing component, so a slow dependency receives
  amplified traffic — retry amplification is observable in the metrics.

**Advanced metrics** (all from real simulation data, definitions on the
structs):

- System: Generated, Completed, Rejected, Failed, Timeouts, Dropped,
  InFlight, ErrorRate, TimeoutRate, Avg/P50/P95/P99/Max latency
  (nearest-rank over terminal outcomes — completions plus mid-path
  failures at their failure latency, so timeouts count as experienced
  latency rather than vanishing from the sample), and per-request
  `FailureRecord`s (kind, attempt, caller, detected-at, path, timestamp).
- Per component: Arrived, Completed, Rejected, Failed, QueueDepth,
  MaxQueueDepth, InFlight, Utilization, AvgQueueWaitMS, AvgServiceMS
  (measured, not spec), ThroughputRPS, ArrivalRPS, CapacityRPS,
  Saturated, and **QueueTrend** ("growing"/"draining"/"stable") computed
  from periodic occupancy sampling of the run's own windows.

**Bottleneck diagnosis** (`diagnose.go`) — `Diagnose(metrics)` and
`DiagnoseWithBaseline(metrics, baseline)` flag components using only
simulation-derived signals:

- critical: rejections, saturation (arrival RPS > capacity), growing
  queue with arrival rate above service rate, utilization ≥ 97%
- high: utilization ≥ 90%, queueing dominating service time, failures at
  the component
- moderate: utilization ≥ 75%

Every bottleneck carries machine-computed reasons with the actual numbers,
plus system impacts (load shedding, error/timeout rates, tail ratio,
baseline deltas such as "p95 latency increased 271% (8.7ms → 32.4ms)").
Output is deterministically ordered: severity, then arrival rate, then ID.

### Example Scenario (emergent cascade)

`go run .` — 1M users / 500k DAU / 40 req/user/day / 5× peak ≈ 1157 RPS
against Client → API → Cache(80% hit) → DB(4 slots, 5ms, capacity 500).
Run 1 is healthy. Run 2 crashes the cache (pass-through) from t=20s to
t=35s with the same seed:

```text
RUN 1 (healthy):  db arrivals 24,953  util 52%  p95 8.7ms   no rejections
diagnosis: no bottlenecks detected

RUN 2 (cache crash 20s→35s):
  db arrivals 36,181 (+45%: cache hits became DB traffic)
  db rejections 5,361 (queue limit 20 exceeded under the surge)
  p95 32.4ms (+271%), p99 32.9ms (10.6x p50)

diagnosis: Bottleneck: db (critical)
Why:
  - rejected 5361 of 36181 arrivals (14.8%) — admission capacity exhausted
  - incoming load 603.0 RPS exceeds modeled capacity 500.0 RPS
Impact:
  - load shedding active: 5361 requests rejected
  - heavy tail: p99 32.9ms is 10.6x p50
  - p95 latency increased 271% vs baseline (8.7ms → 32.4ms)
  - throughput decreased 8% vs baseline
```

Cache failure → more DB requests → DB saturation → queue growth →
latency rise → load shedding: every step is measured component behavior,
not scripted logic.

### How to Run

```bash
cd apps/simulator

go test ./... -count=1   # 67 tests (engine 19, workload 8, sim 40)
go run .                 # scenario: healthy baseline vs cache-crash cascade
```

### Tests

67 total, all passing, all deterministic (fixed seeds; controlled
comparisons reuse the same seed so baseline vs failed runs see identical
arrivals):

- Step 3 `sim` tests (14 new, `failure_test.go`):
  - failure spec validation (6 rejection cases)
  - crash with pass-through: DB absorbs the cache's traffic share
  - crash without pass-through: requests fail, traffic completes after recovery
  - latency failure: p50/p99 rise by ~the injected penalty (same-seed control)
  - error-rate injection: DB-attributed failure share ≈ configured rate
  - network failure: packet-loss drops observed as terminal failures
  - **cascading overload**: DB latency beyond the caller's timeout budget
    produces timeouts attributed to the true caller chain
    (`caller=cache, detected=db`), retries consume attempts, and the DB
    sees more arrivals than there are distinct failing requests
    (amplification)
  - recovery: failures confined to the injection window
  - percentile math: nearest-rank exact values on a known series
  - percentile ordering from real run data
  - bottleneck detection under overload (db, critical, with reasons)
  - diagnosis stays silent on a healthy system
  - baseline deltas reported (p95 increase / throughput decrease)
  - failure-run determinism (metrics + failure records)

`go vet` and `gofmt` clean.

### Current Limitations

- Latency percentiles include mid-path failures but there is no
  per-hop latency attribution yet (which component added what).
- Utilization/queue trends come from periodic sampling (1s windows), not
  continuous traces; percentile distributions of queue depth are not kept.
- Crash affects the whole component; no partial degradation, no
  degradation of hit ratio, no slow-drain models.
- Retries are per-request FIFO; no circuit breakers, no retry budgets,
  no hedging; retry amplification exists but is not yet throttled.
- Queue depth at the horizon can hide an unbounded-growth trend in short
  runs; trend detection needs ≥ 6 samples per component.
- No cost engine, no architecture comparison UI, no frontend, no
  provider-specific behavior — still deliberately out of scope.

### Next Step

Step 4 — Cost Engine and Architecture Comparison
