import { describe, expect, it, vi } from "vitest";
import { createSymbolRefsStore, isSymbolQuery } from "./symbolRefs.svelte.js";
import type { SymbolHit } from "./symbolRefs.svelte.js";
import type { components } from "../api/generated/schema.js";
import type { MiddlemanClient } from "../types.js";

type SymbolRefsResponse = components["schemas"]["SymbolRefsResponse"];

function makeHit(over: Partial<SymbolHit> = {}): SymbolHit {
  return { path: "a.go", line: 1, text: "func Foo() {}", kind: "definition", ...over };
}

function makeResponse(
  query: string,
  hits: SymbolHit[] | null,
  over: Partial<SymbolRefsResponse> = {},
): SymbolRefsResponse {
  return {
    query,
    hits,
    in_pr_total: hits?.length ?? 0,
    outside_pr_total: 0,
    truncated: false,
    classifier: "ctags",
    ...over,
  };
}

function stubClient(
  over: Partial<Record<"GET" | "POST" | "DELETE", unknown>> = {},
): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}

// deferred lets a test control exactly when a client.GET call settles, so
// two in-flight searches can be resolved in a chosen order.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("isSymbolQuery", () => {
  it("accepts a single word with no whitespace", () => {
    expect(isSymbolQuery("Foo")).toBe(true);
    expect(isSymbolQuery("foo_bar")).toBe(true);
    expect(isSymbolQuery("a")).toBe(true);
  });

  it("accepts exactly 128 characters", () => {
    expect(isSymbolQuery("a".repeat(128))).toBe(true);
  });

  it("rejects an empty string", () => {
    expect(isSymbolQuery("")).toBe(false);
  });

  it("rejects a whitespace-only string", () => {
    expect(isSymbolQuery("   ")).toBe(false);
  });

  it("rejects a string with embedded whitespace", () => {
    expect(isSymbolQuery("has space")).toBe(false);
  });

  it("rejects a multi-line string", () => {
    expect(isSymbolQuery("two\nlines")).toBe(false);
  });

  it("rejects 129 characters", () => {
    expect(isSymbolQuery("a".repeat(129))).toBe(false);
  });

  it("accepts leading/trailing whitespace whose trimmed value is just inside the limit", () => {
    expect(isSymbolQuery(`  ${"a".repeat(128)}  `)).toBe(true);
  });

  it("rejects leading/trailing whitespace whose trimmed value is just outside the limit", () => {
    expect(isSymbolQuery(`  ${"a".repeat(129)}  `)).toBe(false);
  });

  it("rejects a multi-byte symbol under 128 characters but over 128 UTF-8 bytes", () => {
    // U+65E5 encodes to 3 UTF-8 bytes but counts as 1 UTF-16 code unit,
    // so 50 of them is 50 characters (well under the character count)
    // but 150 bytes (over the server's byte bound).
    const symbol = "日".repeat(50);
    expect(symbol.length).toBe(50);
    expect(isSymbolQuery(symbol)).toBe(false);
  });

  it("accepts a multi-byte symbol comfortably under both the character and byte limits", () => {
    const symbol = "日".repeat(10);
    expect(symbol.length).toBe(10);
    expect(isSymbolQuery(symbol)).toBe(true);
  });
});

