"use client";

/**
 * LOADLINE control console — the first slice of the frontend wired to
 * the Go backend over Protobuf + ConnectRPC.
 *
 * What this page owns: UI state only. Every number displayed comes from
 * the simulation service (create → run → stream progress → results →
 * diagnosis → capacity → cost). Nothing is computed or faked here.
 */
import { useMemo, useReducer, useRef, useState } from "react";
import {
  ComponentKind,
  FailureType,
  RunStatus,
  createLoadlineClient,
} from "@loadline/api";
import type {
  Diagnosis,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
  ProgressSnapshot,
} from "@loadline/api";

type Phase = "idle" | "running" | "done" | "error";

interface State {
  phase: Phase;
  simId: string | null;
  progress: ProgressSnapshot | null;
  results: FinalResults | null;
  diagnosis: Diagnosis | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
  error: string;
}

const initial: State = {
  phase: "idle",
  simId: null,
  progress: null,
  results: null,
  diagnosis: null,
  capacity: null,
  cost: null,
  error: "",
};

type Action =
  | { type: "reset" }
  | { type: "created"; id: string }
  | { type: "progress"; snap: ProgressSnapshot }
  | { type: "results"; results: FinalResults }
  | { type: "diagnosis"; diagnosis: Diagnosis }
  | { type: "capacity"; capacity: GetCapacityResponse }
  | { type: "cost"; cost: GetCostEstimateResponse }
  | { type: "error"; message: string };

function reducer(s: State, a: Action): State {
  switch (a.type) {
    case "reset":
      return initial;
    case "created":
      return { ...s, simId: a.id };
    case "progress":
      return { ...s, phase: "running", progress: a.snap };
    case "results":
      return { ...s, phase: "done", results: a.results };
    case "diagnosis":
      return { ...s, diagnosis: a.diagnosis };
    case "capacity":
      return { ...s, capacity: a.capacity };
    case "cost":
      return { ...s, cost: a.cost };
    case "error":
      return { ...s, phase: "error", error: a.message };
  }
}

// The scenario: AWS catalog architecture under load, then the same
// architecture with the cache crashed for 5s — the cascade the product
// exists to expose. Two runs, same seed, controlled comparison.
const ARCHITECTURE = {
  schemaVersion: "1",
  name: "aws-web",
  components: [
    { id: "client", kind: ComponentKind.CLIENT },
    {
      id: "api",
      kind: ComponentKind.API_SERVER,
      provider: "aws",
      service: "lambda",
      config: { memoryMb: 512 },
    },
    {
      id: "cache",
      kind: ComponentKind.CACHE,
      provider: "aws",
      service: "elasticache",
      hitRatio: 0.8,
    },
    {
      id: "db",
      kind: ComponentKind.DATABASE,
      provider: "aws",
      service: "rds",
      config: { storageGb: 100 },
    },
  ],
  links: [
    { from: "client", to: "api" },
    { from: "api", to: "cache" },
    { from: "cache", to: "db" },
  ],
};

const WORKLOAD = {
  totalUsers: 10_000_000n,
  dau: 1_000_000n,
  requestsPerUserPerDay: 40,
  peakMultiplier: 5,
  readWriteRatio: 4,
  payloadBytes: 4096n,
};

const OPTIONS = {
  seed: 7n,
  durationMs: 10_000,
  maxRetries: 2,
  backoffBaseMs: 5,
  timeoutMs: 50,
  retryOn: ["api", "cache"],
};

