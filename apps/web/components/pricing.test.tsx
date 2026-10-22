/**
 * Pricing page tests: the four tiers, their published prices, the 20%
 * annual toggle, the recommended badge, the comparison rows and the CTA
 * targets must render exactly from the pricing configuration — no
 * component-side math, no invented limits, no payment links.
 */
import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PricingPlans } from "./pricing";
import { PLANS, formatUsd, priceFor } from "../app/pricing";
import { GITHUB_URL } from "../app/site";

function card(name: string): HTMLElement {
  const heading = screen.getByRole("heading", { level: 3, name });
  const article = heading.closest("article");
  if (!article) throw new Error(`no card for ${name}`);
  return article;
}

describe("PricingPlans", () => {
  it("renders all four tiers with audience, price and CTA", () => {
    render(<PricingPlans />);

    for (const plan of PLANS) {
      const el = card(plan.name);
      expect(within(el).getByText(plan.audience)).toBeInTheDocument();
      expect(within(el).getByRole("link", { name: plan.cta.label })).toHaveAttribute(
        "href",
        plan.cta.href,
      );
    }

    // Monthly rates by default.
    expect(within(card("Free")).getByText("$0")).toBeInTheDocument();
    expect(within(card("Pro")).getByText("$9")).toBeInTheDocument();
    expect(within(card("Team")).getByText("$29")).toBeInTheDocument();
    expect(within(card("Enterprise")).getByText("Custom")).toBeInTheDocument();
  });

  it("highlights Pro as recommended and only Pro", () => {
    render(<PricingPlans />);
    expect(within(card("Pro")).getByText("recommended")).toBeInTheDocument();
    for (const name of ["Free", "Team", "Enterprise"]) {
      expect(within(card(name)).queryByText("recommended")).toBeNull();
    }
    expect(card("Pro").className).toContain("recommended");
    expect(card("Free").className).not.toContain("recommended");
  });

  it("switches to annual pricing with the 20% discount and the savings", async () => {
    const user = userEvent.setup();
    render(<PricingPlans />);

    await user.click(screen.getByRole("button", { name: /annual/i }));

    const pro = card("Pro");
    expect(within(pro).getByText("$7.20")).toBeInTheDocument();
    expect(within(pro).getByText("$9")).toBeInTheDocument(); // struck-through original
    expect(pro.textContent).toContain("save $21.60/yr");

    const team = card("Team");
    expect(within(team).getByText("$23.20")).toBeInTheDocument();
    expect(team.textContent).toContain("save $69.60/yr");

    // The savings line states the discount for both paid tiers.
    const note = document.querySelector(".bill-note");
    expect(note?.textContent).toContain("20%");
    expect(note?.textContent).toContain("save $21.60/yr on Pro");
    expect(note?.textContent).toContain("$69.60/yr on Team");

    // Enterprise stays custom; Free stays zero.
    expect(within(card("Enterprise")).getByText("Custom")).toBeInTheDocument();
    expect(within(card("Free")).getByText("$0")).toBeInTheDocument();

    // And back again.
    await user.click(screen.getByRole("button", { name: /monthly/i }));
    expect(within(card("Pro")).getByText("$9")).toBeInTheDocument();
    expect(within(card("Team")).getByText("$29")).toBeInTheDocument();
  });

  it("exposes the billing toggle as pressed states for assistive tech", async () => {
    const user = userEvent.setup();
    render(<PricingPlans />);
    const monthly = screen.getByRole("button", { name: /monthly/i });
    const annual = screen.getByRole("button", { name: /annual/i });
    expect(monthly).toHaveAttribute("aria-pressed", "true");
    expect(annual).toHaveAttribute("aria-pressed", "false");

    await user.click(annual);
    expect(monthly).toHaveAttribute("aria-pressed", "false");
    expect(annual).toHaveAttribute("aria-pressed", "true");
  });

  it("routes CTAs at existing surfaces, never a checkout", () => {
    render(<PricingPlans />);
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(PLANS.length);
    for (const link of links) {
      expect(link.getAttribute("href")).not.toMatch(
        /checkout|billing|payment|subscribe/i,
      );
    }
    expect(
      screen.getByRole("link", { name: "Contact Sales" }),
    ).toHaveAttribute("href", GITHUB_URL);
  });

  it("renders the comparison with a price row, limits row and inclusion marks", () => {
    render(<PricingPlans />);

    const table = screen.getByRole("table");
    // Feature column + one column per plan.
    expect(within(table).getAllByRole("columnheader")).toHaveLength(
      PLANS.length + 1,
    );

    const headers = within(table)
      .getAllByRole("columnheader")
      .map((h) => h.textContent);
    expect(headers).toContain("Free");
    expect(headers.join(" ")).toContain("recommended");

    // Price row reflects the current (monthly) billing period.
    const rows = within(table).getAllByRole("row");
    const priceRow = rows.find((r) =>
      within(r).queryByRole("rowheader", { name: "Price" }),
    );
    expect(priceRow?.textContent).toContain(
      `${formatUsd(priceFor(PLANS[1], "monthly")!)} / mo`,
    );

    // The simulations limit row shows tier wording, not invented numbers.
    const simRow = rows.find((r) =>
      within(r).queryByRole("rowheader", { name: "Simulations per month" }),
    );
    expect(simRow?.textContent).toContain("limited");
    expect(simRow?.textContent).toContain("higher limits");
    expect(simRow?.textContent).toContain("custom");
    expect(simRow?.textContent).not.toMatch(/\d/);

    // Capability rows carry readable inclusion state for screen readers.
    const chaosRow = rows.find((r) =>
      within(r).queryByRole("rowheader", {
        name: "Advanced failure injection and chaos scenarios",
      }),
    );
    expect(chaosRow).toBeDefined();
    expect(within(chaosRow!).getAllByText("not included")).toHaveLength(1);
    expect(within(chaosRow!).getAllByText("included")).toHaveLength(3);
  });

  it("lists every tier's own features on its card", () => {
    render(<PricingPlans />);
    // Inherited tier is stated once per card that has a predecessor.
    expect(within(card("Pro")).getByText("Everything in Free")).toBeInTheDocument();
    expect(within(card("Team")).getByText("Everything in Pro")).toBeInTheDocument();
    expect(
      within(card("Enterprise")).getByText("Everything in Team"),
    ).toBeInTheDocument();
    expect(
      within(card("Free")).queryByText(/^Everything in/),
    ).toBeNull();

    expect(
      within(card("Free")).getByText("Limited simulations per month"),
    ).toBeInTheDocument();
    expect(
      within(card("Pro")).getByText("Higher monthly simulation limits"),
    ).toBeInTheDocument();
    expect(
      within(card("Enterprise")).getByText(
        "Governance, auditability, and dedicated support",
      ),
    ).toBeInTheDocument();
  });
});
