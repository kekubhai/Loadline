"use client";

/**
 * LOADLINE — operational workstation shell.
 *
 * Layout: top nav (identity + views + run control), left palette
 * (provider catalog + architecture components), center canvas, right
 * inspector, bottom operations strip (live progress + system metrics).
 *
 * This file owns the CANONICAL editor state — one copy of
 * {architecture, workload, options} that every panel renders from and
 * edits through dispatches — plus backend I/O. Panels never keep their
 * own copy of the architecture. Every displayed number comes from the
 * simulation service; nothing is computed or faked here.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import {
  ComponentKind,
  create,
  createLoadlineClient,
} from "@loadline/api";
import {
  ArchitectureSchema,
  ComponentSpecSchema,
  FinalResultsSchema,
  LinkSchema,
  ProviderConfigSchema,
  SimulationOptionsSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import type {
  Architecture,
  CatalogService,
  ComponentSpec,
  Diagnosis,
  Failure,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
  Link,
  ProviderConfig,
  SimulationOptions,
  WorkloadSpec,
} from "@loadline/api";
import { ArchitectureCanvas } from "../components/canvas";
import type { CanvasHandles } from "../components/canvas";
import { Inspector } from "../components/inspector";
import type { ComponentPatch } from "../components/inspector";
import { Palette } from "../components/palette";
import { SimConsole } from "../components/simconsole";
import type { SimOutcome } from "../components/simconsole";
import { WorkloadPanel } from "../components/workloadpanel";
import { FailurePanel } from "../components/failurepanel";
import {
  BottleneckPanel,
  CapacityPanel,
  CostPanel,
  HealthPanel,
} from "../components/analysis";
import { f0, f1, f2, pct } from "../components/format";
import {
  Badge,
  Button,
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

/* ------------------------------------------------------- editor reducer -- */

interface EditorState {
  architecture: Architecture;
  workload: WorkloadSpec;
  options: SimulationOptions;
  failures: Failure[];
  /** Editor selection: component id, "link:<index>", or catalog token. */
  selected: string | null;
  /** Canvas positions per component id (editor presentation state). */
  positions: Record<string, { x: number; y: number }>;
}

const SCHEMA_VERSION = "1";

function freshId(components: ComponentSpec[], base: string): string {
  if (!components.some((c) => c.id === base)) return base;
  for (let i = 2; ; i++) {
    const id = `${base}-${i}`;
    if (!components.some((c) => c.id === id)) return id;
  }
}

type EditAction =
  | { type: "select"; id: string | null }
  | { type: "renameArch"; name: string }
  | {
      type: "addComponent";
      provider: string;
      service: string;
      kind: number;
      pos?: { x: number; y: number };
    }
  | { type: "addClient" }
  | { type: "patchComponent"; id: string; patch: ComponentPatch }
  | { type: "renameComponent"; from: string; to: string }
  | { type: "deleteComponent"; id: string }
  | { type: "moveNode"; id: string; x: number; y: number }
  | { type: "resetLayout" }
  | { type: "connect"; from: string; to: string }
  | { type: "patchLink"; index: number; condition: string }
  | { type: "deleteLink"; index: number }
  | { type: "addFailure"; failure: Failure }
  | { type: "removeFailure"; index: number }
  | { type: "patchWorkload"; patch: Partial<WorkloadSpec> }
  | { type: "patchOptions"; patch: Partial<SimulationOptions> };

