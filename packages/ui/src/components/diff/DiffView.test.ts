import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createSymbolRefsStore } from "../../stores/symbolRefs.svelte.js";
import { createCodeBrowserStore } from "../../stores/codeBrowser.svelte.js";
import type { SymbolHit } from "../../stores/symbolRefs.svelte.js";
import type { DiffFile as DiffFileType } from "../../api/types.js";
import type { MiddlemanClient } from "../../types.js";
import DiffView from "./DiffView.svelte";

// Stubbed out only for the browse-action tests below, which are the first
// in this suite that need `diff` to actually load so the symbol-refs
// gutter (nested inside DiffView's `{:else if diff}` branch) mounts. The
// real DiffFile pulls in a much larger store surface (ai, reviewThreads'
// per-line lookups, etc.) that this suite has no other reason to fake --
// same pattern as CommentGutter.test.ts's card stubs.
vi.mock("./DiffFile.svelte", () => ({
  default: vi.fn().mockImplementation(() => ({ $$: {} })),
}));

// DiffView has no broader unit-test suite by design -- its layout is real
// flexbox that jsdom doesn't implement, and the Playwright suite is the
// actual net for that (see Task 9/10 history). This file exists solely to
// cover the scope-change effect guarding the symbol-refs gutter: DiffView
// is the one place that reacts to both diffStore's scope and
// symbolRefsStore's active search, so the guard can only be exercised by
// mounting it, not by testing either store in isolation.

// Deliberately fails every request so `diff`/`commits` stay null/empty
// throughout. This suite only cares about the scope-tracking effect, not
// diff rendering, so there's no need for a realistic diff payload --
// loadDiff/loadCommits already handle a failed fetch by setting an error
// state, not by throwing.
function installFailingFetch(): void {
  globalThis.fetch = vi.fn(async () => ({
    ok: false,
    status: 404,
    json: async () => ({}),
  }) as unknown as Response);
}

function hit(overrides: Partial<SymbolHit> = {}): SymbolHit {
  return { path: "a.go", line: 1, text: "ref line", kind: "reference", ...overrides };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Answers only the /commits endpoint (reading `sha` at call time, not
// capture time, so a test can advance it to simulate a later commits
// refresh); every other URL 404s, matching installFailingFetch's spirit
// that this suite doesn't care about diff/file rendering.
function installCommitsFetch(getSha: () => string): void {
  globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
    const url =
      typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url.includes("/commits")) {
      return jsonResponse({
        commits: [
          { sha: getSha(), message: "head", author_name: "Alice", authored_at: "2026-01-01T00:00:00Z" },
        ],
      });
    }
    return jsonResponse({}, 404);
  }) as unknown as typeof fetch;
}

// Minimal single-file diff, only for the tests below that need the
// SymbolRefsGutter's browse action to reach a row -- which requires
// DiffView's `{:else if diff}` branch (and the gutter it renders inside)
// to actually mount, not just the commits list the `b`-hotkey tests above
// get away with. Content/hunks are irrelevant to what's being tested.
function makeDiffFile(overrides: Partial<DiffFileType> = {}): DiffFileType {
  return {
    path: "a.go",
    old_path: "a.go",
    status: "modified",
    is_binary: false,
    is_whitespace_only: false,
    additions: 1,
    deletions: 0,
    hunks: [{
      old_start: 1,
      old_count: 1,
      new_start: 1,
      new_count: 1,
      lines: [{ type: "context", content: "line 1", old_num: 1, new_num: 1 }],
    }],
    ...overrides,
  };
}

// Like installCommitsFetch, but also answers the diff endpoint with a
// single-file payload so DiffView's `{:else if diff}` branch (and the
// symbol-refs gutter nested inside it) actually mounts.
function installCommitsAndDiffFetch(getSha: () => string, files: DiffFileType[]): void {
  globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
    const url =
      typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url.includes("/commits")) {
      return jsonResponse({
        commits: [
          { sha: getSha(), message: "head", author_name: "Alice", authored_at: "2026-01-01T00:00:00Z" },
        ],
      });
    }
    if (url.includes("/diff")) {
      return jsonResponse({ files });
    }
    return jsonResponse({}, 404);
  }) as unknown as typeof fetch;
}

