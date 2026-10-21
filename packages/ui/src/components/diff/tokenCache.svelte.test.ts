import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync } from "svelte";
import { createTokenCache } from "./tokenCache.svelte.js";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("createTokenCache", () => {
  it("accumulates entries across batches and resets to empty", () => {
    const cache = createTokenCache<number>();
    cache.addBatch([["0:0", 1], ["0:1", 2]]);
    cache.addBatch([["1:0", 3]]);
    expect(cache.get("0:0")).toBe(1);
    expect(cache.get("1:0")).toBe(3);
    expect(cache.size).toBe(3);
    cache.reset();
    expect(cache.get("0:0")).toBeUndefined();
    expect(cache.size).toBe(0);
  });

  it("costs O(batch) per batch, not a copy of everything so far", () => {
    const cache = createTokenCache<number>();
    const batches = 40;
    const batchSize = 50;
    const set = vi.spyOn(Map.prototype, "set");
    for (let b = 0; b < batches; b++) {
      const entries: Array<[string, number]> = [];
      for (let i = 0; i < batchSize; i++) entries.push([`${b}:${i}`, i]);
      cache.addBatch(entries);
    }
    // Copying the accumulated map each batch would be ~ batches^2 *
    // batchSize / 2 = 40,000 sets; accumulating in place is 2,000.
    expect(set.mock.calls.length).toBeLessThan(batches * batchSize * 2);
    expect(cache.size).toBe(batches * batchSize);
  });

  it("notifies readers once per batch and once per reset", () => {
    const cache = createTokenCache<number>();
    const seen: Array<number | undefined> = [];
    const cleanup = $effect.root(() => {
      $effect(() => {
        seen.push(cache.get("a"));
      });
    });
    flushSync();
    cache.addBatch([["a", 1], ["b", 2]]);
    flushSync();
    cache.addBatch([["c", 3]]);
    flushSync();
    cache.reset();
    flushSync();
    cleanup();
    expect(seen).toEqual([undefined, 1, 1, undefined]);
  });

  it("can reset inside an effect without the effect depending on its own write", () => {
    const cache = createTokenCache<number>();
    let runs = 0;
    const cleanup = $effect.root(() => {
      $effect(() => {
        runs++;
        cache.reset();
        cache.addBatch([["a", runs]]);
      });
    });
    flushSync();
    flushSync();
    cleanup();
    expect(runs).toBe(1);
  });
});
