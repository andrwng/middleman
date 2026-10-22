import { afterEach, describe, expect, it, vi } from "vitest";
import { createAIReviewRunStore } from "./aiReviewRun.svelte.js";

function run(status: string, extra: Record<string, unknown> = {}) {
  return { id: 1, status, head_sha: "h", instructions: "", summary: "", raw_json: "", suggestion_count: 0, created_at: "2026-01-01T00:00:00Z", ...extra };
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("aiReviewRun store", () => {
  it("polls while active and notifies once more when the run ends", async () => {
    vi.useFakeTimers();
    const replies = [run("running"), run("running"), run("done", { summary: "ok" })];
    globalThis.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ run: replies.shift() ?? run("done") }), { status: 200 }),
    ) as unknown as typeof fetch;
    const onActivity = vi.fn();
    const store = createAIReviewRunStore({ onActivity });
    store.start("acme", "widget", 1);
    await vi.advanceTimersByTimeAsync(0);
    expect(store.isActive()).toBe(true);
    await vi.advanceTimersByTimeAsync(1500);
    await vi.advanceTimersByTimeAsync(1500);
    expect(store.getRun()?.status).toBe("done");
    const calls = onActivity.mock.calls.length;
    await vi.advanceTimersByTimeAsync(4500);
    expect(onActivity.mock.calls.length).toBe(calls); // stopped polling
    store.stop();
  });

  it("reports a conflict when a run is already active", async () => {
    globalThis.fetch = vi.fn(async (_u: unknown, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response(JSON.stringify({ detail: "already in progress" }), { status: 409 })
        : new Response(JSON.stringify({ run: null }), { status: 200 }),
    ) as unknown as typeof fetch;
    const store = createAIReviewRunStore();
    store.start("acme", "widget", 1);
    expect(await store.startRun("focus")).toBe(false);
    expect(store.getError()).toContain("already in progress");
    store.stop();
  });
});
