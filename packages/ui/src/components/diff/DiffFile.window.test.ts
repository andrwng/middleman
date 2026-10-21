import { cleanup, fireEvent, render, waitFor } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { tick } from "svelte";

vi.mock("../../utils/highlight.js", () => ({
  tokenizeLineDual: () => Promise.resolve([]),
  langFromPath: () => "text",
}));

import DiffFile from "./DiffFile.svelte";
import { createDiffWindow, type DiffWindow } from "./diffWindow.svelte.js";
import type { DiffFile as DiffFileType } from "../../api/types.js";
import type { MiddlemanClient } from "../../types.js";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createReviewThreadsStore } from "../../stores/reviewThreads.svelte.js";
import { createSymbolRefsStore } from "../../stores/symbolRefs.svelte.js";
import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

// Controllable IntersectionObserver: tests move the file between zones by
// firing entries on the observer that owns each margin.
class MockIO {
  static instances: MockIO[] = [];
  readonly callback: IntersectionObserverCallback;
  readonly rootMargin: string;
  disconnected = false;
  constructor(cb: IntersectionObserverCallback, opts?: IntersectionObserverInit) {
    this.callback = cb;
    this.rootMargin = opts?.rootMargin ?? "";
    MockIO.instances.push(this);
  }
  observe(): void {}
  unobserve(): void {}
  disconnect(): void { this.disconnected = true; }
  takeRecords(): IntersectionObserverEntry[] { return []; }
}

type G = { IntersectionObserver?: unknown };
let savedIO: unknown;

// Root height 1000 => mount 2000px, prefetch 4000px, unmount 6000px.
const MOUNT = "2000px 0px";
const PREFETCH = "4000px 0px";
const UNMOUNT = "6000px 0px";

function fire(margin: string, target: Element, isIntersecting: boolean): void {
  const io = MockIO.instances.find((o) => !o.disconnected && o.rootMargin === margin)!;
  io.callback(
    [{
      target,
      isIntersecting,
      intersectionRatio: isIntersecting ? 1 : 0,
      boundingClientRect: { top: 0, bottom: 0 } as DOMRectReadOnly,
      intersectionRect: {} as DOMRectReadOnly,
      rootBounds: { top: 0, bottom: 1000 } as DOMRectReadOnly,
      time: 0,
    } as IntersectionObserverEntry],
    io as unknown as IntersectionObserver,
  );
}

function fileEl(): HTMLElement {
  return document.querySelector<HTMLElement>(".diff-file")!;
}

async function toMount(): Promise<void> {
  fire(UNMOUNT, fileEl(), true);
  fire(PREFETCH, fileEl(), true);
  fire(MOUNT, fileEl(), true);
  await tick();
}

async function toFar(): Promise<void> {
  fire(MOUNT, fileEl(), false);
  fire(PREFETCH, fileEl(), false);
  fire(UNMOUNT, fileEl(), false);
  await tick();
}

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

function makeFile(overrides: Partial<DiffFileType> = {}): DiffFileType {
  return {
    path: "src/foo.ts",
    old_path: "src/foo.ts",
    status: "modified",
    is_binary: false,
    is_whitespace_only: false,
    additions: 3,
    deletions: 1,
    hunks: [{
      old_start: 1,
      old_count: 3,
      new_start: 1,
      new_count: 5,
      lines: [
        { type: "context", content: "line 1", old_num: 1, new_num: 1 },
        { type: "delete", content: "old line", old_num: 2 },
        { type: "add", content: "new line", new_num: 2 },
      ],
    }],
    ...overrides,
  };
}

let counter = 0;
let win: DiffWindow;

type DiffStore = ReturnType<typeof createDiffStore>;

function renderWithWindow(
  file: DiffFileType,
  patchStore?: (s: DiffStore) => DiffStore,
) {
  const base = createDiffStore({ client: stubClient() });
  const diffStore = patchStore ? patchStore(base) : base;
  const owner = `win-owner-${++counter}`;
  const rendered = render(DiffFile, {
    props: { file, owner, name: "n", number: 1, diffWindow: win },
    context: new Map<symbol, unknown>([
      [
        STORES_KEY,
        {
          diff: diffStore,
          ai: createAIStore(),
          reviewThreads: createReviewThreadsStore({ client: stubClient() }),
          symbolRefs: createSymbolRefsStore({ client: stubClient() }),
          detail: {
            getReviewCommentsByFilePath: () => new Map<string, PublishedReviewComment[]>(),
            getReviewCommentAnchor: () => undefined,
            getReviewCommentPlacement: () => null,
            getHiddenRootSet: () => new Set<number>(),
            isShowingHiddenThreads: () => false,
            getHiddenThreadCount: () => 0,
            hideReviewThread: () => Promise.resolve(),
            unhideReviewThread: () => Promise.resolve(),
            getReviewCommentRootForPlatformID: (pid: number) => pid,
          },
        },
      ],
    ]),
  });
  return { ...rendered, diffStore, owner };
}