// diffStore never calls client.GET (it loads via raw fetch, stubbed
// above), so this stub only needs to answer the symbol-refs search
// endpoint that symbolRefsStore.search() calls.
function symbolRefsClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({
      data: {
        query: "Foo",
        hits: [hit()],
        in_pr_total: 1,
        outside_pr_total: 0,
        truncated: false,
      },
      error: undefined,
    })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

// DiffView only calls start()/stop() on ai and brief, and load()/clear()
// on reviewThreads -- it never reads anything back from them -- so
// trivial fakes suffice; there's no need for the real stores here.
function fakeLifecycleStore() {
  return { start: vi.fn(), stop: vi.fn() };
}
function fakeReviewThreadsStore() {
  return { load: vi.fn(async () => {}), clear: vi.fn() };
}

// DiffToolbar (rendered once `diff` loads) reads this store straight from
// context too, same as codeBrowser -- only needed by the browse-action
// tests below, which are the first in this suite to get diff loading far
// enough for DiffToolbar to actually mount.
function fakeDetailStore() {
  return {
    getHiddenThreadCount: () => 0,
    isShowingHiddenThreads: () => false,
    setShowHiddenThreads: vi.fn(),
    getDetail: () => null,
    getReviewCommentsByFilePath: () => new Map(),
  };
}

// CodeBrowserPanel (mounted for real when the 'b' hotkey opens it) reads
// this store straight from context, not via a DiffView prop -- a trivial
// fake with mocked open()/close()/navigateTo()/loadTree() keeps these
// tests from making real network calls or loading Shiki, matching
// CodeBrowserPanel.test.ts's own fake.
function fakeCodeBrowserStore() {
  return {
    get isOpen() {
      return true;
    },
    get path() {
      return null;
    },
    get entriesByDir() {
      return new Map();
    },
    get content() {
      return null;
    },
    get status() {
      return "ready" as const;
    },
    get error() {
      return null;
    },
    open: vi.fn(async () => {}),
    close: vi.fn(),
    navigateTo: vi.fn(async () => {}),
    loadTree: vi.fn(async () => {}),
  };
}

function renderDiffView() {
  const client = symbolRefsClient();
  const diffStore = createDiffStore({ client });
  const symbolRefsStore = createSymbolRefsStore({ client });
  const codeBrowserStore = fakeCodeBrowserStore();
  render(DiffView, {
    props: { owner: "acme", name: "widget", number: 7 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, {
        diff: diffStore,
        ai: fakeLifecycleStore(),
        brief: fakeLifecycleStore(),
        reviewThreads: fakeReviewThreadsStore(),
        symbolRefs: symbolRefsStore,
        codeBrowser: codeBrowserStore,
        detail: fakeDetailStore(),
      }],
    ]),
  });
  return { diffStore, symbolRefsStore, codeBrowserStore };
}

