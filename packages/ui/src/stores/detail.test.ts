import { describe, expect, it, vi } from "vitest";
import { createDetailStore, placeReviewComment, type PublishedReviewComment } from "./detail.svelte.js";
import type { MiddlemanClient } from "../types.js";
import type { PullDetail, PREvent, ReviewCommentAnchor } from "../api/types.js";

function makeReviewCommentEvent(opts: {
  id: number;
  platformId: number;
  inReplyTo?: number;
  createdAt?: string;
  path?: string;
  line?: number;
}): PREvent {
  const meta: Record<string, unknown> = {
    path: opts.path ?? "f.go",
    line: opts.line ?? 1,
    side: "RIGHT",
  };
  if (opts.inReplyTo) meta.in_reply_to = opts.inReplyTo;
  return {
    ID: opts.id,
    MergeRequestID: 1,
    PlatformID: opts.platformId,
    EventType: "review_comment",
    Author: "u",
    Summary: opts.path ?? "f.go",
    Body: "body",
    MetadataJSON: JSON.stringify(meta),
    CreatedAt: opts.createdAt ?? "2026-05-21T12:00:00Z",
    DedupeKey: `review-comment-${opts.platformId}`,
  } as PREvent;
}

function buildDetailWith(opts: {
  events: PREvent[];
  hiddenRootIds: number[];
}): PullDetail {
  return {
    merge_request: { Number: 1 } as PullDetail["merge_request"],
    events: opts.events,
    repo_owner: "acme",
    repo_name: "widget",
    platform_host: "github.com",
    worktree_links: [],
    workflow_approval: { checked: false, required: false, count: 0 },
    detail_loaded: true,
    hidden_thread_root_ids: opts.hiddenRootIds,
  } as PullDetail;
}

function makeStubClient(detail: PullDetail): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: detail, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

describe("detail store hidden-thread filter", () => {
  it("excludes hidden threads from getReviewCommentsByFilePath by default", async () => {
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 100 }), // hidden root
      makeReviewCommentEvent({ id: 2, platformId: 101, inReplyTo: 100 }),
      makeReviewCommentEvent({ id: 3, platformId: 200, path: "g.go" }), // visible
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [100] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    const map = store.getReviewCommentsByFilePath();
    expect(map.get("f.go")?.map((c) => c.id) ?? []).toEqual([]);
    expect(map.get("g.go")?.map((c) => c.id) ?? []).toEqual([200]);
  });

  it("includes hidden events with isHidden=true when toggle is on", async () => {
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 100 }),
      makeReviewCommentEvent({ id: 2, platformId: 101, inReplyTo: 100 }),
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [100] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    store.setShowHiddenThreads(true);
    const map = store.getReviewCommentsByFilePath();
    const onFile = map.get("f.go") ?? [];
    expect(onFile.map((c) => ({ id: c.id, isHidden: c.isHidden }))).toEqual([
      { id: 100, isHidden: true },
      { id: 101, isHidden: true },
    ]);
  });

  it("resolves multi-level replies to the correct root", async () => {
    // A → B → C, hide root A: all three should be hidden.
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 10 }),
      makeReviewCommentEvent({ id: 2, platformId: 11, inReplyTo: 10 }),
      makeReviewCommentEvent({ id: 3, platformId: 12, inReplyTo: 11 }),
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [10] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    const map = store.getReviewCommentsByFilePath();
    expect(map.get("f.go") ?? []).toEqual([]);
  });
});

describe("hide/unhide actions", () => {
  it("hideReviewThread mutates state optimistically and POSTs", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 50 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);

    await store.hideReviewThread(50);

    expect(store.getHiddenRootSet().has(50)).toBe(true);
    expect(client.POST).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
      expect.objectContaining({
        params: { path: { owner: "acme", name: "widget", number: 1 } },
        body: { root_comment_id: 50 },
      }),
    );
  });

  it("unhideReviewThread mutates state and DELETEs", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 60 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [60] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);

    await store.unhideReviewThread(60);

    expect(store.getHiddenRootSet().has(60)).toBe(false);
    expect(client.DELETE).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
      expect.objectContaining({
        params: {
          path: { owner: "acme", name: "widget", number: 1, root_comment_id: 60 },
        },
      }),
    );
  });

  it("hideReviewThread rolls back state on POST failure", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 70 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);
    // Set the rollback-triggering response after loadDetail so the
    // background sync POST (/sync) doesn't consume the one-shot.
    (client.POST as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      data: undefined,
      error: { detail: "boom" },
    });

    await store.hideReviewThread(70);

    expect(store.getHiddenRootSet().has(70)).toBe(false);
    expect(store.getDetailError()).toBe("boom");
  });
});

describe("showHiddenThreads lifecycle", () => {
  it("resets to off when the user navigates to another PR", async () => {
    const events: PREvent[] = [];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });

    await store.loadDetail("acme", "widget", 1);
    store.setShowHiddenThreads(true);
    expect(store.isShowingHiddenThreads()).toBe(true);

    await store.loadDetail("acme", "widget", 2);
    expect(store.isShowingHiddenThreads()).toBe(false);
  });

  it("also resets on clearDetail", async () => {
    const events: PREvent[] = [];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const store = createDetailStore({ client: makeStubClient(detail) });

    await store.loadDetail("acme", "widget", 1);
    store.setShowHiddenThreads(true);
    store.clearDetail();
    expect(store.isShowingHiddenThreads()).toBe(false);
  });
});

