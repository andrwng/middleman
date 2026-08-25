import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { STORES_KEY } from "../../context.js";
import { createSymbolRefsStore } from "../../stores/symbolRefs.svelte.js";
import type { SymbolHit, SymbolRefsStatus } from "../../stores/symbolRefs.svelte.js";
import type { MiddlemanClient } from "../../types.js";

// scrollToDiffLine.ts is mocked wholesale so row-click tests can assert on
// exactly what the component passed it, without exercising the real DOM
// jump logic (which is covered by scrollToDiffLine.test.ts already). The
// mock's second parameter is typed as the real DiffJumpDeps (a type-only
// import, unaffected by the vi.mock below) rather than `unknown`, so
// `.mock.calls[0]` comes back correctly typed and tests can call into the
// deps without an unsafe cast. The return type is annotated as the full
// DiffJumpOutcome union (not narrowed via `as const`) so a test can
// mockResolvedValueOnce("missing"/"pending") without a type error.
const scrollToDiffLineMock = vi.fn(
  async (
    _target: { path: string; line: number },
    _deps: DiffJumpDeps,
  ): Promise<DiffJumpOutcome> => "line",
);
vi.mock("./scrollToDiffLine.js", () => ({
  scrollToDiffLine: (target: { path: string; line: number }, deps: DiffJumpDeps) =>
    scrollToDiffLineMock(target, deps),
}));

import SymbolRefsGutter from "./SymbolRefsGutter.svelte";
import type { DiffJumpDeps, DiffJumpOutcome } from "./scrollToDiffLine.js";

function hit(over: Partial<SymbolHit> = {}): SymbolHit {
  return { path: "a.go", line: 1, text: "ref line", kind: "reference", ...over };
}

interface FakeStoreOverrides {
  query?: string;
  hits?: SymbolHit[];
  inPrTotal?: number;
  outsidePrTotal?: number;
  truncated?: boolean;
  status?: SymbolRefsStatus;
  error?: string | null;
  // Paths the fake diff store reports as part of the currently
  // rendered diff (backs diffStore.getDiff()?.files). Defaults to
  // every hit's own path, so tests that don't care about the
  // "not in this view" marker never see it fire by surprise.
  diffFiles?: string[];
}

function fakeSymbolRefsStore(overrides: FakeStoreOverrides = {}) {
  const {
    query = "Foo",
    hits = [],
    inPrTotal = hits.length,
    outsidePrTotal = 0,
    truncated = false,
    status = "ready",
    error = null,
  } = overrides;
  return {
    getQuery: () => query,
    getHits: () => hits,
    getInPrTotal: () => inPrTotal,
    getOutsidePrTotal: () => outsidePrTotal,
    isTruncated: () => truncated,
    getStatus: () => status,
    getError: () => error,
    isActive: () => status !== "idle",
    search: vi.fn(async () => {}),
    close: vi.fn(),
  };
}

function fakeDiffStore(files: string[]) {
  return {
    isFileCollapsed: vi.fn(() => false),
    toggleFileCollapsed: vi.fn(),
    requestRevealLine: vi.fn(),
    consumeRevealTarget: vi.fn(),
    getDiff: vi.fn(() => ({ files: files.map((path) => ({ path })) })),
  };
}

function renderGutter(overrides: FakeStoreOverrides = {}) {
  const symbolRefsStore = fakeSymbolRefsStore(overrides);
  const diffFiles = overrides.diffFiles ?? (overrides.hits ?? []).map((h) => h.path);
  const diffStore = fakeDiffStore(diffFiles);
  const rendered = render(SymbolRefsGutter, {
    props: { owner: "o", name: "n", number: 1, width: 320 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { symbolRefs: symbolRefsStore, diff: diffStore }],
    ]),
  });
  return { ...rendered, symbolRefsStore, diffStore };
}

