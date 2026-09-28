/**
 * SimConsole integration tests — the component that owns a run end to
 * end. The Connect client is mocked (the real transport is covered by
 * the Go tests and the cross-stack E2E test); the component logic under
 * test — frame handling, run-state transitions, results rendering — is
 * the exact code the browser ships.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { create } from "@loadline/api";
import {
  ArchitectureSchema,
  ComponentKind,
  FinalResultsSchema,
  SystemMetricsSchema,
  LoadPlanSchema,
  RunSummarySchema,
  WorkloadSpecSchema,
  SimulationOptionsSchema,
  ProgressSnapshotSchema,
  RunStatus,
} from "@loadline/api";
import {
  ControlFrameSchema,
  GetSimulationStatusResponseSchema,
  StreamMetricsResponseSchema,
} from "@loadline/api";
import type {
  Architecture,
  WorkloadSpec,
  SimulationOptions,
  StreamMetricsResponse,
  ProgressSnapshot,
} from "@loadline/api";
import { SimConsole, type SimOutcome } from "./simconsole";

/* ------------------------------------------------------------- fixtures -- */

const arch: Architecture = create(ArchitectureSchema, {
  schemaVersion: "1",
  name: "test",
  components: [
    { id: "client", kind: ComponentKind.CLIENT },
    { id: "api", kind: ComponentKind.API_SERVER, provider: "aws", service: "lambda" },
    { id: "db", kind: ComponentKind.DATABASE, provider: "aws", service: "rds" },
  ],
  links: [{ from: "client", to: "api" }, { from: "api", to: "db" }],
});

const workload = create(WorkloadSpecSchema, {
  totalUsers: 1_000_000n,
  dau: 100_000n,
  requestsPerUserPerDay: 10,
  peakMultiplier: 3,
  readWriteRatio: 4,
  payloadBytes: 4096n,
});

const options = create(SimulationOptionsSchema, { seed: 7n, durationMs: 10_000 });

const finalResults = create(FinalResultsSchema, {
  plan: create(LoadPlanSchema, {
    totalUsers: 1_000_000n,
    dau: 100_000n,
    dauFraction: 0.1,
    requestsPerUserPerDay: 10,
    requestsPerDay: 1_000_000,
    averageRps: 11.6,
    peakMultiplier: 3,
    peakRps: 34.7,
    readFraction: 0.8,
    writeFraction: 0.2,
    payloadBytes: 4096n,
    meanInterArrivalMillis: 28.8,
  }),
  metrics: create(SystemMetricsSchema, {
    durationMs: 10_000,
    generated: 347n,
    completed: 340n,
    rejected: 2n,
    failed: 5n,
    errorRate: 0.0144,
    p50Ms: 8.1,
    p95Ms: 12.4,
    p99Ms: 15.9,
    maxLatencyMs: 20.2,
  }),
  summary: create(RunSummarySchema, { eventsProcessed: 5000n, stopReason: "queue_empty" }),
});

function snapshot(
  over: Partial<Pick<ProgressSnapshot, "simTimeMs" | "generated" | "completed" | "eventsProcessed" | "eventsPending">> = {},
): ProgressSnapshot {
  return create(ProgressSnapshotSchema, {
    simTimeMs: 1000,
    generated: 35n,
    completed: 34n,
    eventsProcessed: 500n,
    eventsPending: 100n,
    ...over,
  } as never) as ProgressSnapshot;
}

/* --------------------------------------------------------- client mock --- */

/** Builds a real StreamMetricsResponse so frames carry $typeName. */
function frame(caseType: "progress" | "status" | "control" | "results", value: unknown): StreamMetricsResponse {
  return create(StreamMetricsResponseSchema, {
    event: { case: caseType, value: value as never },
  } as never);
}

type FrameHandler = (id: string) => AsyncIterable<StreamMetricsResponse>;

