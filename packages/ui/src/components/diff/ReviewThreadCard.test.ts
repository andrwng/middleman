import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const resolve = vi.fn(async () => true);
const hide = vi.fn(async () => true);
const addComment = vi.fn(async () => true);

vi.mock("../../context.js", () => ({
  getStores: () => ({ reviewThreads: { resolve, hide, unhide: vi.fn(), addComment } }),
}));

import ReviewThreadCard from "./ReviewThreadCard.svelte";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 5, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc1234",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [
      { id: 1, author: "user", body: "rename this", created_at: "" },
      { id: 2, author: "agent", body: "agreed", created_at: "" },
    ],
    ...over,
  };
}

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("ReviewThreadCard", () => {
  it("renders the comments and a status chip", () => {
    const { getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect(getByText("rename this")).toBeTruthy();
    expect(getByText("agreed")).toBeTruthy();
    expect(getByText(/open/i)).toBeTruthy();
  });

  it("resolve button calls the store", async () => {
    const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread() } });
    await fireEvent.click(getByTitle("Resolve this thread"));
    expect(resolve).toHaveBeenCalledWith(5);
  });

  it("collapses to a stub when hidden, with an unhide affordance", () => {
    const { getByText, queryByText } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true }) },
    });
    expect(getByText(/hidden/i)).toBeTruthy();
    expect(queryByText("rename this")).toBeNull(); // body not shown while hidden
  });
});
