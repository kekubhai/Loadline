# LOADLINE — system design simulation platform

> **Can this architecture actually handle my requirements, what will it cost, and where will it break?**

LOADLINE is an open-source **system design simulator** for engineers who need to answer that question before shipping. Design an architecture on a canvas from real cloud services (Cloudflare, AWS, GCP), define a workload in user-level terms, and run a **deterministic discrete-event simulation** of the whole system under load and failure.

Every number the UI shows — throughput, p50/p95/p99 latency, queue depth, utilization, capacity headroom, bottleneck diagnosis, estimated monthly cost — is output of the simulated events. Nothing is fabricated to look realistic, and every estimate carries its assumptions.

LOADLINE is not an architecture diagram editor and not a cloud price calculator. The point is the loop:

**Requirements → Architecture → Simulation → Failure → Diagnosis → Modification → Comparison**

## Features

- **Architecture canvas** — add components, connect them, configure them; drag real cloud services from a provider catalog onto the canvas.
- **Provider catalog** — Cloudflare (Workers, KV, R2, D1, Queues, Durable Objects), AWS (EC2, Lambda, CloudFront, ElastiCache, SQS, RDS, S3), GCP (Cloud Run, Cloud CDN, Memorystore, Pub/Sub, Cloud SQL, Cloud Storage), mapped onto nine generic component kinds: client, load balancer, API server, cache, queue, worker, database, object storage, network.
- **Workload model** — an explicit, inspectable derivation chain: users → DAU → requests/user/day → requests/day → average RPS → peak multiplier → peak RPS. No hidden jumps from "1M users" to "10k RPS".
- **Discrete-event simulation engine (Go)** — requests arrive (Poisson), queue (FIFO), consume service time, complete, time out, retry and get rejected by real admission control. Simulated time is independent of wall-clock time; a 60-second horizon runs in milliseconds.
- **Deterministic by construction** — identical architecture + workload + failures + duration + seed produce byte-identical results (SplitMix64 per-event streams, event ordering by `(timestamp, ID)`, seed echoed on every run).
- **Metrics** — throughput, error/timeout rate, p50/p95/p99 (nearest-rank percentiles over per-request observations), queue depth and trend, utilization, arrival vs modeled capacity, headroom — each with a documented definition.
- **Failure injection** — component crash, increased latency, increased error rate, network failure. Cascades (cache crash → database saturation → queue growth → timeouts → retry amplification) emerge from the model; they are not scripted.
- **Bottleneck diagnosis** — reads only the run's own metrics and embeds measured numbers in every reason string: *"incoming load 603.0 RPS exceeds modeled capacity 500.0 RPS"*.
- **Capacity estimates** — modeled ceiling = concurrency ÷ effective service time (or the catalog's modeled RPS for serverless), with the derivation printed. Always labeled an estimate, never a production guarantee.
- **Cost estimation** — from the same run: completed requests, busy compute time and data movement, scaled to a 730-hour month with the factor, unit prices and free-tier allowances printed per line item. An estimate — never live billing.
- **Architecture comparison** — duplicate a design and compare both under the exact same workload, seed and failure scenario: cost, throughput, latency percentiles, error rate, capacity, headroom, bottlenecks.
- **Sharing without accounts** — the share link encodes the whole architecture in the URL; export/import as JSON; optional PostgreSQL-backed workspace for projects, versions and runs.
- **Arena challenges** — bounded system-design problems: read the brief, build, run, submit a benchmark.

## Repository structure

```text
apps/web          Next.js 15 + TypeScript frontend (canvas, panels, analysis)
apps/simulator    Go simulation engine + ConnectRPC service (cmd/loadline-server)
packages/api      @loadline/api — generated ConnectRPC client + shared types
proto             Connect/gRPC service definitions (buf)
providers         Provider/service catalog: capacity, latency, scaling, pricing models
scenarios         Ready-made architectures and workloads
docs              Simulation model specification (docs/simulation-model.md)
```

## Quick start

Requirements: **Node.js 22+**, **pnpm 9+**, **Go 1.23+**.

```sh
# 1. install workspace dependencies
pnpm install

# 2. start the simulation service (terminal 1) — listens on :8080
cd apps/simulator
go run ./cmd/loadline-server

# 3. start the web app (terminal 2) — http://localhost:3000
pnpm dev
```

A database is **not** required to simulate: the workspace API (projects, versions, runs) is only enabled when `DATABASE_URL` is set (see `.env.example`). Running a what-if simulation never depends on it.

### Commands

```sh
pnpm build        # build all apps
pnpm typecheck    # TypeScript, across the workspace
pnpm test         # frontend test suites (vitest)
go test ./...     # engine, API and persistence tests (apps/simulator)
```

## How it works

```text
Next.js (canvas, controls, metrics)
   │  ConnectRPC / HTTP
   ▼
Go service (cmd/loadline-server)
   │
   ├── workload model     users → RPS derivation, printed plan
   ├── simulation engine  event queue, queues, routing, failures, retries
   ├── metrics engine     throughput, percentiles, utilization, trends
   ├── diagnosis          bottleneck causes from measured numbers only
   └── cost engine        simulation outputs → pricing model → estimate
```

The engine is framework-independent: it runs and tests without a browser, and the frontend contains no authoritative simulation logic.

## Documentation

- **`/docs` (on the deployed site)** — *How LOADLINE models capacity, latency, failure & cost*: workload chain, queue and latency formulas, metric definitions, capacity and cost models, known limitations.
- **`/faq` (on the deployed site)** — what the simulator measures and what it deliberately does not promise.
- **[Simulation model specification](docs/simulation-model.md)** — the full engine reference: event loop, determinism mechanics, every formula, and the limitation list.
- **[AGENTS.md](AGENTS.md)** — engineering principles the project is built on (model behavior, determinism, provider abstraction, honest metrics).
- Machine-readable summaries for LLMs: `/llms.txt` and `/llms-full.txt` on the deployed site.

> The docs/faq links point at the production origin. Set `NEXT_PUBLIC_SITE_URL`
> in `apps/web/.env.local` (or your host's environment) to your deployed
> origin before release — it drives canonical URLs, the sitemap, robots.txt
> and structured data.

## License

[MIT](LICENSE) — open source, no telemetry, no account system.

---

**system design simulator** · capacity planning · bottleneck detection · p99 latency model · cloud cost estimation · discrete-event simulation · failure injection · architecture comparison
