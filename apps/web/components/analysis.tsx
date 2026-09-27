"use client";

/**
 * LOADLINE — post-run analysis surfaces.
 *
 * One module for the four terminal artifacts of a run: BOTTLENECK,
 * CAPACITY, COST, and ARCHITECTURE STATUS. Every number is read verbatim
 * from the backend's diagnosis, capacity reports, and cost estimate —
 * no derived or recomputed values here. The layout is deliberately dense
 * and monochrome: status color only where the backend assigned severity.
 */
import type { ReactNode } from "react";
import type {
  CapacityReport,
  CostEstimate,
  Diagnosis,
  SystemMetrics,
} from "@loadline/api";
import { f0, f1, pct, price } from "./format";
import { Divider, EmptyState, Section } from "./ui";

type Tone = "neutral" | "ok" | "warn" | "bad";

function severityTone(severity: string): Tone {
  switch (severity) {
    case "critical":
      return "bad";
    case "high":
      return "warn";
    default:
      return "neutral";
  }
}

/** One big-number cell: tiny label over a large tabular value. */
function Headline({
  label,
  value,
  unit,
  note,
  tone = "neutral",
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  note?: string;
  tone?: Tone;
}) {
  return (
    <div className={`headline headline-${tone}`}>
      <div className="headline-label">{label}</div>
      <div className="headline-value">
        {value}
        {unit && <span className="headline-unit"> {unit}</span>}
      </div>
      {note && <div className="headline-note">{note}</div>}
    </div>
  );
}

/* ---------------------------------------------------------- bottleneck --- */

/**
 * The run's bottleneck report — the most prominent post-run surface.
 * Shows the worst component (backend-ordered) with its utilization,
 * queue, machine-computed reasons, and measured impacts. No generated
 * prose: every line is a simulator-computed string.
 */
export function BottleneckPanel({
  diagnosis,
  metrics,
}: {
  diagnosis: Diagnosis | null;
  metrics: SystemMetrics | null;
}) {
  if (!diagnosis) {
    return <EmptyState>no diagnosis — run a simulation first.</EmptyState>;
  }

  const primary = diagnosis.bottlenecks[0];
  const secondary = diagnosis.bottlenecks.slice(1);

  if (!primary) {
    return (
      <div className="diag-clear">
        <span className="diag-clear-mark">✓</span> no bottleneck — arrival
        rates, queues, and utilization stayed within modeled limits for the
        whole run.
      </div>
    );
  }

  const comp = metrics?.components.find((c) => c.id === primary.componentId);
  const meta: string[] = [];
  if (comp) {
    meta.push(`${pct(comp.utilization)} utilization`);
    meta.push(`queue ${f0(comp.maxQueueDepth)}`);
    meta.push(`${f1(comp.arrivalRps)} rps in`);
  }

  return (
    <div className={`bottleneck bottleneck-${severityTone(primary.severity)}`}>
      <div className="bottleneck-tag">BOTTLENECK</div>
      <div className="bottleneck-head">
        <span className="bottleneck-name">{primary.componentId}</span>
        <span className={`badge badge-${severityTone(primary.severity)}`}>
          {primary.severity}
        </span>
        {primary.kind && <span className="bottleneck-kind">{primary.kind}</span>}
      </div>
      {meta.length > 0 && (
        <div className="bottleneck-meta">{meta.join(" · ")}</div>
      )}

      <div className="bottleneck-cols">
        <div>
          <div className="bn-label">why</div>
          <ul className="bn-list">
            {primary.reasons.map((r, i) => (
              <li key={i}>{r}</li>
            ))}
          </ul>
        </div>
        {(primary.impacts.length > 0 || diagnosis.impacts.length > 0) && (
          <div>
            <div className="bn-label">impact</div>
            <ul className="bn-list bn-impacts">
              {primary.impacts.map((r, i) => (
                <li key={`p${i}`}>{r}</li>
              ))}
              {diagnosis.impacts.map((r, i) => (
                <li key={`d${i}`}>{r}</li>
              ))}
            </ul>
          </div>
        )}
      </div>

      {secondary.length > 0 && (
        <>
          <Divider />
          <Section label={`secondary — ${secondary.length}`}>
            {secondary.map((b) => (
              <div key={b.componentId} className="diag">
                <div className="diag-head">
                  <span className="diag-id">{b.componentId}</span>
                  <span className={`badge badge-${severityTone(b.severity)}`}>
                    {b.severity}
                  </span>
                </div>
                <ul className="reasons">
                  {b.reasons.slice(0, 2).map((r, i) => (
                    <li key={i}>{r}</li>
                  ))}
                </ul>
              </div>
            ))}
          </Section>
        </>
      )}
    </div>
  );
}

