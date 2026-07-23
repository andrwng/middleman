import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const createThreads = vi.fn(async () => true);
const clearDraft = vi.fn();
const clearDraftCommentsForPath = vi.fn();

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
    diff: { getDraft: () => draft, clearDraft, clearDraftCommentsForPath, getCommits: () => [] },
    pulls: { loadPulls: vi.fn() },
    reviewThreads: { createThreads, getError: () => null },
  }),
  getClient: () => ({ POST: vi.fn() }),
}));

import ReviewPanel from "./ReviewPanel.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  draft = singlePathDraft;
});

describe("ReviewPanel agent checkbox (local)", () => {
  it("defaults the agent checkbox ticked and submits act-immediately", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    expect((getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(getByText("Create & apply"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), "act-immediately");
  });

  it("submits persist-only when the agent checkbox is unticked", async () => {
    const { getByText, getByRole } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByRole("checkbox"));
    await fireEvent.click(getByText("Create review threads"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), undefined);
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
  it("with scopePath submits only that path's comments and clears only those", async () => {
    draft = twoPathDraft;
    const { getByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, scopePath: "a.md", onclose: vi.fn() },
    });
    await fireEvent.click(getByText("Create & apply"));
    expect(createThreads).toHaveBeenCalledTimes(1);
    expect(createThreads.mock.calls[0]![0].map((d: { path: string }) => d.path)).toEqual(["a.md"]);
    expect(clearDraftCommentsForPath).toHaveBeenCalledWith("a.md");
    expect(clearDraft).not.toHaveBeenCalled();
  });

  it("with no scopePath submits all comments and clears the whole draft", async () => {
    draft = twoPathDraft;
    const { getByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByText("Create & apply"));
    expect(createThreads).toHaveBeenCalledTimes(1);
    expect(createThreads.mock.calls[0]![0].map((d: { path: string }) => d.path)).toEqual(["a.md", "b.md"]);
    expect(clearDraft).toHaveBeenCalledTimes(1);
    expect(clearDraftCommentsForPath).not.toHaveBeenCalled();
  });
});
