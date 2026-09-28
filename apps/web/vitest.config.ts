import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

/**
 * Frontend test config.
 *
 * - jsdom environment for component tests.
 * - "@loadline/api" resolves to the workspace package's TypeScript source
 *   (protobuf-es v2 is runtime-free until messages are created, so tests
 *   import the same code the app ships — no prebuild of the package).
 */
export default defineConfig({
  // The app tsconfig uses "jsx": "preserve" (Next.js convention); tests
  // need real JSX transforms, so override here.
  oxc: { jsx: { runtime: "automatic" } },
  resolve: {
    alias: {
      "@loadline/api": fileURLToPath(
        new URL("../../packages/api/src/index.ts", import.meta.url),
      ),
    },
  },
  test: {
    environment: "jsdom",
    globals: false,
    include: ["components/**/*.test.tsx", "app/**/*.test.ts", "tests/**/*.test.ts"],
    setupFiles: ["./vitest.setup.ts"],
  },
});
