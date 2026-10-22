import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";

const prState = { owner: "acme" };
const chatState = { open: new Set<string>() };
const chatOpen = vi.fn((_pr: unknown, id: string) => chatState.open.add(id));

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: {
      getEditRequest: () => null,
      ackEditRequest: vi.fn(),
      updateDraftCommentBody: vi.fn(),
      getCommits: () => [{ sha: "abc" }],
      getCurrentPR: () => ({ owner: prState.owner, name: "widget", number: 1 }),
    },
    draftChat: {
      open: chatOpen, close: vi.fn(), send: vi.fn(), cancel: vi.fn(), apply: vi.fn(), revert: vi.fn(),
      isOpen: (id: string) => chatState.open.has(id),
      getMessages: () => [], isBusy: () => false, isSending: () => false, getError: () => null,
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

  it("offers Ask Claude on GitHub and local drafts but not on replies", () => {
    const gh = render(PendingCommentCard, { props: { comment: base, currentHeadSha: "abc", ondelete: vi.fn() } });
    expect(gh.getByText("Ask Claude")).toBeTruthy();
    gh.unmount();
    const reply = render(PendingCommentCard, { props: { comment: { ...base, inReplyTo: 9 }, currentHeadSha: "abc", ondelete: vi.fn() } });
    expect(reply.queryByText("Ask Claude")).toBeNull();
    reply.unmount();
    prState.owner = "local";
    const local = render(PendingCommentCard, { props: { comment: base, currentHeadSha: "abc", ondelete: vi.fn() } });
    expect(local.getByText("Ask Claude")).toBeTruthy();
    prState.owner = "acme";
  });

  it("keeps the chat open across a remount of the card", () => {
    chatState.open.add(base.id);
    const { getByPlaceholderText } = render(PendingCommentCard, { props: { comment: base, currentHeadSha: "abc", ondelete: vi.fn() } });
    expect(getByPlaceholderText(/Ask about this comment/)).toBeTruthy();
    chatState.open.clear();
  });
});
