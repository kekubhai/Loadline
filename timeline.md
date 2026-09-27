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

Step 4 — Cost Engine and Architecture Comparison ✅ (see below)

---

## Step 4 — Provider Abstraction, Capacity Estimation, and Cost Engine
Status: COMPLETE

### What Was Built

A new `providers` package with a strict dependency direction: **providers
 depend on the generic simulation layer, never the reverse**. The
simulator operates exclusively on generic `ComponentSpec` behavior and
has zero knowledge of AWS, Cloudflare, or GCP.

**ServiceModel abstraction** (`provider.go`) — every catalog service
exposes the six required facets as data + behavior:

```text
Identity   Provider() / Service() / Summary()
Capacity   concurrency, service time, queue limit, modeled RPS ceiling, notes
Latency    per-op service times (read/write/get/put/enqueue/transit/compute)
Scaling    static | autoscaled | serverless, unit costs
Failure    MTTR + baseline error posture (input to future injection defaults)
Pricing    per-request, per-compute-unit, instance-hour, storage/DB/cache
           GB-month, queue-op, egress-GB, free-tier allowances
```

All 18 services are instances of one `GenericService` implementation —
uniform behavior, differing only in documented numbers.

**Config resolution** — typed `Config{Concurrency, QueueLimit, Units,
MemoryMB, StorageGB, HitRatio}` merges over catalog defaults and emits the
generic spec plus a complete assumption trail. `BuildSpec` maps
(provider, service) → generic component kind inside the providers package;
memory sizing scales modeled service time for per-compute-unit services
(documented linear assumption, not a benchmark).

### Providers and Services

| Provider | Services (18 total) |
|---|---|
| AWS (7) | EC2, Lambda, RDS, ElastiCache, SQS, S3, CloudFront |
| Cloudflare (5) | Workers, KV, R2, Queues, Durable Objects |
| GCP (6) | Cloud Run, Cloud SQL, Memorystore, Pub/Sub, Cloud Storage, Cloud CDN |

Nothing connects to real cloud accounts. Every number is a labeled
modeling assumption (mid-tier plan, primary commercial region).

### Capacity Model

`EstimateCapacity(runResult, specs)` combines three sources — measured
simulation behavior, service-model assumptions, and the Step 3 diagnosis
— into per-component reports:

```text
CurrentRPS         = measured mean arrival rate (simulation output)
MaxSustainableRPS  = concurrency ÷ effective service time
                     (catalog ceiling for serverless kinds)
Utilization        = measured busy fraction
Headroom           = 1 − utilization
Saturated          = arrival > ceiling (measured OR modeled)
Bottleneck         = diagnosis flagged the component
Assumptions        = full trail: class notes, config, derivation math
```

Example derivation printed by the demo:

```text
ceiling = concurrency 8 ÷ service 0.005s ≈ 1600 RPS; arrival is 0% of ceiling
```

### Pricing Model

`EstimateCost(runResult, specs, plan)` converts measured usage into a
monthly ESTIMATE, scaling the simulated window to 730h at observed rates
(the scale factor is printed). Categories: compute (instance-hours +
GB-seconds), requests, storage, database, cache, queue, network (egress
from actual payload sizes, 20% egress fraction), total. Free-tier
allowances are deducted per line with the deduction annotated in the line
description. Every line item carries quantity × unit-price with its
derivation string, and the estimate carries the global assumptions:

```text
NOTE: ESTIMATE from local pricing models — not live billing data.
```

### Example Architecture (provider-built)

The demo's new section builds Client → Lambda → ElastiCache → RDS from
the catalog and runs the full pipeline:

```text
== capacity ==
db       rds            6.0 RPS   max 1600   util 0.5%   headroom 99.5%
api      lambda        17.1 RPS   max 4000   util 0.4%   headroom 99.6%
cache    elasticache   17.1 RPS   max 2.0M   util 0.0%   headroom 100%

== monthly cost ==
api      lambda        $18.35   (GB-s compute + requests, free tiers applied)
cache    elasticache  $113.15   (1 instance-hour line)
db       rds          $187.79   (instance + 100GB storage + egress)
TOTAL                 $312.42   ESTIMATE — not live billing data
```

