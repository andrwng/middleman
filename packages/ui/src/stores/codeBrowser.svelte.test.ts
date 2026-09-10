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

  it("a failed blob fetch leaves status missing with an error message, not stale content", async () => {
    const client = stubClient();
    (client.GET as ReturnType<typeof vi.fn>).mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: undefined, error: { detail: "not found" } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/missing.txt");

    expect(store.status).toBe("missing");
    expect(store.error).toBe("not found");
    expect(store.content).toBeNull();
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
