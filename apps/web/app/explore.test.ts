/**
 * Explore gallery tests: every system must be a VALID, runnable
 * architecture — exactly the invariants the simulator's backend enforces
 * (sim.Architecture.Validate + provider resolution). A gallery card that
 * cannot run would be worse than no card at all.
 */
import { describe, expect, it } from "vitest";
import { ComponentKind } from "@loadline/api";
import {
  editorReducer,
  initialEditorState,
  validateArchitecture,
} from "./editor";
import {
  EXPLORE_CATEGORIES,
  exploreAsTemplate,
  exploreState,
  exploreSystemById,
} from "./explore";
import { TEMPLATES } from "./templates";

/** Keys accepted by providers.Catalog() — kept explicit on purpose. */
const CATALOG_KEYS = new Set([
  "aws/ec2",
  "aws/lambda",
  "aws/rds",
  "aws/elasticache",
  "aws/sqs",
  "aws/s3",
  "aws/cloudfront",
  "cloudflare/workers",
  "cloudflare/kv",
  "cloudflare/r2",
  "cloudflare/queues",
  "cloudflare/durable_objects",
  "gcp/cloud_run",
  "gcp/cloud_sql",
  "gcp/memorystore",
  "gcp/pub_sub",
  "gcp/cloud_storage",
  "gcp/cloud_cdn",
]);

const ALL_SYSTEMS = EXPLORE_CATEGORIES.flatMap((c) => c.systems);

describe("explore categories", () => {
  it("covers the seven requested categories", () => {
    expect(EXPLORE_CATEGORIES.map((c) => c.id)).toEqual([
      "social",
      "ai",
      "infrastructure",
      "fintech",
      "marketplaces",
      "media",
      "developer",
    ]);
  });

  it("has unique category and system ids", () => {
    expect(new Set(EXPLORE_CATEGORIES.map((c) => c.id)).size).toBe(
      EXPLORE_CATEGORIES.length,
    );
    expect(new Set(ALL_SYSTEMS.map((s) => s.id)).size).toBe(ALL_SYSTEMS.length);
  });

  it("has multiple systems per category with meaningful descriptions", () => {
    for (const c of EXPLORE_CATEGORIES) {
      expect(c.systems.length, c.id).toBeGreaterThanOrEqual(3);
      for (const s of c.systems) {
        expect(s.name.length, s.id).toBeGreaterThan(0);
        expect(s.description.length, s.id).toBeGreaterThan(20);
        expect(exploreSystemById(s.id), s.id).toBe(s);
      }
    }
  });
});

