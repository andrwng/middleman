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
import ClaudeReviewSection from "./ClaudeReviewSection.svelte";
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

function doneRun(extra: Record<string, unknown> = {}) {
  return { id: 1, status: "done", head_sha: "h", instructions: "focus on errors", summary: "Two findings.", raw_json: "", suggestion_count: 2, created_at: "2026-01-01T00:00:00Z", ...extra };
}

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

describe("ClaudeReviewSection", () => {
  it("is hidden when there is no run", () => {
    const { container } = render(ClaudeReviewSection);
    expect(container.textContent?.trim()).toBe("");
  });

  it("shows the summary and instructions of a finished run", () => {
    run.current = doneRun();
    const { getByText } = render(ClaudeReviewSection);
    expect(getByText("Two findings.")).toBeTruthy();
    expect(getByText(/focus on errors/)).toBeTruthy();
  });

  it("shows the error and partial count of a failed run", () => {
    run.current = doneRun({ status: "failed", error: "claude exited: 1", summary: "" });
    const { getByText } = render(ClaudeReviewSection);
    expect(getByText(/claude exited: 1/)).toBeTruthy();
    expect(getByText(/2 suggestions were kept/)).toBeTruthy();
  });

  it("re-runs with the previous instructions prefilled", async () => {
    run.current = doneRun();
    const { getByText, getByRole } = render(ClaudeReviewSection);
    await fireEvent.click(getByText("Re-run"));
    expect((getByRole("textbox") as HTMLTextAreaElement).value).toBe("focus on errors");
  });

  it("offers Stop while the run is active", async () => {
    run.current = doneRun({ status: "running", summary: "" });
    run.active = true;
    const { getByText } = render(ClaudeReviewSection);
    await fireEvent.click(getByText("Stop"));
    expect(cancel).toHaveBeenCalled();
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
