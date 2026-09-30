/**
 * Workspace state tests. Pure logic only: the reducer, the selectors that
 * feed the stored-run comparison table, and the display formatters. No
 * metrics are computed here — every value asserted is either a state
 * transition or a pass-through of a stored backend payload.
 */
import { describe, expect, it } from "vitest";
import { create, RunStatus } from "@loadline/api";
import {
  ArchitectureRecordSchema,
  ArchitectureVersionSchema,
  ProjectSchema,
  SimulationRunRecordSchema,
  SimulationRunResultSchema,
  WorkloadRecordSchema,
} from "@loadline/api";
import type {
  ArchitectureRecord,
  SimulationRunRecord,
  SimulationRunResult,
} from "@loadline/api";
import {
  canExecute,
  comparedResults,
  detailResult,
  initialWorkspaceState,
  nameError,
  runLabel,
  runProblem,
  runStatusDisplay,
  storedRunsToEntries,
  versionComponentCount,
  versionLabel,
  workspaceReducer,
  formatTimestamp,
  shortId,
} from "./workspace";
import type { WorkspaceState } from "./workspace";

function project(id: string, name = id) {
  return create(ProjectSchema, { id, name });
}

function architecture(id: string, projectId: string): ArchitectureRecord {
  return create(ArchitectureRecordSchema, {
    id,
    projectId,
    name: `arch-${id}`,
    versionCount: 2,
    latestVersion: 2,
  });
}

function version(id: string, architectureId: string, nodes: number) {
  return create(ArchitectureVersionSchema, {
    id,
    architectureId,
    version: 1,
    definition: {
      schemaVersion: "1",
      name: "web",
      components: Array.from({ length: nodes }, (_, i) => ({
        id: `c${i}`,
        kind: 1,
        serviceTimeMillis: {},
        defaultServiceTimeMillis: 0,
        concurrency: 0,
        queueLimit: 0,
        capacityRps: 0,
        hitRatio: 0,
        fanOut: 1,
        provider: "",
        service: "",
      })),
      links: [],
    },
  });
}

function workload(id: string, architectureVersionId: string) {
  return create(WorkloadRecordSchema, {
    id,
    architectureVersionId,
    name: "peak",
    spec: { totalUsers: 100n, dau: 50n, requestsPerUserPerDay: 10, peakMultiplier: 3, readWriteRatio: 4, payloadBytes: 1024n },
  });
}

function run(
  id: string,
  architectureVersionId: string,
  overrides: Partial<
    Pick<SimulationRunRecord, "status" | "error" | "seed" | "durationMs">
  > = {},
): SimulationRunRecord {
  return create(SimulationRunRecordSchema, {
    id,
    architectureVersionId,
    workloadId: "w1",
    status: RunStatus.COMPLETED,
    seed: 7n,
    durationMs: 5_000,
    ...overrides,
  });
}

function result(metrics: Partial<{ generated: bigint; completed: bigint }> = {}) {
  return create(SimulationRunResultSchema, {
    run: run("r1", "v1"),
    metrics: {
      durationMs: 5_000,
      generated: metrics.generated ?? 5_800n,
      completed: metrics.completed ?? 5_700n,
      rejected: 0n,
      failed: 100n,
      timeouts: 0n,
      dropped: 0n,
      inFlight: 0n,
      errorRate: 0.017,
      timeoutRate: 0,
      avgLatencyMs: 3.2,
      p50Ms: 2.1,
      p95Ms: 8.4,
      p99Ms: 12.5,
      maxLatencyMs: 40,
      components: [],
    },
    cost: { currency: "USD", total: 123.45, byCategory: {}, components: [], assumptions: [] },
  });
}

