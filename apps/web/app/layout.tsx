import type { Metadata } from "next";
import type { ReactNode } from "react";
import {
  SITE_DESCRIPTION,
  SITE_KEYWORDS,
  SITE_NAME,
  SITE_TITLE,
  SITE_URL,
  jsonLdScript,
  siteGraphLd,
} from "./site";
import { SpeedInsights } from "@vercel/speed-insights/next";
import "./globals.css";
import "./primitives.css";
import "./shell.css";
import "./analysis.css";
import "./workspace.css";
import "./arena.css";
import "./sitepages.css";

/**
 * Site-wide metadata. Every route inherits this; content pages override
 * title/description/canonical through their own `metadata` export.
 * SITE_URL (from ./site) is the single origin for canonical, og:url,
 * robots and sitemap — set NEXT_PUBLIC_SITE_URL before release.
 */
export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: SITE_TITLE,
    template: "%s · LOADLINE",
  },
  description: SITE_DESCRIPTION,
  keywords: SITE_KEYWORDS,
  applicationName: SITE_NAME,
  authors: [{ name: "LOADLINE contributors" }],
  category: "technology",
  alternates: {
    canonical: "/",
    // Machine-readable summary for LLMs (llmstxt.org convention).
    types: {
      "text/markdown": "/llms.txt",
    },
  },
  openGraph: {
    type: "website",
    url: "/",
    siteName: SITE_NAME,
    title: SITE_TITLE,
    description: SITE_DESCRIPTION,
    locale: "en_US",
  },
  twitter: {
    card: "summary_large_image",
    title: SITE_TITLE,
    description: SITE_DESCRIPTION,
  },
  robots: {
    index: true,
    follow: true,
    googleBot: {
      index: true,
      follow: true,
      "max-image-preview": "large",
      "max-snippet": -1,
      "max-video-preview": -1,
    },
  },
};

export default function RootLayout({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="en">
      <body>
        {/* Product/website structured data on every route (AEO/GEO). */}
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: jsonLdScript(siteGraphLd()) }}
        />
        {children}
        <SpeedInsights />
      </body>
    </html>
  );
}
