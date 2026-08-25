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