function withSelection(): WorkspaceState {
  let s = workspaceReducer(initialWorkspaceState, {
    type: "projects",
    projects: [project("p1"), project("p2")],
  });
  s = workspaceReducer(s, { type: "selectProject", id: "p1" });
  s = workspaceReducer(s, {
    type: "architectures",
    architectures: [architecture("a1", "p1"), architecture("a2", "p1")],
  });
  s = workspaceReducer(s, { type: "selectArchitecture", id: "a1" });
  s = workspaceReducer(s, {
    type: "versions",
    versions: [version("v1", "a1", 4), version("v2", "a1", 5)],
  });
  s = workspaceReducer(s, { type: "selectVersion", id: "v2" });
  s = workspaceReducer(s, {
    type: "workloads",
    workloads: [workload("w1", "v2"), workload("w0", "v1")],
  });
  s = workspaceReducer(s, { type: "selectWorkload", id: "w1" });
  return workspaceReducer(s, {
    type: "runs",
    runs: [run("r2", "v2"), run("r1", "v1")],
  });
}

describe("workspaceReducer", () => {
  it("clears the whole selection below a newly selected project", () => {
    const s = withSelection();
    const next = workspaceReducer(s, { type: "selectProject", id: "p2" });
    expect(next.projectId).toBe("p2");
    expect(next.architectureId).toBe("");
    expect(next.versionId).toBe("");
    expect(next.workloadId).toBe("");
    expect(next.architectures).toEqual([]);
    expect(next.versions).toEqual([]);
    expect(next.workloads).toEqual([]);
    expect(next.runs).toEqual([]);
    expect(next.compared).toEqual([]);
  });

  it("keeps the project but clears versions below a newly selected architecture", () => {
    const s = withSelection();
    const next = workspaceReducer(s, { type: "selectArchitecture", id: "a2" });
    expect(next.projectId).toBe("p1");
    expect(next.architectureId).toBe("a2");
    expect(next.versionId).toBe("");
    expect(next.versions).toEqual([]);
    expect(next.runs).toEqual([]);
  });

  it("clears workloads and runs below a newly selected version", () => {
    const s = withSelection();
    const next = workspaceReducer(s, { type: "selectVersion", id: "v1" });
    expect(next.versionId).toBe("v1");
    expect(next.workloadId).toBe("");
    expect(next.workloads).toEqual([]);
    expect(next.runs).toEqual([]);
  });

  it("toggles the comparison selection and caches results by run id", () => {
    let s = withSelection();
    s = workspaceReducer(s, { type: "result", runId: "r2", result: result() });
    s = workspaceReducer(s, { type: "toggleCompare", runId: "r2" });
    expect(s.compared).toEqual(["r2"]);
    expect(s.results.r2).toBeDefined();
    s = workspaceReducer(s, { type: "toggleCompare", runId: "r2" });
    expect(s.compared).toEqual([]);
  });

  it("drops a comparison selection for a run that no longer exists", () => {
    let s = withSelection();
    s = workspaceReducer(s, { type: "toggleCompare", runId: "r1" });
    s = workspaceReducer(s, { type: "runs", runs: [run("r2", "v2")] });
    expect(s.compared).toEqual([]);
  });

  it("toggles the detail panel for the same run", () => {
    let s = withSelection();
    s = workspaceReducer(s, { type: "detail", runId: "r2" });
    expect(s.detailRunId).toBe("r2");
    s = workspaceReducer(s, { type: "detail", runId: "r2" });
    expect(s.detailRunId).toBe("");
  });

  it("clears the in-flight marker on success and keeps the message on failure", () => {
    let s = workspaceReducer(initialWorkspaceState, { type: "busy", what: "runs" });
    expect(s.busy).toBe("runs");
    s = workspaceReducer(s, { type: "error", message: "no such project" });
    expect(s.busy).toBe("");
    expect(s.error).toBe("no such project");

    s = workspaceReducer(s, { type: "busy", what: "runs" });
    expect(s.error).toBe("");
    s = workspaceReducer(s, { type: "notice", message: "run saved" });
    expect(s.busy).toBe("");
    expect(s.notice).toBe("run saved");
  });
});

