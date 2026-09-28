import { defineConfig } from "vitest/config";

/**
 * Cross-stack E2E config. The workspace TS client is imported directly
 * from packages/api/src (same code the browser bundles). The Go server
 * must already be running — see the header of roundtrip.test.mts.
 */
export default defineConfig({
  test: {
    environment: "node",
    globals: false,
    include: ["**/*.test.mts"],
    testTimeout: 60_000,
  },
});
