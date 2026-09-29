# LOADLINE — Simulation Model

This document specifies what the simulator actually computes: the event
loop, the workload formulas, every component behavior, the queue model,
the latency model, the failure model, the metrics and their exact
definitions, bottleneck diagnosis, the capacity model, cost assumptions,
and the known limitations.

Every number LOADLINE displays comes from the mechanisms described here
or is labeled as an assumption. Nothing is invented to look realistic.

**What LOADLINE is:** a deterministic, explainable, internally consistent
simulation with explicit assumptions.

**What LOADLINE is not:** a production-accuracy oracle. Modeled ceilings
and cost figures are estimates from labeled local models, not guarantees
or live billing. See Limitations.

---

## 1. Simulation model

LOADLINE is a discrete-event simulation (DES). Simulated time is an
integer nanosecond counter, fully decoupled from wall-clock time: the
simulation of a 60-second horizon takes milliseconds of real time (or
longer, if deliberately paced for observation — pacing never changes
results, only their wall-clock duration).

```
Event { timestamp, type, ID, requestID, componentID, cause, handler }
   ↓
priority queue, ordered strictly by (timestamp, ID)
   ↓
runner: pop earliest → jump clock to its timestamp → execute handler
   ↓
handler schedules new events → repeat
```

The run stops when:

- the queue empties (`queue_empty`),
- simulated time reaches the horizon (`horizon`), or
- the processed-event guard trips (`max_events`).

A user may also stop a run early (`stopped`); partial results cover only
the simulated span actually processed, so rates stay honest.

### Determinism

Given identical (architecture, workload, failures, duration, seed), runs
are byte-identical. The mechanisms:

- **Event ordering** is `(timestamp, ID)` with monotonically increasing
  IDs — no wall clock, no map iteration order in any scheduling path.
- **Randomness** is SplitMix64. Each event gets an independent RNG stream
  derived from `(runSeed, eventID)`. The failure/backoff rolls use a
  separate stream (`runSeed ^ 0xF41A11`), the read/write splitter
  (`runSeed ^ 0xA11CE`) and arrival clock (`runSeed ^ 0xB0B`) their own.
- **Unseeded runs default to seed 1** and echo the effective seed; RNG
  use without a seed panics.
- Component iteration uses architecture order (`order []string`), never
  Go map range.

Enforced by tests at the engine, sim, API, and pacing layers.

---

## 2. Event loop

1. `workload.arrive` — the first arrival is scheduled at the first
   Poisson gap; each arrival schedules the next.
2. `component.finish` — a request in service; releases its slot, applies
   the timeout/error checks, then hops onward.
3. `request.retry` — a caller retrying a failed hop after backoff.
4. `failure.start` / `failure.stop` — install/clear failure state on a
   component.
5. `metrics.sample` — periodic occupancy sampling (default every 1s of
   simulated time) for queue-trend diagnosis and progress snapshots.
   Purely observational: it cannot influence scheduling (proven by a
   hook-on/hook-off determinism test).

---

## 3. Workload formulas

The derivation chain is a pure function with every intermediate value
exposed in the `Plan` — nothing jumps from "1M users" to "10k RPS"
without showing its work.

```
RequestsPerDay  = DAU × RequestsPerUserPerDay
AverageRPS      = RequestsPerDay ÷ 86400
PeakRPS         = AverageRPS × PeakMultiplier
ReadFraction    = ReadWriteRatio ÷ (ReadWriteRatio + 1)
WriteFraction   = 1 − ReadFraction
MeanInterArrivalMillis = 1000 ÷ PeakRPS
```

- **Rounding:** nothing is rounded or truncated. Fractional
  requests/user/day produces fractional requests/day (e.g. 2.5 req/day
  stays 2.5). Integer fields (`TotalUsers`, `DAU`, `PayloadBytes`) are
  int64.
- **Zero values:** `TotalUsers ≤ 0`, `DAU ≤ 0`,
  `RequestsPerUserPerDay ≤ 0`, `ReadWriteRatio ≤ 0` are rejected with
  explicit errors. Zero traffic in a run comes from a *valid* spec whose
  peak inter-arrival exceeds the horizon — the run then reports honest
  zeros (0 generated, 0.0 rates, no NaN).
- **Extreme values:** large-but-valid specs are computed in float64 and
  must not overflow to ±Inf or NaN (tested at ~9.2e18 users).
- **Invalid values:** `DAU > TotalUsers`, `PeakMultiplier < 1`,
  `PayloadBytes < 0` are rejected before the run starts.
