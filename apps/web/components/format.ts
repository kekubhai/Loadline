/**
 * Shared display formatters — presentation only. No simulation math
 * happens here; every value passed through these helpers was computed
 * by the backend (simulation kernel, provider models, cost engine).
 */
import { ComponentKind } from "@loadline/api";

/** Integer with thousands separators. */
export function f0(v?: number | bigint | null): string {
  return (v ?? 0).toLocaleString(undefined, { maximumFractionDigits: 0 });
}

/** One decimal place. */
export function f1(v?: number | null): string {
  return (v ?? 0).toFixed(1);
}

/** Two decimal places. */
export function f2(v?: number | null): string {
  return (v ?? 0).toFixed(2);
}

/** Fraction [0,1] → percentage string. */
export function pct(v?: number | null): string {
  return `${((v ?? 0) * 100).toFixed(1)}%`;
}

/** Compact price: $x.xx, or exponential for sub-cent unit prices. */
export function price(v?: number | null): string {
  const p = v ?? 0;
  return p === 0 || p >= 0.01 ? `$${p.toFixed(2)}` : `$${p.toExponential(2)}`;
}

/** Normalize a proto enum name: "COMPONENT_KIND_API_SERVER" → "api server". */
export function normKind(k?: string | null): string {
  return (k ?? "")
    .replace(/^COMPONENT_KIND_/, "")
    .replace(/_/g, " ")
    .trim()
    .toLowerCase();
}

const KIND_LABELS: Record<number, string> = {
  [ComponentKind.CLIENT]: "client",
  [ComponentKind.LOAD_BALANCER]: "load balancer",
  [ComponentKind.API_SERVER]: "api server",
  [ComponentKind.CACHE]: "cache",
  [ComponentKind.QUEUE]: "queue",
  [ComponentKind.WORKER]: "worker",
  [ComponentKind.DATABASE]: "database",
  [ComponentKind.OBJECT_STORAGE]: "object storage",
  [ComponentKind.NETWORK]: "network",
};

/** Human label for a numeric ComponentKind value. */
export function kindName(kind: number | undefined): string {
  return KIND_LABELS[kind ?? 0] ?? "component";
}
