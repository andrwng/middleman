import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/svelte";

const run = {
  current: null as null | Record<string, unknown>,
  active: false,
  error: null as string | null,
};
const startRun = vi.fn(async () => true);
const cancel = vi.fn(async () => {});
const acceptAllSuggestions = vi.fn();
const discardAllSuggestions = vi.fn();
const undoDiscard = vi.fn();
const suggestions = {
  list: [] as Array<Record<string, unknown>>,
  discarded: [] as Array<Record<string, unknown>>,
};

vi.mock("../../context.js", () => ({
  getStores: () => ({
    aiReviewRun: {
      getRun: () => run.current,
      isActive: () => run.active,
      isStarting: () => false,
      getError: () => run.error,
      startRun,
      cancel,
    },
    diff: {
      getSuggestions: () => suggestions.list,
      getLastDiscarded: () => suggestions.discarded,
      acceptAllSuggestions,
      discardAllSuggestions,
      undoDiscard,
      getCurrentPR: () => ({ owner: "acme", name: "widget", number: 1 }),
    },
  }),
}));

import ReviewWithClaudeForm from "./ReviewWithClaudeForm.svelte";
import SuggestionsSection from "./SuggestionsSection.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  run.current = null;
  run.active = false;
  run.error = null;
  suggestions.list = [];
  suggestions.discarded = [];
});

describe("ReviewWithClaudeForm", () => {
  it("starts a run with the typed instructions", async () => {
    const onstarted = vi.fn();
    const { getByRole, getByText } = render(ReviewWithClaudeForm, { props: { onstarted, oncancel: vi.fn() } });
    await fireEvent.input(getByRole("textbox"), { target: { value: "check error handling" } });
    await fireEvent.click(getByText("Start review"));
    expect(startRun).toHaveBeenCalledWith("check error handling");
    await Promise.resolve();
    expect(onstarted).toHaveBeenCalled();
  });

  it("shows why a run could not start", async () => {
    startRun.mockResolvedValueOnce(false);
    run.error = "a Review with Claude run is already in progress";
    const { getByText, findByText } = render(ReviewWithClaudeForm, { props: { oncancel: vi.fn() } });
    await fireEvent.click(getByText("Start review"));
    expect(await findByText(/already in progress/)).toBeTruthy();
  });
});

describe("SuggestionsSection", () => {
  const sug = (id: string) => ({ id, path: "a.go", line: 3, side: "RIGHT", commitSha: "h", body: `body ${id}`, createdAt: "2026-01-01T00:00:00Z", origin: "claude" });

  it("lists suggestions with bulk actions", async () => {
    suggestions.list = [sug("s1"), sug("s2")];
    const { getByText } = render(SuggestionsSection);
    expect(getByText("Suggestions")).toBeTruthy();
    expect(getByText("body s1")).toBeTruthy();
    await fireEvent.click(getByText("Add all to drafts"));
    expect(acceptAllSuggestions).toHaveBeenCalled();
    await fireEvent.click(getByText("Discard all"));
    expect(discardAllSuggestions).toHaveBeenCalled();
  });

  it("offers undo after a discard", async () => {
    suggestions.discarded = [sug("s1")];
    const { getByText } = render(SuggestionsSection);
    await fireEvent.click(getByText("Undo"));
    expect(undoDiscard).toHaveBeenCalled();
  });
});