### How to Run

```bash
cd apps/simulator

go test ./... -count=1   # 79 tests (engine 19, workload 8, sim 40, providers 12)
go run .                 # cascade scenario + provider capacity/cost report
```

### Tests

79 total, all passing, all deterministic. Step 4 additions (12 new):

- catalog completeness: all 18 services present, every facet populated
  and documented
- kind mapping: all 16 mapped services land on the right generic kind
- unknown service rejected; config overrides applied; defaults preserved
- memory sizing: smaller Lambda allocation models slower execution
- capacity ceiling math: RDS 8 ÷ 5ms = 1600 RPS exact
- capacity report: measured RPS present, headroom+utilization = 1,
  assumptions explicit
- pricing math: hand-computed 100GB × $0.115 database line; category sum
  equals total; repeat calls identical (determinism)
- free-tier deduction: sub-1M SQS ops price to $0
- egress priced from workload payload bytes
- end-to-end: catalog → generic architecture → simulation → capacity +
  cost from real outputs

`go vet` and `gofmt` clean.

### Current Limitations

- Pricing is a coarse local model: single region class, no tiers/reserved
  instances/savings plans, no NAT/load-balancer/CDN forwarding fees.
- Capacity ceilings are linear (concurrency ÷ service time); no queuing-
  theory correction at high utilization.
- Free-tier deduction is linear on the largest matching bucket, not
  per-account marginal accounting.
- FailureModel exposes MTTR/error posture but Step 3 injections do not
  yet default from provider profiles.
- Autoscaling is described (min/max/unit cost) but the simulator does not
  model scale-out events mid-run; EC2-style fleets are static per run.
- Durable Objects duration pricing modeled as GB-s compute only.
- No architecture comparison yet (next step), no frontend.

### Next Step

Step 5 — ConnectRPC API + Frontend Wiring ✅ (see below)

---

## Step 5 — Backend API (Protobuf + ConnectRPC) and Frontend Wiring
Status: COMPLETE

### What Was Built

The completed Go simulation backend is now exposed to the Next.js frontend
over **Protobuf + ConnectRPC**. The engine stays fully independent: the
transport layer only adapts messages and orchestrates run lifecycle —
every metric, diagnosis, capacity number, and cost line still comes from
the simulation kernel or the provider models.

Dependency direction (enforced by imports):

```text
Next.js (browser)
   |  @loadline/api (generated TS stubs + client factory)
   |  Connect protocol (binary proto over HTTP; gRPC / gRPC-Web also served)
   v
apps/simulator/cmd/loadline-server   (thin: routes, CORS, h2c)
   v
apps/simulator/loadlinev1            (API layer: store + service + adapters)
   v
sim / workload / providers / engine  (UNCHANGED simulation core)
```

**Protobuf contract** (`proto/loadline/v1/simulation.proto`, package
`loadline.v1`, versioned `Architecture.schema_version` from day one).
One `SimulationService` with all nine RPCs:

| RPC | Kind | Purpose |
|---|---|---|
| `CreateSimulation` | unary | validate + store architecture/workload/options, assign ID |
| `RunSimulation` | unary | start async execution on the engine |
| `GetSimulationStatus` | unary | poll status + latest progress snapshot |
| `StreamMetrics` | server-stream | live progress frames, then status + final results |
| `GetResults` | unary | plan, system + per-component metrics, failure records, engine summary |
| `GetDiagnosis` | unary | bottleneck report, optionally vs a baseline run |
| `GetCapacity` | unary | per-component capacity estimates (provider-backed runs) |
| `GetCostEstimate` | unary | monthly ESTIMATE with full assumption trail |
| `ListCatalog` | unary | the 18-service provider catalog |

