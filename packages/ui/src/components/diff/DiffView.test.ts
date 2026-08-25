import { cleanup, render } from "@testing-library/svelte";
import { tick } from "svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createSymbolRefsStore } from "../../stores/symbolRefs.svelte.js";
import type { SymbolHit } from "../../stores/symbolRefs.svelte.js";
import type { MiddlemanClient } from "../../types.js";
import DiffView from "./DiffView.svelte";

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

function renderDiffView() {
  const client = symbolRefsClient();
  const diffStore = createDiffStore({ client });
  const symbolRefsStore = createSymbolRefsStore({ client });
  render(DiffView, {
    props: { owner: "acme", name: "widget", number: 7 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, {
        diff: diffStore,
        ai: fakeLifecycleStore(),
        brief: fakeLifecycleStore(),
        reviewThreads: fakeReviewThreadsStore(),
        symbolRefs: symbolRefsStore,
      }],
    ]),
  });
  return { diffStore, symbolRefsStore };
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
});
