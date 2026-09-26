# AGENTS.md

## Project Mission

Build an engineering-grade system-design simulation platform.

The product answers:

> **Can this architecture actually handle my requirements, what will it cost, and where will it break?**

Do not reduce the project to a diagram editor, cloud price calculator, or animation demo.

The core loop is:

**Requirements → Architecture → Simulation → Failure → Diagnosis → Modification → Comparison**

---

# Non-Negotiable Principles

## 1. Model behavior, don't fake it

Metrics must come from the simulation model.

Do not generate numbers solely to make the UI look realistic.

Bad:

```text
p99 = random(100, 500)
```

Good:

```text
requests
→ queue
→ service time
→ capacity
→ completion
→ measured latency
```

If a metric cannot be justified by the underlying model, don't expose it as a factual result.

---

## 2. Keep the simulator independent from the UI

The Go simulator is the source of truth.

Do not put core simulation logic in React components.

The simulator must be runnable and testable without Next.js.

Preferred boundary:

```text
Next.js
   |
WebSocket / API
   |
Go
   |
Simulation Engine
```

---

## 3. Use discrete-event simulation

Simulation time must be independent from wall-clock/browser rendering time.

Use an event queue.

Conceptually:

```text
Event {
    timestamp
    type
    requestID
    componentID
}
```

Process events in timestamp order.

The UI may animate the resulting events, but animation must never determine simulation behavior.

---

## 4. Determinism is mandatory

Every simulation should support a seed.

Given identical:

* Architecture
* Workload
* Configuration
* Seed

the result should be reproducible.

Tests should use fixed seeds whenever randomness is involved.

Never use uncontrolled randomness in simulation behavior.

---

## 5. Provider services are data + behavior

Do not scatter provider-specific conditions throughout the engine.

Avoid:

```go
if provider == "cloudflare" {
    ...
}
```

throughout unrelated simulation code.

Prefer a provider/service model:

```text
Provider
  └── Service
       ├── CapacityModel
       ├── LatencyModel
       ├── ScalingModel
       ├── FailureModel
       └── PricingModel
```

Cloudflare, AWS, GCP, and future providers should plug into the same abstractions.

---

# System Architecture

```text
                  NEXT.JS
                     |
       +-------------+-------------+
       |             |             |
    Canvas        Controls      Metrics
       |             |             |
       +-------------+-------------+
                     |
                 WebSocket
                     |
                     v
                    GO
                     |
          +----------+----------+
          |                     |
     Workload Model       Simulation Engine
                                |
             +------------------+------------------+
             |                  |                  |
          Network            Compute            Storage
             |                  |                  |
             +------------------+------------------+
                                |
                          Metrics Engine
                                |
                           Cost Engine
```

---

# Go Backend Rules

## Simulation engine

The engine should own:

* Event scheduling
* Requests
* Queues
* Component state
* Routing
* Service time
* Capacity
* Concurrency
* Failures
* Timeouts
* Retries
* Backpressure
* Scaling
* Metrics

The engine should not import:

* React
* Next.js
* Browser APIs
* DOM packages

Keep it framework-independent.

---

## Workload model

Never jump directly from:

```text
1M users
```

to:

```text
10,000 RPS
```

without modeling the assumptions.

Use an explicit chain:

```text
Users
 ↓
DAU
 ↓
Requests/user/day
 ↓
Requests/day
 ↓
Average RPS
 ↓
Peak multiplier
 ↓
Peak RPS
```

Make these calculations inspectable.

---

## Capacity model

Capacity should be based on component behavior.

Examples:

```text
capacity = requests/sec
capacity = concurrent requests
capacity = bytes/sec
capacity = operations/sec
```

Do not use a universal capacity formula for every service.

Different service classes need different models.

---

## Latency model

Latency should be composable.

Conceptually:

```text
total latency =
network
+ queueing
+ service time
+ downstream latency
```

Tail latency should be affected by queueing and downstream behavior.

Do not calculate p99 as a simple fixed multiplier of average latency.

---

## Queueing

Queues are real system state.

Track:

* Current depth
* Maximum depth
* Arrival rate
* Service rate
* Wait time
* Dropped/expired items

A queue should naturally grow when arrival rate exceeds service rate.

---

## Failure propagation

Failures should propagate through dependencies.

Example:

```text
Redis failure
   ↓
cache misses
   ↓
database traffic increases
   ↓
database saturation
   ↓
queue grows
   ↓
latency increases
   ↓
timeouts
   ↓
retries
   ↓
additional load
```

The engine should be capable of representing this causal chain.

---

# Metrics

Minimum metrics:

* Throughput
* Average latency
* p50
* p95
* p99
* Error rate
* Dropped requests
* Queue depth
* Utilization
* Component throughput
* Capacity
* Headroom

Metrics must have a clear definition.

Document whether each metric is:

* per request
* per component
* system-wide
* average
* percentile
* instantaneous
* cumulative

---

# Cost Engine

Cost is an estimate, not a billing oracle.

Pricing models may include:

* Per request
* Compute time
* Instance-hour
* Storage/month
* Bandwidth/egress

The cost engine should consume simulation outputs.

Conceptually:

```text
simulation
   ↓
requests
compute time
storage
bandwidth
   ↓
pricing model
   ↓
estimated monthly cost
```

Always retain the assumptions used to calculate cost.

