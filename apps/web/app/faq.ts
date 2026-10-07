/**
 * FAQ content — one array drives three consumers: the visible /faq page,
 * its FAQPage JSON-LD, and llms-full.txt. Answers are plain text (they are
 * injected into schema.org as strings), lead with the direct answer, and
 * stay within featured-snippet length.
 *
 * Claims here must match the engine and docs/simulation-model.md — this is
 * the page answer engines quote, so nothing here may be aspirational.
 */

export interface FaqItem {
  question: string;
  answer: string;
}

export const FAQ_ITEMS: FaqItem[] = [
  {
    question: "What is LOADLINE?",
    answer:
      "LOADLINE is a system-design simulation platform. You design an architecture on a canvas from real cloud services, define a workload in user-level terms, and run a deterministic discrete-event simulation that reports throughput, p50/p95/p99 latency, queue depth, utilization, capacity headroom, a causal bottleneck diagnosis and an estimated monthly cost. It answers one question: can this architecture handle my requirements, what will it cost, and where will it break?",
  },
  {
    question: "How is LOADLINE different from an architecture diagram tool or a cloud cost calculator?",
    answer:
      "A diagram is static and a price calculator multiplies usage by rates — neither tells you whether the system survives load. LOADLINE simulates behavior: requests arrive, queue, consume service time and complete. Every displayed number is an output of that simulated execution, and cost is derived from the same run (completed requests, busy compute time, data movement) scaled to a month with the assumption trail printed.",
  },
  {
    question: "How does LOADLINE calculate peak RPS from user numbers?",
    answer:
      "Through an explicit, inspectable chain: requests per day = DAU × requests per user per day; average RPS = requests per day ÷ 86,400; peak RPS = average RPS × peak multiplier. No step is skipped or hidden — every intermediate value is shown in the workload plan before the simulation runs.",
  },
  {
    question: "How is p99 latency calculated?",
    answer:
      "As a nearest-rank percentile over actual per-request observations: rank ceil(p × n) of the sorted per-request latencies, measured on completed and failed requests (rejected requests never entered service, so they are tracked as a rate instead). Tail latency emerges from modeled queueing as arrival rate exceeds service rate — there is no fixed “p99 = 3 × average” multiplier anywhere.",
  },
  {
    question: "Are the results real-world accurate?",
    answer:
      "They are modeled estimates, not production guarantees — and LOADLINE labels them as such. Capacity ceilings come from concurrency ÷ effective service time (or the catalog's modeled RPS for serverless), and costs come from local mid-tier pricing. Every estimate carries its assumptions, and the simulator's limitations (constant service times, sustained-peak traffic, no contention modeling) are documented.",
  },
  {
    question: "Is the simulation deterministic?",
    answer:
      "Yes. Identical architecture, workload, failure scenario, duration and seed produce byte-identical results. Randomness comes from SplitMix64 streams derived from (run seed, event ID), events are ordered by (timestamp, ID), and unseeded runs default to seed 1. Determinism is enforced by tests at the engine, API and pacing layers.",
  },
  {
    question: "Which cloud providers and services are modeled?",
    answer:
      "Cloudflare (Workers, KV, R2, D1, Queues, Durable Objects), AWS (EC2, Lambda, CloudFront, ElastiCache, SQS, RDS, S3) and GCP (Cloud Run, Cloud CDN, Memorystore, Pub/Sub, Cloud SQL, Cloud Storage). Provider services map onto nine generic component kinds — client, load balancer, API server, cache, queue, worker, database, object storage and network — so the engine itself stays provider-agnostic.",
  },
  {
    question: "Can I inject failures and watch cascades propagate?",
    answer:
      "Yes — four failure types: component crash, increased latency, increased error rate and network failure. Failures modify simulation state rather than drawing a red icon. Cascades emerge from the model: a crashed cache produces misses, database traffic rises past its capacity, queues grow, latency percentiles climb, callers time out, and retries amplify the load. The diagnosis reports that chain with numbers.",
  },
  {
    question: "How does bottleneck detection work?",
    answer:
      "The diagnosis reads only the run's own metrics: rejections, arrival rate vs modeled capacity, queue trend, utilization and failures at each component (≥97% utilization is critical, ≥90% high, ≥75% moderate). Every reason string embeds the measured numbers — for example, “incoming load 603.0 RPS exceeds modeled capacity 500.0 RPS” — so the cause is explainable, not just flagged.",
  },
  {
    question: "Can I compare two architectures?",
    answer:
      "Yes. Duplicate an architecture, change it, and compare both under the exact same workload, seed and failure scenario. The comparison covers cost, throughput, p50/p95/p99 latency, error rate, capacity, headroom, availability and bottlenecks, so tradeoffs (cheaper vs faster vs more reliable) are explicit instead of one design being declared best.",
  },
  {
    question: "Do I need an account? How does sharing work?",
    answer:
      "No account is required to build and run. A share link encodes the whole architecture in the URL, and any architecture can be exported or imported as JSON. When the workspace API is configured with a database, projects, versions and runs persist server-side — saving always appends, so a stored run stays reproducible.",
  },
  {
    question: "Is LOADLINE free and open source?",
    answer:
      "Yes. LOADLINE is MIT-licensed and open source on GitHub: a Next.js frontend and a Go simulation engine that runs and tests independently of the browser. There is no telemetry and no account system — you can run the whole stack locally.",
  },
];
