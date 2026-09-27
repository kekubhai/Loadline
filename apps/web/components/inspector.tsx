"use client";

/**
 * Right panel — details and configuration for the current selection.
 *
 * Selection kinds:
 *   null                           → architecture editor (workload, options, failures)
 *   "catalog:<provider>"           → provider overview
 *   "catalog:<provider>:<service>" → catalog service model (+ add to canvas)
 *   "link:<index>"                 → link editor (condition, delete)
 *   anything else                  → architecture component editor
 *
 * Every number shown comes from the backend (catalog, run results,
 * capacity reports, cost estimates). Editing writes into the canonical
 * editor state owned by the page; this panel never derives simulation
 * values and only exposes fields the backend model consumes.
 */
import { useState } from "react";
import type { ReactNode } from "react";
import {
  ComponentKind,
  FailureType,
} from "@loadline/api";
import type {
  CatalogService,
  ComponentSpec,
  Failure,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
  Link,
  ProviderConfig,
  SimulationOptions,
  WorkloadSpec,
} from "@loadline/api";
import { Badge, Button, Divider, EmptyState, Section, Select, toneForWord } from "./ui";
import { f0, f1, f2, kindName, pct, price } from "./format";

/** Partial update of one component; config merges shallowly in the page. */
export interface ComponentPatch {
  id?: string;
  provider?: string;
  service?: string;
  concurrency?: number;
  queueLimit?: number;
  capacityRps?: number;
  defaultServiceTimeMillis?: number;
  hitRatio?: number;
  fanOut?: number;
  config?: Partial<ProviderConfig> | null;
}

function KV({ k, v }: { k: string; v: ReactNode }) {
  return (
    <span className="kv-item">
      <span className="kv-key">{k}</span>
      <span className="kv-val">{v}</span>
    </span>
  );
}

/* --------------------------------------------------------------- inputs -- */

/**
 * Numeric input with local text state: commits valid numbers as they are
 * typed, restores the canonical value on blur. Avoids fighting the user
 * over intermediate states like "" or "0.".
 */