Do not hard-code prices in simulation logic.

---

# Frontend Rules

Next.js/TypeScript owns:

* Canvas
* Node rendering
* Connections
* Configuration UI
* Provider catalog UI
* Workload controls
* Simulation controls
* Metrics visualization
* Cost visualization
* Comparison UI

Frontend state should represent the architecture and current simulation state.

It should not independently calculate authoritative capacity or performance.

---

# Architecture Schema

The architecture should be serializable.

Conceptually:

```text
Architecture
├── metadata
├── requirements
├── nodes[]
└── edges[]
```

Nodes:

```text
{
    id,
    type,
    provider,
    service,
    configuration
}
```

Edges:

```text
{
    source,
    target,
    configuration
}
```

Keep the schema versioned from the beginning.

Future schema migrations are easier if architecture documents contain a version.

---

# Provider Catalog

V1 providers:

### Cloudflare

* Workers
* KV
* R2
* D1
* Queues
* Durable Objects

### AWS

* EC2
* Lambda
* CloudFront
* ElastiCache
* SQS
* RDS
* S3

### GCP

* Cloud Run
* Cloud CDN
* Memorystore
* Pub/Sub
* Cloud SQL
* Cloud Storage

Do not expand provider coverage until the existing models are useful.

Accuracy and explainability are more important than catalog size.

---

# V1 Generic Components

Support:

* Client / Users
* CDN
* Load Balancer
* API Server
* Cache
* Queue
* Worker
* Database
* Object Storage
* Network

Every component must have meaningful simulation behavior.

Do not create components solely for visual completeness.

---

# Failure Injection

V1:

* Component crash
* Increased latency
* Increased error rate
* Network failure

Failure injection must modify simulation state.

It must not simply display a red icon.

---

# Bottleneck Detection

After simulation, identify components where:

* Utilization is near capacity
* Arrival rate exceeds service rate
* Queue depth is growing
* Latency dominates downstream latency
* Errors originate
* A failure creates cascading overload

The diagnosis should explain the cause.

Bad:

```text
Database is slow.
```

Good:

```text
Database received 8,400 RPS.
Modeled capacity is 7,200 RPS.

Arrival rate exceeds service rate by 16.7%.

Queue depth grows continuously, causing p99 latency
to increase from 180ms to 1.2s.
```

---

# Architecture Comparison

Comparison must use the same workload whenever possible.

Compare:

* Cost
* Throughput
* p50
* p95
* p99
* Error rate
* Capacity
* Headroom
* Availability
* Bottlenecks

Never compare architectures using different workload assumptions without explicitly stating the difference.

---

# Testing Strategy

## Go

Unit tests for:

* Event ordering
* Queue behavior
* Capacity
* Latency
* Retry behavior
* Failure propagation
* Workload calculations
* Cost calculations
* Deterministic seeds

Integration tests for:

* Multi-component request flows
* Cascading failures
* Architecture execution

Golden/deterministic tests are encouraged for important scenarios.

---

## Frontend

Test:

* Architecture serialization
* Node/edge manipulation
* Configuration validation
* Rendering of simulation state
* Metrics display
* Comparison calculations

Do not duplicate simulation behavior in frontend tests.

---

# Engineering Workflow

Before implementing a feature:

1. Define the real system-design behavior.
2. Define inputs.
3. Define state.
4. Define events.
5. Define outputs/metrics.
6. Define failure modes.
7. Define deterministic tests.
8. Implement the engine.
9. Expose it through the API/WebSocket layer.
10. Build the UI around the engine output.

Engine first. UI second.

---

# What Not To Do

Do not add:

* AI
* Authentication
* Payments
* Teams
* GitHub integration
* Terraform generation
* Kubernetes generation
* Real cloud deployment
* Live cloud billing
* Huge provider catalogs

until the core simulation loop is reliable.

Do not optimize for feature count.

Do not add a service unless its behavior can be meaningfully modeled.

Do not claim real-world capacity from arbitrary assumptions.

Do not hide assumptions.

Do not fake metrics.

Do not couple simulation time to browser animation.

Do not put provider-specific pricing inside generic simulation components.

---

# Definition of Done

V1 is complete when a user can:

```text
Select provider
    ↓
Select services
    ↓
Build architecture
    ↓
Define workload
    ↓
Run simulation
    ↓
Increase traffic
    ↓
Observe degradation
    ↓
Inject failure
    ↓
See causal bottleneck
    ↓
See estimated capacity
    ↓
See estimated cost
    ↓
Duplicate architecture
    ↓
Compare architectures
    ↓
Share architecture
```

If that workflow is not reliable, V1 is not done.

---

# Long-Term Product Direction

After V1:

```text
Architecture
     ↓
Simulation
     ↓
Diagnosis
     ↓
AI recommendations
     ↓
Architecture mutation
     ↓
Re-simulation
     ↓
Best tradeoff
```

Potential future features:

* AI architecture generation
* AI diagnosis
* AI optimization
* GitHub architecture extraction
* Terraform/Kubernetes generation
* Production telemetry import
* More cloud providers
* Better provider-specific models
* Team collaboration
* API access
* Historical simulations
* Cost optimization
* Automated architecture benchmarking

But these are future layers.

The foundation must remain:

**accurate modeling + deterministic simulation + transparent assumptions + explainable results.**
