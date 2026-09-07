import { describe, expect, it, vi } from "vitest";
import { createReviewThreadsStore } from "./reviewThreads.svelte.js";
import type { MiddlemanClient } from "../types.js";
import type { ReviewThread } from "./reviewThreads.svelte.js";

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

// Builds a store pre-loaded with exactly the given threads, for the
// placement tests below where the anchor/resolution shape matters more
// than the load/API plumbing already covered above.
async function storeWith(threads: ReviewThread[]) {
  const client = stubClient({
    GET: vi.fn(async () => ({ data: { threads }, error: undefined })),
  });
  const store = createReviewThreadsStore({ client });
  await store.load("local", "demo", 1);
  return store;
}

describe("reviewThreads store", () => {
  it("loads threads for a local worktree and queries by anchor", async () => {
    const client = stubClient();
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    expect(client.GET).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: {} } },
    );
    expect(store.getThreads()).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 12, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 99, "RIGHT")).toHaveLength(0);
  });

  it("passes the caller's revision as the 'at' query param", async () => {
    const get = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const client = stubClient({ GET: get });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "deadbeef");
    expect(get).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "deadbeef" } } },
    );
  });

  it("refresh resolves against the same revision load() was given", async () => {
    // Decision: refresh() has no 'at' of its own (it re-reads silently
    // during a poll), so it must reuse the revision load() last resolved
    // against -- otherwise every moved card would snap back to its
    // recorded line mid-poll.
    const get = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const client = stubClient({ GET: get });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "deadbeef");
    await store.refresh();
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "deadbeef" } } },
    );
  });

  it("a load with no revision does not erase a previously remembered one", async () => {
    // The store is one shared instance across surfaces. DocReviewSurface
    // calls load() with no 'at' at all -- it has no opinion about
    // resolution -- and that call must not wipe out the revision the
    // diff surface asked refresh() to keep re-resolving against.
    const get = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const client = stubClient({ GET: get });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "deadbeef");
    await store.load("local", "demo", 7); // e.g. a surface with no opinion on 'at'
    await store.refresh();
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "deadbeef" } } },
    );
  });

  it("clear() resets the remembered revision so the next load starts clean", async () => {
    // Without this reset, switching PRs/worktrees would carry a stale
    // revision into the next one's refresh() calls -- clear() is the
    // right place to drop it, since a plain load() (no 'at') deliberately
    // does not (see the test above).
    const get = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const client = stubClient({ GET: get });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "deadbeef");
    store.clear();
    await store.load("local", "demo", 7);
    await store.refresh();
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: {} } },
    );
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

  it("createThreads forwards appended comments per draft", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    await store.createThreads(
      [{
        path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "q1",
        comments: [
          { author: "agent", body: "a1" },
          { author: "user", body: "q2" },
        ],
      }],
      "act-immediately",
    );
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      {
        params: { path: { owner: "local", name: "demo", number: 7 } },
        body: {
          mode: "act-immediately",
          threads: [{
            path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc", body: "q1",
            comments: [
              { author: "agent", body: "a1" },
              { author: "user", body: "q2" },
            ],
          }],
        },
      },
    );
  });

  it("unresolve POSTs to the unresolve endpoint and upserts the thread", async () => {
    const post = vi.fn(async () => ({ data: thread({ status: "open" }), error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.unresolve(1);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unresolve",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("open");
  });

  it("discuss POSTs to the discuss endpoint and replaces state", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread({ status: "discussed" })] }, error: undefined }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.discuss(1);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/discuss",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("discussed");
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

  it("refresh swallows errors and keeps prior state", async () => {
    const get = vi.fn()
      .mockResolvedValueOnce({ data: { threads: [thread()] }, error: undefined })
      .mockResolvedValueOnce({ data: undefined, error: { detail: "boom" } });
    const store = createReviewThreadsStore({ client: stubClient({ GET: get }) });
    await store.load("local", "demo", 7);
    await store.refresh();
    expect(store.getThreads()).toHaveLength(1); // unchanged
    expect(store.getError()).toBeNull(); // refresh is silent
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

  it("ask posts to the ask endpoint and upserts the returned thread", async () => {
    const post = vi.fn(async () => ({
      data: thread({ comments: [{ id: 1, author: "user", body: "why?", sent_to_agent: true, created_at: "" }] }),
      error: undefined,
    }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.ask(1, "why?");
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } }, body: { body: "why?" } },
    );
    expect(store.getThreads()[0]!.comments?.[0]?.sent_to_agent).toBe(true);
  });

  it("editComment posts to the edit endpoint and upserts the returned thread", async () => {
    const post = vi.fn(async () => ({
      data: thread({ comments: [{ id: 1, author: "user", body: "new", edited_at: "2026-07-28T00:00:00Z", created_at: "" }] }),
      error: undefined,
    }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.editComment(1, 1, "new");
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments/{comment_id}/edit",
      {
        params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1, comment_id: 1 } },
        body: { body: "new" },
      },
    );
    expect(store.getThreads()[0]!.comments?.[0]?.body).toBe("new");
    expect(store.getThreads()[0]!.comments?.[0]?.edited_at).toBe("2026-07-28T00:00:00Z");
  });

  it("editComment surfaces errors and returns false", async () => {
    const post = vi.fn(async () => ({ data: undefined, error: { detail: "cannot edit" } }));
    const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
    await store.load("local", "demo", 7);
    const ok = await store.editComment(1, 1, "new");
    expect(ok).toBe(false);
    expect(store.getError()).toBe("cannot edit");
  });
});

