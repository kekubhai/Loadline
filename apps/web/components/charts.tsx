"use client";

/**
 * LOADLINE — post-run charts.
 *
 * Every chart here is a SCALED VIEW of numbers the simulator already
 * produced: bars are drawn against the largest value in their own row
 * (or against the run's timeout budget), and no chart invents, rounds
 * into existence, or interpolates a measurement. Where the backend has
 * no model for a value (no capacity ceiling, no failure records) the
 * chart says so instead of drawing a plausible-looking bar.
 *
 * Reusable primitives (Bar, Scale) live at the top so the latency,
 * load/capacity, outcome, and comparison charts share one geometry and
 * one set of tones.
 */
import type { ReactNode } from "react";
import type {
  ComparisonEntry,
  ComponentMetrics,
  FailureRecord,
  SystemMetrics,
} from "@loadline/api";
import { f0, f1, f2, pct } from "./format";
import { EmptyState } from "./ui";

type Tone = "neutral" | "ok" | "warn" | "bad";

/* ------------------------------------------------------------- geometry -- */

/** Largest value in a list, floored at `floor` so an all-zero chart is drawable. */
function scaleTo(values: number[], floor = 0): number {
  const max = values.reduce((m, v) => (Number.isFinite(v) && v > m ? v : m), floor);
  return max > 0 ? max : 1;
}

/** Fraction of the scale a value occupies, clamped to [0,1]. */
function frac(value: number, scale: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.max(0, Math.min(1, value / scale));
}

/* ------------------------------------------------------------------ bar -- */

/**
 * One horizontal bar row: fixed label column, a track with an optional
 * fill and an optional reference tick (e.g. a capacity ceiling or the
 * timeout budget), and a right-aligned formatted value.
 */
function Bar({
  label,
  display,
  fraction,
  tone = "neutral",
  tick,
  tickLabel,
  note,
}: {
  label: string;
  display: string;
  /** Bar length as a 0..1 fraction of the chart's scale. */
  fraction: number;
  tone?: Tone;
  /** Reference position on the same scale, as a 0..1 fraction. */
  tick?: number;
  tickLabel?: string;
  note?: ReactNode;
}) {
  const width = `${Math.max(0, Math.min(1, Number.isFinite(fraction) ? fraction : 0)) * 100}%`;
  return (
    <div className="chart-row">
      <span className="chart-label" title={label}>
        {label}
      </span>
      <span className="chart-track">
        <span className={`chart-fill chart-${tone}`} style={{ width }} />
        {tick !== undefined && (
          <span
            className="chart-tick"
            style={{
              left: `${Math.max(0, Math.min(1, Number.isFinite(tick) ? tick : 0)) * 100}%`,
            }}
            title={tickLabel}
          />
        )}
      </span>
      <span className="chart-value">{display}</span>
      {note !== undefined && <span className="chart-note">{note}</span>}
    </div>
  );
}

/** Chart heading + optional caption, matching the panel vocabulary. */
function ChartHead({ title, caption }: { title: string; caption?: string }) {
  return (
    <div className="chart-head">
      <span className="chart-title">{title}</span>
      {caption && <span className="chart-caption">{caption}</span>}
    </div>
  );
}

/* ------------------------------------------------------ latency profile -- */

/**
 * avg / p50 / p95 / p99 / max latency as bars on one scale, with the
 * run's timeout budget drawn as a reference tick. A bar past the tick is
 * over budget — the same comparison the health verdict makes, shown
 * geometrically.
 */
export function LatencyProfile({
  metrics,
  budgetMs,
}: {
  metrics: SystemMetrics | null;
  budgetMs: number;
}) {
  if (!metrics) {
    return <EmptyState>no latency measurements — run a simulation first.</EmptyState>;
  }
  const rows = [
    { key: "avg", label: "average", value: metrics.avgLatencyMs },
    { key: "p50", label: "p50", value: metrics.p50Ms },
    { key: "p95", label: "p95", value: metrics.p95Ms },
    { key: "p99", label: "p99", value: metrics.p99Ms },
    { key: "max", label: "max", value: metrics.maxLatencyMs },
  ];
  const hasBudget = budgetMs > 0;
  const scale = scaleTo(
    rows.map((r) => r.value).concat(hasBudget ? [budgetMs] : []),
    1,
  );

  return (
    <div className="chart">
      <ChartHead
        title="latency distribution"
        caption={
          hasBudget
            ? `dashed tick = ${f0(budgetMs)}ms timeout budget`
            : "no timeout budget configured"
        }
      />
      {rows.map((r) => {
        const over = hasBudget && r.value > budgetMs;
        return (
          <Bar
            key={r.key}
            label={r.label}
            display={`${f2(r.value)} ms`}
            fraction={frac(r.value, scale)}
            tone={over ? "bad" : r.key === "p99" || r.key === "max" ? "warn" : "neutral"}
            tick={hasBudget ? frac(budgetMs, scale) : undefined}
            tickLabel={hasBudget ? `budget ${f0(budgetMs)}ms` : undefined}
            note={over ? "over budget" : undefined}
          />
        );
      })}
      <p className="chart-foot">
        nearest-rank percentiles over terminal outcomes, measured by the
        simulator; {pct(metrics.timeoutRate)} of requests timed out.
      </p>
    </div>
  );
}

