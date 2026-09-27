"use client";

/**
 * LOADLINE — simulation console (operational controls).
 *
 * This component owns a run end to end: it creates and starts a paced
 * simulation, subscribes to StreamMetrics, and drives the run-control
 * RPCs (pause/resume/stop/set-wall-duration). It renders ONLY values
 * received from the backend — progress frames, control frames, and final
 * results. It computes no performance numbers of its own: the sparkline
 * and queue chart plot completed-count and queue-depth samples exactly
 * as the engine reported them.
 *
 * Pacing semantics (matches the backend contract): wallDurationMs is the
 * wall time the FULL horizon would take at 1× speed. Speed buttons
 * divide that base duration by the multiplier and push it via
 * SetWallDuration — pacing changes only wall-clock duration, never
 * results (determinism is enforced by backend tests). RESET creates a
 * fresh simulation: runs are immutable once finished.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  createLoadlineClient,
  RunStatus,
  type Architecture,
  type ControlFrame,
  type FinalResults,
  type LoadPlan,
  type ProgressSnapshot,
  type SimulationOptions,
  type WorkloadSpec,
} from "@loadline/api";
import { f0, f1, f2, kindName, pct } from "./format";
import {
  Badge,
  Button,
  Divider,
  EmptyState,
  Field,
  Input,
  Metric,
  Panel,
  Section,
  StatusIndicator,
  toneForWord,
} from "./ui";

/** Wall time the full horizon takes at 1× (the speed baseline). */
const BASE_PACE_MS = 10_000;
const SPEEDS = [1, 5, 10, 50];

/** What the owning page learns when a run reaches a terminal state. */
export interface SimOutcome {
  status: "completed" | "stopped";
  simId: string;
}

const RUN_LABELS: Record<number, string> = {
  [RunStatus.UNSPECIFIED]: "idle",
  [RunStatus.PENDING]: "pending",
  [RunStatus.RUNNING]: "running",
  [RunStatus.COMPLETED]: "completed",
  [RunStatus.FAILED]: "failed",
  [RunStatus.PAUSED]: "paused",
  [RunStatus.STOPPING]: "stopping…",
  [RunStatus.STOPPED]: "stopped",
};

function statusLabel(s: RunStatus): string {
  return RUN_LABELS[s] ?? "idle";
}

function statusTone(s: RunStatus): "ok" | "warn" | "bad" | "idle" {
  switch (s) {
    case RunStatus.COMPLETED:
      return "ok";
    case RunStatus.RUNNING:
    case RunStatus.PAUSED:
    case RunStatus.STOPPING:
      return "warn";
    case RunStatus.FAILED:
    case RunStatus.STOPPED:
      return "bad";
    default:
      return "idle";
  }
}

/* ------------------------------------------------------------ plots ------ */

/**
 * Sparkline over raw samples (already backend values, in sample order).
 * Returns a normalized SVG polyline — presentation only, no math on the
 * underlying metric beyond min/max scaling for display.
 */
