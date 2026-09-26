# Architecture Simulation Platform

## Overview

We are building a system-design and architecture simulation platform inspired by tools like Breakscale, but focused on a broader engineering question:

> **Can this architecture actually handle my requirements, what will it cost, and where will it break?**

The product lets users design architectures using real cloud/provider services, define a workload, simulate the system under load and failure, estimate capacity and cost, identify bottlenecks, and compare alternative architectures.

The product is not a static architecture diagramming tool and not a generic cloud cost calculator. The core value is the loop:

**Define requirements → design architecture → simulate → observe → diagnose → modify → compare.**

---

## V1 Scope

V1 must be focused and shippable.

### Architecture Builder

Users can create an architecture on a visual canvas.

Initial generic components:

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

Users can:

* Add components
* Connect components
* Configure components
* Move/delete components
* Save/load architectures
* Duplicate an architecture for comparison

### Provider Catalog

V1 starts with a small set of real providers rather than attempting to model every service.

Initial providers:

#### Cloudflare

* Workers
* KV
* R2
* D1
* Queues
* Durable Objects

#### AWS

* EC2
* Lambda
* CloudFront
* ElastiCache
* SQS
* RDS
* S3

#### GCP

* Cloud Run
* Cloud CDN
* Memorystore
* Pub/Sub
* Cloud SQL
* Cloud Storage

Provider services must expose structured metadata for:

* Service identity
* Service category
* Pricing model
* Approximate pricing inputs
* Throughput/capacity assumptions
* Concurrency limits
* Base latency assumptions
* Scaling behavior
* Relevant service limits
* Failure characteristics

These values are simulation inputs and estimates, not guarantees of real-world cloud performance.

### Workload Definition

Users define requirements such as:

* Total users
* DAU
* Requests per user per day
* Peak multiplier
* Read/write ratio
* Payload size
* Storage requirements
* Availability target
* Latency target
* Monthly budget

The system converts user-level requirements into an explicit workload:

**Users → DAU → requests/day → average RPS → peak RPS → component load**

The calculations must be visible and explainable.

### Simulation Engine

The simulation engine is written in Go.

It uses a deterministic discrete-event simulation model.

The engine must model:

* Request arrival
* Request routing
* Service time
* Queueing
* Capacity
* Concurrency
* Network latency
* Failures
* Timeouts
* Retries
* Component overload
* Backpressure where applicable
* Scaling where applicable

The simulator must produce actual metrics from simulated events rather than fabricating plausible-looking numbers.

Each simulation should be reproducible using a seed.

### Metrics

At minimum:

* Throughput
* Average latency
* p50 latency
* p95 latency
* p99 latency
* Error rate
* Dropped requests
* Queue depth
* Component utilization
* Component throughput
* Capacity/headroom

### Capacity

The system should answer:

> Given this workload, can the architecture handle it?

Results should include:

* Required peak RPS
* Architecture capacity
* Current utilization
* Headroom
* Estimated maximum supported workload/users

The product must show the assumptions behind capacity estimates.

Never present a magic "supports X users" number without explaining how it was derived.

### Cost

V1 supports basic estimated pricing models:

* Per request
* Compute time
* Instance/hour
* Storage/month
* Bandwidth/egress

Show:

* Estimated monthly cost
* Cost by component
* Pricing assumptions
* Workload assumptions

Always label this as an estimate. Cloud pricing varies by region, usage type, discounts, free tiers, and pricing changes.

### Failure Injection

V1 supports:

* Component crash
* Increased latency
* Increased error rate
* Network failure

A failure should propagate through the simulated architecture naturally.

Example:

**Redis failure → cache misses → database load increases → database saturates → queue grows → p99 latency increases → errors increase**

The system should expose the causal chain.

### Bottleneck Detection

After a simulation, automatically identify major bottlenecks.

Example:

```text
PostgreSQL

Incoming: 8,400 RPS
Capacity: 7,200 RPS
Utilization: 100%+
Queue: growing

Result:
p99 latency increased
Error rate increased
```

The product should explain *why* the component became the bottleneck.

### Architecture Comparison

Users can duplicate architectures and compare them.

Compare:

* Cost
* Throughput
* p50/p95/p99 latency
* Error rate
* Capacity
* Headroom
* Availability
* Bottlenecks

The goal is to make engineering tradeoffs explicit.

### Scenarios

V1 includes predefined scenarios so users do not start from a blank canvas.

Initial examples:

* 10K-user SaaS
* 100K-user social application
* 1M-user API
* E-commerce application
* Real-time application

Each scenario should demonstrate a meaningful system-design concept.

### Sharing

V1 should support architecture sharing without requiring accounts.

Architecture definitions should be serializable and shareable through a URL or equivalent portable representation.

---

## Product Principles

### 1. Simulation over animation

Animations are presentation.

The underlying state must come from a real simulation model.

### 2. Explain every important number

If the product says:

`Capacity = 11,200 RPS`

the user must be able to understand where that number came from.

### 3. Requirements come before architecture

The product should encourage users to define:

* workload
* latency target
* availability target
* budget

before judging an architecture.

### 4. Failure is a first-class feature

A healthy architecture is not enough.

Users should be able to intentionally break components and observe cascading effects.