describe("symbolRefs store", () => {
  it("starts idle with no hits", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    expect(store.isActive()).toBe(false);
    expect(store.getStatus()).toBe("idle");
    expect(store.getHits()).toEqual([]);
    expect(store.getSearchedSha()).toBe("");
  });

  it("search records the sha it was searched against, and close() clears it", async () => {
    const store = createSymbolRefsStore({
      client: stubClient({
        GET: vi.fn(async () => ({ data: makeResponse("Foo", [makeHit()]), error: undefined })),
      }),
    });

    await store.search("acme", "widget", 7, "sha-1", "Foo");
    expect(store.getSearchedSha()).toBe("sha-1");

    store.close();
    expect(store.getSearchedSha()).toBe("");
  });

  it("a later search against a different sha overwrites the recorded one", async () => {
    const store = createSymbolRefsStore({
      client: stubClient({
        GET: vi.fn(async () => ({ data: makeResponse("Foo", [makeHit()]), error: undefined })),
      }),
    });

    await store.search("acme", "widget", 7, "sha-1", "Foo");
    expect(store.getSearchedSha()).toBe("sha-1");

    await store.search("acme", "widget", 7, "sha-2", "Bar");
    expect(store.getSearchedSha()).toBe("sha-2");
  });

  it("search loads then reports hits, totals, and truncated from the response", async () => {
    const hit = makeHit({ path: "b.go", line: 5, kind: "reference" });
    const get = vi.fn(async () => ({
      data: makeResponse("Foo", [hit], { in_pr_total: 3, outside_pr_total: 2, truncated: true }),
      error: undefined,
    }));
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    const pending = store.search("acme", "widget", 7, "deadbeef", "Foo");
    expect(store.getStatus()).toBe("loading");
    await pending;

    expect(get).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/symbol-refs",
      {
        params: {
          path: { owner: "acme", name: "widget", number: 7 },
          query: { q: "Foo", sha: "deadbeef" },
        },
      },
    );
    expect(store.getStatus()).toBe("ready");
    expect(store.getQuery()).toBe("Foo");
    expect(store.getHits()).toEqual([hit]);
    expect(store.getInPrTotal()).toBe(3);
    expect(store.getOutsidePrTotal()).toBe(2);
    expect(store.isTruncated()).toBe(true);
    expect(store.isActive()).toBe(true);
  });

  it("a failed request leaves status error with the server's detail and no stale hits", async () => {
    const get = vi
      .fn()
      .mockResolvedValueOnce({ data: makeResponse("Foo", [makeHit()]), error: undefined })
      .mockResolvedValueOnce({ data: undefined, error: { detail: "boom" } });
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    await store.search("acme", "widget", 7, "sha1", "Foo");
    expect(store.getHits()).toHaveLength(1);
    expect(store.getStatus()).toBe("ready");

    await store.search("acme", "widget", 7, "sha1", "Bar");
    expect(store.getStatus()).toBe("error");
    expect(store.getError()).toBe("boom");
    expect(store.getHits()).toEqual([]);
  });

  it("falls back to a generic message when the server error has no detail", async () => {
    const get = vi.fn(async () => ({ data: undefined, error: {} }));
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    await store.search("acme", "widget", 7, "sha1", "Foo");
    expect(store.getStatus()).toBe("error");
    expect(store.getError()).toBe("Symbol search failed");
  });

  it("treats a null hits payload as empty, matching the nullable generated type", async () => {
    const get = vi.fn(async () => ({
      data: makeResponse("Foo", null),
      error: undefined,
    }));
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    await store.search("acme", "widget", 7, "sha1", "Foo");

    expect(store.getStatus()).toBe("ready");
    expect(store.getHits()).toEqual([]);
    expect(store.getInPrTotal()).toBe(0);
    expect(store.getOutsidePrTotal()).toBe(0);
    expect(store.isTruncated()).toBe(false);
  });

  it("close returns the store to idle and empties the hits", async () => {
    const get = vi.fn(async () => ({
      data: makeResponse("Foo", [makeHit()]),
      error: undefined,
    }));
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });
    await store.search("acme", "widget", 7, "sha1", "Foo");
    expect(store.isActive()).toBe(true);

    store.close();

    expect(store.isActive()).toBe(false);
    expect(store.getStatus()).toBe("idle");
    expect(store.getHits()).toEqual([]);
    expect(store.getQuery()).toBe("");
  });

  it("close() while a search is in flight discards the late response", async () => {
    const d = deferred<{ data: SymbolRefsResponse; error: undefined }>();
    const get = vi.fn().mockReturnValueOnce(d.promise);
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    const pending = store.search("acme", "widget", 7, "sha1", "Foo");
    expect(store.getStatus()).toBe("loading");

    store.close();
    expect(store.getStatus()).toBe("idle");

    d.resolve({ data: makeResponse("Foo", [makeHit()]), error: undefined });
    await pending;

    expect(store.getStatus()).toBe("idle");
    expect(store.getHits()).toEqual([]);
    expect(store.getQuery()).toBe("");
    expect(store.isActive()).toBe(false);
  });

  it("discards an out-of-order response so an earlier search resolving last cannot clobber a later one", async () => {
    const dA = deferred<{ data: SymbolRefsResponse; error: undefined }>();
    const dB = deferred<{ data: SymbolRefsResponse; error: undefined }>();
    const get = vi.fn().mockReturnValueOnce(dA.promise).mockReturnValueOnce(dB.promise);
    const store = createSymbolRefsStore({ client: stubClient({ GET: get }) });

    const pA = store.search("acme", "widget", 7, "sha1", "Foo");
    const pB = store.search("acme", "widget", 7, "sha1", "Bar");

    // Resolve the newer search (B) first, then the older one (A) last —
    // the store must still show B's results.
    dB.resolve({ data: makeResponse("Bar", [makeHit({ path: "bar.go" })]), error: undefined });
    await pB;
    dA.resolve({ data: makeResponse("Foo", [makeHit({ path: "foo.go" })]), error: undefined });
    await pA;

    expect(store.getQuery()).toBe("Bar");
    expect(store.getHits()).toEqual([makeHit({ path: "bar.go" })]);
    expect(store.getStatus()).toBe("ready");
  });
});

