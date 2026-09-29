/**
 * Comparison state tests — the reducer contract and the row builder.
 * The row builder must be pass-through only: no scores, no ranking, no
 * derived verdicts. Every value here comes from fixture entries shaped
 * exactly like the backend's ComparisonEntry.
 */
import { describe, expect, it } from "vitest";
import { create } from "@loadline/api";
import {
  ArchitectureSchema,
  ComparisonResultSchema,
  SimulationOptionsSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import type { ComparisonEntry } from "@loadline/api";
import {
  comparisonReducer,
  comparisonRows,
  initialComparisonState,
  primaryBottleneck,
} from "./comparison";
import type { Architecture } from "@loadline/api";

function archA(): Architecture {
  return create(ArchitectureSchema, {
    schemaVersion: "1",
    name: "a",
    components: [
      { id: "client" },
      { id: "api" },
    ],
    links: [{ from: "client", to: "api" }],
  });
}

function archB(): Architecture {
  return create(ArchitectureSchema, {
    schemaVersion: "1",
    name: "b",
    components: [
      { id: "client" },
      { id: "api" },
      { id: "cache" },
    ],
    links: [
      { from: "client", to: "api" },
      { from: "api", to: "cache" },
    ],
  });
}

function entry(name: string, over: Partial<ComparisonEntry>): ComparisonEntry {
  return create(ComparisonResultSchema, {
    workload: create(WorkloadSpecSchema, { totalUsers: 10n, dau: 1n }),
    seed: 7n,
    entries: [],
  }) && ({ name, error: "", ...over } as ComparisonEntry);
}

describe("comparison reducer", () => {
  it("saves named architecture snapshots", () => {
    let s = initialComparisonState;
    s = comparisonReducer(s, { type: "save", name: "aws", architecture: archA() });
    expect(s.library).toHaveLength(1);
    expect(s.library[0].name).toBe("aws");
    expect(s.library[0].architecture.components).toHaveLength(2);
  });

  it("overwrites a save with the same name (no duplicates)", () => {
    let s = initialComparisonState;
    s = comparisonReducer(s, { type: "save", name: "aws", architecture: archA() });
    s = comparisonReducer(s, { type: "save", name: "aws", architecture: archB() });
    expect(s.library).toHaveLength(1);
    expect(s.library[0].architecture.components).toHaveLength(3);
  });

  it("rejects blank names", () => {
    let s = initialComparisonState;
    s = comparisonReducer(s, { type: "save", name: "   ", architecture: archA() });
    expect(s.library).toHaveLength(0);
  });

  it("toggles selection and removes cascade to selection", () => {
    let s = initialComparisonState;
    s = comparisonReducer(s, { type: "save", name: "aws", architecture: archA() });
    s = comparisonReducer(s, { type: "save", name: "gcp", architecture: archB() });
    s = comparisonReducer(s, { type: "toggleSelect", name: "aws" });
    s = comparisonReducer(s, { type: "toggleSelect", name: "gcp" });
    expect(s.selected).toEqual(["aws", "gcp"]);
    s = comparisonReducer(s, { type: "toggleSelect", name: "aws" });
    expect(s.selected).toEqual(["gcp"]);
    s = comparisonReducer(s, { type: "remove", name: "gcp" });
    expect(s.selected).toEqual([]);
    expect(s.library).toHaveLength(1);
  });

  it("tracks run lifecycle: started → completed / failed", () => {
    let s = initialComparisonState;
    s = comparisonReducer(s, { type: "started" });
    expect(s.running).toBe(true);
    s = comparisonReducer(s, { type: "failed", message: "boom" });
    expect(s.running).toBe(false);
    expect(s.error).toBe("boom");
    s = comparisonReducer(s, { type: "started" });
    const result = create(ComparisonResultSchema, {
      workload: create(WorkloadSpecSchema, { totalUsers: 1n, dau: 1n }),
      seed: 1n,
      entries: [],
    });
    s = comparisonReducer(s, { type: "completed", result });
    expect(s.running).toBe(false);
    expect(s.result?.seed).toBe(1n);
  });
});

describe("comparisonRows", () => {
  const a = entry("a", {
    plan: {
      peakRps: 100,
    } as ComparisonEntry["plan"],
    metrics: {
      completed: 1000n,
      rejected: 5n,
      failed: 2n,
      avgLatencyMs: 10,
      p50Ms: 9,
      p95Ms: 20,
      p99Ms: 30,
      errorRate: 0.002,
      timeoutRate: 0.001,
      components: [
        { id: "api", maxQueueDepth: 4, utilization: 0.5 },
        { id: "db", maxQueueDepth: 9, utilization: 0.8 },
      ],
    } as unknown as ComparisonEntry["metrics"],
    capacity: [
      { componentId: "api", headroom: 0.5 },
      { componentId: "db", headroom: 0.2 },
    ] as ComparisonEntry["capacity"],
    cost: { total: 123.45 } as ComparisonEntry["cost"],
  });
  const b = entry("b", {
    metrics: {
      completed: 900n,
      rejected: 0n,
      failed: 0n,
      avgLatencyMs: 12,
      p50Ms: 11,
      p95Ms: 25,
      p99Ms: 40,
      errorRate: 0,
      timeoutRate: 0,
      components: [{ id: "api", maxQueueDepth: 2, utilization: 0.3 }],
    } as unknown as ComparisonEntry["metrics"],
    capacity: [{ componentId: "api", headroom: 0.7 }] as ComparisonEntry["capacity"],
  });

  it("produces one row per metric with per-entry values in order", () => {
    const rows = comparisonRows([a, b]);
    const names = rows.map((r) => r.metric);
    expect(names).toContain("p95");
    expect(names).toContain("Est. monthly cost");
    expect(names).toContain("Headroom (worst component)");
    const p95 = rows.find((r) => r.metric === "p95")!;
    expect(p95.values).toEqual([20, 25]);
    const cost = rows.find((r) => r.metric === "Est. monthly cost")!;
    expect(cost.values).toEqual([123.45, null]);
  });

  it("takes worst-case queue depth, utilization, and headroom", () => {
    const rows = comparisonRows([a, b]);
    const q = rows.find((r) => r.metric === "Max queue depth")!;
    expect(q.values).toEqual([9, 2]);
    const u = rows.find((r) => r.metric === "Max utilization")!;
    expect(u.values).toEqual([0.8, 0.3]);
    const h = rows.find((r) => r.metric === "Headroom (worst component)")!;
    expect(h.values).toEqual([0.2, 0.7]);
  });

  it("is pass-through only: no score, rank, or verdict row exists", () => {
    const rows = comparisonRows([a, b]);
    for (const r of rows) {
      expect(r.metric.toLowerCase()).not.toContain("score");
      expect(r.metric.toLowerCase()).not.toContain("rank");
      expect(r.metric.toLowerCase()).not.toContain("best");
    }
    expect(rows.find((r) => r.metric === "Overall")).toBeUndefined();
  });

  it("nulls values for failed entries but keeps other entries intact", () => {
    const failed = entry("bad", { error: "validation failed" });
    const rows = comparisonRows([failed, a]);
    const p95 = rows.find((r) => r.metric === "p95")!;
    expect(p95.values[0]).toBeNull();
    expect(p95.values[1]).toBe(20);
  });
});

describe("primaryBottleneck", () => {
  it("returns the backend's first (worst) bottleneck id", () => {
    const e = entry("a", {
      diagnosis: {
        bottlenecks: [
          { componentId: "db", severity: "critical", reasons: [], impacts: [], kind: "database" },
          { componentId: "api", severity: "high", reasons: [], impacts: [], kind: "api_server" },
        ],
        impacts: [],
        healthy: false,
        summary: "2 bottleneck(s)",
      } as unknown as ComparisonEntry["diagnosis"],
    });
    expect(primaryBottleneck(e)).toBe("db");
  });

  it("returns null for healthy or failed entries", () => {
    expect(
      primaryBottleneck(entry("a", { diagnosis: { bottlenecks: [], impacts: [], healthy: true, summary: "" } as unknown as ComparisonEntry["diagnosis"] })),
    ).toBeNull();
    expect(primaryBottleneck(entry("bad", { error: "x" }))).toBeNull();
  });
});

describe("shared options snapshot", () => {
  it("setSharedInputs snapshots workload and options", () => {
    let s = initialComparisonState;
    const wl = create(WorkloadSpecSchema, {
      totalUsers: 1000n, dau: 100n, requestsPerUserPerDay: 10,
      peakMultiplier: 3, readWriteRatio: 4, payloadBytes: 512n,
    });
    const opts = create(SimulationOptionsSchema, { seed: 42n, durationMs: 30000 });
    s = comparisonReducer(s, { type: "setSharedInputs", workload: wl, options: opts });
    expect(s.workload?.dau).toBe(100n);
    expect(s.options?.seed).toBe(42n);
  });
});
