import type { Metadata } from "next";
import Link from "next/link";
import { SiteFooter, SiteHeader } from "../../components/sitechrome";
import { GITHUB_URL, SPEC_URL, breadcrumbLd, jsonLdScript } from "../site";

const DESCRIPTION =
  "How LOADLINE simulates an architecture: workload-to-RPS chain, queue and latency formulas, p99 computation, failure cascades, capacity ceilings, cost assumptions and limitations.";

export const metadata: Metadata = {
  title: "How LOADLINE Models Capacity, Latency, Failure & Cost",
  description: DESCRIPTION,
  alternates: {
    canonical: "/docs",
  },
  openGraph: {
    type: "article",
    url: "/docs",
    siteName: "LOADLINE",
    title: "How LOADLINE Models Capacity, Latency, Failure & Cost",
    description: DESCRIPTION,
    locale: "en_US",
  },
  twitter: {
    card: "summary_large_image",
    title: "How LOADLINE Models Capacity, Latency, Failure & Cost",
    description: DESCRIPTION,
  },
};

const COMPONENTS: [string, string][] = [
  ["client", "Generates Poisson arrivals at the configured peak RPS and receives responses."],
  [
    "load balancer",
    "Routes with its own concurrency and queue; optional fan-out replicates a request to N downstream targets.",
  ],
  [
    "api server",
    "Computes service time per operation and forwards reads and writes along its links.",
  ],
  [
    "cache",
    "Reads hit with the configured probability (seeded roll) and terminate; misses and writes continue downstream.",
  ],
  [
    "queue",
    "Zero-delay pass-through that buffers into exactly one linked worker — the worker's queue holds the real backlog.",
  ],
  ["worker", "Async compute: drains its queue with its concurrency slots; terminal node."],
  ["database", "Priced read/write service times — the classic bottleneck."],
  ["object storage", "Priced get/put service times."],
  ["network", "Pure transit-latency hop, unconstrained."],
];

const FAILURES: [string, string][] = [
  ["crash", "The component cannot serve: requests fail at it (or pass through untouched when configured, e.g. cache → origin)."],
  ["increased latency", "A fixed per-request service-time penalty while active."],
  ["increased error rate", "A fraction of serviced requests fails while active."],
  ["network failure", "Transit latency inflation plus packet loss (drops)."],
];

const METRICS: [string, string, string][] = [
  ["Throughput", "Completed requests ÷ measured window", "per component, window average"],
  ["Error rate", "Failed ÷ generated requests", "system, cumulative"],
  ["p50 / p95 / p99", "Nearest-rank percentile over per-request terminal latencies", "system, per request"],
  ["Queue depth", "Current FIFO length; max depth is the high-water mark", "per component, instantaneous / max"],
  ["Utilization", "Busy time ÷ (concurrency × window)", "per component, window average"],
  ["Arrival vs capacity RPS", "Arrived ÷ window against the configured modeled ceiling", "per component, window average"],
  ["Headroom", "1 − utilization (and 1 − arrival/capacity)", "per component"],
];

