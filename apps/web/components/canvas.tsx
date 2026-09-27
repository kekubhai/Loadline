"use client";

/**
 * Architecture canvas — grid, nodes, edges, zoom, pan, and direct
 * manipulation: add (palette drop), select, move, connect, delete,
 * fit, reset.
 *
 * Purely presentational: it renders the architecture the page owns and
 * reports intents back. Layout is deterministic (layered by dependency
 * depth) with optional per-node position overrides carried in the node
 * data, so the same state always draws identically. No simulation
 * behavior lives here and no numbers are derived — metrics shown on
 * nodes are backend results passed through verbatim.
 */
import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  PointerEvent as RPointerEvent,
  DragEvent as RDragEvent,
} from "react";

export interface CanvasNode {
  id: string;
  /** Provider/service or generic-kind line. */
  sub: string;
  /** Generic component class; drives the role tag. */
  role: string;
  /** True while a failure injection targets this component. */
  faulted?: boolean;
  /** Persistent user position; undefined = auto layered layout. */
  pos?: { x: number; y: number };
  /** Compact measured values from the last run (display only). */
  metrics?: { rps?: number; util?: number };
}

export interface CanvasEdge {
  from: string;
  to: string;
  /** Link condition ("read" / "write" / "") rendered at the midpoint. */
  label?: string;
}

const NODE_W = 148;
const NODE_H = 60;
const GAP_X = 72;
const GAP_Y = 30;
const PAD = 48;
const GRID = 24;
const MIN_Z = 0.3;
const MAX_Z = 2.5;

function clamp(v: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, v));
}

/** Two-letter role tag per generic component class. */
const ROLE_TAG: Record<string, string> = {
  client: "CL",
  "load balancer": "LB",
  "api server": "AP",
  cache: "CA",
  queue: "QU",
  worker: "WK",
  database: "DB",
  "object storage": "OS",
  network: "NW",
};

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
  const colHeights = depths.map(
    (d) => cols.get(d)!.length * (NODE_H + GAP_Y) - GAP_Y,
  );
  const innerH = Math.max(0, ...colHeights);
  const pos = new Map<string, { x: number; y: number }>();
  depths.forEach((d, ci) => {
    const ids = cols.get(d)!;
    const y0 = PAD + (innerH - colHeights[ci]) / 2;
    ids.forEach((id, i) => {
      pos.set(id, {
        x: PAD + ci * (NODE_W + GAP_X),
        y: y0 + i * (NODE_H + GAP_Y),
      });
    });
  });
  // User positions win over the layered layout.
  for (const n of nodes) {
    if (n.pos) pos.set(n.id, n.pos);
  }
  const w =
    PAD * 2 + Math.max(0, depths.length - 1) * (NODE_W + GAP_X) + NODE_W;
  const h = PAD * 2 + innerH;
  return { pos, w, h };
}

export interface CanvasHandles {
  fit: () => void;
  reset: () => void;
}

