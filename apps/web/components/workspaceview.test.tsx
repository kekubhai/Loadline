/**
 * WorkspaceView integration tests — the persistence workflow end to end
 * inside the component. The Connect client is mocked (the real transport,
 * SQL, and engine are covered by the Go tests); what is under test is the
 * wiring the browser ships: fetch on selection, save the editor document,
 * execute a stored version + workload, and render the stored result.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { create, RunStatus } from "@loadline/api";
import {
  ArchitectureRecordSchema,
  ArchitectureSchema,
  ArchitectureVersionSchema,
  ComponentKind,
  ProjectSchema,
  SimulationOptionsSchema,
  SimulationRunRecordSchema,
  SimulationRunResultSchema,
  WorkloadRecordSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import type { Architecture, SimulationOptions, WorkloadSpec } from "@loadline/api";
import { WorkspaceView } from "./workspaceview";

/* ------------------------------------------------------------- fixtures -- */

const architecture: Architecture = create(ArchitectureSchema, {
  schemaVersion: "1",
  name: "aws-web",
  components: [
    { id: "client", kind: ComponentKind.CLIENT },
    { id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "lambda" },
    { id: "db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds" },
  ],
  links: [
    { from: "client", to: "api" },
    { from: "api", to: "db" },
  ],
});

const workload: WorkloadSpec = create(WorkloadSpecSchema, {
  totalUsers: 1_000_000n,
  dau: 100_000n,
  requestsPerUserPerDay: 10,
  peakMultiplier: 3,
  readWriteRatio: 4,
  payloadBytes: 4096n,
});

const options: SimulationOptions = create(SimulationOptionsSchema, {
  seed: 7n,
  durationMs: 5_000,
});

const project = create(ProjectSchema, { id: "p1", name: "prod-system" });
const otherProject = create(ProjectSchema, { id: "p2", name: "staging-system" });
const record = create(ArchitectureRecordSchema, {
  id: "a1",
  projectId: "p1",
  name: "aws-web",
  versionCount: 1,
  latestVersion: 1,
});
const version = create(ArchitectureVersionSchema, {
  id: "v1",
  architectureId: "a1",
  version: 1,
  definition: architecture,
});
const workloadRecord = create(WorkloadRecordSchema, {
  id: "w1",
  architectureVersionId: "v1",
  name: "peak",
  spec: workload,
});

function storedResult() {
  const run = create(SimulationRunRecordSchema, {
    id: "r1",
    architectureVersionId: "v1",
    workloadId: "w1",
    status: RunStatus.COMPLETED,
    seed: 7n,
    durationMs: 5_000,
  });
  return create(SimulationRunResultSchema, {
    run,
    plan: {
      totalUsers: 1_000_000n,
      dau: 100_000n,
      dauFraction: 0.1,
      requestsPerUserPerDay: 10,
      requestsPerDay: 1_000_000,
      averageRps: 11.57,
      peakMultiplier: 3,
      peakRps: 34.72,
      readFraction: 0.8,
      writeFraction: 0.2,
      payloadBytes: 4096n,
      meanInterArrivalMillis: 28.8,
    },
    metrics: {
      durationMs: 5_000,
      generated: 5_800n,
      completed: 5_700n,
      rejected: 0n,
      failed: 100n,
      timeouts: 2n,
      dropped: 0n,
      inFlight: 0n,
      errorRate: 0.017,
      timeoutRate: 0.0003,
      avgLatencyMs: 3.2,
      p50Ms: 2.1,
      p95Ms: 8.4,
      p99Ms: 12.5,
      maxLatencyMs: 40,
      components: [
        {
          id: "db",
          kind: "database",
          arrived: 1_200n,
          completed: 1_150n,
          rejected: 0n,
          failed: 0n,
          queueDepth: 0,
          maxQueueDepth: 3,
          inFlight: 0,
          utilization: 0.82,
          avgQueueWaitMs: 1.1,
          avgServiceMs: 5,
          throughputRps: 230,
          arrivalRps: 240,
          capacityRps: 500,
          queueTrend: "stable",
          saturated: false,
        },
      ],
    },
    summary: { eventsProcessed: 1_234n, eventsScheduled: 1_300n, eventsPending: 0, stopReason: "horizon" },
    diagnosis: { bottlenecks: [], impacts: [], healthy: true, summary: "no component is saturated" },
    capacity: [],
    cost: { currency: "USD", total: 123.45, byCategory: {}, components: [], assumptions: [] },
  });
}