export default function DocsPage() {
  return (
    <>
      <SiteHeader current="/docs" />
      <main className="site-main doc">
        <nav className="crumbs" aria-label="breadcrumb">
          <Link href="/">LOADLINE</Link> / docs
        </nav>

        <h1>How LOADLINE simulates an architecture</h1>
        <p className="lead">
          LOADLINE is a deterministic, discrete-event simulation (DES) of your
          architecture. Simulated time is an integer nanosecond counter fully
          decoupled from wall-clock time: a 60-second horizon runs in
          milliseconds. Requests arrive, queue, consume service time and
          complete — every metric the UI shows is measured from those events,
          never fabricated to look realistic.
        </p>

        <div className="callout">
          <b>What LOADLINE is:</b> an explainable, reproducible simulation
          with explicit assumptions. <b>What it is not:</b> a production-accuracy
          oracle — modeled ceilings and cost figures are estimates from labeled
          local models, not guarantees or live billing.
        </div>

        <h2>From users to peak RPS — the workload chain</h2>
        <p>
          User-level requirements are converted through an explicit chain with
          every intermediate value exposed in the run plan. Nothing jumps from
          “1M users” to “10k RPS” without showing its work:
        </p>
        <pre>
          <code>{`RequestsPerDay = DAU × RequestsPerUserPerDay
AverageRPS    = RequestsPerDay ÷ 86400
PeakRPS       = AverageRPS × PeakMultiplier
ReadFraction  = ReadWriteRatio ÷ (ReadWriteRatio + 1)
MeanInterArrivalMillis = 1000 ÷ PeakRPS`}</code>
        </pre>
        <p>
          Arrivals are a homogeneous Poisson process: exponential gaps drawn
          from a seeded stream, with mean rate exactly <code>PeakRPS</code> at
          peak. Values are validated before the run starts (<code>DAU ≤ TotalUsers</code>,
          <code> PeakMultiplier ≥ 1</code>, non-negative payload), and invalid
          specs are rejected explicitly rather than silently accepted.
        </p>

        <h2>Component models</h2>
        <p>
          Nine generic component kinds carry the behavior; provider services
          (Cloudflare, AWS, GCP) map onto them in the catalog layer, so the
          engine itself never contains provider-specific branches.
        </p>
        <table>
          <thead>
            <tr>
              <th>Kind</th>
              <th>Modeled behavior</th>
            </tr>
          </thead>
          <tbody>
            {COMPONENTS.map(([kind, behavior]) => (
              <tr key={kind}>
                <td>{kind}</td>
                <td>{behavior}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p>
          Architectures are validated before any run: exactly one client, no
          cycles, every node reachable, every queue fed by exactly one worker.
        </p>

        <h2>Queueing and latency</h2>
        <p>
          Queues are real system state — one FIFO line per constrained
          component. When arrival rate exceeds service rate, depth grows; the
          trend classifier reports <code>growing</code>, <code>draining</code> or
          <code>stable</code>. Admission rejects only when concurrency slots are
          full <em>and</em> the queue is at its limit; rejected requests never
          enter service, so they are excluded from latency percentiles and
          tracked as a rate instead.
        </p>
        <pre>
          <code>{`request latency = Σ over visited components (
    queue wait    — time in that component's FIFO
  + service time  — spec service time + injected failure penalty
)
+ transit         — unconstrained hops (network, client)`}</code>
        </pre>
        <p>
          Tail latency <em>emerges</em> from queueing: under load, p99 grows
          because later requests wait behind earlier ones. Percentiles are
          nearest-rank over actual per-request observations
          (rank <code>ceil(p × n)</code> of sorted latencies) — there is no
          artificial tail multiplier anywhere in the engine.
        </p>

        <h2>Failure injection and cascades</h2>
        <table>
          <thead>
            <tr>
              <th>Type</th>
              <th>Effect while active</th>
            </tr>
          </thead>
          <tbody>
            {FAILURES.map(([type, effect]) => (
              <tr key={type}>
                <td>{type}</td>
                <td>{effect}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p>
          Failures modify simulation state — they are not a red icon. Cascades
          are emergent, never scripted: a crashed cache produces misses, cache
          misses become database traffic, database arrival exceeds its
          capacity, the queue grows, percentiles rise, callers with budgets
          time out, and retries re-enter the failing component and amplify the
          load. A test asserts each link of that chain from measured numbers.
        </p>

        <h2>Metrics and their definitions</h2>
        <table>
          <thead>
            <tr>
              <th>Metric</th>
              <th>Definition</th>
              <th>Scope</th>
            </tr>
          </thead>
          <tbody>
            {METRICS.map(([metric, definition, scope]) => (
              <tr key={metric}>
                <td>{metric}</td>
                <td>{definition}</td>
                <td>{scope}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p>
          The run satisfies a conservation invariant —
          <code> generated = completed + rejected + failed + in-flight </code>
          — asserted by tests including fan-out and horizon-edge cases.
        </p>

        <h2>Capacity model</h2>
        <p>
          For each provider-backed component the modeled ceiling is derived,
          not guessed, and the derivation is printed in the report's
          assumption trail:
        </p>
        <pre>
          <code>{`MaxSustainableRPS = concurrency ÷ effective service time   (static kinds)
                  = catalog ModeledRPS                            (serverless kinds)
Headroom          = 1 − Utilization
Saturated         = arrival exceeded the modeled ceiling`}</code>
        </pre>
        <blockquote>
          Example: 8 database slots ÷ 5 ms service ≈ 1,600 RPS modeled
          ceiling — “arrival is 50% of ceiling” is then a measured statement
          about the run, not a marketing number.
        </blockquote>
        <p>
          This is a <strong>modeled ceiling of the current configuration</strong> —
          an estimate from the service model, not a measured quantity and not a
          guaranteed production limit. Real systems usually degrade earlier
          (contention, GC, hot keys, connection pools).
        </p>

        <h2>Cost estimation</h2>
        <p>Cost consumes simulation outputs — it never lives inside component logic:</p>
        <pre>
          <code>{`simulation outputs (completed requests, GB-s busy, window)
   ↓ scale to a 730h month at observed average rates (factor printed)
pricing model per service (unit prices + free-tier allowances)
   ↓
monthly estimate with full assumption trail`}</code>
        </pre>
        <p>
          Every line item carries component, category, quantity, unit, unit
          price and monthly cost; every estimate carries the scale factor, the
          workload basis (DAU × requests/user/day) and the marker that prices
          are local mid-tier-region estimates. Pricing models cover per
          request, compute time, instance-hour, storage/month and
          bandwidth/egress. It is always labeled an estimate — never live
          billing.
        </p>

        <h2>Determinism and reproducibility</h2>
        <p>
          Given identical architecture, workload, failures, duration and seed,
          runs are byte-identical. Randomness is SplitMix64: each event draws
          from an independent stream derived from <code>(runSeed, eventID)</code>,
          events are ordered by <code>(timestamp, ID)</code> with monotonically
          increasing IDs, no wall clock enters any scheduling path, and
          unseeded runs default to seed 1. Tests lock this at the engine, sim,
          API and pacing layers.
        </p>

        <h2>Known limitations</h2>
        <p>Honesty is part of the model — the engine deliberately does not model:</p>
        <ul>
          <li>
            <strong>Service-time invariance</strong> — service times are
            constant per op; real systems degrade under contention, so modeled
            ceilings are optimistic near saturation.
          </li>
          <li>
            <strong>Cache realism</strong> — hits are independent seeded rolls
            at a fixed ratio; no key space, TTL, eviction or locality.
          </li>
          <li>
            <strong>Traffic shape</strong> — a sustained Poisson stream at peak
            intensity; no diurnal curves or geographic distribution.
          </li>
          <li>
            <strong>Autoscaling</strong> — fleets are static per run; no
            mid-run scale-out events.
          </li>
          <li>
            <strong>Retry sophistication</strong> — per-request FIFO retries
            with exponential backoff; no circuit breakers or hedging.
          </li>
          <li>
            <strong>Latency attribution</strong> — per-request, not per-hop;
            there is no “which component added what” breakdown yet.
          </li>
        </ul>

        <div className="cta-row">
          <Link className="cta" href="/">
            Open the simulator
          </Link>
          <Link className="cta alt" href="/faq">
            Read the FAQ
          </Link>
          <a className="cta alt" href={SPEC_URL}>
            Full model spec (markdown)
          </a>
          <a className="cta alt" href={GITHUB_URL}>
            Source on GitHub
          </a>
        </div>

        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: jsonLdScript({
              "@context": "https://schema.org",
              "@type": "TechArticle",
              headline: "How LOADLINE models capacity, latency, failure and cost",
              description: DESCRIPTION,
              articleSection: "System design simulation",
              inLanguage: "en",
              author: {
                "@type": "Organization",
                name: "LOADLINE",
                url: GITHUB_URL,
              },
              mainEntityOfPage: "/docs",
            }),
          }}
        />
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: jsonLdScript(
              breadcrumbLd([
                { name: "LOADLINE", path: "/" },
                { name: "Docs", path: "/docs" },
              ]),
            ),
          }}
        />
      </main>
      <SiteFooter />
    </>
  );
}
