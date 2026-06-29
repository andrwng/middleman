import { describe, it, expect } from "vitest";
import { resolveStack, clampGutterWidth, MIN_GUTTER_WIDTH, MIN_BODY_WIDTH } from "./gutterStack";

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

describe("clampGutterWidth", () => {
  const WIDE = 1200; // plenty of room: max = 1200 - 320 = 880

  it("passes through a width within range", () => {
    expect(clampGutterWidth(400, WIDE)).toBe(400);
  });
  it("floors at MIN_GUTTER_WIDTH when the desired width is too small", () => {
    expect(clampGutterWidth(50, WIDE)).toBe(MIN_GUTTER_WIDTH);
  });
  it("caps so the body keeps at least MIN_BODY_WIDTH", () => {
    // max = 1200 - 320 = 880; a larger request is capped there.
    expect(clampGutterWidth(2000, WIDE)).toBe(WIDE - MIN_BODY_WIDTH);
  });
  it("falls back to the gutter floor when the viewport is too narrow for both minimums", () => {
    // viewWidth 300 → 300-320 = -20 < MIN_GUTTER_WIDTH, so the floor wins.
    expect(clampGutterWidth(260, 300)).toBe(MIN_GUTTER_WIDTH);
  });
});
