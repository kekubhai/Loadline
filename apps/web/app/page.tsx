"use client";

/**
 * LOADLINE — operational workstation shell.
 *
 * Layout: top nav (identity + views + run control), left palette
 * (provider catalog + architecture components), center canvas, right
 * inspector, bottom operations strip (live progress + system metrics).
 *
 * The CANONICAL editor state — one copy of
 * {architecture, workload, options} — lives in ./editor (reducer +
 * initial demo architecture + local validation); the run-outcome state
 * lives in ./runstate. This file owns backend I/O and layout only:
 * every panel renders from that state and edits it through dispatched
 * actions. Panels never keep their own copy of the architecture. Every
 * displayed number comes from the simulation service; nothing is
 * computed or faked here.
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
  createLoadlineClient,
} from "@loadline/api";
import type {
  Architecture,
  CatalogService,
  Failure,
  SimulationOptions,
  WorkloadSpec,
} from "@loadline/api";
import type { ComponentPatch } from "../components/inspector";
import { ArchitectureCanvas } from "../components/canvas";
import type { CanvasHandles } from "../components/canvas";
import { Inspector } from "../components/inspector";
import { Palette } from "../components/palette";
import {
  CommandPalette,
  type Command,
} from "../components/commandpalette";
import { SimConsole } from "../components/simconsole";
import type { SimConsoleHandles, SimOutcome } from "../components/simconsole";
import { WorkloadPanel } from "../components/workloadpanel";
import { FailurePanel } from "../components/failurepanel";
import { ComparisonView } from "../components/comparisonview";
import { WorkspaceView } from "../components/workspaceview";
import { ArenaView } from "../components/arenaview";
import { ExploreGallery } from "../components/exploregallery";
import {
  BottleneckPanel,
  CapacityPanel,
  CostPanel,
  HealthPanel,
} from "../components/analysis";
import {
  FailureTimeline,
  LatencyProfile,
  LoadVsCapacity,
  OutcomeMix,
} from "../components/charts";
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
import {
  initialEditorState,
  validateArchitecture,
  kindRole,
  kindFromProtoName,
} from "./editor";
import type { EditAction, EditorState } from "./editor";
import { canRedo, canUndo, historyOf, historyReducer } from "./history";
import {
  decodeShareHash,
  encodeShareHash,
  exportDocument,
  parseDocument,
  serializeDocument,
} from "./documentio";
import { TEMPLATES, templateById, templateState } from "./templates";
import {
  EXPLORE_CATEGORIES,
  exploreState,
  exploreSystemById,
} from "./explore";
import type { ExploreSystem } from "./explore";
import {
  runReducer,
  initialRun,
  pageStatus,
  fetchOutcomeArtifacts,
} from "./runstate";
import type { PageRunState } from "./runstate";

/* --------------------------------------------------------- editor intents -- */

