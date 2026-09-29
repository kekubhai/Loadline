/**
 * Chart tests: every chart is a scaled view of simulator output. The
 * assertions below pin the two properties the product depends on:
 *
 *  1. values are rendered verbatim (no chart rounds a measurement into a
 *     different number, and a missing backend model stays missing),
 *  2. geometry is derived from the data (bar length = value / scale, the
 *     scale being the largest value in the row) — never from a fixed
 *     multiplier or a random walk.
 *
 * Fixtures are protobuf messages created through the same generated
 * schemas the app uses.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { create } from "@loadline/api";
import {
  ComponentMetricsSchema,
  ComparisonEntrySchema,
  CostEstimateSchema,
  FailureRecordSchema,
  SystemMetricsSchema,
} from "@loadline/api";
import {
  ComparisonBars,
  comparisonSeries,
  FailureTimeline,
  LatencyProfile,
  LoadVsCapacity,
  OutcomeMix,
} from "./charts";

function comp(over: Partial<Parameters<typeof create>[1]> = {}) {
  return create(ComponentMetricsSchema, {
    id: "api",
    kind: "api_server",
    ...over,
  });
}

/** Widths of the fills/segments in render order, as floats. */
function widths(container: HTMLElement, selector: string): number[] {
  return [...container.querySelectorAll<HTMLElement>(selector)].map((el) =>
    parseFloat(el.style.width),
  );
}

describe("LatencyProfile", () => {
  const metrics = create(SystemMetricsSchema, {
    durationMs: 10_000,
    avgLatencyMs: 12.5,
    p50Ms: 10,
    p95Ms: 33.5,
    p99Ms: 207.2,
    maxLatencyMs: 1610.6,
    components: [],
  });

  it("shows an honest empty state before any run", () => {
    render(<LatencyProfile metrics={null} budgetMs={500} />);
    expect(screen.getByText(/no latency measurements/i)).toBeInTheDocument();
  });

  it("renders the backend percentiles verbatim", () => {
    const { container } = render(
      <LatencyProfile metrics={metrics} budgetMs={500} />,
    );
    for (const label of ["average", "p50", "p95", "p99", "max"]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByText("12.50 ms")).toBeInTheDocument();
    expect(screen.getByText("33.50 ms")).toBeInTheDocument();
    expect(screen.getByText("207.20 ms")).toBeInTheDocument();
    expect(screen.getByText("1610.60 ms")).toBeInTheDocument();
    // Five rows, five bars — nothing extra is drawn.
    expect(container.querySelectorAll(".chart-fill")).toHaveLength(5);
  });

  it("scales bars to the largest value plus the timeout budget", () => {
    const { container } = render(
      <LatencyProfile metrics={metrics} budgetMs={800} />,
    );
    const scale = 1610.6; // max latency is the largest value on the row
    const bars = widths(container, ".chart-fill");
    expect(bars[1]).toBeCloseTo((10 / scale) * 100, 5); // p50
    expect(bars[3]).toBeCloseTo((207.2 / scale) * 100, 5); // p99
    expect(bars[4]).toBeCloseTo(100, 5); // max fills the track
  });

  it("flags percentiles measured beyond the timeout budget", () => {
    render(<LatencyProfile metrics={metrics} budgetMs={50} />);
    // p99 (207.2ms) and max (1610.6ms) are past the 50ms budget; the
    // three values under it are not flagged.
    expect(screen.getAllByText(/over budget/)).toHaveLength(2);
    expect(screen.getByText(/50ms timeout budget/)).toBeInTheDocument();
  });
});

describe("LoadVsCapacity", () => {
  it("shows an honest empty state when no component metrics exist", () => {
    render(<LoadVsCapacity components={[]} />);
    expect(screen.getByText(/no per-component metrics/i)).toBeInTheDocument();
  });

  it("draws the arrival bar against the shared scale and marks utilization", () => {
    const { container } = render(
      <LoadVsCapacity
        components={[
          comp({ id: "api", arrivalRps: 800, capacityRps: 1000, utilization: 0.8 }),
          comp({ id: "db", arrivalRps: 1200, capacityRps: 0, utilization: 0 }),
        ]}
      />,
    );
    const scale = 1200;
    const bars = widths(container, ".chart-fill");
    expect(bars[0]).toBeCloseTo((800 / scale) * 100, 5);
    expect(bars[1]).toBeCloseTo(100, 5);
    expect(screen.getByText("800.0 rps")).toBeInTheDocument();
    expect(screen.getByText("1200.0 rps")).toBeInTheDocument();
    expect(screen.getByText(/80.0% util/)).toBeInTheDocument();
  });

  it("says when a component has no capacity model instead of inventing a ceiling", () => {
    render(
      <LoadVsCapacity components={[comp({ id: "cdn", arrivalRps: 500, capacityRps: 0 })]} />,
    );
    expect(screen.getByText(/no ceiling model/)).toBeInTheDocument();
    expect(screen.queryByText(/util/)).not.toBeInTheDocument();
  });

  it("marks a saturated component as over its ceiling", () => {
    render(
      <LoadVsCapacity
        components={[
          comp({ id: "db", arrivalRps: 900, capacityRps: 700, utilization: 1.3, saturated: true }),
        ]}
      />,
    );
    expect(screen.getByText(/over ceiling/)).toBeInTheDocument();
  });
});

