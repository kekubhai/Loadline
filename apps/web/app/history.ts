/**
 * LOADLINE — editor undo/redo history (pure logic, no React).
 *
 * The editor document {architecture, workload, options, failures,
 * positions} is the user's work product; every structural edit is
 * therefore undoable. Selection changes are deliberately NOT recorded
 * (clicking around is not an edit), and repeated edits that touch the
 * same field (dragging a node, typing a number) coalesce into a single
 * undo step so Ctrl+Z behaves the way people expect.
 *
 * History snapshots are plain editor states: undo restores the exact
 * document that existed before the edit, with no recomputation of any
 * kind. Nothing here derives simulation values — the backend does that.
 */
import { editorReducer } from "./editor";
import type { EditAction, EditorState } from "./editor";

/** Maximum undo depth. Oldest entries are dropped first. */
export const HISTORY_LIMIT = 100;

export interface EditorHistory {
  past: EditorState[];
  present: EditorState;
  future: EditorState[];
  /** Document signature of `present` (selection excluded). */
  sig: string;
  /** Coalesce key of the last accepted edit, or null. */
  coalesce: string | null;
}

export type HistoryAction =
  | { type: "edit"; action: EditAction }
  | { type: "undo" }
  | { type: "redo" }
  /** Replace the document wholesale (template load, import). */
  | { type: "load"; state: EditorState };

/**
 * Document signature: everything that makes the architecture document
 * what it is, minus `selected` (a selection is not an edit). BigInts are
 * stringified because protobuf uint64 fields are bigint in TS.
 */
export function docSignature(s: EditorState): string {
  return JSON.stringify(
    [s.architecture, s.workload, s.options, s.failures, s.positions],
    (_key, value: unknown) => (typeof value === "bigint" ? value.toString() : value),
  );
}

/**
 * Coalesce key: consecutive edits sharing a key merge into one undo
 * step. Number fields and node drags are the cases that produce a burst
 * of actions for what the user perceives as a single change.
 */
function coalesceKey(a: EditAction): string | null {
  switch (a.type) {
    case "moveNode":
      return `move:${a.id}`;
    case "patchWorkload":
      return `workload:${Object.keys(a.patch).sort().join(",")}`;
    case "patchOptions":
      return `options:${Object.keys(a.patch).sort().join(",")}`;
    case "patchComponent":
      return `component:${a.id}:${Object.keys(a.patch).sort().join(",")}`;
    case "renameArch":
      return "rename-arch";
    case "renameComponent":
      return `rename:${a.from}`;
    default:
      return null;
  }
}

export function historyOf(present: EditorState): EditorHistory {
  return {
    past: [],
    present,
    future: [],
    sig: docSignature(present),
    coalesce: null,
  };
}

export function canUndo(h: EditorHistory): boolean {
  return h.past.length > 0;
}

export function canRedo(h: EditorHistory): boolean {
  return h.future.length > 0;
}

export function historyReducer(h: EditorHistory, a: HistoryAction): EditorHistory {
  switch (a.type) {
    case "edit": {
      const next = editorReducer(h.present, a.action);
      // The reducer returns the same reference for no-op edits (duplicate
      // link, illegal rename): nothing to record.
      if (next === h.present) return h;
      const sig = docSignature(next);
      if (sig === h.sig) {
        // Presentation-only change (selection): keep it, don't record it.
        return { ...h, present: next };
      }
      const key = coalesceKey(a.action);
      if (key !== null && h.coalesce === key) {
        // Same field edited again: advance the present, leave `past`.
        return { ...h, present: next, sig };
      }
      const past = [...h.past, h.present].slice(-HISTORY_LIMIT);
      return { past, present: next, future: [], sig, coalesce: key };
    }

    case "undo": {
      if (h.past.length === 0) return h;
      const past = h.past.slice(0, -1);
      const present = h.past[h.past.length - 1];
      const future = [h.present, ...h.future].slice(0, HISTORY_LIMIT);
      return { past, present, future, sig: docSignature(present), coalesce: null };
    }

    case "redo": {
      if (h.future.length === 0) return h;
      const [present, ...future] = h.future;
      const past = [...h.past, h.present].slice(-HISTORY_LIMIT);
      return { past, present, future, sig: docSignature(present), coalesce: null };
    }

    case "load":
      return historyOf(a.state);
  }
}