Code generation is `buf`-driven (`buf.yaml`, `buf.gen.yaml`):
`protoc-gen-go` + `protoc-gen-go-grpc` + `protoc-gen-connect-go` produce Go
stubs in `apps/simulator/loadline/v1` (+ `.../loadlinev1connect`), and
`protoc-gen-es` produces TypeScript in `packages/api/src`. `buf lint`
passes under the STANDARD ruleset.

**API layer** (`apps/simulator/loadlinev1/`):

- `store.go` — in-memory run store. A `Run` holds inputs, lifecycle
  (`PENDING → RUNNING → COMPLETED | FAILED`), the latest progress
  snapshot, results, and resolved provider specs. Subscribers register a
  wake channel against a monotonic `version` counter; notifications drain
  the subscriber set (no double-close) and the version check closes the
  missed-wakeup window between reading state and subscribing.
- `convert.go` — the only place proto messages meet domain structs:
  provider references (`provider`/`service`/`config`) resolve through
  `providers.ResolveService` so the simulator sees only generic specs;
  user-specified generic fields win over catalog defaults.
- `service.go` — the Connect handler. `RunSimulation` executes on a
  background goroutine with a panic guard; results publish into the
  store, which wakes stream subscribers. `StreamMetrics` replays the
  latest snapshot before terminal frames (a run can finish faster than a
  subscriber attaches — simulations run far faster than wall time).

**Engine-side progress hook** (`sim.Options.Progress`) — the one addition
to the simulation core. The kernel's existing sampler event emits a
`Snapshot` (sim time, system counters, per-component queue/in-flight/
arrived/completed/utilization) through an observational callback. It
cannot influence scheduling, is ignored when nil, and a dedicated test
(`TestProgressHookDeterminism`) proves hook-on vs hook-off runs are
identical. This is how the API streams progress WITHOUT the API layer
owning simulation state.

**Server** (`apps/simulator/cmd/loadline-server`) — a deliberately thin
binary: registers the generated handler, wraps rs/cors (origins via
`LOADLINE_ALLOWED_ORIGINS`, default `http://localhost:3000`), and h2c so
gRPC works without TLS. Serves Connect + gRPC + gRPC-Web on one port.

**Shared frontend package** (`packages/api`) — workspace package exporting
the generated protobuf-es types and a `createLoadlineClient({ baseUrl })`
factory (binary-proto Connect transport over `fetch`).

**Frontend** (`apps/web/app/page.tsx`) — the first real console slice,
all state, no computation: pick the AWS catalog scenario (Client → Lambda
→ ElastiCache → RDS), toggle a cache-crash injection (t=1s → 6s,
pass-through), then create → run → stream live progress → results →
diagnosis → capacity → cost. The last healthy run is kept as the
diagnosis baseline for the crash run. Panels render only server-computed
numbers: latency percentiles, per-component tables, bottlenecks with
their reasons, capacity ceilings + headroom, and the monthly cost
estimate with its ESTIMATE marker.

### Example: end-to-end over HTTP (real server, curl)

`go run ./cmd/loadline-server -addr :8090`, then against
`/loadline.v1.SimulationService/...` with 1M-DAU / 40-req / 5× peak
(≈2315 RPS peak), 3s window:

```text
CreateSimulation → {"simulation":{"id":"sim-1", ...}}
RunSimulation    → RUNNING
GetResults       → generated 6839, completed 6761, avg 28.0ms, p95 33.5ms, p99 35.9ms
GetDiagnosis     → healthy (no bottleneck at this load)
GetCapacity      → db rds: current 808 RPS, max 1600, util 67%, headroom 33%
                   (assumption trail: "ceiling = concurrency 8 ÷ service 0.005s ≈ 1600 RPS")
GetCostEstimate  → total $2859.46/month (compute, requests, database, network)
StreamMetrics    → progress frames ... status COMPLETED ... final results
```

### How to Run

```bash
# generate stubs after editing proto (requires buf + plugins on PATH)
buf generate

terminal 1:
cd apps/simulator && go run ./cmd/loadline-server -addr :8080

terminal 2:
cd apps/web && pnpm dev    # open http://localhost:3000

tests:
cd apps/simulator && go test ./... -count=1      # 86 tests
cd packages/api && pnpm typecheck                # generated types
cd apps/web && pnpm typecheck                    # console page
```

