import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/svelte";

const acceptSuggestion = vi.fn();
const discardSuggestion = vi.fn();
const updateSuggestionBody = vi.fn();
vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: { acceptSuggestion, discardSuggestion, updateSuggestionBody, getCurrentPR: () => ({ owner: "acme", name: "widget", number: 1 }) },
    draftChat: {
      open: vi.fn(), close: vi.fn(), send: vi.fn(), cancel: vi.fn(), apply: vi.fn(), revert: vi.fn(),
      getMessages: () => [], isBusy: () => false, isSending: () => false, getError: () => null,
    },
  }),
}));

import SuggestionCard from "./SuggestionCard.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const s = {
  id: "s1", path: "a.go", line: 12, side: "RIGHT" as const, commitSha: "abc",
  body: "Handle the error.", createdAt: "2026-01-01T00:00:00Z", origin: "claude" as const,
  rationale: "err is dropped",
};

describe("SuggestionCard", () => {
  it("shows body and rationale, and acts on the suggestion", async () => {
    const { getByText } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
    expect(getByText("Handle the error.")).toBeTruthy();
    expect(getByText("err is dropped")).toBeTruthy();
    await fireEvent.click(getByText("Add to drafts"));
    expect(acceptSuggestion).toHaveBeenCalledWith("s1");
    await fireEvent.click(getByText("Discard"));
    expect(discardSuggestion).toHaveBeenCalledWith("s1");
  });

  it("flags a suggestion written against an older commit", () => {
    const { getByText, unmount } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "def" } });
    expect(getByText(/outdated/i)).toBeTruthy();
    unmount();
    const fresh = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
    expect(fresh.queryByText(/outdated/i)).toBeNull();
  });

  it("edits the body in place", async () => {
    const { getByText, getByRole } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
    await fireEvent.click(getByText("Edit"));
    const box = getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "Return the error." } });
    await fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    expect(updateSuggestionBody).toHaveBeenCalledWith("s1", "Return the error.");
  });

  it("opens the chat with Ask Claude", async () => {
    const { getByText, getByPlaceholderText } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
    await fireEvent.click(getByText("Ask Claude"));
    expect(getByPlaceholderText(/Ask about this comment/)).toBeTruthy();
  });
});
