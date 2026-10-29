import { SITE_NAME } from "./site";
import { OG_SIZE, ogCard } from "./ogcard";

/**
 * Open Graph / Twitter card for the home page (and any route without its
 * own opengraph-image). nodejs runtime: rendered once at build time
 * (static), no per-request cost.
 */
export const runtime = "nodejs";

export const alt = `${SITE_NAME} — system design simulation: capacity, cost and bottlenecks`;
export const size = OG_SIZE;
export const contentType = "image/png";

export default function OpengraphImage() {
  return ogCard({
    eyebrow: "system-design simulation",
    title: "Can this architecture handle the load — and where will it break?",
    subtitle: "Simulate load and failure. See capacity, p99, bottlenecks and estimated cost.",
    chips: ["capacity & headroom", "p50 / p95 / p99", "bottleneck diagnosis", "failure cascades", "monthly cost"],
  });
}