### Tests

86 total (79 from Steps 1–4 unchanged + 7 new), all passing, all
deterministic. New `loadlinev1` suite runs the REAL service over an
in-process HTTP server (generated client, real serialization/routing):

- `TestFullLifecycle` — create → run → results → diagnosis → capacity →
  cost; checks request conservation (Generated = Completed + Rejected +
  Failed + InFlight), 4 component reports, utilization + headroom = 1,
  non-empty assumptions, and the not-live-billing marker on cost.
- `TestStreamMetrics` — stream attaches BEFORE the run starts; asserts ≥1
  progress frame from real sampling windows, terminal status, and a
  results frame with completed traffic.
- `TestDeterminismAcrossAPI` — two identical seeded runs through the full
  API produce byte-identical generated/completed/p95/p99/avg — the API
  layer cannot perturb engine determinism.
- `TestValidationErrors` — unknown provider service, unknown failure
  target, invalid workload, not-found IDs, and cyclic architectures fail
  the run with structured errors.
- `TestFailureInjectionEndToEnd` — heavy workload (~2315 RPS), cache
  crash 1s→6s with pass-through vs same-seed baseline: db arrivals grow
  and the diagnosis reports the db as critical with machine-computed
  reasons (the Step 3 cascade, now through the wire).
- `TestProgressHookDeterminism` — the progress hook does not perturb
  simulation results.
- `TestCatalog` — 18 services, deterministic order, kinds populated.

`go vet` clean, `gofmt` clean, `buf lint` clean, both TS packages
`--noEmit` clean. End-to-end smoke-tested over real TCP with curl
(unary RPCs, JSON codec) and a throwaway Connect client (streaming).

### Current Limitations

- The store is in-memory and single-process: run IDs die with the server.
  Sharing/persisting architectures is future work.
- Progress snapshots arrive at the sampling cadence (1s simulated); there
  is no continuous event firehose and no resume of past frames beyond the
  latest-snapshot replay.
- Simulation execution is synchronous per run (single goroutine, no
  cancellation): a run cannot be aborted mid-flight yet.
- Capacity/cost require provider references on components; generic-only
  architectures get metrics + diagnosis but no estimates (by design).
- CORS defaults to localhost:3000; production origins come from env.

### Next Step

Step 6 — The Architecture Editor ✅ (see below)

---

## Step 6 — The Architecture Editor (Canvas, Palette, Inspector)
Status: COMPLETE

### What Was Built

The architecture view is now a working editor. The frontend owns ONE
canonical editor state — `{architecture, workload, options, failures}` in a
single reducer in `apps/web/app/page.tsx` — and every panel renders from it
and edits through dispatched actions. No component keeps its own copy of
the architecture; nothing simulated is computed in React.

**Canvas** (`components/canvas.tsx`) — still dependency-free (no XYFlow;
the project never had it), still deterministic layered layout, now with
direct manipulation:

| Interaction | How |
|---|---|
| Add node | drag a service row from the palette onto the canvas (HTML5 DnD, `loadline/service` payload); drop position becomes the node's persistent position |
| Add client | "+ client" button in the palette (the simulator requires exactly one client) |
| Select node / edge | click; edges get a wide invisible hit path and selection opens the link editor |
| Move node | pointer drag with pointer capture; committed on release into editor state |
| Connect | drag the square port on a node's right edge; rubber-band follows the cursor and snaps to the nearest node center; drop creates a `Link` (dupes and self-loops rejected in the reducer) |
| Delete node | inspector delete button (client is protected), removes attached links + failures atomically |
| Delete link | link editor's delete button (selection, not hover-click, so deletion is explicit) |
| Zoom | wheel zoom around the cursor, ± buttons, clamped 0.3×–2.5× |
| Pan | pointer drag on empty canvas |
| Fit view | `fit` button (also in palette) scales/centers the graph |
| Reset view | `reset` button clears all manual positions (re-layering deterministically) and refits |