/* ------------------------------------------------------------ capacity --- */

/**
 * Per-component capacity: CURRENT LOAD / MAX SUSTAINABLE / HEADROOM with
 * a thin utilization meter. Reports arrive backend-sorted (worst first);
 * order is kept stable here.
 */
export function CapacityPanel({
  reports,
}: {
  reports: CapacityReport[];
}) {
  if (reports.length === 0) {
    return (
      <EmptyState>
        no capacity estimates — components need provider + service references.
      </EmptyState>
    );
  }
  return (
    <div className="cap-list">
      {reports.map((r) => {
        const util = Math.min(1, Math.max(0, r.utilization));
        const tone: Tone = r.saturated ? "bad" : util >= 0.9 ? "warn" : "neutral";
        return (
          <div key={r.componentId} className="cap-row">
            <div className="cap-id">
              {r.componentId}
              <span className="cap-svc">
                {r.provider}/{r.service}
              </span>
              {r.saturated && <span className="badge badge-bad">saturated</span>}
              {r.bottleneck && <span className="badge badge-warn">bottleneck</span>}
            </div>
            <div className="cap-heads">
              <Headline label="current load" value={`${f0(r.currentRps)}`} unit="rps" />
              <Headline
                label="max sustainable"
                value={f0(r.maxSustainableRps)}
                unit="rps"
              />
              <Headline
                label="headroom"
                value={pct(r.headroom)}
                tone={r.saturated ? "bad" : util >= 0.9 ? "warn" : "ok"}
              />
            </div>
            <div className="meter" aria-label={`utilization ${pct(util)}`}>
              <div className={`meter-fill meter-${tone}`} style={{ width: `${util * 100}%` }} />
              <span className="meter-cap">{pct(util)} util</span>
            </div>
            {r.assumptions.length > 0 && (
              <div className="cap-assume">{r.assumptions[0]}</div>
            )}
          </div>
        );
      })}
    </div>
  );
}

/* ---------------------------------------------------------------- cost --- */

/**
 * Monthly cost: total headline, per-category breakdown, the ESTIMATE
 * marker, and the full assumption trail from the pricing model.
 */
