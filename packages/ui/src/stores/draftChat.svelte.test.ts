import { afterEach, describe, expect, it, vi } from "vitest";
import { createDraftChatStore } from "./draftChat.svelte.js";

const pr = { owner: "acme", name: "widget", number: 1 };

type Msg = { id: number; author: string; kind: string; body: string; applied: boolean; status: string; created_at: string; prev_body?: string; error?: string };
const msg = (id: number, over: Partial<Msg> = {}): Msg => ({
  id, author: "claude", kind: "message", body: "", applied: false, status: "done", created_at: "2026-01-01T00:00:00Z", ...over,
});

function installFetch(listReplies: Msg[][]) {
  const calls: { url: string; method: string; body: unknown }[] = [];
  globalThis.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ url, method, body: init?.body ? JSON.parse(init.body as string) : undefined });
    const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status });
    if (url.endsWith("/messages") && method === "GET") {
      const next = listReplies.length > 1 ? listReplies.shift()! : listReplies[0]!;
      return json({ messages: next });
    }
    if (url.endsWith("/messages") && method === "POST") {
      if ((JSON.parse(init!.body as string) as { body: string }).body === "boom") return json({ detail: "message too long" }, 400);
      return json({ messages: [msg(1, { author: "user", body: "why?" }), msg(2, { status: "queued" })] });
    }
    return json({ id: "s1", body: "x", state: "suggestion" });
  }) as unknown as typeof fetch;
  return calls;
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("draft chat store", () => {
  it("sends, polls while Claude is busy, and stops when it is done", async () => {
    vi.useFakeTimers();
    const calls = installFetch([[], [msg(1, { author: "user" }), msg(2, { status: "running" })], [msg(1, { author: "user" }), msg(2, { body: "Because." })]]);
    const store = createDraftChatStore();
    store.open(pr, "s1");
    await vi.advanceTimersByTimeAsync(0);
    expect(await store.send("s1", "why?")).toBe(true);
    expect(store.isBusy("s1")).toBe(true);
    await vi.advanceTimersByTimeAsync(1500);
    await vi.advanceTimersByTimeAsync(1500);
    expect(store.isBusy("s1")).toBe(false);
    expect(store.getMessages("s1").at(-1)?.body).toBe("Because.");
    const gets = calls.filter((c) => c.method === "GET").length;
    await vi.advanceTimersByTimeAsync(4500);
    expect(calls.filter((c) => c.method === "GET").length).toBe(gets);
    store.close("s1");
  });

  it("reports a new revision so the card can refresh", async () => {
    vi.useFakeTimers();
    installFetch([[msg(2, { status: "running" })], [msg(2, { status: "running" }), msg(3, { kind: "revision", body: "new", prev_body: "old", applied: true })], [msg(2), msg(3, { kind: "revision", applied: true })]]);
    const onRevision = vi.fn();
    const store = createDraftChatStore({ onRevision });
    store.open(pr, "s1");
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(1500);
    expect(onRevision).toHaveBeenCalledTimes(1);
    store.close("s1");
  });

  it("apply and revert call their routes and refresh the card", async () => {
    const calls = installFetch([[msg(3, { kind: "revision" })]]);
    const onRevision = vi.fn();
    const store = createDraftChatStore({ onRevision });
    store.open(pr, "s1");
    await store.apply("s1", 3);
    await store.revert("s1", 3);
    expect(calls.some((c) => c.url.endsWith("/messages/3/apply") && c.method === "POST")).toBe(true);
    expect(calls.some((c) => c.url.endsWith("/messages/3/revert") && c.method === "POST")).toBe(true);
    expect(onRevision).toHaveBeenCalledTimes(2);
    store.close("s1");
  });

  it("surfaces send errors", async () => {
    installFetch([[]]);
    const store = createDraftChatStore();
    store.open(pr, "s1");
    expect(await store.send("s1", "boom")).toBe(false);
    expect(store.getError("s1")).toBe("message too long");
    store.close("s1");
  });

  it("asks for a fresh session when requested", async () => {
    const calls = installFetch([[]]);
    const store = createDraftChatStore();
    store.open(pr, "s1");
    await store.send("s1", "why?", true);
    const post = calls.find((c) => c.method === "POST" && c.url.endsWith("/messages"))!;
    expect(post.body).toEqual({ body: "why?", fresh: true });
    store.close("s1");
  });

  it("closing while Claude is busy still refreshes the card when it revises", async () => {
    vi.useFakeTimers();
    installFetch([
      [msg(2, { status: "running" })],
      [msg(2, { status: "running" }), msg(3, { kind: "revision", body: "new", applied: true })],
      [msg(2), msg(3, { kind: "revision", body: "new", applied: true })],
    ]);
    const onRevision = vi.fn();
    const store = createDraftChatStore({ onRevision });
    store.open(pr, "s1");
    await vi.advanceTimersByTimeAsync(0);
    store.close("s1");
    expect(store.isOpen("s1")).toBe(false);
    await vi.advanceTimersByTimeAsync(1500);
    expect(onRevision).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1500);
    const before = (globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length;
    await vi.advanceTimersByTimeAsync(4500);
    expect((globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length).toBe(before);
  });
});
