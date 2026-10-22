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

  it("flags a draft the server has not saved", () => {
    const { getByRole } = render(PendingCommentCard, {
      props: { comment: { ...base, syncError: "database is locked" }, currentHeadSha: "abc", ondelete: vi.fn() },
    });
    const alert = getByRole("alert");
    expect(alert.textContent).toContain("Not saved");
    expect(alert.textContent).toContain("database is locked");
  });

  it("marks drafts that came from Claude", () => {
    const claude = render(PendingCommentCard, {
      props: { comment: { ...base, origin: "claude" }, currentHeadSha: "abc", ondelete: vi.fn() },
    });
    expect(claude.getByText("Claude")).toBeTruthy();
    claude.unmount();
    const user = render(PendingCommentCard, { props: { comment: base, currentHeadSha: "abc", ondelete: vi.fn() } });
    expect(user.queryByText("Claude")).toBeNull();
  });
});
