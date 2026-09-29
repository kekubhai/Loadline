"use client";

import { useEffect, useMemo, useRef, useState } from "react";

/** One actionable row. Commands are real page actions, not text suggestions. */
export interface Command {
  id: string;
  label: string;
  /** Short context, e.g. "view" or a keyboard hint. */
  group: string;
  /** Right-aligned keyboard hint, e.g. "⌘K". */
  hint?: string;
  run: () => void;
  disabled?: boolean;
}

/**
 * Command palette (⌘/Ctrl+K). Filtering is a plain case-insensitive
 * substring match: predictable beats clever, and every row maps to a real
 * action the same toolbar button would run.
 */
export function CommandPalette({
  open,
  onClose,
  commands,
}: {
  open: boolean;
  onClose: () => void;
  commands: Command[];
}) {
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // Reset the query whenever the palette is opened.
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setIndex(0);
    // Autofocus after paint so the input exists.
    const t = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(t);
  }, [open]);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands.filter((c) => !c.disabled);
    return commands.filter(
      (c) => !c.disabled && c.label.toLowerCase().includes(q),
    );
  }, [commands, query]);

  // Keep the selection inside the result set while filtering.
  useEffect(() => {
    setIndex((i) => (i >= matches.length ? 0 : i));
  }, [matches.length]);

  if (!open) return null;

  const runAt = (i: number) => {
    const c = matches[i];
    if (!c) return;
    onClose();
    c.run();
  };

  return (
    <div className="pal-overlay" onPointerDown={onClose}>
      <div
        className="pal-sheet"
        role="dialog"
        aria-label="command palette"
        onPointerDown={(e) => e.stopPropagation()}
      >
        <input
          ref={inputRef}
          className="pal-search"
          type="text"
          placeholder="type a command…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setIndex((i) => (i + 1) % Math.max(matches.length, 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setIndex((i) =>
                i - 1 < 0 ? Math.max(matches.length - 1, 0) : i - 1,
              );
            } else if (e.key === "Enter") {
              e.preventDefault();
              runAt(index);
            } else if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            }
          }}
        />
        <div className="pal-results">
          {matches.length === 0 && (
            <div className="pal-empty">no matching command</div>
          )}
          {matches.map((c, i) => (
            <button
              key={c.id}
              type="button"
              className={`pal-item${i === index ? " active" : ""}`}
              onMouseEnter={() => setIndex(i)}
              onClick={() => runAt(i)}
            >
              <span className="pal-group">{c.group}</span>
              <span className="pal-label">{c.label}</span>
              {c.hint && <span className="pal-hint">{c.hint}</span>}
            </button>
          ))}
        </div>
        <div className="pal-foot">
          ↑↓ move · ⏎ run · esc close · {matches.length} command
          {matches.length === 1 ? "" : "s"}
        </div>
      </div>
    </div>
  );
}
