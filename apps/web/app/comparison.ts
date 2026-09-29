/**
 * LOADLINE — comparison state (pure logic, no JSX).
 *
 * Owns the architecture-comparison workflow state: a named library of
 * saved architectures, the selection for a comparison run, and the
 * per-architecture results returned by RunComparison. This module only
 * sequences state — every metric, bottleneck, capacity number, and cost
 * line comes verbatim from the backend's simulation runs. No scores, no
 * ranking, no derived verdicts: the table renders what the backend sent.
 */
import { create } from "@loadline/api";
import {
  ArchitectureSchema,
  ComparisonResultSchema,
  WorkloadSpecSchema,
  SimulationOptionsSchema,
} from "@loadline/api";
import type {
  Architecture,
  ComparisonEntry,
  ComparisonResult,
  Failure,
  WorkloadSpec,
  SimulationOptions,
} from "@loadline/api";

/** One saved architecture in the comparison library. */
export interface SavedArchitecture {
  name: string;
  architecture: Architecture;
}

export interface ComparisonState {
  /** Saved architecture library (named, serialized snapshots). */
  library: SavedArchitecture[];
  /** Names selected for the next comparison run (≥ 2 to enable). */
  selected: string[];
  /** Shared workload sent to every architecture. */
  workload: WorkloadSpec | null;
  /** Shared options: seed, duration, retries, and the failure scenario. */
  options: SimulationOptions | null;
  /** Backend result of the last comparison run (null before any run). */
  result: ComparisonResult | null;
  /** True while the comparison RPC is in flight. */
  running: boolean;
  error: string;
}

export const initialComparisonState: ComparisonState = {
  library: [],
  selected: [],
  workload: null,
  options: null,
  result: null,
  running: false,
  error: "",
};

export type ComparisonAction =
  | { type: "save"; name: string; architecture: Architecture }
  | { type: "remove"; name: string }
  | { type: "toggleSelect"; name: string }
  | { type: "setSharedInputs"; workload: WorkloadSpec; options: SimulationOptions }
  | { type: "started" }
  | { type: "completed"; result: ComparisonResult }
  | { type: "failed"; message: string }
  | { type: "reset" };

export function comparisonReducer(s: ComparisonState, a: ComparisonAction): ComparisonState {
  switch (a.type) {
    case "save": {
      const name = a.name.trim();
      if (!name) return s;
      // Save = snapshot the architecture document as-is (proto clone).
      const snapshot = create(ArchitectureSchema, a.architecture);
      const existing = s.library.findIndex((e) => e.name === name);
      const entry: SavedArchitecture = { name, architecture: snapshot };
      const library =
        existing >= 0
          ? s.library.map((e, i) => (i === existing ? entry : e))
          : [...s.library, entry];
      return { ...s, library };
    }
    case "remove": {
      const library = s.library.filter((e) => e.name !== a.name);
      const selected = s.selected.filter((n) => n !== a.name);
      return { ...s, library, selected };
    }
    case "toggleSelect": {
      const selected = s.selected.includes(a.name)
        ? s.selected.filter((n) => n !== a.name)
        : [...s.selected, a.name];
      return { ...s, selected };
    }
    case "setSharedInputs":
      return {
        ...s,
        workload: create(WorkloadSpecSchema, a.workload),
        options: create(SimulationOptionsSchema, {
          ...a.options,
          failures: (a.options.failures ?? []).map((f) => f) as Failure[],
        }),
      };
    case "started":
      return { ...s, running: true, error: "", result: null };
    case "completed":
      return { ...s, running: false, result: create(ComparisonResultSchema, a.result) };
    case "failed":
      return { ...s, running: false, error: a.message };
    case "reset":
      return initialComparisonState;
  }
}

/* --------------------------------------------------------------- helpers -- */

/** Rows of the comparison table, in fixed metric order. Values are raw. */
export interface ComparisonRow {
  metric: string;
  unit: string;
  /** Per-entry raw values, aligned with the entries array. Null = absent. */
  values: (number | null)[];
  /** Rawer-is-better direction, for optional display emphasis only. */
  lowerBetter: boolean;
}

/**
 * Build the metric rows from the backend entries. ONLY pass-through:
 * every number is read from the entry's metrics/capacity/cost — no
 * derived scores, no weighting, no normalization.
 */
export function comparisonRows(entries: ComparisonEntry[]): ComparisonRow[] {
  // All entries stay in the values arrays (null when failed) so table
  // columns stay aligned with the entry list.
  const ok = entries;
  return [
    {
      metric: "Peak RPS (workload)",
      unit: "rps",
      values: ok.map((e) => e.plan?.peakRps ?? null),
      lowerBetter: false,
    },
    {
      metric: "Throughput (completed)",
      unit: "req",
      values: ok.map((e) => num(e.metrics?.completed)),
      lowerBetter: false,
    },
    {
      metric: "Rejected",
      unit: "req",
      values: ok.map((e) => num(e.metrics?.rejected)),
      lowerBetter: true,
    },
    {
      metric: "Failed",
      unit: "req",
      values: ok.map((e) => num(e.metrics?.failed)),
      lowerBetter: true,
    },
    {
      metric: "Avg latency",
      unit: "ms",
      values: ok.map((e) => e.metrics?.avgLatencyMs ?? null),
      lowerBetter: true,
    },
    {
      metric: "p50",
      unit: "ms",
      values: ok.map((e) => e.metrics?.p50Ms ?? null),
      lowerBetter: true,
    },
    {
      metric: "p95",
      unit: "ms",
      values: ok.map((e) => e.metrics?.p95Ms ?? null),
      lowerBetter: true,
    },
    {
      metric: "p99",
      unit: "ms",
      values: ok.map((e) => e.metrics?.p99Ms ?? null),
      lowerBetter: true,
    },
    {
      metric: "Error rate",
      unit: "%",
      values: ok.map((e) => e.metrics?.errorRate ?? null),
      lowerBetter: true,
    },
    {
      metric: "Timeout rate",
      unit: "%",
      values: ok.map((e) => e.metrics?.timeoutRate ?? null),
      lowerBetter: true,
    },
    {
      metric: "Max queue depth",
      unit: "req",
      values: ok.map((e) => maxOf(e.metrics?.components?.map((c) => c.maxQueueDepth))),
      lowerBetter: true,
    },
    {
      metric: "Max utilization",
      unit: "%",
      values: ok.map((e) => maxOf(e.metrics?.components?.map((c) => c.utilization))),
      lowerBetter: true,
    },
    {
      metric: "Headroom (worst component)",
      unit: "%",
      values: ok.map((e) => {
        const caps = e.capacity ?? [];
        if (caps.length === 0) return null;
        return Math.min(...caps.map((r) => r.headroom));
      }),
      lowerBetter: false,
    },
    {
      metric: "Est. monthly cost",
      unit: "usd",
      values: ok.map((e) => (e.cost ? e.cost.total : null)),
      lowerBetter: true,
    },
  ];
}

function maxOf(xs: number[] | undefined): number | null {
  if (!xs || xs.length === 0) return null;
  return Math.max(...xs);
}

/** uint64 proto fields arrive as bigint; normalize to number for rows. */
function num(v: bigint | number | undefined | null): number | null {
  if (v === undefined || v === null) return null;
  return Number(v);
}

/** Primary bottleneck per entry: the backend's first (worst) component. */
export function primaryBottleneck(e: ComparisonEntry): string | null {
  if (e.error !== "") return null;
  const b = e.diagnosis?.bottlenecks?.[0];
  return b ? b.componentId : null;
}
