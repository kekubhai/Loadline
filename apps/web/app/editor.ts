/**
 * LOADLINE — canonical editor + run-outcome state (pure logic, no JSX).
 *
 * This module owns ONE copy of {architecture, workload, options, failures}
 * plus canvas presentation state, mutated only through `editorReducer`.
 * It exists separately from page.tsx so the state machine can be tested
 * directly: the reducer is the same code the page ships, and nothing here
 * touches the DOM, React, or the network. Every number the UI displays
 * still comes from the simulation service — nothing simulated is computed
 * in this file.
 */
import {
  ArchitectureSchema,
  ComponentKind,
  ComponentSpecSchema,
  LinkSchema,
  ProviderConfigSchema,
  SimulationOptionsSchema,
  WorkloadSpecSchema,
  create,
} from "@loadline/api";
import type {
  Architecture,
  ComponentSpec,
  Failure,
  SimulationOptions,
  WorkloadSpec,
} from "@loadline/api";
import type { ComponentPatch } from "../components/inspector";

export const SCHEMA_VERSION = "1";

export interface EditorState {
  architecture: Architecture;
  workload: WorkloadSpec;
  options: SimulationOptions;
  failures: Failure[];
  /** Editor selection: component id, "link:<index>", or catalog token. */
  selected: string | null;
  /** Canvas positions per component id (editor presentation state). */
  positions: Record<string, { x: number; y: number }>;
}

/** First free id derived from base: rds, rds-2, rds-3, … Deterministic. */
export function freshId(components: ComponentSpec[], base: string): string {
  if (!components.some((c) => c.id === base)) return base;
  for (let i = 2; ; i++) {
    const id = `${base}-${i}`;
    if (!components.some((c) => c.id === id)) return id;
  }
}

export type EditAction =
  | { type: "select"; id: string | null }
  | { type: "renameArch"; name: string }
  | {
      type: "addComponent";
      provider: string;
      service: string;
      kind: number;
      pos?: { x: number; y: number };
    }
  | { type: "addClient" }
  | { type: "patchComponent"; id: string; patch: ComponentPatch }
  | { type: "renameComponent"; from: string; to: string }
  | { type: "deleteComponent"; id: string }
  | { type: "duplicateComponent"; id: string }
  | { type: "moveNode"; id: string; x: number; y: number }
  | { type: "resetLayout" }
  | { type: "connect"; from: string; to: string }
  | { type: "patchLink"; index: number; condition: string }
  | { type: "deleteLink"; index: number }
  | { type: "addFailure"; failure: Failure }
  | { type: "removeFailure"; index: number }
  | { type: "patchWorkload"; patch: Partial<WorkloadSpec> }
  | { type: "patchOptions"; patch: Partial<SimulationOptions> };

