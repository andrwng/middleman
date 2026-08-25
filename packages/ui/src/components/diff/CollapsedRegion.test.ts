import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import CollapsedRegion from "./CollapsedRegion.svelte";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import type { MiddlemanClient } from "../../types.js";

// Mock highlight utils to avoid loading Shiki in tests — mirrors the same
// mock in DiffFile.test.ts, which exercises the same module.
vi.mock("../../utils/highlight.js", () => ({
  tokenizeLineDual: () => Promise.resolve([]),
  langFromPath: () => "text",
}));

afterEach(() => {
  cleanup();
});

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

// loadBlobRange is stubbed to synthesize placeholder content of exactly the
// requested length. The anchor numbering under test is computed purely
// from gapNewStart/lineCount/i (see newNumForTop/newNumForBottom in
// CollapsedRegion.svelte), not from blob content — the label just makes a
// failing assertion easier to read.
function stubbedDiffStore() {
  const diffStore = createDiffStore({ client: stubClient() });
  vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
    (_path: string, _sha: string, start: number, end: number) =>
      Promise.resolve(
        Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => `line ${start + i}`),
      ),
  );
  return diffStore;
}

interface RegionProps {
  position?: "top" | "middle" | "bottom";
  layout?: "unified" | "split";
  lineCount: number;
  owner: string;
  name: string;
  number: number;
  path: string;
  sha: string;
  gapOldStart: number;
  gapNewStart: number;
  lang?: string;
  revealNewLine?: number | null;
  onrevealed?: () => void;
}

// diffStoreOverride lets a test install its own loadBlobRange mock
// (e.g. one that simulates EOF) BEFORE the component ever mounts —
// CollapsedRegion's reveal effect can issue its first fetch
// synchronously during render, so overriding stubbedDiffStore()'s
// default mock only after renderRegion() returns would be too late.
function renderRegion(
  props: Partial<RegionProps> = {},
  diffStoreOverride?: ReturnType<typeof stubbedDiffStore>,
) {
  const diffStore = diffStoreOverride ?? stubbedDiffStore();
  const rendered = render(CollapsedRegion, {
    props: {
      layout: "unified",
      lineCount: 3,
      owner: "o",
      name: "n",
      number: 1,
      path: "a/b.go",
      sha: "deadbeef",
      gapOldStart: 100,
      gapNewStart: 100,
      ...props,
    } as RegionProps,
    context: new Map<symbol, unknown>([[STORES_KEY, { diff: diffStore }]]),
  });
  return { ...rendered, diffStore };
}