export default function Home() {
  const [serverUrl, setServerUrl] = useState("http://localhost:8080");
  const [injectCrash, setInjectCrash] = useState(false);
  const [state, dispatch] = useReducer(reducer, initial);

  // Typed client, rebuilt when the server URL changes.
  const client = useMemo(() => createLoadlineClient({ baseUrl: serverUrl }), [serverUrl]);

  // ID of the most recent healthy run, used as the diagnosis baseline.
  const baselineIdRef = useRef<string | null>(null);

  async function run() {
    dispatch({ type: "reset" });
    try {
      // 1. Create the simulation (provider-backed architecture), with an
      //    optional cache-crash injection.
      const created = await client.createSimulation({
        architecture: ARCHITECTURE,
        workload: WORKLOAD,
        options: injectCrash
          ? {
              ...OPTIONS,
              failures: [
                {
                  target: "cache",
                  type: FailureType.CRASH,
                  startMs: 1_000,
                  durationMs: 5_000,
                  config: { passThrough: true },
                },
              ],
            }
          : OPTIONS,
      });
      const id = created.simulation?.id ?? "";
      dispatch({ type: "created", id });

      // 2. Subscribe to the metrics stream first, then trigger the run —
      //    the stream then observes every state change of the run.
      const resultsPromise = (async () => {
        // Attach slightly before RunSimulation so no progress is missed.
        await new Promise((r) => setTimeout(r, 20));
        const stream = client.streamMetrics({ simulationId: id });
        let final: FinalResults | null = null;
        for await (const ev of stream) {
          if (ev.event.case === "progress" && ev.event.value) {
            dispatch({ type: "progress", snap: ev.event.value });
          }
          if (ev.event.case === "status" && ev.event.value) {
            const st = ev.event.value;
            if (st.status !== RunStatus.COMPLETED) {
              throw new Error(st.error || `simulation ended with ${st.status}`);
            }
          }
          if (ev.event.case === "results" && ev.event.value) {
            final = ev.event.value;
          }
        }
        if (!final) throw new Error("stream ended without results");
        return final;
      })();

      await client.runSimulation({ simulationId: id });
      const final = await resultsPromise;
      if (!injectCrash) baselineIdRef.current = id;

      // 3. Fetch analysis built on the finished run.
      dispatch({ type: "results", results: final });
      const diag = await client.getDiagnosis({
        simulationId: id,
        // Compare the crash run against the last healthy baseline.
        baselineSimulationId: injectCrash ? (baselineIdRef.current ?? "") : "",
      });
      if (diag.diagnosis) dispatch({ type: "diagnosis", diagnosis: diag.diagnosis });
      try {
        const cap = await client.getCapacity({ simulationId: id });
        dispatch({ type: "capacity", capacity: cap });
        const cost = await client.getCostEstimate({ simulationId: id });
        dispatch({ type: "cost", cost });
      } catch {
        // Architectures without provider references have no capacity/cost
        // estimates — surfaced as absent panels, not errors.
      }
    } catch (e) {
      dispatch({ type: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }

  return (
    <main style={{ maxWidth: 960, margin: "0 auto", padding: 24, fontFamily: "ui-monospace, monospace" }}>
      <h1 style={{ fontSize: 24 }}>LOADLINE</h1>
      <p style={{ color: "#666" }}>
        System-design simulation: requirements → architecture → simulation → failure → diagnosis.
      </p>

      <section style={{ display: "flex", gap: 12, alignItems: "center", margin: "16px 0" }}>
        <label>
          server{" "}
          <input
            value={serverUrl}
            onChange={(e) => setServerUrl(e.target.value)}
            style={{ width: 220, fontFamily: "inherit" }}
          />
        </label>
        <label>
          <input
            type="checkbox"
            checked={injectCrash}
            onChange={(e) => setInjectCrash(e.target.checked)}
          />{" "}
          crash cache (t=1s → 6s)
        </label>
        <button
          onClick={() => void run()}
          disabled={state.phase === "running"}
          style={{ padding: "6px 16px" }}
        >
          {state.phase === "running" ? "simulating…" : "run simulation"}
        </button>
      </section>

      {state.error && <p style={{ color: "#c0392b" }}>error: {state.error}</p>}

      {state.progress && state.phase === "running" && (
        <section>
          <h2>live progress (sim time {f0(state.progress.simTimeMs)} ms)</h2>
          <table cellPadding={4}>
            <thead>
              <tr>
                <th>component</th>
                <th>queue</th>
                <th>in-flight</th>
                <th>arrived</th>
                <th>completed</th>
                <th>util</th>
              </tr>
            </thead>
            <tbody>
              {state.progress.components.map((c) => (
                <tr key={c.componentId}>
                  <td>{c.componentId}</td>
                  <td>{c.queueDepth}</td>
                  <td>{c.inFlight}</td>
                  <td>{f0(c.arrived)}</td>
                  <td>{f0(c.completed)}</td>
                  <td>{pct(c.utilization)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {state.results && (
        <section>
          <h2>results</h2>
          <p>
            generated {f0(state.results.metrics?.generated)} · completed{" "}
            {f0(state.results.metrics?.completed)} · rejected {f0(state.results.metrics?.rejected)} ·
            failed {f0(state.results.metrics?.failed)}
          </p>
          <p>
            latency ms: avg {f2(state.results.metrics?.avgLatencyMs)} · p50{" "}
            {f2(state.results.metrics?.p50Ms)} · p95 {f2(state.results.metrics?.p95Ms)} · p99{" "}
            {f2(state.results.metrics?.p99Ms)} · max {f2(state.results.metrics?.maxLatencyMs)}
          </p>
          <h3>workload plan (derived)</h3>
          <p>
            DAU {state.results.plan?.dau?.toString()} of{" "}
            {state.results.plan?.totalUsers?.toString()} users →{" "}
            {f0(state.results.plan?.requestsPerDay)} req/day → avg{" "}
            {f1(state.results.plan?.averageRps)} RPS → peak {f1(state.results.plan?.peakRps)} RPS
            (×{f1(state.results.plan?.peakMultiplier)})
          </p>
          <h3>components</h3>
          <table cellPadding={4}>
            <thead>
              <tr>
                <th>id</th>
                <th>kind</th>
                <th>arrived</th>
                <th>rejected</th>
                <th>queue max</th>
                <th>trend</th>
                <th>util</th>
                <th>arrival rps</th>
              </tr>
            </thead>
            <tbody>
              {state.results.metrics?.components.map((c) => (
                <tr key={c.id}>
                  <td>{c.id}</td>
                  <td>{c.kind}</td>
                  <td>{f0(c.arrived)}</td>
                  <td>{f0(c.rejected)}</td>
                  <td>{c.maxQueueDepth}</td>
                  <td>{c.queueTrend}</td>
                  <td>{pct(c.utilization)}</td>
                  <td>{f1(c.arrivalRps)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {state.diagnosis && (
        <section>
          <h2>diagnosis</h2>
          <p>{state.diagnosis.summary}</p>
          {state.diagnosis.bottlenecks.map((b) => (
            <div key={b.componentId} style={{ margin: "8px 0", paddingLeft: 12, borderLeft: "3px solid #c0392b" }}>
              <strong>
                {b.componentId} ({b.severity})
              </strong>
              <ul>
                {b.reasons.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </div>
          ))}
          {state.diagnosis.impacts.length > 0 && (
            <ul style={{ color: "#666" }}>
              {state.diagnosis.impacts.map((i, k) => (
                <li key={k}>{i}</li>
              ))}
            </ul>
          )}
        </section>
      )}

      {state.capacity && (
        <section>
          <h2>capacity (estimates from simulation outputs)</h2>
          <table cellPadding={4}>
            <thead>
              <tr>
                <th>component</th>
                <th>service</th>
                <th>current rps</th>
                <th>max sustainable</th>
                <th>headroom</th>
                <th>flags</th>
              </tr>
            </thead>
            <tbody>
              {state.capacity.reports.map((r) => (
                <tr key={r.componentId}>
                  <td>{r.componentId}</td>
                  <td>{r.service}</td>
                  <td>{f1(r.currentRps)}</td>
                  <td>{f0(r.maxSustainableRps)}</td>
                  <td>{pct(r.headroom)}</td>
                  <td>{[r.saturated && "SAT", r.bottleneck && "BN"].filter(Boolean).join(" ")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {state.cost && (
        <section>
          <h2>monthly cost (ESTIMATE — not live billing data)</h2>
          <p>
            total ${f2(state.cost.estimate?.total)} per month ({state.cost.estimate?.currency})
          </p>
          <table cellPadding={4}>
            <tbody>
              {Object.entries(state.cost.estimate?.byCategory ?? {}).map(([cat, amt]) => (
                <tr key={cat}>
                  <td>{cat}</td>
                  <td>${f2(amt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </main>
  );
}

function f0(v?: number | bigint) {
  return (v ?? 0).toLocaleString(undefined, { maximumFractionDigits: 0 });
}
function f1(v?: number) {
  return (v ?? 0).toFixed(1);
}
function f2(v?: number) {
  return (v ?? 0).toFixed(2);
}
function pct(v?: number) {
  return `${((v ?? 0) * 100).toFixed(1)}%`;
}
