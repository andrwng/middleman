import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ReviewCommentsSection from "./ReviewCommentsSection.svelte";
import { STORES_KEY } from "../../context.js";
import { placeReviewComment, type PublishedReviewComment } from "../../stores/detail.svelte.js";
import type { ReviewCommentAnchor } from "../../api/types.js";

function comment(over: Partial<PublishedReviewComment>): PublishedReviewComment {
  return {
    id: 1,
    author: "bob",
    body: "note",
    createdAt: "2026-05-21T10:00:00Z",
    path: "a.go",
    line: 3,
    startLine: null,
    side: "RIGHT",
    commitId: "sha-old",
    htmlUrl: "",
    inReplyTo: 0,
    isHidden: false,
    ...over,
  };
}

function renderSection(comments: PublishedReviewComment[], anchors: ReviewCommentAnchor[]) {
  const byID = new Map(anchors.map((a) => [a.comment_id, a]));
  const diff = {
    getScope: () => ({ kind: "head" }),
    getCommits: () => [{ sha: "sha-head" }, { sha: "sha-old" }],
    getCurrentPR: () => ({ owner: "acme", name: "widget", number: 1 }),
    isFileCollapsed: () => false,
    toggleFileCollapsed: vi.fn(),
    requestRevealLine: vi.fn(),
    consumeRevealTarget: vi.fn(),
    ensureFileReady: vi.fn(async (_path: string) => true),
    resetToHead: vi.fn(async () => {}),
    selectCommit: vi.fn(async () => {}),
  };
  render(ReviewCommentsSection, {
    context: new Map<symbol, unknown>([[STORES_KEY, {
      detail: {
        getReviewCommentsByFilePath: () => new Map([["a.go", comments]]),
        getReviewCommentAnchor: (id: number) => byID.get(id),
        getReviewCommentPlacement: (c: PublishedReviewComment) => placeReviewComment(c, byID.get(c.id)),
      },
      diff,
      viewer: { getLogin: () => "alice" },
    }]]),
  });
  return diff;
}

describe("ReviewCommentsSection", () => {
  // jsdom ships no CSS.escape, which the jump's selector uses.
  beforeEach(() => {
    vi.stubGlobal("CSS", { escape: (s: string) => s });
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists a moved comment at its line in the diff on screen and jumps there in place", async () => {
    const diff = renderSection(
      [comment({ id: 1, body: "moved note" })],
      [{ comment_id: 1, state: "moved", path: "a.go", line: 9 }],
    );
    expect(screen.getByText("+9")).toBeTruthy();

    await fireEvent.click(screen.getByText("moved note"));

    expect(diff.selectCommit).not.toHaveBeenCalled();
    expect(diff.resetToHead).not.toHaveBeenCalled();
    // The target file is made ready (expanded, loaded, mounted) first.
    expect(diff.ensureFileReady).toHaveBeenCalledWith("a.go");
  });

  it("badges a removed comment and jumps to the commit it was made on", async () => {
    const diff = renderSection(
      [comment({ id: 2, body: "gone note" })],
      [{ comment_id: 2, state: "removed" }],
    );
    expect(screen.getByText("+3")).toBeTruthy();
    expect(screen.getByText("line removed")).toBeTruthy();

    await fireEvent.click(screen.getByText("gone note"));

    expect(diff.selectCommit).toHaveBeenCalledWith("sha-old");
  });
});