// stubClient drives a real createSymbolRefsStore for the one test that
// needs genuine reactivity (the comments/strings toggle resetting across
// two real searches) rather than a static fake.
function stubClient(responses: unknown[]): MiddlemanClient {
  let i = 0;
  return {
    GET: vi.fn(async () => {
      const data = responses[Math.min(i, responses.length - 1)];
      i++;
      return { data, error: undefined };
    }),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

afterEach(() => {
  cleanup();
  scrollToDiffLineMock.mockClear();
});

describe("SymbolRefsGutter", () => {
  it("exposes itself as a labelled landmark for assistive tech", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1 });
    expect(screen.getByRole("complementary", { name: "Symbol references" })).toBeTruthy();
  });

  it("renders the query and the in-PR count in the header", () => {
    renderGutter({ query: "Frobnicate", hits: [hit()], inPrTotal: 7 });
    expect(screen.getByText("Frobnicate")).toBeTruthy();
    expect(screen.getByText("7")).toBeTruthy();
  });

  it("groups rows by file, preserving the store's order", () => {
    const hits = [
      hit({ path: "b.go", line: 5, text: "ref in b" }),
      hit({ path: "b.go", line: 9, text: "ref2 in b" }),
      hit({ path: "a.go", line: 2, text: "ref in a" }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: hits.length });
    const groupPaths = Array.from(
      container.querySelectorAll(".symref-group__path"),
    ).map((el) => el.textContent);
    expect(groupPaths).toEqual(["b.go", "a.go"]);
  });

  it("renders the same file as separate groups when the store's order revisits it across kind blocks", () => {
    // Mirrors the server's real sort (kind, then path, then line): a file
    // with both a definition and a reference hit is NOT contiguous in the
    // array, so it must render as two separate groups, not be re-grouped.
    const hits = [
      hit({ path: "a.go", line: 1, kind: "definition", text: "func Foo()" }),
      hit({ path: "b.go", line: 3, kind: "definition", text: "func Bar()" }),
      hit({ path: "a.go", line: 40, kind: "reference", text: "Foo()" }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: hits.length });
    const groupPaths = Array.from(
      container.querySelectorAll(".symref-group__path"),
    ).map((el) => el.textContent);
    expect(groupPaths).toEqual(["a.go", "b.go", "a.go"]);
  });

  it("a definition row and a reference row are distinguishable in the DOM", () => {
    const hits = [
      hit({ path: "a.go", line: 1, text: "func Foo()", kind: "definition" }),
      hit({ path: "a.go", line: 9, text: "Foo()", kind: "reference" }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: hits.length });
    const rows = container.querySelectorAll(".symref-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.className).toContain("symref-row--definition");
    expect(rows[1]?.className).not.toContain("symref-row--definition");
    expect(rows[0]?.textContent).toContain("def");
    expect(rows[1]?.textContent).toContain("ref");
  });

  it("hides comment/string hits initially, reveals them on click, and hides them again on a second click", async () => {
    const hits = [
      hit({ path: "a.go", line: 1, text: "func Foo()", kind: "definition" }),
      hit({ path: "a.go", line: 42, text: "// Foo does a thing", kind: "comment" }),
    ];
    renderGutter({ hits, inPrTotal: hits.length });

    expect(screen.queryByText("// Foo does a thing")).toBeNull();
    const toggle = screen.getByText(/1 in comments\/strings/);
    await fireEvent.click(toggle);
    expect(screen.getByText("// Foo does a thing")).toBeTruthy();
    await fireEvent.click(toggle);
    expect(screen.queryByText("// Foo does a thing")).toBeNull();
  });

  it("renders no comments/strings toggle when there are none", () => {
    renderGutter({ hits: [hit({ kind: "reference" })], inPrTotal: 1 });
    expect(screen.queryByText(/in comments\/strings/)).toBeNull();
  });

  it("shows the outside-PR footer only when there are hits elsewhere, and it is not a button", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1, outsidePrTotal: 0 });
    expect(screen.queryByText(/elsewhere in the repo/)).toBeNull();
    cleanup();

    renderGutter({ hits: [hit()], inPrTotal: 1, outsidePrTotal: 3 });
    const footer = screen.getByText(/\+3 elsewhere in the repo/);
    expect(footer.tagName).not.toBe("BUTTON");
    expect(footer.closest("button")).toBeNull();
  });

  it("shows the truncation note only when the list was capped", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1, truncated: false });
    expect(screen.queryByText(/capped at 500/)).toBeNull();
    cleanup();

    renderGutter({ hits: [hit()], inPrTotal: 1, truncated: true });
    expect(screen.getByText(/capped at 500/)).toBeTruthy();
  });

  it("renders a loading state", () => {
    renderGutter({ status: "loading" });
    expect(screen.getByText(/searching/i)).toBeTruthy();
  });

  it("renders an error state", () => {
    renderGutter({ status: "error", error: "symbol search failed: boom" });
    expect(screen.getByText("symbol search failed: boom")).toBeTruthy();
  });

  it("renders an empty state when there are no hits", () => {
    renderGutter({ status: "ready", hits: [], query: "Frobnicate" });
    expect(screen.getByText(/appears only where it was selected/i)).toBeTruthy();
  });

  it("clicking a row calls scrollToDiffLine with that row's path, line, and correctly-curried deps", async () => {
    const hits = [hit({ path: "pkg/foo.go", line: 42, text: "func Foo()", kind: "definition" })];
    const { diffStore } = renderGutter({ hits, inPrTotal: 1 });

    await fireEvent.click(screen.getByText("func Foo()"));

    expect(scrollToDiffLineMock).toHaveBeenCalledTimes(1);
    const call = scrollToDiffLineMock.mock.calls[0]!;
    expect(call[0].path).toBe("pkg/foo.go");
    expect(call[0].line).toBe(42);

    // Exercise the deps jumpTo built, rather than just trusting they were
    // constructed correctly by inspection: each function must reach the
    // diff store with owner/name/number curried in the right order ahead
    // of the argument(s) the caller supplies. A swapped owner/name (or a
    // dropped number) in SymbolRefsGutter's jumpTo would fail these
    // assertions even though `call[0]` above already looks right.
    const deps = call[1];
    deps.isFileCollapsed("other/path.go");
    expect(diffStore.isFileCollapsed).toHaveBeenCalledWith("o", "n", 1, "other/path.go");

    deps.toggleFileCollapsed("other/path.go");
    expect(diffStore.toggleFileCollapsed).toHaveBeenCalledWith("o", "n", 1, "other/path.go");

    deps.requestRevealLine("other/path.go", 99);
    expect(diffStore.requestRevealLine).toHaveBeenCalledWith("other/path.go", 99);

    deps.clearRevealTarget();
    expect(diffStore.consumeRevealTarget).toHaveBeenCalledTimes(1);
  });

  it("the close button calls symbolRefs.close()", async () => {
    const { symbolRefsStore } = renderGutter({ hits: [hit()], inPrTotal: 1 });
    await fireEvent.click(screen.getByTitle("Close"));
    expect(symbolRefsStore.close).toHaveBeenCalledTimes(1);
  });

  it("resets the comments/strings toggle to collapsed on a new search", async () => {
    const client = stubClient([
      {
        query: "A",
        hits: [hit({ kind: "comment", text: "// first" })],
        in_pr_total: 1,
        outside_pr_total: 0,
        truncated: false,
      },
      {
        query: "B",
        hits: [hit({ kind: "comment", text: "// second" })],
        in_pr_total: 1,
        outside_pr_total: 0,
        truncated: false,
      },
    ]);
    const symbolRefsStore = createSymbolRefsStore({ client });
    const diffStore = fakeDiffStore(["a.go"]);
    render(SymbolRefsGutter, {
      props: { owner: "o", name: "n", number: 1, width: 320 },
      context: new Map<symbol, unknown>([
        [STORES_KEY, { symbolRefs: symbolRefsStore, diff: diffStore }],
      ]),
    });

    await symbolRefsStore.search("o", "n", 1, "sha", "A");
    await tick();
    await fireEvent.click(screen.getByText(/1 in comments\/strings/));
    expect(screen.getByText("// first")).toBeTruthy();

    await symbolRefsStore.search("o", "n", 1, "sha", "B");
    await tick();
    expect(screen.queryByText("// second")).toBeNull();
    expect(screen.getByText(/1 in comments\/strings/)).toBeTruthy();
  });
});

