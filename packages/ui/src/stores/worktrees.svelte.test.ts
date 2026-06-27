import { describe, expect, it, vi } from "vitest";
import { createWorktreesStore } from "./worktrees.svelte.js";
import type { MiddlemanClient } from "../types.js";

function stubClient(
  over: Partial<Record<"GET" | "POST" | "DELETE" | "PUT" | "PATCH", unknown>> = {},
): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    PUT: vi.fn(async () => ({ data: undefined, error: undefined })),
    PATCH: vi.fn(async () => ({ data: undefined, error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}

describe("worktrees store", () => {
  it("loadMarkdownFiles returns the file list", async () => {
    const client = stubClient({
      GET: vi.fn(async () => ({ data: { files: ["a.md", "b.md"] }, error: undefined })),
    });
    const store = createWorktreesStore({ client });
    const files = await store.loadMarkdownFiles(7);
    expect(files).toEqual(["a.md", "b.md"]);
  });

  it("loadMarkdownFiles returns [] on API error", async () => {
    const client = stubClient({
      GET: vi.fn(async () => ({ data: undefined, error: { detail: "not found" } })),
    });
    const store = createWorktreesStore({ client });
    const files = await store.loadMarkdownFiles(7);
    expect(files).toEqual([]);
  });

  it("loadMarkdownFiles returns [] on thrown error", async () => {
    const client = stubClient({
      GET: vi.fn(async () => { throw new Error("network failure"); }),
    });
    const store = createWorktreesStore({ client });
    const files = await store.loadMarkdownFiles(7);
    expect(files).toEqual([]);
  });
});