function editorReducer(s: EditorState, a: EditAction): EditorState {
  switch (a.type) {
    case "select":
      return { ...s, selected: a.id };

    case "renameArch":
      return {
        ...s,
        architecture: { ...s.architecture, name: a.name },
      };

    case "addComponent": {
      const id = freshId(s.architecture.components, a.service.toLowerCase());
      const comp = create(ComponentSpecSchema, {
        id,
        kind: a.kind,
        provider: a.provider,
        service: a.service,
      });
      return {
        ...s,
        architecture: {
          ...s.architecture,
          components: [...s.architecture.components, comp],
        },
        positions: a.pos ? { ...s.positions, [id]: a.pos } : s.positions,
        selected: id,
      };
    }

    case "addClient": {
      const id = freshId(s.architecture.components, "client");
      const comp = create(ComponentSpecSchema, { id, kind: ComponentKind.CLIENT });
      return {
        ...s,
        architecture: {
          ...s.architecture,
          components: [comp, ...s.architecture.components],
        },
        selected: id,
      };
    }

    case "patchComponent": {
      const components = s.architecture.components.map((c) => {
        if (c.id !== a.id) return c;
        const next = create(ComponentSpecSchema, c);
        const p = a.patch;
        if (p.provider !== undefined) next.provider = p.provider;
        if (p.service !== undefined) next.service = p.service;
        if (p.concurrency !== undefined) next.concurrency = p.concurrency;
        if (p.queueLimit !== undefined) next.queueLimit = p.queueLimit;
        if (p.capacityRps !== undefined) next.capacityRps = p.capacityRps;
        if (p.defaultServiceTimeMillis !== undefined)
          next.defaultServiceTimeMillis = p.defaultServiceTimeMillis;
        if (p.hitRatio !== undefined) next.hitRatio = p.hitRatio;
        if (p.fanOut !== undefined) next.fanOut = p.fanOut;
        if (p.config !== undefined) {
          if (p.config === null) {
            next.config = undefined;
          } else {
            const merged = create(ProviderConfigSchema, c.config);
            const partial = p.config;
            if (partial.concurrency !== undefined)
              merged.concurrency = partial.concurrency;
            if (partial.queueLimit !== undefined)
              merged.queueLimit = partial.queueLimit;
            if (partial.units !== undefined) merged.units = partial.units;
            if (partial.memoryMb !== undefined)
              merged.memoryMb = partial.memoryMb;
            if (partial.storageGb !== undefined)
              merged.storageGb = partial.storageGb;
            if (partial.hitRatio !== undefined)
              merged.hitRatio = partial.hitRatio;
            next.config = merged;
          }
        }
        return next;
      });
      return { ...s, architecture: { ...s.architecture, components } };
    }

    case "renameComponent": {
      const to = a.to.trim();
      if (
        !to ||
        to === a.from ||
        s.architecture.components.some((c) => c.id === to)
      ) {
        return s;
      }
      const components = s.architecture.components.map((c) =>
        c.id === a.from ? { ...c, id: to } : c,
      );
      const links = s.architecture.links.map((l) =>
        create(LinkSchema, {
          from: l.from === a.from ? to : l.from,
          to: l.to === a.from ? to : l.to,
        }),
      );
      const failures = s.failures.map((f) =>
        f.target === a.from ? { ...f, target: to } : f,
      );
      return {
        ...s,
        architecture: { ...s.architecture, components, links },
        failures,
        selected: s.selected === a.from ? to : s.selected,
      };
    }

    case "deleteComponent": {
      const components = s.architecture.components.filter((c) => c.id !== a.id);
      const links = s.architecture.links.filter(
        (l) => l.from !== a.id && l.to !== a.id,
      );
      const failures = s.failures.filter((f) => f.target !== a.id);
      return {
        ...s,
        architecture: { ...s.architecture, components, links },
        failures,
        selected: s.selected === a.id ? null : s.selected,
      };
    }

    case "moveNode":
      return {
        ...s,
        positions: { ...s.positions, [a.id]: { x: a.x, y: a.y } },
      };

    case "resetLayout":
      return { ...s, positions: {} };

    case "connect": {
      if (a.from === a.to) return s;
      if (
        s.architecture.links.some(
          (l) => l.from === a.from && l.to === a.to,
        )
      ) {
        return s;
      }
      return {
        ...s,
        architecture: {
          ...s.architecture,
          links: [
            ...s.architecture.links,
            create(LinkSchema, { from: a.from, to: a.to }),
          ],
        },
        selected: `link:${s.architecture.links.length}`,
      };
    }

    case "patchLink": {
      const links = s.architecture.links.map((l, i) =>
        i === a.index ? { ...l, condition: a.condition } : l,
      );
      return { ...s, architecture: { ...s.architecture, links } };
    }

    case "deleteLink": {
      const links = s.architecture.links.filter((_, i) => i !== a.index);
      return {
        ...s,
        architecture: { ...s.architecture, links },
        selected: s.selected === `link:${a.index}` ? null : s.selected,
      };
    }

    case "addFailure":
      return { ...s, failures: [...s.failures, a.failure] };

    case "removeFailure":
      return { ...s, failures: s.failures.filter((_, i) => i !== a.index) };

    case "patchWorkload":
      return { ...s, workload: { ...s.workload, ...a.patch } };

    case "patchOptions":
      return { ...s, options: { ...s.options, ...a.patch } };
  }
}

