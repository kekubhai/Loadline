"use client";

/**
 * LOADLINE — shared editor form primitives.
 *
 * Extracted from the inspector so the workload panel, failure panel, and
 * inspector all use identical input behavior. Presentational only: every
 * component here writes through callbacks and holds no canonical state.
 */
import { useState } from "react";
import type { ReactNode } from "react";
import { create } from "@loadline/api";
import { FailureConfigSchema, FailureSchema, FailureType } from "@loadline/api";
import type { Failure } from "@loadline/api";
import { Button, Divider, EmptyState, Section, Select } from "./ui";
import { f0 } from "./format";

/** Human labels for the V1 failure types. */
export const FAILURE_LABELS: Record<number, string> = {
  [FailureType.CRASH]: "crash",
  [FailureType.INCREASED_LATENCY]: "increased latency",
  [FailureType.INCREASED_ERROR_RATE]: "increased error rate",
  [FailureType.NETWORK_FAILURE]: "network failure",
};

/**
 * Numeric input with local text state: commits valid numbers as they are
 * typed, restores the canonical value on blur. Avoids fighting the user
 * over intermediate states like "" or "0.".
 */
export function NumInput({
  value,
  onChange,
  step,
  min,
  max,
  width = 88,
}: {
  value: number;
  onChange: (v: number) => void;
  step?: number;
  min?: number;
  max?: number;
  width?: number;
}) {
  const [text, setText] = useState<string | null>(null);
  return (
    <input
      className="input input-num"
      style={{ width }}
      inputMode="decimal"
      value={text ?? String(value)}
      step={step}
      min={min}
      max={max}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        if (e.target.value.trim() !== "" && Number.isFinite(n)) {
          if ((min === undefined || n >= min) && (max === undefined || n <= max)) {
            onChange(n);
          }
        }
      }}
      onBlur={() => setText(null)}
    />
  );
}

/** Integer input (int32 fields). */
export function IntInput({
  value,
  onChange,
  min,
  width = 88,
}: {
  value: number;
  onChange: (v: number) => void;
  min?: number;
  width?: number;
}) {
  return (
    <NumInput
      value={value}
      onChange={(v) => onChange(Math.round(v))}
      min={min}
      width={width}
    />
  );
}

/** 64-bit integer input (int64 fields arrive as bigint). */
export function BigIntInput({
  value,
  onChange,
  min = 0n,
  width = 120,
}: {
  value: bigint;
  onChange: (v: bigint) => void;
  min?: bigint;
  width?: number;
}) {
  const [text, setText] = useState<string | null>(null);
  return (
    <input
      className="input input-num"
      style={{ width }}
      inputMode="numeric"
      value={text ?? value.toString()}
      onChange={(e) => {
        setText(e.target.value);
        try {
          const v = BigInt(e.target.value);
          if (v >= min) onChange(v);
        } catch {
          /* incomplete text — keep local state until it parses */
        }
      }}
      onBlur={() => setText(null)}
    />
  );
}

export function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="ins-row">
      <span className="ins-row-label">{label}</span>
      {children}
    </label>
  );
}

/* ------------------------------------------------------- failure editor --- */

/**
 * FailureEditor — schedule/remove injections for ONE target component.
 * Used by the inspector (target = selected component) and reused by the
 * simulation view's failure panel (target = a selected dropdown value).
 */
export function FailureEditor({
  target,
  failures,
  onAdd,
  onRemove,
  compact,
}: {
  target: string;
  failures: Failure[];
  onAdd: (f: Failure) => void;
  onRemove: (index: number) => void;
  /** Compact hides the section chrome (embedded in another panel). */
  compact?: boolean;
}) {
  const mine = failures
    .map((f, i) => ({ f, i }))
    .filter(({ f }) => f.target === target);
  const [type, setType] = useState<FailureType>(FailureType.CRASH);
  const [startMs, setStartMs] = useState(1000);
  const [durationMs, setDurationMs] = useState(5000);
  const [addedLatencyMs, setAddedLatencyMs] = useState(20);
  const [errorRate, setErrorRate] = useState(0.2);
  const [packetLossRate, setPacketLossRate] = useState(0.1);
  const [passThrough, setPassThrough] = useState(true);

  const body = (
    <>
      {mine.length === 0 && !compact && (
        <EmptyState>none scheduled for this component.</EmptyState>
      )}
      {mine.map(({ f, i }) => (
        <div key={`${f.target}-${i}`} className="ins-failure">
          <span className="ins-failure-line">
            {FAILURE_LABELS[f.type] ?? f.type} · t={f0(f.startMs)}ms →{" "}
            {f0(f.startMs + f.durationMs)}ms
            {f.config?.passThrough ? " · pass-through" : ""}
          </span>
          <button
            type="button"
            className="pal-del"
            title="remove failure"
            onClick={() => onRemove(i)}
          >
            ×
          </button>
        </div>
      ))}
      <div className="ins-failure-form">
        <Row label="type">
          <Select
            value={String(type)}
            onChange={(v) => setType(Number(v) as FailureType)}
            width={150}
            options={Object.entries(FAILURE_LABELS).map(([v, label]) => ({
              value: v,
              label,
            }))}
          />
        </Row>
        <Row label="start ms">
          <NumInput value={startMs} min={0} onChange={setStartMs} width={80} />
        </Row>
        <Row label="duration ms">
          <NumInput
            value={durationMs}
            min={1}
            onChange={setDurationMs}
            width={80}
          />
        </Row>
        {type === FailureType.INCREASED_LATENCY && (
          <Row label="added ms">
            <NumInput
              value={addedLatencyMs}
              min={0}
              onChange={setAddedLatencyMs}
              width={80}
            />
          </Row>
        )}
        {type === FailureType.INCREASED_ERROR_RATE && (
          <Row label="error rate">
            <NumInput
              value={errorRate}
              min={0}
              max={1}
              step={0.05}
              onChange={setErrorRate}
              width={80}
            />
          </Row>
        )}
        {type === FailureType.NETWORK_FAILURE && (
          <>
            <Row label="packet loss">
              <NumInput
                value={packetLossRate}
                min={0}
                max={1}
                step={0.05}
                onChange={setPacketLossRate}
                width={80}
              />
            </Row>
            <Row label="added ms">
              <NumInput
                value={addedLatencyMs}
                min={0}
                onChange={setAddedLatencyMs}
                width={80}
              />
            </Row>
          </>
        )}
        {type === FailureType.CRASH && (
          <label className="check">
            <input
              type="checkbox"
              checked={passThrough}
              onChange={(e) => setPassThrough(e.target.checked)}
            />
            <span>pass through (fallback to downstream)</span>
          </label>
        )}
        <Button
          onClick={() =>
            onAdd(
              create(FailureSchema, {
                target,
                type,
                startMs,
                durationMs,
                config: create(FailureConfigSchema, {
                  addedLatencyMillis: addedLatencyMs,
                  errorRate,
                  packetLossRate,
                  passThrough,
                }),
              }),
            )
          }
        >
          schedule
        </Button>
      </div>
    </>
  );

  if (compact) return body;
  return (
    <>
      <Divider />
      <Section label={`failure injections — ${mine.length}`}>{body}</Section>
    </>
  );
}
