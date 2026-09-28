import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// Component tests render several panels across cases; unmount between
// tests so queries never see a previous test's DOM.
afterEach(() => {
  cleanup();
});
