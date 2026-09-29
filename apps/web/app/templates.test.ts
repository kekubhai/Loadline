/**
 * Template tests: every template must be a VALID, runnable architecture
 * (one client, no dangling links, provider services that exist in the
 * simulator's catalog) — a template that cannot run would be worse than
 * no template at all.
 */
import { describe, expect, it } from "vitest";
import { ComponentKind } from "@loadline/api";
import {
  initialEditorState,
  editorReducer,
  validateArchitecture,
} from "./editor";
import { TEMPLATES, templateById, templateState } from "./templates";

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

describe("templates", () => {
  it("are non-empty and uniquely identified", () => {
    expect(TEMPLATES.length).toBeGreaterThanOrEqual(4);
    expect(new Set(TEMPLATES.map((t) => t.id)).size).toBe(TEMPLATES.length);
    for (const t of TEMPLATES) {
      expect(t.name.length).toBeGreaterThan(0);
      expect(t.description.length).toBeGreaterThan(20);
      expect(templateById(t.id)).toBe(t);
    }
  });

  it("each validates with no issues (exactly one client, no dangling links)", () => {
    for (const t of TEMPLATES) {
      expect(validateArchitecture(t.architecture, []), t.id).toEqual([]);
      const clients = t.architecture.components.filter(
        (c) => c.kind === ComponentKind.CLIENT,
      );
      expect(clients, t.id).toHaveLength(1);
    }
  });

  it("only reference provider services that exist in the catalog", () => {
    for (const t of TEMPLATES) {
      for (const c of t.architecture.components) {
        if (!c.provider) continue;
        expect(
          CATALOG_KEYS.has(`${c.provider}/${c.service}`),
          `${t.id}: ${c.provider}/${c.service}`,
        ).toBe(true);
      }
    }
  });

  it("keep component ids unique and referenced by every link", () => {
    for (const t of TEMPLATES) {
      const ids = t.architecture.components.map((c) => c.id);
      expect(new Set(ids).size, t.id).toBe(ids.length);
      const set = new Set(ids);
      for (const l of t.architecture.links) {
        expect(set.has(l.from), `${t.id}: ${l.from}`).toBe(true);
        expect(set.has(l.to), `${t.id}: ${l.to}`).toBe(true);
      }
    }
  });

  it("carry a kind consistent with the provider's own kind mapping", () => {
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
    for (const t of TEMPLATES) {
      for (const c of t.architecture.components) {
        if (!c.provider) continue;
        const want = KIND_BY_SERVICE[`${c.provider}/${c.service}`];
        expect(want, `${t.id}: ${c.provider}/${c.service}`).toBeDefined();
        expect(c.kind, `${t.id}: ${c.id}`).toBe(want);
      }
    }
  });
});

describe("templateState", () => {
  it("keeps the user's workload and options, replaces the architecture", () => {
    const t = templateById("queue-workers")!;
    const state = templateState(t, initialEditorState);
    expect(state.architecture).toBe(t.architecture);
    expect(state.workload).toEqual(initialEditorState.workload);
    expect(state.options.seed).toBe(initialEditorState.options.seed);
    expect(state.selected).toBeNull();
    expect(state.positions).toEqual({});
  });

  it("drops retry reasons that do not exist in the template", () => {
    const base = editorReducer(initialEditorState, {
      type: "patchOptions",
      patch: { retryOn: ["api", "cache", "gone"] },
    });
    const state = templateState(templateById("queue-workers")!, base);
    // "cache" does not exist in the worker pipeline, "api" does.
    expect(state.options.retryOn).toContain("api");
    expect(state.options.retryOn).not.toContain("cache");
    expect(state.options.retryOn).not.toContain("gone");
  });

  it("clears failures whose targets may not exist in the template", () => {
    const base = editorReducer(initialEditorState, {
      type: "addFailure",
      failure: { target: "cache", type: 1, startMs: 0, durationMs: 1 } as never,
    });
    const state = templateState(templateById("edge-cdn")!, base);
    expect(state.failures).toEqual([]);
    expect(validateArchitecture(state.architecture, state.failures)).toEqual([]);
  });
});
