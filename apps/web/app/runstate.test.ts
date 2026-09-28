/**
 * Run-outcome state tests: reducer transitions, page-status derivation,
 * and the artifact fetch sequence for a terminal run (results →
 * diagnosis → capacity → cost, with baseline semantics and stale-data
 * isolation).
 */
import { describe, expect, it, vi } from "vitest";
import { create } from "@loadline/api";
import {
  FinalResultsSchema,
  SystemMetricsSchema,
  DiagnosisSchema,
} from "@loadline/api";
import {
  runReducer,
  initialRun,
  pageStatus,
  fetchOutcomeArtifacts,
} from "./runstate";

function resultsMessage() {
  return create(FinalResultsSchema, {
    metrics: create(SystemMetricsSchema, {
      durationMs: 10_000,
      generated: 1000n,
      completed: 990n,
      rejected: 0n,
      failed: 0n,
      errorRate: 0,
    }),
  });
}

describe("runReducer", () => {
  it("starts idle with no artifacts", () => {
    expect(initialRun.phase).toBe("idle");
    expect(initialRun.results).toBeNull();
    expect(initialRun.simId).toBeNull();
  });

  it("records the simulation id and results", () => {
    let s = runReducer(initialRun, { type: "created", id: "sim-1" });
    expect(s.simId).toBe("sim-1");
    s = runReducer(s, { type: "results", results: resultsMessage() });
    expect(s.phase).toBe("done");
    expect(s.results?.metrics?.generated).toBe(1000n);
  });

  it("reset drops ALL previous artifacts (stale-data isolation)", () => {
    let s = runReducer(initialRun, { type: "created", id: "sim-1" });
    s = runReducer(s, { type: "results", results: resultsMessage() });
    s = runReducer(s, { type: "diagnosis", diagnosis: create(DiagnosisSchema, {}) });
    s = runReducer(s, { type: "reset" });
    expect(s).toEqual(initialRun);
  });
});

describe("pageStatus", () => {
  it("active runs win over any previous outcome", () => {
    const s = pageStatus({
      active: true,
      outcome: { status: "completed", simId: "sim-9" },
    });
    expect(s.state).toBe("warn");
    expect(s.label).toBe("running");
  });

  it("derives idle/completed/stopped/failed labels", () => {
    expect(pageStatus({ active: false, outcome: null }).label).toBe("idle");
    expect(pageStatus({ active: false, outcome: { status: "completed", simId: "sim-1" } }).state).toBe("ok");
    expect(pageStatus({ active: false, outcome: { status: "stopped", simId: "sim-1" } }).state).toBe("bad");
    expect(pageStatus({ active: false, outcome: { status: "failed", simId: "sim-1" } }).label).toContain("failed");
  });
});

describe("fetchOutcomeArtifacts", () => {
  const okClient = () => ({
    getResults: vi.fn().mockResolvedValue({ metrics: { generated: 5 } }),
    getDiagnosis: vi.fn().mockResolvedValue({ diagnosis: create(DiagnosisSchema, {}) }),
    getCapacity: vi.fn().mockResolvedValue({ reports: [], simulationId: "sim-1" }),
    getCostEstimate: vi.fn().mockResolvedValue({ estimate: { total: 10 }, simulationId: "sim-1" }),
  });

  it("fetches results → diagnosis → capacity → cost in order", async () => {
    const client = okClient();
    const dispatch = vi.fn();
    await fetchOutcomeArtifacts({ status: "completed", simId: "sim-1" }, {
      client: client as never,
      healthy: true,
      baselineId: null,
      dispatch,
    });
    expect(client.getResults).toHaveBeenCalledWith({ simulationId: "sim-1" });
    expect(client.getDiagnosis).toHaveBeenCalledWith({ simulationId: "sim-1", baselineSimulationId: "" });
    expect(client.getCapacity).toHaveBeenCalledWith({ simulationId: "sim-1" });
    expect(client.getCostEstimate).toHaveBeenCalledWith({ simulationId: "sim-1" });
    const kinds = dispatch.mock.calls.map((c) => c[0].type);
    expect(kinds).toEqual(["results", "diagnosis", "capacity", "cost"]);
  });

  it("sends the baseline id for failure runs and none for healthy runs", async () => {
    const client = okClient();
    await fetchOutcomeArtifacts({ status: "completed", simId: "sim-2" }, {
      client: client as never,
      healthy: false,
      baselineId: "sim-1",
      dispatch: vi.fn(),
    });
    expect(client.getDiagnosis).toHaveBeenCalledWith({
      simulationId: "sim-2",
      baselineSimulationId: "sim-1",
    });
  });

  it("surfaces fetch errors through the error action without throwing", async () => {
    const client = okClient();
    client.getCapacity.mockRejectedValue(new Error("no provider-backed components"));
    const dispatch = vi.fn();
    await fetchOutcomeArtifacts({ status: "stopped", simId: "sim-3" }, {
      client: client as never,
      healthy: true,
      baselineId: null,
      dispatch,
    });
    const kinds = dispatch.mock.calls.map((c) => c[0].type);
    expect(kinds).toEqual(["results", "diagnosis", "error"]);
    expect(dispatch.mock.calls.at(-1)![0].message).toContain("no provider-backed components");
  });
});