describe("explore systems", () => {
  it("each validates with no issues (exactly one client, no dangling links)", () => {
    for (const s of ALL_SYSTEMS) {
      expect(validateArchitecture(s.architecture, []), s.id).toEqual([]);
      const clients = s.architecture.components.filter(
        (c) => c.kind === ComponentKind.CLIENT,
      );
      expect(clients, s.id).toHaveLength(1);
    }
  });

  it("only reference provider services that exist in the catalog", () => {
    for (const s of ALL_SYSTEMS) {
      for (const c of s.architecture.components) {
        if (!c.provider) continue;
        expect(
          CATALOG_KEYS.has(`${c.provider}/${c.service}`),
          `${s.id}: ${c.provider}/${c.service}`,
        ).toBe(true);
      }
    }
  });

  it("keep component ids unique and referenced by every link", () => {
    for (const s of ALL_SYSTEMS) {
      const ids = s.architecture.components.map((c) => c.id);
      expect(new Set(ids).size, s.id).toBe(ids.length);
      const set = new Set(ids);
      for (const l of s.architecture.links) {
        expect(set.has(l.from), `${s.id}: ${l.from}`).toBe(true);
        expect(set.has(l.to), `${s.id}: ${l.to}`).toBe(true);
      }
    }
  });

  it("mirror the backend's queue/worker pairing rules", () => {
    for (const s of ALL_SYSTEMS) {
      const byId = new Map(s.architecture.components.map((c) => [c.id, c]));
      for (const c of s.architecture.components) {
        if (c.kind === ComponentKind.QUEUE) {
          const workers = s.architecture.links.filter(
            (l) => l.from === c.id && byId.get(l.to)?.kind === ComponentKind.WORKER,
          ).length;
          expect(workers, `${s.id}: queue ${c.id}`).toBe(1);
        }
        if (c.kind === ComponentKind.WORKER) {
          expect(
            s.architecture.links.some((l) => l.from === c.id),
            `${s.id}: worker ${c.id} must be terminal`,
          ).toBe(false);
          const queues = s.architecture.links.filter(
            (l) => l.to === c.id && byId.get(l.from)?.kind === ComponentKind.QUEUE,
          ).length;
          expect(queues, `${s.id}: worker ${c.id}`).toBe(1);
        }
      }
    }
  });

  it("are acyclic with every non-client component reachable from the client", () => {
    for (const s of ALL_SYSTEMS) {
      const outgoing = new Map<string, string[]>();
      for (const l of s.architecture.links) {
        outgoing.set(l.from, [...(outgoing.get(l.from) ?? []), l.to]);
      }
      const client = s.architecture.components.find(
        (c) => c.kind === ComponentKind.CLIENT,
      )!;
      const seen = new Set<string>([client.id]);
      const stack = [client.id];
      while (stack.length > 0) {
        const id = stack.pop()!;
        for (const to of outgoing.get(id) ?? []) {
          if (!seen.has(to)) {
            seen.add(to);
            stack.push(to);
          }
        }
      }
      for (const c of s.architecture.components) {
        if (c.kind !== ComponentKind.CLIENT) {
          expect(seen.has(c.id), `${s.id}: ${c.id} reachable`).toBe(true);
        }
      }
    }
  });

  it("carry kinds consistent with the provider catalog mapping", () => {
    const KIND_BY_SERVICE: Record<string, number> = {
      "aws/lambda": ComponentKind.API_SERVER,
      "gcp/cloud_run": ComponentKind.API_SERVER,
      "cloudflare/workers": ComponentKind.API_SERVER,
      "aws/ec2": ComponentKind.API_SERVER,
      "cloudflare/durable_objects": ComponentKind.API_SERVER,
      "aws/cloudfront": ComponentKind.NETWORK,
      "gcp/cloud_cdn": ComponentKind.NETWORK,
      "aws/elasticache": ComponentKind.CACHE,
      "gcp/memorystore": ComponentKind.CACHE,
      "cloudflare/kv": ComponentKind.CACHE,
      "aws/sqs": ComponentKind.QUEUE,
      "gcp/pub_sub": ComponentKind.QUEUE,
      "cloudflare/queues": ComponentKind.QUEUE,
      "aws/rds": ComponentKind.DATABASE,
      "gcp/cloud_sql": ComponentKind.DATABASE,
      "aws/s3": ComponentKind.OBJECT_STORAGE,
      "gcp/cloud_storage": ComponentKind.OBJECT_STORAGE,
      "cloudflare/r2": ComponentKind.OBJECT_STORAGE,
    };
    for (const s of ALL_SYSTEMS) {
      for (const c of s.architecture.components) {
        if (!c.provider) continue;
        const want = KIND_BY_SERVICE[`${c.provider}/${c.service}`];
        expect(want, `${s.id}: ${c.provider}/${c.service}`).toBeDefined();
        expect(c.kind, `${s.id}: ${c.id}`).toBe(want);
      }
    }
  });

  it("do not collide with existing template ids", () => {
    const templateIds = new Set(TEMPLATES.map((t) => t.id));
    for (const s of ALL_SYSTEMS) {
      expect(templateIds.has(s.id), s.id).toBe(false);
    }
  });
});

describe("exploreState", () => {
  it("keeps the user's workload and options, replaces the architecture", () => {
    const s = exploreSystemById("openai-inference")!;
    const state = exploreState(s, initialEditorState);
    expect(state.architecture).toBe(s.architecture);
    expect(state.workload).toEqual(initialEditorState.workload);
    expect(state.options.seed).toBe(initialEditorState.options.seed);
    expect(state.selected).toBeNull();
    expect(state.positions).toEqual({});
  });

  it("drops retry reasons and failures that do not exist in the system", () => {
    const base = editorReducer(initialEditorState, {
      type: "addFailure",
      failure: { target: "cache", type: 1, startMs: 0, durationMs: 1 } as never,
    });
    const state = exploreState(exploreSystemById("openai-inference")!, base);
    expect(state.failures).toEqual([]);
    expect(validateArchitecture(state.architecture, state.failures)).toEqual([]);
  });

  it("round-trips through the same loading path as templates", () => {
    const s = exploreSystemById("discord-messages")!;
    const t = exploreAsTemplate(s);
    expect(t.id).toBe(s.id);
    expect(t.architecture).toBe(s.architecture);
  });
});
