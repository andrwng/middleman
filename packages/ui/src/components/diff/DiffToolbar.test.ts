import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const diffRefresh = vi.fn(async () => {});
const threadsRefresh = vi.fn(async () => {});
const refsOpenBlank = vi.fn();
const state = { owner: "local" };
const shaState = { sha: "abc123" };

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
      getCurrentPR: () => ({ owner: state.owner, name: "demo", number: 7 }),
      refresh: diffRefresh,
      isRefreshing: () => false,
      getRefreshError: () => null,
      getCurrentCommitSha: () => shaState.sha,
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
