import { GITHUB_URL, SITE_DESCRIPTION, absolute } from "../site";

/**
 * llms.txt — the llmstxt.org convention: a concise, citable markdown
 * summary of the site for language models and answer engines. Served as a
 * route (not a static file) so every link uses the deployed SITE_URL.
 */
export const dynamic = "force-static";

const BODY = `# LOADLINE

> ${SITE_DESCRIPTION}

LOADLINE answers: *Can this architecture actually handle my requirements, what will it cost, and where will it break?* Core loop: requirements → architecture → simulation → failure → diagnosis → modification → comparison. It is not a diagram editor and not a cloud price calculator: every displayed number is output of a seeded discrete-event simulation, and every estimate carries its assumptions.

## Pages

- [Workbench](${absolute("/")}): the application — visual architecture canvas, provider catalog, workload controls, live simulation console, bottleneck/capacity/cost analysis.
- [How LOADLINE works](${absolute("/docs")}): workload-to-RPS chain, component, queue and latency models, metric definitions, capacity and cost formulas, known limitations.
- [FAQ](${absolute("/faq")}): what LOADLINE is, how p99 is computed, determinism, providers, failure injection, comparison and sharing.
- [Simulation model specification](${GITHUB_URL}/blob/main/docs/simulation-model.md): the full reference for the Go engine.
- [Source code](${GITHUB_URL}): MIT-licensed Go simulation engine + Next.js frontend.

## Key facts

- **Engine:** Go discrete-event simulation; identical architecture + workload + failures + duration + seed produce byte-identical results (SplitMix64 per-event streams; unseeded runs default to seed 1).
- **Workload chain:** users → DAU → requests/user/day → requests/day → average RPS (÷ 86,400) → × peak multiplier → peak RPS; every intermediate value is shown.
- **Latency:** total latency = queue wait + service time + transit across visited components; p50/p95/p99 are nearest-rank percentiles over per-request observations — tails emerge from queueing, never from a fixed multiplier.
- **Queueing:** one FIFO per constrained component; queues grow when arrival rate exceeds service rate; admission rejects only when slots are full and the queue is at its limit.
- **Failures:** crash, increased latency, increased error rate, network failure. Cascades (cache crash → database saturation → queue growth → timeouts → retry amplification) emerge from the model and are asserted by tests.
- **Metrics:** throughput, error/timeout rate, p50/p95/p99, queue depth and trend, utilization, arrival vs modeled capacity, headroom — each with a documented definition.
- **Capacity:** max sustainable RPS = concurrency ÷ effective service time (static kinds) or the catalog's modeled RPS (serverless kinds); always labeled a modeled ceiling, not a production guarantee.
- **Cost:** estimate from local per-request / compute-time / instance-hour / storage / egress pricing, scaled from the simulated window to 730h with the factor and allowances printed. Never live billing.
- **Components:** nine generic kinds — client, load balancer, API server, cache, queue, worker, database, object storage, network. Cloudflare, AWS and GCP services map onto them in the catalog layer.
- **Bottleneck diagnosis:** reads only the run's own metrics (rejections, arrival vs capacity, queue trend, utilization) and embeds measured numbers in every reason string.
`;

export function GET(): Promise<Response> {
  return Promise.resolve(
    new Response(BODY, {
      headers: { "Content-Type": "text/markdown; charset=utf-8" },
    }),
  );
}

