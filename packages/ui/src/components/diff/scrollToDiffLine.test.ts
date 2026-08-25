import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { findDiffLineEl, scrollToDiffLine, type DiffJumpDeps } from "./scrollToDiffLine";

beforeEach(() => {
  // jsdom: scrollIntoView is not implemented (see CommentGutter.test.ts:22).
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.useRealTimers();
});

// Builds a `.diff-file` container under document.body for a test to attach
// line shapes to.
function makeDiffFile(path: string): HTMLElement {
  const fileEl = document.createElement("div");
  fileEl.className = "diff-file";
  fileEl.dataset.filePath = path;
  document.body.appendChild(fileEl);
  return fileEl;
}

// The wrapped shape DiffFile renders every line in.
function appendLineWrap(parent: HTMLElement, line: number, side: "LEFT" | "RIGHT"): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "line-wrap";
  wrap.dataset.anchorLine = String(line);
  wrap.dataset.anchorSide = side;
  parent.appendChild(wrap);
  return wrap;
}

// The bare shape a context line revealed inside a CollapsedRegion carries:
// no .line-wrap, attributes stamped on the .diff-line element itself.
function appendBareDiffLine(parent: HTMLElement, line: number, side: "LEFT" | "RIGHT"): HTMLElement {
  const el = document.createElement("div");
  el.className = "diff-line";
  el.dataset.anchorLine = String(line);
  el.dataset.anchorSide = side;
  parent.appendChild(el);
  return el;
}

describe("findDiffLineEl", () => {
  it("finds the .line-wrap shape", () => {
    const fileEl = makeDiffFile("a/b.go");
    const wrap = appendLineWrap(fileEl, 10, "RIGHT");

    expect(findDiffLineEl({ path: "a/b.go", line: 10, side: "RIGHT" })).toBe(wrap);
  });

  it("finds the bare .diff-line shape (the revealed-context case)", () => {
    const fileEl = makeDiffFile("a/b.go");
    const bare = appendBareDiffLine(fileEl, 20, "RIGHT");

    expect(findDiffLineEl({ path: "a/b.go", line: 20 })).toBe(bare);
  });

  it("defaults the side to RIGHT and does not match a LEFT-only anchor", () => {
    const fileEl = makeDiffFile("a/b.go");
    const leftOnly = appendLineWrap(fileEl, 5, "LEFT");

    expect(findDiffLineEl({ path: "a/b.go", line: 5 })).toBeNull();
    expect(findDiffLineEl({ path: "a/b.go", line: 5, side: "LEFT" })).toBe(leftOnly);
  });

  it("returns null for a line that is not present", () => {
    const fileEl = makeDiffFile("a/b.go");
    appendLineWrap(fileEl, 10, "RIGHT");

    expect(findDiffLineEl({ path: "a/b.go", line: 999 })).toBeNull();
  });

  it("still matches a path with CSS-special characters", () => {
    const fileEl = makeDiffFile("a/b[1].go");
    const wrap = appendLineWrap(fileEl, 7, "RIGHT");

    expect(findDiffLineEl({ path: "a/b[1].go", line: 7 })).toBe(wrap);
  });
});

function makeDeps(overrides: Partial<DiffJumpDeps> = {}): DiffJumpDeps {
  return {
    isFileCollapsed: () => false,
    toggleFileCollapsed: vi.fn(),
    requestRevealLine: vi.fn(),
    ...overrides,
  };
}

describe("scrollToDiffLine", () => {
  it("flashes a bare revealed-context line the same way as a wrapped one", async () => {
    // The shape CollapsedRegion produces: no .line-wrap, attributes on
    // the .diff-line element itself. flashDiffLine doesn't branch on
    // element shape, but this is the actual case Task 8 depends on, so
    // it's worth pinning directly rather than only through .line-wrap.
    vi.useFakeTimers();
    const fileEl = makeDiffFile("a/b.go");
    const bare = appendBareDiffLine(fileEl, 20, "RIGHT");
    const deps = makeDeps();

    const outcome = await scrollToDiffLine({ path: "a/b.go", line: 20 }, deps);

    expect(outcome).toBe("line");
    expect(bare.scrollIntoView).toHaveBeenCalledWith({ block: "center", behavior: "smooth" });
    expect(bare.classList.contains("line-wrap--flash")).toBe(true);

    await vi.advanceTimersByTimeAsync(1500);
    expect(bare.classList.contains("line-wrap--flash")).toBe(false);
  });

  it("flashes a rendered line, then removes the flash after the timeout", async () => {
    vi.useFakeTimers();
    const fileEl = makeDiffFile("a/b.go");
    const wrap = appendLineWrap(fileEl, 10, "RIGHT");
    const deps = makeDeps();

    const outcome = await scrollToDiffLine({ path: "a/b.go", line: 10 }, deps);

    expect(outcome).toBe("line");
    expect(wrap.scrollIntoView).toHaveBeenCalledWith({ block: "center", behavior: "smooth" });
    expect(wrap.classList.contains("line-wrap--flash")).toBe(true);
    expect(deps.requestRevealLine).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1500);
    expect(wrap.classList.contains("line-wrap--flash")).toBe(false);
  });

  it("expands a collapsed file before looking, and finds a line that mounts after the toggle", async () => {
    const fileEl = makeDiffFile("a/b.go");
    // Nothing rendered yet — simulates a collapsed file. The toggle mock
    // stands in for Svelte mounting the line once the file expands.
    let mounted: HTMLElement | null = null;
    const toggleFileCollapsed = vi.fn(() => {
      mounted = appendLineWrap(fileEl, 10, "RIGHT");
    });
    const deps = makeDeps({
      isFileCollapsed: () => true,
      toggleFileCollapsed,
    });

    const outcome = await scrollToDiffLine({ path: "a/b.go", line: 10 }, deps);

    expect(toggleFileCollapsed).toHaveBeenCalledWith("a/b.go");
    expect(outcome).toBe("line");
    expect(mounted).not.toBeNull();
    expect(mounted?.scrollIntoView).toHaveBeenCalled();
  });

  it("requests a reveal and scrolls to the file header when the line is missing from a present file", async () => {
    const fileEl = makeDiffFile("a/b.go");
    appendLineWrap(fileEl, 1, "RIGHT"); // unrelated line — the file itself is rendered
    const deps = makeDeps();

    const outcome = await scrollToDiffLine({ path: "a/b.go", line: 42 }, deps);

    expect(outcome).toBe("pending");
    expect(deps.requestRevealLine).toHaveBeenCalledWith("a/b.go", 42);
    expect(fileEl.scrollIntoView).toHaveBeenCalledWith({ block: "start", behavior: "smooth" });
  });

  it("reports missing when the file itself is not present, without throwing", async () => {
    const deps = makeDeps();

    const outcome = await scrollToDiffLine({ path: "does/not-exist.go", line: 1 }, deps);

    expect(outcome).toBe("missing");
  });
});