beforeEach(() => {
  MockIO.instances = [];
  savedIO = (globalThis as G).IntersectionObserver;
  (globalThis as G).IntersectionObserver = MockIO;
  Element.prototype.scrollIntoView = vi.fn();
  const root = document.createElement("div");
  Object.defineProperty(root, "clientHeight", { configurable: true, value: 1000 });
  document.body.appendChild(root);
  win = createDiffWindow(root);
});

afterEach(() => {
  cleanup();
  win.destroy();
  vi.restoreAllMocks();
  (globalThis as G).IntersectionObserver = savedIO;
  document.body.innerHTML = "";
});

function hunkHeader(): Element | null {
  return Array.from(document.querySelectorAll(".hunk-text"))
    .find((el) => /@@ -1,3 \+1,5 @@/.test(el.textContent ?? "")) ?? null;
}

describe("DiffFile with a viewport window", () => {
  it("renders a sized placeholder instead of the body while far from the viewport", () => {
    renderWithWindow(makeFile());
    const ph = document.querySelector<HTMLElement>(".diff-file__placeholder");
    expect(ph).toBeTruthy();
    // (3 additions + 1 deletion + 3 rows x 1 hunk) x 20px fallback row.
    expect(ph!.style.height).toBe("140px");
    expect(hunkHeader()).toBeNull();
    expect(fileEl().dataset.bodyMounted).toBe("false");
  });

  it("uses the remembered height for the placeholder when there is one", () => {
    win.rememberHeight("src/foo.ts", 777);
    renderWithWindow(makeFile());
    expect(document.querySelector<HTMLElement>(".diff-file__placeholder")!.style.height).toBe("777px");
  });

  it("mounts the body once the file enters the mount zone, and unmounts it far away", async () => {
    renderWithWindow(makeFile());
    await toMount();
    expect(hunkHeader()).toBeTruthy();
    expect(document.querySelector(".diff-file__placeholder")).toBeNull();
    expect(fileEl().dataset.bodyMounted).toBe("true");
    expect(win.isBodyMounted("src/foo.ts")).toBe(true);

    await toFar();
    expect(hunkHeader()).toBeNull();
    expect(document.querySelector(".diff-file__placeholder")).toBeTruthy();
    expect(win.isBodyMounted("src/foo.ts")).toBe(false);
  });

  it("measures the body before unmounting it, so the placeholder keeps its height", async () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (this: HTMLElement) {
      // Only a still-attached body measures: proves the height is read
      // before the DOM removal, not after.
      return this.isConnected && this.classList.contains("file-content") ? 333 : 0;
    });
    renderWithWindow(makeFile());
    await toMount();
    await toFar();
    expect(win.height("src/foo.ts")).toBe(333);
    expect(document.querySelector<HTMLElement>(".diff-file__placeholder")!.style.height).toBe("333px");
  });

  it("does not mount the body in the prefetch zone", async () => {
    renderWithWindow(makeFile());
    fire(UNMOUNT, fileEl(), true);
    fire(PREFETCH, fileEl(), true);
    await tick();
    expect(hunkHeader()).toBeNull();
  });

  it("pins the file while a comment composer is open, so its draft survives scrolling far away", async () => {
    renderWithWindow(makeFile());
    await toMount();

    const wrap = document.querySelector<HTMLElement>('.line-wrap[data-anchor-line="2"][data-anchor-side="RIGHT"]')!;
    const btn = wrap.querySelector<HTMLButtonElement>(".add-comment-btn")!;
    await fireEvent.mouseDown(btn);
    await fireEvent.click(btn);
    const textarea = document.querySelector<HTMLTextAreaElement>(".composer__textarea")!;
    expect(textarea).toBeTruthy();
    await fireEvent.input(textarea, { target: { value: "half-written thought" } });

    expect(win.isPinned("src/foo.ts")).toBe(true);
    await toFar();

    const after = document.querySelector<HTMLTextAreaElement>(".composer__textarea");
    expect(after).toBe(textarea);
    expect(after!.value).toBe("half-written thought");

    // Closing the composer releases the pin, and the far body unmounts.
    const cancel = Array.from(document.querySelectorAll("button"))
      .find((b) => b.textContent?.trim() === "Cancel")!;
    await fireEvent.click(cancel);
    await tick();
    expect(win.isPinned("src/foo.ts")).toBe(false);
    expect(hunkHeader()).toBeNull();
  });

  it("pins the file while a pending draft is being edited, so the edit survives scrolling far away", async () => {
    const { diffStore, owner } = renderWithWindow(makeFile());
    diffStore.setActivePR(owner, "n", 1);
    diffStore.addDraftComment({ path: "src/foo.ts", line: 2, side: "RIGHT", commitSha: "", body: "original" });
    await toMount();

    await fireEvent.click(document.querySelector<HTMLButtonElement>('[title="Edit pending comment"]')!);
    const textarea = document.querySelector<HTMLTextAreaElement>(".pending textarea")!;
    expect(textarea).toBeTruthy();
    textarea.focus();
    await fireEvent.input(textarea, { target: { value: "edited but unsaved" } });
    await new Promise((r) => setTimeout(r, 0));
    expect(win.isPinned("src/foo.ts")).toBe(true);

    await toFar();
    const after = document.querySelector<HTMLTextAreaElement>(".pending textarea");
    expect(after).toBe(textarea);
    expect(after!.value).toBe("edited but unsaved");
  });

  it("releases the editor pin once the editor is blurred and empty", async () => {
    const { diffStore, owner } = renderWithWindow(makeFile());
    diffStore.setActivePR(owner, "n", 1);
    diffStore.addDraftComment({ path: "src/foo.ts", line: 2, side: "RIGHT", commitSha: "", body: "original" });
    await toMount();

    await fireEvent.click(document.querySelector<HTMLButtonElement>('[title="Edit pending comment"]')!);
    const textarea = document.querySelector<HTMLTextAreaElement>(".pending textarea")!;
    textarea.focus();
    await fireEvent.input(textarea, { target: { value: "" } });
    await new Promise((r) => setTimeout(r, 0));
    // Focused, though empty: still pinned.
    expect(win.isPinned("src/foo.ts")).toBe(true);

    textarea.blur();
    await new Promise((r) => setTimeout(r, 0));
    expect(win.isPinned("src/foo.ts")).toBe(false);
    await toFar();
    expect(hunkHeader()).toBeNull();
  });

  it("releases the editor pin when the editor is closed", async () => {
    const { diffStore, owner } = renderWithWindow(makeFile());
    diffStore.setActivePR(owner, "n", 1);
    diffStore.addDraftComment({ path: "src/foo.ts", line: 2, side: "RIGHT", commitSha: "", body: "original" });
    await toMount();

    await fireEvent.click(document.querySelector<HTMLButtonElement>('[title="Edit pending comment"]')!);
    const textarea = document.querySelector<HTMLTextAreaElement>(".pending textarea")!;
    await fireEvent.input(textarea, { target: { value: "typed" } });
    await new Promise((r) => setTimeout(r, 0));
    expect(win.isPinned("src/foo.ts")).toBe(true);

    const cancel = Array.from(document.querySelectorAll<HTMLButtonElement>(".pending button"))
      .find((b) => b.textContent?.trim() === "Cancel")!;
    await fireEvent.click(cancel);
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector(".pending textarea")).toBeNull();
    expect(win.isPinned("src/foo.ts")).toBe(false);
  });

  it("pins the file while the document selection is inside it", async () => {
    renderWithWindow(makeFile());
    await toMount();
    const wrap = document.querySelector<HTMLElement>(".line-wrap")!;
    const sel = {
      isCollapsed: false,
      rangeCount: 1,
      anchorNode: wrap,
      focusNode: wrap,
      toString: () => "x y",
      getRangeAt: () => ({ getBoundingClientRect: () => ({ width: 0, height: 0 }) }),
      removeAllRanges: () => {},
    };
    vi.spyOn(window, "getSelection").mockReturnValue(sel as unknown as Selection);
    document.dispatchEvent(new Event("selectionchange"));
    await tick();
    expect(win.isPinned("src/foo.ts")).toBe(true);

    await toFar();
    expect(hunkHeader()).toBeTruthy();

    sel.isCollapsed = true;
    sel.rangeCount = 0;
    document.dispatchEvent(new Event("selectionchange"));
    await tick();
    expect(win.isPinned("src/foo.ts")).toBe(false);
    expect(hunkHeader()).toBeNull();
  });

  it("pins the file while one of its collapsed regions is expanded", async () => {
    const { diffStore } = renderWithWindow(makeFile());
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_p: string, _s: string, start: number, end: number) =>
        Promise.resolve(Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => `line ${start + i}`)),
    );
    await toMount();
    diffStore.requestRevealLine("src/foo.ts", 10);
    await waitFor(() => {
      expect(document.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]')).not.toBeNull();
    });
    expect(win.isPinned("src/foo.ts")).toBe(true);
    await toFar();
    expect(document.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]')).not.toBeNull();
  });

  it("only auto-marks a file viewed after its body was mounted and seen", async () => {
    const { diffStore, owner } = renderWithWindow(makeFile());
    // Reviewed-file marks are keyed by the active PR.
    diffStore.setActivePR(owner, "n", 1);
    const visible = MockIO.instances.find((o) => !o.disconnected && o.rootMargin === "0px 0px")!;
    const fireVisible = (isIntersecting: boolean, top: number, bottom: number): void => {
      visible.callback(
        [{
          target: fileEl(),
          isIntersecting,
          intersectionRatio: isIntersecting ? 1 : 0,
          boundingClientRect: { top, bottom } as DOMRectReadOnly,
          intersectionRect: {} as DOMRectReadOnly,
          rootBounds: { top: 0, bottom: 1000 } as DOMRectReadOnly,
          time: 0,
        } as IntersectionObserverEntry],
        visible as unknown as IntersectionObserver,
      );
    };

    // Seen and scrolled past while only a placeholder: not viewed.
    fireVisible(true, 100, 300);
    await tick();
    fireVisible(false, -400, -10);
    await tick();
    expect(diffStore.isFileReviewed("src/foo.ts")).toBe(false);

    // Seen with its body mounted, then scrolled past: viewed.
    await toMount();
    fireVisible(true, 100, 300);
    await tick();
    fireVisible(false, -400, -10);
    await tick();
    expect(diffStore.isFileReviewed("src/foo.ts")).toBe(true);
  });
});