describe("createSymbolRefsStore: openBlank", () => {
  it("moves an idle store to prompt and makes it active", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    expect(store.getStatus()).toBe("idle");
    expect(store.isActive()).toBe(false);

    store.openBlank();

    expect(store.getStatus()).toBe("prompt");
    expect(store.isActive()).toBe(true);
    expect(store.getQuery()).toBe("");
    expect(store.getHits()).toEqual([]);
  });

  it("bumps focusSeq on every call, including when already in prompt", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    const before = store.getFocusSeq();

    store.openBlank();
    const afterFirst = store.getFocusSeq();
    store.openBlank();

    expect(afterFirst).toBeGreaterThan(before);
    expect(store.getFocusSeq()).toBeGreaterThan(afterFirst);
  });

  // The whole point of openBlank: reaching for the search box must never
  // discard a result list the user is reading.
  it("leaves an existing result set completely untouched", async () => {
    const hits = [makeHit({ line: 7 })];
    const store = createSymbolRefsStore({
      client: stubClient({
        GET: vi.fn(async () => ({ data: makeResponse("Foo", hits), error: undefined })),
      }),
    });
    await store.search("o", "n", 1, "abc123", "Foo");
    expect(store.getStatus()).toBe("ready");

    const focusSeqBefore = store.getFocusSeq();
    store.openBlank();

    expect(store.getStatus()).toBe("ready");
    expect(store.getQuery()).toBe("Foo");
    expect(store.getHits()).toEqual(hits);
    expect(store.getInPrTotal()).toBe(1);
    expect(store.getFocusSeq()).toBeGreaterThan(focusSeqBefore);
  });

  it("returns to idle on close, so a later openBlank prompts again", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.openBlank();
    store.close();
    expect(store.getStatus()).toBe("idle");
    expect(store.isActive()).toBe(false);
  });
});

describe("createSymbolRefsStore: the back stack", () => {
  function pos(line: number): { path: string; line: number } {
    return { path: "a.go", line };
  }

  it("starts empty and pops nothing", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    expect(store.canGoBack()).toBe(false);
    expect(store.popPosition()).toBeNull();
  });

  it("pops in last-in-first-out order and drains to empty", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.pushPosition(pos(10));
    store.pushPosition(pos(20));

    expect(store.canGoBack()).toBe(true);
    expect(store.popPosition()).toEqual(pos(20));
    expect(store.popPosition()).toEqual(pos(10));
    expect(store.canGoBack()).toBe(false);
    expect(store.popPosition()).toBeNull();
  });

  it("keeps the newest 10 entries, dropping the oldest", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    for (let i = 1; i <= 12; i++) store.pushPosition(pos(i));

    const drained: number[] = [];
    for (;;) {
      const p = store.popPosition();
      if (p === null) break;
      drained.push(p.line);
    }

    // 1 and 2 fell off the bottom; the rest survive, newest first.
    expect(drained).toEqual([12, 11, 10, 9, 8, 7, 6, 5, 4, 3]);
  });

  it("preserves the side of a captured position", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.pushPosition({ path: "a.go", line: 5, side: "LEFT" });

    expect(store.popPosition()).toEqual({ path: "a.go", line: 5, side: "LEFT" });
  });

  it("clears the stack on close, since the gutter's trail dies with it", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.pushPosition(pos(10));

    store.close();

    expect(store.canGoBack()).toBe(false);
  });

  // The trail deliberately survives a new search: the positions are still
  // valid for the same SHA, and DiffView closes the store outright when
  // the scope or SHA moves -- which is what clears them.
  it("does NOT clear the stack on a new search", async () => {
    const store = createSymbolRefsStore({
      client: stubClient({
        GET: vi.fn(async () => ({ data: makeResponse("Foo", [makeHit()]), error: undefined })),
      }),
    });
    store.pushPosition(pos(10));

    await store.search("o", "n", 1, "abc123", "Foo");

    expect(store.canGoBack()).toBe(true);
    expect(store.popPosition()).toEqual(pos(10));
  });
});

// The launch point a selection-side search records, so the gutter's Back button
// can return to the highlighted symbol rather than to the viewport's midline.
describe("createSymbolRefsStore: the search origin", () => {
  const origin = { path: "src/v/kafka/handler.cc", line: 3227, side: "RIGHT" as const };

  it("starts empty and round-trips what was set", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    expect(store.getOrigin()).toBeNull();

    store.setOrigin(origin);

    expect(store.getOrigin()).toEqual(origin);
  });

  it("reading does not consume it -- only clearOrigin does", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.setOrigin(origin);

    expect(store.getOrigin()).toEqual(origin);
    expect(store.getOrigin()).toEqual(origin);
    store.clearOrigin();

    expect(store.getOrigin()).toBeNull();
  });

  // The toolbar button and the `s` key know no launch point, and a stale origin
  // from an earlier selection-side search would send Back to an unrelated
  // symbol.
  it("is cleared by openBlank", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.setOrigin(origin);

    store.openBlank();

    expect(store.getOrigin()).toBeNull();
  });

  it("is cleared by close", () => {
    const store = createSymbolRefsStore({ client: stubClient() });
    store.setOrigin(origin);

    store.close();

    expect(store.getOrigin()).toBeNull();
  });

  // Deliberate: re-querying from the gutter's own input does not change where
  // the reader began, so "return me to where I started" still holds.
  it("survives a search, so re-querying keeps the launch point", async () => {
    const store = createSymbolRefsStore({
      client: stubClient({
        GET: vi.fn(async () => ({ data: makeResponse("Foo", [makeHit()]), error: undefined })),
      }),
    });
    store.setOrigin(origin);

    await store.search("o", "n", 1, "abc123", "Foo");

    expect(store.getOrigin()).toEqual(origin);
  });
});
