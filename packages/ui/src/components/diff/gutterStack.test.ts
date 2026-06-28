import { describe, it, expect } from "vitest";
import { resolveStack } from "./gutterStack";

describe("resolveStack", () => {
  it("returns [] for empty input", () => {
    expect(resolveStack([], 8)).toEqual([]);
  });
  it("keeps a single item at its desiredTop", () => {
    expect(resolveStack([{ desiredTop: 100, height: 40 }], 8)).toEqual([100]);
  });
  it("leaves non-overlapping items unchanged", () => {
    expect(resolveStack([{ desiredTop: 0, height: 20 }, { desiredTop: 100, height: 20 }], 8)).toEqual([0, 100]);
  });
  it("pushes an overlapping item below the previous one + gap", () => {
    // first occupies [0,30); second wants 10 but must clear 30+8=38
    expect(resolveStack([{ desiredTop: 0, height: 30 }, { desiredTop: 10, height: 20 }], 8)).toEqual([0, 38]);
  });
  it("returns tops in INPUT order even when desiredTops are out of order", () => {
    const tops = resolveStack([{ desiredTop: 100, height: 20 }, { desiredTop: 0, height: 20 }], 8);
    expect(tops).toEqual([100, 0]); // item 0 wanted 100 and got it; item 1 wanted 0 and got it
  });
});
