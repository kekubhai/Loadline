/**
 * LOADLINE — cross-stack end-to-end test.
 *
 * Architecture → Workload → Run → Backend → Simulation → Results →
 * Frontend-shaped assertions, using the REAL workspace TS client
 * (@loadline/api, binary Connect proto over fetch) against the REAL Go
 * server binary over real TCP. No mocks at any layer — this is exactly
 * the code path the browser uses.
 *
 * Prereq: loadline-server listening on E2E_BASE_URL (default
 * http://127.0.0.1:8080). Start it with:
 *
 *   cd apps/simulator && go run ./cmd/loadline-server -addr 127.0.0.1:8080
 *
 * Run with (from the repo root):
 *
 *   cd tests/e2e && E2E_BASE_URL=http://127.0.0.1:8080 npx vitest run --root .
 */
import { describe, expect, it, beforeAll } from "vitest";
import {
  create,
  createLoadlineClient,
  ArchitectureSchema,
  ComponentKind,
  FailureConfigSchema,
  FailureSchema,
  FailureType,
  LinkSchema,
  ProviderConfigSchema,
  RunStatus,
  SimulationOptionsSchema,
  WorkloadSpecSchema,
} from "../../packages/api/src/index";
import type {
  LoadlineClient,
  SystemMetrics,
  Failure,
} from "../../packages/api/src/index";

const BASE = process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080";

/* ----------------------------------------------------------- fixtures --- */

// Mirrors the page's INITIAL_ARCHITECTURE: Client → Lambda → ElastiCache →
// RDS. Provider-backed so capacity + cost are eligible.
function arch() {
  return create(ArchitectureSchema, {
    schemaVersion: "1",
    name: "e2e-web",
    components: [
      { id: "client", kind: ComponentKind.CLIENT },
      {
        id: "api",
        kind: ComponentKind.API_SERVER,
        provider: "aws",
        service: "lambda",
        config: create(ProviderConfigSchema, { memoryMb: 512 }),
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
        config: create(ProviderConfigSchema, { storageGb: 100 }),
      },
    ],
    links: [
      create(LinkSchema, { from: "client", to: "api" }),
      create(LinkSchema, { from: "api", to: "cache" }),
      create(LinkSchema, { from: "cache", to: "db" }),
    ],
  });
}

function workload() {
  return create(WorkloadSpecSchema, {
    totalUsers: 10_000_000n,
    dau: 1_000_000n,
    requestsPerUserPerDay: 40,
    peakMultiplier: 5,
    readWriteRatio: 4,
    payloadBytes: 4096n,
  });
}

function options(failures: Failure[] = []) {
  return create(SimulationOptionsSchema, {
    seed: 7n,
    durationMs: 10_000,
    maxRetries: 2,
    backoffBaseMs: 5,
    timeoutMs: 50,
    retryOn: ["api", "cache"],
    failures,
  });
}

function componentById(m: SystemMetrics, id: string) {
  const c = m.components.find((x) => x.id === id);
  if (!c) throw new Error(`component ${id} missing from metrics`);
  return c;
}

/* --------------------------------------------------------------- suite --- */

let client: LoadlineClient;
const createdIds: string[] = [];

beforeAll(() => {
  client = createLoadlineClient({ baseUrl: BASE });
});

