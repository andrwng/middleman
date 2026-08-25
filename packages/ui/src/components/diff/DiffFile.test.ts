import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { tick } from "svelte";

// Mock highlight utils to avoid loading Shiki in tests.
vi.mock("../../utils/highlight.js", () => ({
  tokenizeLineDual: () => Promise.resolve([]),
  langFromPath: () => "text",
}));

// jsdom does not ship IntersectionObserver; install a stub that reports the
// observed element as visible immediately so the tokenization effect actually
// runs under test. The original global (if any) is saved and restored after
// the suite so it does not leak into sibling test files.
type GlobalWithIO = { IntersectionObserver?: unknown };
let originalIntersectionObserver: unknown;
let originalIntersectionObserverExisted = false;

beforeAll(() => {
  originalIntersectionObserverExisted = "IntersectionObserver" in globalThis;
  originalIntersectionObserver = (globalThis as GlobalWithIO).IntersectionObserver;
  class IntersectionObserverStub {
    private readonly callback: IntersectionObserverCallback;
    root: Element | null = null;
    rootMargin = "";
    thresholds: readonly number[] = [];
    constructor(callback: IntersectionObserverCallback) {
      this.callback = callback;
    }
    observe(target: Element): void {
      // Report the element as visible immediately so viewport-gated work
      // (like tokenization in DiffFile) actually executes under test.
      const entry = {
        isIntersecting: true,
        intersectionRatio: 1,
        target,
        boundingClientRect: {} as DOMRectReadOnly,
        intersectionRect: {} as DOMRectReadOnly,
        rootBounds: null,
        time: 0,
      } as IntersectionObserverEntry;
      this.callback([entry], this as unknown as IntersectionObserver);
    }
    unobserve(): void {}
    disconnect(): void {}
    takeRecords(): IntersectionObserverEntry[] { return []; }
  }
  (globalThis as GlobalWithIO).IntersectionObserver = IntersectionObserverStub;
});

afterAll(() => {
  if (originalIntersectionObserverExisted) {
    (globalThis as GlobalWithIO).IntersectionObserver = originalIntersectionObserver;
  } else {
    delete (globalThis as GlobalWithIO).IntersectionObserver;
  }
});

import DiffFile from "./DiffFile.svelte";
import type { DiffFile as DiffFileType } from "../../api/types.js";
import type { MiddlemanClient } from "../../types.js";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createReviewThreadsStore } from "../../stores/reviewThreads.svelte.js";
import { createSymbolRefsStore } from "../../stores/symbolRefs.svelte.js";

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

// Use unique owner per test so module-level collapsed state doesn't leak.
let testCounter = 0;
function uniqueOwner(): string {
  return `test-owner-${++testCounter}`;
}

