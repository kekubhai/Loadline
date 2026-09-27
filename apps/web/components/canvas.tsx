"use client";

/**
 * Architecture canvas — grid, nodes, edges, zoom, pan, selection.
 *
 * Purely presentational: it renders the architecture the page owns and
 * reports selections back. Layout is deterministic (layered by dependency
 * depth, ordered by component order), so the same architecture always
 * draws identically. No simulation behavior lives here.
 */
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as RPointerEvent } from "react";

export interface CanvasNode {
  id: string;
  label: string;
  sub: string;
}

export interface CanvasEdge {
  from: string;
  to: string;
  /** Optional link condition ("read" / "write") rendered at the midpoint. */
  label?: string;
}

const NODE_W = 128;
const NODE_H = 44;
const GAP_X = 76;
const GAP_Y = 26;
const PAD = 48;
const GRID = 24;
const MIN_Z = 0.4;
const MAX_Z = 2.2;

function clamp(v: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, v));
}

interface Layout {
  pos: Map<string, { x: number; y: number }>;
  w: number;
  h: number;
}

/** Layered left-to-right layout by dependency depth. Deterministic. */
function layout(nodes: CanvasNode[], edges: CanvasEdge[]): Layout {
  const depth = new Map<string, number>();
  for (const n of nodes) depth.set(n.id, 0);
  let changed = true;
  let guard = 0;
  while (changed && guard++ < 64) {
    changed = false;
    for (const e of edges) {
      const d = depth.get(e.from);
      const t = depth.get(e.to);
      if (d !== undefined && t !== undefined && t < d + 1) {
        depth.set(e.to, d + 1);
        changed = true;
      }
    }
  }
  const cols = new Map<number, string[]>();
  for (const n of nodes) {
    const d = depth.get(n.id) ?? 0;
    if (!cols.has(d)) cols.set(d, []);
    cols.get(d)!.push(n.id);
  }
  const depths = [...cols.keys()].sort((a, b) => a - b);
  const colHeights = depths.map((d) => cols.get(d)!.length * (NODE_H + GAP_Y) - GAP_Y);
  const innerH = Math.max(0, ...colHeights);
  const pos = new Map<string, { x: number; y: number }>();
  depths.forEach((d, ci) => {
    const ids = cols.get(d)!;
    const y0 = PAD + (innerH - colHeights[ci]) / 2;
    ids.forEach((id, i) => {
      pos.set(id, { x: PAD + ci * (NODE_W + GAP_X), y: y0 + i * (NODE_H + GAP_Y) });
    });
  });
  const w = PAD * 2 + Math.max(0, depths.length - 1) * (NODE_W + GAP_X) + NODE_W;
  const h = PAD * 2 + innerH;
  return { pos, w, h };
}