describe("DiffFile lazy hunk states with a viewport window", () => {
  function lazyStore(state: "idle" | "loading" | "error", requestHunks: () => void) {
    return (s: DiffStore): DiffStore => ({
      ...s,
      isLazy: () => true,
      getFileHunkState: () => state,
      requestHunks,
    });
  }

  it("requests hunks once the file enters the prefetch zone, not before", async () => {
    const requestHunks = vi.fn();
    renderWithWindow(makeFile({ hunks: [] }), lazyStore("idle", requestHunks));
    await tick();
    expect(requestHunks).not.toHaveBeenCalled();
    fire(UNMOUNT, fileEl(), true);
    fire(PREFETCH, fileEl(), true);
    await tick();
    expect(requestHunks).toHaveBeenCalledWith("src/foo.ts");
  });

  it("shows a spinner placeholder while hunks load, even in the mount zone", async () => {
    renderWithWindow(makeFile({ hunks: [] }), lazyStore("loading", vi.fn()));
    await toMount();
    const ph = document.querySelector(".diff-file__placeholder");
    expect(ph).toBeTruthy();
    expect(ph!.querySelector(".diff-file__spinner")).toBeTruthy();
    expect(fileEl().dataset.bodyMounted).toBe("false");
  });

  it("shows an error with a retry button that re-requests hunks", async () => {
    const requestHunks = vi.fn();
    renderWithWindow(makeFile({ hunks: [] }), lazyStore("error", requestHunks));
    await toMount();
    const retry = Array.from(document.querySelectorAll("button"))
      .find((b) => b.textContent?.trim() === "Retry")!;
    expect(retry).toBeTruthy();
    expect(document.querySelector(".diff-file__placeholder")!.textContent).toContain("Failed to load diff");
    await fireEvent.click(retry);
    expect(requestHunks).toHaveBeenCalledWith("src/foo.ts");
  });
});
