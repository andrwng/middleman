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

// A marked inline token, narrowed to the two fields we need. The full
// Token union is structurally compatible.
export interface InlineToken {
  type: string;
  raw: string;
}

function countNewlines(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) === 10) n++;
  return n;
}

// wrapInlineTokens renders a block's already-parsed inline tokens into
// per-source-line anchor spans. With marked's `breaks: true`, soft-wrap
// boundaries appear as explicit `br` tokens; each one closes the current
// span and starts the next source line. Every other token is rendered
// whole via renderRaw and appended to the current line's span.
//
// Rendering whole tokens (rather than the old approach of splitting the
// raw string on \n and parsing each line independently) is what keeps
// inline markup that spans a soft-wrapped line — e.g. a bold phrase at
// the start of a wrapped bullet — intact. Splitting first fed each half
// to the inline parser separately, so `**foo` / `bar**` on adjacent
// lines parsed as literal asterisks instead of a single <strong>.
//
// A token whose own raw spans lines (bold that wraps mid-phrase) can't
// be split across spans, so it stays in one span anchored to where that
// span began; its internal newlines still advance the line counter so
// later spans keep their source-line mapping. Internal newlines are
// collapsed to spaces before rendering, matching markdown's soft-wrap.
export function wrapInlineTokens(
  tokens: InlineToken[],
  startLine: number,
  side: AnchorSide,
  renderRaw: (raw: string) => string,
): string {
  const spans: string[] = [];
  let line = startLine;
  let bufStartLine = startLine;
  let cur = "";
  for (const tok of tokens) {
    if (tok.type === "br") {
      spans.push(span(bufStartLine, side, cur));
      line += 1;
      bufStartLine = line;
      cur = "";
      continue;
    }
    cur += renderRaw(tok.raw.replace(/\n/g, " "));
    line += countNewlines(tok.raw);
  }
  spans.push(span(bufStartLine, side, cur));
  return spans.join(" ");
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

// anchorOverlapsBlock reports whether an inclusive anchor range
// [anchorStart, anchorEnd] overlaps a block whose source-line range
// is stored as half-open [blockStart, blockEnd).
//
// The half-open convention matches blockRangeByIdx in
// RenderedMarkdownView — blockEnd is one past the last source line
// the block contains, so blocks abut without overlapping at their
// boundaries. Without this distinction, an anchor on a boundary
// line would match both the preceding and the succeeding block and
// the same thread would render twice.
export function anchorOverlapsBlock(
  blockStart: number,
  blockEnd: number,
  anchorStart: number,
  anchorEnd: number,
): boolean {
  return anchorStart < blockEnd && anchorEnd >= blockStart;
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
