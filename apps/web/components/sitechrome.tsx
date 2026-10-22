import Link from "next/link";
import { GITHUB_URL, SITE_TAGLINE } from "../app/site";

/**
 * Server-rendered site chrome for the content pages (/docs, /faq).
 * Internal links here are the crawl path from content to the application
 * and back; nothing below is client-rendered.
 */

const NAV = [
  { href: "/", label: "app" },
  { href: "/pricing", label: "pricing" },
  { href: "/docs", label: "docs" },
  { href: "/faq", label: "faq" },
] as const;

export function SiteHeader({ current }: { current?: string }) {
  return (
    <header className="site-head">
      <Link href="/" className="site-brand">
        LOADLINE
      </Link>
      <span className="site-tag">system-design simulation · {SITE_TAGLINE}</span>
      <nav aria-label="site">
        {NAV.map((item) => (
          <Link
            key={item.href}
            href={item.href}
            aria-current={current === item.href ? "page" : undefined}
          >
            {item.label}
          </Link>
        ))}
        <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
          github
        </a>
      </nav>
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className="site-foot">
      <span>
        LOADLINE — model the architecture before you build it. Every number is
        simulation output with its assumptions attached; estimates are not
        guarantees.
      </span>
      <nav aria-label="footer">
        <Link href="/">app</Link>
        <Link href="/pricing">pricing</Link>
        <Link href="/docs">docs</Link>
        <Link href="/faq">faq</Link>
        <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer">
          github
        </a>
        <a href="/llms.txt">llms.txt</a>
        <a href="/sitemap.xml">sitemap</a>
      </nav>
      <span className="site-foot-note">MIT licensed · open source</span>
    </footer>
  );
}
