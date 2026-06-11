import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const applyAll = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
const selectCommit = vi.fn(async () => undefined);
const resetToHead = vi.fn(async () => undefined);
const getCurrentPR = vi.fn(() => null);
const isFileCollapsed = vi.fn(() => false);
const toggleFileCollapsed = vi.fn();
let running = false;
const threadsRef: { value: unknown[] } = { value: [] };
const commitsRef: { value: unknown } = { value: [] };
const scopeRef: { value: unknown } = { value: { kind: "head" } };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { getThreads: () => threadsRef.value, applyAll, deleteThread },
    worktreeSession: { hasRunningTurn: () => running },
    diff: {
      getCommits: () => commitsRef.value,
      getScope: () => scopeRef.value,
      selectCommit,
      resetToHead,
      getCurrentPR,
      isFileCollapsed,
      toggleFileCollapsed,
    },
  }),
}));

import ReviewThreadsSection from "./ReviewThreadsSection.svelte";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 1, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [{ id: 1, author: "user", body: "rename this please", created_at: "" }],
    ...over,
  };
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  running = false;
  threadsRef.value = [];
  commitsRef.value = [];
  scopeRef.value = { kind: "head" };
});

describe("ReviewThreadsSection", () => {
  it("renders nothing when there are no threads", () => {
    threadsRef.value = [];
    const { queryByText } = render(ReviewThreadsSection);
    expect(queryByText("Review threads")).toBeNull();
  });

  it("lists non-hidden threads by path, without the comment preview", () => {
    threadsRef.value = [thread(), thread({ id: 2, hidden: true })];
    const { getByText, queryByText, getByTitle } = render(ReviewThreadsSection);
    expect(getByText("Review threads")).toBeTruthy();
    expect(getByText("a.go")).toBeTruthy(); // path shown
    expect(queryByText(/rename this please/)).toBeNull(); // preview removed (#9)
    expect(getByTitle("a.go")).toBeTruthy(); // full path on hover (#7/#9)
    expect(getByText("1")).toBeTruthy(); // count = 1 non-hidden
    expect(queryByText("2")).toBeNull();
  });

  it("shows a status dot instead of the raw status word (#11)", () => {
    threadsRef.value = [thread({ status: "applied" })];
    const { container, queryByText } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item__dot--applied")).toBeTruthy();
    expect(queryByText("applied")).toBeNull();
  });

  it("highlights the active thread when its row is clicked (#8)", async () => {
    threadsRef.value = [thread({ id: 1 }), thread({ id: 2, path: "b.go" })];
    const { getByText, container } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item-row--active")).toBeNull();
    await fireEvent.click(getByText("a.go"));
    const active = container.querySelector(".thread-item-row--active");
    expect(active).toBeTruthy();
    expect(active?.textContent).toContain("a.go");
  });

  it("deletes a thread from the sidebar after a confirm click (#15)", async () => {
    threadsRef.value = [thread({ id: 7 })];
    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("Delete this thread"));
    expect(deleteThread).not.toHaveBeenCalled(); // first click arms the confirm
    await fireEvent.click(getByTitle("Click again to delete"));
    expect(deleteThread).toHaveBeenCalledWith(7);
  });

  it("Apply all stays enabled while a turn runs and shows a queue tooltip", async () => {
    threadsRef.value = [thread({ status: "discussed" })];
    running = true;
    const { getByText } = render(ReviewThreadsSection);
    const btn = getByText("Apply all") as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(btn.getAttribute("title") ?? "").toMatch(/queue/i);
    await fireEvent.click(btn);
    expect(applyAll).toHaveBeenCalled();
  });

  it("Apply all triggers when idle", async () => {
    threadsRef.value = [thread({ status: "open" })];
    running = false;
    const { getByText } = render(ReviewThreadsSection);
    await fireEvent.click(getByText("Apply all"));
    expect(applyAll).toHaveBeenCalled();
  });
});

describe("ReviewThreadsSection — click-to-navigate", () => {
  function commit(over: Record<string, unknown> = {}) {
    return { sha: "abc", subject: "x", parents: [], author: "x", date: "", ...over };
  }

  it("clicks a thread anchored to the PR head → resetToHead", async () => {
    const t = thread({ id: 1, commit_sha: "headsha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    scopeRef.value = { kind: "commit", sha: "headsha" };

    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).toHaveBeenCalledOnce();
    expect(selectCommit).not.toHaveBeenCalled();
  });

  it("clicks a thread anchored to a mid-stack commit → selectCommit(sha)", async () => {
    const t = thread({ id: 1, commit_sha: "midsha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" }), commit({ sha: "midsha" })];
    scopeRef.value = { kind: "head" };

    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(selectCommit).toHaveBeenCalledWith("midsha");
    expect(resetToHead).not.toHaveBeenCalled();
  });

  it("clicks a thread whose commit_sha is not in the commit list → resetToHead AND row flagged orphan", async () => {
    const t = thread({ id: 1, commit_sha: "rebased-away-sha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    scopeRef.value = { kind: "head" };

    const { container, getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).toHaveBeenCalledOnce();
    expect(selectCommit).not.toHaveBeenCalled();
    expect(container.querySelector(".thread-item__dot--orphan")).toBeTruthy();
  });

  it("does not flag orphan while commits are still loading", () => {
    const t = thread({ id: 1, commit_sha: "anything" });
    threadsRef.value = [t];
    commitsRef.value = null; // loading
    scopeRef.value = { kind: "head" };

    const { container } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item__dot--orphan")).toBeNull();
  });

  it("attaches an orphan aria-label to the button for orphan threads", async () => {
    const t = thread({ id: 1, commit_sha: "rebased-away-sha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    scopeRef.value = { kind: "head" };
    const { getByTitle } = render(ReviewThreadsSection);
    const btn = getByTitle(t.path) as HTMLButtonElement;
    expect(btn.getAttribute("aria-label") ?? "").toMatch(/anchored to a commit no longer in this branch/);
  });
});
