/**
 * Presentation-only formatter tests: these helpers shape backend values
 * for display; they never compute simulation numbers.
 */
import { describe, expect, it } from "vitest";
import { f0, f1, f2, pct, price, normKind, kindName, parseCatalogId } from "./format";

describe("numeric formatters", () => {
  it("f0 renders integers with separators and handles missing values", () => {
    expect(f0(46303)).toBe("46,303");
    expect(f0(undefined)).toBe("0");
    expect(f0(null)).toBe("0");
    expect(f0(1000n)).toBe("1,000");
  });

  it("f1/f2 render fixed decimals", () => {
    expect(f1(34.72)).toBe("34.7");
    expect(f2(25.5)).toBe("25.50");
    expect(f1(undefined)).toBe("0.0");
  });

  it("pct converts fractions to percentages", () => {
    expect(pct(0.5934)).toBe("59.3%");
    expect(pct(0)).toBe("0.0%");
    expect(pct(null)).toBe("0.0%");
  });

  it("price renders dollars, exponential for sub-cent", () => {
    expect(price(2859.46)).toBe("$2859.46");
    expect(price(0)).toBe("$0.00");
    expect(price(0.0000012)).toBe("$1.20e-6");
  });
});

describe("kind labels", () => {
  it("normKind strips the proto enum prefix", () => {
    expect(normKind("COMPONENT_KIND_API_SERVER")).toBe("api server");
    expect(normKind(null)).toBe("");
  });

  it("kindName maps numeric ComponentKind values", () => {
    expect(kindName(1)).toBe("client");
    expect(kindName(7)).toBe("database");
    expect(kindName(undefined)).toBe("component");
    expect(kindName(42)).toBe("component");
  });
});

describe("palette selection parsing", () => {
  it("parses catalog tokens and rejects component ids", () => {
    expect(parseCatalogId("catalog:aws:lambda")).toEqual({ provider: "aws", service: "lambda" });
    expect(parseCatalogId("catalog:aws")).toBeNull();
    expect(parseCatalogId("db")).toBeNull();
    expect(parseCatalogId(null)).toBeNull();
  });
});
