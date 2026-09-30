"use client";

/**
 * LOADLINE — workspace view (persistence).
 *
 * The saved half of the product: what the user BUILDS and KEEPS, as
 * opposed to the ephemeral what-if runs the simulation console performs.
 *
 *   Project → Architecture → Version → Workload → Run → result
 *
 * Everything shown here is fetched from PostgreSQL through the Go
 * WorkspaceService. Creating a run calls the same in-memory engine the
 * console uses and stores what it returned, so a stored run replays
 * exactly (same architecture version + workload + seed). Nothing on this
 * screen computes a metric: the headline numbers, bottlenecks, capacity
 * ceilings, and cost lines are the engine's own output as it was stored.
 */
import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import { createWorkspaceClient, RunStatus } from "@loadline/api";
import type {
  Architecture,
  ArchitectureVersion,
  Failure,
  SimulationOptions,
  SimulationRunRecord,
  SimulationRunResult,
  WorkloadRecord,
  WorkloadSpec,
} from "@loadline/api";
import {
  canExecute,
  comparedResults,
  detailResult,
  detailRun,
  formatTimestamp,
  initialWorkspaceState,
  nameError,
  runLabel,
  runProblem,
  runStatusDisplay,
  selectedArchitecture,
  selectedProject,
  selectedVersion,
  selectedWorkload,
  shortId,
  storedRunsToEntries,
  versionLabel,
  workspaceReducer,
} from "../app/workspace";
import { comparisonRows } from "../app/comparison";
import {
  BottleneckPanel,
  CapacityPanel,
  CostPanel,
} from "./analysis";
import { f0, f1, f2, pct, price } from "./format";
import {
  Badge,
  Button,
  EmptyState,
  Field,
  Input,
  Panel,
  Section,
} from "./ui";

