/**
 * LOADLINE UI primitives.
 *
 * One restrained visual language: warm paper, near-black ink, thin rules.
 * Every component here is presentational — no data fetching, no derived
 * numbers, no application logic. Panels own structure; pages own content.
 */
import type { ReactNode } from "react";

/* --------------------------------------------------------------------------
   Panel — the primary container. Thin border, quiet surface, dense header.
   -------------------------------------------------------------------------- */

export function Panel({
  title,
  tag,
  actions,
  children,
}: {
  title?: string;
  /** Small right-aligned annotation in the header (e.g. "ESTIMATE"). */
  tag?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      {(title || tag || actions) && (
        <header className="panel-header">
          {title && <h2 className="panel-title">{title}</h2>}
          <div className="panel-actions">
            {tag && <span className="panel-tag">{tag}</span>}
            {actions}
          </div>
        </header>
      )}
      <div className="panel-body">{children}</div>
    </section>
  );
}

/* --------------------------------------------------------------------------
   Section — labeled content block inside a panel.
   -------------------------------------------------------------------------- */

export function Section({
  label,
  children,
}: {
  label: string;
  children?: ReactNode;
}) {
  return (
    <div className="section">
      <div className="section-label">{label}</div>
      {children}
    </div>
  );
}

/* --------------------------------------------------------------------------
   Button — square-ish, ink border, uppercase label.
   -------------------------------------------------------------------------- */

export function Button({
  children,
  variant = "default",
  disabled,
  type = "button",
  onClick,
}: {
  children: ReactNode;
  variant?: "default" | "primary" | "danger";
  disabled?: boolean;
  type?: "button" | "submit";
  onClick?: () => void;
}) {
  return (
    <button
      type={type}
      disabled={disabled}
      onClick={onClick}
      className={`btn btn-${variant}`}
    >
      {children}
    </button>
  );
}

/* --------------------------------------------------------------------------
   Input / Select / Field — dense form controls with fixed-width labels.
   -------------------------------------------------------------------------- */

export function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
    </label>
  );
}

export function Input({
  value,
  onChange,
  width = 220,
  spellCheck = false,
}: {
  value: string;
  onChange: (v: string) => void;
  width?: number;
  spellCheck?: boolean;
}) {
  return (
    <input
      className="input"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      style={{ width }}
      spellCheck={spellCheck}
    />
  );
}

export function Checkbox({
  checked,
  onChange,
  children,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  children: ReactNode;
}) {
  return (
    <label className="check">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span>{children}</span>
    </label>
  );
}

export function Select({
  value,
  onChange,
  options,
  width = 160,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  width?: number;
}) {
  return (
    <select
      className="select"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      style={{ width }}
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

/* --------------------------------------------------------------------------
   Badge — small uppercase status chip. Muted tints, never neon.
   -------------------------------------------------------------------------- */

export type Tone = "neutral" | "ok" | "warn" | "bad";

export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: Tone;
}) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

/** Severity / trend strings from the simulator mapped to display tones. */
export function toneForWord(word: string): Tone {
  switch (word) {
    case "critical":
    case "growing":
      return "bad";
    case "high":
    case "warning":
      return "warn";
    case "moderate":
    case "draining":
      return "neutral";
    case "stable":
    case "ok":
      return "ok";
    default:
      return "neutral";
  }
}

/* --------------------------------------------------------------------------
   Tabs — underline style, no pills, no animation.
   -------------------------------------------------------------------------- */

export function Tabs({
  tabs,
  active,
  onChange,
}: {
  tabs: string[];
  active: string;
  onChange: (id: string) => void;
}) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button
          key={t}
          role="tab"
          aria-selected={t === active}
          className={`tab${t === active ? " tab-active" : ""}`}
          onClick={() => onChange(t)}
        >
          {t}
        </button>
      ))}
    </div>
  );
}

/* --------------------------------------------------------------------------
   Tooltip — CSS-only, small dotted underline, no JS.
   -------------------------------------------------------------------------- */

export function Tooltip({ text, children }: { text: string; children: ReactNode }) {
  return (
    <span className="tooltip" data-tip={text}>
      {children}
    </span>
  );
}

/* --------------------------------------------------------------------------
   Metric — one labeled value with optional unit and note.
   -------------------------------------------------------------------------- */

export function Metric({
  label,
  value,
  unit,
  note,
}: {
  label: ReactNode;
  value: ReactNode;
  unit?: string;
  note?: string;
}) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">
        {value}
        {unit && <span className="metric-unit">{unit}</span>}
      </div>
      {note && <div className="metric-note">{note}</div>}
    </div>
  );
}

/* --------------------------------------------------------------------------
   StatusIndicator — labeled run-state dot.
   -------------------------------------------------------------------------- */

export function StatusIndicator({
  state,
  label,
}: {
  state: "ok" | "warn" | "bad" | "idle";
  label: string;
}) {
  return (
    <span className="status">
      <span className={`status-dot status-${state}`} aria-hidden />
      {label}
    </span>
  );
}

/* --------------------------------------------------------------------------
   Toolbar — horizontal control strip.
   -------------------------------------------------------------------------- */

export function Toolbar({ children }: { children: ReactNode }) {
  return <div className="toolbar">{children}</div>;
}

/* --------------------------------------------------------------------------
   Divider — labeled horizontal rule.
   -------------------------------------------------------------------------- */

export function Divider({ label }: { label?: string }) {
  if (!label) return <hr className="divider" />;
  return (
    <div className="divider-labeled">
      <span>{label}</span>
    </div>
  );
}

/* --------------------------------------------------------------------------
   EmptyState — quiet placeholder, one line, no illustrations.
   -------------------------------------------------------------------------- */

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}