export function editorReducer(s: EditorState, a: EditAction): EditorState {
  switch (a.type) {
    case "select":
      return { ...s, selected: a.id };

    case "renameArch":
      return {
        ...s,
        architecture: { ...s.architecture, name: a.name },
      };

    case "addComponent": {
      const id = freshId(s.architecture.components, a.service.toLowerCase());
      const comp = create(ComponentSpecSchema, {
        id,
        kind: a.kind,
        provider: a.provider,
        service: a.service,
      });
      return {
        ...s,
        architecture: {
          ...s.architecture,
          components: [...s.architecture.components, comp],
        },
        positions: a.pos ? { ...s.positions, [id]: a.pos } : s.positions,
        selected: id,
      };
    }

    case "addClient": {
      const id = freshId(s.architecture.components, "client");
      const comp = create(ComponentSpecSchema, { id, kind: ComponentKind.CLIENT });
      return {
        ...s,
        architecture: {
          ...s.architecture,
          components: [comp, ...s.architecture.components],
        },
        selected: id,
      };
    }

    case "patchComponent": {
      const components = s.architecture.components.map((c) => {
        if (c.id !== a.id) return c;
        const next = create(ComponentSpecSchema, c);
        const p = a.patch;
        if (p.provider !== undefined) next.provider = p.provider;
        if (p.service !== undefined) next.service = p.service;
        if (p.concurrency !== undefined) next.concurrency = p.concurrency;
        if (p.queueLimit !== undefined) next.queueLimit = p.queueLimit;
        if (p.capacityRps !== undefined) next.capacityRps = p.capacityRps;
        if (p.defaultServiceTimeMillis !== undefined)
          next.defaultServiceTimeMillis = p.defaultServiceTimeMillis;
        if (p.hitRatio !== undefined) next.hitRatio = p.hitRatio;
        if (p.fanOut !== undefined) next.fanOut = p.fanOut;
        if (p.config !== undefined) {
          if (p.config === null) {
            next.config = undefined;
          } else {
            const merged = create(ProviderConfigSchema, c.config);
            const partial = p.config;
            if (partial.concurrency !== undefined)
              merged.concurrency = partial.concurrency;
            if (partial.queueLimit !== undefined)
              merged.queueLimit = partial.queueLimit;
            if (partial.units !== undefined) merged.units = partial.units;
            if (partial.memoryMb !== undefined)
              merged.memoryMb = partial.memoryMb;
            if (partial.storageGb !== undefined)
              merged.storageGb = partial.storageGb;
            if (partial.hitRatio !== undefined)
              merged.hitRatio = partial.hitRatio;
            next.config = merged;
          }
        }
        return next;
      });
      return { ...s, architecture: { ...s.architecture, components } };
    }

    case "renameComponent": {
      const to = a.to.trim();
      if (
        !to ||
        to === a.from ||
        s.architecture.components.some((c) => c.id === to)
      ) {
        return s;
      }
      const components = s.architecture.components.map((c) =>
        c.id === a.from ? { ...c, id: to } : c,
      );
      const links = s.architecture.links.map((l) =>
        create(LinkSchema, {
          from: l.from === a.from ? to : l.from,
          to: l.to === a.from ? to : l.to,
        }),
      );
      const failures = s.failures.map((f) =>
        f.target === a.from ? { ...f, target: to } : f,
      );
      return {
        ...s,
        architecture: { ...s.architecture, components, links },
        failures,
        selected: s.selected === a.from ? to : s.selected,
      };
    }

    case "deleteComponent": {
      const components = s.architecture.components.filter((c) => c.id !== a.id);
      const links = s.architecture.links.filter(
        (l) => l.from !== a.id && l.to !== a.id,
      );
      const failures = s.failures.filter((f) => f.target !== a.id);
      return {
        ...s,
        architecture: { ...s.architecture, components, links },
        failures,
        selected: s.selected === a.id ? null : s.selected,
      };
    }

    case "duplicateComponent": {
      const src = s.architecture.components.find((c) => c.id === a.id);
      if (!src) return s;
      const id = freshId(s.architecture.components, `${src.id}-copy`);
      const comp = create(ComponentSpecSchema, { ...src, id });
      const from = s.positions[src.id];
      return {
        ...s,
        architecture: {
          ...s.architecture,
          components: [...s.architecture.components, comp],
        },
        positions: from
          ? { ...s.positions, [id]: { x: from.x + 32, y: from.y + 32 } }
          : s.positions,
        selected: id,
      };
    }

    case "moveNode":
      return {
        ...s,
        positions: { ...s.positions, [a.id]: { x: a.x, y: a.y } },
      };

    case "resetLayout":
      return { ...s, positions: {} };

    case "connect": {
      if (a.from === a.to) return s;
      if (
        s.architecture.links.some(
          (l) => l.from === a.from && l.to === a.to,
        )
      ) {
        return s;
      }
      return {
        ...s,
        architecture: {
          ...s.architecture,
          links: [
            ...s.architecture.links,
            create(LinkSchema, { from: a.from, to: a.to }),
          ],
        },
        selected: `link:${s.architecture.links.length}`,
      };
    }

    case "patchLink": {
      const links = s.architecture.links.map((l, i) =>
        i === a.index ? { ...l, condition: a.condition } : l,
      );
      return { ...s, architecture: { ...s.architecture, links } };
    }

    case "deleteLink": {
      const links = s.architecture.links.filter((_, i) => i !== a.index);
      return {
        ...s,
        architecture: { ...s.architecture, links },
        selected: s.selected === `link:${a.index}` ? null : s.selected,
      };
    }

    case "addFailure":
      return { ...s, failures: [...s.failures, a.failure] };

    case "removeFailure":
      return { ...s, failures: s.failures.filter((_, i) => i !== a.index) };

    case "patchWorkload":
      return { ...s, workload: { ...s.workload, ...a.patch } };

    case "patchOptions":
      return { ...s, options: { ...s.options, ...a.patch } };
  }
}

/* ---------------------------------------------------- demo architecture -- */