export function WorkspaceView({
  serverUrl,
  architecture,
  workload,
  options,
  failures,
  onLoadDocument,
  onNotice,
}: {
  /** Base URL of the Go server (same one the console talks to). */
  serverUrl: string;
  /** The editor's current architecture — what "save" persists. */
  architecture: Architecture;
  /** The editor's current workload — what "save workload" persists. */
  workload: WorkloadSpec;
  /** The editor's run configuration (seed, horizon, retries). */
  options: SimulationOptions;
  /** The editor's failure injections, applied to a persisted run. */
  failures: Failure[];
  /** Replace part of the editor document (load a version / workload / run). */
  onLoadDocument: (doc: {
    architecture?: Architecture;
    workload?: WorkloadSpec;
  }) => void;
  /** Surface a one-line message in the page's banner. */
  onNotice: (message: string) => void;
}) {
  const client = useMemo(
    () => createWorkspaceClient({ baseUrl: serverUrl }),
    [serverUrl],
  );
  const [s, d] = useReducer(workspaceReducer, initialWorkspaceState);
  const [newProject, setNewProject] = useState("");
  const [newArchName, setNewArchName] = useState("");
  const [newWorkloadName, setNewWorkloadName] = useState("");
  /** Two-step destructive action: the key armed for confirmation. */
  const [armed, setArmed] = useState("");

  const project = selectedProject(s);
  const arch = selectedArchitecture(s);
  const version = selectedVersion(s);
  const wl = selectedWorkload(s);
  const open = detailRun(s);
  const openResult = detailResult(s);
  const busy = s.busy !== "";
  const rows = s.compared.length >= 1 ? storedRunsToEntries(comparedResults(s)) : [];

  /** Run one client call with busy/error bookkeeping and a useful message. */
  const call = useCallback(
    async <T,>(what: string, fn: () => Promise<T>): Promise<T | null> => {
      d({ type: "busy", what });
      try {
        const out = await fn();
        d({ type: "idle" });
        return out;
      } catch (e) {
        // Connect surfaces the backend's structured message; anything else
        // (a dead server) surfaces its own text. Either way the user sees
        // what happened, and no stack trace is invented here.
        d({ type: "error", message: e instanceof Error ? e.message : String(e) });
        return null;
      }
    },
    [],
  );

  /* ------------------------------------------------------------- loading -- */

  useEffect(() => {
    let alive = true;
    d({ type: "busy", what: "projects" });
    client
      .listProjects({})
      .then((res) => {
        if (!alive) return;
        d({ type: "projects", projects: res.projects });
        d({ type: "idle" });
      })
      .catch((e: unknown) => {
        if (!alive) return;
        d({
          type: "error",
          message: e instanceof Error ? e.message : String(e),
        });
      });
    return () => {
      alive = false;
    };
  }, [client]);

  // Architectures of the selected project.
  useEffect(() => {
    if (!s.projectId) return;
    let alive = true;
    client
      .listArchitectures({ projectId: s.projectId })
      .then((res) => {
        if (alive) d({ type: "architectures", architectures: res.architectures });
      })
      .catch((e: unknown) => {
        if (alive) d({ type: "error", message: message(e) });
      });
    return () => {
      alive = false;
    };
  }, [client, s.projectId]);

  // Versions of the selected architecture.
  useEffect(() => {
    if (!s.architectureId) return;
    let alive = true;
    client
      .listArchitectureVersions({ architectureId: s.architectureId })
      .then((res) => {
        if (alive) d({ type: "versions", versions: res.versions });
      })
      .catch((e: unknown) => {
        if (alive) d({ type: "error", message: message(e) });
      });
    return () => {
      alive = false;
    };
  }, [client, s.architectureId]);

  // Workloads attached to the selected version.
  useEffect(() => {
    if (!s.versionId) return;
    let alive = true;
    client
      .listWorkloads({ architectureVersionId: s.versionId })
      .then((res) => {
        if (alive) d({ type: "workloads", workloads: res.workloads });
      })
      .catch((e: unknown) => {
        if (alive) d({ type: "error", message: message(e) });
      });
    return () => {
      alive = false;
    };
  }, [client, s.versionId]);

  // Run history for the project: every run of every architecture in it,
  // newest first. Project scope (rather than version scope) is what makes
  // "run again after a modification" comparable across versions.
  const reloadRuns = useCallback(async () => {
    if (!s.projectId) return;
    const res = await call("runs", () =>
      client.listSimulationRuns({ projectId: s.projectId }),
    );
    if (res) d({ type: "runs", runs: res.runs });
  }, [call, client, s.projectId]);

  useEffect(() => {
    void reloadRuns();
  }, [reloadRuns]);

  /* ------------------------------------------------------------- actions -- */

  const createProject = async () => {
    if (nameError(newProject)) return;
    const res = await call("create project", () =>
      client.createProject({ name: newProject.trim() }),
    );
    if (!res?.project) return;
    d({ type: "projects", projects: [...s.projects, res.project] });
    d({ type: "selectProject", id: res.project.id });
    setNewProject("");
    onNotice(`project saved: ${res.project.name}`);
  };

  const deleteProject = async (id: string) => {
    if (armed !== `project:${id}`) {
      setArmed(`project:${id}`);
      return;
    }
    setArmed("");
    const ok = await call("delete project", () => client.deleteProject({ id }));
    if (ok === null) return;
    d({ type: "projects", projects: s.projects.filter((p) => p.id !== id) });
    if (s.projectId === id) d({ type: "selectProject", id: "" });
    onNotice("project deleted with everything it contained");
  };

  /** Save the editor document as a brand-new architecture (v1). */
  const saveNewArchitecture = async () => {
    if (!s.projectId) return;
    const name = (newArchName.trim() || architecture.name || "architecture").trim();
    const res = await call("save architecture", () =>
      client.createArchitecture({
        projectId: s.projectId,
        name,
        definition: architecture,
      }),
    );
    if (!res?.architecture || !res.version) return;
    d({ type: "architectures", architectures: [res.architecture, ...s.architectures] });
    d({ type: "selectArchitecture", id: res.architecture.id });
    d({ type: "versions", versions: [res.version] });
    d({ type: "selectVersion", id: res.version.id });
    setNewArchName("");
    onNotice(`saved ${name} as v${res.version.version}`);
  };

  /** Append an immutable version of the selected architecture. */
  const saveNewVersion = async () => {
    if (!arch) return;
    const res = await call("save version", () =>
      client.createArchitectureVersion({
        architectureId: arch.id,
        definition: architecture,
      }),
    );
    if (!res?.version) return;
    d({ type: "versions", versions: [...s.versions, res.version] });
    d({ type: "selectVersion", id: res.version.id });
    onNotice(
      `saved v${res.version.version} — history is append-only, earlier versions are untouched`,
    );
  };

  const saveWorkload = async () => {
    if (!version) return;
    const name = (newWorkloadName.trim() || "peak").trim();
    const res = await call("save workload", () =>
      client.createWorkload({
        architectureVersionId: version.id,
        name,
        spec: workload,
      }),
    );
    if (!res?.workload) return;
    d({ type: "workloads", workloads: [res.workload, ...s.workloads] });
    d({ type: "selectWorkload", id: res.workload.id });
    setNewWorkloadName("");
    onNotice(`workload saved: ${name}`);
  };

  /** Run the selected version + workload and persist both run and result. */
  const runAndSave = async () => {
    if (!version || !wl) return;
    d({ type: "busy", what: "run" });
    try {
      const res = await client.executeSimulation({
        architectureVersionId: version.id,
        workloadId: wl.id,
        // The editor's failure injections are the scenario for this run.
        options: { ...options, failures },
      });
      if (!res.result?.run) throw new Error("backend returned no run");
      d({ type: "result", runId: res.result.run.id, result: res.result });
      d({ type: "runs", runs: [res.result.run, ...s.runs] });
      d({ type: "detail", runId: res.result.run.id });
      d({ type: "idle" });
      const status = runStatusDisplay(res.result.run.status);
      onNotice(
        res.result.run.status === RunStatus.FAILED
          ? `run failed: ${res.result.run.error}`
          : `run saved · seed ${res.result.run.seed} · ${status.label}`,
      );
    } catch (e) {
      d({ type: "error", message: message(e) });
    }
  };

  /** Open a stored run: fetch its result payload on first view. */
  const openRun = async (run: SimulationRunRecord) => {
    d({ type: "detail", runId: run.id });
    if (s.results[run.id]) return;
    const res = await call("load run", () =>
      client.getSimulationRun({ id: run.id }),
    );
    if (res?.result) d({ type: "result", runId: run.id, result: res.result });
  };

  /** Select a run for the side-by-side table (fetching its result once). */
  const toggleCompare = async (run: SimulationRunRecord) => {
    d({ type: "toggleCompare", runId: run.id });
    if (s.results[run.id]) return;
    const res = await call("load run", () =>
      client.getSimulationRun({ id: run.id }),
    );
    if (res?.result) d({ type: "result", runId: run.id, result: res.result });
  };

  /**
   * Restore a stored run into the editor: its architecture version AND its
   * workload. This is the "modify → run again" path — the user changes the
   * document, saves a new version, and re-runs against the new version.
   */
  const loadRunIntoEditor = async (run: SimulationRunRecord) => {
    const versionRes = await call("load run", () =>
      client.getArchitectureVersion({ id: run.architectureVersionId }),
    );
    if (!versionRes?.version?.definition) return;
    onLoadDocument({ architecture: versionRes.version.definition });
    if (run.workloadId) {
      const wlRes = await call("load run", () =>
        client.getWorkload({ id: run.workloadId }),
      );
      if (wlRes?.workload?.spec) onLoadDocument({ workload: wlRes.workload.spec });
    }
    onNotice(`loaded ${runLabel(run)} into the editor`);
  };

  /* ----------------------------------------------------------------- view -- */

  return (
    <div className="ws-view">
      {s.error && (
        <div className="banner banner-bad" role="status">
          <span className="banner-mark">!</span>
          <span>{s.error}</span>
        </div>
      )}

      <div className="ws-grid">
        {/* ------------------------------------------------------ projects */}
        <Panel title="Projects" tag="postgresql · workspace">
          <div className="ws-create">
            <Field label="new project">
              <Input
                value={newProject}
                onChange={setNewProject}
                width={160}
                spellCheck={false}
              />
            </Field>
            <Button
              onClick={createProject}
              disabled={busy || newProject.trim() === ""}
            >
              create
            </Button>
          </div>
          {s.projects.length === 0 ? (
            <EmptyState>
              no projects yet — create one, then save the architecture you are
              building into it.
            </EmptyState>
          ) : (
            <ul className="ws-list">
              {s.projects.map((p) => (
                <li
                  key={p.id}
                  className={`ws-row${p.id === s.projectId ? " active" : ""}`}
                >
                  <button
                    type="button"
                    className="ws-row-main"
                    onClick={() => d({ type: "selectProject", id: p.id })}
                  >
                    <span className="ws-row-name">{p.name}</span>
                    <span className="ws-row-meta">
                      {shortId(p.id)} · {formatTimestamp(p.createdAt)}
                    </span>
                  </button>
                  <button
                    type="button"
                    className="ws-row-act"
                    onClick={() => deleteProject(p.id)}
                  >
                    {armed === `project:${p.id}` ? "confirm?" : "delete"}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        {/* ------------------------------------------------- architectures */}
        <Panel title="Architectures" tag={project ? project.name : "select a project"}>
          {!project ? (
            <EmptyState>select a project to see its architectures.</EmptyState>
          ) : (
            <>
              <div className="ws-create">
                <Field label="save current document as">
                  <Input
                    value={newArchName}
                    onChange={setNewArchName}
                    width={160}
                    spellCheck={false}
                  />
                </Field>
                <Button onClick={saveNewArchitecture} disabled={busy}>
                  save as v1
                </Button>
              </div>
              {s.architectures.length === 0 ? (
                <EmptyState>
                  no architectures in this project yet — the editor's current
                  document becomes v1.
                </EmptyState>
              ) : (
                <ul className="ws-list">
                  {s.architectures.map((a) => (
                    <li
                      key={a.id}
                      className={`ws-row${a.id === s.architectureId ? " active" : ""}`}
                    >
                      <button
                        type="button"
                        className="ws-row-main"
                        onClick={() => d({ type: "selectArchitecture", id: a.id })}
                      >
                        <span className="ws-row-name">{a.name}</span>
                        <span className="ws-row-meta">
                          {a.latestVersion === 0
                            ? "no versions"
                            : `v${a.latestVersion} · ${a.versionCount} version${a.versionCount === 1 ? "" : "s"}`}
                          {" · "}
                          {formatTimestamp(a.updatedAt)}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </Panel>

        {/* ------------------------------------------------------ versions */}
        <Panel title="Versions" tag={arch ? arch.name : "select an architecture"}>
          {!arch ? (
            <EmptyState>select an architecture to see its version chain.</EmptyState>
          ) : (
            <>
              <div className="ws-create">
                <Button onClick={saveNewVersion} disabled={busy}>
                  save current document as v{arch.latestVersion + 1}
                </Button>
              </div>
              <ul className="ws-list">
                {s.versions.map((v) => (
                  <li
                    key={v.id}
                    className={`ws-row${v.id === s.versionId ? " active" : ""}`}
                  >
                    <button
                      type="button"
                      className="ws-row-main"
                      onClick={() => d({ type: "selectVersion", id: v.id })}
                    >
                      <span className="ws-row-name">{versionLabel(v)}</span>
                      <span className="ws-row-meta">
                        {formatTimestamp(v.createdAt)} · immutable
                      </span>
                    </button>
                    <button
                      type="button"
                      className="ws-row-act"
                      onClick={() =>
                        onLoadDocument({ architecture: v.definition })
                      }
                    >
                      load
                    </button>
                  </li>
                ))}
              </ul>
            </>
          )}
        </Panel>

        {/* ----------------------------------------------------- workloads */}
        <Panel title="Workloads" tag={version ? `v${version.version}` : "select a version"}>
          {!version ? (
            <EmptyState>
              select a version — a workload belongs to the version it was
              measured against.
            </EmptyState>
          ) : (
            <>
              <div className="ws-create">
                <Field label="save current workload as">
                  <Input
                    value={newWorkloadName}
                    onChange={setNewWorkloadName}
                    width={140}
                    spellCheck={false}
                  />
                </Field>
                <Button onClick={saveWorkload} disabled={busy}>
                  save
                </Button>
              </div>
              {s.workloads.length === 0 ? (
                <EmptyState>
                  no workloads yet — the editor's workload becomes the first.
                </EmptyState>
              ) : (
                <ul className="ws-list">
                  {s.workloads.map((w) => (
                    <li
                      key={w.id}
                      className={`ws-row${w.id === s.workloadId ? " active" : ""}`}
                    >
                      <button
                        type="button"
                        className="ws-row-main"
                        onClick={() => d({ type: "selectWorkload", id: w.id })}
                      >
                        <span className="ws-row-name">{w.name}</span>
                        <span className="ws-row-meta">
                          {w.spec
                            ? `${f0(w.spec.dau)} dau · ${f1(w.spec.peakMultiplier)}x peak`
                            : "no spec"}
                        </span>
                      </button>
                      <button
                        type="button"
                        className="ws-row-act"
                        onClick={() => onLoadDocument({ workload: w.spec })}
                      >
                        load
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </Panel>
      </div>

      {/* ---------------------------------------------------------- run */}
      <Panel title="Run & save" tag="saves the run and its result to postgresql">
        {!canExecute(version, wl) ? (
          <EmptyState>
            pick an architecture version and a workload from that version — the
            engine runs the stored documents, never a copy held in the browser.
          </EmptyState>
        ) : (
          <div className="ws-run">
            <div className="ws-run-facts">
              <Fact label="architecture" value={`${arch?.name ?? "?"} · v${version?.version}`} />
              <Fact label="workload" value={wl?.name ?? "?"} />
              <Fact label="seed" value={String(options.seed ?? 0n)} />
              <Fact label="horizon" value={`${f0(options.durationMs)} ms`} />
              <Fact
                label="failures"
                value={
                  failures.length === 0
                    ? "none"
                    : failures.map((f) => f.target).join(", ")
                }
              />
            </div>
            <Button variant="primary" onClick={runAndSave} disabled={busy}>
              {s.busy === "run" ? "running…" : "run & save"}
            </Button>
          </div>
        )}
      </Panel>

      {/* ------------------------------------------------------- history */}
      <Panel
        title="Simulation history"
        tag={project ? `${s.runs.length} run${s.runs.length === 1 ? "" : "s"}` : "select a project"}
      >
        {s.runs.length === 0 ? (
          <EmptyState>
            no stored runs yet. A run appears here once it is executed and
            persisted, with the seed it used, so it can always be reproduced.
          </EmptyState>
        ) : (
          <div className="ws-table-wrap">
            <table className="ws-table">
              <thead>
                <tr>
                  <th>run</th>
                  <th>status</th>
                  <th>version</th>
                  <th>seed</th>
                  <th>horizon</th>
                  <th>p99</th>
                  <th>completed</th>
                  <th>created</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {s.runs.map((r) => {
                  const status = runStatusDisplay(r.status);
                  const res = s.results[r.id];
                  return (
                    <tr key={r.id}>
                      <td>{shortId(r.id)}</td>
                      <td>
                        <Badge tone={status.tone}>{status.label}</Badge>
                      </td>
                      <td>{shortId(r.architectureVersionId)}</td>
                      <td>{String(r.seed)}</td>
                      <td>{f0(r.durationMs)} ms</td>
                      <td>{res?.metrics ? `${f2(res.metrics.p99Ms)} ms` : "—"}</td>
                      <td>{res?.metrics ? f0(res.metrics.completed) : "—"}</td>
                      <td>{formatTimestamp(r.createdAt)}</td>
                      <td className="ws-row-actions">
                        <button
                          type="button"
                          className="ws-row-act"
                          onClick={() => void openRun(r)}
                        >
                          {s.detailRunId === r.id ? "close" : "open"}
                        </button>
                        <button
                          type="button"
                          className="ws-row-act"
                          onClick={() => void loadRunIntoEditor(r)}
                        >
                          load
                        </button>
                        <label className="ws-compare-toggle">
                          <input
                            type="checkbox"
                            checked={s.compared.includes(r.id)}
                            onChange={() => void toggleCompare(r)}
                          />
                          compare
                        </label>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {/* ---------------------------------------------------- run detail */}
      {open && (
        <Panel title={`Stored run ${shortId(open.id)}`} tag={`seed ${open.seed}`}>
          {runProblem(open) !== "" ? (
            <EmptyState>
              {runProblem(open)} — a failed run is stored honestly, with no
              fabricated metrics.
            </EmptyState>
          ) : openResult ? (
            <StoredRunDetail run={open} result={openResult} />
          ) : (
            <EmptyState>loading stored result…</EmptyState>
          )}
        </Panel>
      )}

      {/* ----------------------------------------------------- comparison */}
      {s.compared.length > 0 && (
        <Panel
          title="Compare stored runs"
          tag="same table as a live comparison · no scores, no ranking"
        >
          {rows.length < 1 ? (
            <EmptyState>loading the selected runs…</EmptyState>
          ) : (
            <div className="cmp-table-wrap">
              <table className="cmp-table">
                <thead>
                  <tr>
                    <th>metric</th>
                    {rows.map((e) => (
                      <th key={e.name}>{e.name}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {comparisonRows(rows).map((row) => (
                    <tr key={row.metric}>
                      <td>
                        {row.metric} <span className="ws-unit">{row.unit}</span>
                      </td>
                      {row.values.map((v, i) => (
                        <td key={`${row.metric}:${i}`}>{formatCell(row.metric, v)}</td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      )}
    </div>
  );
}

/** The stored result of one run: headline, bottlenecks, capacity, cost. */
function StoredRunDetail({
  run,
  result,
}: {
  run: SimulationRunRecord;
  result: SimulationRunResult;
}) {
  const m = result.metrics;
  const status = runStatusDisplay(run.status);
  return (
    <div className="ws-detail">
      <div className="ws-detail-head">
        <Badge tone={status.tone}>{status.label}</Badge>
        <span className="ws-detail-run">{runLabel(run)}</span>
        <span className="ws-detail-when">
          ran {formatTimestamp(run.startedAt)} · horizon {f0(run.durationMs)} ms
        </span>
      </div>

      {result.plan && (
        <Section label="derived load">
          <div className="ws-facts">
            <Fact label="dau" value={f0(result.plan.dau)} />
            <Fact label="requests/day" value={f0(result.plan.requestsPerDay)} />
            <Fact label="average rps" value={f1(result.plan.averageRps)} />
            <Fact
              label="peak rps"
              value={`${f1(result.plan.peakRps)} (x${f1(result.plan.peakMultiplier)})`}
            />
          </div>
        </Section>
      )}

      {m && (
        <Section label="measured outcome">
          <div className="ws-facts">
            <Fact label="generated" value={f0(m.generated)} />
            <Fact label="completed" value={f0(m.completed)} />
            <Fact label="rejected" value={f0(m.rejected)} />
            <Fact label="failed" value={f0(m.failed)} />
            <Fact label="avg" value={`${f2(m.avgLatencyMs)} ms`} />
            <Fact label="p50" value={`${f2(m.p50Ms)} ms`} />
            <Fact label="p95" value={`${f2(m.p95Ms)} ms`} />
            <Fact label="p99" value={`${f2(m.p99Ms)} ms`} />
            <Fact label="error rate" value={pct(m.errorRate)} />
            <Fact label="timeout rate" value={pct(m.timeoutRate)} />
          </div>
        </Section>
      )}

      {m && (
        <Section label="per-component">
          <div className="ws-table-wrap">
            <table className="ws-table">
              <thead>
                <tr>
                  <th>component</th>
                  <th>kind</th>
                  <th>arrived</th>
                  <th>completed</th>
                  <th>rejected</th>
                  <th>failed</th>
                  <th>queue</th>
                  <th>util</th>
                  <th>trend</th>
                </tr>
              </thead>
              <tbody>
                {(m.components ?? []).map((c) => (
                  <tr key={c.id}>
                    <td>{c.id}</td>
                    <td>{c.kind}</td>
                    <td>{f0(c.arrived)}</td>
                    <td>{f0(c.completed)}</td>
                    <td>{f0(c.rejected)}</td>
                    <td>{f0(c.failed)}</td>
                    <td>{f0(c.maxQueueDepth)}</td>
                    <td>{pct(c.utilization)}</td>
                    <td>{c.queueTrend}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Section>
      )}

      <Section label="diagnosis">
        <BottleneckPanel
          diagnosis={result.diagnosis ?? null}
          metrics={m ?? null}
        />
      </Section>

      <Section label="capacity (modeled)">
        <CapacityPanel reports={result.capacity ?? []} />
      </Section>

      <Section label="monthly cost (estimate)">
        <CostPanel estimate={result.cost ?? null} />
      </Section>

      {result.summary && (
        <Section label="engine">
          <div className="ws-facts">
            <Fact label="events processed" value={f0(result.summary.eventsProcessed)} />
            <Fact label="events scheduled" value={f0(result.summary.eventsScheduled)} />
            <Fact label="pending" value={f0(result.summary.eventsPending)} />
            <Fact label="stop reason" value={result.summary.stopReason || "—"} />
          </div>
        </Section>
      )}
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="ws-fact">
      <span className="ws-fact-label">{label}</span>
      <span className="ws-fact-value">{value}</span>
    </div>
  );
}

/**
 * Format one comparison cell. Cost is rendered as a price; rates and
 * utilization as percentages; everything else as a plain number (2
 * decimals for latency, integers for counts).
 */
function formatCell(metric: string, v: number | null): string {
  if (v === null || v === undefined) return "—";
  if (metric.startsWith("Est. monthly cost")) return price(v);
  if (metric.endsWith("rate") || metric.startsWith("Max utilization") || metric.includes("Headroom")) {
    return pct(v);
  }
  if (metric.includes("latency") || metric === "p50" || metric === "p95" || metric === "p99") {
    return `${f2(v)} ms`;
  }
  return metric.includes("rps") ? f1(v) : f0(v);
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
