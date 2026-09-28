/**
 * Analysis panel tests: the terminal run surfaces (results metrics,
 * bottleneck diagnosis, capacity, cost) render backend values verbatim
 * and present honest empty states before any run exists.
 *
 * The fixtures here are shaped like real backend responses (they are
 * created through the same generated protobuf schemas the app uses);
 * no test invents simulation math.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { create } from "@loadline/api";
import {
  CostEstimateSchema,
  DiagnosisSchema,
  BottleneckSchema,
  CapacityReportSchema,
  SystemMetricsSchema,
} from "@loadline/api";
import { BottleneckPanel, CapacityPanel, CostPanel } from "./analysis";

const metrics = create(SystemMetricsSchema, {
  durationMs: 10_000,
  generated: 46_303n,
  completed: 29_609n,
  rejected: 16_539n,
  failed: 32n,
  errorRate: 0.07,
  p50Ms: 25.5,
  p95Ms: 33.5,
  p99Ms: 207.2,
  maxLatencyMs: 1610.6,
  components: [],
});

describe("BottleneckPanel", () => {
  it("shows an honest empty state before any run", () => {
    render(<BottleneckPanel diagnosis={null} metrics={null} />);
    expect(screen.getByText(/no diagnosis/i)).toBeInTheDocument();
  });

  it("renders the clear verdict when the backend reports a healthy run", () => {
    render(
      <BottleneckPanel
        diagnosis={create(DiagnosisSchema, { healthy: true, summary: "no bottleneck(s)" })}
        metrics={metrics}
      />,
    );
    expect(screen.getByText(/no bottleneck/i)).toBeInTheDocument();
  });

  it("renders the primary bottleneck with severity, reasons, and impacts verbatim", () => {
    const diagnosis = create(DiagnosisSchema, {
      healthy: false,
      summary: "1 bottleneck(s); primary: db (critical)",
      bottlenecks: [
        create(BottleneckSchema, {
          componentId: "db",
          kind: "database",
          severity: "critical",
          reasons: [
            "rejected 16539 of 18990 arrivals (87.1%) — admission capacity exhausted",
            "incoming load 949.5 RPS exceeds modeled capacity 61.5 RPS",
          ],
          impacts: ["load shedding active: 16539 requests rejected"],
        }),
      ],
      impacts: ["throughput decreased 36% vs baseline (46231 → 29609 completed)"],
    });
    render(<BottleneckPanel diagnosis={diagnosis} metrics={metrics} />);

    expect(screen.getByText("BOTTLENECK")).toBeInTheDocument();
    expect(screen.getByText("db")).toBeInTheDocument();
    expect(screen.getByText("critical")).toBeInTheDocument();
    expect(
      screen.getByText(/rejected 16539 of 18990 arrivals/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/incoming load 949.5 RPS exceeds modeled capacity/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/throughput decreased 36% vs baseline/),
    ).toBeInTheDocument();
  });

  it("lists secondary bottlenecks", () => {
    const diagnosis = create(DiagnosisSchema, {
      bottlenecks: [
        create(BottleneckSchema, { componentId: "db", severity: "critical", reasons: ["r1"] }),
        create(BottleneckSchema, { componentId: "api", severity: "high", reasons: ["r2", "r3"] }),
      ],
    });
    render(<BottleneckPanel diagnosis={diagnosis} metrics={metrics} />);
    expect(screen.getByText(/secondary — 1/)).toBeInTheDocument();
    expect(screen.getByText("api")).toBeInTheDocument();
  });
});

describe("CapacityPanel", () => {
  it("explains that capacity needs provider-backed components", () => {
    render(<CapacityPanel reports={[]} />);
    expect(screen.getByText(/no capacity estimates/i)).toBeInTheDocument();
  });

  it("renders current load, ceiling, headroom, and flags", () => {
    render(
      <CapacityPanel
        reports={[
          create(CapacityReportSchema, {
            componentId: "db",
            provider: "aws",
            service: "rds",
            currentRps: 949.5,
            maxSustainableRps: 1600,
            utilization: 0.59,
            headroom: 0.41,
            saturated: true,
            bottleneck: true,
            assumptions: ["ceiling = concurrency 8 ÷ service 0.005s ≈ 1600 RPS"],
          }),
        ]}
      />,
    );
    expect(screen.getByText("aws/rds")).toBeInTheDocument();
    expect(screen.getByText("saturated")).toBeInTheDocument();
    expect(screen.getByText("bottleneck")).toBeInTheDocument();
    expect(screen.getByText("950")).toBeInTheDocument(); // formatted value
    expect(screen.getByText("1,600")).toBeInTheDocument();
    expect(screen.getByText("41.0%")).toBeInTheDocument(); // headroom
    expect(
      screen.getByText(/ceiling = concurrency 8 ÷ service 0.005s/),
    ).toBeInTheDocument();
  });
});

describe("CostPanel", () => {
  it("shows an empty state without an estimate", () => {
    render(<CostPanel estimate={null} />);
    expect(screen.getByText(/no cost estimate/i)).toBeInTheDocument();
  });

  it("renders the total, categories, and assumption trail", () => {
    render(
      <CostPanel
        estimate={create(CostEstimateSchema, {
          currency: "USD",
          total: 2859.46,
          byCategory: { compute: 1200.5, database: 1658.96 },
          assumptions: [
            "prices are LOCAL ESTIMATES for mid-tier plans, primary commercial regions; not live billing data",
          ],
        })}
      />,
    );
    expect(screen.getByText("$2859.46")).toBeInTheDocument();
    expect(screen.getByText("compute")).toBeInTheDocument();
    expect(screen.getByText("$1200.50")).toBeInTheDocument();
    expect(screen.getByText("database")).toBeInTheDocument();
    expect(screen.getByText(/not live billing data/)).toBeInTheDocument();
  });
});
