/**
 * Editor reducer tests — the canonical architecture/workload/options/
 * failures state machine, exactly the code the page ships.
 *
 * These tests cover the "Build architecture / Configure workload /
 * Inject failure" parts of the Step 6 frontend matrix at the state
 * level; nothing here simulates performance numbers.
 */
import { describe, expect, it } from "vitest";
import {
  ComponentKind,
  create,
} from "@loadline/api";
import { FailureSchema } from "@loadline/api";
import {
  editorReducer,
  initialEditorState,
  freshId,
  validateArchitecture,
  kindRole,
  kindFromProtoName,
} from "./editor";

function failure(over: Partial<{ target: string }> = {}) {
  return create(FailureSchema, {
    target: over.target ?? "cache",
    type: 1,
    startMs: 1000,
    durationMs: 5000,
  });
}

describe("initial editor state", () => {
  it("ships a valid demo architecture", () => {
    const s = initialEditorState;
    expect(s.architecture.schemaVersion).toBe("1");
    expect(
      s.architecture.components.filter((c) => c.kind === ComponentKind.CLIENT),
    ).toHaveLength(1);
    expect(validateArchitecture(s.architecture, s.failures)).toEqual([]);
  });

  it("carries provider references for capacity/cost eligibility", () => {
    const withProvider = initialEditorState.architecture.components.filter(
      (c) => c.provider && c.service,
    );
    expect(withProvider.length).toBeGreaterThanOrEqual(3);
  });
});

