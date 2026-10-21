import { describe, expect, it } from "vitest";
import { SortedIndexSet } from "./activeFile.js";

describe("SortedIndexSet", () => {
  it("keeps the largest member available as last()", () => {
    const s = new SortedIndexSet();
    expect(s.last()).toBeUndefined();
    for (const i of [4, 1, 9, 6]) s.add(i);
    expect(s.last()).toBe(9);
    s.add(9); // duplicate
    s.delete(9);
    expect(s.last()).toBe(6);
    s.delete(3); // absent
    s.delete(6);
    s.delete(4);
    expect(s.last()).toBe(1);
    expect(s.has(1)).toBe(true);
    expect(s.has(4)).toBe(false);
    s.clear();
    expect(s.last()).toBeUndefined();
    expect(s.size).toBe(0);
  });

  it("matches a brute-force max over random adds and deletes", () => {
    const s = new SortedIndexSet();
    const ref = new Set<number>();
    let seed = 7;
    const rand = () => (seed = (seed * 1103515245 + 12345) % 2147483648) % 50;
    for (let n = 0; n < 2000; n++) {
      const i = rand();
      if (n % 3 === 0) { s.delete(i); ref.delete(i); } else { s.add(i); ref.add(i); }
      const max = ref.size ? Math.max(...ref) : undefined;
      expect(s.last()).toBe(max);
      expect(s.size).toBe(ref.size);
    }
  });
});
