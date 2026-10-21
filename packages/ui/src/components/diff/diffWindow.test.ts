import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createDiffWindow, estimateBodyHeight } from "./diffWindow.svelte.js";

// Controllable IntersectionObserver / ResizeObserver mocks. Each test
// drives intersection changes by hand and identifies observers by the
// rootMargin the window gave them.
class MockIO {
  static instances: MockIO[] = [];
  readonly callback: IntersectionObserverCallback;
  readonly rootMargin: string;
  readonly root: Element | null;
  targets = new Set<Element>();
  disconnected = false;
  constructor(cb: IntersectionObserverCallback, opts?: IntersectionObserverInit) {
    this.callback = cb;
    this.rootMargin = opts?.rootMargin ?? "";
    this.root = (opts?.root as Element | null | undefined) ?? null;
    MockIO.instances.push(this);
  }
  observe(t: Element): void { this.targets.add(t); }
  unobserve(t: Element): void { this.targets.delete(t); }
  disconnect(): void { this.disconnected = true; this.targets.clear(); }
  takeRecords(): IntersectionObserverEntry[] { return []; }
  fire(target: Element, isIntersecting: boolean, rect?: { top: number; bottom: number }): void {
    const r = rect ?? { top: 0, bottom: 0 };
    this.callback(
      [{
        target,
        isIntersecting,
        intersectionRatio: isIntersecting ? 1 : 0,
        boundingClientRect: { top: r.top, bottom: r.bottom } as DOMRectReadOnly,
        intersectionRect: {} as DOMRectReadOnly,
        rootBounds: { top: 0, bottom: 1000 } as DOMRectReadOnly,
        time: 0,
      } as IntersectionObserverEntry],
      this as unknown as IntersectionObserver,
    );
  }
}

class MockRO {
  static instances: MockRO[] = [];
  readonly callback: ResizeObserverCallback;
  disconnected = false;
  constructor(cb: ResizeObserverCallback) {
    this.callback = cb;
    MockRO.instances.push(this);
  }
  observe(): void {}
  unobserve(): void {}
  disconnect(): void { this.disconnected = true; }
  fire(): void { this.callback([], this as unknown as ResizeObserver); }
}

type G = { IntersectionObserver?: unknown; ResizeObserver?: unknown };
let savedIO: unknown;
let savedRO: unknown;

function live(margin: string): MockIO {
  const io = MockIO.instances.filter((o) => !o.disconnected && o.rootMargin === margin);
  expect(io).toHaveLength(1);
  return io[0]!;
}

function makeRoot(height = 1000): HTMLElement {
  const root = document.createElement("div");
  Object.defineProperty(root, "clientHeight", { configurable: true, value: height });
  document.body.appendChild(root);
  return root;
}

// Viewport height 1000 => margins: mount 2000, prefetch 4000,
// unmount 6000, visible 0.
const MOUNT = "2000px 0px";
const PREFETCH = "4000px 0px";
const UNMOUNT = "6000px 0px";
const VISIBLE = "0px 0px";

beforeEach(() => {
  MockIO.instances = [];
  MockRO.instances = [];
  savedIO = (globalThis as G).IntersectionObserver;
  savedRO = (globalThis as G).ResizeObserver;
  (globalThis as G).IntersectionObserver = MockIO;
  (globalThis as G).ResizeObserver = MockRO;
});

afterEach(() => {
  (globalThis as G).IntersectionObserver = savedIO;
  (globalThis as G).ResizeObserver = savedRO;
  document.body.innerHTML = "";
});

