import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import { setReviewNavCollapsed } from "../../lib/uiState.svelte.js";
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

function reviewThreadsStub() {
  return {
    getThreads: () => [],
    applyAll: vi.fn(async () => true),
  };
}

function worktreeSessionStub() {
  return {
    hasRunningTurn: () => false,
  };
}

function codeBrowserStub(isOpen = false) {
  return {
    isOpen,
    navigateTo: vi.fn(async () => {}),
  };
}

// ReviewCommentsSection (mounted in the sidebar) reads detail + viewer at
// render; provide empty stubs so it renders its empty state without crashing.
function detailStub() {
  return {
    getReviewCommentsByFilePath: () => new Map(),
  };
}

function viewerStub() {
  return {
    getLogin: () => null,
  };
}

function renderSidebar(overrides: { diff?: ReturnType<typeof diffStub>; codeBrowser?: ReturnType<typeof codeBrowserStub> } = {}) {
  const diff = overrides.diff ?? diffStub();
  const codeBrowser = overrides.codeBrowser ?? codeBrowserStub();
  const rendered = render(DiffSidebar, {
    context: new Map<symbol, unknown>([
      [
        STORES_KEY,
        {
          diff,
          pulls: pullsStub(),
          ai: aiStub(),
          reviewThreads: reviewThreadsStub(),
          worktreeSession: worktreeSessionStub(),
          detail: detailStub(),
          viewer: viewerStub(),
          codeBrowser,
        },
      ],
    ]),
  });
  return { ...rendered, diff, codeBrowser };
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

describe("DiffSidebar file click", () => {
  beforeEach(() => {
    // isReviewNavCollapsed's backing $state is a module-level singleton,
    // initialized once at module load and mutated in place by
    // toggleReviewNavCollapsed -- localStorage.clear() alone (the other
    // beforeEach above) doesn't reset it, so a rail-collapsed assertion
    // earlier in this file leaves it collapsed for every test after,
    // hiding the file rows these tests click on.
    setReviewNavCollapsed(false);
  });

  it("requests the diff scroll but does not touch the code browser when it's closed", async () => {
    const { diff, codeBrowser } = renderSidebar({ codeBrowser: codeBrowserStub(false) });
    await fireEvent.click(screen.getByText("a.go"));

    expect(diff.requestScrollToFile).toHaveBeenCalledWith("a.go");
    expect(codeBrowser.navigateTo).not.toHaveBeenCalled();
  });

  // The code browser panel takes over the diff area's own slot while
  // open, so requestScrollToFile alone has nothing visible to scroll --
  // this is what makes clicking a sidebar file open it in the code
  // viewer instead, matching what's actually on screen.
  it("also opens the file in the code browser when it's open", async () => {
    const { diff, codeBrowser } = renderSidebar({ codeBrowser: codeBrowserStub(true) });
    await fireEvent.click(screen.getByText("a.go"));

    expect(diff.requestScrollToFile).toHaveBeenCalledWith("a.go");
    expect(codeBrowser.navigateTo).toHaveBeenCalledWith("a.go");
  });
});
