import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import CommitListItem from "./CommitListItem.svelte";
import type { CommitInfo } from "../../api/types.js";

function baseCommit(overrides: Partial<CommitInfo> = {}): CommitInfo {
  return {
    sha: "abc1234deadbeef",
    message: "feat: do the thing",
    author_name: "alice",
    authored_at: new Date().toISOString(),
    ...overrides,
  };
}

function renderItem(commit: CommitInfo) {
  return render(CommitListItem, {
    props: { commit, active: false, reviewed: false, onclick: vi.fn() },
  });
}

afterEach(() => cleanup());

describe("CommitListItem branch-head marker", () => {
  it("renders no marker when branch_heads is absent", () => {
    const { container } = renderItem(baseCommit());
    expect(container.querySelector(".commit-item__branches")).toBeNull();
  });

  it("renders the marker with branch names in the title", () => {
    const { container } = renderItem(baseCommit({ branch_heads: ["feat/login"] }));
    const mark = container.querySelector(".commit-item__branches");
    expect(mark).toBeTruthy();
    expect(mark?.getAttribute("title")).toBe("feat/login");
    expect(container.querySelector(".commit-item__branch-count")).toBeNull();
  });

  it("shows a count badge when more than one branch points at the commit", () => {
    const { container } = renderItem(
      baseCommit({ branch_heads: ["selective-sync", "wip/cleanup"] }),
    );
    expect(container.querySelector(".commit-item__branch-count")?.textContent).toBe("2");
    expect(
      container.querySelector(".commit-item__branches")?.getAttribute("title"),
    ).toBe("selective-sync, wip/cleanup");
  });
});
