import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DiffScope, DraftComment } from "../../stores/diff.svelte.js";
import { jumpToDraft, type DraftJumpStore } from "./jumpToDraft.js";
import { clearDiffLineHighlight } from "./scrollToDiffLine.js";

beforeEach(() => {
  // jsdom: scrollIntoView is not implemented.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  document.body.innerHTML = "";
  clearDiffLineHighlight();
});

function draft(over: Partial<DraftComment> = {}): DraftComment {
  return {
    id: "d1",
    path: "big.go",
    line: 4000,
    side: "RIGHT",
    commitSha: "",
    body: "x",
    createdAt: "2026-10-21T00:00:00Z",
    ...over,
  };
}

function fileEl(path: string): HTMLElement {
  const el = document.createElement("div");
  el.className = "diff-file";
  el.dataset.filePath = path;
  document.body.appendChild(el);
  return el;
}

function lineEl(parent: HTMLElement, line: number, side: "LEFT" | "RIGHT"): HTMLElement {
  const el = document.createElement("div");
  el.className = "line-wrap";
  el.dataset.anchorLine = String(line);
  el.dataset.anchorSide = side;
  parent.appendChild(el);
  return el;
}

function store(over: Partial<DraftJumpStore> = {}): DraftJumpStore {
  return {
    getScope: () => ({ kind: "head" }) as DiffScope,
    getCommits: () => [{ sha: "head" }],
    resetToHead: vi.fn(async () => {}),
    selectCommit: vi.fn(async () => {}),
    getCurrentPR: () => ({ owner: "o", name: "n", number: 1 }),
    isFileCollapsed: () => false,
    toggleFileCollapsed: vi.fn(),
    requestRevealLine: vi.fn(),
    consumeRevealTarget: vi.fn(),
    ensureFileReady: vi.fn(async () => true),
    ...over,
  };
}

describe("jumpToDraft", () => {
  it("waits for the draft's file to be ready, then lands on its line", async () => {
    const file = fileEl("big.go");
    // The body (and so the line) only exists once ensureFileReady has
    // expanded, loaded and mounted the file.
    const ensureFileReady = vi.fn(async () => {
      await Promise.resolve();
      lineEl(file, 4000, "RIGHT");
      return true;
    });
    const s = store({ ensureFileReady });

    await jumpToDraft(draft(), s);

    expect(ensureFileReady).toHaveBeenCalledWith("big.go");
    const line = file.querySelector<HTMLElement>('[data-anchor-line="4000"]')!;
    expect(line.scrollIntoView).toHaveBeenCalledWith({ block: "center", behavior: "smooth" });
    expect(line.classList.contains("line-wrap--jump-highlight")).toBe(true);
  });

  it("matches the draft's side", async () => {
    const file = fileEl("big.go");
    const right = lineEl(file, 7, "RIGHT");
    const left = lineEl(file, 7, "LEFT");

    await jumpToDraft(draft({ line: 7, side: "LEFT" }), store());

    expect(left.classList.contains("line-wrap--jump-highlight")).toBe(true);
    expect(right.classList.contains("line-wrap--jump-highlight")).toBe(false);
  });

  it("does not scroll anywhere when the file never becomes ready", async () => {
    const file = fileEl("big.go");
    const s = store({ ensureFileReady: vi.fn(async () => false) });

    await jumpToDraft(draft(), s);

    expect(file.scrollIntoView).not.toHaveBeenCalled();
    expect(s.requestRevealLine).not.toHaveBeenCalled();
  });
});
