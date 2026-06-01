import { describe, it, expect } from "vitest";
import { prMatchesViewerReviewer } from "./pulls.svelte.js";

describe("prMatchesViewerReviewer", () => {
  it("returns false when viewer login is empty", () => {
    const pr = { requested_reviewers: ["alice"], reviewer_logins: ["alice"] };
    expect(prMatchesViewerReviewer(pr, "")).toBe(false);
  });

  it("matches when viewer is on the currently-requested list", () => {
    const pr = { requested_reviewers: ["alice", "bob"], reviewer_logins: [] };
    expect(prMatchesViewerReviewer(pr, "alice")).toBe(true);
  });

  it("matches when viewer has already submitted a review (GitHub clears requested_reviewers)", () => {
    const pr = { requested_reviewers: [], reviewer_logins: ["alice"] };
    expect(prMatchesViewerReviewer(pr, "alice")).toBe(true);
  });

  it("case-insensitive match", () => {
    const pr = { requested_reviewers: ["Alice"], reviewer_logins: [] };
    expect(prMatchesViewerReviewer(pr, "alice")).toBe(true);
  });

  it("ignores team: entries — we can't tell if the viewer is in that team", () => {
    const pr = { requested_reviewers: ["team:platform"], reviewer_logins: [] };
    expect(prMatchesViewerReviewer(pr, "alice")).toBe(false);
  });

  it("returns false when viewer is on neither list", () => {
    const pr = { requested_reviewers: ["bob"], reviewer_logins: ["carol"] };
    expect(prMatchesViewerReviewer(pr, "alice")).toBe(false);
  });

  it("handles null/undefined lists gracefully", () => {
    expect(prMatchesViewerReviewer({ requested_reviewers: null, reviewer_logins: null }, "alice")).toBe(false);
    expect(prMatchesViewerReviewer({}, "alice")).toBe(false);
  });
});