describe("cross-stack end-to-end", () => {
  it("the frontend-shaped workflow produces real backend results", async () => {
    // 1. Create: serialize the editor state into a simulation request.
    const created = await client.createSimulation({
      architecture: arch(),
      workload: workload(),
      options: options(),
    });
    const id = created.simulation?.id ?? "";
    expect(id).not.toBe("");
    createdIds.push(id);

    // 2. Run (as-fast-as-possible pacing).
    await client.runSimulation({ simulationId: id, wallDurationMs: 0 });

    // 3. Poll status to terminal (the console would stream; the contract
    // is the same terminal status). Status arrives as a numeric enum.
    let status: RunStatus = RunStatus.PENDING;
    for (let i = 0; i < 300; i++) {
      const st = await client.getSimulationStatus({ simulationId: id });
      status = st.status;
      if (status !== RunStatus.PENDING && status !== RunStatus.RUNNING) break;
      await new Promise((r) => setTimeout(r, 100));
    }
    expect(status).toBe(RunStatus.COMPLETED);

    // 4. Results: conservation + percentiles + per-component reports.
    const res = await client.getResults({ simulationId: id });
    const m = res.metrics!;
    expect(Number(m.generated)).toBeGreaterThan(0);
    expect(Number(m.completed)).toBeGreaterThan(0);
    // Conservation: Generated = Completed + Rejected + Failed + InFlight.
    expect(
      Number(m.generated),
    ).toBe(
      Number(m.completed) + Number(m.rejected ?? 0) + Number(m.failed ?? 0) + Number(m.inFlight ?? 0),
    );
    // Percentile ordering on real data.
    expect(m.p50Ms).toBeGreaterThan(0);
    expect(m.p95Ms).toBeGreaterThanOrEqual(m.p50Ms);
    expect(m.p99Ms).toBeGreaterThanOrEqual(m.p95Ms);
    expect(m.maxLatencyMs).toBeGreaterThanOrEqual(m.p99Ms);
    // Per-component reports for every node in the architecture.
    expect(m.components).toHaveLength(4);
    // The derived workload plan is echoed back.
    expect(res.plan!.peakRps).toBeGreaterThan(0);
    expect(res.summary!.eventsProcessed).toBeGreaterThan(0);

    // 5. Capacity: headroom + utilization = 1, assumptions recorded.
    const cap = await client.getCapacity({ simulationId: id });
    expect(cap.reports).toHaveLength(3);
    for (const r of cap.reports) {
      expect(r.maxSustainableRps).toBeGreaterThan(0);
      expect(r.utilization + r.headroom).toBeCloseTo(1, 3);
      expect(r.assumptions.length).toBeGreaterThan(0);
    }

    // 6. Cost: positive estimate with the ESTIMATE marker.
    const cost = await client.getCostEstimate({ simulationId: id });
    expect(cost.estimate!.total).toBeGreaterThan(0);
    expect(
      cost.estimate!.assumptions.some((a) => a.includes("not live billing")),
    ).toBe(true);
  });

  it("failure injection changes results; diagnosis explains the cascade", async () => {
    // Baseline (healthy) run — same seed as the failed run below.
    const base = await client.createSimulation({
      architecture: arch(),
      workload: workload(),
      options: options(),
    });
    const baseID = base.simulation!.id!;
    createdIds.push(baseID);
    await client.runSimulation({ simulationId: baseID });
    for (let i = 0; i < 300; i++) {
      const st = await client.getSimulationStatus({ simulationId: baseID });
      if (st.status === RunStatus.COMPLETED) break;
      if (st.status === RunStatus.FAILED) throw new Error(st.error);
      await new Promise((r) => setTimeout(r, 100));
    }

    // Cache crash with pass-through: reads bypass to the DB. This is the
    // exact injection the frontend's failure panel schedules.
    const crashOpts = options([
      create(FailureSchema, {
        target: "cache",
        type: FailureType.CRASH,
        startMs: 1000,
        durationMs: 5000,
        config: create(FailureConfigSchema, { passThrough: true }),
      }),
    ]);
    const crash = await client.createSimulation({
      architecture: arch(),
      workload: workload(),
      options: crashOpts,
    });
    const crashID = crash.simulation!.id!;
    createdIds.push(crashID);
    await client.runSimulation({ simulationId: crashID });
    for (let i = 0; i < 300; i++) {
      const st = await client.getSimulationStatus({ simulationId: crashID });
      if (st.status === RunStatus.COMPLETED) break;
      if (st.status === RunStatus.FAILED) throw new Error(st.error);
      await new Promise((r) => setTimeout(r, 100));
    }

    // Same seed → identical arrivals; the crash must shift REAL load.
    const resB = await client.getResults({ simulationId: baseID });
    const resC = await client.getResults({ simulationId: crashID });
    const dbBase = Number(componentById(resB.metrics!, "db").arrived);
    const dbCrash = Number(componentById(resC.metrics!, "db").arrived);
    expect(dbCrash).toBeGreaterThan(dbBase);

    // The cascade raises tail latency: cache misses pay the DB queue.
    expect(resC.metrics!.p99Ms).toBeGreaterThan(resB.metrics!.p99Ms);

    // Diagnosis vs the healthy baseline: db flagged critical with
    // machine-computed reasons.
    const diag = await client.getDiagnosis({
      simulationId: crashID,
      baselineSimulationId: baseID,
    });
    expect(diag.diagnosis!.healthy).toBe(false);
    const db = diag.diagnosis!.bottlenecks.find((b) => b.componentId === "db");
    expect(db).toBeDefined();
    expect(db!.severity).toBe("critical");
    expect(db!.reasons.length).toBeGreaterThan(0);
    // Baseline deltas are reported.
    expect(
      diag.diagnosis!.impacts.some(
        (i) => i.includes("throughput decreased") || i.includes("latency increased"),
      ),
    ).toBe(true);
  });

  it("invalid workload is rejected cleanly with a structured error", async () => {
    const bad = workload();
    bad.peakMultiplier = 0.5; // must be >= 1
    await expect(
      client.createSimulation({ architecture: arch(), workload: bad, options: options() }),
    ).rejects.toThrow(/peak/i);
  });

  it("invalid architecture is rejected cleanly", async () => {
    const bad = arch();
    bad.components[1].service = "not_a_service";
    await expect(
      client.createSimulation({ architecture: bad, workload: workload(), options: options() }),
    ).rejects.toThrow();
  });

  it("unknown run IDs produce clean not-found errors", async () => {
    await expect(
      client.getResults({ simulationId: "sim-404" }),
    ).rejects.toThrow();
    await expect(
      client.runSimulation({ simulationId: "sim-404" }),
    ).rejects.toThrow();
  });
});