describe("placeReviewComment", () => {
  const c: PublishedReviewComment = {
    id: 1, author: "u", body: "b", createdAt: "", path: "a.go", line: 3, startLine: 2,
    side: "RIGHT", commitId: "c1", htmlUrl: "", inReplyTo: 0, isHidden: false,
  };
  const cases: [string, PublishedReviewComment, ReviewCommentAnchor | undefined, ReturnType<typeof placeReviewComment>][] = [
    ["no resolution keeps the recorded anchor", c, undefined, { path: "a.go", line: 3, startLine: 2 }],
    ["moved follows the resolution", c, { comment_id: 1, state: "moved", path: "b.go", line: 9, start_line: 8 }, { path: "b.go", line: 9, startLine: 8 }],
    ["a collapsed range drops its start", c, { comment_id: 1, state: "moved", path: "a.go", line: 9 }, { path: "a.go", line: 9, startLine: null }],
    ["current follows the resolution", c, { comment_id: 1, state: "current", path: "a.go", line: 3, start_line: 2 }, { path: "a.go", line: 3, startLine: 2 }],
    ["removed has no line", c, { comment_id: 1, state: "removed" }, null],
    ["unmappable has no line", c, { comment_id: 1, state: "unmappable" }, null],
    ["no recorded line and no resolution has no line", { ...c, line: 0 }, undefined, null],
  ];
  it.each(cases)("%s", (_name, comment, anchor, want) => {
    expect(placeReviewComment(comment, anchor)).toEqual(want);
  });
});

describe("review comment anchors", () => {
  // Answers the detail endpoint with `detail` and the anchors endpoint with
  // whatever `anchorsAt` returns for the requested revision.
  function routingClient(
    detail: PullDetail,
    anchorsAt: (at: string) => Promise<ReviewCommentAnchor[]>,
  ): MiddlemanClient {
    return {
      GET: vi.fn(async (path: string, opts: { params: { query?: { at?: string } } }) => {
        if (path.endsWith("/review-comment-anchors")) {
          const at = opts.params.query?.at ?? "";
          return { data: { at, anchors: await anchorsAt(at) }, error: undefined };
        }
        return { data: detail, error: undefined };
      }),
      POST: vi.fn(async () => ({ data: undefined, error: undefined })),
      DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    } as unknown as MiddlemanClient;
  }

  async function loadedStore(anchorsAt: (at: string) => Promise<ReviewCommentAnchor[]>) {
    const detail = buildDetailWith({
      events: [makeReviewCommentEvent({ id: 1, platformId: 100, line: 3 })],
      hiddenRootIds: [],
    });
    const store = createDetailStore({ client: routingClient(detail, anchorsAt) });
    await store.loadDetail("acme", "widget", 1);
    const comment = store.getReviewCommentsByFilePath().get("f.go")![0]!;
    return { store, comment };
  }

  it("places a comment at its resolution for the revision on screen", async () => {
    const { store, comment } = await loadedStore(async () => [
      { comment_id: 100, state: "moved", path: "f.go", line: 4 },
    ]);
    expect(store.getReviewCommentPlacement(comment)).toEqual({ path: "f.go", line: 3, startLine: null });

    await store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-b");

    expect(store.getReviewCommentAnchor(100)?.state).toBe("moved");
    expect(store.getReviewCommentPlacement(comment)).toEqual({ path: "f.go", line: 4, startLine: null });
  });

  it("ignores a resolution for a revision the reader has left", async () => {
    let releaseOld!: () => void;
    const old = new Promise<void>((r) => { releaseOld = r; });
    const { store, comment } = await loadedStore(async (at) => {
      if (at === "sha-old") {
        await old;
        return [{ comment_id: 100, state: "removed" }];
      }
      return [{ comment_id: 100, state: "moved", path: "f.go", line: 7 }];
    });

    const slow = store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-old");
    await store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-new");
    releaseOld();
    await slow;

    expect(store.getReviewCommentPlacement(comment)?.line).toBe(7);
  });

  it("shows recorded lines while a new revision's resolution is in flight", async () => {
    let release!: () => void;
    const pending = new Promise<void>((r) => { release = r; });
    const { store, comment } = await loadedStore(async (at) => {
      if (at === "sha-b") await pending;
      return [{ comment_id: 100, state: "moved", path: "f.go", line: at === "sha-a" ? 5 : 6 }];
    });
    await store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-a");
    expect(store.getReviewCommentPlacement(comment)?.line).toBe(5);

    const inFlight = store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-b");
    expect(store.getReviewCommentPlacement(comment)?.line).toBe(3);

    release();
    await inFlight;
    expect(store.getReviewCommentPlacement(comment)?.line).toBe(6);
  });

  it("forgets resolutions when another pull request loads", async () => {
    const { store } = await loadedStore(async () => [{ comment_id: 100, state: "removed" }]);
    await store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-b");
    expect(store.getReviewCommentAnchor(100)).toBeDefined();

    await store.loadDetail("acme", "widget", 2);

    expect(store.getReviewCommentAnchor(100)).toBeUndefined();
  });

  it("forgets resolutions when the detail is cleared", async () => {
    const { store } = await loadedStore(async () => [{ comment_id: 100, state: "removed" }]);
    await store.resolveReviewCommentAnchors("acme", "widget", 1, "sha-b");
    expect(store.getReviewCommentAnchor(100)).toBeDefined();

    store.clearDetail();

    expect(store.getReviewCommentAnchor(100)).toBeUndefined();
  });

  it("does not ask for local worktrees", async () => {
    const anchorsAt = vi.fn(async () => []);
    const { store } = await loadedStore(anchorsAt);
    await store.resolveReviewCommentAnchors("local", "wt", 1, "sha-b");
    expect(anchorsAt).not.toHaveBeenCalled();
  });
});