export default function Home() {
  const [serverUrl, setServerUrl] = useState("http://localhost:8080");
  /**
   * The editor document with undo/redo. `dispatch` is the edit path all
   * panels use; history is a layer around it, not a second source of
   * truth: `editor` is always `hist.present`.
   */
  const [hist, dispatchHist] = useReducer(
    historyReducer,
    initialEditorState,
    historyOf,
  );
  const editor = hist.present;
  const dispatch = useCallback(
    (action: EditAction) => dispatchHist({ type: "edit", action }),
    [],
  );
  const undo = useCallback(() => dispatchHist({ type: "undo" }), []);
  const redo = useCallback(() => dispatchHist({ type: "redo" }), []);
  const loadState = useCallback(
    (state: EditorState) => dispatchHist({ type: "load", state }),
    [],
  );
  /**
   * Load part of a saved document into the editor (the workspace view's
   * "load" actions). Loading replaces the indicated part and clears the
   * canvas layout, so the loaded architecture is laid out fresh rather
   * than inheriting positions from whatever was open before. It goes
   * through the history reducer, so a load is as undoable as any edit.
   */
  const loadDocument = useCallback(
    (doc: { architecture?: Architecture; workload?: WorkloadSpec }) =>
      loadState({
        ...editor,
        ...(doc.architecture
          ? { architecture: doc.architecture, positions: {}, selected: null }
          : {}),
        ...(doc.workload ? { workload: doc.workload } : {}),
      }),
    [editor, loadState],
  );
  const [state, runDispatch] = useReducer(runReducer, initialRun);
  const [view, setView] = useState("architecture");
  const [catalog, setCatalog] = useState<CatalogService[] | null>(null);
  const [catalogError, setCatalogError] = useState("");
  /** Command palette (⌘/Ctrl+K). */
  const [paletteOpen, setPaletteOpen] = useState(false);
  /** Console-owned run state: active flag + last terminal outcome. */
  const [pageRun, setPageRun] = useState<PageRunState>({ active: false, outcome: null });

  const canvasHandles = useRef<CanvasHandles | null>(null);
  const consoleHandles = useRef<SimConsoleHandles | null>(null);

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

  const onDuplicateComponent = useCallback(
    (id: string) => dispatch({ type: "duplicateComponent", id }),
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

  /* ------------------------------------------------------- document I/O -- */

  /** One-shot status line for import/share actions (transient). */
  const [notice, setNotice] = useState("");
  const fileInput = useRef<HTMLInputElement | null>(null);

  const onExport = useCallback(() => {
    const doc = exportDocument(editor);
    const blob = new Blob([serializeDocument(doc)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = window.document.createElement("a");
    a.href = url;
    a.download = `${editor.architecture.name || "architecture"}.loadline.json`;
    a.click();
    URL.revokeObjectURL(url);
    setNotice("architecture exported as JSON");
  }, [editor]);

  const onImportFile = useCallback(
    async (file: File) => {
      try {
        const text = await file.text();
        const parsed = parseDocument(text);
        loadState({
          ...editor,
          architecture: parsed.architecture,
          positions: parsed.positions,
          workload: parsed.workload ?? editor.workload,
          options: parsed.options ?? editor.options,
          failures: parsed.failures,
          selected: null,
        });
        setNotice(`imported ${parsed.architecture.name || file.name}`);
      } catch (e) {
        setNotice(`import failed — ${e instanceof Error ? e.message : String(e)}`);
      }
    },
    [editor, loadState],
  );

  const onShare = useCallback(async () => {
    const hash = encodeShareHash(exportDocument(editor));
    const url = `${window.location.origin}${window.location.pathname}${hash}`;
    try {
      await navigator.clipboard.writeText(url);
      setNotice("share link copied — it contains the whole architecture");
    } catch {
      window.location.hash = hash.slice(1);
      setNotice("share link put in the address bar (clipboard unavailable)");
    }
  }, [editor]);

  const onTemplate = useCallback(
    (id: string) => {
      const t = templateById(id);
      if (!t) return;
      loadState(templateState(t, editor));
      setNotice(`loaded template: ${t.name}`);
    },
    [editor, loadState],
  );

  /** Gallery-loaded system, if the current architecture is one of them. */
  const activeExploreSystemId = useMemo(() => {
    for (const c of EXPLORE_CATEGORIES) {
      for (const s of c.systems) {
        if (s.architecture === editor.architecture) return s.id;
      }
    }
    return null;
  }, [editor.architecture]);

  const onExploreSystem = useCallback(
    (s: ExploreSystem) => {
      loadState(exploreState(s, editor));
      setNotice(`loaded system: ${s.name} — raise the load, inject a failure, read the diagnosis`);
    },
    [editor, loadState],
  );

  // A shared link opens as the architecture it encodes.
  useEffect(() => {
    const doc = decodeShareHash(window.location.hash);
    if (!doc) return;
    try {
      const parsed = parseDocument(JSON.stringify(doc));
      loadState({
        ...initialEditorState,
        architecture: parsed.architecture,
        positions: parsed.positions,
        workload: parsed.workload ?? initialEditorState.workload,
        options: parsed.options ?? initialEditorState.options,
        failures: parsed.failures,
      });
      setNotice("loaded architecture from the share link");
    } catch (e) {
      setNotice(`share link rejected — ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [loadState]);

  useEffect(() => {
    if (!notice) return;
    const t = window.setTimeout(() => setNotice(""), 6000);
    return () => window.clearTimeout(t);
  }, [notice]);

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
      // Drop ALL of the previous run's analysis before fetching the new
      // run's artifacts: results otherwise lingered under a new sim id
      // during the (async) fetch window and after artifact failures.
      runDispatch({ type: "reset" });
      if (outcome.status === "failed") {
        // A failed run has no results and must not serve as anyone's
        // diagnosis baseline.
        baselineIdRef.current = null;
        return;
      }
      const id = outcome.simId;
      const healthy = editor.failures.length === 0;
      if (healthy) baselineIdRef.current = id;

      void fetchOutcomeArtifacts(outcome, {
        client,
        healthy,
        baselineId: baselineIdRef.current,
        dispatch: runDispatch,
      });
    },
    [client, editor.failures.length],
  );

  const status = pageStatus(pageRun);
  const m = state.results?.metrics;

  /* --------------------------------------------------- canvas projections -- */

  /**
   * Diagnosis severity per component. Bottlenecks arrive most-severe-first,
   * so the first entry for an id wins; used for canvas highlighting.
   */
  const severityByComponent = useMemo(() => {
    const map = new Map<string, string>();
    for (const b of state.diagnosis?.bottlenecks ?? []) {
      if (!b.componentId || map.has(b.componentId)) continue;
      map.set(b.componentId, b.severity || "moderate");
    }
    return map;
  }, [state.diagnosis]);

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
          severity: metric ? severityByComponent.get(c.id) : undefined,
          pos: editor.positions[c.id],
          metrics: metric
            ? {
                rps: metric.arrivalRps,
                util: metric.utilization,
                queue: metric.maxQueueDepth,
                headroom:
                  metric.capacityRps > 0
                    ? Math.max(0, 1 - metric.arrivalRps / metric.capacityRps)
                    : undefined,
              }
            : undefined,
        };
      }),
    [
      editor.architecture.components,
      editor.failures,
      editor.positions,
      m,
      severityByComponent,
    ],
  );

  const canvasEdges: { from: string; to: string; label?: string }[] =
    editor.architecture.links.map((l) => ({
      from: l.from,
      to: l.to,
      label: l.condition || undefined,
    }));

  const metricsByComponent = useMemo(() => {
    const map = new Map<
      string,
      { rps?: number; util?: number; queue?: number; headroom?: number }
    >();
    for (const c of m?.components ?? []) {
      map.set(c.id, {
        rps: c.arrivalRps,
        util: c.utilization,
        queue: c.maxQueueDepth,
        headroom:
          c.capacityRps > 0
            ? Math.max(0, 1 - c.arrivalRps / c.capacityRps)
            : undefined,
      });
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

  /* ------------------------------------------------------- shortcuts -- */

  /**
   * Start a run from anywhere (keyboard, palette). The console only exists
   * on the simulation view, so the view switch happens first and the run
   * request follows after React has mounted it.
   */
  const runSimulation = useCallback(() => {
    if (warnings.length > 0 || consoleHandles.current?.isBusy()) return;
    setView("simulation");
    window.setTimeout(() => consoleHandles.current?.run(), 0);
  }, [warnings.length]);

  const commands = useMemo<Command[]>(() => {
    const cmd = (
      id: string,
      group: string,
      label: string,
      hint: string | undefined,
      run: () => void,
    ): Command => ({ id, group, label, hint, run });
    const list: Command[] = [
      cmd("view-arch", "view", "Go to architecture", undefined, () =>
        setView("architecture"),
      ),
      cmd("view-sim", "view", "Go to simulation", undefined, () =>
        setView("simulation"),
      ),
      cmd("view-cmp", "view", "Go to comparison", undefined, () =>
        setView("comparison"),
      ),
      cmd("run", "simulation", "Run simulation", "R", runSimulation),
      cmd(
        "stop",
        "simulation",
        "Stop the active run",
        undefined,
        () => consoleHandles.current?.stop(),
      ),
      cmd("fit", "canvas", "Fit view", "F", () => canvasHandles.current?.fit()),
      cmd(
        "reset-view",
        "canvas",
        "Reset layout and view",
        undefined,
        () => canvasHandles.current?.reset(),
      ),
      cmd("undo", "edit", "Undo", "⌘Z", undo),
      cmd("redo", "edit", "Redo", "⇧⌘Z", redo),
      cmd(
        "add-client",
        "edit",
        "Add client / users node",
        undefined,
        () => dispatch({ type: "addClient" }),
      ),
      cmd("export", "document", "Export architecture as JSON", "⌘S", onExport),
      cmd("import", "document", "Import architecture from file", undefined, () =>
        fileInput.current?.click(),
      ),
      cmd(
        "share",
        "document",
        "Copy share link",
        undefined,
        () => void onShare(),
      ),
      ...TEMPLATES.map((t) =>
        cmd(
          `template-${t.id}`,
          "template",
          `Start from ${t.name}`,
          undefined,
          () => onTemplate(t.id),
        ),
      ),
      ...EXPLORE_CATEGORIES.flatMap((c) =>
        c.systems.map((s) =>
          cmd(
            `explore-${s.id}`,
            "explore",
            `Explore ${s.name}`,
            undefined,
            () => onExploreSystem(s),
          ),
        ),
      ),
    ];
    return list;
  }, [onExploreSystem, onExport, onShare, onTemplate, redo, runSimulation, undo]);

  /**
   * Global keyboard layer. Typing in a field never triggers an editor
   * action; modifiers are reserved for the app (⌘/Ctrl+K palette,
   * ⌘/Ctrl+Z/Y history, ⌘/Ctrl+S export) so browser defaults stay
   * intact for everything else.
   */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      const tag = t?.tagName;
      const typing =
        !!t &&
        (tag === "INPUT" ||
          tag === "TEXTAREA" ||
          tag === "SELECT" ||
          t.isContentEditable);
      const mod = e.metaKey || e.ctrlKey;
      const key = e.key.toLowerCase();

      if (mod && key === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
        return;
      }
      if (mod && key === "z") {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
        return;
      }
      if (mod && key === "y") {
        e.preventDefault();
        redo();
        return;
      }
      if (mod && key === "s") {
        e.preventDefault();
        onExport();
        return;
      }
      if (typing) return;

      if (e.key === "Escape") {
        // Escape clears selection/overlay only. It deliberately does not
        // stop a run: a stray keypress must not discard a simulation.
        if (paletteOpen) setPaletteOpen(false);
        else if (editor.selected) dispatch({ type: "select", id: null });
        return;
      }
      if (e.key === "Delete" || e.key === "Backspace") {
        const sel = editor.selected;
        if (!sel) return;
        e.preventDefault();
        if (sel.startsWith("link:")) {
          const index = Number(sel.slice(5));
          if (Number.isInteger(index)) dispatch({ type: "deleteLink", index });
        } else {
          dispatch({ type: "deleteComponent", id: sel });
        }
        return;
      }
      if (e.key === " ") {
        if (!consoleHandles.current?.isBusy()) return;
        e.preventDefault();
        consoleHandles.current.togglePause();
        return;
      }
      if (key === "f") {
        canvasHandles.current?.fit();
        return;
      }
      if (key === "r") {
        runSimulation();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dispatch, editor.selected, onExport, paletteOpen, redo, runSimulation, undo]);

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
          {["architecture", "simulation", "comparison", "workspace", "arena"].map((v) => (
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

      {catalogError && (
        <div className="banner banner-bad" role="status">
          <span className="banner-mark">!</span>
          <span>
            cannot reach the simulation server at <code>{serverUrl}</code> — start
            it with <code>go run ./cmd/loadline-server</code> from
            <code> apps/simulator</code>, or correct the server URL in the
            simulation tab. The catalog and every run depend on it.
          </span>
        </div>
      )}

      {notice && (
        <div className="banner" role="status">
          <span className="banner-mark">›</span>
          <span>{notice}</span>
        </div>
      )}

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
              onAddService={onAddService}
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

            <div className="pal-head">Document</div>
            <div className="pal-actions">
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={undo}
                disabled={!canUndo(hist)}
                title="undo (Ctrl+Z)"
              >
                undo
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={redo}
                disabled={!canRedo(hist)}
                title="redo (Ctrl+Shift+Z)"
              >
                redo
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={onExport}
                title="download the architecture as JSON (Ctrl+S)"
              >
                export json
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={() => fileInput.current?.click()}
                title="load an architecture file"
              >
                import json
              </button>
              <button
                type="button"
                className="btn btn-default pal-action"
                onClick={() => void onShare()}
                title="copy a link that contains this architecture"
              >
                copy share link
              </button>
            </div>
            <input
              ref={fileInput}
              type="file"
              accept="application/json,.json"
              className="visually-hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void onImportFile(f);
                e.target.value = "";
              }}
            />

            <div className="pal-head">Start from</div>
            <div className="tpl-list">
              {TEMPLATES.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  className={`tpl-row${editor.architecture.name === t.architecture.name ? " active" : ""}`}
                  title={t.description}
                  onClick={() => onTemplate(t.id)}
                >
                  <span className="tpl-name">{t.name}</span>
                  <span className="tpl-note">
                    {t.architecture.components.length} nodes
                  </span>
                </button>
              ))}
            </div>
            <p className="pal-note">
              ⌘K commands · ⌘Z undo · R run · F fit · Del remove
            </p>

            <div className="pal-head">Explore systems</div>
            <ExploreGallery
              activeSystemId={activeExploreSystemId}
              onSelect={onExploreSystem}
            />
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
              onDuplicateNode={onDuplicateComponent}
              onDropService={onDropService}
              handleRef={canvasHandles}
            />
            {state.error && <p className="error-line">error: {state.error}</p>}
            {warnings.length > 0 && (
              <div className="issues" role="alert">
                <div className="issues-head">
                  {warnings.length} issue{warnings.length === 1 ? "" : "s"} to fix
                  before this architecture can run
                </div>
                <ul className="issues-list">
                  {warnings.map((w, i) => (
                    <li key={i}>{w}</li>
                  ))}
                </ul>
              </div>
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
      ) : view === "comparison" ? (
        /* -------------------------------------------- comparison view */
        <div className="sim-view">
          <div className="sim-toolbar">
            <Field label="server">
              <Input value={serverUrl} onChange={setServerUrl} width={220} />
            </Field>
            <span className="sim-toolbar-note">
              save architectures, then compare them under the exact same
              workload, seed, and failure scenario
            </span>
          </div>
          <ComparisonView
            architecture={editor.architecture}
            workload={editor.workload}
            options={
              editor.failures.length > 0
                ? { ...editor.options, failures: editor.failures }
                : editor.options
            }
            runComparison={async ({ workload, architectures, options }) => {
              const res = await client.runComparison({
                workload,
                options,
                architectures: architectures.map((a) => ({
                  name: a.name,
                  architecture: a.architecture,
                })),
              });
              if (!res.result) throw new Error("backend returned no comparison result");
              return res.result;
            }}
          />
        </div>
      ) : view === "arena" ? (
        /* ------------------------------------------------ arena view */
        <div className="sim-view">
          <div className="sim-toolbar">
            <Field label="server">
              <Input value={serverUrl} onChange={setServerUrl} width={220} />
            </Field>
            <span className="sim-toolbar-note">
              standardized benchmarks: the server supplies the workload, runs the
              simulation, computes the score, and keeps the leaderboard
            </span>
          </div>
          <ArenaView
            serverUrl={serverUrl}
            onLoadWorkload={(w) => onPatchWorkload(w)}
            onNotice={setNotice}
          />
        </div>
      ) : view === "workspace" ? (
        /* ---------------------------------------------- workspace view */
        <div className="sim-view">
          <div className="sim-toolbar">
            <Field label="server">
              <Input value={serverUrl} onChange={setServerUrl} width={220} />
            </Field>
            <span className="sim-toolbar-note">
              projects, architectures, versions, workloads and runs live in
              postgresql · saving always appends, so a stored run stays
              reproducible
            </span>
          </div>
          <WorkspaceView
            serverUrl={serverUrl}
            architecture={editor.architecture}
            workload={editor.workload}
            options={editor.options}
            failures={editor.failures}
            onLoadDocument={loadDocument}
            onNotice={setNotice}
          />
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
            onSimId={(id) => runDispatch({ type: "created", id: id ?? "" })}
            onOutcome={(o) => {
              setPageRun((p) => ({ ...p, outcome: o }));
              onConsoleOutcome(o);
            }}
            handleRef={consoleHandles}
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

              {/* Charts: scaled views of the same backend numbers. */}
              <div className="sim-charts">
                <Panel
                  title="Latency profile"
                  tag="percentiles vs the run's timeout budget"
                >
                  <LatencyProfile metrics={m} budgetMs={editor.options.timeoutMs} />
                </Panel>

                <Panel title="Load vs capacity" tag="arrival rps against modeled ceilings">
                  <LoadVsCapacity components={m.components} />
                </Panel>

                <Panel title="Outcomes" tag="per component · arrived split by termination">
                  <OutcomeMix components={m.components} />
                </Panel>

                <Panel title="Failure timeline" tag="request-level records from the simulator">
                  <FailureTimeline
                    failures={state.results?.failures ?? []}
                    durationMs={m.durationMs}
                  />
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

          {!state.results && !pageRun.active && (
            <Panel
              title="No run yet"
              tag="the console executes what the architecture view defines"
            >
              <ol className="steps">
                <li>
                  <b>build</b> — drag services from the palette onto the canvas,
                  then drag from a node&apos;s dot to connect them.
                </li>
                <li>
                  <b>configure</b> — set the workload (users → peak rps) and the
                  options below; the inspector edits whatever is selected.
                </li>
                <li>
                  <b>run</b> — press <b>run</b> in the console: progress streams
                  live from the engine and never affects the results.
                </li>
                <li>
                  <b>read</b> — bottleneck, latency profile, capacity headroom,
                  and estimated monthly cost are simulator output, not guesses.
                </li>
                <li>
                  <b>break it</b> — inject a failure above and re-run to watch
                  the cascade, then compare architectures side by side.
                </li>
              </ol>
            </Panel>
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

      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        commands={commands}
      />
    </div>
  );
}
