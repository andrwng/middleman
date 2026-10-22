import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import { installDraftServer, stubClient } from "./testDraftServer.js";

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

const draftInput = { path: "a.go", line: 1, side: "RIGHT" as const, commitSha: "abc", body: "hi" };

const legacyPayload = JSON.stringify({
  "acme/widget#1": {
    body: "",
    event: "COMMENT",
    comments: [{ id: "legacy-1", path: "a.go", line: 2, side: "RIGHT", commitSha: "abc", body: "old", createdAt: "2026-01-01T00:00:00.000Z" }],
  },
});

describe("diff store drafts persistence", () => {
  it("creates drafts on the server and reloads them in a fresh store", async () => {
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    const c = store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")?.map((d) => d.id)).toEqual([c.id]);

    const fresh = createDiffStore({ client: stubClient() });
    fresh.setActivePR("acme", "widget", 1);
    await fresh.whenDraftsSettled();
    expect(fresh.getDraft().comments.map((x) => x.id)).toEqual([c.id]);
  });

  it("remove waits for pending create", async () => {
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const release = server.holdCreates();
    const c = store.addDraftComment(draftInput);
    store.removeDraftComment(c.id);
    expect(store.getDraft().comments).toHaveLength(0);
    release();
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1") ?? []).toHaveLength(0);
    const posts = server.calls
      .filter((x) => x.url.includes("/review-drafts") && x.method === "POST")
      .map((x) => (x.url.endsWith("/delete") ? "delete" : "create"));
    expect(posts).toEqual(["create", "delete"]);
  });

  it("stale load is ignored", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [{ id: "old", path: "a.go", line: 1, side: "RIGHT", commit_sha: "s", body: "x", origin: "user", state: "draft", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }]);
    const release = server.holdList("acme/widget#1");
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    store.setActivePR("acme", "widget", 2);
    await new Promise((r) => setTimeout(r, 0));
    release();
    await store.whenDraftsSettled();
    expect(store.getDraft().comments).toHaveLength(0);
    // The late PR 1 response was dropped rather than written into state.
    expect(store.hasDraftForPR("acme", "widget", 1)).toBe(false);
  });

  it("persists the review summary and event", async () => {
    vi.useFakeTimers();
    try {
      const server = installDraftServer();
      const store = createDiffStore({ client: stubClient() });
      store.setActivePR("acme", "widget", 1);
      store.setDraftBody("summary");
      store.setDraftEvent("APPROVE");
      await vi.advanceTimersByTimeAsync(500);
      await store.whenDraftsSettled();
      expect(server.metas.get("acme/widget#1")).toEqual({ body: "summary", event: "APPROVE" });
    } finally {
      vi.useRealTimers();
    }
  });

  it("import is idempotent and clears the key only on success", async () => {
    localStorage.setItem("diff-draft-reviews", legacyPayload);
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getDraft().comments.map((c) => c.id)).toEqual(["legacy-1"]);
    expect(localStorage.getItem("diff-draft-reviews")).toBeNull();

    // A second import of the same payload (another tab) does not duplicate.
    localStorage.setItem("diff-draft-reviews", legacyPayload);
    const again = createDiffStore({ client: stubClient() });
    again.setActivePR("acme", "widget", 1);
    await again.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")).toHaveLength(1);
  });

  it("keeps the legacy key when the import fails", async () => {
    localStorage.setItem("diff-draft-reviews", legacyPayload);
    globalThis.fetch = vi.fn(async () => new Response("{}", { status: 500 })) as unknown as typeof fetch;
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(localStorage.getItem("diff-draft-reviews")).not.toBeNull();
    expect(store.getDraftSyncError()).not.toBeNull();
  });

  it("keeps a draft whose create failed and marks it unsaved", async () => {
    const server = installDraftServer();
    server.failCreates(1);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const c = store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    await store.reloadDrafts();
    const kept = store.getDraft().comments.find((x) => x.id === c.id);
    expect(kept).toBeDefined();
    expect(kept!.syncError).toContain("database is locked");
  });

  it("publish saves unsaved drafts before publishing", async () => {
    const server = installDraftServer();
    server.failCreates(1);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const c = store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    const result = await store.publishDraft({ event: "COMMENT", body: "" });
    expect(result).toEqual({ ok: true });
    expect(server.publishCount()).toBe(1);
    const created = server.calls.filter((x) => x.method === "POST" && x.url.endsWith("/review-drafts"));
    expect(created.map((x) => (x.body as { drafts: { id: string }[] }).drafts[0]!.id)).toEqual([c.id, c.id]);
  });

  it("publish refuses while a draft cannot be saved", async () => {
    const server = installDraftServer();
    server.failCreates(10);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    const result = await store.publishDraft({ event: "COMMENT", body: "" });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error).toMatch(/could not be saved/);
    expect(server.publishCount()).toBe(0);
    expect(store.getDraft().comments).toHaveLength(1);
  });

  it("editing an unsaved draft keeps the edit for the retry instead of PATCHing", async () => {
    const server = installDraftServer();
    server.failCreates(1);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const c = store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    store.updateDraftCommentBody(c.id, "edited");
    await store.whenDraftsSettled();
    expect(server.calls.some((x) => x.method === "PATCH")).toBe(false);
    await store.publishDraft({ event: "COMMENT", body: "" });
    const retried = server.calls.filter((x) => x.method === "POST" && x.url.endsWith("/review-drafts")).at(-1)!;
    expect((retried.body as { drafts: { body: string }[] }).drafts[0]!.body).toBe("edited");
  });

  it("import drops PRs that no longer exist and keeps only transient failures", async () => {
    localStorage.setItem("diff-draft-reviews", JSON.stringify({
      "acme/widget#1": { body: "", event: "COMMENT", comments: [{ id: "ok-1", path: "a.go", line: 1, side: "RIGHT", commitSha: "abc", body: "ok", createdAt: "2026-01-01T00:00:00.000Z" }] },
      "local/gone#9": { body: "", event: "COMMENT", comments: [{ id: "gone-1", path: "a.go", line: 1, side: "RIGHT", commitSha: "abc", body: "gone", createdAt: "2026-01-01T00:00:00.000Z" }] },
      "acme/flaky#2": { body: "", event: "COMMENT", comments: [{ id: "flaky-1", path: "a.go", line: 1, side: "RIGHT", commitSha: "abc", body: "flaky", createdAt: "2026-01-01T00:00:00.000Z" }] },
    }));
    const server = installDraftServer();
    server.keyStatus.set("local/gone#9", 404);
    server.keyStatus.set("acme/flaky#2", 500);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const left = JSON.parse(localStorage.getItem("diff-draft-reviews") ?? "{}") as Record<string, unknown>;
    expect(Object.keys(left)).toEqual(["acme/flaky#2"]);

    // A deleted draft from a PR that already imported is not resurrected.
    store.removeDraftComment("ok-1");
    await store.whenDraftsSettled();
    const again = createDiffStore({ client: stubClient() });
    again.setActivePR("acme", "widget", 1);
    await again.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1") ?? []).toHaveLength(0);
  });

  it("a load in flight does not restore a draft deleted or edited meanwhile", async () => {
    const server = installDraftServer();
    const row = (id: string, body: string) => ({ id, path: "a.go", line: 1, side: "RIGHT", commit_sha: "s", body, origin: "user", state: "draft", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" });
    server.drafts.set("acme/widget#1", [row("x", "x"), row("y", "y")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.setActivePR("acme", "widget", 2);
    await store.whenDraftsSettled();

    const release = server.holdList("acme/widget#1");
    store.setActivePR("acme", "widget", 1);
    await new Promise((r) => setTimeout(r, 0)); // list request captured its snapshot
    store.removeDraftComment("x");
    store.updateDraftCommentBody("y", "y edited");
    await new Promise((r) => setTimeout(r, 0));
    release();
    await store.whenDraftsSettled();
    const comments = store.getDraft().comments;
    expect(comments.map((c) => c.id)).toEqual(["y"]);
    expect(comments[0]!.body).toBe("y edited");
  });

  it("reloading the same PR's diff reloads its drafts", async () => {
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    await store.loadDiff("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getDraft().comments).toHaveLength(0);
    // Another tab, or a local branch switch, changed what the server holds.
    server.drafts.set("acme/widget#1", [{ id: "z", path: "a.go", line: 1, side: "RIGHT", commit_sha: "s", body: "z", origin: "user", state: "draft", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }]);
    await store.loadDiff("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getDraft().comments.map((c) => c.id)).toEqual(["z"]);
  });
});