function mockClient(handlers: {
  stream?: FrameHandler;
  failCreate?: string;
  failRun?: string;
  failStream?: string;
} = {}) {
  const calls = { create: 0, run: 0, stream: 0, pause: 0, resume: 0, stop: 0 };
  return {
    calls,
    createSimulation: vi.fn(async () => {
      calls.create++;
      if (handlers.failCreate) throw new Error(handlers.failCreate);
      return { simulation: { id: "sim-77", architecture: arch, workload, options } };
    }),
    runSimulation: vi.fn(async () => {
      calls.run++;
      if (handlers.failRun) throw new Error(handlers.failRun);
      return { simulation: { id: "sim-77" } };
    }),
    streamMetrics: vi.fn(() => {
      // NOTE: the real Connect client returns an async iterable directly
      // (NOT a promise of one); the component does `for await (…of stream)`.
      calls.stream++;
      if (handlers.failStream) throw new Error(handlers.failStream);
      const impl: FrameHandler = handlers.stream ?? async function* (id) {
        yield frame("progress", snapshot());
        yield frame("status", create(GetSimulationStatusResponseSchema, { simulationId: id, status: RunStatus.COMPLETED }));
        yield frame("results", finalResults);
      };
      return impl("sim-77");
    }),
    pauseSimulation: vi.fn(async () => { calls.pause++; return { simulationId: "sim-77", status: RunStatus.PAUSED }; }),
    resumeSimulation: vi.fn(async () => { calls.resume++; return { simulationId: "sim-77", status: RunStatus.RUNNING }; }),
    stopSimulation: vi.fn(async () => { calls.stop++; return { simulationId: "sim-77", status: RunStatus.STOPPING }; }),
    setWallDuration: vi.fn(async () => ({ simulationId: "sim-77", status: RunStatus.RUNNING })),
  };
}

type MockClient = ReturnType<typeof mockClient>;

vi.mock("@loadline/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@loadline/api")>();
  return {
    ...actual,
    createLoadlineClient: vi.fn(() => mockClientFactory()),
  };
});

let mockClientFactory: () => MockClient = () => mockClient();

/* ------------------------------------------------------------- harness --- */

function renderConsole(over: { options?: SimulationOptions } = {}) {
  const outcomes: SimOutcome[] = [];
  const onRunning = vi.fn();
  render(
    <SimConsole
      architecture={arch}
      workload={workload}
      options={over.options ?? options}
      serverUrl="http://test:8080"
      running={false}
      onRunningChange={onRunning}
      onOutcome={(o) => outcomes.push(o)}
      onSimId={vi.fn()}
    />,
  );
  return { outcomes, onRunning };
}

beforeEach(() => {
  mockClientFactory = () => mockClient();
});

/* --------------------------------------------------------------- tests --- */

