// Per-line anchor spans let the rendered markdown viewer resolve a
// user's text selection back to a source-line range, the same way
// the diff view's <tr> rows do. The spans carry data-anchor-line
// (1-based source line) and data-anchor-side ("LEFT" or "RIGHT").

export type AnchorSide = "LEFT" | "RIGHT";

const ESC: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESC[c]!);
}

function span(line: number, side: AnchorSide, inner: string): string {
  return `<span class="rmd-anchor" data-anchor-line="${line}" data-anchor-side="${side}">${inner}</span>`;
}

// wrapProseBlock splits raw on \n (markdown soft-wrap boundaries),
// runs each segment through the caller-supplied inline parser, and
// joins with a single space — the same join markdown's HTML output
// uses for soft-wrapped lines inside a paragraph.
export function wrapProseBlock(
  raw: string,
  startLine: number,
  side: AnchorSide,
  parseInline: (segment: string) => string,
): string {
  const lines = raw.split("\n");
  return lines
    .map((seg, i) => span(startLine + i, side, parseInline(seg)))
    .join(" ");
}

// wrapCodeBlock preserves newlines between segments because <pre>
// renders them as line breaks. Inline content is NOT parsed —
// code is rendered literally with HTML escaping applied.
export function wrapCodeBlock(
  raw: string,
  startLine: number,
  side: AnchorSide,
): string {
  if (raw === "") return "";
  const lines = raw.split("\n");
  return lines
    .map((seg, i) => span(startLine + i, side, escapeHtml(seg)))
    .join("\n");
}

export interface AnchorRange {
  startLine: number;
  endLine: number;
  side: AnchorSide;
}

// nearestAnchor walks up from node looking for an ancestor with
// data-anchor-line. Returns null if none is found inside root.
function nearestAnchor(node: Node | null, root: HTMLElement): HTMLElement | null {
  let cur: Node | null = node;
  while (cur && cur !== root) {
    if (cur.nodeType === Node.ELEMENT_NODE) {
      const el = cur as HTMLElement;
      if (el.dataset.anchorLine != null) return el;
    }
    cur = cur.parentNode;
  }
  return null;
}

export function computeRangeFromSelection(
  root: HTMLElement,
  sel: Selection | null,
): AnchorRange | null {
  if (!sel || sel.rangeCount === 0) return null;
  const anchorEl = nearestAnchor(sel.anchorNode, root);
  const focusEl = nearestAnchor(sel.focusNode, root);
  if (!anchorEl || !focusEl) return null;
  if (!root.contains(anchorEl) || !root.contains(focusEl)) return null;
  const aSide = anchorEl.dataset.anchorSide as AnchorSide | undefined;
  const fSide = focusEl.dataset.anchorSide as AnchorSide | undefined;
  if (!aSide || !fSide || aSide !== fSide) return null;
  const a = parseInt(anchorEl.dataset.anchorLine ?? "", 10);
  const f = parseInt(focusEl.dataset.anchorLine ?? "", 10);
  if (Number.isNaN(a) || Number.isNaN(f)) return null;
  const [startLine, endLine] = a < f ? [a, f] : [f, a];
  return { startLine, endLine, side: aSide };
}