beforeEach(() => {
  installFailingFetch();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("DiffView symbol-refs scope guard", () => {
  it("closes an active symbol search when the diff scope genuinely changes", async () => {
    const { diffStore, symbolRefsStore } = renderDiffView();
    await tick();

    await symbolRefsStore.search("acme", "widget", 7, "sha-head", "Foo");
    expect(symbolRefsStore.isActive()).toBe(true);

    // head -> a specific commit: a real scope change while the previous
    // scope's search results are still showing.
    diffStore.selectCommit("sha-a");
    await tick();

    expect(symbolRefsStore.isActive()).toBe(false);
  });

  it("does not close a search on a no-op scope reassignment to the same value", async () => {
    const { diffStore, symbolRefsStore } = renderDiffView();
    await tick();

    // Settle on commit:sha-a *before* starting the search, so the
    // re-selection below is a genuine no-op relative to the scope
    // already in place, not itself the "real" transition into it.
    diffStore.selectCommit("sha-a");
    await tick();

    await symbolRefsStore.search("acme", "widget", 7, "sha-a", "Foo");
    expect(symbolRefsStore.isActive()).toBe(true);

    // Re-selecting the SAME commit reassigns diffStore's scope to a
    // fresh object literal (not the same reference) with identical
    // fields. Watching object identity would misread this as a change
    // and close the search the user is still looking at; watching the
    // derived string key must not.
    diffStore.selectCommit("sha-a");
    await tick();

    expect(symbolRefsStore.isActive()).toBe(true);
  });

  it("closes an active symbol search when the PR head advances while scope stays 'head' (a refresh, not a scope change)", async () => {
    let headSha = "sha-head-1";
    installCommitsFetch(() => headSha);

    const { diffStore, symbolRefsStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await symbolRefsStore.search("acme", "widget", 7, "sha-head-1", "Foo");
    expect(symbolRefsStore.isActive()).toBe(true);

    // Simulate a refresh advancing the PR head: diffScopeKey
    // maps every head-scope view to the same "head" string, so this
    // never looks like a scope change -- only the resolved SHA moves.
    headSha = "sha-head-2";
    await diffStore.refresh();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-2");
    });

    await waitFor(() => {
      expect(symbolRefsStore.isActive()).toBe(false);
    });
  });

  it("does not close a search while the head SHA it was searched against is still current", async () => {
    installCommitsFetch(() => "sha-head-1");

    const { diffStore, symbolRefsStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await symbolRefsStore.search("acme", "widget", 7, "sha-head-1", "Foo");
    expect(symbolRefsStore.isActive()).toBe(true);

    // A refresh that resolves to the SAME head SHA (nothing actually
    // changed) must not disturb the search the user is still looking at.
    await diffStore.refresh();
    await tick();
    await tick();

    expect(symbolRefsStore.isActive()).toBe(true);
  });
});

// `s` is the keyboard twin of the toolbar's Refs button. It rides
// DiffView's existing window-level handleKeydown, whose guards already
// skip form fields and modified keys -- so these four cases pin that the
// new key inherits them rather than reimplementing them.
describe("DiffView: the s hotkey", () => {
  it("opens the refs search box", async () => {
    installCommitsFetch(() => "sha-head-1");
    const { diffStore, symbolRefsStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await fireEvent.keyDown(window, { key: "s" });

    expect(symbolRefsStore.getStatus()).toBe("prompt");
  });

  it("is ignored while a text field has focus, so typing an s is safe", async () => {
    installCommitsFetch(() => "sha-head-1");
    const { diffStore, symbolRefsStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });
    const field = document.createElement("input");
    document.body.appendChild(field);

    await fireEvent.keyDown(field, { key: "s", bubbles: true });

    expect(symbolRefsStore.getStatus()).toBe("idle");
    field.remove();
  });

  it("is ignored with a modifier, leaving Cmd/Ctrl-S alone", async () => {
    installCommitsFetch(() => "sha-head-1");
    const { diffStore, symbolRefsStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await fireEvent.keyDown(window, { key: "s", metaKey: true });
    await fireEvent.keyDown(window, { key: "s", ctrlKey: true });

    expect(symbolRefsStore.getStatus()).toBe("idle");
  });

  it("does nothing when the scope has no resolvable SHA", async () => {
    const { symbolRefsStore } = renderDiffView();
    await tick();

    await fireEvent.keyDown(window, { key: "s" });

    expect(symbolRefsStore.getStatus()).toBe("idle");
  });
});

// `b` is the keyboard twin of the toolbar's Browse button, gated on a
// resolvable SHA for the same reason `s` is: the panel loads a tree and
// file content at a specific commit, and there is none to browse
// otherwise.
describe("DiffView: the b hotkey", () => {
  it("opens the code browser panel on 'b' when a SHA is resolved", async () => {
    installCommitsFetch(() => "sha-head-1");
    const { diffStore, codeBrowserStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await fireEvent.keyDown(window, { key: "b" });

    expect(screen.getByText("Browse files")).toBeTruthy();
    expect(codeBrowserStore.open).toHaveBeenCalledWith("acme", "widget", 7, "sha-head-1", "");
  });

  it("does not open the code browser panel on 'b' when no SHA is resolved", async () => {
    renderDiffView();
    await tick();

    await fireEvent.keyDown(window, { key: "b" });

    expect(screen.queryByText("Browse files")).toBeNull();
  });

  // The panel is keyed by sha={currentSha}, so stepping commits
  // ([`/`]) re-renders the mounted CodeBrowserPanel with a new sha
  // prop rather than unmounting it. This pins that CodeBrowserPanel's
  // own effect actually re-runs browser.open() with the new SHA --
  // Svelte's $props() destructuring is reactive, but that reactivity
  // is exactly the thing Task 7's effect could have broken without
  // reading `sha` directly in its body.
  it("keeps the panel open and re-seeds the browser at the new SHA when the commit steps while the panel is open", async () => {
    installCommitsFetch(() => "sha-head-1");
    const { diffStore, codeBrowserStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
    });

    await fireEvent.keyDown(window, { key: "b" });
    expect(screen.getByText("Browse files")).toBeTruthy();
    expect(codeBrowserStore.open).toHaveBeenCalledWith("acme", "widget", 7, "sha-head-1", "");

    // selectCommit is the same mechanism the `[`/`]` handlers
    // (stepPrev/stepNext) drive: it changes diffStore's scope, which
    // moves currentSha/getCurrentCommitSha() to the newly-selected
    // commit without unmounting DiffView or its open CodeBrowserPanel.
    diffStore.selectCommit("sha-a");
    await tick();

    expect(diffStore.getCurrentCommitSha()).toBe("sha-a");
    expect(screen.getByText("Browse files")).toBeTruthy();
    expect(codeBrowserStore.open).toHaveBeenCalledWith("acme", "widget", 7, "sha-a", "");
  });
});

// SymbolRefsGutter lives inside DiffView's own subtree, which already owns
// the single `{#if codeBrowserOpen}<CodeBrowserPanel>` instance the `b`
// tests above exercise. The gutter's browse action must reuse that same
// instance -- reached through a callback prop -- rather than mounting a
// second one of its own; the most important thing this proves is that only
// one panel is ever on screen, not just that the store's open() was called.
describe("DiffView: symbol-refs browse action opens the same code browser panel", () => {
  it("seeds the existing panel at the hit's path and never mounts a second panel", async () => {
    installCommitsAndDiffFetch(() => "sha-head-1", [makeDiffFile()]);
    const { diffStore, symbolRefsStore, codeBrowserStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
      expect(diffStore.getDiff()).not.toBeNull();
    });

    // No code browser panel yet -- only the symbol-refs gutter is open.
    await symbolRefsStore.search("acme", "widget", 7, "sha-head-1", "Foo");
    await tick();
    expect(screen.queryByText("Browse files")).toBeNull();

    await fireEvent.click(
      screen.getByRole("button", { name: /browse a\.go in the code browser/i, hidden: true }),
    );

    // Exactly one panel mounts, seeded with the hit's own path -- not the
    // (empty) active-file default the `b` hotkey uses -- and forced, so a
    // stale bookmark from earlier browsing can't silently override it.
    expect(screen.getAllByText("Browse files")).toHaveLength(1);
    expect(codeBrowserStore.open).toHaveBeenCalledWith(
      "acme", "widget", 7, "sha-head-1", "a.go", { forcePath: true },
    );

    // The symbol-refs gutter itself is untouched -- the row's own
    // reveal-and-jump click behaviour is additive, not replaced.
    expect(symbolRefsStore.isActive()).toBe(true);
  });

  it("reuses the already-open panel (navigating it directly) rather than mounting a duplicate or re-running open()", async () => {
    // Deliberately a different path than the hit below (still "a.go", the
    // symbolRefsClient fixture's default): loadDiff's setActiveIfNeeded
    // makes this file the active one, so if the browse action's re-seed
    // were a no-op, initialPath would already read "b.go" both before and
    // after -- masking a broken callback behind an unchanged prop.
    installCommitsAndDiffFetch(() => "sha-head-1", [makeDiffFile({ path: "b.go", old_path: "b.go" })]);
    const { diffStore, symbolRefsStore, codeBrowserStore } = renderDiffView();
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
      expect(diffStore.getDiff()).not.toBeNull();
    });

    // Open the panel first via the `b` hotkey (the pre-existing entry point).
    await fireEvent.keyDown(window, { key: "b" });
    expect(screen.getAllByText("Browse files")).toHaveLength(1);
    codeBrowserStore.open.mockClear();

    await symbolRefsStore.search("acme", "widget", 7, "sha-head-1", "Foo");
    await tick();

    await fireEvent.click(
      screen.getByRole("button", { name: /browse a\.go in the code browser/i, hidden: true }),
    );

    // Still exactly one panel -- the browse action reused the SAME
    // instance rather than mounting a second one alongside it. It
    // navigates the store directly (navigateTo) rather than re-running
    // open() -- CodeBrowserPanel's own effect deliberately does not react
    // to initialPath changes while mounted (see finding 1's fix), so
    // reassigning DiffView's seed-path state here would silently do
    // nothing; navigateTo is the correct "go to this path now" call.
    expect(screen.getAllByText("Browse files")).toHaveLength(1);
    expect(codeBrowserStore.open).not.toHaveBeenCalled();
    expect(codeBrowserStore.navigateTo).toHaveBeenCalledWith("a.go");
  });
});

