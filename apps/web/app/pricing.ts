/**
 * Pricing — the single source of truth for the /pricing page.
 *
 * Every price, feature, audience statement, comparison cell and CTA target
 * lives here; the page and the components only render it. Changing a plan
 * means editing this file (plus the test that pins the published numbers).
 *
 * Scope note: this is segmentation and UI only. There is no checkout,
 * billing API or account system in the product — the plan CTAs route into
 * existing surfaces (the simulator, the repository), never a payment flow.
 */

import { GITHUB_URL } from "./site";

export type PlanId = "free" | "pro" | "team" | "enterprise";

/** Billing periods offered on the page. */
export type Billing = "monthly" | "annual";

/** Annual billing is a flat 20% discount on the monthly rate. */
export const ANNUAL_DISCOUNT = 0.2;

/** Currency for every number on the page. */
export const CURRENCY = "USD";

/**
 * Feature catalog — id → card label. Plans reference features by id, so the
 * cards and the comparison table can never drift apart: both are derived
 * from this map and PLAN.features below.
 */
export const FEATURES: Record<string, string> = {
  editor: "Architecture editor and basic templates",
  simulationsFree: "Limited simulations per month",
  simulationsPro: "Higher monthly simulation limits",
  metrics: "Basic latency, throughput, and bottleneck metrics",
  library: "Public architecture library",
  arena: "Arena challenges and leaderboards",
  chaos: "Advanced failure injection and chaos scenarios",
  versioning: "Architecture versioning and simulation comparisons",
  estimates: "Detailed cost and capacity estimates",
  reports: "Exportable reports",
  privateArchs: "Private architectures",
  workspaces: "Shared workspaces and architecture reviews",
  history: "Team-wide simulation history",
  scenarios: "Shared templates and reusable scenarios",
  access: "Team collaboration and access controls",
  sso: "SSO and role-based access control",
  deployment: "Private deployment options",
  customComponents: "Custom components and workload models",
  governance: "Governance, auditability, and dedicated support",
};

/** The two plan features that are limits, not capabilities — row below. */
const SIMULATION_FEATURE_IDS = new Set(["simulationsFree", "simulationsPro"]);

export interface Plan {
  id: PlanId;
  name: string;
  /** Monthly rate in USD; `null` means custom pricing (Enterprise). */
  monthlyUsd: number | null;
  /** Who the plan is for — one sentence, shown on the card. */
  audience: string;
  /** Tier whose features this plan also includes (`null` for Free). */
  includes: PlanId | null;
  /** Feature ids shown on the card, in display order. */
  features: string[];
  cta: { label: string; href: string };
  /** The one plan marked "recommended" — never more than one. */
  recommended: boolean;
}

/**
 * The simulator's open surface: no account is required to build and run,
 * so every self-serve CTA simply opens it.
 */
const APP_HREF = "/";

export const PLANS: Plan[] = [
  {
    id: "free",
    name: "Free",
    monthlyUsd: 0,
    audience: "For students, learners, and individual developers.",
    includes: null,
    features: [
      "editor",
      "simulationsFree",
      "metrics",
      "library",
      "arena",
    ],
    cta: { label: "Get Started Free", href: APP_HREF },
    recommended: false,
  },
  {
    id: "pro",
    name: "Pro",
    monthlyUsd: 9,
    audience: "For developers who need deeper architecture validation.",
    includes: "free",
    features: [
      "simulationsPro",
      "chaos",
      "versioning",
      "estimates",
      "reports",
      "privateArchs",
    ],
    cta: { label: "Upgrade to Pro", href: APP_HREF },
    recommended: true,
  },
  {
    id: "team",
    name: "Team",
    monthlyUsd: 29,
    audience: "For startups and engineering teams.",
    includes: "pro",
    features: ["workspaces", "history", "scenarios", "access"],
    cta: { label: "Start Team Workspace", href: APP_HREF },
    recommended: false,
  },
  {
    id: "enterprise",
    name: "Enterprise",
    monthlyUsd: null,
    audience: "For organizations with advanced requirements.",
    includes: "team",
    features: [
      "sso",
      "deployment",
      "customComponents",
      "governance",
    ],
    cta: { label: "Contact Sales", href: GITHUB_URL },
    recommended: false,
  },
];

export function planById(id: PlanId): Plan {
  const plan = PLANS.find((p) => p.id === id);
  if (!plan) throw new Error(`unknown plan: ${id}`);
  return plan;
}

