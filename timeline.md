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

Step 2 — Components and Workload Modeling
