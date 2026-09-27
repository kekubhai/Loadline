"use client";

/**
 * LOADLINE — operational workstation shell.
 *
 * Layout: top nav (identity + views + run control), left palette
 * (provider catalog + architecture components), center canvas, right
 * inspector, bottom operations strip (live progress + system metrics).
 *
 * This file owns UI state and backend I/O only. Every displayed number
 * comes from the simulation service; nothing is computed or faked here.
 */
import { useEffect, useMemo, useReducer, useRef, useState } from "react";
import {
  ComponentKind,
  FailureType,
  RunStatus,
  createLoadlineClient,
} from "@loadline/api";
import type {
  CatalogService,
  ComponentSpec,
  Diagnosis,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
  ProgressSnapshot,
} from "@loadline/api";
import { ArchitectureCanvas } from "../components/canvas";
import { Inspector } from "../components/inspector";
import { Palette } from "../components/palette";
import { f0, f1, f2, kindName, pct } from "../components/format";
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
  toneForWord,
} from "../components/ui";

/* ------------------------------------------------------------ run state -- */

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
      return { ...s, phase: "done", progress: null, results: a.results };
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

/* ------------------------------------------------------- scenario inputs -- */

// The demonstration architecture, built on the AWS catalog: Client → Lambda
// → ElastiCache → RDS. The crash toggle reuses the same seed for a
// controlled baseline-vs-cascade comparison.
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
  ] as ComponentSpec[],
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

  // Workstation views.
  const [view, setView] = useState("architecture");
  const [selected, setSelected] = useState<string | null>(null);
  const [catalog, setCatalog] = useState<CatalogService[] | null>(null);
  const [catalogError, setCatalogError] = useState("");

  // Typed client, rebuilt when the server URL changes.
  const client = useMemo(() => createLoadlineClient({ baseUrl: serverUrl }), [serverUrl]);

  // ID of the most recent healthy run, used as the diagnosis baseline.
  const baselineIdRef = useRef<string | null>(null);

  // Fetch the provider catalog once per server URL; drives the palette.
  useEffect(() => {
    let alive = true;
    setCatalog(null);
    setCatalogError("");
    client
      .listCatalog({})
      .then((res) => {
        if (alive) setCatalog(res.services);
      })
      .catch((e) => {
        if (alive) setCatalogError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [client]);

  async function run() {
    dispatch({ type: "reset" });
    try {
      // 1. Register the simulation (with an optional cache-crash injection).
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

      // 2. Subscribe to the metrics stream slightly before triggering the
      //    run so no progress frame is missed.
      const resultsPromise = (async () => {
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

      // 3. Fetch the analysis built on the finished run.
      dispatch({ type: "results", results: final });
      const diag = await client.getDiagnosis({
        simulationId: id,
        baselineSimulationId: injectCrash ? (baselineIdRef.current ?? "") : "",
      });
      if (diag.diagnosis) dispatch({ type: "diagnosis", diagnosis: diag.diagnosis });
      try {
        const cap = await client.getCapacity({ simulationId: id });
        dispatch({ type: "capacity", capacity: cap });
        const cost = await client.getCostEstimate({ simulationId: id });
        dispatch({ type: "cost", cost });
      } catch {
        // Provider-backed estimates are absent for generic architectures —
        // surfaced as missing panels, not errors.
      }
    } catch (e) {
      dispatch({ type: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }

  const status = STATUS[state.phase];
  const m = state.results?.metrics;

  const canvasNodes = useMemo(
    () =>
      ARCHITECTURE.components.map((c) => {
        const hasProvider = Boolean(c.provider && c.service);
        return {
          id: c.id,
          label: c.id,
          sub: hasProvider ? `${c.provider}/${c.service}` : kindName(c.kind),
        };
      }),
    [],
  );
  const canvasEdges = ARCHITECTURE.links.map((l) => ({
    from: l.from,
    to: l.to,
  }));

  return (
    <div className="shell">
      {/* ------------------------------------------------------------ nav */}
      <header className="topnav">
        <div className="topnav-brand">
          <span className="brand-mark">LOADLINE</span>
          <span className="brand-note">
            system-design simulation · requirements → architecture → simulation → failure →
            diagnosis
          </span>
        </div>
        <nav className="topnav-views" aria-label="views">
          {["architecture", "simulation"].map((v) => (
            <button
              key={v}
              type="button"
              className={`topnav-view${view === v ? " active" : ""}`}
              onClick={() => setView(v)}
            >
              {v}
            </button>
          ))}
        </nav>
        <div className="topnav-actions">
          <StatusIndicator state={status.state} label={status.label} />
          <Button variant="primary" disabled={state.phase === "running"} onClick={() => void run()}>
            {state.phase === "running" ? "simulating…" : "run"}
          </Button>
        </div>
      </header>

      {view === "architecture" ? (
        <div className="workbench">
          {/* --------------------------------------------------- left rail */}
          <aside className="left-rail">
            <Palette
              catalog={catalog}
              catalogError={catalogError}
              components={ARCHITECTURE.components}
              selected={selected}
              onSelect={setSelected}
            />
          </aside>

          {/* ------------------------------------------------------ canvas */}
          <section className="center">
            <div className="center-head">
              <span className="panel-title">architecture</span>
              <span className="center-tag">
                {ARCHITECTURE.components.length} components · {ARCHITECTURE.links.length} links ·
                schema v{ARCHITECTURE.schemaVersion}
                {state.simId ? ` · sim ${state.simId}` : ""}
              </span>
            </div>
            <ArchitectureCanvas
              nodes={canvasNodes}
              edges={canvasEdges}
              selected={selected}
              onSelect={setSelected}
            />
            {state.error && <p className="error-line">error: {state.error}</p>}
          </section>

          {/* -------------------------------------------------- inspector */}
          <aside className="right-rail">
            <div className="rail-head">
              <span className="panel-title">inspector</span>
            </div>
            <Inspector
              selected={selected}
              catalog={catalog}
              components={ARCHITECTURE.components}
              results={state.results}
              capacity={state.capacity}
              cost={state.cost}
            />
          </aside>
        </div>
      ) : (
        /* --------------------------------------------- simulation view */
        <div className="sim-view">
          <div className="sim-toolbar">
            <Field label="server">
              <Input value={serverUrl} onChange={setServerUrl} width={220} />
            </Field>
            <Checkbox checked={injectCrash} onChange={setInjectCrash}>
              crash cache (t=1s → 6s)
            </Checkbox>
            <span className="sim-toolbar-note">
              same seed both runs · healthy run is kept as diagnosis baseline
            </span>
          </div>

          {state.phase === "idle" && !state.error && (
            <Panel title="Console">
              <EmptyState>
                no run yet — point the server field at the simulation backend and execute.
              </EmptyState>
            </Panel>
          )}

          {state.results && m && (
            <div className="sim-columns">
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
                    <Metric label="p95" value={f2(m.p95Ms)} />
                    <Metric label="p99" value={f2(m.p99Ms)} />
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
                  users → DAU → requests/user/day → requests/day → average RPS → peak multiplier →
                  peak RPS
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

              <Panel title="Diagnosis">
                <p className="prose">{state.diagnosis?.summary ?? ""}</p>
                {state.diagnosis && state.diagnosis.bottlenecks.length === 0 && (
                  <EmptyState>no bottlenecks detected</EmptyState>
                )}
                {state.diagnosis?.bottlenecks.map((b) => (
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
                ))}
                {state.diagnosis && state.diagnosis.impacts.length > 0 && (
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

              <Panel title="Capacity" tag="estimates from simulation outputs">
                <table>
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
                    {(state.capacity?.reports ?? []).map((r) => (
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

              <Panel title="Monthly cost" tag="estimate — not live billing data">
                {state.cost?.estimate ? (
                  <>
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
                  </>
                ) : (
                  <EmptyState>
                    no provider-backed estimates — components need provider + service references.
                  </EmptyState>
                )}
              </Panel>
            </div>
          )}
        </div>
      )}

      {/* ------------------------------------------------------ ops strip */}
      <footer className="ops-strip">
        {state.progress ? (
          <>
            <div className="ops-cell ops-label">
              live · t={f0(state.progress.simTimeMs)}ms
            </div>
            <div className="ops-scroll">
              {state.progress.components.map((c) => (
                <span key={c.componentId} className="ops-chip">
                  <span className="ops-chip-id">{c.componentId}</span>
                  <span className="ops-chip-val">q{c.queueDepth}</span>
                  <span className="ops-chip-val">f{c.inFlight}</span>
                  <span className="ops-chip-val">{pct(c.utilization)}</span>
                </span>
              ))}
            </div>
          </>
        ) : (
          <div className="ops-scroll">
            {m ? (
              <>
                <span className="ops-chip">
                  <span className="ops-chip-id">throughput</span>
                  <span className="ops-chip-val">
                    {f1(
                      m.components.reduce((acc, c) => acc + c.throughputRps, 0) /
                        Math.max(1, m.components.filter((c) => c.kind !== "client").length),
                    )}{" "}
                    rps
                  </span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">p50</span>
                  <span className="ops-chip-val">{f2(m.p50Ms)}ms</span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">p95</span>
                  <span className="ops-chip-val">{f2(m.p95Ms)}ms</span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">p99</span>
                  <span className="ops-chip-val">{f2(m.p99Ms)}ms</span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">errors</span>
                  <span className="ops-chip-val">{pct(m.errorRate)}</span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">rejected</span>
                  <span className="ops-chip-val">{f0(m.rejected)}</span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">util</span>
                  <span className="ops-chip-val">
                    {pct(
                      m.components.reduce((acc, c) => acc + c.utilization, 0) /
                        Math.max(1, m.components.length),
                    )}
                  </span>
                </span>
                <span className="ops-chip">
                  <span className="ops-chip-id">queue</span>
                  <span className="ops-chip-val">
                    {m.components.reduce((acc, c) => acc + c.queueDepth, 0)}
                  </span>
                </span>
              </>
            ) : (
              <span className="ops-empty">
                no simulation data — run to populate the operations strip
              </span>
            )}
          </div>
        )}
        <div className="ops-spacer" />
        <StatusIndicator state={status.state} label={state.simId ? `sim ${state.simId}` : status.label} />
      </footer>
    </div>
  );
}