export function ArchitectureCanvas({
  nodes,
  edges,
  selected,
  onSelect,
}: {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ x: 24, y: 24, z: 1 });
  const [panning, setPanning] = useState(false);
  const panRef = useRef<{ px: number; py: number; vx: number; vy: number } | null>(null);
  const movedRef = useRef(false);
  const fittedRef = useRef(false);

  const laid = useMemo(() => layout(nodes, edges), [nodes, edges]);

  /** Fit the whole graph into the viewport, centered. */
  const fit = useCallback(() => {
    const vp = viewportRef.current;
    if (!vp || laid.w === 0) return;
    const z = clamp(
      Math.min((vp.clientWidth - 20) / laid.w, (vp.clientHeight - 20) / laid.h),
      MIN_Z,
      MAX_Z,
    );
    setView({
      z,
      x: (vp.clientWidth - laid.w * z) / 2,
      y: (vp.clientHeight - laid.h * z) / 2,
    });
  }, [laid]);

  // Fit once on mount.
  useEffect(() => {
    if (fittedRef.current) return;
    fittedRef.current = true;
    fit();
  }, [fit]);

  // Wheel zoom around the cursor — native listener so preventDefault works.
  useEffect(() => {
    const vp = viewportRef.current;
    if (!vp) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = vp.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      setView((v) => {
        const z = clamp(v.z * Math.exp(-e.deltaY * 0.0015), MIN_Z, MAX_Z);
        const k = z / v.z;
        return { z, x: mx - (mx - v.x) * k, y: my - (my - v.y) * k };
      });
    };
    vp.addEventListener("wheel", onWheel, { passive: false });
    return () => vp.removeEventListener("wheel", onWheel);
  }, []);

  function onPointerDown(e: RPointerEvent<HTMLDivElement>) {
    panRef.current = { px: e.clientX, py: e.clientY, vx: view.x, vy: view.y };
    movedRef.current = false;
    setPanning(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function onPointerMove(e: RPointerEvent<HTMLDivElement>) {
    const p = panRef.current;
    if (!p) return;
    const dx = e.clientX - p.px;
    const dy = e.clientY - p.py;
    if (Math.abs(dx) + Math.abs(dy) > 3) movedRef.current = true;
    setView((v) => ({ ...v, x: p.vx + dx, y: p.vy + dy }));
  }

  function onPointerUp() {
    panRef.current = null;
    setPanning(false);
  }

  return (
    <div
      ref={viewportRef}
      className={`canvas-viewport${panning ? " panning" : ""}`}
      style={{
        backgroundPosition: `${view.x}px ${view.y}px`,
        backgroundSize: `${GRID * view.z}px ${GRID * view.z}px`,
      }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onClick={() => {
        if (!movedRef.current) onSelect(null);
      }}
    >
      <div
        className="canvas-world"
        style={{
          transform: `translate(${view.x}px, ${view.y}px) scale(${view.z})`,
          width: laid.w,
          height: laid.h,
        }}
      >
        <svg className="canvas-edges" width={laid.w} height={laid.h}>
          <defs>
            <marker
              id="ll-arrow"
              markerWidth="7"
              markerHeight="7"
              refX="6"
              refY="3.5"
              orient="auto"
            >
              <path d="M0,0 L7,3.5 L0,7" className="canvas-edge-marker" />
            </marker>
          </defs>
          {edges.map((e, i) => {
            const a = laid.pos.get(e.from);
            const b = laid.pos.get(e.to);
            if (!a || !b) return null;
            const x1 = a.x + NODE_W;
            const y1 = a.y + NODE_H / 2;
            const x2 = b.x - 4;
            const y2 = b.y + NODE_H / 2;
            const dx = Math.max(24, (x2 - x1) / 2);
            return (
              <Fragment key={`${e.from}-${e.to}-${i}`}>
                <path
                  className="canvas-edge"
                  d={`M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`}
                  markerEnd="url(#ll-arrow)"
                />
                {e.label && (
                  <text
                    className="canvas-edge-label"
                    x={(x1 + x2) / 2}
                    y={(y1 + y2) / 2 - 4}
                    textAnchor="middle"
                  >
                    {e.label}
                  </text>
                )}
              </Fragment>
            );
          })}
        </svg>
        {nodes.map((n) => {
          const p = laid.pos.get(n.id);
          if (!p) return null;
          return (
            <div
              key={n.id}
              className={`canvas-node${selected === n.id ? " selected" : ""}`}
              style={{ left: p.x, top: p.y, width: NODE_W, height: NODE_H }}
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                onSelect(n.id);
              }}
            >
              <div className="canvas-node-id">{n.label}</div>
              <div className="canvas-node-sub">{n.sub}</div>
            </div>
          );
        })}
      </div>
      <div
        className="canvas-controls"
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        <button type="button" title="fit to view" onClick={fit}>
          fit
        </button>
        <button
          type="button"
          title="zoom in"
          onClick={() => setView((v) => ({ ...v, z: clamp(v.z * 1.25, MIN_Z, MAX_Z) }))}
        >
          +
        </button>
        <button
          type="button"
          title="zoom out"
          onClick={() => setView((v) => ({ ...v, z: clamp(v.z / 1.25, MIN_Z, MAX_Z) }))}
        >
          −
        </button>
      </div>
      <div className="canvas-hint">drag pan · wheel zoom · click node to inspect</div>
    </div>
  );
}
