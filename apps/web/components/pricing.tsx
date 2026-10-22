"use client";

/**
 * Pricing surface — renders the plan segmentation defined in ../app/pricing.
 *
 * The billing toggle is the only interactive piece: it swaps the displayed
 * rate between the monthly price and its 20% annual discount and rewrites
 * the savings line. Nothing here computes entitlements, limits or checkout —
 * every number comes from the pricing configuration, and every CTA points at
 * an existing surface (the simulator, the repository).
 */
import { useState } from "react";
import {
  ANNUAL_DISCOUNT,
  COMPARISON,
  FEATURES,
  PLANS,
  formatUsd,
  planById,
  priceFor,
  savingsFor,
  annualTotalUsd,
  type Billing,
  type Plan,
} from "../app/pricing";

export function PricingPlans() {
  const [billing, setBilling] = useState<Billing>("monthly");

  return (
    <>
      <div className="price-controls">
        <div className="bill-toggle" role="group" aria-label="billing period">
          <button
            type="button"
            aria-pressed={billing === "monthly"}
            onClick={() => setBilling("monthly")}
          >
            monthly
          </button>
          <button
            type="button"
            aria-pressed={billing === "annual"}
            onClick={() => setBilling("annual")}
          >
            annual <span className="bill-save">−{Math.round(ANNUAL_DISCOUNT * 100)}%</span>
          </button>
        </div>
        <p className="bill-note" aria-live="polite">
          {billing === "annual"
            ? `billed annually at ${Math.round(ANNUAL_DISCOUNT * 100)}% off — save ${savingsSentence()}`
            : `switch to annual to take ${Math.round(ANNUAL_DISCOUNT * 100)}% off — ${savingsSentence()}`}
        </p>
      </div>

      <div className="price-grid">
        {PLANS.map((plan) => (
          <PlanCard key={plan.id} plan={plan} billing={billing} />
        ))}
      </div>

      <section className="price-compare" aria-labelledby="compare-h">
        <h2 id="compare-h">Compare plans</h2>
        <p className="price-compare-note">
          Key features and limits per tier — the same segmentation as the
          cards, one row per capability.
        </p>
        <div
          className="price-table-wrap"
          role="region"
          aria-label="plan comparison"
          tabIndex={0}
        >
          <table className="price-table">
            <thead>
              <tr>
                <th scope="col">Feature</th>
                {PLANS.map((p) => (
                  <th
                    scope="col"
                    key={p.id}
                    className={p.recommended ? "rec" : undefined}
                  >
                    {p.name}
                    {p.recommended && (
                      <span className="price-rec-col">recommended</span>
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">Price</th>
                {PLANS.map((p) => (
                  <td
                    key={p.id}
                    className={p.recommended ? "rec" : undefined}
                  >
                    {priceFor(p, billing) === null
                      ? "Custom"
                      : `${formatUsd(priceFor(p, billing)!)} / mo`}
                  </td>
                ))}
              </tr>
              {COMPARISON.map((row) => (
                <tr key={row.id}>
                  <th scope="row">{row.label}</th>
                  {PLANS.map((p) => {
                    const value = row.cells[p.id];
                    return (
                      <td
                        key={p.id}
                        className={p.recommended ? "rec" : undefined}
                      >
                        {typeof value === "string" ? value : <Included value={value} />}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

function PlanCard({ plan, billing }: { plan: Plan; billing: Billing }) {
  const price = priceFor(plan, billing);
  const savings = savingsFor(plan, billing);
  const inherited = plan.includes ? planById(plan.includes).name : null;

  return (
    <article
      className={`price-card${plan.recommended ? " recommended" : ""}`}
      aria-labelledby={`plan-${plan.id}`}
    >
      <header className="price-head">
        <h3 className="price-name" id={`plan-${plan.id}`}>
          {plan.name}
        </h3>
        {plan.recommended && <span className="price-rec">recommended</span>}
      </header>

      <p className="price-audience">{plan.audience}</p>

      <div className="price-amount">
        <span className="price-num">
          {price === null ? "Custom" : formatUsd(price)}
        </span>
        {price !== null && <span className="price-per">/ month</span>}
        {price !== null && plan.monthlyUsd !== null && price !== plan.monthlyUsd && (
          <s className="price-was">{formatUsd(plan.monthlyUsd)}</s>
        )}
      </div>
      <p className="price-basis">
        {price === null
          ? "custom pricing"
          : savings !== null
            ? `billed annually · ${formatUsd(annualTotalUsd(plan.monthlyUsd!))}/yr · save ${formatUsd(savings)}/yr`
            : plan.monthlyUsd === 0
              ? "free forever"
              : "billed monthly"}
      </p>

      <ul className="price-feats">
        {inherited && <li className="price-inherit">Everything in {inherited}</li>}
        {plan.features.map((id) => (
          <li key={id}>{FEATURES[id]}</li>
        ))}
      </ul>

      <a
        className={`cta${plan.recommended ? "" : " alt"} price-cta`}
        href={plan.cta.href}
      >
        {plan.cta.label}
      </a>
    </article>
  );
}

function Included({ value }: { value: boolean }) {
  return value ? (
    <>
      <span aria-hidden="true">✓</span>
      <span className="visually-hidden">included</span>
    </>
  ) : (
    <>
      <span aria-hidden="true">—</span>
      <span className="visually-hidden">not included</span>
    </>
  );
}

/** "$21.60/yr on Pro, $69.60/yr on Team" — built from the config. */
function savingsSentence(): string {
  const parts = PLANS.filter((p) => p.monthlyUsd).map(
    (p) => `${formatUsd(savingsFor(p, "annual")!)}/yr on ${p.name}`,
  );
  return parts.length > 1
    ? `${parts.slice(0, -1).join(", ")} and ${parts.at(-1)}`
    : parts.join("");
}
