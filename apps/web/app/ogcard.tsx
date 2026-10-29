import { ImageResponse } from "next/og";
import { SITE_HOST, SITE_NAME } from "./site";

/**
 * Shared Open Graph / Twitter card renderer, in the site's own visual
 * language (warm paper, near-black ink, red load line). Each route's
 * opengraph-image.tsx supplies its own copy; layout stays identical so
 * shared links read as one family.
 *
 * The architecture strip is illustrative only — it carries no metrics,
 * because a preview image has no simulation behind it.
 */

export const OG_SIZE = { width: 1200, height: 630 };

const paper = "#f4f1ea";
const card = "#faf8f3";
const ink = "#1c1a17";
const soft = "#57524a";
const faint = "#8c857a";
const line = "#d8d3c7";
const bad = "#a83232";

const STRIP = ["client", "cdn", "load balancer", "api", "cache", "database"];

export type OgCardProps = {
  /** Small uppercase label above the title, e.g. "docs". */
  eyebrow: string;
  title: string;
  subtitle: string;
  chips: string[];
  /** Path shown after the host in the top-right, e.g. "/docs". */
  path?: string;
};

export function ogCard({ eyebrow, title, subtitle, chips, path = "" }: OgCardProps) {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "52px 64px",
          background: paper,
          color: ink,
        }}
      >
        {/* top row: wordmark + url */}
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 18 }}>
            <Mark />
            <span style={{ fontSize: 30, fontWeight: 700, letterSpacing: 8 }}>
              {SITE_NAME}
            </span>
          </div>
          <span style={{ fontSize: 22, color: faint, letterSpacing: 1 }}>
            {SITE_HOST}
            {path}
          </span>
        </div>

        {/* headline */}
        <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
          <span
            style={{
              fontSize: 20,
              letterSpacing: 5,
              color: bad,
              textTransform: "uppercase",
            }}
          >
            {eyebrow}
          </span>
          <div
            style={{
              display: "flex",
              fontSize: title.length > 48 ? 58 : 68,
              fontWeight: 700,
              lineHeight: 1.08,
              maxWidth: 1040,
            }}
          >
            {title}
          </div>
          <div style={{ display: "flex", fontSize: 26, color: soft, maxWidth: 1000 }}>
            {subtitle}
          </div>
        </div>

        {/* architecture strip sitting on the load line, then chips */}
        <div style={{ display: "flex", flexDirection: "column", gap: 26 }}>
          <div style={{ display: "flex", flexDirection: "column" }}>
            <div style={{ display: "flex", alignItems: "center" }}>
              {STRIP.map((node, i) => (
                <div key={node} style={{ display: "flex", alignItems: "center" }}>
                  {i > 0 && (
                    <div style={{ display: "flex", width: 34, height: 2, background: faint }} />
                  )}
                  <div
                    style={{
                      display: "flex",
                      border: `2px solid ${ink}`,
                      background: card,
                      padding: "6px 14px",
                      fontSize: 18,
                      color: ink,
                    }}
                  >
                    {node}
                  </div>
                </div>
              ))}
            </div>
            <div
              style={{ display: "flex", height: 4, background: bad, marginTop: 14 }}
            />
          </div>
          <div style={{ display: "flex", gap: 14, flexWrap: "wrap", alignItems: "center" }}>
            {chips.map((c) => (
              <div
                key={c}
                style={{
                  display: "flex",
                  border: `2px solid ${line}`,
                  background: card,
                  padding: "6px 16px",
                  fontSize: 20,
                  color: soft,
                }}
              >
                {c}
              </div>
            ))}
          </div>
        </div>
      </div>
    ),
    { ...OG_SIZE },
  );
}

/** The icon.svg mark, redrawn with boxes so it needs no SVG/font loading. */
function Mark() {
  return (
    <div
      style={{
        display: "flex",
        position: "relative",
        width: 48,
        height: 48,
        border: `3px solid ${ink}`,
        background: paper,
      }}
    >
      <div style={{ position: "absolute", left: 10, top: 7, width: 8, height: 25, background: ink, display: "flex" }} />
      <div style={{ position: "absolute", left: 10, top: 25, width: 22, height: 8, background: ink, display: "flex" }} />
      <div style={{ position: "absolute", left: 5, top: 36, width: 32, height: 2, background: bad, display: "flex" }} />
    </div>
  );
}
