import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte";
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
}

function renderRegion(props: Partial<RegionProps> = {}) {
  const diffStore = stubbedDiffStore();
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
