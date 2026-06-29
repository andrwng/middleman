// gutterStack.ts
// Place gutter cards top-to-bottom by desired position; an item that would
// overlap the one above is pushed down by `gap`. Pure (no DOM) for testability.
export function resolveStack(
  items: { desiredTop: number; height: number }[],
  gap: number,
): number[] {
  const order = items
    .map((_, i) => i)
    .sort((a, b) => items[a]!.desiredTop - items[b]!.desiredTop || a - b);
  const tops = new Array<number>(items.length);
  let prevBottom = -Infinity;
  for (const i of order) {
    const top = Math.max(items[i]!.desiredTop, prevBottom + gap); // first: prevBottom=-Inf → desiredTop
    tops[i] = top;
    prevBottom = top + items[i]!.height;
  }
  return tops;
}

// Gutter width resize policy. The comment gutter is horizontally resizable via
// a divider; the chosen width is clamped so the gutter stays usable and the
// document body keeps a minimum readable width. Pure (no DOM) for testability.
export const MIN_GUTTER_WIDTH = 200;
export const MIN_BODY_WIDTH = 320;

export function clampGutterWidth(desired: number, viewWidth: number): number {
  // The gutter may not push the body below MIN_BODY_WIDTH; if the viewport is
  // too narrow for both minimums, the gutter floor (MIN_GUTTER_WIDTH) wins.
  const max = Math.max(MIN_GUTTER_WIDTH, viewWidth - MIN_BODY_WIDTH);
  return Math.max(MIN_GUTTER_WIDTH, Math.min(max, desired));
}
