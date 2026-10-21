import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiffWindow, type DiffWindow } from "./diffWindow.svelte.js";
import {
  createFileReadiness,
  type FileHunkState,
  type FileReadinessDeps,
} from "./fileReadiness.js";

beforeEach(() => {
  // jsdom: scrollIntoView is not implemented.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  document.body.innerHTML = "";
});

function makeFileEl(path: string): HTMLElement {
  const el = document.createElement("div");
  el.className = "diff-file";
  el.dataset.filePath = path;
  document.body.appendChild(el);
  return el;
}

interface Harness {
  deps: FileReadinessDeps;
  win: DiffWindow;
  state: {
    collapsed: boolean;
    hunks: FileHunkState;
    files: string[] | undefined;
    window: DiffWindow | undefined;
  };
  calls: string[];
  clock: { now: number };
  frames: { count: number };
  // Runs on every awaited frame, so a test can make things "arrive".
  onFrame: { fn: ((frame: number) => void) | undefined };
}

// A real (IntersectionObserver-less) window plus a fake store. The
// window's body-mounted flag is driven by hand, standing in for DiffFile.
function harness(): Harness {
  const win = createDiffWindow(document.createElement("div"));
  const state: Harness["state"] = {
    collapsed: false,
    hunks: "loaded",
    files: ["big.go"],
    window: win,
  };
  const calls: string[] = [];
  const clock = { now: 0 };
  const frames = { count: 0 };
  const onFrame: Harness["onFrame"] = { fn: undefined };
  const deps: FileReadinessDeps = {
    getWindow: () => state.window,
    hasFile: (p) => (state.files === undefined ? undefined : state.files.includes(p)),
    isFileCollapsed: () => state.collapsed,
    expandForJump: (p) => {
      calls.push(`expand:${p}`);
      state.collapsed = false;
    },
    requestHunks: (p) => {
      calls.push(`hunks:${p}`);
      if (state.hunks === "idle") state.hunks = "loading";
    },
    getFileHunkState: () => state.hunks,
    findFileEl: (p) =>
      document.querySelector<HTMLElement>(`.diff-file[data-file-path="${p}"]`),
    nextFrame: async () => {
      frames.count++;
      clock.now += 16;
      await Promise.resolve();
      onFrame.fn?.(frames.count);
    },
    now: () => clock.now,
  };
  return { deps, win, state, calls, clock, frames, onFrame };
}