// Regression coverage for the bug found in the plan's final e2e task: once
// any bookmark already exists for this PR (from ordinary browsing), a later
// "browse this hit" click was silently overridden back to the stale
// bookmarked file instead of opening the hit's file. Exercised against the
// REAL codeBrowser store (not the fake used by the tests above) so the
// bookmark-priority logic itself is on the hook, not just what args
// DiffView happens to pass to a mock.
describe("DiffView: symbol-refs browse bypasses a stale bookmark", () => {
  function bookmarkedCodeBrowserClient(bookmarkedPath: string): MiddlemanClient {
    return {
      GET: vi.fn(async (path: string) => {
        if (path.includes("code-browser-state")) return { data: { path: bookmarkedPath } };
        if (path.includes("/tree")) return { data: { path: "", entries: [] } };
        if (path.includes("/blob")) return { data: { content: "", truncated: false } };
        throw new Error(`unexpected GET ${path}`);
      }),
      PUT: vi.fn(async () => ({ data: { path: bookmarkedPath } })),
      POST: vi.fn(async () => ({ data: undefined, error: undefined })),
      DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    } as unknown as MiddlemanClient;
  }

  it("opens the hit's file, not the stale bookmark, when browsing a symbol-refs hit", async () => {
    installCommitsAndDiffFetch(() => "sha-head-1", [makeDiffFile()]);
    const client = symbolRefsClient();
    const diffStore = createDiffStore({ client });
    const symbolRefsStore = createSymbolRefsStore({ client });
    // A different file ("b.go") is already bookmarked for this PR, from
    // earlier ordinary browsing.
    const codeBrowserStore = createCodeBrowserStore({
      client: bookmarkedCodeBrowserClient("b.go"),
    });
    render(DiffView, {
      props: { owner: "acme", name: "widget", number: 7 },
      context: new Map<symbol, unknown>([
        [STORES_KEY, {
          diff: diffStore,
          ai: fakeLifecycleStore(),
          brief: fakeLifecycleStore(),
          reviewThreads: fakeReviewThreadsStore(),
          symbolRefs: symbolRefsStore,
          codeBrowser: codeBrowserStore,
          detail: fakeDetailStore(),
        }],
      ]),
    });
    await waitFor(() => {
      expect(diffStore.getCurrentCommitSha()).toBe("sha-head-1");
      expect(diffStore.getDiff()).not.toBeNull();
    });

    await symbolRefsStore.search("acme", "widget", 7, "sha-head-1", "Foo");
    await tick();

    await fireEvent.click(
      screen.getByRole("button", { name: /browse a\.go in the code browser/i, hidden: true }),
    );
    await waitFor(() => {
      expect(codeBrowserStore.path).toBe("a.go");
    });

    // Never resolves to the stale bookmark.
    expect(codeBrowserStore.path).not.toBe("b.go");
  });
});