**Node design** — technical equipment, not SaaS cards: thin border, small
typography, a two-letter role tag (CL/LB/AP/CA/QU/WK/DB/OS/NW), the
provider/service line, a compact two-value metrics row (arrival RPS and
utilization — backend results passed through verbatim, `—` before any run),
a red left edge + corner dot when a failure injection targets the node, and
a highlighted border when it is a connect-drop target.

**Palette** (`components/palette.tsx`) — provider tabs (aws / cloudflare /
gcp from the live catalog) filter the service list; service rows are
draggable onto the canvas; the components section lists the current
architecture with inline remove buttons (client protected).

**Inspector** (`components/inspector.tsx`) — context-sensitive editing:

- *Nothing selected* → architecture editor: name, local validation
  warnings (missing/extra client, dangling links, failures targeting
  missing components — the run button disables while issues exist),
  workload fields (total users, DAU, req/user/day, peak multiplier,
  reads-per-write, payload bytes), run options (seed, duration, retries,
  backoff, timeout, per-component retry-on checkboxes), and the pending
  failure list.
- *Catalog service* → catalog model (capacity, scaling, pricing) + "add to
  canvas".
- *Component* → id rename (rewires links and failures atomically), generic
  model fields exactly matching the backend `ComponentSpec` (concurrency,
  queue limit, capacity RPS, service ms, hit ratio for caches, fan-out for
  LB/network), or — when provider-backed — provider config overrides
  (concurrency, queue limit, units, memory, storage, hit ratio; 0 = catalog
  default, with the catalog values shown beside for reference) plus
  detach-from-catalog. Below: the failure-injection form (type, start,
  duration, type-specific params like added-ms / error-rate / packet-loss /
  pass-through), measured results, capacity estimate, cost line items, and
  clickable upstream/downstream link lists.
- *Link* → condition editor (all / reads only / writes only) + delete.

Numeric inputs keep local text state while typing (committing valid values,
restoring canonical ones on blur) so intermediate states like "0." don't
fight the user. The run button now runs exactly what the editor holds:
architecture, workload, options, and any scheduled failures come from the
same canonical state.

### How to Run

```bash
terminal 1:
cd apps/simulator && go run ./cmd/loadline-server -addr :8080

terminal 2:
cd apps/web && pnpm dev    # open http://localhost:3000

cd apps/web && pnpm typecheck && pnpm build   # both clean
```

### Design Notes

- State: one `useReducer` for the editor (architecture, workload, options,
  failures, selection, canvas positions), a second for run lifecycle —
  mirroring the backend split between inputs and outputs. Canvas positions
  live in editor state (canonical, per component id) but are NOT serialized
  into the Architecture protobuf; the wire schema stays clean.
- The backend was untouched: no proto change, no Go change. Editing maps
  1:1 onto fields the simulator already consumes; the "only expose fields
  supported by the backend model" rule is enforced by construction (the
  generic model editor renders `ComponentSpec` fields, the provider editor
  renders `ProviderConfig` fields).
- Determinism in the editor: same architecture + same positions always
  draw identically; node drag is viewport-committed (no per-move dispatch
  storm), connect targets snap deterministically (nearest center within
  radius).
- No simulation logic in React — the only numeric transforms in the UI are
  display formatting (`format.ts`) and drag-coordinate math.

### Current Limitations

- No persistence yet: refreshing the page resets the editor (sharing /
  compare flows come with Step 7).
- Edges cannot be re-routed by dragging (delete + reconnect instead);
  conditions are edited in the link inspector, not on-canvas.
- Failure form starts/durations are per-injection constants; no visual
  timeline of overlapping failures yet.
- No undo/redo history; deletions are immediate.
- Keyboard shortcuts (delete key, ctrl+drag duplicate) not wired yet.
- Multi-select and box-select are out of scope for this step.

### Next Step

Step 7 — Architecture Comparison and Sharing
