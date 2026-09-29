"use client";

/**
 * LOADLINE — architecture comparison view.
 *
 * Workflow: save architectures into a named library → select two or more
 * → confirm the shared workload (the exact workload the editor holds) →
 * run the comparison → read the results. The table renders backend
 * values verbatim; below it, each architecture's actual bottleneck with
 * its machine-computed evidence, the cost assumptions behind each
 * estimate, and the shared failure scenario's measured outcome per
 * architecture. There is deliberately NO overall score, NO ranking, and
 * NO recommendation: the user decides which tradeoff matters.
 */
import { useReducer, useState } from "react";
import { create } from "@loadline/api";
import type {
  Architecture,
  ComparisonEntry,
  ComparisonResult,
  SimulationOptions,
  WorkloadSpec,
} from "@loadline/api";
import {
  comparisonReducer,
  comparisonRows,
  initialComparisonState,
  primaryBottleneck,
} from "../app/comparison";
import type { SavedArchitecture } from "../app/comparison";
import { f0, f1, f2, pct, price } from "./format";
import {
  Badge,
  Button,
  EmptyState,
  Field,
  Input,
  Panel,
  Section,
  toneForWord,
} from "./ui";

export function ComparisonView({
  architecture,
  workload,
  options,
  runComparison,
}: {
  /** The architecture currently open in the editor (savable). */
  architecture: Architecture;
  /** The editor's workload — the comparison's shared workload. */
  workload: WorkloadSpec;
  /** The editor's options (seed, duration, retries, failure scenario). */
  options: SimulationOptions;
  /** Backend call: RunComparison with the shared inputs. */
  runComparison: (req: {
    workload: WorkloadSpec;
    architectures: { name: string; architecture: Architecture }[];
    options: SimulationOptions;
  }) => Promise<ComparisonResult>;
}) {
  const [state, dispatch] = useReducer(comparisonReducer, initialComparisonState);
  const [saveName, setSaveName] = useState("");

  const save = () => {
    const name = saveName.trim() || architecture.name || "architecture";
    dispatch({ type: "save", name, architecture });
    setSaveName("");
  };

  const run = async () => {
    const chosen = state.library.filter((e) => state.selected.includes(e.name));
    if (chosen.length < 2) return;
    dispatch({ type: "started" });
    dispatch({ type: "setSharedInputs", workload, options });
    try {
      const result = await runComparison({
        workload,
        architectures: chosen.map((c: SavedArchitecture) => ({
          name: c.name,
          architecture: c.architecture,
        })),
        options,
      });
      dispatch({ type: "completed", result });
    } catch (e) {
      dispatch({
        type: "failed",
        message: e instanceof Error ? e.message : String(e),
      });
    }
  };

  const entries = state.result?.entries ?? [];
  const rows = entries.length > 0 ? comparisonRows(entries) : [];
  const canRun = state.selected.length >= 2 && !state.running;
  const failureTargets = [...new Set((options.failures ?? []).map((f) => f.target))];

  return (
    <div className="cmp-view">
      {/* ------------------------------------------------- library ---- */}
      <div className="cmp-setup">
        <Panel
          title="Architecture library"
          tag="save snapshots, select two or more to compare"
        >
          <div className="cmp-save-row">
            <Field label="save current architecture as">
              <Input
                value={saveName}
                onChange={setSaveName}
                width={200}
                spellCheck={false}
              />
            </Field>
            <Button onClick={save}>save</Button>
          </div>

          {state.library.length === 0 ? (
            <EmptyState>
              nothing saved yet — build an architecture in the editor, then
              save it here under a name.
            </EmptyState>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>saved</th>
                  <th>components</th>
                  <th>links</th>
                  <th>select</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {state.library.map((e) => (
                  <tr key={e.name} className={state.selected.includes(e.name) ? "cmp-selected" : ""}>
                    <td>{e.name}</td>
                    <td>{e.architecture.components.length}</td>
                    <td>{e.architecture.links.length}</td>
                    <td>
                      <input
                        type="checkbox"
                        checked={state.selected.includes(e.name)}
                        onChange={() => dispatch({ type: "toggleSelect", name: e.name })}
                        aria-label={`select ${e.name}`}
                      />
                    </td>
                    <td>
                      <button
                        type="button"
                        className="btn btn-default cmp-remove"
                        onClick={() => dispatch({ type: "remove", name: e.name })}
                      >
                        remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>

        <Panel title="Shared workload" tag="identical for every architecture">
          {state.workload ? (
            <div className="kv">
              <span className="kv-item">
                <span className="kv-key">users</span>
                <span className="kv-val">{f0(state.workload.totalUsers)}</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">dau</span>
                <span className="kv-val">{f0(state.workload.dau)}</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">req/user/day</span>
                <span className="kv-val">{f1(state.workload.requestsPerUserPerDay)}</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">peak ×</span>
                <span className="kv-val">{f1(state.workload.peakMultiplier)}</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">seed</span>
                <span className="kv-val">{String(state.options?.seed ?? "")}</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">horizon</span>
                <span className="kv-val">{f0(state.options?.durationMs ?? 0)} ms</span>
              </span>
              <span className="kv-item">
                <span className="kv-key">failure scenario</span>
                <span className="kv-val">
                  {failureTargets.length > 0
                    ? `${failureTargets.join(", ")} (${(options.failures ?? []).map((f) => f.type).join(", ")})`
                    : "none"}
                </span>
              </span>
            </div>
          ) : (
            <EmptyState>
              the workload below is locked in when you run — it is the exact
              workload configured in the editor.
            </EmptyState>
          )}
          <div className="cmp-run-row">
            <Button
              variant="primary"
              disabled={!canRun}
              onClick={() => void run()}
            >
              {state.running
                ? "comparing…"
                : `run comparison (${state.selected.length} selected)`}
            </Button>
            <span className="prose prose-dim">
              each architecture is simulated independently with the same
              workload, seed, and options — differences in results come from
              the architecture alone.
            </span>
          </div>
          {state.error && <p className="error-line">error: {state.error}</p>}
        </Panel>
      </div>

      {/* -------------------------------------------------- results ---- */}
      {entries.length > 0 && (
        <>
          <Panel
            title="Comparison"
            tag={`same workload · seed ${String(state.result?.seed ?? "")} · no score, no ranking — you decide what matters`}
          >
            <div className="cmp-table-wrap">
              <table className="cmp-table">
                <thead>
                  <tr>
                    <th>metric</th>
                    {entries.map((e) => (
                      <th key={e.name} className="num">
                        {e.error !== "" ? `${e.name} (failed)` : e.name}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.metric}>
                      <td>{r.metric}</td>
                      {r.values.map((v, i) => (
                        <td key={i} className="num">
                          {v === null
                            ? "—"
                            : r.unit === "%"
                              ? pct(v)
                              : r.unit === "usd"
                                ? price(v)
                                : r.unit === "ms"
                                  ? f2(v)
                                  : f0(v)}
                        </td>
                      ))}
                    </tr>
                  ))}
                  <tr>
                    <td>bottleneck</td>
                    {entries.map((e) => (
                      <td key={e.name} className="num">
                        {primaryBottleneck(e) ?? (e.error !== "" ? "—" : "none")}
                      </td>
                    ))}
                  </tr>
                </tbody>
              </table>
            </div>
            <p className="prose prose-dim">
              peak RPS is the shared workload&apos;s offered peak; every other
              value is that architecture&apos;s measured result. A failed
              architecture shows its validation error instead of numbers.
            </p>
          </Panel>

          <Panel title="Bottlenecks" tag="per architecture · simulator-computed evidence">
            <div className="cmp-cols">
              {entries.map((e) => (
                <div key={e.name} className="cmp-col">
                  <div className="diag-head">
                    <span className="diag-id">{e.name}</span>
                  </div>
                  {e.error !== "" ? (
                    <p className="error-line">{e.error}</p>
                  ) : e.diagnosis && e.diagnosis.bottlenecks.length > 0 ? (
                    e.diagnosis.bottlenecks.map((b) => (
                      <div key={b.componentId} className="diag">
                        <div className="diag-head">
                          <span className="diag-id">{b.componentId}</span>
                          <Badge tone={toneForWord(b.severity)}>{b.severity}</Badge>
                        </div>
                        <ul className="reasons">
                          {b.reasons.map((r, i) => (
                            <li key={i}>{r}</li>
                          ))}
                        </ul>
                      </div>
                    ))
                  ) : (
                    <EmptyState>no bottleneck detected.</EmptyState>
                  )}
                </div>
              ))}
            </div>
          </Panel>

          <Panel title="Cost assumptions" tag="ESTIMATE — local pricing models, not live billing">
            <div className="cmp-cols">
              {entries.map((e) => (
                <div key={e.name} className="cmp-col">
                  <div className="diag-head">
                    <span className="diag-id">{e.name}</span>
                    {e.cost && <span className="panel-tag">{price(e.cost.total)}/mo</span>}
                  </div>
                  {!e.cost ? (
                    <EmptyState>
                      no estimate — components need provider references.
                    </EmptyState>
                  ) : (
                    <>
                      <ul className="reasons reasons-dim">
                        {e.cost.assumptions.map((a, i) => (
                          <li key={i}>{a}</li>
                        ))}
                      </ul>
                      <Section label="line items">
                        <table>
                          <tbody>
                            {e.cost.components.flatMap((cc) =>
                              cc.lineItems.map((li, i) => (
                                <tr key={`${cc.componentId}-${i}`}>
                                  <td>
                                    {cc.componentId} · {li.category}
                                    <span className="cmp-line-note">{li.description}</span>
                                  </td>
                                  <td className="num">{price(li.monthlyCost)}</td>
                                </tr>
                              )),
                            )}
                          </tbody>
                        </table>
                      </Section>
                    </>
                  )}
                </div>
              ))}
            </div>
          </Panel>

          {failureTargets.length > 0 && (
            <Panel
              title="Failure comparison"
              tag={`same scenario applied to every architecture: ${failureTargets.join(", ")}`}
            >
              <table>
                <thead>
                  <tr>
                    <th>architecture</th>
                    <th>failed</th>
                    <th>rejected</th>
                    <th>timeouts</th>
                    <th>error rate</th>
                    <th>p95</th>
                    <th>p99</th>
                    <th>bottleneck</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e) => (
                    <tr key={e.name}>
                      <td>{e.name}</td>
                      <td className="num">{e.metrics ? f0(e.metrics.failed) : "—"}</td>
                      <td className="num">{e.metrics ? f0(e.metrics.rejected) : "—"}</td>
                      <td className="num">{e.metrics ? f0(e.metrics.timeouts) : "—"}</td>
                      <td className="num">{e.metrics ? pct(e.metrics.errorRate) : "—"}</td>
                      <td className="num">{e.metrics ? f2(e.metrics.p95Ms) : "—"}</td>
                      <td className="num">{e.metrics ? f2(e.metrics.p99Ms) : "—"}</td>
                      <td>{primaryBottleneck(e) ?? "none"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="prose prose-dim">
                the failure scenario is configured in the editor (failure
                injection) and applied identically to every architecture in
                the comparison; targets must exist in each architecture.
              </p>
            </Panel>
          )}
        </>
      )}
    </div>
  );
}

/** One entry's availability of a full result set (used by tests). */
export function entryComplete(e: ComparisonEntry): boolean {
  return e.error === "" && !!e.metrics && !!e.diagnosis;
}