/* ----------------------------------------------------------------- mock --- */

interface MockWorkspaceClient {
  listProjects: Mock;
  listArchitectures: Mock;
  listArchitectureVersions: Mock;
  listWorkloads: Mock;
  listSimulationRuns: Mock;
  createProject: Mock;
  deleteProject: Mock;
  createArchitecture: Mock;
  createArchitectureVersion: Mock;
  createWorkload: Mock;
  executeSimulation: Mock;
  getSimulationRun: Mock;
  getArchitectureVersion: Mock;
  getWorkload: Mock;
}

vi.mock("@loadline/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@loadline/api")>();
  return {
    ...actual,
    createWorkspaceClient: vi.fn(() => mockWorkspaceFactory()),
  };
});

let mockWorkspaceFactory: () => MockWorkspaceClient = () => mockWorkspace();

function mockWorkspace(over: Partial<MockWorkspaceClient> = {}): MockWorkspaceClient {
  return {
    listProjects: vi.fn(async () => ({ projects: [project, otherProject] })),
    listArchitectures: vi.fn(async () => ({ architectures: [record] })),
    listArchitectureVersions: vi.fn(async () => ({ versions: [version] })),
    listWorkloads: vi.fn(async () => ({ workloads: [workloadRecord] })),
    listSimulationRuns: vi.fn(async () => ({ runs: [] })),
    createProject: vi.fn(async () => ({ project })),
    deleteProject: vi.fn(async () => ({})),
    createArchitecture: vi.fn(async () => ({ architecture: record, version })),
    createArchitectureVersion: vi.fn(async () => ({ version })),
    createWorkload: vi.fn(async () => ({ workload: workloadRecord })),
    executeSimulation: vi.fn(async () => ({ result: storedResult() })),
    getSimulationRun: vi.fn(async () => ({ result: storedResult() })),
    getArchitectureVersion: vi.fn(async () => ({ version })),
    getWorkload: vi.fn(async () => ({ workload: workloadRecord })),
    ...over,
  };
}

/* -------------------------------------------------------------- harness --- */

function renderView(over: Partial<MockWorkspaceClient> = {}) {
  const ws = mockWorkspace(over);
  mockWorkspaceFactory = () => ws;
  const onLoadDocument = vi.fn();
  const onNotice = vi.fn();
  render(
    <WorkspaceView
      serverUrl="http://test:8080"
      architecture={architecture}
      workload={workload}
      options={options}
      failures={[]}
      onLoadDocument={onLoadDocument}
      onNotice={onNotice}
    />,
  );
  return { ws, onLoadDocument, onNotice };
}

/** Select project → architecture → version → workload through the UI. */
async function drillDown(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByText("prod-system"));
  await user.click(await screen.findByText("aws-web"));
  await user.click(await screen.findByText(/v1 · aws-web · 3 nodes/));
  await user.click(await screen.findByText("peak"));
}

beforeEach(() => {
  mockWorkspaceFactory = () => mockWorkspace();
});