describe("OutcomeMix", () => {
  it("stacks completed/rejected/failed to the component's own arrival count", () => {
    const { container } = render(
      <OutcomeMix
        components={[
          comp({
            id: "api",
            arrived: 100n,
            completed: 70n,
            rejected: 25n,
            failed: 5n,
          }),
        ]}
      />,
    );
    const segs = widths(container, ".chart-seg");
    expect(segs).toEqual([70, 25, 5]);
    expect(screen.getByText("100")).toBeInTheDocument();
    expect(screen.getByText("70 · 25 · 5")).toBeInTheDocument();
  });

  it("shares one scale across components", () => {
    const { container } = render(
      <OutcomeMix
        components={[
          comp({ id: "a", arrived: 50n, completed: 50n, rejected: 0n, failed: 0n }),
          comp({ id: "b", arrived: 200n, completed: 150n, rejected: 40n, failed: 10n }),
        ]}
      />,
    );
    const segs = widths(container, ".chart-seg");
    expect(segs[0]).toBeCloseTo((50 / 200) * 100, 5);
    expect(segs[3]).toBeCloseTo((150 / 200) * 100, 5);
  });

  it("shows an empty state when nothing saw traffic", () => {
    render(<OutcomeMix components={[comp({ id: "idle", arrived: 0n })]} />);
    expect(screen.getByText(/no component saw traffic/i)).toBeInTheDocument();
  });
});

describe("FailureTimeline", () => {
  it("shows an empty state when the run recorded no failures", () => {
    render(<FailureTimeline failures={[]} durationMs={10_000} />);
    expect(screen.getByText(/no request-level failures/i)).toBeInTheDocument();
  });

  it("bins the simulator's own failure timestamps", () => {
    const failures = [0, 2500, 5000, 7500].map((atMs, i) =>
      create(FailureRecordSchema, {
        requestId: BigInt(i + 1),
        componentId: "db",
        kind: "timeout",
        atMs,
      }),
    );
    const { container } = render(
      <FailureTimeline failures={failures} durationMs={10_000} />,
    );
    expect(screen.getByText(/4 records across 10.0s/)).toBeInTheDocument();
    const cols = [...container.querySelectorAll<HTMLElement>(".chart-col")];
    expect(cols).toHaveLength(24);
    expect(cols.filter((c) => c.classList.contains("chart-col-hot"))).toHaveLength(4);
  });
});

describe("ComparisonBars", () => {
  const ok = create(ComparisonEntrySchema, {
    name: "cache",
    metrics: create(SystemMetricsSchema, {
      durationMs: 10_000,
      p95Ms: 40,
      p99Ms: 90,
      errorRate: 0.02,
      completed: 1000n,
      components: [],
    }),
    cost: create(CostEstimateSchema, { total: 12.5 }),
  });
  const failed = create(ComparisonEntrySchema, {
    name: "no cache",
    error: "component \"db\" has an unknown kind",
  });

  it("builds one series per metric from the comparison entries", () => {
    const series = comparisonSeries([ok, failed]);
    expect(series.map((s) => s.label)).toEqual([
      "p95 latency (ms)",
      "p99 latency (ms)",
      "error rate",
      "completed requests",
      "estimated monthly cost",
    ]);
    expect(series[0].values).toEqual([
      { name: "cache", value: 40 },
      { name: "no cache", value: null },
    ]);
    expect(series[2].values[0].value).toBeCloseTo(0.02, 10);
    expect(series[4].values[0].value).toBeCloseTo(12.5, 10);
  });

  it("renders values verbatim and dashes a missing result", () => {
    render(<ComparisonBars series={comparisonSeries([ok, failed])} />);
    expect(screen.getByText("40.00 ms")).toBeInTheDocument();
    expect(screen.getByText("90.00 ms")).toBeInTheDocument();
    expect(screen.getByText("2.0%")).toBeInTheDocument();
    expect(screen.getByText("1,000")).toBeInTheDocument();
    expect(screen.getByText("$12.50")).toBeInTheDocument();
    // The failed architecture has no value for any metric: five dashes,
    // never a zero-length bar pretending to be a measurement.
    expect(screen.getAllByText("\u2014")).toHaveLength(5);
  });

  it("scales each metric group to its own largest value", () => {
    const second = create(ComparisonEntrySchema, {
      name: "slower",
      metrics: create(SystemMetricsSchema, {
        durationMs: 10_000,
        p95Ms: 160,
        p99Ms: 200,
        errorRate: 0.25,
        completed: 4000n,
        components: [],
      }),
    });
    const { container } = render(
      <ComparisonBars series={comparisonSeries([ok, second])} />,
    );
    const groups = [...container.querySelectorAll<HTMLElement>(".chart-group")];
    expect(groups).toHaveLength(5);
    const p95 = groups[0].querySelectorAll<HTMLElement>(".chart-fill");
    expect(parseFloat(p95[0].style.width)).toBeCloseTo((40 / 160) * 100, 5);
    expect(parseFloat(p95[1].style.width)).toBeCloseTo(100, 5);
  });

  it("shows an empty state when no entry has a result", () => {
    render(<ComparisonBars series={comparisonSeries([failed])} />);
    expect(screen.getByText(/no comparable metrics/i)).toBeInTheDocument();
  });
});
