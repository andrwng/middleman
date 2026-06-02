import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const applyAll = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
let running = false;
const threadsRef: { value: unknown[] } = { value: [] };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { getThreads: () => threadsRef.value, applyAll, deleteThread },
    worktreeSession: { hasRunningTurn: () => running },
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

afterEach(() => { cleanup(); vi.clearAllMocks(); running = false; threadsRef.value = []; });

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

  it("Apply all calls the store and is disabled while a turn runs", async () => {
    threadsRef.value = [thread({ status: "discussed" })];
    running = true;
    const { getByText } = render(ReviewThreadsSection);
    const btn = getByText("Apply all") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.click(btn);
    expect(applyAll).not.toHaveBeenCalled();
  });

  it("Apply all triggers when idle", async () => {
    threadsRef.value = [thread({ status: "open" })];
    running = false;
    const { getByText } = render(ReviewThreadsSection);
    await fireEvent.click(getByText("Apply all"));
    expect(applyAll).toHaveBeenCalled();
  });
});