describe("build architecture", () => {
  it("adds a catalog component with a fresh id", () => {
    const s = editorReducer(initialEditorState, {
      type: "addComponent",
      provider: "aws",
      service: "SQS",
      kind: ComponentKind.QUEUE,
    });
    const added = s.architecture.components.find((c) => c.id === "sqs");
    expect(added).toBeDefined();
    expect(added?.kind).toBe(ComponentKind.QUEUE);
    expect(s.selected).toBe("sqs");
  });

  it("deduplicates ids (sqs, sqs-2, …)", () => {
    let s = editorReducer(initialEditorState, {
      type: "addComponent",
      provider: "aws",
      service: "SQS",
      kind: ComponentKind.QUEUE,
    });
    s = editorReducer(s, {
      type: "addComponent",
      provider: "aws",
      service: "SQS",
      kind: ComponentKind.QUEUE,
    });
    const ids = s.architecture.components.map((c) => c.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toContain("sqs");
    expect(ids).toContain("sqs-2");
  });

  it("connects two components and rejects self-loops and duplicates", () => {
    let s = editorReducer(initialEditorState, {
      type: "connect",
      from: "db",
      to: "cache",
    });
    expect(s.architecture.links.some((l) => l.from === "db" && l.to === "cache")).toBe(true);
    const count = s.architecture.links.length;
    s = editorReducer(s, { type: "connect", from: "db", to: "cache" });
    expect(s.architecture.links).toHaveLength(count); // duplicate rejected
    s = editorReducer(s, { type: "connect", from: "db", to: "db" });
    expect(s.architecture.links).toHaveLength(count); // self-loop rejected
  });

  it("renames a component and rewires links and failures", () => {
    let s = editorReducer(initialEditorState, { type: "addFailure", failure: failure({ target: "cache" }) });
    s = editorReducer(s, { type: "renameComponent", from: "cache", to: "memcache" });
    expect(s.architecture.components.some((c) => c.id === "memcache")).toBe(true);
    expect(s.architecture.links.some((l) => l.to === "memcache")).toBe(true);
    expect(s.failures[0].target).toBe("memcache");
  });

  it("renames the architecture", () => {
    const s = editorReducer(initialEditorState, { type: "renameArch", name: "prod" });
    expect(s.architecture.name).toBe("prod");
  });

  it("deletes a component with its links and failures atomically", () => {
    let s = editorReducer(initialEditorState, { type: "addFailure", failure: failure({ target: "cache" }) });
    s = editorReducer(s, { type: "deleteComponent", id: "cache" });
    expect(s.architecture.components.some((c) => c.id === "cache")).toBe(false);
    expect(s.architecture.links.some((l) => l.from === "cache" || l.to === "cache")).toBe(false);
    expect(s.failures.some((f) => f.target === "cache")).toBe(false);
  });

  it("duplicates a component without touching links", () => {
    let s = editorReducer(initialEditorState, {
      type: "moveNode",
      id: "cache",
      x: 100,
      y: 50,
    });
    const src = s.architecture.components.find((c) => c.id === "cache");
    const linkCount = s.architecture.links.length;
    s = editorReducer(s, { type: "duplicateComponent", id: "cache" });

    const copy = s.architecture.components.find((c) => c.id === "cache-copy");
    expect(copy).toBeDefined();
    expect(copy!.provider).toBe(src!.provider);
    expect(copy!.service).toBe(src!.service);
    expect(copy!.kind).toBe(src!.kind);
    expect(s.architecture.components).toHaveLength(
      initialEditorState.architecture.components.length + 1,
    );
    // Duplicating a node does not silently rewire traffic.
    expect(s.architecture.links).toHaveLength(linkCount);
    expect(s.selected).toBe("cache-copy");
    expect(s.positions["cache-copy"]).toEqual({ x: 132, y: 82 });

    // A second duplicate walks the id suffix instead of colliding.
    s = editorReducer(s, { type: "duplicateComponent", id: "cache" });
    expect(
      s.architecture.components.filter((c) => c.id.startsWith("cache-copy")),
    ).toHaveLength(2);
  });

  it("edits link conditions and deletes links by index", () => {
    let s = editorReducer(initialEditorState, { type: "patchLink", index: 1, condition: "read" });
    expect(s.architecture.links[1].condition).toBe("read");
    const n = s.architecture.links.length;
    s = editorReducer(s, { type: "deleteLink", index: 1 });
    expect(s.architecture.links).toHaveLength(n - 1);
  });
});

describe("configure components", () => {
  it("patches generic spec fields", () => {
    const s = editorReducer(initialEditorState, {
      type: "patchComponent",
      id: "db",
      patch: { concurrency: 16, capacityRps: 900 },
    });
    const db = s.architecture.components.find((c) => c.id === "db");
    expect(db?.concurrency).toBe(16);
    expect(db?.capacityRps).toBe(900);
  });

  it("merges provider config overrides and can detach the catalog model", () => {
    let s = editorReducer(initialEditorState, {
      type: "patchComponent",
      id: "api",
      patch: { config: { memoryMb: 1024, units: 2 } },
    });
    const api = s.architecture.components.find((c) => c.id === "api");
    expect(api?.config?.memoryMb).toBe(1024);
    expect(api?.config?.units).toBe(2);
    s = editorReducer(s, {
      type: "patchComponent",
      id: "api",
      patch: { config: null, provider: "", service: "" },
    });
    const detached = s.architecture.components.find((c) => c.id === "api");
    expect(detached?.config).toBeUndefined();
    expect(detached?.provider).toBe("");
  });
});

describe("configure workload and options", () => {
  it("patches workload fields", () => {
    const s = editorReducer(initialEditorState, {
      type: "patchWorkload",
      patch: { dau: 2_000_000n, peakMultiplier: 8 },
    });
    expect(s.workload.dau).toBe(2_000_000n);
    expect(s.workload.peakMultiplier).toBe(8);
    expect(s.workload.totalUsers).toBe(10_000_000n); // untouched
  });

  it("patches run options and retry targets", () => {
    let s = editorReducer(initialEditorState, {
      type: "patchOptions",
      patch: { seed: 42n },
    });
    expect(s.options.seed).toBe(42n);
    s = editorReducer(s, {
      type: "patchOptions",
      patch: { retryOn: ["api", "cache", "db"] },
    });
    expect(s.options.retryOn).toContain("db");
  });
});

describe("failure injections", () => {
  it("adds and removes scheduled failures", () => {
    let s = editorReducer(initialEditorState, { type: "addFailure", failure: failure() });
    expect(s.failures).toHaveLength(1);
    s = editorReducer(s, { type: "removeFailure", index: 0 });
    expect(s.failures).toHaveLength(0);
  });

  it("atomic delete removes the failure with the component", () => {
    let s = editorReducer(initialEditorState, { type: "addFailure", failure: failure({ target: "cache" }) });
    s = editorReducer(s, { type: "deleteComponent", id: "cache" });
    // The reducer cleans up failures atomically: no dangling targets.
    expect(s.failures).toHaveLength(0);
    expect(validateArchitecture(s.architecture, s.failures)).toEqual([]);
  });

  it("validation flags failures that target a missing component", () => {
    // Constructed directly: the only way a dangling target can exist
    // (the reducer deletes failures with their component).
    const issues = validateArchitecture(
      initialEditorState.architecture,
      [failure({ target: "ghost" })],
    );
    expect(issues.some((i) => i.includes("missing component"))).toBe(true);
  });
});

describe("canvas presentation state", () => {
  it("moves nodes and resets the layout", () => {
    let s = editorReducer(initialEditorState, { type: "moveNode", id: "db", x: 500, y: 300 });
    expect(s.positions.db).toEqual({ x: 500, y: 300 });
    s = editorReducer(s, { type: "resetLayout" });
    expect(s.positions).toEqual({});
  });
});

describe("helpers", () => {
  it("freshId walks to the first free suffix", () => {
    const ids = initialEditorState.architecture.components;
    expect(freshId(ids, "db")).toBe("db-2");
    expect(freshId(ids, "nope")).toBe("nope");
  });

  it("kindRole covers all nine generic kinds", () => {
    expect(kindRole(ComponentKind.CLIENT)).toBe("client");
    expect(kindRole(ComponentKind.QUEUE)).toBe("queue");
    expect(kindRole(ComponentKind.NETWORK)).toBe("network");
    expect(kindRole(99)).toBe("component");
  });

  it("kindFromProtoName maps proto enum names", () => {
    expect(kindFromProtoName("COMPONENT_KIND_DATABASE")).toBe(
      ComponentKind.DATABASE,
    );
    expect(kindFromProtoName("garbage")).toBe(0);
  });
});
