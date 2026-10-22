import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/svelte";

const chat = {
  messages: [] as Array<Record<string, unknown>>,
  busy: false,
  error: null as string | null,
};
const open = vi.fn();
const close = vi.fn();
const send = vi.fn(async () => true);
const cancel = vi.fn(async () => {});
const apply = vi.fn(async () => {});
const revert = vi.fn(async () => {});

vi.mock("../../context.js", () => ({
  getStores: () => ({
    draftChat: {
      open, close, send, cancel, apply, revert,
      getMessages: () => chat.messages,
      isBusy: () => chat.busy,
      isSending: () => false,
      getError: () => chat.error,
    },
    diff: { getCurrentPR: () => ({ owner: "acme", name: "widget", number: 1 }) },
  }),
}));

import DraftChat from "./DraftChat.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  chat.messages = [];
  chat.busy = false;
  chat.error = null;
});

const m = (id: number, over: Record<string, unknown>) => ({ id, author: "claude", kind: "message", body: "", applied: false, status: "done", created_at: "2026-01-01T00:00:00Z", ...over });

describe("DraftChat", () => {
  it("renders both sides of the conversation", () => {
    chat.messages = [m(1, { body: "err is dropped" }), m(2, { author: "user", body: "why?" }), m(3, { body: "Because." })];
    const { getByText } = render(DraftChat, { props: { draftId: "s1" } });
    expect(getByText("why?")).toBeTruthy();
    expect(getByText("Because.")).toBeTruthy();
  });

  it("sends with Ctrl-Enter", async () => {
    const { getByRole } = render(DraftChat, { props: { draftId: "s1" } });
    const box = getByRole("textbox");
    await fireEvent.input(box, { target: { value: "make it shorter" } });
    await fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    expect(send).toHaveBeenCalledWith("s1", "make it shorter");
  });

  it("offers Revert for an applied revision and Apply for a pending one", async () => {
    chat.messages = [
      m(4, { kind: "revision", body: "new text", prev_body: "old text", applied: true }),
      m(5, { kind: "revision", body: "other text", prev_body: "new text", applied: false }),
    ];
    const { getByText } = render(DraftChat, { props: { draftId: "s1" } });
    await fireEvent.click(getByText("Revert"));
    expect(revert).toHaveBeenCalledWith("s1", 4);
    await fireEvent.click(getByText("Apply"));
    expect(apply).toHaveBeenCalledWith("s1", 5);
  });

  it("offers a fresh chat after a failed reply", async () => {
    chat.messages = [m(1, { author: "user", body: "why?" }), m(2, { status: "failed", error: "No conversation found" })];
    const { getByText } = render(DraftChat, { props: { draftId: "s1" } });
    expect(getByText(/No conversation found/)).toBeTruthy();
    await fireEvent.click(getByText("Start fresh chat"));
    expect(send).toHaveBeenCalledWith("s1", "why?", true);
  });

  it("lets the reviewer cancel a running reply", async () => {
    chat.messages = [m(2, { status: "running" })];
    chat.busy = true;
    const { getByText } = render(DraftChat, { props: { draftId: "s1" } });
    await fireEvent.click(getByText("Cancel"));
    expect(cancel).toHaveBeenCalledWith("s1", 2);
  });

  it("shows revision text literally so the change stays visible", () => {
    chat.messages = [m(4, { kind: "revision", body: "**new**", prev_body: "**old**", applied: true })];
    const { getByText, container } = render(DraftChat, { props: { draftId: "s1" } });
    expect(getByText("**new**")).toBeTruthy();
    expect(container.querySelector(".draft-chat__diff strong")).toBeNull();
  });
});