describe("SimConsole run lifecycle", () => {
  it("idle state shows the run button and no metrics", () => {
    renderConsole();
    expect(screen.getByRole("button", { name: "run" })).toBeInTheDocument();
    expect(screen.queryByText("Workload plan")).not.toBeInTheDocument();
  });

  it("runs end to end: creates, streams, completes, reports outcome", async () => {
    const user = userEvent.setup();
    const { outcomes, onRunning } = renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));

    await waitFor(() => expect(outcomes).toHaveLength(1));
    expect(outcomes[0]).toEqual({ status: "completed", simId: "sim-77" });
    expect(onRunning).toHaveBeenCalledWith(true);
    expect(onRunning).toHaveBeenLastCalledWith(false);

    // Final results are rendered verbatim from the backend frame.
    await waitFor(() => expect(screen.getByText("Results")).toBeInTheDocument());
    expect(screen.getByText("34.7")).toBeInTheDocument(); // peak rps
    expect(screen.getByText("Workload plan")).toBeInTheDocument();
  });

  it("renders progress snapshots as live values while running", async () => {
    mockClientFactory = () =>
      mockClient({
        stream: async function* (id) {
          yield frame("progress", snapshot({ simTimeMs: 2500, generated: 90n, completed: 80n }));
          yield frame("control", create(ControlFrameSchema, { simulationId: id, status: RunStatus.PAUSED, simTimeMs: 2500 }));
          // Hold the stream open: the run is paused.
          await new Promise(() => {});
        },
      });
    const user = userEvent.setup();
    renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));

    await waitFor(() => expect(screen.getByText(/sim t=2,?500ms/)).toBeInTheDocument());
    // Control frame drives the status banner — not local guessing.
    expect(screen.getByText("paused")).toBeInTheDocument();
    // Pause is a control-frame state; resume becomes available.
    expect(screen.getByRole("button", { name: "resume" })).toBeEnabled();
  });

  it("reports a failed run when the backend rejects the simulation", async () => {
    mockClientFactory = () => mockClient({ failCreate: "invalid workload: peak multiplier must be >= 1" });
    const user = userEvent.setup();
    const { outcomes } = renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));

    await waitFor(() => expect(outcomes).toEqual([{ status: "failed", simId: "" }]));
    expect(await screen.findByText(/invalid workload/)).toBeInTheDocument();
  });

  it("reports failure when the stream ends with a FAILED status frame", async () => {
    mockClientFactory = () =>
      mockClient({
        stream: async function* (id) {
          yield frame("status", create(GetSimulationStatusResponseSchema, { simulationId: id, status: RunStatus.FAILED, error: "sim: invalid architecture: dependency cycle detected" }));
        },
      });
    const user = userEvent.setup();
    const { outcomes } = renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));
    await waitFor(() => expect(outcomes).toEqual([{ status: "failed", simId: "sim-77" }]));
    expect(await screen.findByText(/dependency cycle detected/)).toBeInTheDocument();
  });

  it("stop issues the control RPC and terminal STOPPED frames produce a stopped outcome with partial results", async () => {
    let stopped = false;
    mockClientFactory = () =>
      mockClient({
        stream: async function* (id) {
          yield frame("progress", snapshot({ simTimeMs: 1500, eventsProcessed: 800n }));
          yield frame("control", create(ControlFrameSchema, { simulationId: id, status: RunStatus.STOPPING, simTimeMs: 1500 }));
          yield frame("control", create(ControlFrameSchema, { simulationId: id, status: RunStatus.STOPPED, simTimeMs: 1600 }));
          yield frame("status", create(GetSimulationStatusResponseSchema, { simulationId: id, status: RunStatus.STOPPED }));
          yield frame("results", create(FinalResultsSchema, {
            metrics: create(SystemMetricsSchema, { durationMs: 1600, generated: 50n, completed: 48n }),
            summary: create(RunSummarySchema, { stopReason: "stopped" }),
          }));
          stopped = true;
        },
      });
    const user = userEvent.setup();
    const { outcomes } = renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));

    await waitFor(() => expect(outcomes).toEqual([{ status: "stopped", simId: "sim-77" }]));
    expect(stopped).toBe(true);
    // Stopped runs are explicitly marked as a partial view.
    expect(await screen.findByText(/stopped at .* of the horizon — partial view/)).toBeInTheDocument();
  });

  it("reset clears results and reports no active run", async () => {
    const user = userEvent.setup();
    const { onRunning } = renderConsole();
    await user.click(screen.getByRole("button", { name: "run" }));
    await waitFor(() => expect(screen.getByText("Results")).toBeInTheDocument());

    const resetBtn = screen.getByRole("button", { name: "reset" });
    await user.click(resetBtn);
    await waitFor(() => {
      expect(screen.queryByText("Results")).not.toBeInTheDocument();
      expect(onRunning).toHaveBeenLastCalledWith(false);
    });
  });

  it("create → stream interleave: results from a stale run never render", async () => {
    // Two sequential runs with different results; the first must not
    // leak into the second's display. One client instance serves both
    // runs (the console caches it), so runs are counted per
    // createSimulation call.
    let runCount = 0;
    const client = mockClient({
      stream: async function* (id) {
        const n = runCount === 1 ? 340n : 999n;
        yield frame("progress", snapshot());
        yield frame("status", create(GetSimulationStatusResponseSchema, { simulationId: id, status: RunStatus.COMPLETED }));
        yield frame("results", create(FinalResultsSchema, {
          metrics: create(SystemMetricsSchema, { durationMs: 10_000, generated: n, completed: n }),
        }));
      },
    });
    client.createSimulation.mockImplementation(async () => {
      runCount++;
      return { simulation: { id: `sim-${runCount}`, architecture: arch, workload, options } };
    });
    mockClientFactory = () => client;
    const user = userEvent.setup();
    renderConsole();

    await user.click(screen.getByRole("button", { name: "run" }));
    await waitFor(() => expect(screen.getAllByText("340").length).toBeGreaterThan(0));

    await user.click(screen.getByRole("button", { name: "reset" }));
    await waitFor(() => expect(screen.queryByText("340")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "run" }));
    await waitFor(() => expect(screen.getAllByText("999").length).toBeGreaterThan(0));
    expect(screen.queryByText("340")).not.toBeInTheDocument();
  });
});