describe("CollapsedRegion anchors on revealed context lines", () => {
  it("unified layout: expanding a middle region stamps RIGHT anchors at gapNewStart + i", async () => {
    const { container, getByRole } = renderRegion({
      position: "middle",
      layout: "unified",
      lineCount: 3,
      gapNewStart: 200,
      gapOldStart: 150,
    });

    await fireEvent.click(getByRole("button"), { shiftKey: true }); // expandAll()
    await waitFor(() => {
      expect(container.querySelectorAll(".diff-line[data-anchor-line]").length).toBe(3);
    });

    for (let i = 0; i < 3; i++) {
      const el = container.querySelector(`.diff-line[data-anchor-line="${200 + i}"]`);
      expect(el).not.toBeNull();
      expect(el?.getAttribute("data-anchor-side")).toBe("RIGHT");
    }
  });

  it("split layout: expanding a middle region stamps the RIGHT cell only, never the LEFT cell", async () => {
    const { container, getByRole } = renderRegion({
      position: "middle",
      layout: "split",
      lineCount: 2,
      gapNewStart: 300,
      gapOldStart: 280,
    });

    await fireEvent.click(getByRole("button"), { shiftKey: true }); // expandAll()
    await waitFor(() => {
      expect(container.querySelectorAll(".ss-row").length).toBe(2);
    });

    expect(container.querySelectorAll(".ss-cell--left [data-anchor-line]").length).toBe(0);

    const rows = container.querySelectorAll(".ss-row");
    for (let i = 0; i < 2; i++) {
      const rightCell = rows[i]?.querySelector(".ss-cell:not(.ss-cell--left)");
      const anchored = rightCell?.querySelector(`[data-anchor-line="${300 + i}"][data-anchor-side="RIGHT"]`);
      expect(anchored).toBeTruthy();
    }
  });

  it("top position: a plain click grows from the bottom edge, numbering by lineCount (not just gapNewStart + i)", async () => {
    const { container, getByRole } = renderRegion({
      position: "top",
      layout: "unified",
      lineCount: 15,
      gapNewStart: 1,
      gapOldStart: 1,
    });

    await fireEvent.click(getByRole("button")); // expandStep() -> expandBottom(10) for "top"
    await waitFor(() => {
      expect(container.querySelectorAll(".diff-line[data-anchor-line]").length).toBe(10);
    });

    // The bottom edge reveals the numerically-highest 10 lines of the
    // 15-line gap first (adjacent to the first hunk): new nums 6..15.
    for (let i = 0; i < 10; i++) {
      const el = container.querySelector(`.diff-line[data-anchor-line="${6 + i}"]`);
      expect(el).not.toBeNull();
      expect(el?.getAttribute("data-anchor-side")).toBe("RIGHT");
    }
    // Lines 1..5 sit at the unrevealed top edge — not yet mounted.
    expect(container.querySelector('.diff-line[data-anchor-line="1"]')).toBeNull();
  });

  it("bottom position: revealed lines number via gapNewStart + i despite the synthetic lineCount DiffFile passes", async () => {
    // DiffFile always passes lineCount={0} for a "bottom" CollapsedRegion
    // (its true extent is unknown until EOF) — reproduce that here to
    // confirm the rendered numbers don't depend on it.
    const { container, getByRole } = renderRegion({
      position: "bottom",
      layout: "unified",
      lineCount: 0,
      gapNewStart: 500,
      gapOldStart: 480,
    });

    await fireEvent.click(getByRole("button")); // expandStep() -> expandTop(10) for "bottom"
    await waitFor(() => {
      expect(container.querySelectorAll(".diff-line[data-anchor-line]").length).toBe(10);
    });

    for (let i = 0; i < 10; i++) {
      const el = container.querySelector(`.diff-line[data-anchor-line="${500 + i}"]`);
      expect(el).not.toBeNull();
      expect(el?.getAttribute("data-anchor-side")).toBe("RIGHT");
    }
  });
});