/* ----------------------------------------------------- load vs capacity -- */

/**
 * Per component: measured arrival rate as a bar, the component's modeled
 * capacity ceiling as a tick on the same scale. Components without a
 * capacity model show the bar and say so — no invented ceiling.
 */
export function LoadVsCapacity({
  components,
}: {
  components: ComponentMetrics[];
}) {
  if (components.length === 0) {
    return <EmptyState>no per-component metrics — run a simulation first.</EmptyState>;
  }
  const scale = scaleTo(
    components.flatMap((c) => [c.arrivalRps, c.capacityRps]),
    1,
  );

  return (
    <div className="chart">
      <ChartHead
        title="arrival load vs modeled capacity"
        caption="bar = measured arrival rps · tick = capacity ceiling"
      />
      {components.map((c) => {
        const tone: Tone = c.saturated
          ? "bad"
          : c.utilization >= 0.9
            ? "warn"
            : "neutral";
        const hasCeiling = c.capacityRps > 0;
        return (
          <Bar
            key={c.id}
            label={c.id}
            display={`${f1(c.arrivalRps)} rps`}
            fraction={frac(c.arrivalRps, scale)}
            tone={tone}
            tick={hasCeiling ? frac(c.capacityRps, scale) : undefined}
            tickLabel={hasCeiling ? `capacity ${f0(c.capacityRps)} rps` : undefined}
            note={
              hasCeiling ? (
                c.saturated ? (
                  <span className="chart-flag chart-flag-bad">over ceiling</span>
                ) : (
                  `${pct(c.utilization)} util`
                )
              ) : (
                <span className="chart-flag">no ceiling model</span>
              )
            }
          />
        );
      })}
      <p className="chart-foot">
        {components.filter((c) => c.capacityRps > 0).length} of{" "}
        {components.length} components carry a provider capacity model.
      </p>
    </div>
  );
}

/* ---------------------------------------------------------- outcome mix -- */

/**
 * Per component: how its arrivals terminated (completed / rejected /
 * failed), stacked to the component's arrival count. The three segments
 * are the component's own counters — the bar length is the arrival
 * total, nothing is normalized away.
 */
export function OutcomeMix({ components }: { components: ComponentMetrics[] }) {
  const rows = components.filter((c) => c.arrived > 0);
  if (rows.length === 0) {
    return <EmptyState>no component saw traffic in this run.</EmptyState>;
  }
  const scale = scaleTo(rows.map((c) => Number(c.arrived)), 1);

  return (
    <div className="chart">
      <ChartHead
        title="request outcomes per component"
        caption="green = completed · amber = rejected · red = failed"
      />
      {rows.map((c) => {
        const arrived = Number(c.arrived);
        const done = Number(c.completed);
        const rejected = Number(c.rejected);
        const failed = Number(c.failed);
        return (
          <div className="chart-row" key={c.id}>
            <span className="chart-label" title={c.id}>
              {c.id}
            </span>
            <span className="chart-track chart-track-stack">
              <span
                className="chart-seg chart-ok"
                style={{ width: `${(done / scale) * 100}%` }}
                title={`${f0(done)} completed`}
              />
              <span
                className="chart-seg chart-warn"
                style={{ width: `${(rejected / scale) * 100}%` }}
                title={`${f0(rejected)} rejected`}
              />
              <span
                className="chart-seg chart-bad"
                style={{ width: `${(failed / scale) * 100}%` }}
                title={`${f0(failed)} failed`}
              />
            </span>
            <span className="chart-value">{f0(arrived)}</span>
            <span className="chart-note">
              {f0(done)} · {f0(rejected)} · {f0(failed)}
            </span>
          </div>
        );
      })}
      <p className="chart-foot">
        arrived = completed + rejected + failed + still in flight at the
        horizon; segment widths share one scale across rows.
      </p>
    </div>
  );
}

/* ------------------------------------------------------- failure timeline -- */

/**
 * Request-level failures binned over the run's simulated span. Bins come
 * from the simulator's own FailureRecord timestamps (at_ms); the chart
 * only groups them for display.
 */
