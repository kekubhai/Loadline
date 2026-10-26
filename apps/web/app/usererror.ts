/**
 * User-facing error copy.
 *
 * Connect RPC failures, Go engine validation, and protobuf decode dumps
 * are internal. The UI shows a sentence the designer can act on — never
 * `[unavailable]`, `sim: invalid architecture:`, or a fromJson stack.
 */

const NETWORK =
  "cannot reach the simulation server — start it with go run ./cmd/loadline-server from apps/simulator, then try again";

const BAD_DOCUMENT =
  "this file is not a valid LOADLINE architecture — export again from the editor";

const GENERIC =
  "the simulation could not complete — check the architecture and try again";

const CONNECT_CODE = /^\[([a-z_]+)\]\s*/i;

const NETWORK_CODES = new Set([
  "unavailable",
  "deadline_exceeded",
  "canceled",
]);

const NETWORK_TEXT =
  /failed to fetch|fetch failed|networkerror|econnrefused|err_connection|err_failed|enotfound|etimedout|econnreset|socket hang up|load failed|network request failed|http 502|http 503|http 504|err_network/i;

/**
 * Map any thrown value (Connect error, TypeError, string) to copy the UI
 * can show. Empty input becomes a generic sentence, never a blank banner.
 */
export function userError(err: unknown): string {
  return userErrorText(rawMessage(err));
}

/**
 * Map a stored or streamed error string (run.error, comparison entry,
 * FAILED status frame) the same way as a thrown value.
 */
export function userErrorText(raw: string): string {
  const original = raw.trim();
  if (!original) return GENERIC;

  const code = connectCode(original);
  let text = stripPrefixes(original);

  if (code && NETWORK_CODES.has(code)) return NETWORK;
  if (NETWORK_TEXT.test(original) || NETWORK_TEXT.test(text)) return NETWORK;

  const mapped = mapKnown(text);
  if (mapped) return mapped;

  if (looksLikeSchemaDump(text) || looksLikeSchemaDump(original)) {
    return BAD_DOCUMENT;
  }

  if (looksInternal(text)) return GENERIC;
  return text;
}

function rawMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return String(err);
}

function connectCode(raw: string): string {
  const m = CONNECT_CODE.exec(raw);
  return m ? m[1].toLowerCase() : "";
}

/** Peel Connect codes and engine wrappers so the remainder can be matched. */
function stripPrefixes(raw: string): string {
  let text = raw.trim();
  text = text.replace(CONNECT_CODE, "");
  text = text.replace(/^sim:\s*/i, "");
  text = text.replace(/^invalid architecture:\s*/i, "");
  text = text.replace(/^invalid workload:\s*/i, "");
  text = text.replace(/^architecture:\s*/i, "");
  text = text.replace(/^workload:\s*/i, "");
  return text.trim();
}

function mapKnown(text: string): string | null {
  const lower = text.toLowerCase();

  if (lower.includes("dependency cycle")) {
    return "this architecture has a cycle — every path from the client must end without looping back";
  }
  if (lower.includes("unreachable from the client")) {
    return "some components are not reachable from the client — connect them or remove them";
  }
  if (/exactly one client/.test(lower) || /no client/.test(lower)) {
    return "the simulator needs exactly one client";
  }
  if (lower.includes("has no components")) {
    return "add at least one component, including a client";
  }
  if (/duplicate component/i.test(text)) {
    const id = quoted(text);
    return id
      ? `two components share the id "${id}" — each id must be unique`
      : "two components share an id — each id must be unique";
  }
  if (/self-link/i.test(text)) {
    const id = quoted(text);
    return id
      ? `component "${id}" links to itself — remove that link`
      : "a component links to itself — remove that link";
  }
  if (/unknown source/i.test(text) || /unknown target/i.test(text) || /references a missing/i.test(text)) {
    return "a link points at a missing component — delete the link or restore the node";
  }
  if (/queue .+ must link to exactly one worker/i.test(text) || /queue ".+" must link/i.test(text)) {
    const id = quoted(text);
    return id
      ? `queue "${id}" must link to exactly one worker`
      : "each queue must link to exactly one worker";
  }
  if (/cannot link onward/i.test(text)) {
    const id = quoted(text);
    return id
      ? `worker "${id}" must be the end of its flow — it cannot link onward`
      : "a worker must be the end of its flow — it cannot link onward";
  }
  if (/must be linked from exactly one queue/i.test(text)) {
    const id = quoted(text);
    return id
      ? `worker "${id}" must be linked from exactly one queue`
      : "each worker must be linked from exactly one queue";
  }
  if (/unknown target component/i.test(text) || /failure .+ unknown target/i.test(text)) {
    return "a failure targets a component that is not on the canvas";
  }
  if (/no provider-backed components/i.test(text)) {
    return "capacity and cost reports need a cloud service from the palette — generic nodes have no pricing model";
  }
  if (/peak .+ exceeds the simulatable ceiling/i.test(text) || /sub-nanosecond/i.test(text)) {
    return "peak load is higher than the simulator can express — lower peak RPS or the peak multiplier";
  }
  if (/would generate about .+ requests \(limit/i.test(text)) {
    return "this workload would generate too many requests for one run — shorten the horizon or lower the offered load";
  }
  if (/peak multiplier/.test(lower)) {
    return "peak multiplier must be at least 1";
  }
  if (/totalusers must be/.test(lower) || /total users must be/.test(lower)) {
    return "total users must be greater than 0";
  }
  if (/dau \(.+\) cannot exceed/i.test(text) || /cannot exceed totalusers/i.test(text)) {
    return "daily active users cannot exceed total users";
  }
  if (/dau must be/.test(lower)) {
    return "daily active users must be greater than 0";
  }
  if (/requestsperuserperday/.test(lower) || /req \/ user/.test(lower)) {
    return "requests per user per day must be greater than 0";
  }
  if (/readwriteratio/.test(lower)) {
    return "read/write ratio must be greater than 0";
  }
  if (/payloadbytes/.test(lower)) {
    return "payload size must be zero or greater";
  }
  if (/architecture is required/.test(lower)) {
    return "an architecture is required to run a simulation";
  }
  if (/workload is required/.test(lower)) {
    return "a workload is required to run a simulation";
  }
  if (/does not match the schema/.test(lower)) {
    return BAD_DOCUMENT;
  }
  if (/backend returned no simulation id/.test(lower)) {
    return "the server did not start a simulation — try again";
  }
  if (/stream ended without results/.test(lower) || /^simulation failed$/.test(lower)) {
    return GENERIC;
  }
  return null;
}

function quoted(text: string): string | null {
  const m = text.match(/"([^"]+)"/);
  return m ? m[1] : null;
}

function looksLikeSchemaDump(text: string): boolean {
  return (
    /cannot decode|fromJson|invalid value at|expected object|\$typeName|proto3|json mapping/i.test(
      text,
    ) && text.length > 80
  );
}

function looksInternal(text: string): boolean {
  if (text.length > 280) return true;
  return /panic:|goroutine |stack trace|\.go:\d+|rpc error|connecterror/i.test(text);
}
