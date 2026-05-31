import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const resolve = vi.fn(async () => true);
const hide = vi.fn(async () => true);
const addComment = vi.fn(async () => true);
const apply = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
const ask = vi.fn(async () => true);
let running = false;

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { resolve, hide, unhide: vi.fn(), addComment, apply, deleteThread, ask },
    worktreeSession: { hasRunningTurn: () => running },
  }),
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

  it("shows Apply for open/discussed threads and calls the store", async () => {
    const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "discussed" }) } });
    await fireEvent.click(getByTitle("Apply this thread's change"));
    expect(apply).toHaveBeenCalledWith(5);
  });

  it("hides Apply once applied/resolved", () => {
    const { queryByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "applied" }) } });
    expect(queryByTitle("Apply this thread's change")).toBeNull();
  });

  it("delete requires a confirm click before calling the store", async () => {
    const { getByText, getByTitle } = render(ReviewThreadCard, { props: { thread: thread() } });
    await fireEvent.click(getByTitle("Delete this thread permanently"));
    expect(deleteThread).not.toHaveBeenCalled();
    expect(getByText("Confirm?")).toBeTruthy();
    await fireEvent.click(getByText("Confirm?"));
    expect(deleteThread).toHaveBeenCalledWith(5);
  });

  it("Reply & ask Claude calls ask; plain Send calls addComment", async () => {
    const { getByText, getByPlaceholderText } = render(ReviewThreadCard, { props: { thread: thread() } });
    const box = getByPlaceholderText(/Reply/i) as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "why a mutex?" } });
    await fireEvent.click(getByText("Ask Claude"));
    expect(ask).toHaveBeenCalledWith(5, "why a mutex?");
  });

  it("Ask is disabled while a turn runs", () => {
    running = true;
    const { getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect((getByText("Ask Claude") as HTMLButtonElement).disabled).toBe(true);
    running = false;
  });

  it("marks user comments that were sent to the agent", () => {
    const { container } = render(ReviewThreadCard, {
      props: { thread: thread({ comments: [{ id: 1, author: "user", body: "ask", sent_to_agent: true, created_at: "" }] }) },
    });
    expect(container.querySelector(".review-thread__sent-badge")).toBeTruthy();
  });
});
