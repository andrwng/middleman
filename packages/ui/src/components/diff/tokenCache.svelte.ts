// Per-file syntax-token cache for DiffFile's batched tokenization.
//
// Entries accumulate in one plain (non-reactive) Map; a version signal
// tells readers when a batch landed or the cache was reset. Each batch
// therefore costs O(batch) instead of copying every entry so far into a
// fresh Map, which made tokenizing a large file quadratic.
//
// Writers never read the version signal, so an $effect that resets and
// fills the cache does not come to depend on its own writes.

export interface TokenCache<T> {
  // Reactive: a reader re-runs after the next addBatch or reset.
  get(key: string): T | undefined;
  addBatch(entries: Iterable<readonly [string, T]>): void;
  reset(): void;
  readonly size: number;
}

export function createTokenCache<T>(): TokenCache<T> {
  let map = new Map<string, T>();
  let counter = 0;
  let version = $state(0);

  function bump(): void {
    version = ++counter;
  }

  return {
    get(key) {
      void version;
      return map.get(key);
    },
    addBatch(entries) {
      for (const [k, v] of entries) map.set(k, v);
      bump();
    },
    reset() {
      map = new Map();
      bump();
    },
    get size() {
      return map.size;
    },
  };
}