export function CostPanel({ estimate }: { estimate: CostEstimate | null }) {
  if (!estimate) {
    return (
      <EmptyState>
        no cost estimate — components need provider + service references.
      </EmptyState>
    );
  }
  const categories = Object.entries(estimate.byCategory).sort(
    ([a], [b]) => a.localeCompare(b),
  );
  return (
    <div className="cost">
      <div className="cost-head">
        <Headline
          label="estimated monthly cost"
          value={price(estimate.total)}
          note={estimate.currency}
        />
        <span className="panel-tag">estimate</span>
      </div>
      <table>
        <thead>
          <tr>
            <th>category</th>
            <th>monthly</th>
          </tr>
        </thead>
        <tbody>
          {categories.map(([cat, amt]) => (
            <tr key={cat}>
              <td>{cat}</td>
              <td className="num">{price(amt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {estimate.assumptions.length > 0 && (
        <>
          <Divider />
          <Section label="assumptions">
            <ul className="reasons reasons-dim">
              {estimate.assumptions.map((a, i) => (
                <li key={i}>{a}</li>
              ))}
            </ul>
          </Section>
        </>
      )}
    </div>
  );
}

/* ------------------------------------------------------ architecture status --- */

/**
 * Compact verdict list — NOT a score. Each row is a real check computed
 * from the run's own artifacts:
 *   capacity  — every provider-backed component under its ceiling
 *   latency   — system p95 within the run's timeout budget
 *   headroom  — per diagnosed bottleneck with a provider-backed report
 *   failure   — the configured injection scenario's measured outcome
 */
export function HealthPanel({
  diagnosis,
  reports,
  metrics,
  timeoutMs,
  failureTargets,
}: {
  diagnosis: Diagnosis | null;
  reports: CapacityReport[];
  metrics: SystemMetrics | null;
  /** The run's end-to-end timeout budget (the latency target). */
  timeoutMs: number;
  /** Components with a failure injection configured for the run. */
  failureTargets: string[];
}) {
  const rows: { mark: "ok" | "warn" | "bad"; label: string; detail: string }[] = [];

  // Capacity: saturated = arrival exceeded the modeled ceiling (backend flag).
  if (reports.length > 0) {
    const sat = reports.filter((r) => r.saturated);
    const tight = reports.filter((r) => !r.saturated && r.utilization >= 0.9);
    if (sat.length > 0) {
      rows.push({
        mark: "bad",
        label: "capacity",
        detail: `${sat.map((r) => r.componentId).join(", ")} over modeled ceiling`,
      });
    } else if (tight.length > 0) {
      rows.push({
        mark: "warn",
        label: "capacity",
        detail: `${tight.map((r) => r.componentId).join(", ")} ≥90% utilized`,
      });
    } else {
      rows.push({
        mark: "ok",
        label: "capacity",
        detail: `${reports.length} component(s) within ceiling`,
      });
    }
  }

  // Latency: measured p95 against the run's own timeout budget.
  if (metrics) {
    const okP95 = timeoutMs > 0 && metrics.p95Ms <= timeoutMs;
    rows.push({
      mark: okP95 ? "ok" : "bad",
      label: "latency target",
      detail: `p95 ${f1(metrics.p95Ms)}ms vs ${f0(timeoutMs)}ms budget`,
    });
  }

  // Headroom: a diagnosed bottleneck that also has a provider report.
  if (diagnosis && reports.length > 0) {
    for (const b of diagnosis.bottlenecks) {
      const rep = reports.find((r) => r.componentId === b.componentId);
      if (rep) {
        rows.push({
      mark: b.severity === "critical" ? "bad" : "warn",
      label: `${b.componentId} headroom`,
          detail: `${pct(rep.headroom)} remaining at ${f0(rep.currentRps)} rps`,
        });
      }
    }
  }

  // Failure scenario: the injection's measured outcome, not a guess.
  if (failureTargets.length > 0 && metrics) {
    const causedErrors =
      metrics.failed > 0 || metrics.rejected > 0 || metrics.timeoutRate > 0;
    rows.push({
      mark: causedErrors ? "bad" : "ok",
      label: `${failureTargets.join(", ")} failure scenario`,
      detail: causedErrors
        ? `${f0(metrics.failed)} failed · ${f0(metrics.rejected)} rejected · ${pct(metrics.timeoutRate)} timeouts`
        : "injection absorbed — no measurable degradation",
    });
  } else if (failureTargets.length === 0) {
    rows.push({
      mark: "ok",
      label: "failure scenario",
      detail: "none configured",
    });
  }

  return (
    <ul className="health">
      {rows.map((r, i) => (
        <li key={i} className="health-row">
          <span className={`health-mark health-${r.mark}`}>
            {r.mark === "ok" ? "✓" : r.mark === "warn" ? "⚠" : "✕"}
          </span>
          <span className="health-label">{r.label}</span>
          <span className="health-detail">{r.detail}</span>
        </li>
      ))}
    </ul>
  );
}