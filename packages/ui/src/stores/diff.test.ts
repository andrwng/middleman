import { afterEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import type { MiddlemanClient } from "../types.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("clearDraftCommentsForPath", () => {
  it("clearDraftCommentsForPath removes only that path's drafts", () => {
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("local", "demo", 1);
    store.addDraftComment({ path: "a.md", line: 1, side: "RIGHT", commitSha: "WORKING-TREE", body: "a1" });
    store.addDraftComment({ path: "a.md", line: 2, side: "RIGHT", commitSha: "WORKING-TREE", body: "a2" });
    store.addDraftComment({ path: "b.md", line: 1, side: "RIGHT", commitSha: "WORKING-TREE", body: "b1" });

    store.clearDraftCommentsForPath("a.md");

    expect(store.getDraftCommentsForPath("a.md")).toHaveLength(0);
    expect(store.getDraftCommentsForPath("b.md")).toHaveLength(1);
    expect(store.getDraft().comments).toHaveLength(1);
  });
});

describe("clearDiff", () => {
  it("clears a stale, unclaimed reveal target so it cannot survive into the next PR", () => {
    // revealTarget is keyed only by (path, line) with no PR/owner scope
    // of its own — the store is an app-wide singleton, reused as-is
    // across PRs. If a jump on PR #1 never resolved (e.g. the file
    // never mounted before the reviewer navigated away), the target
    // must not still be armed once DiffView unmounts and clearDiff()
    // runs, or an unrelated file in the next PR that happens to share
    // the same line number would spontaneously expand and flash.
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    store.requestRevealLine("src/foo.ts", 42);
    expect(store.getRevealTarget()).toEqual({ path: "src/foo.ts", line: 42 });

    store.clearDiff();

    expect(store.getRevealTarget()).toBeNull();
  });
});
