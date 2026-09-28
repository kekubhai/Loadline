/**
 * WorkloadPanel tests: inputs edit the canonical spec; the derivation
 * chain shows "—" until the BACKEND reports a plan (the editor refuses
 * to guess what the simulator will derive), then renders the real
 * backend values.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { create } from "@loadline/api";
import { LoadPlanSchema, WorkloadSpecSchema } from "@loadline/api";
import type { LoadPlan } from "@loadline/api";
import { WorkloadPanel } from "./workloadpanel";
import { f0 } from "./format";

const workload = create(WorkloadSpecSchema, {
  totalUsers: 10_000_000n,
  dau: 1_000_000n,
  requestsPerUserPerDay: 40,
  peakMultiplier: 5,
  readWriteRatio: 4,
  payloadBytes: 4096n,
});

const plan: LoadPlan = create(LoadPlanSchema, {
  totalUsers: 10_000_000n,
  dau: 1_000_000n,
  dauFraction: 0.1,
  requestsPerUserPerDay: 40,
  requestsPerDay: 40_000_000,
  averageRps: 462.96,
  peakMultiplier: 5,
  peakRps: 2314.81,
  readFraction: 0.8,
  writeFraction: 0.2,
  payloadBytes: 4096n,
  meanInterArrivalMillis: 0.43,
});

function renderPanel(plan: LoadPlan | null = null) {
  const onPatch = vi.fn();
  render(<WorkloadPanel workload={workload} onPatch={onPatch} plan={plan} />);
  return { onPatch };
}

describe("WorkloadPanel", () => {
  it("renders derivation inputs from the canonical workload", () => {
    renderPanel();
    // Locale-independent: formatted through the same f0 the panel uses.
    expect(screen.getByText(f0(10_000_000))).toBeInTheDocument(); // users
    expect(screen.getByText(f0(1_000_000))).toBeInTheDocument(); // dau
  });

  it("refuses to derive numbers before any run — no fake plan", () => {
    renderPanel(null);
    expect(screen.getByText(/run a simulation to see the derived plan/i)).toBeInTheDocument();
    // No derived values anywhere: only "—" placeholders.
    expect(screen.getAllByText("—")).toHaveLength(3);
  });

  it("renders backend-derived plan values after a run", () => {
    renderPanel(plan);
    expect(screen.getByText(f0(40_000_000))).toBeInTheDocument(); // req/day
    expect(screen.getByText("463.0")).toBeInTheDocument(); // avg rps
    expect(screen.getByText("2314.8")).toBeInTheDocument(); // peak rps
    expect(screen.getByText("80.0%")).toBeInTheDocument(); // read fraction
    expect(screen.queryByText(/run a simulation to see/i)).not.toBeInTheDocument();
  });

  it("patches flow through onPatch without local state", () => {
    const { onPatch } = renderPanel();
    const dau = screen.getByDisplayValue("1000000");
    fireEvent.change(dau, { target: { value: "2000000" } });
    expect(onPatch).toHaveBeenCalled();
  });
});