describe("createDiffWindow", () => {
  it("creates mount, prefetch, unmount and visible observers on the root with viewport-scaled margins", () => {
    const root = makeRoot();
    createDiffWindow(root);
    for (const m of [MOUNT, PREFETCH, UNMOUNT, VISIBLE]) {
      expect(live(m).root).toBe(root);
    }
  });

  it("moves a file through far -> prefetch -> mount", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    expect(w.zone("a.ts")).toBe("far");
    live(UNMOUNT).fire(el, true);
    expect(w.zone("a.ts")).toBe("far");
    live(PREFETCH).fire(el, true);
    expect(w.zone("a.ts")).toBe("prefetch");
    live(MOUNT).fire(el, true);
    expect(w.zone("a.ts")).toBe("mount");
  });

  it("keeps a mounted body until it leaves the unmount zone, not the prefetch zone", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    live(UNMOUNT).fire(el, true);
    live(PREFETCH).fire(el, true);
    live(MOUNT).fire(el, true);

    live(MOUNT).fire(el, false);
    expect(w.zone("a.ts")).toBe("mount");
    live(PREFETCH).fire(el, false);
    expect(w.zone("a.ts")).toBe("mount");
    live(UNMOUNT).fire(el, false);
    expect(w.zone("a.ts")).toBe("far");

    // Coming back only as far as the prefetch zone does not remount.
    live(UNMOUNT).fire(el, true);
    live(PREFETCH).fire(el, true);
    expect(w.zone("a.ts")).toBe("prefetch");
  });

  it("does not report mount for a file that was only ever in the prefetch zone", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    live(UNMOUNT).fire(el, true);
    live(PREFETCH).fire(el, true);
    live(PREFETCH).fire(el, false);
    expect(w.zone("a.ts")).toBe("far");
  });

  it("never drops a pinned file to far, and releases it on the last unpin", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    const unpinComposer = w.pin("a.ts", "composer");
    const unpinJump = w.pin("a.ts", "jump");
    expect(w.isPinned("a.ts")).toBe(true);
    // Pinned while far: it is mounted anyway (jump targets rely on this).
    expect(w.zone("a.ts")).toBe("mount");

    live(UNMOUNT).fire(el, true);
    live(MOUNT).fire(el, true);
    live(MOUNT).fire(el, false);
    live(UNMOUNT).fire(el, false);
    expect(w.zone("a.ts")).toBe("mount");

    unpinComposer();
    unpinComposer(); // idempotent: must not release the jump pin
    expect(w.isPinned("a.ts")).toBe(true);
    expect(w.zone("a.ts")).toBe("mount");
    unpinJump();
    expect(w.isPinned("a.ts")).toBe(false);
    expect(w.zone("a.ts")).toBe("far");
  });

  it("remembers heights per path", () => {
    const w = createDiffWindow(makeRoot());
    expect(w.height("a.ts")).toBeUndefined();
    w.rememberHeight("a.ts", 640);
    w.rememberHeight("b.ts", 0); // an unmeasurable (detached) body is ignored
    expect(w.height("a.ts")).toBe(640);
    expect(w.height("b.ts")).toBeUndefined();
  });

  it("reports whether a file is visible or has been scrolled past", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    expect(w.visibility("a.ts")).toBe("unknown");
    live(VISIBLE).fire(el, false, { top: 1200, bottom: 1500 });
    expect(w.visibility("a.ts")).toBe("below");
    live(VISIBLE).fire(el, true, { top: 100, bottom: 400 });
    expect(w.visibility("a.ts")).toBe("visible");
    live(VISIBLE).fire(el, false, { top: -500, bottom: -10 });
    expect(w.visibility("a.ts")).toBe("above");
  });

  it("stops tracking a file once unobserved", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    const unobserve = w.observe("a.ts", el);
    live(UNMOUNT).fire(el, true);
    live(MOUNT).fire(el, true);
    unobserve();
    expect(live(MOUNT).targets.has(el)).toBe(false);
    expect(w.zone("a.ts")).toBe("far");
  });

  it("recreates observers with new margins when the root's height changes", () => {
    const root = makeRoot(1000);
    const w = createDiffWindow(root);
    const el = document.createElement("div");
    w.observe("a.ts", el);
    const oldMount = live(MOUNT);

    Object.defineProperty(root, "clientHeight", { configurable: true, value: 500 });
    MockRO.instances[0]!.fire();

    expect(oldMount.disconnected).toBe(true);
    expect(live("1000px 0px").targets.has(el)).toBe(true); // mount
    expect(live("2000px 0px").targets.has(el)).toBe(true); // prefetch
    expect(live("3000px 0px").targets.has(el)).toBe(true); // unmount
    expect(w.zone("a.ts")).toBe("far");
  });

  it("honours custom viewport multipliers", () => {
    createDiffWindow(makeRoot(100), { mountViewports: 1, prefetchViewports: 3, unmountViewports: 5 });
    expect(live("100px 0px")).toBeTruthy();
    expect(live("300px 0px")).toBeTruthy();
    expect(live("500px 0px")).toBeTruthy();
  });

  it("disconnects every observer on destroy", () => {
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    w.destroy();
    expect(MockIO.instances.length).toBeGreaterThan(0);
    for (const io of MockIO.instances) expect(io.disconnected).toBe(true);
    expect(MockRO.instances[0]!.disconnected).toBe(true);
  });

  it("treats every observed file as mounted when IntersectionObserver is unavailable", () => {
    delete (globalThis as G).IntersectionObserver;
    const w = createDiffWindow(makeRoot());
    const el = document.createElement("div");
    w.observe("a.ts", el);
    expect(w.zone("a.ts")).toBe("mount");
    w.destroy();
  });

  it("tracks body-mounted state and resolves waiters", async () => {
    const w = createDiffWindow(makeRoot());
    expect(w.isBodyMounted("a.ts")).toBe(false);
    const waited = w.whenBodyMounted("a.ts");
    w.setBodyMounted("a.ts", true);
    await expect(waited).resolves.toBe(true);
    expect(w.isBodyMounted("a.ts")).toBe(true);
    await expect(w.whenBodyMounted("a.ts")).resolves.toBe(true);

    w.setBodyMounted("a.ts", false);
    const pending = w.whenBodyMounted("a.ts");
    w.destroy();
    await expect(pending).resolves.toBe(false);
  });
});

describe("estimateBodyHeight", () => {
  it("scales changed lines plus three rows per hunk by the row height", () => {
    expect(estimateBodyHeight({ additions: 3, deletions: 1, hunks: [{}, {}] }, 20)).toBe((4 + 6) * 20);
  });

  it("estimates hunk count from line count when hunks have not loaded", () => {
    // 100 changed lines, no hunks yet: assume one hunk per 20 lines.
    expect(estimateBodyHeight({ additions: 100, deletions: 0, hunks: [] }, 10)).toBe((100 + 15) * 10);
    expect(estimateBodyHeight({ additions: 0, deletions: 0, hunks: [] }, 20)).toBe(3 * 20);
  });
});