export function FailureTimeline({
  failures,
  durationMs,
}: {
  failures: FailureRecord[];
  durationMs: number;
}) {
  if (failures.length === 0) {
    return (
      <EmptyState>
        no request-level failures recorded — inject a failure or raise the
        load to produce one.
      </EmptyState>
    );
  }

  const span =
    durationMs > 0
      ? durationMs
      : failures.reduce((m, f) => Math.max(m, f.atMs), 0) || 1;
  const BINS = 24;
  const bins = new Array<number>(BINS).fill(0);
  const kinds = new Map<string, number>();
  let outside = 0;
  for (const f of failures) {
    const prev = kinds.get(f.kind) ?? 0;
    kinds.set(f.kind, prev + 1);
    const i = Math.floor((f.atMs / span) * BINS);
    if (i < 0 || i >= BINS) {
      outside++;
      continue;
    }
    bins[i]++;
  }
  const peak = scaleTo(bins, 1);

  return (
    <div className="chart">
      <ChartHead
        title="failure timeline"
        caption={`${f0(failures.length)} records across ${f1(span / 1000)}s of simulated time`}
      />
      <div className="chart-cols" role="img" aria-label="failures over simulated time">
        {bins.map((count, i) => (
          <span className="chart-col-cell" key={i}>
            <span
              className={`chart-col${count > 0 ? " chart-col-hot" : ""}`}
              style={{ height: `${Math.max(count > 0 ? 4 : 0, (count / peak) * 100)}%` }}
              title={`${f0(count)} failure(s) near t=${f1((i * span) / BINS / 1000)}s`}
            />
          </span>
        ))}
      </div>
      <div className="chart-axis">
        <span>t=0</span>
        <span>{f1(span / 2000)}s</span>
        <span>{f1(span / 1000)}s</span>
      </div>
      <div className="chart-legend">
        {[...kinds.entries()].map(([kind, n]) => (
          <span className="chart-legend-item" key={kind}>
            <span className="chart-swatch chart-bad" />
            {kind} · {f0(n)}
          </span>
        ))}
        {outside > 0 && (
          <span className="chart-legend-item">{f0(outside)} beyond the horizon</span>
        )}
      </div>
    </div>
  );
}

/* --------------------------------------------------------- comparison bars -- */

export interface CompareSeries {
  /** Metric name shown as the group heading. */
  label: string;
  /** Backend values, one per architecture, null when it has no value. */
  values: { name: string; value: number | null }[];
  /** Display formatter for the raw value. */
  format: (v: number) => string;
}

/**
 * Grouped bars for the comparison view: one group per metric, one bar
 * per architecture, each group scaled to its own largest value so
 * metrics with different units never share an axis. Missing values stay
 * as "—" instead of being coerced to zero.
 */
export function ComparisonBars({ series }: { series: CompareSeries[] }) {
  if (series.length === 0) {
    return <EmptyState>no comparable metrics in this result set.</EmptyState>;
  }
  return (
    <div className="chart">
      <ChartHead
        title="side by side"
        caption="each metric scaled to its own largest value · no score, no ranking"
      />
      {series.map((s) => {
        const scale = scaleTo(
          s.values.map((v) => v.value ?? 0),
          0,
        );
        const tones: Tone[] = ["neutral", "warn", "ok", "bad"];
        return (
          <div className="chart-group" key={s.label}>
            <div className="chart-group-head">{s.label}</div>
            {s.values.map((v, i) => (
              <Bar
                key={v.name}
                label={v.name}
                display={v.value === null ? "—" : s.format(v.value)}
                fraction={v.value === null ? 0 : frac(v.value, scale)}
                tone={v.value === null ? "neutral" : tones[i % tones.length]}
              />
            ))}
          </div>
        );
      })}
    </div>
  );
}

/** Metric rows fed to ComparisonBars from a comparison result set. */
export function comparisonSeries(entries: ComparisonEntry[]): CompareSeries[] {
  const ok = entries.filter((e) => !e.error && e.metrics);
  if (ok.length === 0) return [];
  return [
    {
      label: "p95 latency (ms)",
      values: entries.map((e) => ({ name: e.name, value: e.metrics ? e.metrics.p95Ms : null })),
      format: (v) => `${f2(v)} ms`,
    },
    {
      label: "p99 latency (ms)",
      values: entries.map((e) => ({ name: e.name, value: e.metrics ? e.metrics.p99Ms : null })),
      format: (v) => `${f2(v)} ms`,
    },
    {
      label: "error rate",
      values: entries.map((e) => ({ name: e.name, value: e.metrics ? e.metrics.errorRate : null })),
      format: (v) => pct(v),
    },
    {
      label: "completed requests",
      values: entries.map((e) => ({
        name: e.name,
        value: e.metrics ? Number(e.metrics.completed) : null,
      })),
      format: (v) => f0(v),
    },
    {
      label: "estimated monthly cost",
      values: entries.map((e) => ({ name: e.name, value: e.cost ? e.cost.total : null })),
      format: (v) => `$${v.toFixed(2)}`,
    },
  ];
}