describe("placement by resolved anchor", () => {
  const base = {
    id: 1, path: "a.go", side: "RIGHT" as const, line: 5,
    commit_sha: "abc", status: "open", writes_allowed: false, hidden: false,
    created_at: "", updated_at: "", comments: [],
  };

  it("places a moved thread at its resolved line, not its recorded one", async () => {
    const store = await storeWith([
      { ...base, resolved: { state: "moved", path: "a.go", line: 8 } },
    ]);
    expect(store.getThreadsAtAnchor("a.go", 8, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(0);
  });

  it("follows a resolved rename to the new path", async () => {
    const store = await storeWith([
      { ...base, resolved: { state: "moved", path: "b.go", line: 8 } },
    ]);
    expect(store.getThreadsAtAnchor("b.go", 8, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 8, "RIGHT")).toHaveLength(0);
  });

  it("also takes the resolved position when the anchor is confirmed current", async () => {
    // A real "current" resolution always echoes the recorded anchor
    // exactly -- the state literally means "still here". This test gives
    // it a different line on purpose, so the assertion can tell "took the
    // current branch" apart from "silently fell through to the
    // no-resolution default", which would happen to agree by coincidence
    // if resolved and recorded matched.
    const store = await storeWith([
      { ...base, resolved: { state: "current", path: "a.go", line: 9 } },
    ]);
    expect(store.getThreadsAtAnchor("a.go", 9, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(0);
  });

  it("falls back to the recorded anchor when there is no resolution", async () => {
    const store = await storeWith([{ ...base }]);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(1);
  });

  // The spec's resolution table makes "removed" and "unmappable"
  // list-only, and these two tests previously asserted the opposite (a
  // card still drawn at the recorded line). That is the reported bug with
  // the server now knowing better: for a removed line, the recorded
  // number holds unrelated code by definition, so an unlabelled card
  // there is a confident lie. Never hide, always label means the thread
  // stays in the list, badged and openable in place -- not that a card
  // gets drawn on the wrong code.
  it("does not draw a removed thread's card on the code that took its place", async () => {
    const store = await storeWith([
      { ...base, resolved: { state: "removed" } },
    ]);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(0);
    // Still present in the list -- nothing is hidden.
    expect(store.getThreads()).toHaveLength(1);
    expect(store.isPlaceable(store.getThreads()[0]!)).toBe(false);
  });

  it("does not draw an unmappable thread's card either", async () => {
    const store = await storeWith([
      { ...base, resolved: { state: "unmappable" } },
    ]);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(0);
    expect(store.getThreads()).toHaveLength(1);
    expect(store.isPlaceable(store.getThreads()[0]!)).toBe(false);
  });

  it("still places a thread the server declined to resolve", async () => {
    // No `resolved` block at all means "no opinion", which must keep the
    // recorded anchor placeable -- that is what a LEFT-side thread, a
    // hidden thread, and every `at`-less load rely on.
    const store = await storeWith([{ ...base }]);
    expect(store.isPlaceable(store.getThreads()[0]!)).toBe(true);
    expect(store.getThreadsAtAnchor("a.go", 5, "RIGHT")).toHaveLength(1);
  });
});

describe("resolveAt", () => {
  const base = {
    id: 1, path: "a.go", side: "RIGHT" as const, line: 5,
    commit_sha: "abc", status: "open", writes_allowed: false, hidden: false,
    created_at: "", updated_at: "", comments: [],
  };

  function clientReturning(threads: ReviewThread[]) {
    const get = vi.fn(async () => ({ data: { threads }, error: undefined }));
    return { get, client: stubClient({ GET: get }) };
  }

  it("re-reads the loaded review against the new revision", async () => {
    const { get, client } = clientReturning([base]);
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    await store.resolveAt("newsha");
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "newsha" } } },
    );
  });

  it("makes the new revision stick for later refreshes", async () => {
    const { get, client } = clientReturning([base]);
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "oldsha");
    await store.resolveAt("newsha");
    await store.refresh();
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "newsha" } } },
    );
  });

  it("leaves the revision in effect alone when the caller has none", async () => {
    // "" means "I don't know the revision yet", not "resolve against
    // nothing" -- clearing it would snap every moved card back to its
    // recorded line.
    const { get, client } = clientReturning([base]);
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7, "oldsha");
    const before = get.mock.calls.length;
    await store.resolveAt("");
    expect(get.mock.calls.length).toBe(before); // no request at all
    await store.refresh();
    expect(get).toHaveBeenLastCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 }, query: { at: "oldsha" } } },
    );
  });

  it("does nothing for a non-local review", async () => {
    const { get, client } = clientReturning([base]);
    const store = createReviewThreadsStore({ client });
    await store.load("acme", "widgets", 7);
    const before = get.mock.calls.length;
    await store.resolveAt("newsha");
    expect(get.mock.calls.length).toBe(before);
  });
});
