/**
 * LOADLINE — workspace state (pure logic, no JSX).
 *
 * Owns the PERSISTED side of the product: the projects, architectures,
 * architecture versions, workloads, and simulation runs that live in
 * PostgreSQL, plus the selection that ties them together
 * (project → architecture → version → workload → run).
 *
 * Nothing here computes a simulation number. Every metric, bottleneck,
 * capacity ceiling, and cost line is a stored backend value that this
 * module only selects, sequences, and formats for display. The one
 * derived shape — `storedRunsToEntries` — re-labels stored runs so the
 * existing comparison table can render them side by side; it does not
 * recompute or re-rank anything.
 */
import { create } from "@loadline/api";
import type {
  ArchitectureRecord,
  ArchitectureVersion,
  ComparisonEntry,
  Project,
  RunStatus,
  SimulationRunRecord,
  SimulationRunResult,
  WorkloadRecord,
} from "@loadline/api";
// RunStatus is a protobuf enum: on the wire and in JS it is a NUMBER, and
// the TS member names drop the RUN_STATUS_ prefix. Comparisons must use the
// enum members, never the proto names as strings.
import { ComparisonEntrySchema, RunStatus as Status } from "@loadline/api";

export interface WorkspaceState {
  projects: Project[];
  architectures: ArchitectureRecord[];
  versions: ArchitectureVersion[];
  workloads: WorkloadRecord[];
  runs: SimulationRunRecord[];
  /** Stored result payloads, keyed by run id (fetched on demand). */
  results: Record<string, SimulationRunResult>;
  /** Run whose detail panel is open ("" = none). */
  detailRunId: string;
  /** Run ids checked for the side-by-side table. */
  compared: string[];
  /** Project → architecture → version → workload selection. */
  projectId: string;
  architectureId: string;
  versionId: string;
  workloadId: string;
  /** What is in flight, or "" when idle. */
  busy: string;
  error: string;
  notice: string;
}

export const initialWorkspaceState: WorkspaceState = {
  projects: [],
  architectures: [],
  versions: [],
  workloads: [],
  runs: [],
  results: {},
  detailRunId: "",
  compared: [],
  projectId: "",
  architectureId: "",
  versionId: "",
  workloadId: "",
  busy: "",
  error: "",
  notice: "",
};

export type WorkspaceAction =
  | { type: "busy"; what: string }
  | { type: "idle" }
  | { type: "error"; message: string }
  | { type: "notice"; message: string }
  | { type: "projects"; projects: Project[] }
  | { type: "architectures"; architectures: ArchitectureRecord[] }
  | { type: "versions"; versions: ArchitectureVersion[] }
  | { type: "workloads"; workloads: WorkloadRecord[] }
  | { type: "runs"; runs: SimulationRunRecord[] }
  | { type: "result"; runId: string; result: SimulationRunResult }
  | { type: "detail"; runId: string }
  | { type: "toggleCompare"; runId: string }
  | { type: "selectProject"; id: string }
  | { type: "selectArchitecture"; id: string }
  | { type: "selectVersion"; id: string }
  | { type: "selectWorkload"; id: string };

/**
 * The selection is a strict hierarchy: choosing a project clears the
 * architecture below it, and so on. Keeping that invariant in the
 * reducer (rather than in each fetch) means the UI can never show an
 * architecture that belongs to a different project.
 */
export function workspaceReducer(
  s: WorkspaceState,
  a: WorkspaceAction,
): WorkspaceState {
  switch (a.type) {
    case "busy":
      return { ...s, busy: a.what, error: "" };
    case "idle":
      return { ...s, busy: "" };
    case "error":
      return { ...s, busy: "", error: a.message };
    case "notice":
      return { ...s, busy: "", notice: a.message };
    case "projects":
      return { ...s, projects: a.projects };
    case "architectures":
      return { ...s, architectures: a.architectures };
    case "versions":
      return { ...s, versions: a.versions };
    case "workloads":
      return { ...s, workloads: a.workloads };
    case "runs":
      // The comparison selection only survives for runs that still exist.
      return {
        ...s,
        runs: a.runs,
        compared: s.compared.filter((id) => a.runs.some((r) => r.id === id)),
      };
    case "result":
      return { ...s, results: { ...s.results, [a.runId]: a.result } };
    case "detail":
      return { ...s, detailRunId: s.detailRunId === a.runId ? "" : a.runId };
    case "toggleCompare": {
      const compared = s.compared.includes(a.runId)
        ? s.compared.filter((id) => id !== a.runId)
        : [...s.compared, a.runId];
      return { ...s, compared };
    }
    case "selectProject":
      return {
        ...s,
        projectId: a.id,
        architectureId: "",
        versionId: "",
        workloadId: "",
        architectures: [],
        versions: [],
        workloads: [],
        runs: [],
        detailRunId: "",
        compared: [],
        results: {},
      };
    case "selectArchitecture":
      return {
        ...s,
        architectureId: a.id,
        versionId: "",
        workloadId: "",
        versions: [],
        workloads: [],
        runs: [],
        detailRunId: "",
        compared: [],
        results: {},
      };
    case "selectVersion":
      return {
        ...s,
        versionId: a.id,
        workloadId: "",
        workloads: [],
        runs: [],
        detailRunId: "",
        compared: [],
        results: {},
      };
    case "selectWorkload":
      return { ...s, workloadId: a.id };
  }
}

