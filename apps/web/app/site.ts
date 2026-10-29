/**
 * Site identity — one place for everything SEO/AEO/GEO depends on.
 *
 * SITE_URL drives canonical links, og:url, the sitemap, robots.txt and
 * every JSON-LD block. Set NEXT_PUBLIC_SITE_URL in apps/web/.env.local
 * (or in the host's env) to override the origin, e.g. for a preview
 * deployment; otherwise it falls back to the production domain.
 *
 * This module is imported by client components too (the app shell links
 * to GitHub), so it must stay free of server-only APIs.
 */

const REPO_URL = "https://github.com/kekubhai/Loadline";
const PRODUCTION_URL = "https://loadline.tech";

function normalizeSiteUrl(): string {
  const raw = process.env.NEXT_PUBLIC_SITE_URL?.trim();
  if (!raw) return PRODUCTION_URL;
  try {
    return new URL(raw).toString().replace(/\/$/, "");
  } catch {
    // An invalid env value must never break the build — fall back.
    return PRODUCTION_URL;
  }
}

/** Absolute origin of the deployed site, no trailing slash. */
export const SITE_URL = normalizeSiteUrl();

/** Bare host for display ("loadline.tech"), e.g. on preview cards. */
export const SITE_HOST = new URL(SITE_URL).host;

/** Public source repository. */
export const GITHUB_URL = REPO_URL;

/** Raw simulation-model spec — the citable full reference. */
export const SPEC_URL =
  "https://raw.githubusercontent.com/kekubhai/Loadline/main/docs/simulation-model.md";

export const SITE_NAME = "LOADLINE";
export const SITE_TAGLINE =
  "requirements → architecture → simulation → failure → diagnosis";

/** Meta description — front-loaded, ~155 characters. */
export const SITE_DESCRIPTION =
  "LOADLINE is a system-design simulation platform: simulate load and failure on your architecture to see real capacity, p99 latency, bottlenecks and estimated cost.";

/** Primary title (home). */
export const SITE_TITLE =
  "LOADLINE — System Design Simulator: Capacity, Cost & Bottlenecks";

export const SITE_KEYWORDS = [
  "system design simulator",
  "system design simulation platform",
  "architecture capacity planning tool",
  "estimate if my architecture scales",
  "bottleneck detection tool",
  "cloud cost estimator",
  "load and failure simulation",
  "discrete event simulation",
  "system design tool",
  "latency queueing model",
];

/** Absolute URL for a site path ("/faq" → "https://site/faq"). */
export function absolute(path = "/"): string {
  return `${SITE_URL}${path.startsWith("/") ? path : `/${path}`}`;
}

export function twitterImage(): string {
  return absolute("/opengraph-image");
}

/* ------------------------------------------------------------- JSON-LD -- */

export type JsonLd = Record<string, unknown>;

/**
 * Organization + WebSite + WebApplication as one @graph. Attached to the
 * root layout so every route carries it; answer engines (AI Overviews,
 * Perplexity, ChatGPT search) read these to attribute the product.
 */
export function siteGraphLd(): JsonLd {
  return {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "Organization",
        "@id": absolute("/#organization"),
        name: SITE_NAME,
        url: absolute("/"),
        logo: absolute("/icon.svg"),
        description: SITE_DESCRIPTION,
        sameAs: [GITHUB_URL],
      },
      {
        "@type": "WebSite",
        "@id": absolute("/#website"),
        name: SITE_NAME,
        url: absolute("/"),
        description: SITE_DESCRIPTION,
        inLanguage: "en",
        publisher: { "@id": absolute("/#organization") },
      },
      {
        "@type": "WebApplication",
        "@id": absolute("/#application"),
        name: SITE_NAME,
        alternateName: "Loadline",
        url: absolute("/"),
        description: SITE_DESCRIPTION,
        applicationCategory: "DeveloperApplication",
        applicationSubCategory: "System design simulation",
        operatingSystem: "Web",
        inLanguage: "en",
        isAccessibleForFree: true,
        license: `${GITHUB_URL}/blob/main/LICENSE`,
        keywords: SITE_KEYWORDS.join(", "),
        offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
        featureList: [
          "Visual architecture canvas with real cloud services (Cloudflare, AWS, GCP)",
          "Deterministic discrete-event simulation of load, queues and failures",
          "Capacity, headroom and p50/p95/p99 latency analysis",
          "Bottleneck diagnosis with causal evidence",
          "Estimated monthly cloud cost with assumption trails",
          "Architecture comparison under the same workload and seed",
        ],
        sameAs: [GITHUB_URL],
        publisher: { "@id": absolute("/#organization") },
      },
    ],
  };
}

/** FAQPage schema — rendered by /faq alongside its visible Q&A. */
export function faqLd(items: { question: string; answer: string }[]): JsonLd {
  return {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    mainEntity: items.map((item) => ({
      "@type": "Question",
      name: item.question,
      acceptedAnswer: { "@type": "Answer", text: item.answer },
    })),
  };
}

/** BreadcrumbList for a content page. */
export function breadcrumbLd(
  items: { name: string; path: string }[],
): JsonLd {
  return {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: items.map((item, i) => ({
      "@type": "ListItem",
      position: i + 1,
      name: item.name,
      item: absolute(item.path),
    })),
  };
}

/** Serialize JSON-LD for a <script type="application/ld+json"> tag. */
export function jsonLdScript(data: JsonLd): string {
  return JSON.stringify(data).replace(/</g, "\\u003c");
}
