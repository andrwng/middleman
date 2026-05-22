import { describe, expect, it, vi } from "vitest";
import { createDetailStore } from "./detail.svelte.js";
import type { MiddlemanClient } from "../types.js";
import type { PullDetail, PREvent } from "../api/types.js";

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