export function ArchitectureCanvas({
  nodes,
  edges,
  selected,
  metricsByComponent,
  faultedByComponent,
  onSelect,
  onMoveNode,
  onResetLayout,
  onConnect,
  onDeleteNode,
  onDeleteEdge,
  onDropService,
  handleRef,
}: {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  selected: string | null;
  metricsByComponent: Map<string, { rps?: number; util?: number }>;
  faultedByComponent: Set<string>;
  onSelect: (id: string | null) => void;
  onMoveNode: (id: string, x: number, y: number) => void;
  /** Clear all persistent positions (reset layout). */
  onResetLayout: () => void;
  onConnect: (from: string, to: string) => void;
  onDeleteNode: (id: string) => void;
  onDeleteEdge: (index: number) => void;
  /** A palette service was dropped at world coordinates. */
  onDropService: (
    provider: string,
    service: string,
    x: number,
    y: number,
  ) => void;
  /** Imperative fit/reset for toolbar buttons owned by the page. */
  handleRef: { current: CanvasHandles | null };
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ x: 24, y: 24, z: 1 });
  const [panning, setPanning] = useState(false);
  const panRef = useRef<{ px: number; py: number; vx: number; vy: number } | null>(
    null,
  );
  const movedRef = useRef(false);
  // True between a node/port pointerdown and the click that follows it,
  // so the viewport's deselect-on-click never fires for node clicks.
  const nodeClickRef = useRef(false);
  const fittedRef = useRef(false);

  /* --------------------------------------------------- node drag state -- */
  const [dragNode, setDragNode] = useState<string | null>(null);
  const dragRef = useRef<{ id: string; dx: number; dy: number } | null>(null);
  const [dragPos, setDragPos] = useState<{ id: string; x: number; y: number } | null>(
    null,
  );

  /* ------------------------------------------------- connect drag state -- */
  const [connectFrom, setConnectFrom] = useState<string | null>(null);
  const [cursor, setCursor] = useState<{ x: number; y: number } | null>(null);

  /* --------------------------------------------------- palette dropping -- */
  const [dropTarget, setDropTarget] = useState(false);

  /* ------------------------------------------------------ edge hover/deletion -- */
  const [hoverEdge, setHoverEdge] = useState<number | null>(null);

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

  /** Reset view: clear persistent positions, re-layer, refit. */
  const reset = useCallback(() => {
    onResetLayout();
    // Layout bounds depend only on graph structure, so the current fit
    // math stays valid for the re-layered graph.
    fit();
  }, [onResetLayout, fit]);

  // Publish imperative handles after every render so closures stay fresh.
  useEffect(() => {
    handleRef.current = { fit, reset };
  });

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

  /** Screen → world coordinates. */
  function toWorld(clientX: number, clientY: number) {
    const vp = viewportRef.current;
    if (!vp) return { x: 0, y: 0 };
    const rect = vp.getBoundingClientRect();
    return {
      x: (clientX - rect.left - view.x) / view.z,
      y: (clientY - rect.top - view.y) / view.z,
    };
  }

  /** Nearest node center within snap radius (connect targeting). */
  function nodeAtWorld(x: number, y: number, exclude?: string): string | null {
    let best: string | null = null;
    let bestD = Infinity;
    for (const n of nodes) {
      if (n.id === exclude) continue;
      const p = laid.pos.get(n.id);
      if (!p) continue;
      const d = Math.hypot(
        x - (p.x + NODE_W / 2),
        y - (p.y + NODE_H / 2),
      );
      if (d < bestD) {
        bestD = d;
        best = n.id;
      }
    }
    return bestD <= NODE_W * 0.9 ? best : null;
  }

  /* ------------------------------------------------------ pointer flow -- */

  function onViewportPointerDown(e: RPointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    nodeClickRef.current = false;
    panRef.current = { px: e.clientX, py: e.clientY, vx: view.x, vy: view.y };
    movedRef.current = false;
    setPanning(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function onViewportPointerMove(e: RPointerEvent<HTMLDivElement>) {
    // Connect drag: track the rubber-band cursor.
    if (connectFrom) {
      setCursor(toWorld(e.clientX, e.clientY));
      return;
    }
    // Node drag.
    const d = dragRef.current;
    if (d) {
      const w = toWorld(e.clientX, e.clientY);
      setDragPos({ id: d.id, x: Math.round(w.x - d.dx), y: Math.round(w.y - d.dy) });
      return;
    }
    // Pan.
    const p = panRef.current;
    if (!p) return;
    const dx = e.clientX - p.px;
    const dy = e.clientY - p.py;
    if (Math.abs(dx) + Math.abs(dy) > 3) movedRef.current = true;
    setView((v) => ({ ...v, x: p.vx + dx, y: p.vy + dy }));
  }

  function onViewportPointerUp(e: RPointerEvent<HTMLDivElement>) {
    // Finish a connect drag against the node under the cursor.
    if (connectFrom) {
      const w = toWorld(e.clientX, e.clientY);
      const to = nodeAtWorld(w.x, w.y, connectFrom);
      setConnectFrom(null);
      setCursor(null);
      if (to) onConnect(connectFrom, to);
      return;
    }
    // Commit a node drag.
    const d = dragRef.current;
    if (d) {
      if (dragPos && dragPos.id === d.id) {
        onMoveNode(d.id, dragPos.x, dragPos.y);
      }
      dragRef.current = null;
      setDragPos(null);
      setDragNode(null);
      return;
    }
    panRef.current = null;
    setPanning(false);
  }

  /* ------------------------------------------------------ node dragging -- */

  function startNodeDrag(e: RPointerEvent<HTMLElement>, id: string) {
    const p = laid.pos.get(id);
    if (!p) return;
    const w = toWorld(e.clientX, e.clientY);
    dragRef.current = { id, dx: w.x - p.x, dy: w.y - p.y };
    setDragNode(id);
    nodeClickRef.current = true;
    viewportRef.current?.setPointerCapture(e.pointerId);
  }

  /* ------------------------------------------------------- connect mode -- */

  function startConnect(e: RPointerEvent<HTMLElement>, id: string) {
    e.stopPropagation();
    setConnectFrom(id);
    setCursor(toWorld(e.clientX, e.clientY));
    nodeClickRef.current = true;
    viewportRef.current?.setPointerCapture(e.pointerId);
  }

  /* ----------------------------------------------------------- palette -- */

  function onDragOver(e: RDragEvent) {
    if (!e.dataTransfer.types.includes("loadline/service")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDropTarget(true);
  }

  function onDrop(e: RDragEvent) {
    setDropTarget(false);
    const payload = e.dataTransfer.getData("loadline/service");
    if (!payload) return;
    e.preventDefault();
    const sep = payload.indexOf(":");
    const provider = payload.slice(0, sep);
    const service = payload.slice(sep + 1);
    const w = toWorld(e.clientX, e.clientY);
    onDropService(provider, service, Math.round(w.x), Math.round(w.y));
  }

  /* ------------------------------------------------------------ render -- */

  const pendingTo =
    connectFrom && cursor
      ? (() => {
          const target = nodeAtWorld(cursor.x, cursor.y, connectFrom);
          if (!target) return cursor;
          const p = laid.pos.get(target);
          return p
            ? { x: p.x + NODE_W / 2, y: p.y + NODE_H / 2 }
            : cursor;
        })()
      : null;

  return (
    <div
      ref={viewportRef}
      className={[
        "canvas-viewport",
        panning ? " panning" : "",
        dropTarget ? " canvas-drop" : "",
      ].join("")}
      style={{
        backgroundPosition: `${view.x}px ${view.y}px`,
        backgroundSize: `${GRID * view.z}px ${GRID * view.z}px`,
      }}
      onPointerDown={onViewportPointerDown}
      onPointerMove={onViewportPointerMove}
      onPointerUp={onViewportPointerUp}
      onPointerCancel={() => {
        panRef.current = null;
        setPanning(false);
        dragRef.current = null;
        setDragPos(null);
        setDragNode(null);
        setConnectFrom(null);
        setCursor(null);
      }}
      onClick={() => {
        if (movedRef.current) return;
        if (nodeClickRef.current) {
          nodeClickRef.current = false;
          return;
        }
        onSelect(null);
      }}
      onDragOver={onDragOver}
      onDragLeave={() => setDropTarget(false)}
      onDrop={onDrop}
    >
      <div
        className="canvas-world"
        style={{
          transform: `translate(${view.x}px, ${view.y}px) scale(${view.z})`,
          width: Math.max(laid.w, 1),
          height: Math.max(laid.h, 1),
        }}
      >
        <svg
          className="canvas-edges"
          width={Math.max(laid.w, 1)}
          height={Math.max(laid.h, 1)}
        >
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
            const d = `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
            const hover = hoverEdge === i;
            return (
              <Fragment key={`${e.from}-${e.to}-${i}`}>
                <path
                  className={`canvas-edge${hover ? " hover" : ""}`}
                  d={d}
                  markerEnd="url(#ll-arrow)"
                />
                {/* Wide invisible hit path: hover highlight + click selects. */}
                <path
                  className="canvas-edge-hit"
                  d={d}
                  onPointerEnter={() => setHoverEdge(i)}
                  onPointerLeave={() =>
                    setHoverEdge((h) => (h === i ? null : h))
                  }
                  onClick={(ev) => {
                    ev.stopPropagation();
                    onSelect(`link:${i}`);
                  }}
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
          {connectFrom && pendingTo && (
            <PendingEdge from={laid.pos.get(connectFrom)} to={pendingTo} />
          )}
        </svg>

        {nodes.map((n) => {
          const p =
            dragPos && dragPos.id === n.id
              ? { x: dragPos.x, y: dragPos.y }
              : laid.pos.get(n.id);
          if (!p) return null;
          const m = metricsByComponent.get(n.id);
          const faulted = faultedByComponent.has(n.id);
          const isTarget =
            connectFrom !== null &&
            n.id !== connectFrom &&
            cursor !== null &&
            nodeAtWorld(cursor.x, cursor.y, connectFrom) === n.id;
          return (
            <div
              key={n.id}
              className={[
                "canvas-node",
                selected === n.id ? "selected" : "",
                dragNode === n.id ? "dragging" : "",
                faulted ? "faulted" : "",
                isTarget ? "connect-target" : "",
              ]
                .filter(Boolean)
                .join(" ")}
              style={{ left: p.x, top: p.y, width: NODE_W, height: NODE_H }}
              onPointerDown={(e) => {
                if (e.button !== 0) return;
                e.stopPropagation();
                onSelect(n.id);
                startNodeDrag(e, n.id);
              }}
              onDoubleClick={() => onDeleteNode(n.id)}
              title="drag to move · double-click to delete"
              onClick={(e) => e.stopPropagation()}
            >
              <div className="canvas-node-top">
                <span className="canvas-node-id">{n.id}</span>
                <span className="canvas-node-tag">
                  {ROLE_TAG[n.role] ?? "··"}
                </span>
              </div>
              <div className="canvas-node-sub">{n.sub}</div>
              <div className="canvas-node-metrics">
                <span className="canvas-node-metric">
                  {m?.rps !== undefined ? `${Math.round(m.rps)} rps` : "— rps"}
                </span>
                <span
                  className={`canvas-node-metric${
                    (m?.util ?? 0) >= 0.9 ? " hot" : ""
                  }`}
                >
                  {m?.util !== undefined ? `${Math.round(m.util * 100)}%` : "— %"}
                </span>
              </div>
              <button
                type="button"
                className="canvas-node-port"
                title="drag to connect"
                onPointerDown={(e) => startConnect(e, n.id)}
                onClick={(e) => e.stopPropagation()}
              />
              {faulted && (
                <span className="canvas-node-fault" title="failure injected" />
              )}
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
        <button type="button" title="reset layout and view" onClick={reset}>
          reset
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
      <div className="canvas-hint">
        drag service from palette · drag dot to connect · click edge to edit
      </div>
    </div>
  );
}

/** Rubber-band edge while connecting. */
function PendingEdge({
  from,
  to,
}: {
  from?: { x: number; y: number };
  to: { x: number; y: number };
}) {
  if (!from) return null;
  const x1 = from.x + NODE_W;
  const y1 = from.y + NODE_H / 2;
  const dx = Math.max(24, Math.abs(to.x - x1) / 2);
  return (
    <path
      className="canvas-edge canvas-edge-pending"
      d={`M ${x1} ${y1} C ${x1 + dx} ${y1}, ${to.x - dx} ${to.y}, ${to.x} ${to.y}`}
      markerEnd="url(#ll-arrow)"
    />
  );
}
