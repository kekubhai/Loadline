"use client";

/**
 * LOADLINE — workload definition panel.
 *
 * Edits the canonical WorkloadSpec through callbacks; it never keeps its
 * own copy. The right column renders the derivation chain with the
 * intermediate values the BACKEND will compute from these inputs (the
 * load plan is echoed back in every run's FinalResults.plan). The panel
 * intentionally does not compute RPS in the editor: the derivation is
 * the simulator's job, and the plan panel in the console shows the real
 * derived numbers once a run exists.
 */
import { f0, f1, pct } from "./format";
import { BigIntInput, NumInput, Row } from "./inspector-forms";
import { Divider, EmptyState, Section } from "./ui";
import type { WorkloadSpec } from "@loadline/api";

export function WorkloadPanel({
  workload,
  onPatch,
  plan,
}: {
  workload: WorkloadSpec;
  onPatch: (patch: Partial<WorkloadSpec>) => void;
  /** Last run's derived plan (backend output); null before the first run. */
  plan?: {
    dauFraction: number;
    requestsPerDay: number;
    averageRps: number;
    peakRps: number;
    readFraction: number;
    writeFraction: number;
    meanInterArrivalMillis: number;
  } | null;
}) {
  return (
    <div className="wl-grid">
      <div className="wl-form">
        <Row label="total users">
          <BigIntInput
            value={workload.totalUsers}
            onChange={(v) => onPatch({ totalUsers: v })}
          />
        </Row>
        <Row label="dau">
          <BigIntInput value={workload.dau} onChange={(v) => onPatch({ dau: v })} />
        </Row>
        <Row label="requests / user / day">
          <NumInput
            value={workload.requestsPerUserPerDay}
            min={0}
            step={1}
            onChange={(v) => onPatch({ requestsPerUserPerDay: v })}
          />
        </Row>
        <Row label="peak multiplier">
          <NumInput
            value={workload.peakMultiplier}
            min={1}
            step={0.5}
            onChange={(v) => onPatch({ peakMultiplier: v })}
          />
        </Row>
        <Row label="reads per write">
          <NumInput
            value={workload.readWriteRatio}
            min={0.1}
            step={0.5}
            onChange={(v) => onPatch({ readWriteRatio: v })}
          />
          <span className="row-hint">
            {workload.readWriteRatio > 0
              ? `${pct(1 / (1 + workload.readWriteRatio))} writes`
              : "must be > 0"}
          </span>
        </Row>
        <Row label="payload bytes">
          <BigIntInput
            value={workload.payloadBytes}
            min={1n}
            onChange={(v) => onPatch({ payloadBytes: v })}
          />
        </Row>
      </div>

      <div className="wl-derive">
        <Section label="derivation chain — evaluated by the simulator">
          <div className="src-chain">
            <div className="src-row">
              <span className="src-k">users</span>
              <span className="src-v">{f0(workload.totalUsers)}</span>
            </div>
            <span className="src-arrow">
              ↓ × dau fraction
              {plan ? ` = ${pct(plan.dauFraction)}` : ""}
            </span>
            <div className="src-row">
              <span className="src-k">dau</span>
              <span className="src-v">{f0(workload.dau)}</span>
            </div>
            <span className="src-arrow">
              ↓ × {f1(workload.requestsPerUserPerDay)} req/user/day
            </span>
            <div className="src-row">
              <span className="src-k">requests/day</span>
              <span className="src-v">
                {plan ? f0(plan.requestsPerDay) : "—"}
              </span>
            </div>
            <span className="src-arrow">↓ ÷ 86400 s/day</span>
            <div className="src-row">
              <span className="src-k">average rps</span>
              <span className="src-v">{plan ? f1(plan.averageRps) : "—"}</span>
            </div>
            <span className="src-arrow">
              ↓ × {f1(workload.peakMultiplier)} peak multiplier
            </span>
            <div className="src-row">
              <span className="src-k">peak rps</span>
              <span className="src-v">{plan ? f1(plan.peakRps) : "—"}</span>
            </div>
          </div>
          {plan ? (
            <>
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
                  <span className="kv-key">mean inter-arrival</span>
                  <span className="kv-val">
                    {f1(plan.meanInterArrivalMillis)} ms
                  </span>
                </span>
              </div>
            </>
          ) : (
            <EmptyState>
              run a simulation to see the derived plan — the editor does not
              guess these numbers.
            </EmptyState>
          )}
        </Section>
      </div>
    </div>
  );
}