describe("comparison selectors", () => {
  it("returns only checked runs whose stored result was fetched, in list order", () => {
    let s = withSelection();
    s = workspaceReducer(s, { type: "result", runId: "r1", result: result() });
    s = workspaceReducer(s, { type: "toggleCompare", runId: "r1" });
    s = workspaceReducer(s, { type: "toggleCompare", runId: "r2" }); // checked, no result yet
    const rows = comparedResults(s);
    expect(rows.map((r) => r.run.id)).toEqual(["r1"]);
  });

  it("passes stored metrics through untouched and marks failed runs with their error", () => {
    const ok = result({ generated: 5_800n, completed: 5_700n });
    const okRun = run("r1", "v1");
    const failedRun = run("r9", "v2", {
      status: RunStatus.FAILED,
      error: "cyclic architecture",
    });
    const failed = create(SimulationRunResultSchema, { run: failedRun });
    const entries = storedRunsToEntries([
      { run: okRun, result: ok },
      { run: failedRun, result: failed },
    ]);
    expect(entries[0].name).toBe(runLabel(okRun));
    expect(entries[0].error).toBe("");
    expect(entries[0].metrics?.p99Ms).toBe(12.5);
    expect(entries[0].metrics?.completed).toBe(5_700n);
    expect(entries[0].cost?.total).toBe(123.45);

    // A failed run carries its reason and no metrics at all.
    expect(entries[1].error).toBe("cyclic architecture");
    expect(entries[1].metrics).toBeUndefined();
  });
});

describe("formatting", () => {
  it("labels every persisted run status", () => {
    expect(runStatusDisplay(RunStatus.COMPLETED)).toEqual({
      tone: "ok",
      label: "completed",
    });
    expect(runStatusDisplay(RunStatus.STOPPED).tone).toBe("warn");
    expect(runStatusDisplay(RunStatus.FAILED).tone).toBe("bad");
    expect(runStatusDisplay(RunStatus.RUNNING).label).toBe("running");
    expect(runStatusDisplay(RunStatus.PENDING).label).toBe("pending");
  });

  it("renders timestamps in UTC and dashes for absent ones", () => {
    expect(formatTimestamp(undefined)).toBe("—");
    expect(formatTimestamp({ seconds: 0n, nanos: 0 })).toBe("—");
    expect(formatTimestamp({ seconds: 1_759_247_360n, nanos: 0 })).toBe(
      "2025-09-30 15:49:20Z",
    );
  });

  it("identifies runs by short id and seed", () => {
    const r = run("0b6f4b7e-1111-2222-3333-444455556666", "v1");
    expect(shortId(r.id)).toBe("0b6f4b7e");
    expect(runLabel(r)).toBe("0b6f4b7e · seed 7");
  });

  it("describes a version by number, name and node count", () => {
    expect(versionComponentCount(version("v1", "a1", 4))).toBe(4);
    expect(versionLabel(version("v1", "a1", 4))).toBe("v1 · web · 4 nodes");
    const bare = create(ArchitectureVersionSchema, { id: "v3", architectureId: "a1", version: 3 });
    expect(versionComponentCount(bare)).toBeNull();
    expect(versionLabel(bare)).toBe("v3 · unnamed");
  });

  it("explains why a run has no result to show", () => {
    expect(runProblem(run("r1", "v1"))).toBe("");
    expect(runProblem(run("r2", "v1", { status: RunStatus.FAILED, error: "boom" }))).toBe("boom");
    expect(runProblem(run("r3", "v1", { status: RunStatus.RUNNING }))).toBe(
      "run has no result yet",
    );
  });

  it("rejects blank names before they reach the backend", () => {
    expect(nameError("  ")).not.toBe("");
    expect(nameError("prod")).toBe("");
  });

  it("only allows a run when the workload belongs to the selected version", () => {
    const v = version("v2", "a1", 4);
    expect(canExecute(v, workload("w1", "v2"))).toBe(true);
    expect(canExecute(v, workload("w0", "v1"))).toBe(false);
    expect(canExecute(v, null)).toBe(false);
    expect(canExecute(null, workload("w1", "v2"))).toBe(false);
  });

  it("exposes the open detail result only once it is fetched", () => {
    let s = withSelection();
    s = workspaceReducer(s, { type: "detail", runId: "r2" });
    expect(detailResult(s)).toBeNull();
    s = workspaceReducer(s, { type: "result", runId: "r2", result: result() });
    expect(detailResult(s)?.metrics?.p99Ms).toBe(12.5);
  });
});
