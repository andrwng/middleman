import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const applyAll = vi.fn(async () => true);
let running = false;
const threadsRef: { value: unknown[] } = { value: [] };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { getThreads: () => threadsRef.value, applyAll },
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

  it("lists non-hidden threads with status + preview", () => {
    threadsRef.value = [thread(), thread({ id: 2, hidden: true })];
    const { getByText, queryByText } = render(ReviewThreadsSection);
    expect(getByText("Review threads")).toBeTruthy();
    expect(getByText(/rename this please/)).toBeTruthy();
    expect(getByText("1")).toBeTruthy(); // count = 1 non-hidden
    expect(queryByText("2")).toBeNull();
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
