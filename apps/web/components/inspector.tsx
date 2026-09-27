"use client";

/**
 * Right panel — details for the current selection.
 *
 * Selection kinds:
 *   "catalog:<provider>"           → provider overview
 *   "catalog:<provider>:<service>" → catalog service model (assumptions)
 *   anything else                  → architecture component id
 *
 * Every number shown comes from the backend (catalog, run results,
 * capacity reports, cost estimates). This panel formats; it never
 * derives or invents values.
 */
import type { ReactNode } from "react";
import type {
  CatalogService,
  ComponentSpec,
  FinalResults,
  GetCapacityResponse,
  GetCostEstimateResponse,
} from "@loadline/api";
import { Badge, Divider, EmptyState, Section, toneForWord } from "./ui";
import { f0, f1, f2, kindName, normKind, pct, price } from "./format";

function KV({ k, v }: { k: string; v: ReactNode }) {
  return (
    <span className="kv-item">
      <span className="kv-key">{k}</span>
      <span className="kv-val">{v}</span>
    </span>
  );
}

export function Inspector({
  selected,
  catalog,
  components,
  results,
  capacity,
  cost,
}: {
  selected: string | null;
  catalog: CatalogService[] | null;
  components: ComponentSpec[];
  results: FinalResults | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
}) {
  if (!selected) {
    return (
      <EmptyState>
        nothing selected — click a canvas node or a catalog entry in the palette.
      </EmptyState>
    );
  }

  if (selected.startsWith("catalog:")) {
    const [, provider, service] = selected.split(":");
    if (!service) return <ProviderView provider={provider ?? ""} catalog={catalog ?? []} />;
    const svc = (catalog ?? []).find(
      (s) => s.provider === provider && s.service === service,
    );
    if (!svc) {
      return <EmptyState>catalog not loaded — is the simulation server reachable?</EmptyState>;
    }
    return <ServiceView svc={svc} />;
  }

  const comp = components.find((c) => c.id === selected);
  if (!comp) return <EmptyState>unknown component.</EmptyState>;
  return (
    <ComponentView
      comp={comp}
      catalog={catalog ?? []}
      results={results}
      capacity={capacity}
      cost={cost}
    />
  );
}

/* -------------------------------------------------------------------------- */

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

function ServiceView({ svc }: { svc: CatalogService }) {
  return (
    <div className="inspector-body">
      <div className="kv">
        <KV k="provider" v={svc.provider} />
        <KV k="service" v={<strong>{svc.service}</strong>} />
        <KV k="kind" v={normKind(svc.componentKind)} />
      </div>
      {svc.summary && <p className="prose prose-dim">{svc.summary}</p>}
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

function ComponentView({
  comp,
  catalog,
  results,
  capacity,
  cost,
}: {
  comp: ComponentSpec;
  catalog: CatalogService[];
  results: FinalResults | null;
  capacity: GetCapacityResponse | null;
  cost: GetCostEstimateResponse | null;
}) {
  const svc = catalog.find(
    (s) =>
      s.provider === comp.provider &&
      comp.service !== undefined &&
      s.service.toLowerCase() === comp.service.toLowerCase(),
  );
  const m = results?.metrics?.components.find((c) => c.id === comp.id);
  const cap = capacity?.reports.find((r) => r.componentId === comp.id);
  const cc = cost?.estimate?.components.find((c) => c.componentId === comp.id);

  // Configuration rows actually set on the component spec.
  const cfg: [string, ReactNode][] = [];
  if (comp.provider) cfg.push(["provider", comp.provider]);
  if (comp.service) cfg.push(["service", comp.service]);
  cfg.push(["kind", kindName(comp.kind)]);
  if (comp.concurrency > 0) cfg.push(["concurrency", f0(comp.concurrency)]);
  if (comp.queueLimit > 0) cfg.push(["queue limit", f0(comp.queueLimit)]);
  if (comp.capacityRps > 0) cfg.push(["capacity rps", f1(comp.capacityRps)]);
  if (comp.hitRatio > 0) cfg.push(["hit ratio", pct(comp.hitRatio)]);
  if (comp.config && comp.config.memoryMb > 0) cfg.push(["memory", `${f0(comp.config.memoryMb)} MB`]);
  if (comp.config && comp.config.storageGb > 0) cfg.push(["storage", `${f0(comp.config.storageGb)} GB`]);
  const hasUnits = (comp.config?.units ?? 0) > 0;

  return (
    <div className="inspector-body">
      <div className="kv">
        <KV k="component" v={<strong>{comp.id}</strong>} />
        {cfg.slice(0, 3).map(([k, v]) => (
          <KV key={k} k={k} v={v} />
        ))}
      </div>

      {cfg.length > 3 && (
        <>
          <Divider />
          <Section label="configuration">
            <div className="kv">
              {cfg.slice(3).map(([k, v]) => (
                <KV key={k} k={k} v={v} />
              ))}
              {hasUnits && <KV k="units" v={f0(comp.config!.units)} />}
            </div>
          </Section>
        </>
      )}

      {svc && (
        <>
          <Divider />
          <Section label={`catalog model — ${svc.provider}/${svc.service}`}>
            <div className="kv">
              <KV k="concurrency" v={f0(svc.concurrency)} />
              <KV k="queue limit" v={svc.queueLimit > 0 ? f0(svc.queueLimit) : "unbounded"} />
              <KV k="modeled ceiling" v={`${f0(svc.modeledRps)} rps`} />
              <KV k="default service" v={`${f2(svc.defaultServiceTimeMs)} ms`} />
              <KV k="scaling" v={svc.scalingKind} />
            </div>
          </Section>
        </>
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

      {!svc && !m && !cap && (
        <>
          <Divider />
          <EmptyState>no run data yet — run a simulation to see measured behavior here.</EmptyState>
        </>
      )}
    </div>
  );
}