- **Read/write ratios:** any positive ratio; e.g. 4 → 80% read / 20%
  write. The split is applied per-request from a seeded RNG stream
  (statistically exact over large samples, tested).
- **Payload size** is an assumption carried on the plan; it prices
  network egress in the cost engine. It does not add service time
  (documented limitation).
- **Peak multiplier** scales average RPS to the peak the architecture
  must survive; `≥ 1` required. Arrivals run at peak intensity for the
  whole simulated window (a sustained-peak stress model, not a diurnal
  curve — see Limitations).

### Arrival process

Arrivals are a homogeneous Poisson process: exponential gaps with mean
`MeanInterArrivalMillis`, drawn from the seeded stream. At peak the mean
rate is exactly `PeakRPS`.

---

## 4. Component behavior

Nine generic kinds. Every component has meaningful behavior; none exists
for visual completeness. All models are generic — providers map onto
these kinds in the catalog layer; the simulator never sees "AWS".

| Kind | Behavior |
|---|---|
| `client` | Generates arrivals, receives responses. Unconstrained latency hop. |
| `load_balancer` | Routes with its own concurrency+queue; `FanOut` replicates each request to N downstream targets (each leg is an independent unit of work counted in Generated). |
| `api_server` | Computes (service time per op), forwards reads/writes on its links. |
| `cache` | Reads hit with probability `HitRatio` (seeded roll) and terminate at the cache; misses and writes continue downstream. |
| `queue` | Zero-delay pass-through hop that must link to exactly one worker. FIFO buffering happens at the constrained component downstream (the worker's queue). |
| `worker` | Async compute; drains its queue with its concurrency slots; terminal (cannot link onward). |
| `database` | Priced read/write service times; the classic bottleneck. |
| `object_storage` | Priced get/put service times. |
| `network` | Pure transit-latency hop (unconstrained). |

Architecture validation (before any run): exactly one client; no unknown
link IDs; no self-links; **no cycles**; every non-client node reachable
from the client; every queue linked from exactly one worker; workers link
only from queues. Invalid specs (empty IDs, negative concurrency /
queue / capacity / service times, hit ratio outside [0,1]) are rejected
explicitly — never silently accepted, never a mid-run panic.

---

## 5. Queue model

Queues are real system state, one FIFO line per constrained component:

- **Growth:** when arrival rate > service rate, depth grows. The trend
  classifier (`growing` / `draining` / `stable`) compares the mean of
  the last third of sampled windows against the first third; needs ≥ 6
  samples.
- **Drain:** when capacity exceeds load, queues empty and the trend
  reports `draining`/`stable`.
- **Rejection:** admission control is `concurrency slots full AND queue
  at QueueLimit` → the request is refused at the door. It never occupies
  service time and is excluded from latency percentiles (it never
  entered service). `QueueLimit = 0` means unbounded — such queues grow
  without bound and never reject.
- **Depth accounting:** instantaneous depth is `len(queue)`; the
  high-water mark `MaxQueueDepth` is updated on every enqueue.
- **Wait time:** measured per request as `serviceStart − queueEnter`
  (clamped ≥ 0), averaged into `AvgQueueWaitMS`. Requests enqueued at
  exactly t=0 are measured too (the "was queued" flag is explicit, not a
  sentinel on the timestamp).

Note the model fact above: `KindQueue` is a pass-through; the buffering
it represents lives in the linked worker's queue, whose `MaxQueueDepth`
is the honest backlog measurement.

---

## 6. Latency model

Total request latency composes additively from measured pieces:

```
request latency = Σ over visited components (
    queue wait          — time in that component's FIFO
  + service time        — spec service time for the op
                        + injected failure penalty while active
)
+ transit               — unconstrained hops (network/client)
```

- **Tail latency** emerges from queueing: under load, p99 grows because
  later requests wait behind earlier ones. There is no artificial tail
  multiplier anywhere.
- **Percentiles (p50/p95/p99) are nearest-rank over actual per-request
  observations** — rank `ceil(p × n)` on the sorted per-request latencies.
  No approximation formula is used (locked by an adversarial-sample-count
  test, e.g. p95 of 39 samples ranks at 38, not 37).
- **Observation set:** terminal outcomes — completed requests plus
  mid-path failures at their failure latency (timeouts/errors/drops are
  latency the caller actually experienced). Admission rejections are
  excluded (tracked as a rate instead).

---

## 7. Failure model

Four injection types, each modifying simulation state (never a display
flag):

| Type | Effect while active |
|---|---|
| `crash` | Component cannot serve: requests fail at it, or forward untouched when `PassThrough` is set (cache → origin fallback). |
| `increased_latency` | Fixed per-request service-time penalty. |
| `increased_error_rate` | A fraction of serviced requests fail. |
| `network_failure` | Transit latency inflation plus packet loss (drops). |

- **Scoping:** a failure affects only its target component (verified by
  a test asserting an unrelated component's measured service time is
  unchanged, same seed).
- **Start time and duration:** `failure.start` installs state at
  `StartMS`; `failure.stop` clears it at `StartMS + DurationMS`.
  Boundary tests assert zero failures before start or after the window
  (± 1ms event-granularity slack), and that a window ending at the
  horizon behaves.
- **Validation:** unknown target, negative start, non-positive duration,
  out-of-range rates, latency failure without a penalty — all rejected
  before the run.
- **Cascades are emergent.** Cache crash + pass-through → misses become
  DB traffic → DB arrival rate exceeds its capacity → queue grows →
  rejections at the queue limit → latency percentiles rise → callers
  with budgets time out → retries (with backoff) re-enter the failing
  component, amplifying load. No cascade logic is scripted; a test
  asserts each link of this chain from measured numbers.

---

## 8. Metrics

All values are simulation output. Definitions:

### System (per run, over the measured simulated window)

| Metric | Definition | Type |
|---|---|---|
| `Generated` | Requests the workload produced (incl. fan-out legs and retry re-entries) | cumulative |
| `Completed` | Requests that received a full response | cumulative |
| `Rejected` | Requests refused at admission (slots full + queue full) | cumulative |
| `Failed` | Requests terminated by timeout / injected error / crash / drop | cumulative |
| `Timeouts` | Failed with an exceeded end-to-end budget | cumulative |
| `Dropped` | Failed via network packet loss | cumulative |
| `InFlight` | Generated − Completed − Rejected − Failed (still pending at horizon) | instantaneous |
| `ErrorRate` | Failed ÷ Generated | ratio over Generated |
| `TimeoutRate` | Timeouts ÷ Generated | ratio over Generated |
| `Avg/P50/P95/P99/Max` latency | Nearest-rank percentiles over terminal outcomes (see §6) | per request |

Conservation invariant (asserted by tests, including fan-out and
horizon-edge-failure cases): `Generated = Completed + Rejected + Failed +
InFlight`.

### Per component

| Metric | Definition | Type |
|---|---|---|
| `Arrived` | Requests that entered the component | cumulative |
| `Completed` | Requests that finished service here | cumulative |
| `Rejected` | Refused at this component's admission | cumulative |
| `Failed` | Failed while at this component | cumulative |
| `QueueDepth` | Requests currently waiting | instantaneous |
| `MaxQueueDepth` | High-water mark of depth | cumulative max |
| `InFlight` | Requests currently in service | instantaneous |
| `Utilization` | Busy-ms ÷ (concurrency × window) | window average |
| `AvgQueueWaitMS` | Mean measured wait in this queue | per request average |
| `AvgServiceMS` | Measured mean service time (busy ÷ completions) — real data, not the spec value | per request average |
| `ThroughputRPS` | Completed ÷ window | window average |
| `ArrivalRPS` | Arrived ÷ window | window average |
| `CapacityRPS` | The configured modeled ceiling (echoed for comparison) | configuration |
| `Saturated` | ArrivalRPS > CapacityRPS (when CapacityRPS > 0) | boolean flag |
| `QueueTrend` | growing / draining / stable (unknown without ≥6 samples) | sampled trend |

---

## 9. Bottleneck diagnosis

Diagnosis reads only the run's own metrics — no hardcoding per kind, no
hedged prose. Signals and severities:

| Severity | Evidence |
|---|---|
| critical | rejections > 0 (admission exhausted); arrival RPS > modeled capacity; queue growing with arrival above service rate; utilization ≥ 97% |
| high | utilization ≥ 90%; queueing dominating service (wait > 2× service); failures at the component |
| moderate | utilization ≥ 75% |

Every reason string embeds the actual numbers, e.g.:

> `rejected 5361 of 36181 arrivals (14.8%) — admission capacity exhausted`

> `incoming load 603.0 RPS exceeds modeled capacity 500.0 RPS`

> `queue growing continuously (max depth 20, avg wait 18.3ms) — arrival rate exceeds service rate`

System impacts (load shedding, error/timeout rates, heavy-tail ratio)
are computed from the same metrics; with a baseline run, deltas are
reported ("p95 latency increased 271% (8.7ms → 32.4ms)"). Output order
is deterministic: severity desc, then arrival RPS desc, then ID.

Tests lock: evidence strings must contain numbers and may not hedge
("may …" fails the test); a healthy system diagnoses silent; severity
matches the offered load.

---

## 10. Capacity model

Per provider-backed component:

```
CurrentRPS        = measured mean arrival rate (simulation output)
MaxSustainableRPS = concurrency ÷ effective service time          (static kinds)
                  = catalog ModeledRPS                            (serverless kinds)
Utilization       = measured busy fraction
Headroom          = 1 − Utilization
Saturated         = arrival exceeded the modeled ceiling (measured OR modeled)
Bottleneck        = the diagnosis flagged this component
```

**How maximum sustainable RPS is calculated, exactly:** for a static
component, ceiling = configured concurrency ÷ effective per-request
service time (e.g. RDS 8 slots ÷ 5ms = 1600 RPS). The derivation is
printed in the report's assumption trail, e.g.
`ceiling = concurrency 8 ÷ service 0.005s ≈ 1600 RPS; arrival is 50% of ceiling`.
For serverless kinds the catalog's documented `ModeledRPS` is used.

**Honesty rules:** `MaxSustainableRPS` is a MODELED ceiling of the
current configuration — an estimate from the service model, not a
measured quantity and **not a guaranteed production limit**. It assumes
service time holds at every load level; real systems usually degrade
earlier (contention, GC, hot keys, connection pools). The UI presents it
as "max sustainable (modeled)" with the assumption trail attached.

---

## 11. Cost assumptions

Cost is an ESTIMATE from local pricing models — never live billing. The
pipeline:

```
simulation outputs (completed requests, GB-s busy, window)
   ↓ scale to 730h month at observed average rates (factor printed)
pricing model per service (unit prices + free-tier allowances)
   ↓
monthly estimate with full assumption trail
```

Every line item carries: component, category, description, quantity,
unit, unit price, monthly cost. Every estimate carries:
- the scale factor ("usage scaled from 30s simulated window to 730h month …"),
- the workload basis (DAU × req/user/day),
- the marker that prices are local mid-tier-region estimates,
- per-line free-tier deductions annotated in the description.

Documented defaults (each printed when used): 100GB object storage,
50GB database storage, 13GB cache memory, 20% egress fraction, 0.5GB
memory basis when unspecified.

Roll-up invariant (fixed in the Step 8 audit and locked by test):
component `Monthly` sums, category totals, and the grand total all agree
after free-tier deductions.

---

## 12. Known limitations

Modeling scope — what this simulator deliberately does not model:

- **Service-time invariance.** Service times are constant per op;
  real systems degrade under contention, cache eviction, GC, lock
  contention. Modeled ceilings are therefore optimistic near saturation.
- **Cache realism.** Hits are independent seeded rolls at a fixed ratio;
  no key space, TTL, eviction, or locality. Hit ratios do not degrade
  over a run.
- **Traffic shape.** Arrivals are a sustained Poisson stream at peak
  intensity; no diurnal curves, bursts beyond Poisson variance, or
  geographic distribution. Fan-out legs are independent (no partial-
  failure aggregation semantics).
- **Queue component is a pass-through.** `KindQueue` adds no delay;
  buffering is modeled at the linked worker's queue.
- **Autoscaling is described, not simulated.** Fleets are static per
  run; no scale-out events mid-run.
- **Percentile sample size.** Short runs with few terminal outcomes make
  percentiles statistically coarse; the tests require ≥ 100 outcomes
  before trusting them.
- **Queue-trend detection** needs ≥ 6 samples (default 1s windows), so
  runs shorter than ~6s report `unknown`.
- **Latency attribution is per-request, not per-hop.** There is no
  "which component added what" breakdown yet.
- **Retries** are per-request FIFO with exponential backoff; no circuit
  breakers, retry budgets, or hedging. Amplification is observable but
  unthrottled.
- **Payload size** does not add transfer time; it only prices egress.
- **No persistence.** Run stores are in-memory; IDs die with the server.

Cost-model limitations:

- Single region class, mid-tier plans; no reserved instances, savings
  plans, tiers, NAT, or load-balancer forwarding fees.
- Usage is scaled linearly from the simulated window to 730h — the
  estimate inherits the run's traffic shape (sustained peak), which
  overstates a diurnal month's request volume unless interpreted as
  peak-hour billing basis.
- Free-tier deduction is linear on the largest matching bucket, not
  per-account marginal accounting.

Capacity limitations:

- `MaxSustainableRPS` is the modeled ceiling of the *current*
  configuration (see §10) — not a guarantee, not a measured quantity.
- Utilization/headroom are window averages, not time series.

Determinism scope:

- Identical results require identical (architecture, workload,
  failures, duration, seed). Wall-clock pacing is excluded by tests.