// The demonstration architecture, built on the AWS catalog: Client → Lambda
// → ElastiCache → RDS. The crash toggle reuses the same seed for a
// controlled baseline-vs-cascade comparison.
export const INITIAL_ARCHITECTURE: Architecture = create(ArchitectureSchema, {
  schemaVersion: SCHEMA_VERSION,
  name: "aws-web",
  components: [
    { id: "client", kind: ComponentKind.CLIENT },
    {
      id: "api",
      kind: ComponentKind.API_SERVER,
      provider: "aws",
      service: "lambda",
      config: { memoryMb: 512 },
    },
    {
      id: "cache",
      kind: ComponentKind.CACHE,
      provider: "aws",
      service: "elasticache",
      hitRatio: 0.8,
    },
    {
      id: "db",
      kind: ComponentKind.DATABASE,
      provider: "aws",
      service: "rds",
      config: { storageGb: 100 },
    },
  ],
  links: [
    create(LinkSchema, { from: "client", to: "api" }),
    create(LinkSchema, { from: "api", to: "cache" }),
    create(LinkSchema, { from: "cache", to: "db" }),
  ],
});

export const INITIAL_WORKLOAD: WorkloadSpec = create(WorkloadSpecSchema, {
  totalUsers: 10_000_000n,
  dau: 1_000_000n,
  requestsPerUserPerDay: 40,
  peakMultiplier: 5,
  readWriteRatio: 4,
  payloadBytes: 4096n,
});

export const INITIAL_OPTIONS: SimulationOptions = create(SimulationOptionsSchema, {
  seed: 7n,
  durationMs: 10_000,
  maxRetries: 2,
  backoffBaseMs: 5,
  timeoutMs: 50,
  retryOn: ["api", "cache"],
});

export const initialEditorState: EditorState = {
  architecture: INITIAL_ARCHITECTURE,
  workload: INITIAL_WORKLOAD,
  options: INITIAL_OPTIONS,
  failures: [],
  positions: {},
  selected: null,
};

/* --------------------------------------------------------------- helpers -- */

/** All architecture validations the page can do locally (cheap, advisory). */
export function validateArchitecture(arch: Architecture, failures: Failure[]): string[] {
  const issues: string[] = [];
  const ids = new Set(arch.components.map((c) => c.id));
  const clients = arch.components.filter((c) => c.kind === ComponentKind.CLIENT);
  if (clients.length === 0) issues.push("no client — the simulator needs exactly one");
  if (clients.length > 1) issues.push("multiple clients — the simulator needs exactly one");
  for (const c of arch.components) {
    if (c.kind === ComponentKind.UNSPECIFIED) {
      issues.push(
        `component "${c.id}" has an unknown kind — remove it and add it again from the palette`,
      );
    }
  }
  for (const l of arch.links) {
    if (!ids.has(l.from) || !ids.has(l.to)) {
      issues.push(`link ${l.from} → ${l.to} references a missing component`);
    }
  }
  for (const f of failures) {
    if (!ids.has(f.target)) {
      issues.push(`failure targets missing component "${f.target}"`);
    }
  }
  return issues;
}

/** kind → generic role label for canvas nodes. */
export function kindRole(kind: number): string {
  switch (kind) {
    case ComponentKind.CLIENT:
      return "client";
    case ComponentKind.LOAD_BALANCER:
      return "load balancer";
    case ComponentKind.API_SERVER:
      return "api server";
    case ComponentKind.CACHE:
      return "cache";
    case ComponentKind.QUEUE:
      return "queue";
    case ComponentKind.WORKER:
      return "worker";
    case ComponentKind.DATABASE:
      return "database";
    case ComponentKind.OBJECT_STORAGE:
      return "object storage";
    case ComponentKind.NETWORK:
      return "network";
    default:
      return "component";
  }
}

/**
 * Catalog kind name → numeric ComponentKind for drop → node creation.
 *
 * The catalog ships `CatalogService.component_kind` as a plain domain
 * string ("api_server", "cache", …) because `componentKindName` returns
 * `string(spec.Kind)` from the simulator's own enum. Protobuf-encoded
 * enum names ("COMPONENT_KIND_API_SERVER") are accepted too so a future
 * `ListCatalog` that echoes the proto enum still resolves. Anything
 * unresolvable maps to UNSPECIFIED, which `validateArchitecture` reports
 * as an actionable issue rather than letting the run fail at the backend.
 */
export function kindFromProtoName(name: string): number {
  const key = (name ?? "").trim().replace(/^COMPONENT_KIND_/, "").toUpperCase();
  const table: Record<string, number> = {
    CLIENT: ComponentKind.CLIENT,
    LOAD_BALANCER: ComponentKind.LOAD_BALANCER,
    API_SERVER: ComponentKind.API_SERVER,
    CACHE: ComponentKind.CACHE,
    QUEUE: ComponentKind.QUEUE,
    WORKER: ComponentKind.WORKER,
    DATABASE: ComponentKind.DATABASE,
    OBJECT_STORAGE: ComponentKind.OBJECT_STORAGE,
    NETWORK: ComponentKind.NETWORK,
  };
  return table[key] ?? 0;
}
