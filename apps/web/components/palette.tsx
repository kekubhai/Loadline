"use client";

/**
 * Left palette — provider tabs, the service catalog (fetched via
 * ListCatalog), and the architecture's components. Catalog rows are
 * draggable onto the canvas (HTML5 drag with the "loadline/service"
 * payload type); the canvas converts drops into nodes. Selecting a row
 * reports a "catalog:<provider>[:<service>]" token or a component id.
 * No data derivation here.
 */
import { useMemo, useState } from "react";
import type { CatalogService, ComponentSpec } from "@loadline/api";
import { kindName } from "./format";

export const SERVICE_MIME = "loadline/service";

export function Palette({
  catalog,
  catalogError,
  components,
  selected,
  onSelect,
  onAddService,
  onDeleteComponent,
}: {
  catalog: CatalogService[] | null;
  catalogError: string;
  components: ComponentSpec[];
  selected: string | null;
  onSelect: (id: string) => void;
  /** Add a catalog service to the canvas (click-to-add path). */
  onAddService: (provider: string, service: string) => void;
  onDeleteComponent: (id: string) => void;
}) {
  const byProvider = useMemo(() => {
    const m = new Map<string, CatalogService[]>();
    for (const s of catalog ?? []) {
      if (!m.has(s.provider)) m.set(s.provider, []);
      m.get(s.provider)!.push(s);
    }
    return m;
  }, [catalog]);

  const providers = useMemo(
    () => [...byProvider.keys()].sort(),
    [byProvider],
  );

  // Selected provider tab; falls back to the first provider once the
  // catalog arrives. Selecting a provider tab shows its services only —
  // the user asked for provider-then-service navigation.
  const [activeProvider, setActiveProvider] = useState<string | null>(null);
  const shown =
    activeProvider && byProvider.has(activeProvider)
      ? activeProvider
      : (providers[0] ?? null);

  return (
    <nav className="palette">
      <div className="pal-head">Providers</div>
      {catalogError ? (
        <div className="pal-note">{catalogError}</div>
      ) : !catalog ? (
        <div className="pal-note">loading catalog…</div>
      ) : (
        <div className="pal-tabs">
          {providers.map((p) => (
            <button
              key={p}
              type="button"
              className={`pal-tab${shown === p ? " active" : ""}`}
              onClick={() => setActiveProvider(p)}
            >
              {p}
            </button>
          ))}
        </div>
      )}

      <div className="pal-head">Services</div>
      {catalogError ? (
        <div className="pal-note pal-note-bad">
          catalog unavailable — the simulation server at the configured URL
          did not answer
        </div>
      ) : null}
      {catalog && shown && (
        <div className="pal-services">
          {(byProvider.get(shown) ?? []).map((s) => {
            const id = `catalog:${s.provider}:${s.service}`;
            const add = () => onAddService(s.provider, s.service);
            return (
              <div
                key={id}
                className={`pal-row pal-draggable${
                  selected === id ? " selected" : ""
                }`}
                title={`${s.provider}/${s.service} — drag onto the canvas, or click + to add`}
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData(
                    SERVICE_MIME,
                    `${s.provider}:${s.service}`,
                  );
                  e.dataTransfer.effectAllowed = "copy";
                }}
                onClick={() => onSelect(id)}
                onDoubleClick={add}
              >
                <span className="pal-name">{s.service}</span>
                <span className="pal-right">{kindLabel(s.componentKind)}</span>
                <button
                  type="button"
                  className="pal-add"
                  title={`add ${s.service} to the canvas`}
                  aria-label={`add ${s.service}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    add();
                  }}
                >
                  +
                </button>
              </div>
            );
          })}
          <div className="pal-note">drag a service onto the canvas, or press +</div>
        </div>
      )}

      <div className="pal-head">Components</div>
      {components.length === 0 && (
        <div className="pal-note">empty architecture — drop a service</div>
      )}
      {components.map((c) => (
        <div
          key={c.id}
          className={`pal-row${selected === c.id ? " selected" : ""}`}
          onClick={() => onSelect(c.id)}
        >
          <span className="pal-name">{c.id}</span>
          <span className="pal-right">
            {c.provider
              ? `${c.provider}/${c.service}`
              : kindName(c.kind)}
          </span>
          {c.kind !== 1 && ( // 1 = COMPONENT_KIND_CLIENT: the simulator requires exactly one client.
            <button
              type="button"
              className="pal-del"
              title={`remove ${c.id}`}
              onClick={(e) => {
                e.stopPropagation();
                onDeleteComponent(c.id);
              }}
            >
              ×
            </button>
          )}
        </div>
      ))}
    </nav>
  );
}

/** proto enum name "COMPONENT_KIND_X" → short display label. */
function kindLabel(k?: string | null): string {
  return (k ?? "")
    .replace(/^COMPONENT_KIND_/, "")
    .replace(/_/g, " ")
    .trim()
    .toLowerCase();
}
