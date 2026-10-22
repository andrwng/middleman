import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";
import type { ReviewThreadDraftInput, ReviewThreadMode } from "../../stores/reviewThreads.svelte.js";

// Typed to match reviewThreadsStore.createThreads(drafts, mode?) so
// createThreads.mock.calls[0]![0] resolves to ReviewThreadDraftInput[]
// (an untyped `vi.fn(async () => true)` infers a zero-arg signature,
// making Parameters<T> `[]` and calls[0]![0] a TS2493 tuple-index error).
const createThreads = vi.fn<
  (drafts: ReviewThreadDraftInput[], mode?: ReviewThreadMode) => Promise<boolean>
>(async () => true);
const clearDraft = vi.fn();
const clearDraftCommentsForPath = vi.fn();
const publishDraft = vi.fn<
  (req: { event: string; body: string; commitId?: string }) => Promise<{ ok: true } | { ok: false; error: string }>
>(async () => ({ ok: true }));
const loadPulls = vi.fn();
const threadsRefresh = vi.fn(async () => {});
let suggestionList: Array<{ id: string }> = [];
const clientPOST = vi.fn();

const singlePathDraft = {
  comments: [{ id: 1, path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "x", inReplyTo: null }],
  event: "COMMENT",
  body: "",
};

const twoPathDraft = {
  comments: [
    { id: 1, path: "a.md", side: "RIGHT", line: 1, commitSha: "abc", body: "a1", inReplyTo: null },
    { id: 2, path: "b.md", side: "RIGHT", line: 1, commitSha: "abc", body: "b1", inReplyTo: null },
  ],
  event: "COMMENT",
  body: "",
};

// Mutable so each test can point the mocked store at the draft shape it
// needs (single-path for the pre-existing agent-checkbox tests, two-path
// for the scopePath tests) without disturbing the others.
let draft: typeof singlePathDraft | typeof twoPathDraft = singlePathDraft;

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: {
      getDraft: () => draft,
      clearDraft,
      clearDraftCommentsForPath,
      publishDraft,
      getCommits: () => [{ sha: "head-sha" }],
      getSuggestions: () => suggestionList,
    },
    pulls: { loadPulls },
    reviewThreads: { createThreads, getError: () => null, refresh: threadsRefresh },
  }),
  getClient: () => ({ POST: clientPOST }),
}));

import ReviewPanel from "./ReviewPanel.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  draft = singlePathDraft;
});

describe("ReviewPanel agent checkbox (local)", () => {
  it("defaults the agent checkbox ticked and publishes act-immediately", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    expect((getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(getByText("Create & apply"));
    expect(publishDraft).toHaveBeenCalledWith({ event: "COMMENT", body: "", mode: "act-immediately" });
    expect(createThreads).not.toHaveBeenCalled();
    expect(threadsRefresh).toHaveBeenCalled();
  });

  it("publishes persist-only when the agent checkbox is unticked", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByRole("checkbox"));
    await fireEvent.click(getByText("Create review threads"));
    expect(publishDraft).toHaveBeenCalledWith({ event: "COMMENT", body: "", mode: "persist-only" });
  });

  it("does not offer a discuss-first option in the UI", () => {
    const { queryByText, queryByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    expect(queryByRole("radio", { name: /discuss/i })).toBeNull();
    expect(queryByText(/discuss/i)).toBeNull();
  });
});

describe("ReviewPanel scopePath (local)", () => {
  it("with scopePath publishes only that path", async () => {
    draft = twoPathDraft;
    const { getByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, scopePath: "a.md", onclose: vi.fn() },
    });
    await fireEvent.click(getByText("Create & apply"));
    expect(publishDraft).toHaveBeenCalledWith({ event: "COMMENT", body: "", mode: "act-immediately", path: "a.md" });
  });

  it("shows the server's error and keeps the panel open", async () => {
    publishDraft.mockResolvedValueOnce({ ok: false, error: "add at least one inline comment to create review threads" });
    const onclose = vi.fn();
    const { getByText, findByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose },
    });
    await fireEvent.click(getByText("Create & apply"));
    expect(await findByText(/at least one inline comment/)).toBeTruthy();
    expect(onclose).not.toHaveBeenCalled();
  });
});

describe("ReviewPanel publish (GitHub)", () => {
  it("publishes server-side drafts with the head sha and closes", async () => {
    const onclose = vi.fn();
    const { getByText } = render(ReviewPanel, {
      props: { owner: "acme", name: "widget", number: 1, onclose },
    });
    await fireEvent.click(getByText("Publish review"));
    expect(publishDraft).toHaveBeenCalledWith({ event: "COMMENT", body: "", commitId: "head-sha" });
    expect(clientPOST).not.toHaveBeenCalled();
    expect(loadPulls).toHaveBeenCalled();
    expect(onclose).toHaveBeenCalled();
  });

  it("shows the publish error and keeps the panel open", async () => {
    publishDraft.mockResolvedValueOnce({ ok: false, error: "GitHub couldn't anchor comment[0]" });
    const onclose = vi.fn();
    const { getByText, findByText } = render(ReviewPanel, {
      props: { owner: "acme", name: "widget", number: 1, onclose },
    });
    await fireEvent.click(getByText("Publish review"));
    expect(await findByText(/couldn't anchor comment\[0\]/)).toBeTruthy();
    expect(onclose).not.toHaveBeenCalled();
  });
});

describe("ReviewPanel unadded suggestions notice", () => {
  afterEach(() => {
    suggestionList = [];
  });

  it("warns that Claude suggestions will not be published", () => {
    suggestionList = [{ id: "s1" }, { id: "s2" }];
    const { getByText } = render(ReviewPanel, { props: { owner: "acme", name: "widget", number: 1, onclose: vi.fn() } });
    expect(getByText(/2 Claude suggestions not added to drafts/)).toBeTruthy();
  });

  it("stays quiet without suggestions", () => {
    const { queryByText } = render(ReviewPanel, { props: { owner: "acme", name: "widget", number: 1, onclose: vi.fn() } });
    expect(queryByText(/not added to drafts/)).toBeNull();
  });
});
