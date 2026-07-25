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
