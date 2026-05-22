import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ReviewCommentCard from "./ReviewCommentCard.svelte";
import { STORES_KEY } from "../../context.js";
import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

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

function makeDetailStub(over: Record<string, unknown> = {}) {
  return {
    hideReviewThread: vi.fn(async () => {}),
    unhideReviewThread: vi.fn(async () => {}),
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
