import type { DiffHunk, DiffLine } from "../api/types.js";

// A row in the side-by-side layout. `lineIdx` indexes back into the
// original unified hunk.lines array, so callers can reuse the token
// cache keyed by (hunkIdx, lineIdx). `null` means "empty half" — render
// a blank cell to keep the opposing side aligned.
export interface SideBySideCell {
  line: DiffLine;
  lineIdx: number;
}

export interface SideBySideRow {
  left: SideBySideCell | null;
  right: SideBySideCell | null;
}

// pairHunk converts a unified hunk into side-by-side rows.
//
// Algorithm, matching how GitHub lays out split diffs:
//   - Context lines appear on both sides, same row.
//   - A contiguous run of deletes/adds is paired positionally: the j-th
//     delete lines up with the j-th add. Leftover deletes render with
//     an empty right cell; leftover adds render with an empty left cell.
//
// Pairing is positional (not diff-word-based) — callers that want
// intra-line word diffs can layer that on top.
export function pairHunk(hunk: DiffHunk): SideBySideRow[] {
  const rows: SideBySideRow[] = [];
  const lines = hunk.lines;

  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;

    if (line.type === "context") {
      rows.push({
        left: { line, lineIdx: i },
        right: { line, lineIdx: i },
      });
      i++;
      continue;
    }

    // Gather the full contiguous run of non-context lines. git's unified
    // output puts all deletes first, then all adds — but don't assume
    // that: group by type so an ill-formed hunk still pairs sensibly.
    const deletes: SideBySideCell[] = [];
    const adds: SideBySideCell[] = [];
    while (i < lines.length && lines[i]!.type !== "context") {
      const l = lines[i]!;
      if (l.type === "delete") deletes.push({ line: l, lineIdx: i });
      else if (l.type === "add") adds.push({ line: l, lineIdx: i });
      i++;
    }

    const pairs = Math.max(deletes.length, adds.length);
    for (let j = 0; j < pairs; j++) {
      rows.push({
        left: deletes[j] ?? null,
        right: adds[j] ?? null,
      });
    }
  }

  return rows;
}
