import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const diffRefresh = vi.fn(async () => {});
const threadsRefresh = vi.fn(async () => {});
const state = { owner: "local" };

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
    },
    detail: {
      getHiddenThreadCount: () => 0,
      isShowingHiddenThreads: () => false,
      getDetail: () => null,
      setShowHiddenThreads: vi.fn(),
    },
    reviewThreads: { refresh: threadsRefresh },
  }),
}));

import DiffToolbar from "./DiffToolbar.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  state.owner = "local";
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