function NumInput({
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
function IntInput({
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
function BigIntInput({
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

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="ins-row">
      <span className="ins-row-label">{label}</span>
      {children}
    </label>
  );
}

const FAILURE_LABELS: Record<number, string> = {
  [FailureType.CRASH]: "crash",
  [FailureType.INCREASED_LATENCY]: "increased latency",
  [FailureType.INCREASED_ERROR_RATE]: "increased error rate",
  [FailureType.NETWORK_FAILURE]: "network failure",
};

/* ---------------------------------------------------------------- shell -- */

export function Inspector({
  selected,
  catalog,
  components,
  links,
  failures,
  workload,
  options,
  architectureName,
  warnings,
  results,
  capacity,
  cost,
  onPatchComponent,
  onRenameComponent,
  onDeleteComponent,
  onPatchLink,
  onDeleteLink,
  onAddFailure,
  onRemoveFailure,
  onPatchWorkload,
  onPatchOptions,
  onRenameArchitecture,
  onAddService,
  onFocusComponent,
}: {
  selected: string | null;
  catalog: CatalogService[] | null;
  components: ComponentSpec[];
  links: Link[];
  failures: Failure[];
  workload: WorkloadSpec;
  options: SimulationOptions;
  architectureName: string;
  warnings: string[];
  results: FinalResults | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
  onPatchComponent: (id: string, patch: ComponentPatch) => void;
  onRenameComponent: (from: string, to: string) => void;
  onDeleteComponent: (id: string) => void;
  onPatchLink: (index: number, condition: string) => void;
  onDeleteLink: (index: number) => void;
  onAddFailure: (f: Failure) => void;
  onRemoveFailure: (index: number) => void;
  onPatchWorkload: (patch: Partial<WorkloadSpec>) => void;
  onPatchOptions: (patch: Partial<SimulationOptions>) => void;
  onRenameArchitecture: (name: string) => void;
  onAddService: (provider: string, service: string) => void;
  onFocusComponent: (id: string) => void;
}) {
  if (selected === null) {
    return (
      <ArchitectureView
        name={architectureName}
        components={components}
        failures={failures}
        workload={workload}
        options={options}
        warnings={warnings}
        onRename={onRenameArchitecture}
        onPatchWorkload={onPatchWorkload}
        onPatchOptions={onPatchOptions}
        onRemoveFailure={onRemoveFailure}
        onFocusComponent={onFocusComponent}
      />
    );
  }

  if (selected.startsWith("catalog:")) {
    const [, provider, service] = selected.split(":");
    if (!service) return <ProviderView provider={provider ?? ""} catalog={catalog ?? []} />;
    const svc = (catalog ?? []).find(
      (s) => s.provider === provider && s.service === service,
    );
    if (!svc) {
      return (
        <EmptyState>catalog not loaded — is the simulation server reachable?</EmptyState>
      );
    }
    return <ServiceView svc={svc} onAdd={() => onAddService(svc.provider, svc.service)} />;
  }

  if (selected.startsWith("link:")) {
    const idx = Number(selected.slice(5));
    const link = links[idx];
    if (!link) return <EmptyState>link no longer exists.</EmptyState>;
    return (
      <LinkView
        index={idx}
        link={link}
        onPatch={onPatchLink}
        onDelete={() => onDeleteLink(idx)}
      />
    );
  }

  const comp = components.find((c) => c.id === selected);
  if (!comp) return <EmptyState>unknown component.</EmptyState>;
  return (
    <ComponentView
      comp={comp}
      catalog={catalog ?? []}
      components={components}
      links={links}
      failures={failures}
      results={results}
      capacity={capacity}
      cost={cost}
      onPatch={onPatchComponent}
      onRename={onRenameComponent}
      onDelete={onDeleteComponent}
      onAddFailure={onAddFailure}
      onRemoveFailure={onRemoveFailure}
      onFocusComponent={onFocusComponent}
    />
  );
}

/* --------------------------------------------------- architecture editor -- */

function ArchitectureView({
  name,
  components,
  failures,
  workload,
  options,
  warnings,
  onRename,
  onPatchWorkload,
  onPatchOptions,
  onRemoveFailure,
  onFocusComponent,
}: {
  name: string;
  components: ComponentSpec[];
  failures: Failure[];
  workload: WorkloadSpec;
  options: SimulationOptions;
  warnings: string[];
  onRename: (name: string) => void;
  onPatchWorkload: (patch: Partial<WorkloadSpec>) => void;
  onPatchOptions: (patch: Partial<SimulationOptions>) => void;
  onRemoveFailure: (index: number) => void;
  onFocusComponent: (id: string) => void;
}) {
  return (
    <div className="inspector-body">
      <Row label="name">
        <input
          className="input"
          style={{ width: 160 }}
          value={name}
          onChange={(e) => onRename(e.target.value)}
        />
      </Row>

      {warnings.length > 0 && (
        <>
          <Divider />
          <Section label="validation">
            <ul className="reasons ins-warn-list">
              {warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          </Section>
        </>
      )}

      <Divider />
      <Section label="workload — derivation inputs">
        <Row label="total users">
          <BigIntInput
            value={workload.totalUsers}
            onChange={(v) => onPatchWorkload({ totalUsers: v })}
          />
        </Row>
        <Row label="dau">
          <BigIntInput value={workload.dau} onChange={(v) => onPatchWorkload({ dau: v })} />
        </Row>
        <Row label="req / user / day">
          <NumInput
            value={workload.requestsPerUserPerDay}
            min={0}
            onChange={(v) => onPatchWorkload({ requestsPerUserPerDay: v })}
          />
        </Row>
        <Row label="peak multiplier">
          <NumInput
            value={workload.peakMultiplier}
            min={1}
            onChange={(v) => onPatchWorkload({ peakMultiplier: v })}
          />
        </Row>
        <Row label="reads per write">
          <NumInput
            value={workload.readWriteRatio}
            min={0}
            onChange={(v) => onPatchWorkload({ readWriteRatio: v })}
          />
        </Row>
        <Row label="payload bytes">
          <BigIntInput
            value={workload.payloadBytes}
            onChange={(v) => onPatchWorkload({ payloadBytes: v })}
          />
        </Row>
        <p className="prose prose-dim">
          users → dau → requests/day → average rps → peak rps; derived plan is
          reported back by the simulator.
        </p>
      </Section>

      <Section label="run options">
        <Row label="seed">
          <BigIntInput
            value={options.seed}
            onChange={(v) => onPatchOptions({ seed: v })}
          />
        </Row>
        <Row label="duration ms">
          <NumInput
            value={options.durationMs}
            min={1}
            onChange={(v) => onPatchOptions({ durationMs: v })}
          />
        </Row>
        <Row label="max retries">
          <IntInput
            value={options.maxRetries}
            min={0}
            onChange={(v) => onPatchOptions({ maxRetries: v })}
          />
        </Row>
        <Row label="backoff base ms">
          <NumInput
            value={options.backoffBaseMs}
            min={0}
            onChange={(v) => onPatchOptions({ backoffBaseMs: v })}
          />
        </Row>
        <Row label="timeout ms">
          <NumInput
            value={options.timeoutMs}
            min={0}
            onChange={(v) => onPatchOptions({ timeoutMs: v })}
          />
        </Row>
        {components.length > 1 && (
          <>
            <div className="ins-row-label" style={{ marginTop: 6 }}>
              retry on
            </div>
            {components
              .filter((c) => c.kind !== ComponentKind.CLIENT)
              .map((c) => (
                <label key={c.id} className="check">
                  <input
                    type="checkbox"
                    checked={options.retryOn.includes(c.id)}
                    onChange={(e) => {
                      const next = e.target.checked
                        ? [...options.retryOn, c.id]
                        : options.retryOn.filter((id) => id !== c.id);
                      onPatchOptions({ retryOn: next });
                    }}
                  />
                  <span>{c.id}</span>
                </label>
              ))}
          </>
        )}
      </Section>

      <Section label={`failures — ${failures.length} pending`}>
        {failures.length === 0 ? (
          <EmptyState>
            none — select a component to schedule an injection for it.
          </EmptyState>
        ) : (
          failures.map((f, i) => (
            <div key={i} className="ins-failure">
              <span className="ins-failure-line">
                <strong>{f.target}</strong> · {FAILURE_LABELS[f.type] ?? f.type} ·
                t={f0(f.startMs)}ms → {f0(f.startMs + f.durationMs)}ms
              </span>
              <button
                type="button"
                className="pal-del"
                title="remove failure"
                onClick={() => onRemoveFailure(i)}
              >
                ×
              </button>
            </div>
          ))
        )}
      </Section>
    </div>
  );
}

/* ------------------------------------------------------ catalog views ----- */

function ProviderView({ provider, catalog }: { provider: string; catalog: CatalogService[] }) {
  const services = catalog.filter((s) => s.provider === provider);
  if (services.length === 0) {
    return <EmptyState>catalog not loaded — is the simulation server reachable?</EmptyState>;
  }
  return (
    <div className="inspector-body">
      <div className="kv">
        <KV k="provider" v={<strong>{provider}</strong>} />
        <KV k="services" v={f0(services.length)} />
      </div>
      <Divider />
      <Section label="catalog">
        <div className="kv">
          {services.map((s) => (
            <KV key={s.service} k={s.service} v={`${f0(s.modeledRps)} rps modeled`} />
          ))}
        </div>
      </Section>
    </div>
  );
}

function ServiceView({ svc, onAdd }: { svc: CatalogService; onAdd: () => void }) {
  return (
    <div className="inspector-body">
      <div className="kv">
        <KV k="provider" v={svc.provider} />
        <KV k="service" v={<strong>{svc.service}</strong>} />
        <KV k="kind" v={kindName(kindFromProto(svc.componentKind))} />
      </div>
      {svc.summary && <p className="prose prose-dim">{svc.summary}</p>}
      <Button onClick={onAdd}>add to canvas</Button>
      <Divider />
      <Section label="capacity model">
        <div className="kv">
          <KV k="concurrency" v={f0(svc.concurrency)} />
          <KV k="queue limit" v={svc.queueLimit > 0 ? f0(svc.queueLimit) : "unbounded"} />
          <KV k="modeled ceiling" v={`${f0(svc.modeledRps)} rps`} />
          <KV k="default service" v={`${f2(svc.defaultServiceTimeMs)} ms`} />
        </div>
      </Section>
      <Section label="scaling">
        <div className="kv">
          <KV k="mode" v={svc.scalingKind} />
        </div>
      </Section>
      <Section label="pricing">
        <div className="kv">
          {svc.perRequestPrice > 0 && <KV k="per request" v={price(svc.perRequestPrice)} />}
          {svc.perInstanceHourPrice > 0 && (
            <KV k="instance-hour" v={price(svc.perInstanceHourPrice)} />
          )}
          {svc.perRequestPrice === 0 && svc.perInstanceHourPrice === 0 && (
            <KV k="direct charges" v="none modeled" />
          )}
        </div>
      </Section>
    </div>
  );
}

/** proto enum name → numeric ComponentKind for display via kindName. */
function kindFromProto(name: string): number {
  const table: Record<string, number> = {
    COMPONENT_KIND_CLIENT: ComponentKind.CLIENT,
    COMPONENT_KIND_LOAD_BALANCER: ComponentKind.LOAD_BALANCER,
    COMPONENT_KIND_API_SERVER: ComponentKind.API_SERVER,
    COMPONENT_KIND_CACHE: ComponentKind.CACHE,
    COMPONENT_KIND_QUEUE: ComponentKind.QUEUE,
    COMPONENT_KIND_WORKER: ComponentKind.WORKER,
    COMPONENT_KIND_DATABASE: ComponentKind.DATABASE,
    COMPONENT_KIND_OBJECT_STORAGE: ComponentKind.OBJECT_STORAGE,
    COMPONENT_KIND_NETWORK: ComponentKind.NETWORK,
  };
  return table[name] ?? ComponentKind.CLIENT;
}

/* ---------------------------------------------------------- link editor --- */

function LinkView({
  index,
  link,
  onPatch,
  onDelete,
}: {
  index: number;
  link: Link;
  onPatch: (index: number, condition: string) => void;
  onDelete: () => void;
}) {
  return (
    <div className="inspector-body">
      <div className="kv">
        <KV k="link" v={<strong>#{index}</strong>} />
        <KV k="from" v={link.from} />
        <KV k="to" v={link.to} />
      </div>
      <Divider />
      <Section label="condition">
        <Select
          value={link.condition}
          onChange={(v) => onPatch(index, v)}
          width={140}
          options={[
            { value: "", label: "all requests" },
            { value: "read", label: "reads only" },
            { value: "write", label: "writes only" },
          ]}
        />
        <p className="prose prose-dim">
          restricts which requests traverse this link in the simulator.
        </p>
      </Section>
      <Button variant="danger" onClick={onDelete}>
        delete link
      </Button>
    </div>
  );
}

/* ------------------------------------------------------ component editor -- */

const CLIENT_KIND = ComponentKind.CLIENT;

function ComponentView({
  comp,
  catalog,
  components,
  links,
  failures,
  results,
  capacity,
  cost,
  onPatch,
  onRename,
  onDelete,
  onAddFailure,
  onRemoveFailure,
  onFocusComponent,
}: {
  comp: ComponentSpec;
  catalog: CatalogService[];
  components: ComponentSpec[];
  links: Link[];
  failures: Failure[];
  results: FinalResults | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
  onPatch: (id: string, patch: ComponentPatch) => void;
  onRename: (from: string, to: string) => void;
  onDelete: (id: string) => void;
  onAddFailure: (f: Failure) => void;
  onRemoveFailure: (index: number) => void;
  onFocusComponent: (id: string) => void;
}) {
  const svc = catalog.find(
    (s) =>
      s.provider === comp.provider &&
      comp.service !== undefined &&
      comp.service !== "" &&
      s.service.toLowerCase() === comp.service.toLowerCase(),
  );
  const m = results?.metrics?.components.find((c) => c.id === comp.id);
  const cap = capacity?.reports.find((r) => r.componentId === comp.id);
  const cc = cost?.estimate?.components.find((c) => c.componentId === comp.id);
  const isClient = comp.kind === CLIENT_KIND;
  const patch = (p: ComponentPatch) => onPatch(comp.id, p);
  const cfg = comp.config;

  const [rename, setRename] = useState<string | null>(null);

  return (
    <div className="inspector-body">
      <Row label="id">
        <input
          className="input"
          style={{ width: 130 }}
          value={rename ?? comp.id}
          onChange={(e) => setRename(e.target.value)}
          onBlur={() => {
            const next = (rename ?? "").trim();
            if (next && next !== comp.id) onRename(comp.id, next);
            setRename(null);
          }}
        />
        {!isClient && (
          <button
            type="button"
            className="pal-del"
            title={`remove ${comp.id}`}
            onClick={() => onDelete(comp.id)}
          >
            ×
          </button>
        )}
      </Row>
      <div className="kv">
        <KV k="kind" v={kindName(comp.kind)} />
        {comp.provider && <KV k="provider" v={comp.provider} />}
        {comp.service && <KV k="service" v={comp.service} />}
      </div>

      {svc ? (
        <>
          <Divider />
          <Section label={`catalog model — ${svc.provider}/${svc.service}`}>
            <p className="prose prose-dim">
              unspecified fields fall back to catalog defaults; overrides below
              are sent explicitly.
            </p>
            <div className="kv">
              <KV k="catalog concurrency" v={f0(svc.concurrency)} />
              <KV
                k="catalog queue limit"
                v={svc.queueLimit > 0 ? f0(svc.queueLimit) : "unbounded"}
              />
              <KV k="modeled ceiling" v={`${f0(svc.modeledRps)} rps`} />
              <KV k="default service" v={`${f2(svc.defaultServiceTimeMs)} ms`} />
            </div>
          </Section>
          <Section label="provider overrides">
            <Row label="concurrency">
              <IntInput
                value={cfg?.concurrency ?? 0}
                min={0}
                onChange={(v) => patch({ config: { concurrency: v } })}
              />
            </Row>
            <Row label="queue limit">
              <IntInput
                value={cfg?.queueLimit ?? 0}
                min={0}
                onChange={(v) => patch({ config: { queueLimit: v } })}
              />
            </Row>
            <Row label="units">
              <IntInput
                value={cfg?.units ?? 0}
                min={0}
                onChange={(v) => patch({ config: { units: v } })}
              />
            </Row>
            <Row label="memory mb">
              <IntInput
                value={cfg?.memoryMb ?? 0}
                min={0}
                onChange={(v) => patch({ config: { memoryMb: v } })}
              />
            </Row>
            <Row label="storage gb">
              <NumInput
                value={cfg?.storageGb ?? 0}
                min={0}
                onChange={(v) => patch({ config: { storageGb: v } })}
              />
            </Row>
            <Row label="hit ratio 0–1">
              <NumInput
                value={cfg?.hitRatio ?? 0}
                min={0}
                max={1}
                step={0.05}
                onChange={(v) => patch({ config: { hitRatio: v } })}
              />
            </Row>
            <p className="prose prose-dim">0 = use the catalog default.</p>
            <Button
              onClick={() =>
                patch({ provider: "", service: "", config: null })
              }
            >
              detach catalog model
            </Button>
          </Section>
        </>
      ) : (
        <>
          <Divider />
          <Section label="generic model">
            <Row label="concurrency">
              <IntInput
                value={comp.concurrency}
                min={0}
                onChange={(v) => patch({ concurrency: v })}
              />
            </Row>
            <Row label="queue limit">
              <IntInput
                value={comp.queueLimit}
                min={0}
                onChange={(v) => patch({ queueLimit: v })}
              />
            </Row>
            <Row label="capacity rps">
              <NumInput
                value={comp.capacityRps}
                min={0}
                onChange={(v) => patch({ capacityRps: v })}
              />
            </Row>
            <Row label="service ms">
              <NumInput
                value={comp.defaultServiceTimeMillis}
                min={0}
                onChange={(v) => patch({ defaultServiceTimeMillis: v })}
              />
            </Row>
            {comp.kind === ComponentKind.CACHE && (
              <Row label="hit ratio 0–1">
                <NumInput
                  value={comp.hitRatio}
                  min={0}
                  max={1}
                  step={0.05}
                  onChange={(v) => patch({ hitRatio: v })}
                />
              </Row>
            )}
            {(comp.kind === ComponentKind.LOAD_BALANCER ||
              comp.kind === ComponentKind.NETWORK) && (
              <Row label="fan-out">
                <IntInput
                  value={comp.fanOut || 1}
                  min={1}
                  onChange={(v) => patch({ fanOut: v })}
                />
              </Row>
            )}
          </Section>
          <Section label="catalog model">
            <EmptyState>
              generic component — attach a provider service from the palette to
              get capacity and cost estimates.
            </EmptyState>
          </Section>
        </>
      )}

      {!isClient && (
        <FailureSection
          target={comp.id}
          failures={failures}
          onAdd={onAddFailure}
          onRemove={onRemoveFailure}
        />
      )}

      {m && (
        <>
          <Divider />
          <Section label="measured — last run">
            <div className="kv">
              <KV k="arrived" v={f0(m.arrived)} />
              <KV k="completed" v={f0(m.completed)} />
              <KV k="rejected" v={f0(m.rejected)} />
              <KV k="failed" v={f0(m.failed)} />
              <KV k="utilization" v={pct(m.utilization)} />
              <KV k="queue max" v={f0(m.maxQueueDepth)} />
              <KV
                k="queue trend"
                v={<Badge tone={toneForWord(m.queueTrend)}>{m.queueTrend}</Badge>}
              />
              <KV k="arrival rate" v={`${f1(m.arrivalRps)} rps`} />
              <KV k="throughput" v={`${f1(m.throughputRps)} rps`} />
              <KV k="avg queue wait" v={`${f2(m.avgQueueWaitMs)} ms`} />
              <KV k="avg service" v={`${f2(m.avgServiceMs)} ms`} />
              {m.saturated && <KV k="flag" v={<Badge tone="bad">saturated</Badge>} />}
            </div>
          </Section>
        </>
      )}

      {cap && (
        <>
          <Divider />
          <Section label="capacity estimate">
            <div className="kv">
              <KV k="current" v={`${f1(cap.currentRps)} rps`} />
              <KV k="max sustainable" v={`${f0(cap.maxSustainableRps)} rps`} />
              <KV k="utilization" v={pct(cap.utilization)} />
              <KV k="headroom" v={pct(cap.headroom)} />
              {(cap.saturated || cap.bottleneck) && (
                <KV
                  k="flags"
                  v={
                    <>
                      {cap.saturated && <Badge tone="bad">saturated</Badge>}{" "}
                      {cap.bottleneck && <Badge tone="warn">bottleneck</Badge>}
                    </>
                  }
                />
              )}
            </div>
            {cap.assumptions.length > 0 && (
              <ul className="reasons">
                {cap.assumptions.map((a, i) => (
                  <li key={i}>{a}</li>
                ))}
              </ul>
            )}
          </Section>
        </>
      )}

      {cc && (
        <>
          <Divider />
          <Section label={`monthly cost — ${cc.provider}/${cc.service}`}>
            <div className="kv">
              <KV k="monthly" v={<strong>{price(cc.monthly)}</strong>} />
            </div>
            <table>
              <thead>
                <tr>
                  <th>line item</th>
                  <th>qty</th>
                  <th>cost</th>
                </tr>
              </thead>
              <tbody>
                {cc.lineItems.map((li, i) => (
                  <tr key={i}>
                    <td>{li.description}</td>
                    <td>
                      {f2(li.quantity)} {li.unit}
                    </td>
                    <td>{price(li.monthlyCost)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Section>
        </>
      )}

      <Divider />
      <Section label="links">
        {links.every((l) => l.from !== comp.id && l.to !== comp.id) ? (
          <EmptyState>not connected — drag from a node's port dot.</EmptyState>
        ) : (
          <>
            <div className="ins-row-label">upstream</div>
            {links.filter((l) => l.to === comp.id).map((l, i) => (
              <button
                key={`u${i}`}
                type="button"
                className="ins-link-row"
                onClick={() => onFocusComponent(l.from)}
              >
                ← {l.from}
                {l.condition ? ` (${l.condition})` : ""}
              </button>
            ))}
            {links.every((l) => l.to !== comp.id) && (
              <span className="prose prose-dim">none</span>
            )}
            <div className="ins-row-label">downstream</div>
            {links.filter((l) => l.from === comp.id).map((l, i) => (
              <button
                key={`d${i}`}
                type="button"
                className="ins-link-row"
                onClick={() => onFocusComponent(l.to)}
              >
                → {l.to}
                {l.condition ? ` (${l.condition})` : ""}
              </button>
            ))}
            {links.every((l) => l.from !== comp.id) && (
              <span className="prose prose-dim">none</span>
            )}
          </>
        )}
      </Section>
    </div>
  );
}

/* ------------------------------------------------------- failure editor --- */

function FailureSection({
  target,
  failures,
  onAdd,
  onRemove,
}: {
  target: string;
  failures: Failure[];
  onAdd: (f: Failure) => void;
  onRemove: (index: number) => void;
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

  return (
    <>
      <Divider />
      <Section label={`failure injections — ${mine.length}`}>
        {mine.length === 0 && <EmptyState>none scheduled for this component.</EmptyState>}
        {mine.map(({ f, i }) => (
          <div key={i} className="ins-failure">
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
              onAdd({
                target,
                type,
                startMs,
                durationMs,
                config: {
                  addedLatencyMillis: addedLatencyMs,
                  errorRate,
                  packetLossRate,
                  passThrough,
                },
              })
            }
          >
            schedule
          </Button>
        </div>
      </Section>
    </>
  );
}
