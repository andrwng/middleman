import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ReviewCommentCard from "./ReviewCommentCard.svelte";
import { STORES_KEY } from "../../context.js";
import { placeReviewComment, type PublishedReviewComment } from "../../stores/detail.svelte.js";
import type { ReviewCommentAnchor } from "../../api/types.js";

function baseComment(over: Partial<PublishedReviewComment> = {}): PublishedReviewComment {
  return {
    id: 100,
    author: "alice",
    body: "looks good",
    createdAt: "2026-05-21T10:00:00Z",
    path: "f.go",
    line: 1,
    startLine: null,
    side: "RIGHT",
    commitId: "",
    htmlUrl: "",
    inReplyTo: 0,
    isHidden: false,
    ...over,
  };
}

function withStores(stores: object) {
  return new Map<symbol, unknown>([[STORES_KEY, stores]]);
}

function makeDetailStub(over: Record<string, unknown> = {}, anchor?: ReviewCommentAnchor) {
  return {
    hideReviewThread: vi.fn(async () => {}),
    unhideReviewThread: vi.fn(async () => {}),
    getReviewCommentAnchor: () => anchor,
    getReviewCommentPlacement: (c: PublishedReviewComment) => placeReviewComment(c, anchor),
    ...over,
  };
}

function makeDiffStub() {
  return {
    getCommits: () => [{ sha: "deadbeef" }],
    addDraftComment: vi.fn(),
  };
}

describe("ReviewCommentCard hide controls", () => {
  it("shows Hide button on a thread root", () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment(),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(screen.getByLabelText("Hide thread")).toBeTruthy();
  });

  it("does not show Hide button on a reply", () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 101, inReplyTo: 100 }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(screen.queryByLabelText("Hide thread")).toBeNull();
  });

  it("clicking Hide calls hideReviewThread with the root id", async () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 555 }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    await fireEvent.click(screen.getByLabelText("Hide thread"));
    expect(detailStub.hideReviewThread).toHaveBeenCalledWith(555);
  });

  it("applies rc--hidden class and Unhide button when isHidden=true", async () => {
    const detailStub = makeDetailStub();
    const { container } = render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 600, isHidden: true }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(container.querySelector(".rc.rc--hidden")).toBeTruthy();
    await fireEvent.click(screen.getByLabelText("Unhide thread"));
    expect(detailStub.unhideReviewThread).toHaveBeenCalledWith(600);
  });
});

describe("ReviewCommentCard re-anchoring", () => {
  function renderCard(comment: PublishedReviewComment, anchor?: ReviewCommentAnchor, diff = makeDiffStub()) {
    return render(ReviewCommentCard, {
      props: { comment, repoOwner: "acme", repoName: "widget", currentHeadSha: "deadbeef" },
      context: withStores({ detail: makeDetailStub({}, anchor), diff }),
    });
  }

  it("labels a moved comment with its new line and a moved pill", () => {
    const { container } = renderCard(
      baseComment({ line: 3, commitId: "c1" }),
      { comment_id: 100, state: "moved", path: "f.go", line: 9 },
    );
    expect(container.querySelector(".rc__anchor")?.textContent).toBe("+9");
    expect(screen.getByText("moved")).toBeTruthy();
    expect(screen.queryByText("outdated")).toBeNull();
  });

  it("trusts a current resolution over the commit compare", () => {
    renderCard(baseComment({ commitId: "c1" }), { comment_id: 100, state: "current", path: "f.go", line: 1 });
    expect(screen.queryByText("outdated")).toBeNull();
    expect(screen.queryByText("moved")).toBeNull();
  });

  it("marks a removed comment outdated", () => {
    renderCard(baseComment(), { comment_id: 100, state: "removed" });
    expect(screen.getByTitle("The commented line has since been removed")).toBeTruthy();
  });

  it("falls back to the commit compare without a resolution", () => {
    renderCard(baseComment({ commitId: "c1" }));
    expect(screen.getByText("outdated")).toBeTruthy();
  });

  it("drafts a reply where the card is drawn", async () => {
    const diff = makeDiffStub();
    renderCard(
      baseComment({ line: 3, commitId: "c1" }),
      { comment_id: 100, state: "moved", path: "g.go", line: 9 },
      diff,
    );
    await fireEvent.click(screen.getByLabelText("Draft a reply"));
    await fireEvent.input(screen.getByPlaceholderText(/Reply to alice/), { target: { value: "ack" } });
    await fireEvent.click(screen.getByText("Save draft"));
    expect(diff.addDraftComment).toHaveBeenCalledWith(
      expect.objectContaining({ path: "g.go", line: 9, inReplyTo: 100 }),
    );
  });
});