/**
 * Simulations per month, per tier — stated exactly as the segmentation
 * defines it. There is no numeric quota anywhere in the product yet, so
 * none is invented here.
 */
export const SIMULATIONS_PER_MONTH: Record<PlanId, string> = {
  free: "limited",
  pro: "higher limits",
  team: "higher limits",
  enterprise: "custom",
};

/* ------------------------------------------------------------- pricing -- */

const round2 = (n: number): number => Math.round(n * 100) / 100;

/** Monthly-equivalent rate when billed annually (20% off), or null. */
export function annualMonthlyUsd(monthlyUsd: number): number {
  return round2(monthlyUsd * (1 - ANNUAL_DISCOUNT));
}

/** What a year costs when billed annually (monthly rate × 12 × 0.8). */
export function annualTotalUsd(monthlyUsd: number): number {
  return round2(monthlyUsd * 12 * (1 - ANNUAL_DISCOUNT));
}

/** Cash saved per year by billing annually instead of monthly. */
export function annualSavingsUsd(monthlyUsd: number): number {
  return round2(monthlyUsd * 12 - annualTotalUsd(monthlyUsd));
}

/** The rate shown for a plan under a billing period; null = custom. */
export function priceFor(plan: Plan, billing: Billing): number | null {
  if (plan.monthlyUsd === null) return null;
  if (plan.monthlyUsd === 0) return 0;
  return billing === "annual" ? annualMonthlyUsd(plan.monthlyUsd) : plan.monthlyUsd;
}

/** Annual savings line for a plan, or null when it is free/custom. */
export function savingsFor(plan: Plan, billing: Billing): number | null {
  if (plan.monthlyUsd === null || plan.monthlyUsd === 0) return null;
  return billing === "annual" ? annualSavingsUsd(plan.monthlyUsd) : null;
}

/** "9.00" → "$9" / "$7.20" — trimmed cents, always two when needed. */
export function formatUsd(value: number): string {
  return `$${value.toFixed(2).replace(/\.00$/, "")}`;
}

/* ---------------------------------------------------------- features ---- */

/**
 * Every feature a plan actually gets: its own plus everything inherited
 * through the `includes` chain (Free → Pro → Team → Enterprise).
 */
export function featuresFor(plan: Plan): string[] {
  const ids: string[] = [];
  const seen = new Set<string>();
  let cursor: PlanId | null = plan.id;
  while (cursor) {
    const p = planById(cursor);
    for (const id of p.features) {
      if (!seen.has(id)) {
        seen.add(id);
        ids.push(id);
      }
    }
    cursor = p.includes;
  }
  return ids;
}

/* --------------------------------------------------------- comparison --- */

export interface CompareRow {
  id: string;
  label: string;
  /**
   * Cell per plan: `true` = included, `false` = not included,
   * string = tier-specific value (e.g. "higher limits").
   */
  cells: Record<PlanId, boolean | string>;
}

/**
 * The comparison table. Capability rows are derived from the plan feature
 * lists (a plan inherits every ancestor's features), so a feature added to
 * Pro shows up for Team and Enterprise automatically. The simulations row
 * is explicit because each tier states its own limit value.
 */
export const COMPARISON: CompareRow[] = buildComparison();

function buildComparison(): CompareRow[] {
  const rows: CompareRow[] = [];
  const emitted = new Set<string>();

  const cellsFor = (featureId: string): Record<PlanId, boolean | string> => ({
    free: featuresFor(planById("free")).includes(featureId),
    pro: featuresFor(planById("pro")).includes(featureId),
    team: featuresFor(planById("team")).includes(featureId),
    enterprise: featuresFor(planById("enterprise")).includes(featureId),
  });

  for (const plan of PLANS) {
    for (const featureId of plan.features) {
      if (emitted.has(featureId)) continue;
      emitted.add(featureId);

      if (SIMULATION_FEATURE_IDS.has(featureId)) {
        if (emitted.has("simulations")) continue;
        emitted.add("simulations");
        rows.push({
          id: "simulations",
          label: "Simulations per month",
          cells: { ...SIMULATIONS_PER_MONTH },
        });
        continue;
      }

      rows.push({
        id: featureId,
        label: FEATURES[featureId],
        cells: cellsFor(featureId),
      });
    }
  }

  return rows;
}
