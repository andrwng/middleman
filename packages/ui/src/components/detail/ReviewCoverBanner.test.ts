import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import ReviewCoverBanner from "./ReviewCoverBanner.svelte";
import type { components } from "../../api/generated/schema.js";

type MergeRequest = components["schemas"]["MergeRequest"];

// Minimal PR fixture — the banner only reads Title, Author, Number, and
// Body. Every other field is required by the schema but never touched
// by the component, so the cheapest stub is "fill with zeros / empties".
function makePR(): MergeRequest {
  return {
    Additions: 0,
    Author: "alice",
    AuthorDisplayName: "Alice",
    BaseBranch: "main",
    Body: "Cover body content.",
    CIChecksJSON: "",
    CIHadPending: false,
    CIStatus: "",
    ClosedAt: null,
    CommentCount: 0,
    CreatedAt: "2026-05-01T00:00:00Z",
    Deletions: 0,
    DetailFetchedAt: null,
    HeadBranch: "feat",
    HeadRepoCloneURL: "",
    ID: 1,
    IsDraft: false,
    KanbanStatus: "",
    LastActivityAt: "2026-05-01T00:00:00Z",
    MergeableState: "",
    MergedAt: null,
    Number: 1,
    PlatformID: 1,
    RepoID: 1,
    ReviewDecision: "",
    Starred: false,
    State: "open",
    Title: "Cover title",
    URL: "https://example.com/pr/1",
    UpdatedAt: "2026-05-01T00:00:00Z",
    requested_reviewers: null,
  };
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("ReviewCoverBanner forceExpanded override", () => {
  it("renders body when forceExpanded=true even if pr-cover-collapsed=true", () => {
    // Reproduces the peek-flow bug: a user collapsed the cover banner,
    // then later peeked it from the consolidated strip — without the
    // override the banner would mount in its persisted collapsed state
    // and the peek would show nothing but the chevron header.
    localStorage.setItem("pr-cover-collapsed", "true");
    const { container } = render(ReviewCoverBanner, {
      props: {
        pr: makePR(),
        owner: "acme",
        name: "widget",
        forceExpanded: true,
      },
    });
    expect(container.querySelector(".review-cover__body")).toBeTruthy();
  });

  it("respects pr-cover-collapsed=true when forceExpanded is omitted", () => {
    // The non-consolidated branch in PullDetail does not pass the prop.
    // Persisted collapse must still hide the body there.
    localStorage.setItem("pr-cover-collapsed", "true");
    const { container } = render(ReviewCoverBanner, {
      props: { pr: makePR(), owner: "acme", name: "widget" },
    });
    expect(container.querySelector(".review-cover__body")).toBeNull();
  });

  it("chevron shows expanded state when forceExpanded=true even if pr-cover-collapsed=true", () => {
    // Effective collapsed state must follow forceExpanded so the
    // chevron rotation matches the visible body. Otherwise a peeked
    // banner shows a rotated "collapsed" chevron over an open body.
    localStorage.setItem("pr-cover-collapsed", "true");
    const { container } = render(ReviewCoverBanner, {
      props: {
        pr: makePR(),
        owner: "acme",
        name: "widget",
        forceExpanded: true,
      },
    });
    expect(container.querySelector(".review-cover__chevron--collapsed")).toBeNull();
  });
});
