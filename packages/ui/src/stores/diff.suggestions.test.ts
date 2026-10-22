import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import { installDraftServer, stubClient } from "./testDraftServer.js";

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

const sug = (id: string, path = "a.go", line = 1) => ({
  id, path, line, side: "RIGHT", commit_sha: "s", body: `sug ${id}`, origin: "claude", state: "suggestion",
  rationale: `why ${id}`, run_id: 1, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
});

describe("suggestions in the diff store", () => {
  it("loads suggestions apart from drafts and keeps them out of hasDraftForPR", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1"), sug("s2", "b.go")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getDraft().comments).toHaveLength(0);
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["s1", "s2"]);
    expect(store.getSuggestionsForPath("b.go").map((s) => s.rationale)).toEqual(["why s2"]);
    expect(store.hasDraftForPR("acme", "widget", 1)).toBe(false);
  });

  it("accepting moves a suggestion into drafts on the server", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.acceptSuggestion("s1");
    expect(store.getSuggestions()).toHaveLength(0);
    expect(store.getDraft().comments.map((c) => [c.id, c.origin])).toEqual([["s1", "claude"]]);
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")![0]!.state).toBe("draft");
  });

  it("a failed accept puts the suggestion back", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    server.failAction("accept");
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.acceptSuggestion("s1");
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["s1"]);
    expect(store.getDraft().comments).toHaveLength(0);
    expect(store.getDraftSyncError()).not.toBeNull();
  });

  it("discard can be undone", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1"), sug("s2")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.discardAllSuggestions();
    expect(store.getSuggestions()).toHaveLength(0);
    expect(store.getLastDiscarded().map((s) => s.id)).toEqual(["s1", "s2"]);
    await store.whenDraftsSettled();
    store.undoDiscard();
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id).sort()).toEqual(["s1", "s2"]);
    expect(server.drafts.get("acme/widget#1")!.every((d) => d.state === "suggestion")).toBe(true);
  });

  it("editing a suggestion keeps it a suggestion", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.updateSuggestionBody("s1", "better wording");
    await store.whenDraftsSettled();
    expect(store.getSuggestions()[0]!.body).toBe("better wording");
    expect(server.drafts.get("acme/widget#1")![0]!.state).toBe("suggestion");
    expect(server.drafts.get("acme/widget#1")![0]!.body).toBe("better wording");
  });

  it("undo belongs to the PR the suggestions were discarded on", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    server.drafts.set("acme/widget#2", [sug("t1")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.discardSuggestion("s1");
    await store.whenDraftsSettled();

    store.setActivePR("acme", "widget", 2);
    await store.whenDraftsSettled();
    expect(store.getLastDiscarded()).toHaveLength(0);
    store.undoDiscard(); // nothing to undo here
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["t1"]);

    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getLastDiscarded().map((s) => s.id)).toEqual(["s1"]);
    store.undoDiscard();
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["s1"]);
  });

  it("a path-scoped publish keeps other files' drafts and all suggestions", async () => {
    const server = installDraftServer();
    const draftRow = (id: string, path: string) => ({ ...sug(id, path), origin: "user", state: "draft", rationale: undefined });
    server.drafts.set("acme/widget#1", [draftRow("d1", "a.md"), draftRow("d2", "b.md"), sug("s1", "a.md")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    const result = await store.publishDraft({ event: "COMMENT", body: "", mode: "persist-only", path: "a.md" });
    expect(result.ok).toBe(true);
    expect(store.getDraft().comments.map((c) => c.id)).toEqual(["d2"]);
    expect(store.getSuggestions().map((c) => c.id)).toEqual(["s1"]);
  });
});
