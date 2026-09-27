"use client";

/**
 * Left palette — the provider catalog (fetched via ListCatalog) and the
 * current architecture's components. Selection only; no data derivation.
 * Selecting a catalog row reports "catalog:<provider>[:<service>]"; a
 * component row reports its architecture id.
 */
import type { CatalogService, ComponentSpec } from "@loadline/api";
import { kindName, normKind } from "./format";

export function Palette({
  catalog,
  catalogError,
  components,
  selected,
  onSelect,
}: {
  catalog: CatalogService[] | null;
  catalogError: string;
  components: ComponentSpec[];
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const byProvider = new Map<string, CatalogService[]>();
  for (const s of catalog ?? []) {
    if (!byProvider.has(s.provider)) byProvider.set(s.provider, []);
    byProvider.get(s.provider)!.push(s);
  }
  const providers = [...byProvider.keys()].sort();

  return (
    <nav className="palette">
      <div className="pal-head">Providers</div>
      {catalogError ? (
        <div className="pal-note">{catalogError}</div>
      ) : !catalog ? (
        <div className="pal-note">loading catalog…</div>
      ) : (
        providers.map((p) => (
          <div
            key={p}
            className={`pal-row${selected === `catalog:${p}` ? " selected" : ""}`}
            onClick={() => onSelect(`catalog:${p}`)}
          >
            <span className="pal-name">{p}</span>
            <span className="pal-right">{byProvider.get(p)!.length}</span>
          </div>
        ))
      )}

      <div className="pal-head">Services</div>
      {providers.map((p) => (
        <div key={p}>
          <div className="pal-sub">{p}</div>
          {byProvider.get(p)!.map((s) => {
            const id = `catalog:${s.provider}:${s.service}`;
            return (
              <div
                key={id}
                className={`pal-row${selected === id ? " selected" : ""}`}
                onClick={() => onSelect(id)}
              >
                <span className="pal-name">{s.service}</span>
                <span className="pal-right">{normKind(s.componentKind)}</span>
              </div>
            );
          })}
        </div>
      ))}

      <div className="pal-head">Components</div>
      {components.map((c) => (
        <div
          key={c.id}
          className={`pal-row${selected === c.id ? " selected" : ""}`}
          onClick={() => onSelect(c.id)}
        >
          <span className="pal-name">{c.id}</span>
          <span className="pal-right">
            {c.provider ? `${c.provider}/${c.service}` : kindName(c.kind)}
          </span>
        </div>
      ))}
    </nav>
  );
}
