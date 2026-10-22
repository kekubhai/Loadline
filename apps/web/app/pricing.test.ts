/**
 * Pricing configuration tests: the published segmentation (tiers, prices,
 * 20% annual discount, features, limits, CTA targets) must stay exactly as
 * shipped — these numbers are quoted by the page, its JSON-LD and the
 * comparison table, so a silent edit here would mislead buyers.
 */
import { describe, expect, it } from "vitest";
import {
  ANNUAL_DISCOUNT,
  COMPARISON,
  CURRENCY,
  FEATURES,
  PLANS,
  SIMULATIONS_PER_MONTH,
  annualMonthlyUsd,
  annualSavingsUsd,
  annualTotalUsd,
  featuresFor,
  formatUsd,
  planById,
  priceFor,
  savingsFor,
} from "./pricing";
import { GITHUB_URL } from "./site";

describe("plans", () => {
  it("publishes the four tiers with their exact prices", () => {
    expect(PLANS.map((p) => p.id)).toEqual([
      "free",
      "pro",
      "team",
      "enterprise",
    ]);
    expect(PLANS.map((p) => p.name)).toEqual([
      "Free",
      "Pro",
      "Team",
      "Enterprise",
    ]);
    expect(PLANS.map((p) => p.monthlyUsd)).toEqual([0, 9, 29, null]);
    expect(CURRENCY).toBe("USD");
    expect(ANNUAL_DISCOUNT).toBe(0.2);
  });

  it("states an audience for every tier", () => {
    for (const plan of PLANS) {
      expect(plan.audience.length, plan.id).toBeGreaterThan(20);
      expect(plan.audience.endsWith("."), plan.id).toBe(true);
    }
  });

  it("stacks tiers through the includes chain (Free → Pro → Team → Enterprise)", () => {
    expect(planById("free").includes).toBeNull();
    expect(planById("pro").includes).toBe("free");
    expect(planById("team").includes).toBe("pro");
    expect(planById("enterprise").includes).toBe("team");

    // Each tier strictly gains features over its predecessor.
    const counts = PLANS.map((p) => featuresFor(p).length);
    expect(counts).toEqual([5, 11, 15, 19]);
    expect(planById("enterprise") && featuresFor(planById("enterprise"))).toHaveLength(
      Object.keys(FEATURES).length,
    );
  });

  it("references only features that exist, with no orphans", () => {
    const referenced = new Set(PLANS.flatMap((p) => p.features));
    for (const plan of PLANS) {
      for (const id of plan.features) {
        expect(FEATURES[id], `${plan.id}: ${id}`).toBeDefined();
      }
    }
    expect([...referenced].sort()).toEqual(Object.keys(FEATURES).sort());
  });

  it("marks exactly one plan as recommended — Pro", () => {
    const recommended = PLANS.filter((p) => p.recommended);
    expect(recommended.map((p) => p.id)).toEqual(["pro"]);
  });

  it("uses the required CTA labels and routes them at existing surfaces", () => {
    expect(PLANS.map((p) => p.cta.label)).toEqual([
      "Get Started Free",
      "Upgrade to Pro",
      "Start Team Workspace",
      "Contact Sales",
    ]);
    // Self-serve CTAs open the simulator; Contact Sales uses the repository.
    expect(PLANS.slice(0, 3).map((p) => p.cta.href)).toEqual([
      "/",
      "/",
      "/",
    ]);
    expect(planById("enterprise").cta.href).toBe(GITHUB_URL);
    // Nothing may point at a payment or checkout flow.
    for (const plan of PLANS) {
      expect(plan.cta.href).not.toMatch(/checkout|billing|payment|subscribe/i);
    }
  });
});