// renderDiffFile returns the render result plus the store instances it
// built for the component's context, so tests that need to reach past
// the DOM (seed a commit SHA, spy on a store method) can do so without
// re-deriving what was passed in.
function renderDiffFile(file: DiffFileType) {
  const owner = uniqueOwner();
  const diffStore = createDiffStore({ client: stubClient() });
  const symbolRefsStore = createSymbolRefsStore({ client: stubClient() });
  const rendered = render(DiffFile, {
    props: { file, owner, name: "n", number: 1 },
    context: new Map<symbol, unknown>([
      [
        STORES_KEY,
        {
          diff: diffStore,
          ai: createAIStore(),
          reviewThreads: createReviewThreadsStore({ client: stubClient() }),
          symbolRefs: symbolRefsStore,
          detail: {
            getReviewCommentsByFilePath: () => new Map(),
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
  return { ...rendered, owner, diffStore, symbolRefsStore };
}

describe("DiffFile", () => {
  afterEach(() => {
    cleanup();
  });

  it("labels a copied file with its copy source", () => {
    renderDiffFile(makeFile({
      status: "copied",
      path: "src/v/kafka/protocol/consumer_group_describe.h",
      old_path: "src/v/kafka/protocol/describe_redpanda_roles.h",
    }));
    expect(
      screen.getByText("Copied from src/v/kafka/protocol/describe_redpanda_roles.h"),
    ).toBeTruthy();
  });

  it("labels a renamed file with its old path", () => {
    renderDiffFile(makeFile({ status: "renamed", path: "src/new.ts", old_path: "src/old.ts" }));
    expect(screen.getByText("Renamed from src/old.ts")).toBeTruthy();
  });

  it("shows no copy/rename label for a plain modified file", () => {
    renderDiffFile(makeFile());
    expect(screen.queryByText(/^(Copied|Renamed) from/)).toBeNull();
  });

  it("shows no label for a modified file even when old_path differs (status-gated)", () => {
    renderDiffFile(makeFile({ status: "modified", path: "src/foo.ts", old_path: "src/other.ts" }));
    expect(screen.queryByText(/^(Copied|Renamed) from/)).toBeNull();
  });

  it("renders file content when not collapsed", () => {
    renderDiffFile(makeFile());

    expect(screen.getByText("src/foo.ts")).toBeTruthy();
    expect(screen.getByText(/@@ -1,3 \+1,5 @@/)).toBeTruthy();
  });

  it("hides content after clicking the header to collapse", async () => {
    renderDiffFile(makeFile());

    const header = screen.getByTitle("Collapse file");
    await fireEvent.click(header);

    expect(document.querySelector(".file-content")).toBeNull();
  });

  it("shows content again after toggling collapse twice", async () => {
    renderDiffFile(makeFile());

    const header = screen.getByTitle("Collapse file");
    await fireEvent.click(header);

    const expandHeader = screen.getByTitle("Expand file");
    await fireEvent.click(expandHeader);

    const content = document.querySelector(".file-content");
    expect(content?.classList.contains("file-content--collapsed")).toBe(false);
  });
});

// window.getSelection() returns the DOM Selection interface, which carries
// ~20 members (addRange, collapse, extend, setBaseAndExtent, modify, ...)
// that DiffFile never reads. This stub type covers exactly the surface
// computeSelectionRange / computeSymbolSelection / updateToolbarPosition
// touch; the one cast to Selection lives at the call site that hands it to
// the mocked global, not scattered through the assertions below.
interface SelectionStub {
  isCollapsed: boolean;
  rangeCount: number;
  anchorNode: Node | null;
  focusNode: Node | null;
  toString(): string;
  getRangeAt(index: number): { getBoundingClientRect(): DOMRect };
}

// Drives a fake text selection: anchorNode/focusNode must be real nodes
// already attached inside the rendered component (a .line-wrap element
// works directly, since nearestLineWrap walks up from whatever node it is
// given). `text` stands in for what a real Selection.toString() would
// return for that range — decoupled from actual DOM content because the
// tokenizeLineDual mock above means rendered code lines carry no text
// nodes to select from.
function stubSelection(opts: { anchorNode: Node; focusNode: Node; text: string }): void {
  const rect: DOMRect = {
    bottom: 0, height: 0, left: 0, right: 0, top: 0, width: 0, x: 0, y: 0,
    toJSON: () => ({}),
  };
  const sel: SelectionStub = {
    isCollapsed: false,
    rangeCount: 1,
    anchorNode: opts.anchorNode,
    focusNode: opts.focusNode,
    toString: () => opts.text,
    getRangeAt: () => ({ getBoundingClientRect: () => rect }),
  };
  vi.spyOn(window, "getSelection").mockReturnValue(sel as unknown as Selection);
}

// DiffFile's own selectionchange listener runs synchronously off the
// dispatched event, but the $state writes it makes only reach the DOM
// after a tick.
async function fireSelectionChange(): Promise<void> {
  document.dispatchEvent(new Event("selectionchange"));
  await tick();
}

describe("DiffFile selection toolbar: Refs affordance", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('shows a "Refs" button for a single-line selection of a bare symbol', async () => {
    const { diffStore } = renderDiffFile(makeFile());
    // A resolved sha so this test exercises only the selection gate, not
    // the sha-readiness gate covered separately below.
    await diffStore.selectCommit("deadbeef");
    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    expect(wrap).toBeTruthy();
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "fooBar" });
    await fireSelectionChange();

    expect(screen.getByTitle("Find other references to this symbol")).toBeTruthy();
  });

  it("shows no Refs button for an otherwise-valid symbol selection while head scope has no resolved sha yet", async () => {
    // renderDiffFile's diffStore starts in head scope with no commits
    // loaded, so currentCommitSha() is "" until loadCommits() resolves --
    // exactly the window where the button must not be offered, since
    // clicking it would send a request the server rejects with "sha is
    // required".
    renderDiffFile(makeFile());
    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "fooBar" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Find other references to this symbol")).toBeNull();
    // The toolbar itself must not render as an empty box either -- there
    // is nothing else (no Comment/Ask, since this is a single-line
    // selection) that would justify it appearing on its own.
    expect(screen.queryByRole("toolbar")).toBeNull();
  });

  it("the Refs button appears once a sha becomes available, for a selection made while it was still pending", async () => {
    const { diffStore } = renderDiffFile(makeFile());
    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "fooBar" });
    await fireSelectionChange();
    expect(screen.queryByTitle("Find other references to this symbol")).toBeNull();

    // Stands in for loadCommits() resolving: currentCommitSha() becomes
    // non-empty without the selection itself changing, and the button
    // should reactively become available.
    await diffStore.selectCommit("deadbeef");

    expect(screen.getByTitle("Find other references to this symbol")).toBeTruthy();
  });

  it("hides Comment and Ask for that same single-line symbol selection (regression guard: their gate stays multi-line-only)", async () => {
    renderDiffFile(makeFile());
    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "fooBar" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Comment on the selected lines")).toBeNull();
    expect(screen.queryByTitle("Ask Claude about the selected lines")).toBeNull();
  });

  it("still shows Comment and Ask for a multi-line selection, and shows no Refs button", async () => {
    renderDiffFile(makeFile());
    // Context line 1 and the add line 2 are both anchored on the RIGHT
    // side (see anchorFor), so this is a valid multi-line same-side range.
    const startWrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="1"][data-anchor-side="RIGHT"]',
    );
    const endWrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="RIGHT"]',
    );
    expect(startWrap).toBeTruthy();
    expect(endWrap).toBeTruthy();
    stubSelection({ anchorNode: startWrap!, focusNode: endWrap!, text: "line 1\nnew line" });
    await fireSelectionChange();

    expect(screen.getByTitle("Comment on the selected lines")).toBeTruthy();
    expect(screen.getByTitle("Ask Claude about the selected lines")).toBeTruthy();
    expect(screen.queryByTitle("Find other references to this symbol")).toBeNull();
  });

  it("shows no Refs button when a single-line selection contains whitespace", async () => {
    renderDiffFile(makeFile());
    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "foo bar" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Find other references to this symbol")).toBeNull();
  });

  it("shows no Refs button when a single-line selection spans two different sides", async () => {
    renderDiffFile(makeFile());
    // The delete line and the add line both carry line number 2 (old_num
    // and new_num respectively) but resolve to different .line-wrap
    // elements on different sides. A same .line-wrap check must reject
    // this pair on its own; a buggy implementation that compared only the
    // anchorLine VALUE (ignoring side, or ignoring element identity) would
    // wrongly accept it.
    const leftWrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    const rightWrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="RIGHT"]',
    );
    expect(leftWrap).toBeTruthy();
    expect(rightWrap).toBeTruthy();
    stubSelection({ anchorNode: leftWrap!, focusNode: rightWrap!, text: "someSymbol" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Find other references to this symbol")).toBeNull();
  });

  it("calls symbolRefs.search with the trimmed symbol and the SHA from currentCommitSha() when Refs is clicked", async () => {
    const { owner, diffStore, symbolRefsStore } = renderDiffFile(makeFile());
    await diffStore.selectCommit("deadbeef");
    const searchSpy = vi.spyOn(symbolRefsStore, "search").mockResolvedValue(undefined);

    const wrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="2"][data-anchor-side="LEFT"]',
    );
    // Padded with whitespace so this also exercises the "trimmed" half of
    // the requirement: the store must see "fooBar", not "  fooBar  ".
    stubSelection({ anchorNode: wrap!, focusNode: wrap!, text: "  fooBar  " });
    await fireSelectionChange();

    await fireEvent.click(screen.getByTitle("Find other references to this symbol"));

    expect(searchSpy).toHaveBeenCalledWith(owner, "n", 1, "deadbeef", "fooBar");
  });
});

