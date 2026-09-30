"use client";

/**
 * Explore gallery — a categorized picker for real-world system
 * architectures (social, AI, infrastructure, fintech, marketplaces,
 * media, developer). Presentational only: it renders
 * app/explore.ts data and reports the chosen system id upward. Loading
 * a system replaces the canvas architecture through the same editor
 * path templates use, keeping one workload for fair comparisons.
 */
import { useMemo, useState } from "react";
import { EXPLORE_CATEGORIES } from "../app/explore";
import type { ExploreSystem } from "../app/explore";

export function ExploreGallery({
  activeSystemId,
  onSelect,
}: {
  /** Id of the architecture currently loaded, if it came from the gallery. */
  activeSystemId: string | null;
  onSelect: (system: ExploreSystem) => void;
}) {
  const [activeCategory, setActiveCategory] = useState(
    EXPLORE_CATEGORIES[0]?.id ?? "",
  );

  const shown = useMemo(
    () =>
      EXPLORE_CATEGORIES.find((c) => c.id === activeCategory) ??
      EXPLORE_CATEGORIES[0],
    [activeCategory],
  );

  return (
    <div className="explore">
      <div className="explore-tabs" role="tablist" aria-label="system categories">
        {EXPLORE_CATEGORIES.map((c) => (
          <button
            key={c.id}
            type="button"
            role="tab"
            aria-selected={shown?.id === c.id}
            className={`explore-tab${shown?.id === c.id ? " active" : ""}`}
            onClick={() => setActiveCategory(c.id)}
          >
            {c.label}
            <span className="explore-tab-count">{c.systems.length}</span>
          </button>
        ))}
      </div>

      <div className="explore-grid">
        {(shown?.systems ?? []).map((s) => (
          <button
            key={s.id}
            type="button"
            className={`explore-card${activeSystemId === s.id ? " active" : ""}`}
            onClick={() => onSelect(s)}
          >
            <span className="explore-card-name">{s.name}</span>
            <span className="explore-card-desc">{s.description}</span>
            <span className="explore-card-meta">
              {s.architecture.components.length} components ·{" "}
              {s.architecture.links.length} links
            </span>
          </button>
        ))}
      </div>

      <p className="explore-note">
        Loading a system replaces the canvas architecture but keeps your
        workload, seed, and options — so comparisons stay on one workload.
        Every number after the run comes from the simulator.
      </p>
    </div>
  );
}
