import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: {
      getEditRequest: () => null,
      ackEditRequest: vi.fn(),
      updateDraftCommentBody: vi.fn(),
      getCommits: () => [{ sha: "abc" }],
    },
  }),
}));

import PendingCommentCard from "./PendingCommentCard.svelte";

afterEach(() => cleanup());

const base = { id: "d-1", path: "a.go", line: 3, side: "RIGHT" as const, commitSha: "abc", body: "nit", createdAt: "2026-01-01T00:00:00Z" };

describe("PendingCommentCard publish error", () => {
  it("shows the draft's publish error", () => {
    const { getByRole } = render(PendingCommentCard, {
      props: { comment: { ...base, submitError: "GitHub couldn't anchor comment[0]" }, currentHeadSha: "abc", ondelete: vi.fn() },
    });
    expect(getByRole("alert").textContent).toContain("couldn't anchor");
  });

  it("shows no alert without an error", () => {
    const { queryByRole } = render(PendingCommentCard, {
      props: { comment: base, currentHeadSha: "abc", ondelete: vi.fn() },
    });
    expect(queryByRole("alert")).toBeNull();
  });
});
