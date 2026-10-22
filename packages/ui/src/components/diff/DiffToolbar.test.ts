import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const diffRefresh = vi.fn(async () => {});
const threadsRefresh = vi.fn(async () => {});
const refsOpenBlank = vi.fn();
const state = { owner: "local" };
const shaState = { sha: "abc123" };
const runState = { active: false };
const startRun = vi.fn(async () => true);
const cancelRun = vi.fn(async () => {});
const openPanel = vi.fn();

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: {
      getDraft: () => ({ comments: [], event: "COMMENT" }),
      getLayout: () => "unified",
      getTabWidth: () => 1,
      getHideWhitespace: () => false,
      setLayout: vi.fn(),
      setTabWidth: vi.fn(),
      setHideWhitespace: vi.fn(),
      getCurrentPR: () => (state.owner ? { owner: state.owner, name: "demo", number: 7 } : null),
      refresh: diffRefresh,
      isRefreshing: () => false,
      getRefreshError: () => null,
      getCurrentCommitSha: () => shaState.sha,
      getSuggestions: () => [{ id: "s1" }, { id: "s2" }],
    },
    aiReviewRun: {
      isActive: () => runState.active,
      isStarting: () => false,
      getError: () => null,
      getRun: () => null,
      startRun,
      cancel: cancelRun,
      openPanel,
    },
    detail: {
      getHiddenThreadCount: () => 0,
      isShowingHiddenThreads: () => false,
      getDetail: () => null,
      setShowHiddenThreads: vi.fn(),
    },
    reviewThreads: { refresh: threadsRefresh },
    symbolRefs: { openBlank: refsOpenBlank },
  }),
}));

import DiffToolbar from "./DiffToolbar.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  state.owner = "local";
  shaState.sha = "abc123";
  runState.active = false;
});

describe("DiffToolbar refresh routing", () => {
  it("syncs review threads (not a GitHub sync) on a local worktree", async () => {
    state.owner = "local";
    const { getByRole } = render(DiffToolbar, { props: {} });
    await fireEvent.click(getByRole("button", { name: /refresh/i }));
    expect(threadsRefresh).toHaveBeenCalled();
    expect(diffRefresh).not.toHaveBeenCalled();
  });

  it("runs the GitHub sync on a remote PR", async () => {
    state.owner = "acme";
    const { getByRole } = render(DiffToolbar, { props: {} });
    await fireEvent.click(getByRole("button", { name: /refresh/i }));
    expect(diffRefresh).toHaveBeenCalled();
    expect(threadsRefresh).not.toHaveBeenCalled();
  });
});

describe("DiffToolbar refs button", () => {
  it("opens the refs search when clicked", async () => {
    const onRefsClick = vi.fn();
    const { getByRole } = render(DiffToolbar, { props: { onRefsClick } });

    await fireEvent.click(getByRole("button", { name: /refs/i }));

    expect(onRefsClick).toHaveBeenCalled();
  });

  // A search needs a SHA to number its hits against; getCurrentCommitSha
  // returns "" for an unresolved patchset pair or a PR with no commits.
  // Disabled rather than hidden, so the control does not appear and
  // vanish as the scope changes.
  it("is disabled with an explanatory title when there is no current SHA", () => {
    shaState.sha = "";
    const { getByRole } = render(DiffToolbar, { props: { onRefsClick: vi.fn() } });

    const btn = getByRole("button", { name: /refs/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toMatch(/scope/i);
  });

  it("names the s shortcut in its title so the key is discoverable", () => {
    const { getByRole } = render(DiffToolbar, { props: { onRefsClick: vi.fn() } });

    expect(getByRole("button", { name: /refs/i }).getAttribute("title")).toMatch(/\bs\b/);
  });
});

describe("DiffToolbar browse button", () => {
  it("calls onBrowseClick when the Browse button is clicked", async () => {
    const onBrowseClick = vi.fn();
    const { getByRole } = render(DiffToolbar, { props: { onBrowseClick } });

    await fireEvent.click(getByRole("button", { name: /browse/i }));

    expect(onBrowseClick).toHaveBeenCalled();
  });

  // The panel loads a tree and file at a specific commit, so it shares
  // the Refs button's SHA gate: disabled rather than hidden, so the
  // control does not appear and vanish as the scope changes.
  it("is disabled with an explanatory title when there is no current SHA", () => {
    shaState.sha = "";
    const { getByRole } = render(DiffToolbar, { props: { onBrowseClick: vi.fn() } });

    const btn = getByRole("button", { name: /browse/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toMatch(/scope/i);
  });

  it("names the b shortcut in its title so the key is discoverable", () => {
    const { getByRole } = render(DiffToolbar, { props: { onBrowseClick: vi.fn() } });

    expect(getByRole("button", { name: /browse/i }).getAttribute("title")).toMatch(/\bb\b/);
  });
});

describe("DiffToolbar Review with Claude", () => {
  it("starts a run from the toolbar on a GitHub PR", async () => {
    state.owner = "acme";
    const { getByRole, getByText } = render(DiffToolbar, { props: {} });
    await fireEvent.click(getByRole("button", { name: "Review with Claude" }));
    await fireEvent.input(getByRole("textbox"), { target: { value: "focus on tests" } });
    await fireEvent.click(getByText("Start review"));
    expect(startRun).toHaveBeenCalledWith("focus on tests");
  });

  it("is offered on a local worktree too", () => {
    state.owner = "local";
    const { getByRole } = render(DiffToolbar, { props: {} });
    expect(getByRole("button", { name: "Review with Claude" })).toBeTruthy();
  });

  it("is hidden when no pull request is selected", () => {
    state.owner = "";
    const { queryByRole } = render(DiffToolbar, { props: {} });
    expect(queryByRole("button", { name: "Review with Claude" })).toBeNull();
  });

  it("shows progress and Stop while Claude is reviewing", async () => {
    state.owner = "acme";
    runState.active = true;
    const { getByText, queryByRole } = render(DiffToolbar, { props: {} });
    expect(getByText(/Claude is reviewing · 2 suggestions/)).toBeTruthy();
    expect(queryByRole("button", { name: "Review with Claude" })).toBeNull();
    await fireEvent.click(getByText("Stop"));
    expect(cancelRun).toHaveBeenCalled();
  });

  it("opens the Claude review panel from the progress pill", async () => {
    state.owner = "acme";
    runState.active = true;
    const { getByRole } = render(DiffToolbar, { props: {} });
    await fireEvent.click(getByRole("button", { name: /Claude is reviewing/ }));
    expect(openPanel).toHaveBeenCalled();
    expect(cancelRun).not.toHaveBeenCalled();
  });
});