describe("DiffFile reveal-and-jump: plumbs a store reveal target to its collapsed regions", () => {
  beforeEach(() => {
    // jsdom: scrollIntoView is not implemented (see
    // scrollToDiffLine.test.ts) — onRegionRevealed's finishing flash
    // calls it via flashDiffLine.
    Element.prototype.scrollIntoView = vi.fn();
  });

  afterEach(() => {
    cleanup();
  });

  it("hands the target to the containing region and consumes it from the store once revealed", async () => {
    const { container, diffStore } = renderDiffFile(makeFile());
    // Stub loadBlobRange the same way CollapsedRegion.test.ts does, so
    // the region's expansion resolves without a real network call.
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_path: string, _sha: string, start: number, end: number) =>
        Promise.resolve(
          Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => `line ${start + i}`),
        ),
    );

    // makeFile()'s only hunk ends at new_start(1) + new_count(5) = 6,
    // so the file's bottom CollapsedRegion's gap starts at new-line 6
    // and line 10 sits inside it, unrendered until revealed.
    expect(container.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]')).toBeNull();

    diffStore.requestRevealLine("src/foo.ts", 10);
    await tick();

    await waitFor(() => {
      const el = container.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]');
      expect(el).not.toBeNull();
    });
    // The reveal is complete: DiffFile's onrevealed handler consumed
    // the store's target so no other file's region acts on it again.
    expect(diffStore.getRevealTarget()).toBeNull();
  });

  it("ignores a reveal target aimed at a different file: no fetch, target left for its own file to consume", async () => {
    const { container, diffStore } = renderDiffFile(makeFile());
    const loadBlobRangeSpy = vi.spyOn(diffStore, "loadBlobRange");

    diffStore.requestRevealLine("some/other/file.ts", 10);
    await tick();
    await tick();

    expect(loadBlobRangeSpy).not.toHaveBeenCalled();
    expect(container.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]')).toBeNull();
    // This file didn't touch a target that isn't its own — it's still
    // there, unconsumed, for the right file's DiffFile to pick up.
    expect(diffStore.getRevealTarget()).toEqual({ path: "some/other/file.ts", line: 10 });
  });
});

