import { describe, expect, it, vi } from "vitest";
import { createCodeBrowserStore } from "./codeBrowser.svelte.js";
import type { MiddlemanClient } from "../types.js";

// Mirrors the local stubClient() convention used by symbolRefs.svelte.test.ts
// (and the other store tests) -- there is no shared testing helper module in
// this package, so each store's test file defines its own. PUT is added here
// since this store, unlike symbolRefs, persists a bookmark.
function stubClient(
  over: Partial<Record<"GET" | "POST" | "PUT" | "DELETE", unknown>> = {},
): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    PUT: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}

describe("codeBrowser store", () => {
  it("starts idle and closed", () => {
    const store = createCodeBrowserStore({ client: stubClient() });
    expect(store.isOpen).toBe(false);
    expect(store.path).toBeNull();
    expect(store.status).toBe("idle");
    expect(store.entriesByDir.size).toBe(0);
  });

  it("seeds path from the active diff file on first open", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hello", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.path).toBe("src/active.txt");
    expect(store.isOpen).toBe(true);
    expect(store.content).toBe("hello");
    expect(store.status).toBe("ready");
  });

  it("uses the stored bookmark over the active file when one exists", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "src/bookmarked.txt" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.path).toBe("src/bookmarked.txt");
  });

  // The bug this pins: a caller that passes a specific, deliberately-chosen
  // path (e.g. "browse this hit" from the symbol-refs gutter) must not have
  // it silently overridden by a stale bookmark left over from ordinary
  // browsing of the same PR.
  it("forcePath bypasses an existing bookmark, seeding at activeFilePath instead", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "src/bookmarked.txt" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hit content", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/hit.txt", { forcePath: true });

    expect(store.path).toBe("src/hit.txt");
    expect(store.content).toBe("hit content");
  });

  it("loads the root directory listing into entriesByDir on open", async () => {
    const entries = [{ name: "src", path: "src", type: "dir" }];
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries } };
      if (path.includes("/blob")) return { data: { content: "", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.entriesByDir.get("")).toEqual(entries);
  });

  it("navigateTo persists the new path via PUT and loads its content", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "new content", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });
    (client.PUT as ReturnType<typeof vi.fn>).mockResolvedValue({
      data: { path: "src/new.txt" },
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");
    await store.navigateTo("src/new.txt");

    expect(store.path).toBe("src/new.txt");
    expect(store.content).toBe("new content");
    expect(client.PUT).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      expect.objectContaining({
        params: { path: { owner: "acme", name: "widget", number: 1 } },
        body: { path: "src/new.txt" },
      }),
    );
  });

  it("a 404 blob fetch leaves status missing with an error message, not stale content", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) {
        return { data: undefined, error: { detail: "not found" }, response: { status: 404 } };
      }
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/missing.txt");

    expect(store.status).toBe("missing");
    expect(store.error).toBe("not found");
    expect(store.content).toBeNull();
  });

  // A non-404 failure (network error, 502, 400 -- the server rejecting the
  // request rather than reporting the file absent at this SHA) is a real
  // error, not "missing", and must render distinctly. Before this fix,
  // status="error" was never actually reachable: every loadFile failure
  // fell through to "missing" regardless of cause.
  it("a non-404 blob failure sets status error, distinct from a missing file", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) {
        return {
          data: undefined,
          error: { detail: "read blob: exit status 128" },
          response: { status: 502 },
        };
      }
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/broken.txt");

    expect(store.status).toBe("error");
    expect(store.error).toBe("read blob: exit status 128");
    expect(store.content).toBeNull();
  });

  // A response with no explicit status (e.g. a thrown network error the
  // client surfaces as {error} with no {response}) also counts as a real
  // error rather than "missing" -- only an actual 404 means "missing".
  it("a blob failure with no response status also sets status error", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: undefined, error: { detail: "network error" } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/broken.txt");

    expect(store.status).toBe("error");
  });

  // Finding 2: /blob's truncated flag was previously ignored entirely --
  // loadFile read only data.content and reported "ready", so an
  // over-the-cap file silently rendered as a blank pane.
  it("a truncated blob response sets status truncated and clears content", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "", truncated: true } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/huge.bin");

    expect(store.status).toBe("truncated");
    expect(store.content).toBeNull();
  });

  // Finding 3 (loadTree half): a failed root-tree fetch previously no-op'd
  // silently, rendering indistinguishable from a genuinely empty directory.
  it("a failed tree fetch sets a real error status rather than silently rendering empty", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) {
        return { data: undefined, error: { detail: "tree not found" }, response: { status: 502 } };
      }
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.status).toBe("error");
    expect(store.error).toBe("tree not found");
    expect(store.entriesByDir.size).toBe(0);
  });

  // Finding 3 (sequencing race): stepping a commit fires a second open()
  // while the first is still in flight (sha and the active file both
  // change together in the normal flow). The first open's blob fetch is
  // held pending here to simulate it resolving AFTER the second, newer
  // open() has already completed -- the newer open's result must win, not
  // whichever request happens to land last.
  it("a stale open() superseded by a newer one does not clobber the newer result", async () => {
    const client = stubClient();
    let blobCalls = 0;
    let resolveFirstBlob: () => void = () => {};
    const firstBlobGate = new Promise<void>((res) => {
      resolveFirstBlob = res;
    });
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) {
        blobCalls++;
        if (blobCalls === 1) {
          // The FIRST (stale) open's blob fetch stays pending until told
          // to resolve below, well after the second open() has finished.
          await firstBlobGate;
          return { data: { content: "stale content", truncated: false } };
        }
        return { data: { content: "fresh content", truncated: false } };
      }
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    const first = store.open("acme", "widget", 1, "sha1", "src/one.txt");
    // Let the first open() run through code-browser-state, its tree
    // fetch, and reach (and block on) its blob fetch before the second
    // open() starts -- mirroring "the first request is genuinely older
    // and still outstanding" rather than a same-tick double-call.
    while (blobCalls === 0) {
      await Promise.resolve();
    }

    const second = store.open("acme", "widget", 1, "sha2", "src/two.txt");
    await second;

    // Only now does the first (stale) open's blob fetch resolve.
    resolveFirstBlob();
    await first;

    expect(store.path).toBe("src/two.txt");
    expect(store.content).toBe("fresh content");
    expect(store.status).toBe("ready");
  });

  // Finding 4: open() must auto-expand every ancestor directory of the
  // opened path (and load their listings) so the tree renders already
  // revealed down to the file, rather than starting fully collapsed
  // regardless of where the seeded path lives.
  it("auto-expands and loads every ancestor directory of the opened path", async () => {
    const client = stubClient();
    const treePaths: string[] = [];
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(
      async (path: string, opts: { params: { query: { path: string } } }) => {
        if (path.includes("code-browser-state")) return { data: { path: "" } };
        if (path.includes("/tree")) {
          treePaths.push(opts.params.query.path);
          return { data: { path: opts.params.query.path, entries: [] } };
        }
        if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
        throw new Error(`unexpected GET ${path}`);
      },
    );

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "a/b/c.go");

    expect(treePaths).toEqual(["", "a", "a/b"]);
    expect(store.expandedDirs).toEqual(new Set(["a", "a/b"]));
  });

  it("a root-level file expands only the root (no ancestor directories)", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "README.md");

    expect(store.expandedDirs.size).toBe(0);
  });

  it("toggleDir expands/collapses and lazily loads a directory's children once", async () => {
    const client = stubClient();
    let treeCalls = 0;
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) {
        treeCalls++;
        return { data: { path: "", entries: [] } };
      }
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "README.md");
    const callsAfterOpen = treeCalls;

    store.toggleDir("src");
    await Promise.resolve();
    expect(store.expandedDirs.has("src")).toBe(true);
    expect(treeCalls).toBe(callsAfterOpen + 1);

    store.toggleDir("src");
    expect(store.expandedDirs.has("src")).toBe(false);
    // Re-collapsing must not have refetched.
    expect(treeCalls).toBe(callsAfterOpen + 1);
  });

  it("listAllFiles requests a recursive tree listing and returns flat file paths", async () => {
    const client = stubClient();
    let recursiveQuery: unknown;
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(
      async (path: string, opts: { params: { query: Record<string, unknown> } }) => {
        if (path.includes("code-browser-state")) return { data: { path: "" } };
        if (path.includes("/tree")) {
          if (opts.params.query.recursive) {
            recursiveQuery = opts.params.query;
            return {
              data: {
                path: "",
                entries: [
                  { name: "handler.go", path: "internal/handler.go", type: "file" },
                  { name: "README.md", path: "README.md", type: "file" },
                ],
              },
            };
          }
          return { data: { path: "", entries: [] } };
        }
        if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
        throw new Error(`unexpected GET ${path}`);
      },
    );

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "README.md");

    const files = await store.listAllFiles();

    expect(files).toEqual(["internal/handler.go", "README.md"]);
    expect(recursiveQuery).toMatchObject({ sha: "deadbeef", recursive: true });
  });

  it("listAllFiles caches its result per sha, refetching only when sha changes", async () => {
    const client = stubClient();
    let recursiveCalls = 0;
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(
      async (path: string, opts: { params: { query: Record<string, unknown> } }) => {
        if (path.includes("code-browser-state")) return { data: { path: "" } };
        if (path.includes("/tree")) {
          if (opts.params.query.recursive) {
            recursiveCalls++;
            return { data: { path: "", entries: [{ name: "a.go", path: "a.go", type: "file" }] } };
          }
          return { data: { path: "", entries: [] } };
        }
        if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
        throw new Error(`unexpected GET ${path}`);
      },
    );

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "sha1", "a.go");

    await store.listAllFiles();
    await store.listAllFiles();
    expect(recursiveCalls).toBe(1);

    // A commit step changes sha -- the cache must not serve a stale list.
    await store.open("acme", "widget", 1, "sha2", "a.go");
    await store.listAllFiles();
    expect(recursiveCalls).toBe(2);
  });

  it("close resets isOpen, path, and content", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");
    expect(store.isOpen).toBe(true);

    store.close();

    expect(store.isOpen).toBe(false);
    expect(store.path).toBeNull();
    expect(store.content).toBeNull();
    expect(store.status).toBe("idle");
  });
});
