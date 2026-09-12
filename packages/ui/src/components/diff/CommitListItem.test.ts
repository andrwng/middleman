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

function renderItem(commit: CommitInfo, spanAnchorSha: string | null = null) {
  return render(CommitListItem, {
    props: { commit, active: false, reviewed: false, onclick: vi.fn(), spanAnchorSha },
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

describe("CommitListItem span hint", () => {
  function titleOf(container: HTMLElement): string {
    return container.querySelector(".commit-item")!.getAttribute("title") ?? "";
  }

  it("shows only the commit message when nothing can be spanned from", () => {
    // No single commit selected: a shift-click would just select this one, so
    // promising a range would be a lie.
    const { container } = renderItem(baseCommit());
    expect(titleOf(container)).toBe("feat: do the thing");
  });

  it("names the anchor commit when a shift-click would span a range", () => {
    const { container } = renderItem(baseCommit(), "9f8e7d6c5b4a3210");
    const title = titleOf(container);
    expect(title).toContain("feat: do the thing");
    expect(title).toContain("Shift-click");
    // The anchor is identified by its short sha, as the rows display it.
    expect(title).toContain("9f8e7d6");
    expect(title).not.toContain("9f8e7d6c5b4a3210");
  });

  it("offers no hint on the anchor commit's own row", () => {
    // Spanning a commit to itself is a no-op.
    const commit = baseCommit();
    const { container } = renderItem(commit, commit.sha);
    expect(titleOf(container)).toBe("feat: do the thing");
  });
});