// The server partitions hits against the whole PR's changed-file set, but
// a commit-scoped (or range-scoped) diff view only renders a subset of
// those files. A hit whose file isn't part of what's rendered can never
// resolve when clicked — these tests cover the two halves of that fix:
// marking the row ahead of time, and saying something when a click still
// comes up empty.
describe("SymbolRefsGutter: rows outside the current diff scope", () => {
  it('marks a hit whose path is not part of the rendered diff as "not in this view"', () => {
    const hits = [hit({ path: "b.go" })];
    renderGutter({ hits, inPrTotal: 1, diffFiles: ["a.go"] });
    expect(screen.getByText("not in this view")).toBeTruthy();
  });

  it("shows no marker for a hit whose path is part of the rendered diff", () => {
    const hits = [hit({ path: "a.go" })];
    renderGutter({ hits, inPrTotal: 1, diffFiles: ["a.go"] });
    expect(screen.queryByText("not in this view")).toBeNull();
  });

  it('surfaces an inline notice next to a row whose jump resolves "missing"', async () => {
    const hits = [hit({ path: "b.go", line: 7, text: "ref in b" })];
    renderGutter({ hits, inPrTotal: 1, diffFiles: ["a.go"] });
    scrollToDiffLineMock.mockResolvedValueOnce("missing");

    expect(screen.queryByText(/nothing to jump to/)).toBeNull();
    await fireEvent.click(screen.getByText("ref in b"));

    expect(screen.getByText(/nothing to jump to/)).toBeTruthy();
  });

  it('shows no notice when the jump resolves "line" or "pending"', async () => {
    const hits = [hit({ path: "a.go", line: 7, text: "ref in a" })];
    renderGutter({ hits, inPrTotal: 1, diffFiles: ["a.go"] });
    scrollToDiffLineMock.mockResolvedValueOnce("line");

    await fireEvent.click(screen.getByText("ref in a"));

    expect(screen.queryByText(/nothing to jump to/)).toBeNull();
  });

  it("clears a stale missing-jump notice once a later click on the same row succeeds", async () => {
    const hits = [hit({ path: "b.go", line: 7, text: "ref in b" })];
    renderGutter({ hits, inPrTotal: 1, diffFiles: ["a.go"] });

    scrollToDiffLineMock.mockResolvedValueOnce("missing");
    await fireEvent.click(screen.getByText("ref in b"));
    expect(screen.getByText(/nothing to jump to/)).toBeTruthy();

    scrollToDiffLineMock.mockResolvedValueOnce("line");
    await fireEvent.click(screen.getByText("ref in b"));
    expect(screen.queryByText(/nothing to jump to/)).toBeNull();
  });
});