// A revealed context line (rendered by CollapsedRegion via DiffLine) carries
// data-anchor-line/data-anchor-side just like a hunk's .line-wrap does, so
// nearestLineWrap resolves it too — but no composer ever mounts for it (only
// the hunk loops render composers). computeSelectionRange must therefore
// reject any range touching one, while computeSymbolSelection (single-line
// only) is unaffected: a symbol search anchored on a revealed line still
// works end to end.
describe("DiffFile selection toolbar: revealed context lines are not composer anchors", () => {
  beforeEach(() => {
    // jsdom: scrollIntoView is not implemented; unused by these tests but
    // exercised transitively by the reveal machinery's finishing flash.
    Element.prototype.scrollIntoView = vi.fn();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  // Reveals new-side lines 6..10 in the bottom CollapsedRegion's gap in one
  // pass (see the "hands the target to the containing region..." test above
  // for the arithmetic) so a test can select among them.
  async function revealBottomGapLines(
    diffStore: ReturnType<typeof createDiffStore>,
  ): Promise<void> {
    vi.spyOn(diffStore, "loadBlobRange").mockImplementation(
      (_path: string, _sha: string, start: number, end: number) =>
        Promise.resolve(
          Array.from({ length: Math.max(0, end - start + 1) }, (_, i) => `line ${start + i}`),
        ),
    );
    diffStore.requestRevealLine("src/foo.ts", 10);
    await tick();
    await waitFor(() => {
      expect(
        document.querySelector('[data-anchor-line="10"][data-anchor-side="RIGHT"]'),
      ).not.toBeNull();
    });
  }

  it("shows no Comment/Ask toolbar for a selection spanning two revealed gap lines", async () => {
    const { diffStore } = renderDiffFile(makeFile());
    await revealBottomGapLines(diffStore);

    const startWrap = document.querySelector<HTMLElement>(
      '[data-anchor-line="7"][data-anchor-side="RIGHT"]',
    );
    const endWrap = document.querySelector<HTMLElement>(
      '[data-anchor-line="9"][data-anchor-side="RIGHT"]',
    );
    expect(startWrap).toBeTruthy();
    expect(endWrap).toBeTruthy();
    // Both are revealed CollapsedRegion lines, neither a real .line-wrap.
    expect(startWrap!.classList.contains("line-wrap")).toBe(false);
    expect(endWrap!.classList.contains("line-wrap")).toBe(false);

    stubSelection({ anchorNode: startWrap!, focusNode: endWrap!, text: "line 7\nline 8\nline 9" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Comment on the selected lines")).toBeNull();
    expect(screen.queryByTitle("Ask Claude about the selected lines")).toBeNull();
  });

  it("shows no Comment/Ask toolbar for a selection from a real hunk line into a revealed gap line", async () => {
    // The worse variant: one end is a genuine .line-wrap and would pass a
    // check that only inspected the anchor (or only the focus) end.
    const { diffStore } = renderDiffFile(makeFile());
    await revealBottomGapLines(diffStore);

    const hunkWrap = document.querySelector<HTMLElement>(
      '.line-wrap[data-anchor-line="1"][data-anchor-side="RIGHT"]',
    );
    const gapWrap = document.querySelector<HTMLElement>(
      '[data-anchor-line="7"][data-anchor-side="RIGHT"]',
    );
    expect(hunkWrap).toBeTruthy();
    expect(gapWrap).toBeTruthy();
    expect(gapWrap!.classList.contains("line-wrap")).toBe(false);

    stubSelection({ anchorNode: hunkWrap!, focusNode: gapWrap!, text: "line 1\n...\nline 7" });
    await fireSelectionChange();

    expect(screen.queryByTitle("Comment on the selected lines")).toBeNull();
    expect(screen.queryByTitle("Ask Claude about the selected lines")).toBeNull();
  });

  it("still shows the Refs button for a single-line symbol selection on a revealed gap line", async () => {
    const { diffStore } = renderDiffFile(makeFile());
    // A resolved sha so this test exercises only the revealed-gap-line
    // selection gate, not the sha-readiness gate covered separately above.
    await diffStore.selectCommit("deadbeef");
    await revealBottomGapLines(diffStore);

    const gapWrap = document.querySelector<HTMLElement>(
      '[data-anchor-line="8"][data-anchor-side="RIGHT"]',
    );
    expect(gapWrap).toBeTruthy();
    expect(gapWrap!.classList.contains("line-wrap")).toBe(false);

    stubSelection({ anchorNode: gapWrap!, focusNode: gapWrap!, text: "fooBar" });
    await fireSelectionChange();

    expect(screen.getByTitle("Find other references to this symbol")).toBeTruthy();
    expect(screen.queryByTitle("Comment on the selected lines")).toBeNull();
    expect(screen.queryByTitle("Ask Claude about the selected lines")).toBeNull();
  });
});
