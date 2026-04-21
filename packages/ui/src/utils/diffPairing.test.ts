import { describe, expect, it } from "vitest";
import type { DiffHunk, DiffLine } from "../api/types.js";
import { pairHunk, type SideBySideRow } from "./diffPairing.js";

function hunk(lines: DiffLine[]): DiffHunk {
  return {
    old_start: 1,
    old_count: lines.filter((l) => l.type !== "add").length,
    new_start: 1,
    new_count: lines.filter((l) => l.type !== "delete").length,
    lines,
  };
}

function ctx(content: string, oldNum: number, newNum: number): DiffLine {
  return { type: "context", content, old_num: oldNum, new_num: newNum };
}

function del(content: string, oldNum: number): DiffLine {
  return { type: "delete", content, old_num: oldNum };
}

function add(content: string, newNum: number): DiffLine {
  return { type: "add", content, new_num: newNum };
}

type Shape =
  | [string | null, string | null]; // [left content or null, right content or null]

function shapeOf(rows: SideBySideRow[]): Shape[] {
  return rows.map((r) => [
    r.left ? r.left.line.content : null,
    r.right ? r.right.line.content : null,
  ]);
}

describe("pairHunk", () => {
  it("handles pure context", () => {
    const h = hunk([ctx("a", 1, 1), ctx("b", 2, 2)]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["a", "a"],
      ["b", "b"],
    ]);
  });

  it("handles pure adds (new file)", () => {
    const h = hunk([add("x", 1), add("y", 2)]);
    expect(shapeOf(pairHunk(h))).toEqual([
      [null, "x"],
      [null, "y"],
    ]);
  });

  it("handles pure deletes (removed file)", () => {
    const h = hunk([del("x", 1), del("y", 2)]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["x", null],
      ["y", null],
    ]);
  });

  it("pairs balanced delete + add runs positionally", () => {
    const h = hunk([
      ctx("a", 1, 1),
      del("b-old", 2),
      del("c-old", 3),
      add("b-new", 2),
      add("c-new", 3),
      ctx("d", 4, 4),
    ]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["a", "a"],
      ["b-old", "b-new"],
      ["c-old", "c-new"],
      ["d", "d"],
    ]);
  });

  it("pads with empty right when there are more deletes than adds", () => {
    const h = hunk([
      del("x", 1),
      del("y", 2),
      del("z", 3),
      add("X", 1),
    ]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["x", "X"],
      ["y", null],
      ["z", null],
    ]);
  });

  it("pads with empty left when there are more adds than deletes", () => {
    const h = hunk([
      del("x", 1),
      add("X", 1),
      add("Y", 2),
      add("Z", 3),
    ]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["x", "X"],
      [null, "Y"],
      [null, "Z"],
    ]);
  });

  it("separates independent runs split by context lines", () => {
    const h = hunk([
      del("a", 1),
      add("A", 1),
      ctx("mid", 2, 2),
      del("b", 3),
      add("B", 3),
    ]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["a", "A"],
      ["mid", "mid"],
      ["b", "B"],
    ]);
  });

  it("returns lineIdx pointing back to the original unified line index", () => {
    // Reusing the unified-hunk index lets the caller share the
    // tokenization cache keyed by (hunkIdx, lineIdx).
    const h = hunk([
      ctx("a", 1, 1), // 0
      del("b", 2), // 1
      add("B", 2), // 2
    ]);
    const rows = pairHunk(h);
    expect(rows[0]!.left!.lineIdx).toBe(0);
    expect(rows[0]!.right!.lineIdx).toBe(0);
    expect(rows[1]!.left!.lineIdx).toBe(1);
    expect(rows[1]!.right!.lineIdx).toBe(2);
  });

  it("returns an empty array for an empty hunk", () => {
    expect(pairHunk(hunk([]))).toEqual([]);
  });

  it("handles adds interleaved with deletes (not strictly grouped)", () => {
    // Some diff producers interleave del/add within a single run.
    // pairHunk groups them by type within the contiguous non-context
    // run, so pairing is still positional across the run.
    const h = hunk([
      del("a", 1),
      add("A", 1),
      del("b", 2),
      add("B", 2),
    ]);
    expect(shapeOf(pairHunk(h))).toEqual([
      ["a", "A"],
      ["b", "B"],
    ]);
  });
});