/* ---------------------------------------------------- demo architecture -- */

// The demonstration architecture, built on the AWS catalog: Client → Lambda
// → ElastiCache → RDS. The crash toggle reuses the same seed for a
// controlled baseline-vs-cascade comparison.
const INITIAL_ARCHITECTURE: Architecture = create(ArchitectureSchema, {
  schemaVersion: SCHEMA_VERSION,
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
    create(LinkSchema, { from: "client", to: "api" }),
    create(LinkSchema, { from: "api", to: "cache" }),
    create(LinkSchema, { from: "cache", to: "db" }),
  ],
});

const INITIAL_WORKLOAD: WorkloadSpec = create(WorkloadSpecSchema, {
  totalUsers: 10_000_000n,
  dau: 1_000_000n,
  requestsPerUserPerDay: 40,
  peakMultiplier: 5,
  readWriteRatio: 4,
  payloadBytes: 4096n,
});

const INITIAL_OPTIONS: SimulationOptions = create(SimulationOptionsSchema, {
  seed: 7n,
  durationMs: 10_000,
  maxRetries: 2,
  backoffBaseMs: 5,
  timeoutMs: 50,
  retryOn: ["api", "cache"],
});

/* --------------------------------------------------------- run reducer -- */

type Phase = "idle" | "running" | "done" | "error";

interface RunState {
  phase: Phase;
  simId: string | null;
  results: FinalResults | null;
  diagnosis: Diagnosis | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
  error: string;
}

const initialRun: RunState = {
  phase: "idle",
  simId: null,
  results: null,
  diagnosis: null,
  capacity: null,
  cost: null,
  error: "",
};

type RunAction =
  | { type: "reset" }
  | { type: "created"; id: string }
  | { type: "results"; results: FinalResults }
  | { type: "diagnosis"; diagnosis: Diagnosis }
  | { type: "capacity"; capacity: GetCapacityResponse }
  | { type: "cost"; cost: GetCostEstimateResponse }
  | { type: "error"; message: string };

