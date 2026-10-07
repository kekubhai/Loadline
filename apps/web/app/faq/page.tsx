import type { Metadata } from "next";
import Link from "next/link";
import { FAQ_ITEMS } from "../faq";
import { SiteFooter, SiteHeader } from "../../components/sitechrome";
import { breadcrumbLd, faqLd, jsonLdScript } from "../site";

const DESCRIPTION =
  "Answers about LOADLINE: what the system-design simulator measures, how p99 and capacity are computed, determinism, providers, failure injection and sharing.";

export const metadata: Metadata = {
  title: "FAQ — System Design Simulation, Capacity, Latency & Cost",
  description: DESCRIPTION,
  alternates: {
    canonical: "/faq",
  },
  openGraph: {
    type: "website",
    url: "/faq",
    siteName: "LOADLINE",
    title: "FAQ — System Design Simulation, Capacity, Latency & Cost",
    description: DESCRIPTION,
    locale: "en_US",
  },
  twitter: {
    card: "summary_large_image",
    title: "FAQ — System Design Simulation, Capacity, Latency & Cost",
    description: DESCRIPTION,
  },
};

/**
 * Answer-engine surface: one question per <h2>, the direct answer in the
 * first paragraph, mirrored 1:1 into FAQPage JSON-LD below. Keep the two
 * in sync by construction — both render from FAQ_ITEMS.
 */
export default function FaqPage() {
  return (
    <>
      <SiteHeader current="/faq" />
      <main className="site-main doc">
        <nav className="crumbs" aria-label="breadcrumb">
          <Link href="/">LOADLINE</Link> / FAQ
        </nav>

        <h1>Frequently asked questions</h1>
        <p className="lead">
          Straight answers about LOADLINE, the system-design simulation
          platform: what it measures, how capacity and p99 latency are
          computed, which providers are modeled, and what the numbers do and
          do not promise.
        </p>

        <div className="faq-list">
          {FAQ_ITEMS.map((item) => (
            <section className="faq-item" key={item.question}>
              <h2>{item.question}</h2>
              <p>{item.answer}</p>
            </section>
          ))}
        </div>

        <div className="cta-row">
          <Link className="cta" href="/">
            Open the simulator
          </Link>
          <Link className="cta alt" href="/docs">
            Read how the model works
          </Link>
        </div>

        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: jsonLdScript(faqLd(FAQ_ITEMS)) }}
        />
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: jsonLdScript(
              breadcrumbLd([
                { name: "LOADLINE", path: "/" },
                { name: "FAQ", path: "/faq" },
              ]),
            ),
          }}
        />
      </main>
      <SiteFooter />
    </>
  );
}
