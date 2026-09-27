"use client";

/**
 * LOADLINE control console — the first slice of the frontend wired to
 * the Go backend over Protobuf + ConnectRPC.
 *
 * What this page owns: UI state only. Every number displayed comes from
 * the simulation service (create → run → stream progress → results →
 * diagnosis → capacity → cost). Nothing is computed or faked here.
 *
 * Visual language lives in app/globals.css + app/primitives.css and the
 * primitives in components/ui.tsx. This file composes them.
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
import {
  Badge,
  Button,
  Checkbox,
  Divider,
  EmptyState,
  Field,
  Input,
  Metric,
  Panel,
  Section,
  StatusIndicator,
  Toolbar,
  Tooltip,
  toneForWord,
} from "../components/ui";

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

const STATUS: Record<Phase, { state: "ok" | "warn" | "bad" | "idle"; label: string }> = {
  idle: { state: "idle", label: "idle" },
  running: { state: "warn", label: "running" },
  done: { state: "ok", label: "complete" },
  error: { state: "bad", label: "error" },
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

  const status = STATUS[state.phase];
  const m = state.results?.metrics;

  return (
    <main className="console">
      <header className="masthead">
        <div className="masthead-title">Loadline</div>
        <p className="masthead-sub">
          system-design simulation <em>·</em> requirements → architecture → simulation →
          failure → diagnosis
        </p>
        <div className="masthead-bar">
          <Toolbar>
            <Field label="server">
              <Input value={serverUrl} onChange={setServerUrl} width={220} />
            </Field>
            <Checkbox checked={injectCrash} onChange={setInjectCrash}>
              crash cache (t=1s → 6s)
            </Checkbox>
            <Button variant="primary" disabled={state.phase === "running"} onClick={() => void run()}>
              {state.phase === "running" ? "simulating…" : "run simulation"}
            </Button>
          </Toolbar>
          <StatusIndicator state={status.state} label={status.label} />
        </div>
      </header>

      {state.error && <p className="error-line">error: {state.error}</p>}

      <div className="console-stack">
        {/* ------------------------------------------------ live progress */}
        {state.phase === "running" && state.progress && (
          <Panel title="Live progress" tag={`sim time ${f0(state.progress.simTimeMs)} ms`}>
            <table>
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
          </Panel>
        )}

        {/* ---------------------------------------------------- idle hint */}
        {state.phase === "idle" && !state.error && (
          <Panel title="Console">
            <EmptyState>
              no run yet — point the server field at the simulation backend and execute.
            </EmptyState>
          </Panel>
        )}

        {/* ------------------------------------------------------ results */}
        {state.results && m && (
          <>
            <Panel title="Results" tag={state.simId ?? undefined}>
              <div className="metric-row">
                <Metric label="generated" value={f0(m.generated)} />
                <Metric label="completed" value={f0(m.completed)} />
                <Metric label="rejected" value={f0(m.rejected)} />
                <Metric label="failed" value={f0(m.failed)} />
                <Metric label="error rate" value={pct(m.errorRate)} />
                <Metric label="timeout rate" value={pct(m.timeoutRate)} />
              </div>
              <Divider />
              <Section label="latency ms">
                <div className="metric-row">
                  <Metric label="avg" value={f2(m.avgLatencyMs)} />
                  <Metric label="p50" value={f2(m.p50Ms)} />
                  <Metric
                    label="p95"
                    value={f2(m.p95Ms)}
                  />
                  <Metric
                    label={
                      <Tooltip text="99th percentile, nearest-rank over terminal outcomes (completions and mid-path failures).">
                        p99
                      </Tooltip>
                    }
                    value={f2(m.p99Ms)}
                  />
                  <Metric label="max" value={f2(m.maxLatencyMs)} />
                </div>
              </Section>
            </Panel>

            <Panel title="Workload plan" tag="derived">
              <p className="prose">
                DAU {state.results.plan?.dau?.toString()} of{" "}
                {state.results.plan?.totalUsers?.toString()} users →{" "}
                {f0(state.results.plan?.requestsPerDay)} req/day → avg{" "}
                {f1(state.results.plan?.averageRps)} RPS → peak{" "}
                {f1(state.results.plan?.peakRps)} RPS (×{f1(state.results.plan?.peakMultiplier)})
              </p>
              <p className="prose prose-dim">
                users → DAU → requests/user/day → requests/day → average RPS → peak multiplier
                → peak RPS
              </p>
            </Panel>

            <Panel title="Components">
              <table>
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
                  {m.components.map((c) => (
                    <tr key={c.id}>
                      <td>{c.id}</td>
                      <td>{c.kind}</td>
                      <td>{f0(c.arrived)}</td>
                      <td>{f0(c.rejected)}</td>
                      <td>{c.maxQueueDepth}</td>
                      <td>
                        <Badge tone={toneForWord(c.queueTrend)}>{c.queueTrend}</Badge>
                      </td>
                      <td>{pct(c.utilization)}</td>
                      <td>{f1(c.arrivalRps)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Panel>
          </>
        )}

        {/* ---------------------------------------------------- diagnosis */}
        {state.diagnosis && (
          <Panel title="Diagnosis">
            <p className="prose">{state.diagnosis.summary}</p>
            {state.diagnosis.bottlenecks.length === 0 ? (
              <EmptyState>no bottlenecks detected</EmptyState>
            ) : (
              state.diagnosis.bottlenecks.map((b) => (
                <div key={b.componentId} className={`diag diag-${b.severity}`}>
                  <div className="diag-head">
                    <span className="diag-id">{b.componentId}</span>
                    <Badge tone={toneForWord(b.severity)}>{b.severity}</Badge>
                  </div>
                  <ul className="reasons">
                    {b.reasons.map((r, i) => (
                      <li key={i}>{r}</li>
                    ))}
                  </ul>
                </div>
              ))
            )}
            {state.diagnosis.impacts.length > 0 && (
              <>
                <Section label="impact" />
                <ul className="impacts">
                  {state.diagnosis.impacts.map((i, k) => (
                    <li key={k}>{i}</li>
                  ))}
                </ul>
              </>
            )}
          </Panel>
        )}

        {/* ----------------------------------------------------- capacity */}
        {state.capacity && (
          <Panel title="Capacity" tag="estimates from simulation outputs">
            <table>
              <thead>
                <tr>
                  <th>component</th>
                  <th>service</th>
                  <th>current rps</th>
                  <th>max sustainable</th>
                  <th>
                    <Tooltip text="headroom = 1 − utilization">headroom</Tooltip>
                  </th>
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
                    <td>
                      {r.saturated && <Badge tone="bad">sat</Badge>}{" "}
                      {r.bottleneck && <Badge tone="warn">bn</Badge>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        )}

        {/* --------------------------------------------------------- cost */}
        {state.cost && state.cost.estimate && (
          <Panel title="Monthly cost" tag="estimate — not live billing data">
            <div className="metric-row">
              <Metric
                label="total"
                value={`$${f2(state.cost.estimate.total)}`}
                unit="/mo"
                note={state.cost.estimate.currency}
              />
            </div>
            <Divider />
            <div className="kv">
              {Object.entries(state.cost.estimate.byCategory ?? {}).map(([cat, amt]) => (
                <span key={cat} className="kv-item">
                  <span className="kv-key">{cat}</span>
                  <span className="kv-val">${f2(amt)}</span>
                </span>
              ))}
            </div>
          </Panel>
        )}

        {/* ------------------------------------------------- cost absent */}
        {state.results && !state.cost && !state.capacity && (
          <Panel title="Capacity / cost">
            <EmptyState>
              no provider-backed estimates — components need provider + service references.
            </EmptyState>
          </Panel>
        )}
      </div>
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
