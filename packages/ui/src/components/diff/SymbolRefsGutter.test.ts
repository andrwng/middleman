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
// clearDiffLineHighlightMock backs the gutter's onMount teardown (see the
// "clears the jump highlight on unmount" test below) — also mocked
// wholesale rather than exercising the real DOM/classList logic, which
// scrollToDiffLine.test.ts already covers directly.
const clearDiffLineHighlightMock = vi.fn();
vi.mock("./scrollToDiffLine.js", () => ({
  scrollToDiffLine: (target: { path: string; line: number }, deps: DiffJumpDeps) =>
    scrollToDiffLineMock(target, deps),
  clearDiffLineHighlight: () => clearDiffLineHighlightMock(),
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
  // Defaults to "ctags" -- the non-degraded case -- so every test that
  // doesn't care about the classifier renders exactly as it did before
  // the degraded note existed.
  classifier?: string;
  status?: SymbolRefsStatus;
  error?: string | null;
  // Backs getFocusSeq(). Static here -- the fake is a plain object, not
  // reactive -- so a test that needs the value to CHANGE must build the
  // real store instead (see the focus test below).
  focusSeq?: number;
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
    classifier = "ctags",
    status = "ready",
    error = null,
    focusSeq = 0,
  } = overrides;
  return {
    getQuery: () => query,
    getHits: () => hits,
    getInPrTotal: () => inPrTotal,
    getOutsidePrTotal: () => outsidePrTotal,
    isTruncated: () => truncated,
    getClassifier: () => classifier,
    getStatus: () => status,
    getError: () => error,
    isActive: () => status !== "idle",
    getFocusSeq: () => focusSeq,
    search: vi.fn(async () => {}),
    close: vi.fn(),
    openBlank: vi.fn(),
  };
}

function fakeDiffStore(files: string[]) {
  return {
    isFileCollapsed: vi.fn(() => false),
    toggleFileCollapsed: vi.fn(),
    requestRevealLine: vi.fn(),
    consumeRevealTarget: vi.fn(),
    getDiff: vi.fn(() => ({ files: files.map((path) => ({ path })) })),
    getCurrentCommitSha: () => "abc123",
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
  clearDiffLineHighlightMock.mockClear();
});

describe("SymbolRefsGutter", () => {
  it("exposes itself as a labelled landmark for assistive tech", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1 });
    expect(screen.getByRole("complementary", { name: "Symbol references" })).toBeTruthy();
  });

  it("clears the jump highlight when the gutter unmounts (the close button, or any other cause)", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1 });
    expect(clearDiffLineHighlightMock).not.toHaveBeenCalled();

    cleanup();

    expect(clearDiffLineHighlightMock).toHaveBeenCalledTimes(1);
  });

  it("renders the query and the in-PR count in the header", () => {
    // The query now lives in the header's search input (an editable
    // control has no textContent to match on), not in a read-only span,
    // so it's read via .value rather than screen.getByText.
    const { container } = renderGutter({ query: "Frobnicate", hits: [hit()], inPrTotal: 7 });
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']");
    expect(input?.value).toBe("Frobnicate");
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
    const { container } = renderGutter({ status: "ready", hits: [], query: "Frobnicate" });
    expect(screen.getByText(/no other occurrences of/i)).toBeTruthy();
    expect(container.querySelector(".symref-state")?.textContent).toContain("Frobnicate");
  });

  it("the empty state doesn't contradict an outside-PR footer rendered right below it", () => {
    // The empty state is most often reached when the selection was on a
    // deleted (LEFT-side) line -- i.e. exactly when the symbol is NOT
    // "where it was selected" -- so the copy must not claim that, and it
    // must stay true even when the repo-wide footer renders right below it.
    renderGutter({ status: "ready", hits: [], query: "Frobnicate", outsidePrTotal: 23 });
    expect(screen.getByText(/no other occurrences of/i)).toBeTruthy();
    expect(screen.getByText(/\+23 elsewhere in the repo/)).toBeTruthy();
    expect(screen.queryByText(/appears only where it was selected/i)).toBeNull();
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

    // A failed jump on this row must not survive into the next search's
    // result set, even though "B"'s hit below shares this same
    // (path, line) — hit()'s defaults leave both at ("a.go", 1).
    scrollToDiffLineMock.mockResolvedValueOnce("missing");
    await fireEvent.click(screen.getByText("// first"));
    expect(screen.getByText(/nothing to jump to/)).toBeTruthy();

    await symbolRefsStore.search("o", "n", 1, "sha", "B");
    await tick();
    expect(screen.queryByText("// second")).toBeNull();
    expect(screen.getByText(/1 in comments\/strings/)).toBeTruthy();

    // Re-reveal the (reset, re-collapsed) section: the new row at that
    // same (path, line) must render clean, not carry over the stale
    // "missing" notice from a row nobody has clicked yet this search.
    await fireEvent.click(screen.getByText(/1 in comments\/strings/));
    expect(screen.getByText("// second")).toBeTruthy();
    expect(screen.queryByText(/nothing to jump to/)).toBeNull();
  });
});