function Sparkline({
  points,
  width = 260,
  height = 40,
  label,
}: {
  points: number[];
  width?: number;
  height?: number;
  label?: string;
}) {
  if (points.length === 0) {
    return <div className="spark spark-empty">collecting…</div>;
  }
  const min = Math.min(...points);
  const max = Math.max(...points);
  const span = max - min;
  const step = points.length > 1 ? width / (points.length - 1) : width;
  const d = points
    .map((p, i) => {
      const x = i * step;
      const y = span > 0 ? height - ((p - min) / span) * (height - 4) - 2 : height - 2;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <div className="spark">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        preserveAspectRatio="none"
        role="img"
        aria-label={label}
      >
        <path className="spark-line" d={d} />
      </svg>
      <span className="spark-cap">
        {label} {min === max ? `= ${f0(min)}` : `${f0(min)} → ${f0(max)}`}
      </span>
    </div>
  );
}

/** One horizontal bar of the live queue chart (depth / max, raw values). */
function QueueBar({ id, depth }: { id: string; depth: number }) {
  return (
    <div className="qbar">
      <span className="qbar-id">{id}</span>
      <div className="qbar-track">
        <div className={`qbar-fill${depth > 0 ? " hot" : ""}`} style={{ width: "100%" }} />
      </div>
      <span className="qbar-val">{f0(depth)}</span>
    </div>
  );
}

/* ------------------------------------------------------------- console --- */

export function SimConsole({
  architecture,
  workload,
  options,
  serverUrl,
  running,
  onRunningChange,
  onOutcome,
}: {
  /** Current editor architecture (canonical state lives in the page). */
  architecture: Architecture;
  workload: WorkloadSpec;
  options: SimulationOptions;
  serverUrl: string;
  /** True while the page considers a console run active. */
  running: boolean;
  onRunningChange: (v: boolean) => void;
  onOutcome: (outcome: SimOutcome) => void;
}) {
  // Display status starts as a mirror of `running`; it is then driven by
  // real control/status frames from the stream.
  const [status, setStatus] = useState<RunStatus>(
    running ? RunStatus.RUNNING : RunStatus.UNSPECIFIED,
  );
  /** Last N progress samples, in arrival order — sparkline history. */
  const [history, setHistory] = useState<ProgressSnapshot[]>([]);
  const [live, setLive] = useState<ProgressSnapshot | null>(null);
  const [results, setResults] = useState<FinalResults | null>(null);
  const [error, setError] = useState("");
  const [speed, setSpeed] = useState(1);
  const [paced, setPaced] = useState(false);
  const [elapsed, setElapsed] = useState(0);

  const runIdRef = useRef(0);
  const simIdRef = useRef<string | null>(null);
  const clientRef = useRef<ReturnType<typeof createLoadlineClient> | null>(null);
  const statusRef = useRef<RunStatus>(status);
  const speedRef = useRef(1);
  const startedRef = useRef(0);

  const applyStatus = useCallback((s: RunStatus) => {
    statusRef.current = s;
    setStatus(s);
  }, []);

  // Wall-clock stopwatch for the display only (simulation time comes
  // from the engine's snapshots — this never feeds back into the sim).
  useEffect(() => {
    if (status !== RunStatus.RUNNING) return;
    const t = window.setInterval(() => {
      setElapsed(Date.now() - startedRef.current);
    }, 100);
    return () => window.clearInterval(t);
  }, [status]);

  const busy =
    status === RunStatus.RUNNING ||
    status === RunStatus.PAUSED ||
    status === RunStatus.STOPPING;

  // Pause is offered once a real sample exists: before that the run may
  // already have drained its tiny event queue, and the backend rejects
  // pauses of finished runs (FailedPrecondition).
  const windowLive = (live?.eventsProcessed ?? 0n) > 0n;
  const canPause = status === RunStatus.RUNNING && windowLive;
  const canResume = status === RunStatus.PAUSED;
  const canStop = busy;
  const canSpeed = paced && status === RunStatus.RUNNING && windowLive;

  /* ------------------------------------------------------------ actions -- */

  const start = useCallback(async () => {
    const runId = ++runIdRef.current;
    const isCurrent = () => runIdRef.current === runId;

    const client = clientRef.current ?? createLoadlineClient({ baseUrl: serverUrl });
    clientRef.current = client;

    setLive(null);
    setResults(null);
    setError("");
    setSpeed(1);
    speedRef.current = 1;
    setPaced(true);
    setHistory([]);
    startedRef.current = Date.now();
    setElapsed(0);
    applyStatus(RunStatus.RUNNING);
    onRunningChange(true);

    try {
      const created = await client.createSimulation({
        architecture,
        workload,
        options,
      });
      const id = created.simulation?.id ?? "";
      if (!id) throw new Error("backend returned no simulation id");
      simIdRef.current = id;
      if (!isCurrent()) return;

      const streamPromise = (async () => {
        // Let RunSimulation land first so the stream replays live state
        // instead of racing the start RPC.
        await new Promise((r) => setTimeout(r, 20));
        const stream = client.streamMetrics({ simulationId: id });
        let gotResults: FinalResults | null = null;
        for await (const ev of stream) {
          if (!isCurrent()) return;
          const event = ev.event;
          switch (event.case) {
            case "progress": {
              const snap = event.value;
              if (snap) {
                setLive(snap);
                setHistory((h) => [...h.slice(-119), snap]);
              }
              break;
            }
            case "control": {
              const ctl: ControlFrame | undefined = event.value;
              if (ctl) applyStatus(ctl.status);
              break;
            }
            case "status":
              if (event.value) {
                applyStatus(event.value.status);
                if (event.value.status === RunStatus.FAILED) {
                  throw new Error(event.value.error || "simulation failed");
                }
              }
              break;
            case "results":
              if (event.value) {
                gotResults = event.value;
                setResults(event.value);
              }
              break;
          }
        }
        if (statusRef.current === RunStatus.FAILED) {
          throw new Error("simulation failed");
        }
        if (!gotResults) throw new Error("stream ended without results");
        return statusRef.current;
      })();
      // If the start RPC fails below, the floating stream must not
      // become an unhandled rejection.
      streamPromise.catch(() => {});

      await client.runSimulation({
        simulationId: id,
        wallDurationMs: BASE_PACE_MS / speedRef.current,
      });
      const finalStatus = await streamPromise;
      if (!isCurrent()) return;

      onRunningChange(false);
      onOutcome({
        status: finalStatus === RunStatus.STOPPED ? "stopped" : "completed",
        simId: id,
      });
    } catch (e) {
      if (!isCurrent()) return;
      onRunningChange(false);
      applyStatus(RunStatus.FAILED);
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [architecture, workload, options, serverUrl, applyStatus, onRunningChange, onOutcome]);

  const pause = useCallback(async () => {
    const id = simIdRef.current;
    const client = clientRef.current;
    if (!id || !client) return;
    try {
      await client.pauseSimulation({ simulationId: id });
      // The control frame on the stream confirms the transition.
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const resume = useCallback(async () => {
    const id = simIdRef.current;
    const client = clientRef.current;
    if (!id || !client) return;
    try {
      await client.resumeSimulation({
        simulationId: id,
        wallDurationMs: BASE_PACE_MS / speedRef.current,
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const stop = useCallback(async () => {
    const id = simIdRef.current;
    const client = clientRef.current;
    if (!id || !client) return;
    try {
      applyStatus(RunStatus.STOPPING);
      await client.stopSimulation({ simulationId: id });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [applyStatus]);

  const changeSpeed = useCallback(async (s: number) => {
    setSpeed(s);
    speedRef.current = s;
    const id = simIdRef.current;
    const client = clientRef.current;
    if (!id || !client || statusRef.current !== RunStatus.RUNNING || !paced) return;
    try {
      await client.setWallDuration({
        simulationId: id,
        wallDurationMs: BASE_PACE_MS / s,
      });
    } catch {
      // Pacing is best-effort display comfort; a rejected re-target must
      // not read as a failed run. The next control frame re-syncs us.
    }
  }, [paced]);

  /** RESET = abandon this run object and start a fresh one. */
  const reset = useCallback(() => {
    runIdRef.current++; // invalidates in-flight stream callbacks
    simIdRef.current = null;
    applyStatus(RunStatus.UNSPECIFIED);
    setLive(null);
    setResults(null);
    setError("");
    setHistory([]);
    setElapsed(0);
    setSpeed(1);
    speedRef.current = 1;
    setPaced(false);
    onRunningChange(false);
  }, [applyStatus, onRunningChange]);

  /* ----------------------------------------------------- derived views -- */

  const completedPoints = useMemo(
    () => history.map((s) => Number(s.completed)),
    [history],
  );
  const inFlight = live ? Number(live.inFlight) : 0;
  const rm = results?.metrics;
  const plan: LoadPlan | undefined = results?.plan;
  const simTime = live?.simTimeMs ?? rm?.durationMs ?? 0;

  return (
    <div className="sim-console">
      {/* ------------------------------------------------------ banner -- */}
      <div className={`sim-banner sim-banner-${statusTone(status)}`}>
        <StatusIndicator state={statusTone(status)} label={statusLabel(status)} />
        <span className="sim-banner-time">
          sim t={f0(simTime)}ms · wall {f1(elapsed / 1000)}s
          {live
            ? ` · events ${f0(live.eventsProcessed)}/${f0(
                Number(live.eventsProcessed) + Number(live.eventsPending),
              )}`
            : ""}
        </span>
        {live && (
          <span className="sim-banner-metric">generated {f0(live.generated)}</span>
        )}
        {live && (
          <span className="sim-banner-metric">in flight {f0(inFlight)}</span>
        )}
        <span className="sim-banner-spacer" />
        {simIdRef.current && <span className="sim-banner-id">{simIdRef.current}</span>}
      </div>

      {/* ---------------------------------------------------- controls -- */}
      <div className="sim-controls">
        <div className="ctl-group">
          <Button variant="primary" disabled={busy} onClick={() => void start()}>
            {busy ? "running…" : "run"}
          </Button>
          <Button disabled={!canPause} onClick={() => void pause()}>
            pause
          </Button>
          <Button disabled={!canResume} onClick={() => void resume()}>
            resume
          </Button>
          <Button variant="danger" disabled={!canStop} onClick={() => void stop()}>
            stop
          </Button>
          <Button disabled={running && busy} onClick={reset}>
            reset
          </Button>
        </div>

        <div className="ctl-group">
          <span className="ctl-label">speed</span>
          {SPEEDS.map((s) => (
            <button
              key={s}
              type="button"
              className={`pace-btn${speed === s ? " active" : ""}`}
              disabled={!canSpeed}
              title={
                canSpeed
                  ? undefined
                  : "available while running, after the first sample window"
              }
              onClick={() => void changeSpeed(s)}
            >
              {s}×
            </button>
          ))}
        </div>

        <span className="sim-controls-note">
          paced · horizon ≈ {f1(BASE_PACE_MS / speed / 1000)}s wall at this speed ·
          pacing never changes results
        </span>
      </div>

      {error && <p className="sim-error">error: {error}</p>}

      {/* ------------------------------------------------- live metrics -- */}
      {busy && (
        <div className="sim-console-main">
          <Panel title="Live" tag="sampled from the running engine">
            {live ? (
              <>
                <div className="metric-row">
                  <Metric label="sim time" value={f0(live.simTimeMs)} unit="ms" />
                  <Metric label="generated" value={f0(live.generated)} />
                  <Metric label="completed" value={f0(live.completed)} />
                  <Metric label="rejected" value={f0(live.rejected)} />
                  <Metric label="failed" value={f0(live.failed)} />
                  <Metric label="in flight" value={f0(inFlight)} />
                </div>
                <Divider />
                <Section label="completed requests — per progress sample">
                  <Sparkline points={completedPoints} label="completed" />
                  <p className="prose prose-dim">
                    cumulative completions at each progress sample; the slope
                    between samples is measured throughput.
                  </p>
                </Section>
                <Section label="queue depth (live)">
                  {live.components.map((c) => (
                    <QueueBar key={c.componentId} id={c.componentId} depth={c.queueDepth} />
                  ))}
                </Section>
              </>
            ) : (
              <EmptyState>
                waiting for the first progress sample from the engine…
              </EmptyState>
            )}
          </Panel>

          <Panel title="Occupancy" tag="per component · instantaneous">
            {live ? (
              <table>
                <thead>
                  <tr>
                    <th>component</th>
                    <th>kind</th>
                    <th>arrived</th>
                    <th>completed</th>
                    <th>queue</th>
                    <th>in flight</th>
                    <th>util</th>
                  </tr>
                </thead>
                <tbody>
                  {live.components.map((c) => (
                    <tr key={c.componentId}>
                      <td>{c.componentId}</td>
                      <td>{kindName(undefined)}</td>
                      <td>{f0(c.arrived)}</td>
                      <td>{f0(c.completed)}</td>
                      <td>{f0(c.queueDepth)}</td>
                      <td>{f0(c.inFlight)}</td>
                      <td>{pct(c.utilization)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <EmptyState>no samples yet.</EmptyState>
            )}
          </Panel>
        </div>
      )}

      {/* ------------------------------------------------ final results -- */}
      {results && rm && (
        <div className="sim-console-main">
          <Panel
            title="Results"
            tag={
              status === RunStatus.STOPPED
                ? `stopped at ${f0(rm.durationMs)}ms of the horizon — partial view`
                : `full horizon · stop reason ${results.summary?.stopReason ?? "n/a"}`
            }
          >
            <div className="metric-row">
              <Metric label="generated" value={f0(rm.generated)} />
              <Metric label="completed" value={f0(rm.completed)} />
              <Metric label="rejected" value={f0(rm.rejected)} />
              <Metric label="failed" value={f0(rm.failed)} />
              <Metric label="error rate" value={pct(rm.errorRate)} />
              <Metric label="timeout rate" value={pct(rm.timeoutRate)} />
            </div>
            <Divider />
            <Section label="latency ms">
              <div className="metric-row">
                <Metric label="avg" value={f2(rm.avgLatencyMs)} />
                <Metric label="p50" value={f2(rm.p50Ms)} />
                <Metric label="p95" value={f2(rm.p95Ms)} />
                <Metric label="p99" value={f2(rm.p99Ms)} />
                <Metric label="max" value={f2(rm.maxLatencyMs)} />
              </div>
            </Section>
            {status === RunStatus.STOPPED && (
              <p className="prose prose-dim">
                stopped before the horizon: rates describe the measured span only
                ({f0(rm.durationMs)}ms), not a full run.
              </p>
            )}
          </Panel>

          {plan && (
            <Panel title="Workload plan" tag="derived by the backend">
              <div className="src-chain">
                <div className="src-row">
                  <span className="src-k">users</span>
                  <span className="src-v">{f0(plan.totalUsers)}</span>
                </div>
                <span className="src-arrow">↓ × dau fraction {pct(plan.dauFraction)}</span>
                <div className="src-row">
                  <span className="src-k">dau</span>
                  <span className="src-v">{f0(plan.dau)}</span>
                </div>
                <span className="src-arrow">
                  ↓ × {f1(plan.requestsPerUserPerDay)} req/user/day
                </span>
                <div className="src-row">
                  <span className="src-k">requests/day</span>
                  <span className="src-v">{f0(plan.requestsPerDay)}</span>
                </div>
                <span className="src-arrow">↓ ÷ 86400s</span>
                <div className="src-row">
                  <span className="src-k">average rps</span>
                  <span className="src-v">{f1(plan.averageRps)}</span>
                </div>
                <span className="src-arrow">
                  ↓ × {f1(plan.peakMultiplier)} peak
                </span>
                <div className="src-row">
                  <span className="src-k">peak rps</span>
                  <span className="src-v">{f1(plan.peakRps)}</span>
                </div>
              </div>
              <Divider />
              <div className="kv">
                <span className="kv-item">
                  <span className="kv-key">reads</span>
                  <span className="kv-val">{pct(plan.readFraction)}</span>
                </span>
                <span className="kv-item">
                  <span className="kv-key">writes</span>
                  <span className="kv-val">{pct(plan.writeFraction)}</span>
                </span>
                <span className="kv-item">
                  <span className="kv-key">payload</span>
                  <span className="kv-val">{f0(plan.payloadBytes)} B</span>
                </span>
                <span className="kv-item">
                  <span className="kv-key">mean inter-arrival</span>
                  <span className="kv-val">{f2(plan.meanInterArrivalMillis)} ms</span>
                </span>
              </div>
            </Panel>
          )}
        </div>
      )}

      {status === RunStatus.FAILED && !error && (
        <Panel title="Failed">
          <EmptyState>the run failed without a reported reason.</EmptyState>
        </Panel>
      )}
    </div>
  );
}

/* Field + server-URL input are re-exported callers' concerns; the page
   keeps the server field in its toolbar so both views share it. */
export { Field };
