/**
 * Document I/O tests: export → parse must be lossless, malformed input
 * must fail with an actionable message, and the share link must survive
 * a round trip through base64url without a server.
 */
import { describe, expect, it } from "vitest";
import { create, toJson } from "@bufbuild/protobuf";
import {
  ArchitectureSchema,
  ComponentKind,
  FailureSchema,
  FailureType,
} from "@loadline/api";
import { initialEditorState, editorReducer } from "./editor";
import {
  DOCUMENT_KIND,
  decodeShareHash,
  encodeShareHash,
  exportDocument,
  parseDocument,
  serializeDocument,
} from "./documentio";

const archJson = () => toJson(ArchitectureSchema, initialEditorState.architecture);

describe("exportDocument / parseDocument", () => {
  it("round-trips the architecture, workload, options, and positions", () => {
    const before = archJson();
    let state = editorReducer(initialEditorState, {
      type: "moveNode",
      id: "api",
      x: 64,
      y: 32,
    });
    state = editorReducer(state, { type: "renameArch", name: "exported" });

    const text = serializeDocument(exportDocument(state));
    const parsed = parseDocument(text);

    expect(toJson(ArchitectureSchema, parsed.architecture)).toEqual({
      ...(before as object),
      name: "exported",
    });
    expect(parsed.architecture.name).toBe("exported");
    expect(parsed.positions.api).toEqual({ x: 64, y: 32 });
    expect(parsed.workload?.peakMultiplier).toBe(state.workload.peakMultiplier);
    expect(parsed.workload?.totalUsers).toBe(state.workload.totalUsers);
    expect(parsed.options?.seed).toBe(state.options.seed);
    expect(parsed.failures).toEqual([]);
    expect(JSON.parse(text).kind).toBe(DOCUMENT_KIND);
  });

  it("round-trips injected failures with their targets", () => {
    const state = editorReducer(initialEditorState, {
      type: "addFailure",
      failure: create(FailureSchema, {
        target: "cache",
        type: FailureType.CRASH,
        startMs: 1000,
        durationMs: 2000,
      }),
    });
    const parsed = parseDocument(serializeDocument(exportDocument(state)));
    expect(parsed.failures).toHaveLength(1);
    expect(parsed.failures[0].target).toBe("cache");
    expect(parsed.failures[0].startMs).toBe(1000);
  });

  it("accepts a bare architecture JSON (git-friendly file)", () => {
    const parsed = parseDocument(JSON.stringify(archJson()));
    expect(parsed.architecture.components).toHaveLength(4);
    expect(parsed.workload).toBeUndefined();
  });

  it("keeps the schema version in the document", () => {
    const doc = exportDocument(initialEditorState);
    expect(doc.schemaVersion).toBe(initialEditorState.architecture.schemaVersion);
    expect(doc.kind).toBe(DOCUMENT_KIND);
  });
});

describe("parseDocument errors", () => {
  const bad = (text: string) => () => parseDocument(text);

  it("rejects invalid JSON", () => {
    expect(bad("{not json")).toThrow(/invalid JSON/i);
  });

  it("rejects a non-object document", () => {
    expect(bad("[1,2,3]")).toThrow(/expected a JSON object/i);
  });

  it("rejects an unknown document kind", () => {
    expect(bad(JSON.stringify({ kind: "other.thing" }))).toThrow(/unsupported document kind/i);
  });

  it("rejects an unsupported schema version", () => {
    expect(
      bad(
        JSON.stringify({
          kind: DOCUMENT_KIND,
          schemaVersion: "99",
          architecture: archJson(),
        }),
      ),
    ).toThrow(/unsupported schema version/i);
  });

  it("rejects duplicate component ids", () => {
    const dup = archJson() as { components: { id: string }[] };
    dup.components[1].id = dup.components[0].id;
    expect(bad(JSON.stringify(dup))).toThrow(/duplicate component ids/i);
  });

  it("rejects a link to a missing component", () => {
    const doc = exportDocument(initialEditorState);
    const arch = doc.architecture as { links: { from: string; to: string }[] };
    arch.links.push({ from: "api", to: "nowhere" });
    expect(bad(JSON.stringify(doc))).toThrow(/references a missing component/i);
  });

  it("rejects a failure targeting a missing component", () => {
    const doc = exportDocument(initialEditorState);
    doc.failures = [
      toJson(
        FailureSchema,
        create(FailureSchema, { target: "ghost", type: FailureType.CRASH }),
      ),
    ];
    expect(bad(JSON.stringify(doc))).toThrow(/missing component "ghost"/i);
  });

  it("reports a bad workload block instead of crashing", () => {
    const doc = exportDocument(initialEditorState);
    doc.workload = { totalUsers: "not-a-number" };
    expect(bad(JSON.stringify(doc))).toThrow(/workload block/i);
  });
});

describe("share hash", () => {
  it("round-trips a document through the URL fragment", () => {
    const doc = exportDocument(initialEditorState);
    const hash = encodeShareHash(doc);
    expect(hash.startsWith("#doc=")).toBe(true);
    expect(decodeShareHash(hash)).toEqual(doc);
  });

  it("survives non-ASCII architecture names", () => {
    const state = editorReducer(initialEditorState, {
      type: "renameArch",
      name: "système — 関連",
    });
    const hash = encodeShareHash(exportDocument(state));
    const back = decodeShareHash(hash);
    expect(back).not.toBeNull();
    const parsed = parseDocument(JSON.stringify(back));
    expect(parsed.architecture.name).toBe("système — 関連");
  });

  it("returns null for absent or corrupted fragments", () => {
    expect(decodeShareHash("")).toBeNull();
    expect(decodeShareHash("#other=1")).toBeNull();
    expect(decodeShareHash("#doc=%%%not-base64%%%")).toBeNull();
    expect(decodeShareHash(`#doc=${btoa(JSON.stringify({ kind: "nope" }))}`)).toBeNull();
  });

  it("rejects a share payload that fails document validation", () => {
    const doc = exportDocument(initialEditorState);
    const arch = doc.architecture as { components: { id: string }[] };
    arch.components[1].id = arch.components[0].id;
    const hash = encodeShareHash(doc);
    expect(() => parseDocument(JSON.stringify(decodeShareHash(hash)))).toThrow(
      /duplicate component ids/i,
    );
  });
});

describe("component kinds in documents", () => {
  it("keeps numeric kinds intact through JSON", () => {
    const state = editorReducer(initialEditorState, {
      type: "addComponent",
      provider: "aws",
      service: "sqs",
      kind: ComponentKind.QUEUE,
    });
    const parsed = parseDocument(serializeDocument(exportDocument(state)));
    const queue = parsed.architecture.components.find((c) => c.id === "sqs");
    expect(queue?.kind).toBe(ComponentKind.QUEUE);
  });
});
