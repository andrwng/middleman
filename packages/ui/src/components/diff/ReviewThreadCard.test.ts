import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const resolve = vi.fn(async () => true);
const hide = vi.fn(async () => true);
const addComment = vi.fn(async () => true);
const apply = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
const ask = vi.fn(async () => true);
const discuss = vi.fn(async () => true);
const unresolve = vi.fn(async () => true);
const editComment = vi.fn(async () => true);
let running = false;

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: {
      resolve, unresolve, hide, unhide: vi.fn(), addComment, apply, deleteThread, ask, discuss, editComment,
    },
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

afterEach(() => { cleanup(); vi.clearAllMocks(); running = false; });

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

  it("the hidden stub shows the thread status (resolved)", () => {
    const { getByText } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true, status: "resolved" }) },
    });
    expect(getByText(/hidden/i)).toBeTruthy();
    expect(getByText("resolved")).toBeTruthy();
  });

  it("the hidden stub reflects a still-open thread's status", () => {
    const { getByText } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true, status: "open" }) },
    });
    expect(getByText("open")).toBeTruthy();
  });

  it("deletes a hidden thread from the stub after a confirm click", async () => {
    const { getByText, getByTitle } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true }) },
    });
    const del = getByTitle("Delete this thread permanently");
    await fireEvent.click(del);
    expect(deleteThread).not.toHaveBeenCalled();
    expect(getByText("Confirm?")).toBeTruthy();
    await fireEvent.click(del);
    expect(deleteThread).toHaveBeenCalledWith(5);
  });

  it("Show clears a pending delete confirm on the hidden stub", async () => {
    const { getByText, getByTitle, queryByText } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true }) },
    });
    await fireEvent.click(getByTitle("Delete this thread permanently"));
    expect(getByText("Confirm?")).toBeTruthy();
    await fireEvent.click(getByText("Show"));
    expect(queryByText("Confirm?")).toBeNull();
    expect(getByText("Delete")).toBeTruthy();
    expect(deleteThread).not.toHaveBeenCalled();
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

  it("Ask Claude calls ask and not addComment", async () => {
    const { getByText, getByPlaceholderText } = render(ReviewThreadCard, { props: { thread: thread() } });
    const box = getByPlaceholderText(/Reply/i) as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "why a mutex?" } });
    await fireEvent.click(getByText("Ask Claude"));
    expect(ask).toHaveBeenCalledWith(5, "why a mutex?");
    expect(addComment).not.toHaveBeenCalled();
  });

  it("Send calls addComment and not ask", async () => {
    const { getByText, getByPlaceholderText } = render(ReviewThreadCard, { props: { thread: thread() } });
    const box = getByPlaceholderText(/Reply/i) as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "why a mutex?" } });
    await fireEvent.click(getByText("Send"));
    expect(addComment).toHaveBeenCalledWith(5, "why a mutex?");
    expect(ask).not.toHaveBeenCalled();
  });

  it("the agent button and Apply stay enabled while a turn runs", () => {
    running = true;
    const { getByText, getByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "discussed" }) } });
    // Empty composer => the button reads "Discuss"; stays enabled while busy (queue semantics).
    expect((getByText("Discuss") as HTMLButtonElement).disabled).toBe(false);
    expect((getByTitle(/Apply/i) as HTMLButtonElement).disabled).toBe(false);
  });

  it("the agent button is Discuss when the composer is empty and calls discuss", async () => {
    const { getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
    await fireEvent.click(getByText("Discuss"));
    expect(discuss).toHaveBeenCalledWith(5);
    expect(ask).not.toHaveBeenCalled();
  });

  it("pops an asking badge when Discuss is pressed", async () => {
    const { getByText, container } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect(container.querySelector(".review-thread__asking")).toBeNull();
    await fireEvent.click(getByText("Discuss"));
    expect(container.querySelector(".review-thread__asking")).toBeTruthy();
  });

  it("a resolved thread offers Unresolve instead of Resolve", async () => {
    const { getByTitle, queryByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "resolved" }) } });
    expect(queryByTitle("Resolve this thread")).toBeNull();
    await fireEvent.click(getByTitle("Reopen this thread"));
    expect(unresolve).toHaveBeenCalledWith(5);
  });

  it("marks user comments that were sent to the agent", () => {
    const { container } = render(ReviewThreadCard, {
      props: { thread: thread({ comments: [{ id: 1, author: "user", body: "ask", sent_to_agent: true, created_at: "" }] }) },
    });
    expect(container.querySelector(".review-thread__sent-badge")).toBeTruthy();
  });

  it("badge is absent for un-asked user comments and agent comments", () => {
    const { container } = render(ReviewThreadCard, {
      props: {
        thread: thread({
          comments: [
            { id: 1, author: "user", body: "just a note", sent_to_agent: false, created_at: "" },
            { id: 2, author: "agent", body: "ok", sent_to_agent: false, created_at: "" },
          ],
        }),
      },
    });
    expect(container.querySelector(".review-thread__sent-badge")).toBeNull();
  });

  it('variant="gutter" adds the gutter modifier class to the card', () => {
    const { container } = render(ReviewThreadCard, {
      props: { thread: thread(), variant: "gutter" },
    });
    expect(container.querySelector(".review-thread--gutter")).toBeTruthy();
  });

  it("defaults to inline variant (no gutter class) when variant is omitted", () => {
    const { container } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect(container.querySelector(".review-thread--gutter")).toBeNull();
  });

  it('variant="gutter" also applies to the hidden thread stub', () => {
    const { container } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true }), variant: "gutter" },
    });
    expect(container.querySelector(".review-thread--gutter")).toBeTruthy();
  });

  it("shows an Edit control on the user's own comment", () => {
    const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect(getByTitle("Edit this comment")).toBeTruthy();
  });

  it("does not show an Edit control on agent comments", () => {
    const { queryByTitle } = render(ReviewThreadCard, {
      props: {
        thread: thread({ comments: [{ id: 2, author: "agent", body: "agreed", created_at: "" }] }),
      },
    });
    expect(queryByTitle("Edit this comment")).toBeNull();
  });

  it("clicking Edit shows a prefilled textarea with Save disabled until the text changes", async () => {
    const { getByTitle, getByDisplayValue, getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
    await fireEvent.click(getByTitle("Edit this comment"));
    const box = getByDisplayValue("rename this") as HTMLTextAreaElement;
    const save = getByText("Save") as HTMLButtonElement;
    expect(save.disabled).toBe(true); // unchanged from original body
    await fireEvent.input(box, { target: { value: "" } });
    expect(save.disabled).toBe(true); // empty
    await fireEvent.input(box, { target: { value: "renamed please" } });
    expect(save.disabled).toBe(false);
  });

  it("Save calls editComment and leaves edit mode on success", async () => {
    const { getByTitle, getByDisplayValue, getByText, queryByText } = render(ReviewThreadCard, {
      props: { thread: thread() },
    });
    await fireEvent.click(getByTitle("Edit this comment"));
    const box = getByDisplayValue("rename this") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "renamed please" } });
    await fireEvent.click(getByText("Save"));
    expect(editComment).toHaveBeenCalledWith(5, 1, "renamed please");
    expect(queryByText("Save")).toBeNull(); // edit mode exited
  });

  it("Cancel restores the rendered body without calling the store", async () => {
    const { getByTitle, getByDisplayValue, getByText, queryByText } = render(ReviewThreadCard, {
      props: { thread: thread() },
    });
    await fireEvent.click(getByTitle("Edit this comment"));
    const box = getByDisplayValue("rename this") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "discard me" } });
    await fireEvent.click(getByText("Cancel"));
    expect(editComment).not.toHaveBeenCalled();
    expect(queryByText("Save")).toBeNull(); // edit mode exited
    expect(getByText("rename this")).toBeTruthy(); // original body still rendered
  });

  it("a failed save stays in edit mode with the typed text intact", async () => {
    editComment.mockResolvedValueOnce(false);
    const { getByTitle, getByDisplayValue, getByText } = render(ReviewThreadCard, {
      props: { thread: thread() },
    });
    await fireEvent.click(getByTitle("Edit this comment"));
    const box = getByDisplayValue("rename this") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "renamed please" } });
    await fireEvent.click(getByText("Save"));
    expect(editComment).toHaveBeenCalledWith(5, 1, "renamed please");
    expect(getByText("Save")).toBeTruthy(); // still in edit mode, fail-soft
    expect(getByDisplayValue("renamed please")).toBeTruthy(); // typed text preserved
  });

  it("only one comment can be in edit mode at a time", async () => {
    const { getAllByTitle, getByDisplayValue, queryByDisplayValue } = render(ReviewThreadCard, {
      props: {
        thread: thread({
          comments: [
            { id: 1, author: "user", body: "first", created_at: "" },
            { id: 2, author: "user", body: "second", created_at: "" },
          ],
        }),
      },
    });
    const [editFirst, editSecond] = getAllByTitle("Edit this comment");
    await fireEvent.click(editFirst!);
    expect(getByDisplayValue("first")).toBeTruthy();
    await fireEvent.click(editSecond!);
    expect(queryByDisplayValue("first")).toBeNull(); // first comment's editor closed
    expect(getByDisplayValue("second")).toBeTruthy(); // now editing the second instead
  });

  it("renders the (edited) marker only for comments with edited_at set", () => {
    const { container } = render(ReviewThreadCard, {
      props: {
        thread: thread({
          comments: [
            { id: 1, author: "user", body: "rename this", edited_at: "2026-07-28T00:00:00Z", created_at: "" },
            { id: 2, author: "agent", body: "agreed", created_at: "" },
          ],
        }),
      },
    });
    expect(container.querySelectorAll(".review-thread__edited")).toHaveLength(1);
  });
});

describe("ReviewThreadCard — clicks are not blocked while session is busy", () => {
  it("Apply stays enabled when worktreeSession.hasRunningTurn() is true", async () => {
    running = true;
    const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "open" }) } });
    const btn = getByTitle(/Apply/i) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    expect(apply).toHaveBeenCalledWith(5);
  });

  it("Discuss button (empty composer) stays enabled while busy with a queue tooltip", () => {
    running = true;
    const { container } = render(ReviewThreadCard, { props: { thread: thread() } });
    const btn = container.querySelector(".review-thread__ask") as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(btn.getAttribute("title") ?? "").toMatch(/queue/i);
  });
});