/* ------------------------------------------------------------ selectors -- */

export function selectedProject(s: WorkspaceState): Project | null {
  return s.projects.find((p) => p.id === s.projectId) ?? null;
}

export function selectedArchitecture(
  s: WorkspaceState,
): ArchitectureRecord | null {
  return s.architectures.find((a) => a.id === s.architectureId) ?? null;
}

export function selectedVersion(s: WorkspaceState): ArchitectureVersion | null {
  return s.versions.find((v) => v.id === s.versionId) ?? null;
}

export function selectedWorkload(s: WorkspaceState): WorkloadRecord | null {
  return s.workloads.find((w) => w.id === s.workloadId) ?? null;
}

/** The run whose detail panel is open, or null. */
export function detailRun(s: WorkspaceState): SimulationRunRecord | null {
  return s.runs.find((r) => r.id === s.detailRunId) ?? null;
}

/** The stored result currently open, or null. */
export function detailResult(s: WorkspaceState): SimulationRunResult | null {
  return s.results[s.detailRunId] ?? null;
}

/** Stored results for the runs checked for comparison, in list order. */
export function comparedResults(
  s: WorkspaceState,
): { run: SimulationRunRecord; result: SimulationRunResult }[] {
  return s.runs
    .filter((r) => s.compared.includes(r.id) && s.results[r.id])
    .map((r) => ({ run: r, result: s.results[r.id] }));
}

/**
 * Re-label stored runs as comparison entries so the existing table can
 * render them. Every field is passed through untouched: a failed run
 * carries its error and no metrics, exactly as stored.
 */
export function storedRunsToEntries(
  rows: { run: SimulationRunRecord; result: SimulationRunResult }[],
): ComparisonEntry[] {
  return rows.map(({ run, result }) =>
    create(ComparisonEntrySchema, {
      name: runLabel(run),
      error: run.status === Status.FAILED ? run.error : "",
      plan: result.plan,
      metrics: result.metrics,
      failures: result.failures ?? [],
      summary: result.summary,
      diagnosis: result.diagnosis,
      capacity: result.capacity ?? [],
      cost: result.cost,
    }),
  );
}

/* ----------------------------------------------------------- formatting -- */

/** Short, stable label for a run: identity plus the seed it ran with. */
export function runLabel(run: SimulationRunRecord): string {
  return `${shortId(run.id)} · seed ${run.seed}`;
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}

export type StatusTone = "ok" | "warn" | "bad" | "neutral";

/** Display form of a persisted run status. */
export function runStatusDisplay(status: RunStatus): {
  tone: StatusTone;
  label: string;
} {
  switch (status) {
    case Status.COMPLETED:
      return { tone: "ok", label: "completed" };
    case Status.STOPPED:
      return { tone: "warn", label: "stopped" };
    case Status.FAILED:
      return { tone: "bad", label: "failed" };
    case Status.RUNNING:
      return { tone: "warn", label: "running" };
    case Status.PENDING:
      return { tone: "neutral", label: "pending" };
    default:
      return { tone: "neutral", label: "unknown" };
  }
}

/**
 * Format a protobuf Timestamp as a compact UTC instant. Returns "—" for
 * an absent or zero timestamp, which is what an unfinished run carries.
 */
export function formatTimestamp(ts?: { seconds?: bigint; nanos?: number }): string {
  if (!ts?.seconds) return "—";
  const date = new Date(Number(ts.seconds) * 1000);
  if (Number.isNaN(date.getTime())) return "—";
  return `${date.toISOString().slice(0, 19).replace("T", " ")}Z`;
}

/** Component count of a stored architecture version, or null when absent. */
export function versionComponentCount(v: ArchitectureVersion): number | null {
  return v.definition ? v.definition.components.length : null;
}

/** One-line description of a stored version for list rows. */
export function versionLabel(v: ArchitectureVersion): string {
  const name = v.definition?.name ?? "unnamed";
  const count = versionComponentCount(v);
  return count === null ? `v${v.version} · ${name}` : `v${v.version} · ${name} · ${count} nodes`;
}

/** Human-readable reason a run produced no result, or "". */
export function runProblem(run: SimulationRunRecord): string {
  if (run.status === Status.FAILED) return run.error || "run failed";
  if (run.status !== Status.COMPLETED && run.status !== Status.STOPPED) {
    return "run has no result yet";
  }
  return "";
}

/** Reject a blank name before it reaches the backend. */
export function nameError(name: string): string {
  return name.trim() ? "" : "a name is required";
}

/**
 * Whether a workload/version pair can be executed: both are selected and
 * the workload belongs to the selected version.
 */
export function canExecute(
  version: ArchitectureVersion | null,
  workload: WorkloadRecord | null,
): boolean {
  return (
    version !== null &&
    workload !== null &&
    workload.architectureVersionId === version.id
  );
}