describe("billing math", () => {
  it("applies exactly 20% off the monthly rate when billed annually", () => {
    expect(annualMonthlyUsd(9)).toBe(7.2);
    expect(annualMonthlyUsd(29)).toBe(23.2);
    expect(annualMonthlyUsd(9) / 9).toBeCloseTo(1 - ANNUAL_DISCOUNT, 10);
    expect(annualMonthlyUsd(29) / 29).toBeCloseTo(1 - ANNUAL_DISCOUNT, 10);
    // Free stays free; Enterprise stays custom under either period.
    expect(annualMonthlyUsd(0)).toBe(0);
    expect(priceFor(planById("free"), "annual")).toBe(0);
    expect(priceFor(planById("enterprise"), "annual")).toBeNull();
    expect(priceFor(planById("enterprise"), "monthly")).toBeNull();
  });

  it("computes yearly totals and savings consistently", () => {
    expect(annualTotalUsd(9)).toBe(86.4);
    expect(annualTotalUsd(29)).toBe(278.4);
    expect(annualSavingsUsd(9)).toBe(21.6);
    expect(annualSavingsUsd(29)).toBe(69.6);
    // Savings are exactly the 20% that annual billing removes.
    expect(annualSavingsUsd(9)).toBeCloseTo(9 * 12 * ANNUAL_DISCOUNT, 10);
    expect(annualSavingsUsd(29)).toBeCloseTo(29 * 12 * ANNUAL_DISCOUNT, 10);

    expect(savingsFor(planById("pro"), "annual")).toBe(21.6);
    expect(savingsFor(planById("team"), "annual")).toBe(69.6);
    // No savings line under monthly billing, nor for free/custom tiers.
    expect(savingsFor(planById("pro"), "monthly")).toBeNull();
    expect(savingsFor(planById("free"), "annual")).toBeNull();
    expect(savingsFor(planById("enterprise"), "annual")).toBeNull();
  });

  it("formats USD the way the page shows it", () => {
    expect(formatUsd(0)).toBe("$0");
    expect(formatUsd(9)).toBe("$9");
    expect(formatUsd(7.2)).toBe("$7.20");
    expect(formatUsd(23.2)).toBe("$23.20");
    expect(formatUsd(278.4)).toBe("$278.40");
  });
});

describe("comparison", () => {
  it("covers every feature exactly once, in plan order, plus the limits row", () => {
    const ids = COMPARISON.map((r) => r.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids[0]).toBe("editor");
    expect(ids).toContain("simulations");
    // One row per feature: the two simulations features collapse into one.
    expect(ids.length).toBe(Object.keys(FEATURES).length - 1);

    for (const row of COMPARISON) {
      expect(Object.keys(row.cells).sort()).toEqual(
        ["enterprise", "free", "pro", "team"].sort(),
      );
      if (row.id !== "simulations") {
        expect(FEATURES[row.id], row.id).toBe(row.label);
      }
    }
  });

  it("states the simulations limit per tier instead of a made-up number", () => {
    const row = COMPARISON.find((r) => r.id === "simulations");
    expect(row?.label).toBe("Simulations per month");
    expect(row?.cells).toEqual({ ...SIMULATIONS_PER_MONTH });
    for (const value of Object.values(row!.cells)) {
      expect(typeof value).toBe("string");
      expect(value).not.toMatch(/\d/);
    }
  });

  it("derives inclusion from the tier chain and never loses a feature", () => {
    for (const row of COMPARISON) {
      if (row.id === "simulations") continue;
      for (const plan of PLANS) {
        const expected = featuresFor(plan).includes(row.id);
        expect(row.cells[plan.id], `${row.id}/${plan.id}`).toBe(expected);
      }
    }
  });

  it("keeps inclusion monotone: a feature never disappears up the tiers", () => {
    const order = ["free", "pro", "team", "enterprise"] as const;
    for (const row of COMPARISON) {
      let included = false;
      for (const id of order) {
        const value = row.cells[id];
        if (value === false) {
          expect(included, `${row.id}/${id}`).toBe(false);
        } else {
          included = true;
        }
      }
    }
  });
});
