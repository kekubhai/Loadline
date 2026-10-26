import { describe, expect, it } from "vitest";
import { userError, userErrorText } from "./usererror";

describe("userError / userErrorText", () => {
  it("maps Connect unavailable and browser fetch failures to a server-down sentence", () => {
    expect(userError(new Error("[unavailable] fetch failed"))).toMatch(
      /cannot reach the simulation server/i,
    );
    expect(userError(new TypeError("Failed to fetch"))).toMatch(
      /cannot reach the simulation server/i,
    );
    expect(userErrorText("[deadline_exceeded] HTTP 504")).toMatch(
      /cannot reach the simulation server/i,
    );
  });

  it("maps engine architecture rejections without Go prefixes", () => {
    expect(
      userErrorText(
        "[invalid_argument] sim: invalid architecture: architecture: dependency cycle detected",
      ),
    ).toMatch(/cycle/i);
    expect(
      userErrorText("sim: invalid architecture: architecture: some components are unreachable from the client"),
    ).toMatch(/reachable from the client/i);
    expect(userErrorText('architecture: queue "ingest" must link to exactly one worker, found 0')).toBe(
      'queue "ingest" must link to exactly one worker',
    );
    expect(userErrorText('architecture: duplicate component ID "api"')).toBe(
      'two components share the id "api" — each id must be unique',
    );
  });

  it("maps workload and capacity/cost preconditions", () => {
    expect(userErrorText("invalid workload: peak multiplier must be >= 1")).toMatch(
      /peak multiplier must be at least 1/i,
    );
    expect(
      userErrorText(
        "architecture has no provider-backed components; capacity estimates need provider references",
      ),
    ).toMatch(/cloud service from the palette/i);
  });

  it("does not leak protobuf decode dumps", () => {
    expect(
      userError(
        new Error(
          "architecture does not match the schema: cannot decode field architecture.components: invalid value at position 12 ($typeName)",
        ),
      ),
    ).toMatch(/not a valid LOADLINE architecture/i);
  });

  it("passes already-actionable document I/O messages through", () => {
    expect(userErrorText("invalid JSON — the file or pasted text could not be parsed")).toMatch(
      /invalid JSON/i,
    );
  });

  it("never returns a blank string", () => {
    expect(userErrorText("")).toMatch(/could not complete/i);
  });
});
