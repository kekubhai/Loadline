import { render, screen, fireEvent } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CommandPalette, type Command } from "./commandpalette";

const commands: Command[] = [
  { id: "a", group: "view", label: "Go to architecture", run: vi.fn() },
  { id: "b", group: "view", label: "Go to simulation", run: vi.fn() },
  { id: "c", group: "edit", label: "Undo", hint: "⌘Z", run: vi.fn() },
  { id: "d", group: "edit", label: "Hidden", run: vi.fn(), disabled: true },
];

beforeEach(() => {
  vi.clearAllMocks();
});

function setup(open = true, onClose = vi.fn()) {
  const utils = render(
    <CommandPalette open={open} onClose={onClose} commands={commands} />,
  );
  return { ...utils, onClose };
}

describe("CommandPalette", () => {
  it("renders nothing while closed", () => {
    setup(false);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("lists enabled commands and skips disabled ones", () => {
    setup();
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual([
      "viewGo to architecture",
      "viewGo to simulation",
      "editUndo⌘Z",
    ]);
  });

  it("filters as the query changes", () => {
    setup();
    const input = screen.getByPlaceholderText("type a command…");
    fireEvent.change(input, { target: { value: "sim" } });
    const rows = screen.getAllByRole("button");
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("Go to simulation");
  });

  it("runs the selected command on Enter and closes", () => {
    const onClose = vi.fn();
    setup(true, onClose);
    const input = screen.getByPlaceholderText("type a command…");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(commands[1].run).toHaveBeenCalledTimes(1);
    expect(commands[0].run).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it("closes without running anything on Escape", () => {
    const onClose = vi.fn();
    setup(true, onClose);
    fireEvent.keyDown(screen.getByPlaceholderText("type a command…"), {
      key: "Escape",
    });
    expect(onClose).toHaveBeenCalled();
    expect(commands.some((c) => vi.mocked(c.run).mock.calls.length > 0)).toBe(
      false,
    );
  });

  it("shows an empty state when nothing matches", () => {
    setup();
    fireEvent.change(screen.getByPlaceholderText("type a command…"), {
      target: { value: "zzzz" },
    });
    expect(screen.getByText("no matching command")).toBeTruthy();
  });
});
