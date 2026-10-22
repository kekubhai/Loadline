import type { Metadata } from "next";
import Link from "next/link";
import { SiteFooter, SiteHeader } from "../../components/sitechrome";
import { PricingPlans } from "../../components/pricing";
import { CURRENCY, PLANS, priceFor } from "../pricing";
import { GITHUB_URL, breadcrumbLd, jsonLdScript } from "../site";

const DESCRIPTION =
  "LOADLINE pricing: Free ($0), Pro ($9), Team ($29) and Enterprise plans for the system-design simulation platform — features, plan limits and a 20% annual discount compared side by side.";

export const metadata: Metadata = {
  title: "Pricing — Free, Pro, Team & Enterprise Plans",
  description: DESCRIPTION,
  alternates: {
    canonical: "/pricing",
  },
  openGraph: {
    type: "website",
    url: "/pricing",
    siteName: "LOADLINE",
    title: "Pricing — Free, Pro, Team & Enterprise Plans",
    description: DESCRIPTION,
    locale: "en_US",
  },
  twitter: {
    card: "summary_large_image",
    title: "Pricing — Free, Pro, Team & Enterprise Plans",
    description: DESCRIPTION,
  },
};

/**
 * Schema.org offers for the paid tiers — the price a result engine quotes
 * is the published monthly rate; Enterprise stays unpriced on purpose.
 */
function pricingLd() {
  return {
    "@context": "https://schema.org",
    "@type": "ItemList",
    name: "LOADLINE plans",
    itemListElement: PLANS.map((plan, i) => {
      const monthly = priceFor(plan, "monthly");
      return {
        "@type": "ListItem",
        position: i + 1,
        item: {
          "@type": "Product",
          name: `LOADLINE ${plan.name}`,
          description: plan.audience,
          ...(monthly !== null
            ? {
                offers: {
                  "@type": "Offer",
                  price: String(monthly),
                  priceCurrency: CURRENCY,
                  availability: "https://schema.org/InStock",
                },
              }
            : {}),
        },
      };
    }),
  };
}

export default function PricingPage() {
  return (
    <>
      <SiteHeader current="/pricing" />
      <main className="site-main doc">
        <nav className="crumbs" aria-label="breadcrumb">
          <Link href="/">LOADLINE</Link> / pricing
        </nav>

        <h1>Plans and pricing</h1>
        <p className="lead">
          Four tiers over the same deterministic simulator. Free covers the
          editor, basic metrics, the public library and the arena; Pro adds
          failure injection, versioning, comparisons, cost and capacity
          estimates; Team adds shared workspaces and history; Enterprise adds
          SSO, private deployment and governance. Prices in USD, monthly or
          annual (20% off).
        </p>

        <div className="callout">
          <b>Billing:</b> annual pricing is charged once per year at 20% off the
          monthly rate — each card shows the monthly-equivalent rate, the yearly
          total and the exact saving. Enterprise pricing is custom.
        </div>

        <PricingPlans />

        <h2>Billing notes</h2>
        <ul>
          <li>
            All prices are in {CURRENCY}. The annual rate is the monthly rate
            × 12 × 0.8, rounded to the cent.
          </li>
          <li>
            <b>Get Started Free</b> opens the simulator directly — no account is
            required to build and run.{" "}
            <Link href="/faq">The FAQ explains how sharing works</Link>.
          </li>
          <li>
            <b>Upgrade to Pro</b> and <b>Start Team Workspace</b> open the
            simulator too — LOADLINE has no checkout or billing flow today.
          </li>
          <li>
            <b>Contact Sales</b> reaches us through{" "}
            <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
              the LOADLINE repository
            </a>{" "}
            — the current contact channel for team and enterprise questions.
          </li>
        </ul>

        <div className="cta-row">
          <Link className="cta" href="/">
            Open the simulator
          </Link>
          <Link className="cta alt" href="/docs">
            How the model works
          </Link>
        </div>

        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: jsonLdScript(pricingLd()) }}
        />
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: jsonLdScript(
              breadcrumbLd([
                { name: "LOADLINE", path: "/" },
                { name: "Pricing", path: "/pricing" },
              ]),
            ),
          }}
        />
      </main>
      <SiteFooter />
    </>
  );
}
