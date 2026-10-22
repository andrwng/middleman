import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync } from "svelte";
import { createDiffStore } from "./diff.svelte.js";
import type { MiddlemanClient } from "../types.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

afterEach(() => vi.restoreAllMocks());

describe("hasDraftForPR in reactive contexts", () => {
  it("can be read inside $derived and updates when the summary arrives", async () => {
    globalThis.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ prs: [{ owner: "acme", name: "widget", number: 7, count: 1 }] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ) as unknown as typeof fetch;
    const store = createDiffStore({ client: stubClient() });
    const seen: boolean[] = [];
    const cleanup = $effect.root(() => {
      const has = $derived(store.hasDraftForPR("acme", "widget", 7));
      $effect(() => {
        seen.push(has);
      });
    });
    try {
      flushSync();
      await store.whenDraftsSettled();
      flushSync();
    } finally {
      cleanup();
    }
    expect(seen.at(-1)).toBe(true);
  });
});