// ctags labelling (SymbolHit.tag) and the classifier that reports whether
// it was available. A hit WITH a tag shows ctags' own kind, plus the
// qualified name and signature it found, in place of the coarse
// definition/reference badge and the raw matched line. A hit WITHOUT a
// tag -- whether ctags is unavailable, or it just didn't match that line
// -- renders exactly as it always has: that's the fallback, not a second
// code path, and grouping/sorting/the noisy-kind collapse all key off the
// coarse `kind` the server still sets on every hit either way.
describe("SymbolRefsGutter: ctags kind, scope and signature", () => {
  it("a tagged hit renders tag.kind as its badge, not the coarse kind", () => {
    const hits = [
      hit({
        kind: "definition",
        text: "void bar(int x) {",
        tag: { kind: "function", scope: "Foo", signature: "(int x)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "bar" });

    const badge = container.querySelector(".symref-row__kind");
    expect(badge?.textContent).toBe("function");
    expect(badge?.textContent).not.toBe("def");
    // The coarse kind still drives the badge's color/grouping modifier
    // classes -- only the label text changes for a tagged row.
    expect(badge?.className).toContain("symref-row__kind--definition");
    expect(container.querySelector(".symref-row")?.className).toContain(
      "symref-row--definition",
    );
  });

  // Which punctuation joins scope to symbol belongs to the language:
  // C++ uses "::", Go and Python use ".". These three pin that per
  // language, because a single hardcoded separator renders C++
  // punctuation onto dotted Go and Python scopes.
  it("a C++ hit joins scope to symbol with \"::\"", () => {
    const hits = [
      hit({
        path: "src/v/kafka/handler.cc",
        kind: "definition",
        text: "void bar(int x) {",
        tag: { kind: "function", scope: "Foo", signature: "(int x)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "bar" });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "Foo::bar(int x)",
    );
  });

  it("a Go hit joins scope to symbol with \".\", not C++ punctuation", () => {
    const hits = [
      hit({
        path: "internal/cache.go",
        kind: "definition",
        text: "func (c *Cache) Get(k string) string {",
        tag: { kind: "func", scope: "main.Cache", signature: "(k string)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "Get" });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("main.Cache.Get(k string)");
    expect(rowText).not.toContain("::");
  });

  it("a Python hit joins scope to symbol with \".\"", () => {
    const hits = [
      hit({
        path: "tools/cache.py",
        kind: "definition",
        text: "    def get(self, k):",
        tag: { kind: "member", scope: "Cache", signature: "(self, k)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "get" });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("Cache.get(self, k)");
    expect(rowText).not.toContain("::");
  });

  it("a tagged hit with no scope renders symbol + signature, with no stray \"::\"", () => {
    const hits = [
      hit({
        kind: "definition",
        text: "void bar(int x) {",
        tag: { kind: "function", signature: "(int x)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "bar" });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("bar(int x)");
    expect(rowText).not.toContain("::");
  });

  it("a tagged hit with no signature renders scope::symbol with nothing trailing", () => {
    const hits = [
      hit({
        path: "src/v/kafka/handler.hpp",
        kind: "definition",
        text: "class Foo {",
        tag: { kind: "class", scope: "ns" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "Foo" });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe("ns::Foo");
  });

  it("a tagged hit carries the raw matched line in a title attribute", () => {
    const hits = [
      hit({
        kind: "definition",
        text: "void bar(int x) {",
        tag: { kind: "function", scope: "Foo", signature: "(int x)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "bar" });

    const row = container.querySelector(".symref-row");
    expect(row?.getAttribute("title")).toBe("void bar(int x) {");
    // Confirm the raw line is recoverable ONLY via the title, not also
    // rendered as the row's visible text (which shows the tagged label
    // instead) -- otherwise this assertion could pass by accident. The
    // fixture path is a .go file, so the label joins with "." here; the
    // separator itself is pinned per language by the tests above.
    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "Foo.bar(int x)",
    );
  });

  it("an untagged hit renders the coarse kind badge and the raw text (today's behaviour must survive)", () => {
    const hits = [hit({ kind: "reference", text: "return bar(x);" })];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "bar" });

    const badge = container.querySelector(".symref-row__kind");
    expect(badge?.textContent).toBe("ref");
    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "return bar(x);",
    );
    expect(container.querySelector(".symref-row")?.getAttribute("title")).toBe(
      "return bar(x);",
    );
  });

  it("shows the degraded note when the classifier is heuristic, and hides it when ctags", () => {
    renderGutter({ hits: [hit()], inPrTotal: 1, classifier: "heuristic" });
    expect(screen.getByText(/heuristic/i)).toBeTruthy();
    cleanup();

    renderGutter({ hits: [hit()], inPrTotal: 1, classifier: "ctags" });
    expect(screen.queryByText(/heuristic/i)).toBeNull();
  });

  it("grouping and the comments/strings collapse still work with a mix of tagged and untagged hits", async () => {
    const hits = [
      hit({
        path: "a.go",
        line: 1,
        kind: "definition",
        text: "func Foo() {",
        tag: { kind: "function", signature: "()" },
      }),
      hit({ path: "a.go", line: 42, kind: "comment", text: "// Foo does a thing" }),
      hit({ path: "b.go", line: 9, kind: "reference", text: "return Foo();" }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: hits.length, query: "Foo" });

    const groupPaths = Array.from(
      container.querySelectorAll(".symref-group__path"),
    ).map((el) => el.textContent);
    expect(groupPaths).toEqual(["a.go", "b.go"]);

    const rows = container.querySelectorAll(".symref-row");
    expect(rows).toHaveLength(2);
    // a.go: tagged definition -- badge is ctags' own kind, body is the
    // synthesized qualified name/signature, not the raw line.
    expect(rows[0]?.querySelector(".symref-row__kind")?.textContent).toBe("function");
    expect(rows[0]?.querySelector(".symref-row__text")?.textContent).toBe("Foo()");
    // b.go: untagged reference -- coarse badge, raw line verbatim.
    expect(rows[1]?.querySelector(".symref-row__kind")?.textContent).toBe("ref");
    expect(rows[1]?.querySelector(".symref-row__text")?.textContent).toBe(
      "return Foo();",
    );

    expect(screen.queryByText("// Foo does a thing")).toBeNull();
    const toggle = screen.getByText(/1 in comments\/strings/);
    await fireEvent.click(toggle);
    expect(screen.getByText("// Foo does a thing")).toBeTruthy();
    await fireEvent.click(toggle);
    expect(screen.queryByText("// Foo does a thing")).toBeNull();
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

// ctags has no name for an anonymous namespace, so it invents one:
// "__anon" plus a hex run derived from the file. That placeholder reaches
// the gutter inside the scope string, and every shape below was taken from
// real ctags output over redpanda's C++ tree -- all four occur there in
// numbers, so none of these is a corner case.
//
// The rendering rule has two halves, and both matter. An anonymous
// component that comes FIRST collapses to nothing, leaving the leading
// "::" that C++ already uses to mean "unqualified" -- the most common
// shape by a wide margin. One anywhere else becomes "<anon>", because
// collapsing it there would emit "redpanda::::varint", which reads as a
// broken renderer rather than as a scope.
describe("SymbolRefsGutter: anonymous namespace scopes", () => {
  it("a top-level anonymous namespace renders as a bare leading \"::\"", () => {
    const hits = [
      hit({
        path: "src/v/transform/transform.cc",
        kind: "definition",
        text: "void do_transform() {",
        tag: {
          kind: "function",
          scope: "__anonbc440b330211",
          signature: "()",
        },
      }),
    ];
    const { container } = renderGutter({
      hits,
      inPrTotal: 1,
      query: "do_transform",
    });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("::do_transform()");
    expect(rowText).not.toContain("__anon");
  });

  // The separator survives even though the sanitized scope is empty. Keying
  // off the sanitized value instead would drop it and render a bare
  // "do_transform()", silently losing the file-local signal in exactly the
  // shape that occurs most often.
  it("keeps the leading \"::\" when the anonymous component is the whole scope", () => {
    const hits = [
      hit({
        path: "src/v/transform/transform.cc",
        kind: "definition",
        text: "void do_transform() {",
        tag: { kind: "function", scope: "__anonbc440b330211" },
      }),
    ];
    const { container } = renderGutter({
      hits,
      inPrTotal: 1,
      query: "do_transform",
    });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "::do_transform",
    );
  });

  it("an anonymous namespace with a nested scope keeps the leading \"::\"", () => {
    const hits = [
      hit({
        path: "src/v/wasm/wasi.cc",
        kind: "definition",
        text: "void native_object::add() {",
        tag: {
          kind: "function",
          scope: "__anonecb58e540111::native_object",
          signature: "()",
        },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "add" });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "::native_object::add()",
    );
  });

  it("an anonymous component in the middle renders as \"<anon>\", never \"::::\"", () => {
    const hits = [
      hit({
        path: "src/v/serde/varint.cc",
        kind: "definition",
        text: "constexpr size_t MAX_LENGTH = 10;",
        tag: {
          kind: "variable",
          scope: "redpanda::__anondf446e100111::varint",
        },
      }),
    ];
    const { container } = renderGutter({
      hits,
      inPrTotal: 1,
      query: "MAX_LENGTH",
    });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("redpanda::<anon>::varint::MAX_LENGTH");
    expect(rowText).not.toContain("::::");
    expect(rowText).not.toContain("__anon");
  });

  it("a trailing anonymous component renders as \"<anon>\", never \"::::\"", () => {
    const hits = [
      hit({
        path: "tools/clang-tidy/redpanda.cc",
        kind: "definition",
        text: "AST_MATCHER(Decl, isFromRedpanda) {",
        tag: {
          kind: "function",
          scope: "clang::tidy::redpanda::__anoncc1061c90111",
          signature: "()",
        },
      }),
    ];
    const { container } = renderGutter({
      hits,
      inPrTotal: 1,
      query: "AST_MATCHER",
    });

    const rowText = container.querySelector(".symref-row__text")?.textContent;
    expect(rowText).toBe("clang::tidy::redpanda::<anon>::AST_MATCHER()");
    expect(rowText).not.toContain("::::");
  });

  // ctags spells an anonymous union or struct exactly the way it spells an
  // anonymous namespace, and scopeKind cannot tell them apart when the
  // placeholder sits mid-scope. So they get the same treatment, which lands
  // on "<anon>" here because the component is not first.
  it("an anonymous union member renders as \"<anon>\"", () => {
    const hits = [
      hit({
        path: "src/v/model/record.h",
        kind: "definition",
        text: "        int a;",
        tag: { kind: "member", scope: "HasAnon::__anon373dda25040a" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "a" });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "HasAnon::<anon>::a",
    );
  });

  // The whole-component match plus the 8-hex-digit floor is what keeps a
  // real identifier out of the rewrite. "__anonymous_helper" fails on the
  // "y"; "__anonface" is hex but only four digits long.
  it("leaves a real identifier that merely starts with \"__anon\" alone", () => {
    const hits = [
      hit({
        path: "src/v/kafka/handler.cc",
        kind: "definition",
        text: "void foo() {",
        tag: {
          kind: "function",
          scope: "redpanda::__anonymous_helper",
          signature: "()",
        },
      }),
      hit({
        path: "src/v/kafka/handler.cc",
        line: 2,
        kind: "definition",
        text: "void foo() {",
        tag: { kind: "function", scope: "redpanda::__anonface", signature: "()" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 2, query: "foo" });

    const rows = [...container.querySelectorAll(".symref-row__text")].map(
      (el) => el.textContent,
    );
    expect(rows).toEqual([
      "redpanda::__anonymous_helper::foo()",
      "redpanda::__anonface::foo()",
    ]);
  });

  // Go and Python never produce these placeholders -- verified against
  // ctags directly, including Go anonymous struct fields and Python nested
  // classes. Splitting on the language's own separator is what makes the
  // rewrite a structural no-op there rather than something to special-case.
  it("leaves a dotted Go scope untouched", () => {
    const hits = [
      hit({
        path: "internal/cache.go",
        kind: "definition",
        text: "func (c *Cache) Get(k string) string {",
        tag: { kind: "func", scope: "main.Cache", signature: "(k string)" },
      }),
    ];
    const { container } = renderGutter({ hits, inPrTotal: 1, query: "Get" });

    expect(container.querySelector(".symref-row__text")?.textContent).toBe(
      "main.Cache.Get(k string)",
    );
  });
});

// The header's query text is now an editable input, present in every
// status, so a query can be retyped in place. "prompt" is the status the
// toolbar button and the `s` hotkey open into: active (so the gutter is
// mounted) with nothing searched yet.
describe("SymbolRefsGutter: header search input", () => {
  it("renders a hint and no rows in the prompt status", () => {
    const { container } = renderGutter({ status: "prompt", hits: [], inPrTotal: 0 });

    expect(container.querySelector(".symref-prompt")).not.toBeNull();
    expect(container.querySelectorAll(".symref-row")).toHaveLength(0);
  });

  it("seeds the input from the current query so it can be edited in place", () => {
    const { container } = renderGutter({ query: "handle", hits: [hit()], inPrTotal: 1 });

    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']");
    expect(input?.value).toBe("handle");
  });

  it("searches on Enter with the diff store's current SHA", async () => {
    const { container, symbolRefsStore } = renderGutter({ status: "prompt" });
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']")!;

    await fireEvent.input(input, { target: { value: "HandleRequest" } });
    await fireEvent.keyDown(input, { key: "Enter" });

    expect(symbolRefsStore.search).toHaveBeenCalledWith("o", "n", 1, "abc123", "HandleRequest");
  });

  // isSymbolQuery rejects any whitespace, because a word-boundary
  // fixed-string grep can never match a multi-word query. Saying so beats
  // a search box that silently does nothing.
  it("refuses a multi-word query and says why instead of searching", async () => {
    const { container, symbolRefsStore } = renderGutter({ status: "prompt" });
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']")!;

    await fireEvent.input(input, { target: { value: "group manager" } });
    await fireEvent.keyDown(input, { key: "Enter" });

    expect(symbolRefsStore.search).not.toHaveBeenCalled();
    expect(container.querySelector(".symref-invalid")?.textContent).toMatch(/whitespace/i);
  });

  it("does nothing on Enter with an empty query", async () => {
    // query: "" is load-bearing -- fakeSymbolRefsStore defaults it to
    // "Foo", which the input seeds from, so without this the draft is
    // non-empty and Enter legitimately searches.
    const { container, symbolRefsStore } = renderGutter({ status: "prompt", query: "" });
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']")!;

    await fireEvent.keyDown(input, { key: "Enter" });

    expect(symbolRefsStore.search).not.toHaveBeenCalled();
  });

  it("closes the gutter on Escape, matching the close button", async () => {
    const { container, symbolRefsStore } = renderGutter({ status: "prompt" });
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']")!;

    await fireEvent.keyDown(input, { key: "Escape" });

    expect(symbolRefsStore.close).toHaveBeenCalled();
  });

  // Uses the REAL store: focusSeq must actually change to trigger the
  // component's $effect, and the fake store above is a plain, non-reactive
  // object.
  it("focuses and selects the input when the store signals a focus request", async () => {
    const store = createSymbolRefsStore({ client: stubClient([]) });
    store.openBlank();
    const { container } = render(SymbolRefsGutter, {
      props: { owner: "o", name: "n", number: 1, width: 320 },
      context: new Map<symbol, unknown>([
        [STORES_KEY, { symbolRefs: store, diff: fakeDiffStore([]) }],
      ]),
    });
    await tick();
    const input = container.querySelector<HTMLInputElement>("[data-testid='symref-search']")!;

    store.openBlank();
    await tick();

    expect(document.activeElement).toBe(input);
  });
});