describe("CollapsedRegion reveal-and-jump (revealNewLine / onrevealed)", () => {
  it("a revealNewLine inside the window expands the region with a single bulk fetch and fires onrevealed once", async () => {
    const onrevealed = vi.fn();
    const { container, diffStore } = renderRegion({
      position: "middle",
      lineCount: 10,
      gapNewStart: 200,
      gapOldStart: 150,
      revealNewLine: 204,
      onrevealed,
    });

    await waitFor(() => {
      expect(onrevealed).toHaveBeenCalledTimes(1);
    });

    const el = container.querySelector('[data-anchor-line="204"][data-anchor-side="RIGHT"]');
    expect(el).not.toBeNull();
    // Exactly one request for the whole span needed to reach the
    // target (200..204) rather than a series of STEP-sized fetches.
    expect(diffStore.loadBlobRange).toHaveBeenCalledTimes(1);
    expect(diffStore.loadBlobRange).toHaveBeenCalledWith("a/b.go", "deadbeef", 200, 204);
  });

  it("a revealNewLine outside the window does nothing: no fetch, no onrevealed", async () => {
    const onrevealed = vi.fn();
    const { diffStore } = renderRegion({
      position: "middle",
      lineCount: 10,
      gapNewStart: 200,
      gapOldStart: 150,
      revealNewLine: 999, // well outside [200, 209]
      onrevealed,
    });

    expect(diffStore.loadBlobRange).not.toHaveBeenCalled();
    expect(onrevealed).not.toHaveBeenCalled();

    // Give any errant async work a chance to run before re-checking.
    await tick();
    await tick();
    expect(diffStore.loadBlobRange).not.toHaveBeenCalled();
    expect(onrevealed).not.toHaveBeenCalled();
  });

  it('a "bottom" region (unknown extent) expands downward and stops at EOF without calling onrevealed', async () => {
    const onrevealed = vi.fn();
    // Build the diffStore — and install its short-file mock — before
    // the component mounts: CollapsedRegion's reveal effect issues its
    // first fetch synchronously during render, so overriding the mock
    // afterward would be too late and the default (full-response)
    // stub would already have satisfied the request.
    const diffStore = createDiffStore({ client: stubClient() });
    // Simulate a short file: BlobRange clamps at EOF instead of
    // paginating (internal/worktrees/blob.go), so any real short
    // response — regardless of how many lines were asked for —
    // unambiguously means end-of-file. Always return only 3 lines.
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_path: string, _sha: string, start: number) =>
        Promise.resolve(Array.from({ length: 3 }, (_, i) => `line ${start + i}`)),
    );

    const { container } = renderRegion(
      {
        position: "bottom",
        lineCount: 0,
        gapNewStart: 500,
        gapOldStart: 480,
        revealNewLine: 550, // far beyond what this "file" actually contains
        onrevealed,
      },
      diffStore,
    );

    // The collapsed-region bar disappears once bottomExhausted flips
    // true (fullyExpanded === bottomExhausted for a "bottom" region),
    // so its removal is the observable signal that EOF was reached.
    await waitFor(() => {
      expect(container.querySelector(".collapsed-region")).toBeNull();
    });

    expect(onrevealed).not.toHaveBeenCalled();
    // One bulk request determined EOF; confirm it didn't keep firing
    // requests after that (no infinite loop).
    expect(diffStore.loadBlobRange).toHaveBeenCalledTimes(1);
    await tick();
    await tick();
    expect(diffStore.loadBlobRange).toHaveBeenCalledTimes(1);
    expect(onrevealed).not.toHaveBeenCalled();
  });

  it("a null revealNewLine does nothing: no fetch, no onrevealed", async () => {
    const onrevealed = vi.fn();
    const { diffStore } = renderRegion({
      revealNewLine: null,
      onrevealed,
    });

    await tick();
    expect(diffStore.loadBlobRange).not.toHaveBeenCalled();
    expect(onrevealed).not.toHaveBeenCalled();
  });

  it("an undefined revealNewLine (prop omitted entirely) does nothing: no fetch, no onrevealed", async () => {
    const onrevealed = vi.fn();
    const { diffStore } = renderRegion({ onrevealed });

    await tick();
    expect(diffStore.loadBlobRange).not.toHaveBeenCalled();
    expect(onrevealed).not.toHaveBeenCalled();
  });

  it("onrevealed fires exactly once for a target, even though the region expands further afterward", async () => {
    const onrevealed = vi.fn();
    const { getByRole, container, diffStore } = renderRegion({
      position: "middle",
      lineCount: 10,
      gapNewStart: 200,
      gapOldStart: 150,
      revealNewLine: 204,
      onrevealed,
    });

    await waitFor(() => {
      expect(onrevealed).toHaveBeenCalledTimes(1);
    });
    expect(diffStore.loadBlobRange).toHaveBeenCalledTimes(1);

    // Expand the rest of the gap by hand (a plain click) — this grows
    // topCount further, which the reveal effect also reads, so it
    // re-runs. onrevealed must not fire a second time for the same
    // target that already fired it.
    await fireEvent.click(getByRole("button"));
    await waitFor(() => {
      // The whole 10-line gap is now revealed, so the bar is gone.
      expect(container.querySelector(".collapsed-region")).toBeNull();
    });

    expect(onrevealed).toHaveBeenCalledTimes(1);
  });

  it("does not race a fetch already in flight from a click-initiated expand: a reveal target arriving mid-fetch waits rather than issuing a second request (the loading guard)", async () => {
    const onrevealed = vi.fn();
    const diffStore = createDiffStore({ client: stubClient() });
    let callCount = 0;
    let resolveFirst: (() => void) | undefined;
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_path: string, _sha: string, start: number, end: number) => {
        callCount++;
        const lines = Array.from({ length: end - start + 1 }, (_, i) => `line ${start + i}`);
        if (callCount === 1) {
          // Hold the scrub's own fetch open so we can inspect state
          // while it's still in flight.
          return new Promise<string[]>((resolve) => {
            resolveFirst = () => resolve(lines);
          });
        }
        return Promise.resolve(lines);
      },
    );

    const baseProps = {
      position: "middle" as const,
      layout: "unified" as const,
      lineCount: 10,
      owner: "o",
      name: "n",
      number: 1,
      path: "a/b.go",
      sha: "deadbeef",
      gapOldStart: 150,
      gapNewStart: 200,
    };
    const { getByRole, rerender } = renderRegion(baseProps, diffStore);

    // Start a click-initiated expand (this is NOT the scrub gesture —
    // see the dedicated scrub test below for that): a plain click on
    // a "middle" region calls expandStep(), which calls expandTop
    // first — synchronously setting loading = true and issuing the
    // fetch we're holding open. This exercises the reveal effect's
    // `loading` guard specifically.
    await fireEvent.click(getByRole("button"));
    expect(callCount).toBe(1);

    // While that fetch is still in flight, a reveal target for a line
    // inside this region's window (200..209) arrives.
    await rerender({ ...baseProps, revealNewLine: 204, onrevealed });

    // The reveal effect must see loading === true and defer — not
    // start a second, competing fetch while the click-initiated one
    // is pending.
    expect(callCount).toBe(1);
    expect(onrevealed).not.toHaveBeenCalled();

    // Let the held-open fetch resolve. It covers lines 200..204, so
    // the target is now rendered without the reveal effect ever
    // fetching anything itself.
    resolveFirst?.();
    await waitFor(() => {
      expect(onrevealed).toHaveBeenCalledTimes(1);
    });
  });

  it("does not race a fetch already in flight from a real scrub gesture: a reveal target arriving mid-scrub waits on the `flushing` guard rather than issuing a second request", async () => {
    const onrevealed = vi.fn();
    const diffStore = createDiffStore({ client: stubClient() });
    let callCount = 0;
    let resolveFirst: (() => void) | undefined;
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_path: string, _sha: string, start: number, end: number) => {
        callCount++;
        const lines = Array.from({ length: end - start + 1 }, (_, i) => `line ${start + i}`);
        if (callCount === 1) {
          // Hold the scrub's own coalesced fetch open so we can
          // inspect state while it's still in flight.
          return new Promise<string[]>((resolve) => {
            resolveFirst = () => resolve(lines);
          });
        }
        return Promise.resolve(lines);
      },
    );

    const baseProps = {
      position: "middle" as const,
      layout: "unified" as const,
      lineCount: 10,
      owner: "o",
      name: "n",
      number: 1,
      path: "a/b.go",
      sha: "deadbeef",
      gapOldStart: 150,
      gapNewStart: 200,
    };
    const { getByRole, rerender } = renderRegion(baseProps, diffStore);

    // Start a real press-and-hold scrub: pointerdown flips `scrubbing`,
    // then a wheel event past SCRUB_PIXELS_PER_LINE (10) coalesces into
    // a single requestExpandTop() -> flushPending() -> expandTop() call,
    // synchronously setting both `loading` and `flushing` and issuing
    // the fetch we're holding open.
    await fireEvent.pointerDown(getByRole("button"));
    await fireEvent.wheel(getByRole("button"), { deltaY: 100 });
    expect(callCount).toBe(1);

    // While that fetch is still in flight, a reveal target for a line
    // inside this region's window (200..209) arrives.
    await rerender({ ...baseProps, revealNewLine: 204, onrevealed });

    // The reveal effect must see loading/flushing still true and
    // defer — not start a second, competing fetch while the scrub's
    // coalesced fetch is pending.
    expect(callCount).toBe(1);
    expect(onrevealed).not.toHaveBeenCalled();

    // Let the scrub's fetch resolve. It covers lines 200..209 (the
    // whole gap, since a 100px scroll crosses the 10-line threshold
    // once), so the target is now rendered without the reveal effect
    // ever fetching anything itself.
    resolveFirst?.();
    await waitFor(() => {
      expect(onrevealed).toHaveBeenCalledTimes(1);
    });
    expect(callCount).toBe(1);
  });

  it("a 'bottom' region target more than one chunk away converges over several capped fetches, each within the server's line-span cap", async () => {
    const onrevealed = vi.fn();
    const { container, diffStore } = renderRegion({
      position: "bottom",
      lineCount: 0,
      gapNewStart: 1000,
      gapOldStart: 980,
      // 1201 lines away — over twice the 500-line chunk size, so a
      // single uncapped request (the pre-fix behavior) would exceed
      // the server's 2000-line hard cap for some real files, and
      // always takes more than one chunk to satisfy here.
      revealNewLine: 2200,
      onrevealed,
    });

    await waitFor(() => {
      expect(onrevealed).toHaveBeenCalledTimes(1);
    });

    const el = container.querySelector('[data-anchor-line="2200"][data-anchor-side="RIGHT"]');
    expect(el).not.toBeNull();

    // Converged over several requests rather than one all-or-nothing
    // fetch — and critically, no individual fetch's span exceeded the
    // chunk size. This is the assertion that catches the original bug:
    // before the fix, this would have been a single (1000, 2200) call,
    // a 1201-line span the real server would reject.
    const calls = vi.mocked(diffStore.loadBlobRange).mock.calls;
    expect(calls.length).toBeGreaterThan(1);
    for (const [, , start, end] of calls) {
      expect(end - start + 1).toBeLessThanOrEqual(500);
    }
    // The chunks tile the whole span with no gaps or overlaps.
    const totalLines = calls.reduce((sum, [, , start, end]) => sum + (end - start + 1), 0);
    expect(totalLines).toBe(2200 - 1000 + 1);
  });

  describe("window containment boundaries", () => {
    it("the first line of a window is treated as in-window (inclusive lower bound)", async () => {
      const onrevealed = vi.fn();
      const { container, diffStore } = renderRegion({
        position: "middle",
        lineCount: 10,
        gapNewStart: 200,
        gapOldStart: 150,
        revealNewLine: 200, // gapNewStart itself
        onrevealed,
      });

      await waitFor(() => {
        expect(onrevealed).toHaveBeenCalledTimes(1);
      });
      expect(
        container.querySelector('[data-anchor-line="200"][data-anchor-side="RIGHT"]'),
      ).not.toBeNull();
      expect(diffStore.loadBlobRange).toHaveBeenCalledWith("a/b.go", "deadbeef", 200, 200);
    });

    it("the last line of a window is treated as in-window (inclusive upper bound)", async () => {
      const onrevealed = vi.fn();
      const { container, diffStore } = renderRegion({
        position: "middle",
        lineCount: 10,
        gapNewStart: 200,
        gapOldStart: 150,
        revealNewLine: 209, // gapNewStart + lineCount - 1
        onrevealed,
      });

      await waitFor(() => {
        expect(onrevealed).toHaveBeenCalledTimes(1);
      });
      expect(
        container.querySelector('[data-anchor-line="209"][data-anchor-side="RIGHT"]'),
      ).not.toBeNull();
      expect(diffStore.loadBlobRange).toHaveBeenCalledWith("a/b.go", "deadbeef", 209, 209);
    });

    it("one line past the end of a window does nothing: no fetch, no onrevealed", async () => {
      const onrevealed = vi.fn();
      const { diffStore } = renderRegion({
        position: "middle",
        lineCount: 10,
        gapNewStart: 200,
        gapOldStart: 150,
        revealNewLine: 210, // gapNewStart + lineCount, one past the last valid line
        onrevealed,
      });

      await tick();
      await tick();
      expect(diffStore.loadBlobRange).not.toHaveBeenCalled();
      expect(onrevealed).not.toHaveBeenCalled();
    });
  });
});
