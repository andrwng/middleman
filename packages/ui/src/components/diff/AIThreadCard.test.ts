import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent, waitFor } from "@testing-library/svelte";

const createThreads = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
// Mutable so each test can set the session's questions before render.
const state: { questions: Record<string, unknown>[] } = { questions: [] };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    ai: {
      getQuestionsForThread: () => state.questions,
      addFollowUp: vi.fn(),
      deleteThread,
      deleteQuestion: vi.fn(),
      getError: () => null,
    },
    diff: { addDraftComment: vi.fn() },
    fileResolver: { resolve: vi.fn(async () => {}), getVersion: () => 0, lookup: () => undefined },
    reviewThreads: { createThreads },
  }),
}));

import AIThreadCard from "./AIThreadCard.svelte";

const thread = {
  id: 5, path: "a.go", anchor_side: "RIGHT", anchor_line: 12,
  hunk_start_line: 10, hunk_end_line: 12, commit_sha: "abc123", status: "active",
};

function done(id: number, question: string, answer: string) {
  return { id, thread_id: 5, question, answer, status: "done" };
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  state.questions = [];
});

describe("AIThreadCard promote-to-review-thread (local)", () => {
  it("promotes the whole session to a persisted thread and removes the AI thread", async () => {
    state.questions = [
      done(1, "why unbounded?", "bounded by ctx"),
      done(2, "cap attempts?", "add maxAttempts"),
    ];
    const { getByText } = render(AIThreadCard, {
      props: { thread, repoOwner: "local", repoName: "demo" },
    });
    await fireEvent.click(getByText("Promote to review thread"));
    // Persist-only: createThreads is called with no mode argument.
    expect(createThreads).toHaveBeenCalledWith([{
      path: "a.go", side: "RIGHT", line: 12, startLine: 10, commitSha: "abc123",
      body: "why unbounded?",
      comments: [
        { author: "agent", body: "bounded by ctx" },
        { author: "user", body: "cap attempts?" },
        { author: "agent", body: "add maxAttempts" },
      ],
    }]);
    // The Q&A thread is removed once it's been captured as a review thread.
    await waitFor(() => expect(deleteThread).toHaveBeenCalledWith(5));
  });

  it("only promotes answered turns", async () => {
    state.questions = [
      done(1, "q1", "a1"),
      { id: 2, thread_id: 5, question: "q2", answer: "", status: "running" },
      { id: 3, thread_id: 5, question: "q3", answer: "", status: "failed" },
    ];
    const { getByText } = render(AIThreadCard, {
      props: { thread, repoOwner: "local", repoName: "demo" },
    });
    await fireEvent.click(getByText("Promote to review thread"));
    expect(createThreads).toHaveBeenCalledWith([
      expect.objectContaining({ body: "q1", comments: [{ author: "agent", body: "a1" }] }),
    ]);
  });

  it("does not remove the AI thread if the promote fails", async () => {
    createThreads.mockResolvedValueOnce(false);
    state.questions = [done(1, "q1", "a1")];
    const { getByText } = render(AIThreadCard, {
      props: { thread, repoOwner: "local", repoName: "demo" },
    });
    await fireEvent.click(getByText("Promote to review thread"));
    await Promise.resolve();
    expect(deleteThread).not.toHaveBeenCalled();
  });

  it("hides the control on remote PRs", () => {
    state.questions = [done(1, "q1", "a1")];
    const { queryByText } = render(AIThreadCard, {
      props: { thread, repoOwner: "acme", repoName: "widget" },
    });
    expect(queryByText("Promote to review thread")).toBeNull();
  });

  it("hides the control when there are no answered questions", () => {
    state.questions = [{ id: 1, thread_id: 5, question: "q1", answer: "", status: "running" }];
    const { queryByText } = render(AIThreadCard, {
      props: { thread, repoOwner: "local", repoName: "demo" },
    });
    expect(queryByText("Promote to review thread")).toBeNull();
  });
});