describe("ensureFileReady", () => {
  it("expands, pins, requests hunks and scrolls to a collapsed, unloaded, unmounted file, then resolves once its body mounts", async () => {
    const h = harness();
    h.state.collapsed = true;
    h.state.hunks = "idle";
    const fileEl = makeFileEl("big.go");
    let pinnedBeforeMount = false;
    h.onFrame.fn = (frame) => {
      if (frame === 3) {
        h.state.hunks = "loaded";
        pinnedBeforeMount = h.win.isPinned("big.go");
        h.win.setBodyMounted("big.go", true);
      }
    };
    const { ensureFileReady } = createFileReadiness(h.deps);

    const ok = await ensureFileReady("big.go");

    expect(ok).toBe(true);
    expect(h.calls).toEqual(["expand:big.go", "hunks:big.go"]);
    expect(pinnedBeforeMount).toBe(true);
    const scroll = vi.mocked(Element.prototype.scrollIntoView);
    expect(scroll.mock.contexts).toEqual([fileEl]);
    expect(scroll).toHaveBeenCalledWith({ block: "start", behavior: "instant" });
    // The jump-target pin outlives the jump.
    expect(h.win.isPinned("big.go")).toBe(true);
  });

  it("resolves true at once, without scrolling, for a body that is already mounted", async () => {
    const h = harness();
    const fileEl = makeFileEl("big.go");
    h.win.setBodyMounted("big.go", true);
    const { ensureFileReady } = createFileReadiness(h.deps);

    expect(await ensureFileReady("big.go")).toBe(true);
    expect(fileEl.scrollIntoView).not.toHaveBeenCalled(); // shared stub: no element scrolled
    expect(h.frames.count).toBe(0);
  });

  it("resolves false and drops its pin when the file's hunks fail to load", async () => {
    const h = harness();
    h.state.hunks = "idle";
    makeFileEl("big.go");
    h.onFrame.fn = (frame) => {
      if (frame === 2) h.state.hunks = "error";
    };
    const { ensureFileReady } = createFileReadiness(h.deps);

    expect(await ensureFileReady("big.go")).toBe(false);
    expect(h.win.isPinned("big.go")).toBe(false);
  });

  it("gives up after the timeout", async () => {
    const h = harness();
    h.state.hunks = "loading";
    makeFileEl("big.go");
    const { ensureFileReady } = createFileReadiness({ ...h.deps, timeoutMs: 1000 });

    expect(await ensureFileReady("big.go")).toBe(false);
    expect(h.clock.now).toBeGreaterThanOrEqual(1000);
    expect(h.win.isPinned("big.go")).toBe(false);
  });

  it("resolves false for a file that is not in the diff, without pinning", async () => {
    const h = harness();
    const { ensureFileReady } = createFileReadiness(h.deps);

    expect(await ensureFileReady("nope.go")).toBe(false);
    expect(h.win.isPinned("nope.go")).toBe(false);
    expect(h.frames.count).toBe(0);
  });

  it("resolves false for a file that stays collapsed", async () => {
    const h = harness();
    makeFileEl("big.go");
    const deps = { ...h.deps, isFileCollapsed: () => true };
    const { ensureFileReady } = createFileReadiness(deps);

    expect(await ensureFileReady("big.go")).toBe(false);
    expect(h.win.isPinned("big.go")).toBe(false);
  });

  it("waits for the diff, the window and the file element to appear (after a scope switch)", async () => {
    const h = harness();
    h.state.files = undefined;
    h.state.window = undefined;
    h.onFrame.fn = (frame) => {
      if (frame === 2) h.state.files = ["big.go"];
      if (frame === 3) h.state.window = h.win;
      if (frame === 4) makeFileEl("big.go");
      if (frame === 6) h.win.setBodyMounted("big.go", true);
    };
    const { ensureFileReady } = createFileReadiness(h.deps);

    expect(await ensureFileReady("big.go")).toBe(true);
    expect(h.win.isPinned("big.go")).toBe(true);
  });

  it("a newer jump cancels a waiting one and takes over the pin", async () => {
    const h = harness();
    h.state.hunks = "loading";
    makeFileEl("big.go");
    makeFileEl("other.go");
    h.state.files = ["big.go", "other.go"];
    h.win.setBodyMounted("other.go", true);
    const { ensureFileReady } = createFileReadiness(h.deps);

    const first = ensureFileReady("big.go");
    await Promise.resolve();
    await Promise.resolve();
    const second = ensureFileReady("other.go");

    expect(await first).toBe(false);
    expect(await second).toBe(true);
    expect(h.win.isPinned("big.go")).toBe(false);
    expect(h.win.isPinned("other.go")).toBe(true);
  });

  it("releaseJumpPin drops the pin left by a finished jump", async () => {
    const h = harness();
    makeFileEl("big.go");
    h.win.setBodyMounted("big.go", true);
    const { ensureFileReady, releaseJumpPin } = createFileReadiness(h.deps);

    await ensureFileReady("big.go");
    expect(h.win.isPinned("big.go")).toBe(true);
    releaseJumpPin();
    expect(h.win.isPinned("big.go")).toBe(false);
  });

  it("releaseJumpPin (a user scroll) cancels a jump that is still waiting", async () => {
    const h = harness();
    h.state.hunks = "loading";
    makeFileEl("big.go");
    const { ensureFileReady, releaseJumpPin } = createFileReadiness(h.deps);
    h.onFrame.fn = (frame) => {
      if (frame === 2) releaseJumpPin();
    };

    expect(await ensureFileReady("big.go")).toBe(false);
    expect(h.win.isPinned("big.go")).toBe(false);
  });

  it("resolves false when the window is replaced mid-wait", async () => {
    const h = harness();
    h.state.hunks = "loading";
    makeFileEl("big.go");
    h.onFrame.fn = (frame) => {
      if (frame === 2) h.state.window = createDiffWindow(document.createElement("div"));
    };
    const { ensureFileReady } = createFileReadiness(h.deps);

    expect(await ensureFileReady("big.go")).toBe(false);
  });
});
