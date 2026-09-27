"use client";

/**
 * LOADLINE — failure injection panel (simulation view).
 *
 * Operational counterpart to the inspector's per-component editor: pick
 * any component from one dropdown, pick the failure type, schedule it.
 * Writes through the page's canonical dispatch — it holds no state of
 * its own beyond which target is selected. All four V1 injection types
 * are exposed (crash / increased latency / increased error rate /
 * network failure); they become part of the next run's options.
 */
import { useState } from "react";
import type { Failure, FailureType } from "@loadline/api";
import { FailureEditor, FAILURE_LABELS } from "./inspector-forms";
import { Divider, EmptyState, Section, Select } from "./ui";

export function FailurePanel({
  components,
  failures,
  onAdd,
  onRemove,
}: {
  /** Editable (non-client) components available as targets. */
  components: { id: string; label: string }[];
  failures: Failure[];
  onAdd: (f: Failure) => void;
  onRemove: (index: number) => void;
}) {
  const [target, setTarget] = useState<string>(components[0]?.id ?? "");
  const effective = components.some((c) => c.id === target)
    ? target
    : (components[0]?.id ?? "");

  return (
    <div className="fail-panel">
      <Section label="target component">
        {components.length === 0 ? (
          <EmptyState>no injectable components — add services in the architecture view.</EmptyState>
        ) : (
          <Select
            value={effective}
            onChange={setTarget}
            width={200}
            options={components.map((c) => ({ value: c.id, label: c.label }))}
          />
        )}
      </Section>

      {effective && (
        <>
          <Divider />
          <div className="fail-summary">
            {failures.length === 0 ? (
              <EmptyState>
                no injections scheduled — the next run executes the healthy
                architecture.
              </EmptyState>
            ) : (
              <Section label={`scheduled — ${failures.length}`}>
                {failures.map((f, i) => (
                  <div key={i} className="ins-failure">
                    <span className="ins-failure-line">
                      <strong>{f.target}</strong> · {FAILURE_LABELS[f.type] ?? f.type} ·
                      t={f.startMs}ms → {f.startMs + f.durationMs}ms
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
              </Section>
            )}
          </div>
          <FailureEditor
            target={effective}
            failures={failures}
            onAdd={onAdd}
            onRemove={onRemove}
            compact
          />
        </>
      )}
    </div>
  );
}

export type { FailureType };
