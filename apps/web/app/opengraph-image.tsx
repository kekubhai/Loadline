import { ImageResponse } from "next/og";
import { SITE_NAME, SITE_TAGLINE } from "./site";

/**
 * Open Graph / Twitter card, rendered at build time by next/og in the
 * site's own visual language (warm paper, near-black ink, red load line).
 * Served for every route through the metadata file convention.
 * nodejs runtime: rendered once at build time (static), no per-request cost.
 */
export const runtime = "nodejs";

export const alt = `${SITE_NAME} — system design simulation: capacity, cost and bottlenecks`;
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

const paper = "#f4f1ea";
const ink = "#1c1a17";
const soft = "#57524a";
const faint = "#8c857a";
const line = "#d8d3c7";
const bad = "#a83232";

const CHIPS = [
  "capacity & headroom",
  "p50 / p95 / p99",
  "bottleneck diagnosis",
  "failure cascades",
  "estimated monthly cost",
];

export default function OpengraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "52px 60px",
          background: paper,
          color: ink,
        }}
      >
        {/* top row */}
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            fontSize: 20,
            letterSpacing: 4,
            color: faint,
          }}
        >
          <span style={{ textTransform: "uppercase" }}>
            system-design simulation platform
          </span>
          <span>github.com/kekubhai/Loadline</span>
        </div>

        {/* masthead */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 18,
          }}
        >
          <div
            style={{
              fontSize: 104,
              fontWeight: 700,
              letterSpacing: 26,
              textTransform: "uppercase",
            }}
          >
            {SITE_NAME}
          </div>
          <div style={{ fontSize: 26, color: soft, letterSpacing: 2 }}>
            {SITE_TAGLINE}
          </div>
          <div
            style={{
              width: 720,
              height: 4,
              background: bad,
              marginTop: 10,
              display: "flex",
            }}
          />
        </div>

        {/* claim + chips */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 22,
          }}
        >
          <div style={{ display: "flex", gap: 16 }}>
            {CHIPS.map((c) => (
              <div
                key={c}
                style={{
                  display: "flex",
                  border: `2px solid ${line}`,
                  background: "#faf8f3",
                  padding: "8px 18px",
                  fontSize: 22,
                  color: soft,
                }}
              >
                {c}
              </div>
            ))}
          </div>
          <div style={{ fontSize: 24, color: ink }}>
            Can this architecture handle my requirements, what will it cost,
            and where will it break?
          </div>
        </div>
      </div>
    ),
    { ...size },
  );
}
