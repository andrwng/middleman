import { afterEach, describe, expect, it, vi } from "vitest";
import { createAIReviewRunStore, DISMISSED_RUNS_KEY } from "./aiReviewRun.svelte.js";

function run(status: string, extra: Record<string, unknown> = {}) {
  return { id: 1, status, head_sha: "h", instructions: "", summary: "", raw_json: "", suggestion_count: 0, created_at: "2026-01-01T00:00:00Z", ...extra };
}

// serve answers every request with the given latest run.
function serve(r: ReturnType<typeof run> | null): void {
  globalThis.fetch = vi.fn(async () =>
    new Response(JSON.stringify({ run: r }), { status: 200 }),
  ) as unknown as typeof fetch;
}

// started returns a store for acme/widget#1 once its latest run has loaded.
async function started() {
  const store = createAIReviewRunStore();
  store.start("acme", "widget", 1);
  await vi.waitFor(() => expect(store.getRun()).not.toBeNull());
  return store;
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  localStorage.clear();
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

  it("owns the panel's open state and opens it when a run starts", async () => {
    globalThis.fetch = vi.fn(async (_u: unknown, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response(JSON.stringify(run("queued")), { status: 200 })
        : new Response(JSON.stringify({ run: null }), { status: 200 }),
    ) as unknown as typeof fetch;
    const store = createAIReviewRunStore();
    store.start("acme", "widget", 1);
    expect(store.isPanelOpen()).toBe(false);
    store.togglePanel();
    expect(store.isPanelOpen()).toBe(true);
    store.closePanel();
    expect(store.isPanelOpen()).toBe(false);
    expect(await store.startRun("focus")).toBe(true);
    expect(store.isPanelOpen()).toBe(true);
    store.stop();
    expect(store.isPanelOpen()).toBe(false);
  });
});

describe("aiReviewRun store dismissal", () => {
  it("dismissing a finished run hides it, closes the panel, and survives a reload", async () => {
    serve(run("done"));
    const store = await started();
    store.openPanel();
    expect(store.isDismissed()).toBe(false);
    store.dismiss();
    expect(store.isDismissed()).toBe(true);
    expect(store.isPanelOpen()).toBe(false);
    store.stop();

    const reloaded = await started();
    expect(reloaded.isDismissed()).toBe(true);
    reloaded.stop();
  });

  it("a reused run id with a new created_at is not dismissed", async () => {
    localStorage.setItem(DISMISSED_RUNS_KEY, JSON.stringify(["1@2026-01-01T00:00:00Z"]));
    serve(run("done", { created_at: "2026-02-01T00:00:00Z" }));
    const store = await started();
    expect(store.isDismissed()).toBe(false);
    store.stop();
  });

  it.each(["queued", "running"])("a %s run can't be dismissed", async (status) => {
    serve(run(status));
    const store = await started();
    store.dismiss();
    expect(store.isDismissed()).toBe(false);
    expect(localStorage.getItem(DISMISSED_RUNS_KEY)).toBeNull();
    store.stop();
  });

  it.each([
    ["not JSON", "{not json"],
    ["not an array", JSON.stringify({ "1@2026-01-01T00:00:00Z": true })],
    ["non-string entries", JSON.stringify([1, null])],
  ])("unreadable storage (%s) counts as nothing dismissed", async (_label, stored) => {
    localStorage.setItem(DISMISSED_RUNS_KEY, stored);
    serve(run("done"));
    const store = await started();
    expect(store.isDismissed()).toBe(false);
    store.dismiss();
    expect(JSON.parse(localStorage.getItem(DISMISSED_RUNS_KEY) ?? "null")).toEqual(["1@2026-01-01T00:00:00Z"]);
    store.stop();
  });

  it("dismiss still works for this page when storage throws", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage denied");
    });
    serve(run("done"));
    const store = await started();
    expect(store.isDismissed()).toBe(false);
    expect(() => store.dismiss()).not.toThrow();
    expect(store.isDismissed()).toBe(true);
    store.stop();
  });

  it("a run started after a dismissal shows again", async () => {
    globalThis.fetch = vi.fn(async (_u: unknown, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response(JSON.stringify(run("queued", { id: 2 })), { status: 200 })
        : new Response(JSON.stringify({ run: run("done") }), { status: 200 }),
    ) as unknown as typeof fetch;
    const store = await started();
    store.dismiss();
    expect(await store.startRun("again")).toBe(true);
    expect(store.isDismissed()).toBe(false);
    expect(store.isPanelOpen()).toBe(true);
    store.stop();
  });
});
