/**
 * Undo/redo history tests: selection is not an edit, structural edits
 * are, and repeated edits of the same field collapse into one step.
 */
import { describe, expect, it } from "vitest";
import { ComponentKind } from "@loadline/api";
import { initialEditorState, editorReducer } from "./editor";
import {
  HISTORY_LIMIT,
  canRedo,
  canUndo,
  docSignature,
  historyOf,
  historyReducer,
} from "./history";

function edit(h: ReturnType<typeof historyOf>, action: Parameters<typeof editorReducer>[1]) {
  return historyReducer(h, { type: "edit", action });
}

describe("docSignature", () => {
  it("ignores selection but sees document changes", () => {
    const a = historyOf(initialEditorState);
    const selected = { ...a.present, selected: "api" };
    expect(docSignature(selected)).toBe(a.sig);
    const renamed = editorReducer(a.present, { type: "renameArch", name: "other" });
    expect(docSignature(renamed)).not.toBe(a.sig);
  });
});

describe("historyReducer", () => {
  it("records structural edits and undoes them", () => {
    let h = historyOf(initialEditorState);
    expect(canUndo(h)).toBe(false);

    h = edit(h, {
      type: "addComponent",
      provider: "aws",
      service: "sqs",
      kind: ComponentKind.QUEUE,
    });
    expect(h.present.architecture.components).toHaveLength(5);
    expect(canUndo(h)).toBe(true);

    const undone = historyReducer(h, { type: "undo" });
    expect(undone.present.architecture.components).toHaveLength(4);
    expect(canRedo(undone)).toBe(true);

    const redone = historyReducer(undone, { type: "redo" });
    expect(redone.present.architecture.components).toHaveLength(5);
    expect(redone.present).toEqual(h.present);
  });

  it("does not record selection changes", () => {
    const h0 = historyOf(initialEditorState);
    const h1 = edit(h0, { type: "select", id: "api" });
    expect(h1.present.selected).toBe("api");
    expect(h1.past).toHaveLength(0);
    expect(canUndo(h1)).toBe(false);
  });

  it("drops the redo stack once a new edit lands", () => {
    let h = historyOf(initialEditorState);
    h = edit(h, { type: "renameArch", name: "v2" });
    h = historyReducer(h, { type: "undo" });
    expect(canRedo(h)).toBe(true);
    h = edit(h, { type: "renameArch", name: "v3" });
    expect(canRedo(h)).toBe(false);
    expect(h.present.architecture.name).toBe("v3");
  });

  it("coalesces repeated edits of the same field into one step", () => {
    let h = historyOf(initialEditorState);
    h = edit(h, { type: "patchWorkload", patch: { peakMultiplier: 6 } });
    h = edit(h, { type: "patchWorkload", patch: { peakMultiplier: 7 } });
    h = edit(h, { type: "patchWorkload", patch: { peakMultiplier: 8 } });
    expect(h.present.workload.peakMultiplier).toBe(8);
    expect(h.past).toHaveLength(1);

    // A different field starts a new step.
    h = edit(h, { type: "patchWorkload", patch: { totalUsers: 1_000n } });
    expect(h.past).toHaveLength(2);

    // One undo returns to the state before the whole peakMultiplier burst.
    h = historyReducer(h, { type: "undo" });
    expect(h.present.workload.peakMultiplier).toBe(8);
    expect(h.present.workload.totalUsers).toBe(initialEditorState.workload.totalUsers);
    h = historyReducer(h, { type: "undo" });
    expect(h.present.workload.peakMultiplier).toBe(5);
  });

  it("coalesces repeated drags of the same node", () => {
    let h = historyOf(initialEditorState);
    h = edit(h, { type: "moveNode", id: "api", x: 10, y: 10 });
    h = edit(h, { type: "moveNode", id: "api", x: 20, y: 20 });
    expect(h.past).toHaveLength(1);
    h = edit(h, { type: "moveNode", id: "cache", x: 5, y: 5 });
    expect(h.past).toHaveLength(2);
  });

  it("ignores no-op edits from the editor reducer", () => {
    const h0 = historyOf(initialEditorState);
    // Connecting a component to itself is rejected by editorReducer.
    const h1 = edit(h0, { type: "connect", from: "api", to: "api" });
    expect(h1).toBe(h0);
  });

  it("restores the exact previous document, including positions", () => {
    let h = historyOf(initialEditorState);
    h = edit(h, { type: "moveNode", id: "api", x: 120, y: 40 });
    expect(h.present.positions.api).toEqual({ x: 120, y: 40 });
    h = historyReducer(h, { type: "undo" });
    expect(h.present.positions.api).toBeUndefined();
    h = historyReducer(h, { type: "redo" });
    expect(h.present.positions.api).toEqual({ x: 120, y: 40 });
  });

  it("drops the oldest entry past the history limit", () => {
    let h = historyOf(initialEditorState);
    for (let i = 0; i < HISTORY_LIMIT + 10; i++) {
      // Alternate fields so every edit is its own step.
      h = edit(h, { type: "renameArch", name: `arch-${i}` });
      h = edit(h, { type: "patchWorkload", patch: { totalUsers: BigInt(i + 1) } });
    }
    expect(h.past.length).toBeLessThanOrEqual(HISTORY_LIMIT);
    let depth = 0;
    let cur = h;
    while (canUndo(cur)) {
      cur = historyReducer(cur, { type: "undo" });
      depth++;
      expect(depth).toBeLessThanOrEqual(HISTORY_LIMIT);
    }
  });

  it("load replaces the document and clears both stacks", () => {
    let h = historyOf(initialEditorState);
    h = edit(h, { type: "renameArch", name: "before" });
    h = historyReducer(h, { type: "undo" });
    const fresh = { ...initialEditorState, selected: "db" };
    const loaded = historyReducer(h, { type: "load", state: fresh });
    expect(loaded.present).toBe(fresh);
    expect(loaded.past).toHaveLength(0);
    expect(loaded.future).toHaveLength(0);
    expect(canUndo(loaded)).toBe(false);
  });

  it("undo/redo on empty stacks are no-ops", () => {
    const h = historyOf(initialEditorState);
    expect(historyReducer(h, { type: "undo" })).toBe(h);
    expect(historyReducer(h, { type: "redo" })).toBe(h);
  });
});
