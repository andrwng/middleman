import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import DiffSidebar from "./DiffSidebar.svelte";

function diffStub() {
  return {
    getFileList: () => ({
      stale: false,
      files: [
        {
          path: "a.go",
          status: "modified",
          is_binary: false,
          is_whitespace_only: false,
          additions: 1,
          deletions: 0,
          hunks: [],
        },
      ],
    }),
    isFileListLoading: () => false,
    getActiveFile: () => null,
    requestScrollToFile: vi.fn(),
    isFileReviewed: () => false,
    getFileReviewProgress: () => null,
    getCommits: () => [],
    getCommitsError: () => null,
    isCommitsLoading: () => false,
    getScope: () => ({ kind: "head" as const }),
    getCommitIndex: () => null,
    getReviewProgress: () => null,
    isCommitReviewed: () => false,
    hasUnreviewed: () => false,
    selectUnreviewed: vi.fn(),
    selectCommit: vi.fn(),
    selectRange: vi.fn(),
    stepPrev: vi.fn(),
    stepNext: vi.fn(),
    resetToHead: vi.fn(),
    loadCommits: vi.fn(async () => {}),
    getCurrentPR: () => null,
    getDraft: () => ({ comments: [] }),
    removeDraftComment: vi.fn(),
    requestEditDraft: vi.fn(),
  };
}

function pullsStub() {
  return {
    getSelectedPR: () => null,
  };
}

function aiStub() {
  return {
    all: () => ({ threads: [], questions: [] }),
    hasInFlightQuestions: () => false,
    getQuestionsForThread: () => [],
    deleteThread: vi.fn(async () => true),
    deleteQuestion: vi.fn(async () => true),
    getError: () => null,
  };
}

function renderSidebar() {
  return render(DiffSidebar, {
    context: new Map<symbol, unknown>([
      [
        STORES_KEY,
        { diff: diffStub(), pulls: pullsStub(), ai: aiStub() },
      ],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("DiffSidebar collapse-to-rail", () => {
  it("renders the file tree by default", () => {
    const { container } = renderSidebar();
    expect(container.querySelector(".diff-files")).toBeTruthy();
  });

  it("clicking the collapse button writes pr-review-nav-collapsed", async () => {
    renderSidebar();
    const toggle = await screen.findByLabelText(/collapse review nav|expand review nav/i);
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-review-nav-collapsed")).toBe("true");
  });

  it("renders rail markup when pr-review-nav-collapsed=true", () => {
    localStorage.setItem("pr-review-nav-collapsed", "true");
    const { container } = renderSidebar();
    const rail = container.querySelector(".diff-sidebar--rail");
    expect(rail).toBeTruthy();
    expect(rail?.textContent ?? "").toMatch(/0c.+0d.+0q.+1f/);
  });
});