describe("WorkspaceView", () => {
  it("lists stored projects and loads architectures for the selected one", async () => {
    const user = userEvent.setup();
    const { ws } = renderView();

    expect(await screen.findByText("prod-system")).toBeInTheDocument();
    expect(screen.getByText("staging-system")).toBeInTheDocument();
    // Nothing is fetched before a project is chosen.
    expect(ws.listArchitectures).not.toHaveBeenCalled();

    await user.click(screen.getByText("prod-system"));
    await waitFor(() =>
      expect(ws.listArchitectures).toHaveBeenCalledWith({ projectId: "p1" }),
    );
    // The project's run history is loaded too (newest first, project scope).
    expect(ws.listSimulationRuns).toHaveBeenCalledWith({ projectId: "p1" });
  });

  it("saves the editor document as a new architecture (v1)", async () => {
    const user = userEvent.setup();
    const { ws, onNotice } = renderView();

    await user.click(await screen.findByText("prod-system"));
    await user.click(screen.getByRole("button", { name: "save as v1" }));

    await waitFor(() => expect(ws.createArchitecture).toHaveBeenCalledTimes(1));
    // The stored document IS the editor's document — no copy is made here.
    expect(ws.createArchitecture).toHaveBeenCalledWith({
      projectId: "p1",
      name: "aws-web",
      definition: architecture,
    });
    await waitFor(() => expect(onNotice).toHaveBeenCalled());
  });

  it("appends an immutable version when saving the modified document", async () => {
    const user = userEvent.setup();
    const { ws } = renderView();

    await user.click(await screen.findByText("prod-system"));
    await user.click(await screen.findByText("aws-web"));
    await user.click(
      await screen.findByRole("button", { name: /save current document as v2/ }),
    );

    await waitFor(() => expect(ws.createArchitectureVersion).toHaveBeenCalledTimes(1));
    expect(ws.createArchitectureVersion).toHaveBeenCalledWith({
      architectureId: "a1",
      definition: architecture,
    });
  });

  it("executes the stored version + workload and renders the stored result", async () => {
    const user = userEvent.setup();
    const { ws, onNotice } = renderView();

    await drillDown(user);
    await user.click(screen.getByRole("button", { name: /run & save/ }));

    await waitFor(() => expect(ws.executeSimulation).toHaveBeenCalledTimes(1));
    // The run is defined by STORED ids plus the editor's run configuration.
    expect(ws.executeSimulation).toHaveBeenCalledWith({
      architectureVersionId: "v1",
      workloadId: "w1",
      options: expect.objectContaining({ seed: 7n }),
    });

    // The stored result is rendered from the backend payload: the run
    // header, the measured headline, and the per-component row. Numbers go
    // through the shared formatters, which group thousands.
    expect(await screen.findByText(/horizon 5[,.\s]?000 ms/)).toBeInTheDocument();
    // The completed count appears in both the headline and the component row.
    expect(screen.getAllByText(/^5[,.\s]?700$/).length).toBeGreaterThan(0);
    expect(screen.getByText("db")).toBeInTheDocument();
    // Every stored section of the result is present: the derived load, the
    // per-component table, the diagnosis, capacity, cost, and the engine
    // counters (the stop reason comes straight from the stored payload).
    expect(screen.getByText("derived load")).toBeInTheDocument();
    expect(screen.getByText("per-component")).toBeInTheDocument();
    expect(screen.getByText("diagnosis")).toBeInTheDocument();
    expect(screen.getByText("capacity (modeled)")).toBeInTheDocument();
    expect(screen.getByText("monthly cost (estimate)")).toBeInTheDocument();
    // Engine counters come from the stored run summary (the stop reason is
    // also a column header in the history table, so match the count here).
    expect(screen.getByText("engine")).toBeInTheDocument();
    expect(screen.getByText(/^1[,.\s]?234$/)).toBeInTheDocument();
    await waitFor(() => expect(onNotice).toHaveBeenCalled());
  });

  it("loads a stored version back into the editor", async () => {
    const user = userEvent.setup();
    const { onLoadDocument } = renderView();

    await user.click(await screen.findByText("prod-system"));
    await user.click(await screen.findByText("aws-web"));
    await user.click(await screen.findByRole("button", { name: "load" }));

    await waitFor(() => expect(onLoadDocument).toHaveBeenCalledTimes(1));
    expect(onLoadDocument).toHaveBeenCalledWith({ architecture });
  });

  it("restores a stored run's workload as well as its version", async () => {
    const user = userEvent.setup();
    const stored = storedResult();
    const { ws, onLoadDocument } = renderView({
      listSimulationRuns: vi.fn(async () => ({ runs: [stored.run] })),
    });

    await user.click(await screen.findByText("prod-system"));
    await user.click(await screen.findByRole("button", { name: "load" }));

    await waitFor(() => expect(onLoadDocument).toHaveBeenCalledTimes(2));
    expect(ws.getArchitectureVersion).toHaveBeenCalledWith({ id: "v1" });
    expect(ws.getWorkload).toHaveBeenCalledWith({ id: "w1" });
    expect(onLoadDocument).toHaveBeenNthCalledWith(1, { architecture });
    expect(onLoadDocument).toHaveBeenNthCalledWith(2, { workload });
  });

  it("shows the backend's message when a call fails", async () => {
    const user = userEvent.setup();
    renderView({
      listProjects: vi.fn(async () => {
        throw new Error("workspace persistence is disabled: DATABASE_URL is not set");
      }),
    });

    expect(
      await screen.findByText(/workspace persistence is disabled/),
    ).toBeInTheDocument();
  });
});
