/**
 * LOADLINE — architecture document I/O (pure logic, no React).
 *
 * The architecture is a versioned, serializable document (schema v1), so
 * it can leave the app: download as JSON for git-friendly files, paste it
 * back in, or encode it into a URL hash so a colleague opens the same
 * architecture without an account or a server-side store. There is no
 * hosted persistence in V1 by design — the file/URL IS the share.
 *
 * Serialization uses the generated protobuf JSON mapping, so a document
 * written by this module is the same shape the proto schema defines
 * (uint64 fields become decimal strings, enums keep their names).
 */
import { fromJson, toJson } from "@bufbuild/protobuf";
import type { JsonObject } from "@bufbuild/protobuf";
import {
  ArchitectureSchema,
  FailureSchema,
  SimulationOptionsSchema,
  WorkloadSpecSchema,
} from "@loadline/api";
import type { Architecture, Failure, SimulationOptions, WorkloadSpec } from "@loadline/api";
import { SCHEMA_VERSION, validateArchitecture } from "./editor";
import type { EditorState } from "./editor";

/** Discriminator written into every exported document. */
export const DOCUMENT_KIND = "loadline.architecture";

export interface LoadlineDocument {
  kind: typeof DOCUMENT_KIND;
  schemaVersion: string;
  architecture: unknown;
  /** Canvas positions (presentation state kept with the document). */
  positions?: Record<string, { x: number; y: number }>;
  workload?: unknown;
  options?: unknown;
  failures?: unknown[];
}

/** Snapshot the current editor document. */
export function exportDocument(s: EditorState): LoadlineDocument {
  const doc: LoadlineDocument = {
    kind: DOCUMENT_KIND,
    schemaVersion: s.architecture.schemaVersion || SCHEMA_VERSION,
    architecture: toJson(ArchitectureSchema, s.architecture),
  };
  if (Object.keys(s.positions).length > 0) doc.positions = s.positions;
  doc.workload = toJson(WorkloadSpecSchema, s.workload);
  doc.options = toJson(SimulationOptionsSchema, s.options);
  if (s.failures.length > 0) {
    doc.failures = s.failures.map((f) => toJson(FailureSchema, f));
  }
  return doc;
}

/** Pretty JSON for download / clipboard. */
export function serializeDocument(doc: LoadlineDocument): string {
  return JSON.stringify(doc, null, 2);
}

function fail(message: string): never {
  throw new Error(message);
}

function asObject(value: unknown): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail("not a LOADLINE document: expected a JSON object");
  }
  return value as JsonObject;
}

/**
 * Parse an exported document (or a bare Architecture JSON object) back
 * into editor state. Throws with an actionable message — the same
 * validation strings the architecture view shows — instead of surfacing
 * a protobuf stack trace.
 */
export function parseDocument(text: string): {
  architecture: Architecture;
  positions: Record<string, { x: number; y: number }>;
  workload?: WorkloadSpec;
  options?: SimulationOptions;
  failures: Failure[];
} {
  let raw: unknown;
  try {
    raw = JSON.parse(text) as unknown;
  } catch {
    return fail("invalid JSON — the file or pasted text could not be parsed");
  }
  const obj = asObject(raw);

  const isWrapped = obj.kind === DOCUMENT_KIND;
  if (obj.kind !== undefined && !isWrapped) {
    return fail(`unsupported document kind "${String(obj.kind)}"`);
  }
  if (isWrapped && obj.schemaVersion !== undefined && obj.schemaVersion !== SCHEMA_VERSION) {
    return fail(
      `unsupported schema version "${String(obj.schemaVersion)}" — this build reads v${SCHEMA_VERSION}`,
    );
  }

  const archJson = isWrapped ? obj.architecture : raw;
  let architecture: Architecture;
  try {
    architecture = fromJson(ArchitectureSchema, asObject(archJson));
  } catch (e) {
    return fail(`architecture does not match the schema: ${e instanceof Error ? e.message : String(e)}`);
  }
  if (architecture.components.length === 0) {
    return fail("the document contains no components");
  }
  const ids = architecture.components.map((c) => c.id);
  if (new Set(ids).size !== ids.length) {
    return fail("the document has duplicate component ids — ids must be unique");
  }
  const issues = validateArchitecture(architecture, []);
  if (issues.length > 0) return fail(issues.join(" · "));

  const positions = readPositions(isWrapped ? obj.positions : undefined);

  let workload: WorkloadSpec | undefined;
  if (isWrapped && obj.workload !== undefined) {
    try {
      workload = fromJson(WorkloadSpecSchema, asObject(obj.workload));
    } catch {
      return fail("the workload block does not match the schema");
    }
  }

  let options: SimulationOptions | undefined;
  if (isWrapped && obj.options !== undefined) {
    try {
      options = fromJson(SimulationOptionsSchema, asObject(obj.options));
    } catch {
      return fail("the options block does not match the schema");
    }
  }

  const failures: Failure[] = [];
  if (isWrapped && obj.failures !== undefined) {
    if (!Array.isArray(obj.failures)) return fail("failures must be an array");
    const targetIds = new Set(ids);
    for (const f of obj.failures) {
      try {
        failures.push(fromJson(FailureSchema, asObject(f)));
      } catch {
        return fail("a failure entry does not match the schema");
      }
    }
    for (const f of failures) {
      if (!targetIds.has(f.target)) {
        return fail(`failure targets missing component "${f.target}"`);
      }
    }
  }

  return { architecture, positions, workload, options, failures };
}

function readPositions(value: unknown): Record<string, { x: number; y: number }> {
  if (value === undefined || value === null) return {};
  const obj = asObject(value);
  const out: Record<string, { x: number; y: number }> = {};
  for (const [id, pos] of Object.entries(obj)) {
    const p = asObject(pos);
    if (typeof p.x === "number" && typeof p.y === "number") out[id] = { x: p.x, y: p.y };
  }
  return out;
}

/* ------------------------------------------------------------ url sharing -- */

function toBase64Url(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function fromBase64Url(text: string): string {
  const b64 = text.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

/** `#doc=<base64url>` fragment for a shareable, serverless link. */
export function encodeShareHash(doc: LoadlineDocument): string {
  return `#doc=${toBase64Url(serializeDocument(doc))}`;
}

/** Read a `#doc=...` fragment; null when absent or unreadable. */
export function decodeShareHash(hash: string): LoadlineDocument | null {
  const m = /[#&]doc=([^&]+)/.exec(hash);
  if (!m) return null;
  try {
    const parsed = JSON.parse(fromBase64Url(m[1])) as unknown;
    const obj = asObject(parsed);
    if (obj.kind !== DOCUMENT_KIND) return null;
    return obj as unknown as LoadlineDocument;
  } catch {
    return null;
  }
}
