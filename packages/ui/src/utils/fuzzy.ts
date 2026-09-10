// Subsequence fuzzy matcher shared by command-palette-style file/doc
// finders. Lower score = tighter/better match; null = no match. An empty
// query matches everything with score 0.
export function fuzzyScore(text: string, query: string): number | null {
  if (query === "") return 0;
  const t = text.toLowerCase();
  const q = query.toLowerCase();
  let from = 0;
  let score = 0;
  let last = -1;
  for (const ch of q) {
    const idx = t.indexOf(ch, from);
    if (idx === -1) return null;
    if (last >= 0) score += idx - last; // reward compact matches
    last = idx;
    from = idx + 1;
  }
  return score;
}

// Filters and sorts candidates by fuzzyScore against query, best match
// first, ties broken alphabetically. Unmatched candidates are dropped.
export function fuzzyFilter(candidates: string[], query: string): string[] {
  if (!query) return [...candidates].sort((a, b) => a.localeCompare(b));
  return candidates
    .map((c) => ({ c, score: fuzzyScore(c, query) }))
    .filter((x): x is { c: string; score: number } => x.score !== null)
    .sort((a, b) => a.score - b.score || a.c.localeCompare(b.c))
    .map((x) => x.c);
}