function runReducer(s: RunState, a: RunAction): RunState {
  switch (a.type) {
    case "reset":
      return initialRun;
    case "created":
      return { ...s, simId: a.id };
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

/**
 * Run-state derivation for the ops strip. The console owns the live
 * RunStatus while a run executes (it sees the control frames); the page
 * tracks the terminal outcome for the comparison-ready banner.
 */
interface PageRunState {
  /** Live console status while a run is active. */
  active: boolean;
  /** Last terminal outcome of a console run. */
  outcome: SimOutcome | null;
}

function pageStatus(s: PageRunState): { state: "ok" | "warn" | "bad" | "idle"; label: string } {
  if (s.active) return { state: "warn", label: "running" };
  switch (s.outcome?.status) {
    case "completed":
      return { state: "ok", label: `complete · ${s.outcome.simId}` };
    case "stopped":
      return { state: "bad", label: `stopped · ${s.outcome.simId}` };
    default:
      return { state: "idle", label: "idle" };
  }
}

/* --------------------------------------------------------------- helpers -- */

/** All architecture validations the page can do locally (cheap, advisory). */
function validateArchitecture(arch: Architecture, failures: Failure[]): string[] {
  const issues: string[] = [];
  const ids = new Set(arch.components.map((c) => c.id));
  const clients = arch.components.filter((c) => c.kind === ComponentKind.CLIENT);
  if (clients.length === 0) issues.push("no client — the simulator needs exactly one");
  if (clients.length > 1) issues.push("multiple clients — the simulator needs exactly one");
  for (const l of arch.links) {
    if (!ids.has(l.from) || !ids.has(l.to)) {
      issues.push(`link ${l.from} → ${l.to} references a missing component`);
    }
  }
  for (const f of failures) {
    if (!ids.has(f.target)) {
      issues.push(`failure targets missing component "${f.target}"`);
    }
  }
  return issues;
}

/** kind → generic role label for canvas nodes. */
function kindRole(kind: number): string {
  switch (kind) {
    case ComponentKind.CLIENT:
      return "client";
    case ComponentKind.LOAD_BALANCER:
      return "load balancer";
    case ComponentKind.API_SERVER:
      return "api server";
    case ComponentKind.CACHE:
      return "cache";
    case ComponentKind.QUEUE:
      return "queue";
    case ComponentKind.WORKER:
      return "worker";
    case ComponentKind.DATABASE:
      return "database";
    case ComponentKind.OBJECT_STORAGE:
      return "object storage";
    case ComponentKind.NETWORK:
      return "network";
    default:
      return "component";
  }
}

/* --------------------------------------------------------------- shell -- */

export default function Home() {
  const [serverUrl, setServerUrl] = useState("http://localhost:8080");
  const [editor, dispatch] = useReducer(editorReducer, {
    architecture: INITIAL_ARCHITECTURE,
    workload: INITIAL_WORKLOAD,
    options: INITIAL_OPTIONS,      failures: [],
      positions: {},
      selected: null as string | null,
    });
  const [state, runDispatch] = useReducer(runReducer, initialRun);
  const [view, setView] = useState("architecture");
  const [catalog, setCatalog] = useState<CatalogService[] | null>(null);
  const [catalogError, setCatalogError] = useState("");
  /** Console-owned run state: active flag + last terminal outcome. */
  const [pageRun, setPageRun] = useState<PageRunState>({ active: false, outcome: null });

  const canvasHandles = useRef<CanvasHandles | null>(null);


  const client = useMemo(
    () => createLoadlineClient({ baseUrl: serverUrl }),
    [serverUrl],
  );

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

  /* ------------------------------------------------------ editor intents -- */

  const onSelect = useCallback(
    (id: string | null) => dispatch({ type: "select", id }),
    [],
  );

  const onMoveNode = useCallback(
    (id: string, x: number, y: number) => dispatch({ type: "moveNode", id, x, y }),
    [],
  );

  const onResetLayout = useCallback(() => dispatch({ type: "resetLayout" }), []);

  const onConnect = useCallback(
    (from: string, to: string) => dispatch({ type: "connect", from, to }),
    [],
  );

  const onDropService = useCallback(
    (provider: string, service: string, x: number, y: number) => {
      const svc = catalog?.find(
        (s) => s.provider === provider && s.service === service,
      );
      if (!svc) return;
      dispatch({
        type: "addComponent",
        provider,
        service,
        kind: kindFromProtoName(svc.componentKind),
        pos: { x, y },
      });
    },
    [catalog],
  );

  const onAddService = useCallback(
    (provider: string, service: string) => {
      const svc = catalog?.find(
        (s) => s.provider === provider && s.service === service,
      );
      if (!svc) return;
      dispatch({
        type: "addComponent",
        provider,
        service,
        kind: kindFromProtoName(svc.componentKind),
      });
    },
    [catalog],
  );

  const onPatchComponent = useCallback(
    (id: string, patch: ComponentPatch) =>
      dispatch({ type: "patchComponent", id, patch }),
    [],
  );

  const onRenameComponent = useCallback(
    (from: string, to: string) => dispatch({ type: "renameComponent", from, to }),
    [],
  );

  const onDeleteComponent = useCallback(
    (id: string) => dispatch({ type: "deleteComponent", id }),
    [],
  );

  const onPatchLink = useCallback(
    (index: number, condition: string) =>
      dispatch({ type: "patchLink", index, condition }),
    [],
  );

  const onDeleteLink = useCallback(
    (index: number) => dispatch({ type: "deleteLink", index }),
    [],
  );

  const onAddFailure = useCallback(
    (failure: Failure) => dispatch({ type: "addFailure", failure }),
    [],
  );

  const onRemoveFailure = useCallback(
    (index: number) => dispatch({ type: "removeFailure", index }),
    [],
  );

  const onPatchWorkload = useCallback(
    (patch: Partial<WorkloadSpec>) => dispatch({ type: "patchWorkload", patch }),
    [],
  );

  const onPatchOptions = useCallback(
    (patch: Partial<SimulationOptions>) =>
      dispatch({ type: "patchOptions", patch }),
    [],
  );

  const onRenameArchitecture = useCallback(
    (name: string) => dispatch({ type: "renameArch", name }),
    [],
  );

  const onFocusComponent = useCallback((id: string) => {
    dispatch({ type: "select", id });
  }, []);

  /* ------------------------------------------------------------- running -- */

  const baselineIdRef = useRef<string | null>(null);

  /**
   * Console finished a run. The console owns live control + results
   * display; the page then fetches the analysis artifacts (diagnosis,
   * capacity, cost) for the terminal run and tracks the healthy-run
   * baseline for diagnosis comparison.
   */
  const onConsoleOutcome = useCallback(
    (outcome: SimOutcome) => {
      runDispatch({ type: "reset" });
      const id = outcome.simId;
      const healthy = editor.failures.length === 0;
      if (healthy) baselineIdRef.current = id;

      (async () => {
        try {
          const res = await client.getResults({ simulationId: id });
          runDispatch({
            type: "results",
            results: create(FinalResultsSchema, {
              plan: res.plan,
              metrics: res.metrics,
              failures: res.failures,
              summary: res.summary,
            }),
          });
          const diag = await client.getDiagnosis({
            simulationId: id,
            baselineSimulationId: healthy ? "" : (baselineIdRef.current ?? ""),
          });
          if (diag.diagnosis) {
            runDispatch({ type: "diagnosis", diagnosis: diag.diagnosis });
          }
          const cap = await client.getCapacity({ simulationId: id });
          runDispatch({ type: "capacity", capacity: cap });
          const cost = await client.getCostEstimate({ simulationId: id });
          runDispatch({ type: "cost", cost });
        } catch (e) {
          // Analysis artifacts are best-effort: a stopped run or a
          // generic (provider-less) architecture legitimately has no
          // capacity/cost report. Surface the message, keep the results.
          runDispatch({
            type: "error",
            message: e instanceof Error ? e.message : String(e),
          });
        }
      })();
    },
    [client, editor.failures.length],
  );

  const status = pageStatus(pageRun);
  const m = state.results?.metrics;

  /* --------------------------------------------------- canvas projections -- */

  const canvasNodes = useMemo(
    () =>
      editor.architecture.components.map((c) => {
        const hasProvider = Boolean(c.provider && c.service);
        const metric = m?.components.find((x) => x.id === c.id);
        return {
          id: c.id,
          sub: hasProvider ? `${c.provider}/${c.service}` : kindRole(c.kind),
          role: kindRole(c.kind),
          faulted: editor.failures.some((f) => f.target === c.id),
          pos: editor.positions[c.id],
          metrics: metric
            ? {
                rps: metric.arrivalRps,
                util: metric.utilization,
              }
            : undefined,
        };
      }),
    [editor.architecture.components, editor.failures, editor.positions, m],
  );

  const canvasEdges: { from: string; to: string; label?: string }[] =
    editor.architecture.links.map((l) => ({
      from: l.from,
      to: l.to,
      label: l.condition || undefined,
    }));

  const metricsByComponent = useMemo(() => {
    const map = new Map<string, { rps?: number; util?: number }>();
    for (const c of m?.components ?? []) {
      map.set(c.id, { rps: c.arrivalRps, util: c.utilization });
    }
    return map;
  }, [m]);

  const faultedByComponent = useMemo(() => {
    const set = new Set<string>();
    for (const f of editor.failures) set.add(f.target);
    return set;
  }, [editor.failures]);

  const warnings = useMemo(
    () => validateArchitecture(editor.architecture, editor.failures),
    [editor.architecture, editor.failures],
  );

  /* --------------------------------------------------------------- views -- */

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
          <Button
            variant="primary"
            disabled={pageRun.active || warnings.length > 0}
            onClick={() => {
              setView("simulation");
              setPageRun({ active: false, outcome: null });
              runDispatch({ type: "reset" });
            }}
          >
            {pageRun.active ? "running…" : "new run"}
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
              components={editor.architecture.components}
              selected={editor.selected}
              onSelect={onSelect}
              onDeleteComponent={onDeleteComponent}
            />
            <div className="pal-head">Canvas</div>
            <div className="pal-actions">
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={() => dispatch({ type: "addClient" })}
              >
                + client
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={() => canvasHandles.current?.fit()}
              >
                fit view
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={() => canvasHandles.current?.reset()}
              >
                reset view
              </button>
            </div>
          </aside>

          {/* ------------------------------------------------------ canvas */}
          <section className="center">
            <div className="center-head">
              <span className="panel-title">architecture</span>
              <span className="center-tag">
                {editor.architecture.components.length} components ·{" "}
                {editor.architecture.links.length} links · schema v
                {editor.architecture.schemaVersion}
                {state.simId ? ` · sim ${state.simId}` : ""}
              </span>
            </div>
            <ArchitectureCanvas
              nodes={canvasNodes}
              edges={canvasEdges}
              selected={editor.selected}
              metricsByComponent={metricsByComponent}
              faultedByComponent={faultedByComponent}
              onSelect={onSelect}
              onMoveNode={onMoveNode}
              onResetLayout={onResetLayout}
              onConnect={onConnect}
              onDeleteNode={onDeleteComponent}
              onDeleteEdge={onDeleteLink}
              onDropService={onDropService}
              handleRef={canvasHandles}
            />
            {state.error && <p className="error-line">error: {state.error}</p>}
            {warnings.length > 0 && (
              <p className="error-line">
                {warnings.length} validation issue(s) — see the inspector
              </p>
            )}
          </section>

          {/* -------------------------------------------------- inspector */}
          <aside className="right-rail">
            <div className="rail-head">
              <span className="panel-title">inspector</span>
            </div>
            <Inspector
              selected={editor.selected}
              catalog={catalog}
              components={editor.architecture.components}
              links={editor.architecture.links}
              failures={editor.failures}
              workload={editor.workload}
              options={editor.options}
              architectureName={editor.architecture.name}
              warnings={warnings}
              results={state.results}
              capacity={state.capacity}
              cost={state.cost}
              onPatchComponent={onPatchComponent}
              onRenameComponent={onRenameComponent}
              onDeleteComponent={onDeleteComponent}
              onPatchLink={onPatchLink}
              onDeleteLink={onDeleteLink}
              onAddFailure={onAddFailure}
              onRemoveFailure={onRemoveFailure}
              onPatchWorkload={onPatchWorkload}
              onPatchOptions={onPatchOptions}
              onRenameArchitecture={onRenameArchitecture}
              onAddService={onAddService}
              onFocusComponent={onFocusComponent}
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
            <span className="sim-toolbar-note">
              edits in the architecture view define what runs · the console executes them
            </span>
          </div>

          <SimConsole
            architecture={editor.architecture}
            workload={editor.workload}
            options={
              editor.failures.length > 0
                ? { ...editor.options, failures: editor.failures }
                : editor.options
            }
            serverUrl={serverUrl}
            running={pageRun.active}
            onRunningChange={(v) => setPageRun((p) => ({ ...p, active: v }))}
            onOutcome={(o) => {
              setPageRun((p) => ({ ...p, outcome: o }));
              onConsoleOutcome(o);
            }}
          />

          {state.results && m && (
            <div className="sim-analysis">
              <div className="sim-first">
                <Panel
                  title="Results"
                  tag={
                    state.simId
                      ? `${state.simId} · stop reason ${state.results.summary?.stopReason ?? "n/a"}`
                      : `stop reason ${state.results.summary?.stopReason ?? "n/a"}`
                  }
                >
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

                <Panel title="Bottleneck" tag="most severe first · simulator-computed">
                  <BottleneckPanel diagnosis={state.diagnosis} metrics={m} />
                </Panel>
              </div>

              <div className="sim-columns">
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

              <Panel title="Architecture status">
                <HealthPanel
                  diagnosis={state.diagnosis}
                  reports={state.capacity?.reports ?? []}
                  metrics={m}
                  timeoutMs={editor.options.timeoutMs}
                  failureTargets={
                    editor.failures.length > 0
                      ? [...new Set(editor.failures.map((f) => f.target))]
                      : []
                  }
                />
              </Panel>

              <Panel title="Capacity" tag="from simulation outputs + provider models">
                <CapacityPanel reports={state.capacity?.reports ?? []} />
              </Panel>

              <Panel title="Cost">
                <CostPanel estimate={state.cost?.estimate ?? null} />
              </Panel>
              </div>
            </div>
          )}

          <div className="sim-inputs">
            <Panel title="Workload" tag="inputs + backend-derived plan">
              <WorkloadPanel
                workload={editor.workload}
                onPatch={onPatchWorkload}
                plan={state.results?.plan ?? null}
              />
            </Panel>

            <Panel title="Failure injection" tag="applies to the next run">
              <FailurePanel
                components={editor.architecture.components
                  .filter((c) => c.kind !== ComponentKind.CLIENT)
                  .map((c) => ({
                    id: c.id,
                    label: c.provider ? `${c.id} · ${c.provider}/${c.service}` : c.id,
                  }))}
                failures={editor.failures}
                onAdd={onAddFailure}
                onRemove={onRemoveFailure}
              />
            </Panel>
          </div>
        </div>
      )}

      {/* ------------------------------------------------------ ops strip */}
      <footer className="ops-strip">
        <div className="ops-scroll">
          {m ? (
            <>
              <span className="ops-chip">
                <span className="ops-chip-id">completed</span>
                <span className="ops-chip-val">{f0(m.completed)}</span>
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
                <span className="ops-chip-id">window</span>
                <span className="ops-chip-val">{f1(m.durationMs / 1000)}s</span>
              </span>
            </>
          ) : (
            <span className="ops-empty">
              no simulation data — run to populate the operations strip
            </span>
          )}
        </div>
        <div className="ops-spacer" />
        <StatusIndicator
          state={status.state}
          label={state.simId ? `sim ${state.simId}` : status.label}
        />
      </footer>
    </div>
  );
}

/** proto enum name → numeric ComponentKind for drop → node creation. */
function kindFromProtoName(name: string): number {
  const table: Record<string, number> = {
    COMPONENT_KIND_CLIENT: ComponentKind.CLIENT,
    COMPONENT_KIND_LOAD_BALANCER: ComponentKind.LOAD_BALANCER,
    COMPONENT_KIND_API_SERVER: ComponentKind.API_SERVER,
    COMPONENT_KIND_CACHE: ComponentKind.CACHE,
    COMPONENT_KIND_QUEUE: ComponentKind.QUEUE,
    COMPONENT_KIND_WORKER: ComponentKind.WORKER,
    COMPONENT_KIND_DATABASE: ComponentKind.DATABASE,
    COMPONENT_KIND_OBJECT_STORAGE: ComponentKind.OBJECT_STORAGE,
    COMPONENT_KIND_NETWORK: ComponentKind.NETWORK,
  };
  return table[name] ?? 0;
}
