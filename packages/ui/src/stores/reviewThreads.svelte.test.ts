import { describe, expect, it, vi } from "vitest";
import { createReviewThreadsStore } from "./reviewThreads.svelte.js";
import type { MiddlemanClient } from "../types.js";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 1, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [{ id: 1, author: "user", body: "root", created_at: "" }],
    ...over,
  };
}

function stubClient(
  over: Partial<Record<"GET" | "POST" | "DELETE", unknown>> = {},
): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined })),
    POST: vi.fn(async () => ({ data: thread(), error: undefined })),
    DELETE: vi.fn(async () => ({ data: { threads: [] }, error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}

describe("reviewThreads store", () => {
  it("loads threads for a local worktree and queries by anchor", async () => {
    const client = stubClient();
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    expect(client.GET).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 } } },
    );
    expect(store.getThreads()).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 12, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 99, "RIGHT")).toHaveLength(0);
  });

  it("does not call the API for non-local sources", async () => {
    const client = stubClient();
    const store = createReviewThreadsStore({ client });
    await store.load("acme", "widget", 1);
    expect(client.GET).not.toHaveBeenCalled();
    expect(store.getThreads()).toHaveLength(0);
  });

  it("createThreads maps drafts to the request body and replaces state", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread(), thread({ id: 2, path: "b.go" })] }, error: undefined }));
    const client = stubClient({ POST: post });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    const ok = await store.createThreads([
      { path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "rename" },
      { path: "b.go", side: "RIGHT", line: 3, startLine: 1, commitSha: "abc", body: "extract" },
    ]);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      {
        params: { path: { owner: "local", name: "demo", number: 7 } },
        body: { threads: [
          { path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc", body: "rename" },
          { path: "b.go", side: "RIGHT", line: 3, start_line: 1, commit_sha: "abc", body: "extract" },
        ] },
      },
    );
    expect(store.getThreads()).toHaveLength(2);
  });

  it("addComment/resolve upsert the returned thread", async () => {
    const post = vi.fn(async () => ({ data: thread({ status: "resolved" }), error: undefined }));
    const client = stubClient({ POST: post });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    const ok = await store.resolve(1);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("resolved");
  });

  it("surfaces API errors", async () => {
    const client = stubClient({ GET: vi.fn(async () => ({ data: undefined, error: { detail: "boom" } })) });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    expect(store.getError()).toBe("boom");
  });

  it("createThreads forwards a mode", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    await store.createThreads(
      [{ path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "x" }],
      "discuss-first",
    );
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      {
        params: { path: { owner: "local", name: "demo", number: 7 } },
        body: {
          mode: "discuss-first",
          threads: [{ path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc", body: "x" }],
        },
      },
    );
  });

  it("apply posts to the apply endpoint and replaces state", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread({ status: "applied" })] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.apply(1);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("applied");
  });

  it("applyAll posts to apply-all and replaces state", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread({ status: "applied" })] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.applyAll();
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all",
      { params: { path: { owner: "local", name: "demo", number: 7 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("applied");
  });

  it("deleteThread DELETEs and replaces state with the remaining list", async () => {
    const del = vi.fn(async () => ({ data: { threads: [] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ DELETE: del }) });
    await store.load("local", "demo", 7);
    const ok = await store.deleteThread(1);
    expect(ok).toBe(true);
    expect(del).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()).toHaveLength(0);
  });

  it("refresh re-reads threads without toggling loading", async () => {
    const get = vi.fn()
      .mockResolvedValueOnce({ data: { threads: [thread()] }, error: undefined })
      .mockResolvedValueOnce({ data: { threads: [thread(), thread({ id: 2 })] }, error: undefined });
    const store = createReviewThreadsStore({ client: stubClient({ GET: get }) });
    await store.load("local", "demo", 7);
    expect(store.getThreads()).toHaveLength(1);
    await store.refresh();
    expect(store.getThreads()).toHaveLength(2);
    expect(store.isLoading()).toBe(false);
  });
});
