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
