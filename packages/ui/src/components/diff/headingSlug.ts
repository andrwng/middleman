// headingSlug.ts
// GitHub-compatible heading anchor slugs (matches the github-slugger algorithm)
// so that links written against GitHub's convention — e.g. [x](#foo--bar) —
// resolve to the rendered headings. Pure (no DOM) for testability.

// Lowercase, drop punctuation, and map each remaining whitespace character to a
// single hyphen WITHOUT collapsing runs. A heading such as "Foo + Bar" slugs to
// "foo--bar": the "+" is dropped and each of its two surrounding spaces becomes
// a hyphen — the same anchor GitHub generates. Unicode letters/numbers, "_" and
// existing hyphens are preserved.
export function slugify(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s_-]/gu, "")
    .replace(/\s/g, "-");
}

// De-duplicate slugs within a document (GitHub appends -1, -2, ... to repeats).
export function uniqueSlug(text: string, counts: Map<string, number>): string {
  const base = slugify(text) || "section";
  const n = counts.get(base) ?? 0;
  counts.set(base, n + 1);
  return n === 0 ? base : `${base}-${n}`;
}