### 5. Tradeoffs matter

There is rarely one universally correct architecture.

The product should show:

**cheaper vs faster vs more scalable vs more reliable**

rather than declaring a single architecture "best" without context.

### 6. Determinism

Given the same:

* architecture
* workload
* simulation configuration
* random seed

the simulator should produce reproducible results.

### 7. Provider abstraction

The simulation engine must not contain Cloudflare/AWS/GCP-specific business logic everywhere.

Provider services should be represented through a common component/service model.

### 8. Honest modeling

We are building an engineering model, not pretending to reproduce a cloud provider's internal infrastructure.

Approximate values must be identified as assumptions.

### 9. No premature complexity

V1 does not include:

* AI
* Authentication
* Payments
* Teams
* GitHub integration
* Terraform generation
* Kubernetes generation
* Real cloud deployment
* Live cloud billing
* Dozens of providers
* Every service from a provider

Those can come later.

---

## Technical Architecture

```text
                         NEXT.JS
                            |
              +-------------+-------------+
              |             |             |
           Canvas        Controls       Metrics
              |
              | WebSocket
              v
                           GO
                            |
                 +----------+----------+
                 |                     |
           Workload Model        Simulation Engine
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

### Frontend

Next.js + TypeScript.

Responsibilities:

* Architecture canvas
* Provider/service selection
* Node configuration
* Workload configuration
* Simulation controls
* Live simulation visualization
* Metrics dashboard
* Bottleneck visualization
* Cost breakdown
* Architecture comparison

The frontend should not contain the authoritative simulation logic.

### Backend / Engine

Go.

Responsibilities:

* Architecture validation
* Workload generation
* Discrete-event simulation
* Component behavior
* Queues
* Routing
* Failures
* Metrics
* Capacity calculations
* Cost calculations

The Go engine should be independent of Next.js and React.

It should be possible to test the simulator without a browser.

### Communication

Use WebSockets for live simulation events.

The frontend receives:

* Simulation state
* Request/event updates
* Component metrics
* Queue depth
* Utilization
* Errors
* Simulation completion

The protocol should be versioned and structured.

---

## Proposed Repository Structure

```text
/
├── apps/
│   └── web/                     # Next.js application
│
├── services/
│   └── simulator/               # Go simulation service
│       ├── engine/
│       ├── events/
│       ├── components/
│       ├── workload/
│       ├── metrics/
│       ├── cost/
│       └── scenarios/
│
├── packages/
│   ├── schema/                  # Shared architecture/event schemas
│   └── providers/               # Provider/service definitions
│
├── scenarios/
│   ├── saas/
│   ├── social/
│   ├── ecommerce/
│   ├── api/
│   └── realtime/
│
├── docs/
│
├── description.md
└── AGENTS.md
```

---

## Core Domain Model

The architecture should ultimately be representable as data.

Conceptually:

```text
Architecture
├── metadata
├── requirements
├── nodes[]
│   ├── id
│   ├── type
│   ├── provider
│   ├── service
│   └── configuration
└── edges[]
    ├── source
    ├── target
    └── configuration
```

A service definition should describe behavior, not just UI metadata.

```text
Service
├── identity
├── capacity model
├── latency model
├── scaling model
├── failure model
└── pricing model
```

---

## Simulation Model

Prefer a discrete-event engine.

Conceptually:

```text
Event Queue
     |
     v
Pop next event
     |
     v
Process event
     |
     +----> update component state
     |
     +----> schedule next event
     |
     +----> update metrics
     |
     v
Repeat
```

Events may represent:

* Request arrival
* Request accepted
* Service completion
* Network delivery
* Timeout
* Retry
* Failure
* Recovery
* Scale-up
* Scale-down

Do not tie simulation time directly to browser animation time.

The simulator should be able to run faster than real time.

---

## Problem-Solving Model

When implementing any feature, ask:

1. What real system-design problem does this represent?
2. What inputs affect the behavior?
3. What state must be tracked?
4. What events change that state?
5. What metrics prove the behavior?
6. What failure modes matter?
7. How can the result be explained to the user?
8. Can the behavior be tested deterministically?

Avoid adding components that are merely cosmetic variants of existing components.

Every component should have meaningful behavior.

---

## Definition of Done for V1

A user must be able to:

1. Select a provider.
2. Add real services.
3. Build an architecture.
4. Define a workload.
5. Run a deterministic simulation.
6. Increase traffic.
7. Observe degradation.
8. Inject a failure.
9. Identify the bottleneck.
10. See estimated monthly cost.
11. See estimated capacity.
12. Understand the assumptions.
13. Duplicate the architecture.
14. Compare two architectures.
15. Share the architecture.

If this loop works well, V1 is successful.

---

## Future Direction

After V1 is stable, potential additions include:

* AI architecture generation
* AI failure diagnosis
* AI architecture optimization
* GitHub/repository architecture extraction
* Terraform/Kubernetes generation
* More providers
* More accurate provider-specific models
* Historical simulation runs
* Team workspaces
* Private architectures
* API access
* Production telemetry import
* Architecture recommendations
* Cost optimization
* Automated load testing
* Cloud deployment integrations

These are explicitly outside V1.
