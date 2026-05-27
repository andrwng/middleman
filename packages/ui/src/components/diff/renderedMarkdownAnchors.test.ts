import { describe, it, expect } from "vitest";
import {
  wrapProseBlock,
  wrapCodeBlock,
  computeRangeFromSelection,
  anchorOverlapsBlock,
} from "./renderedMarkdownAnchors";

describe("anchorOverlapsBlock", () => {
  // blockRangeByIdx in RenderedMarkdownView stores half-open ranges
  // [blockStart, blockEnd) so a line that's the boundary between two
  // adjacent blocks belongs to exactly one. The anchor is inclusive
  // on both ends; for single-line anchors anchorStart === anchorEnd.

  it("single-line anchor inside a block matches", () => {
    expect(anchorOverlapsBlock(5, 8, 6, 6)).toBe(true);
  });

  it("single-line anchor at the block's start matches", () => {
    expect(anchorOverlapsBlock(5, 8, 5, 5)).toBe(true);
  });

  it("single-line anchor at the block's exclusive end does NOT match", () => {
    // Regression: this case used to match both the preceding block
    // (whose exclusive end == this line) AND the next block (whose
    // start == this line), which is what caused threads to render
    // twice in the rendered markdown view.
    expect(anchorOverlapsBlock(5, 8, 8, 8)).toBe(false);
  });

  it("single-line anchor before the block does not match", () => {
    expect(anchorOverlapsBlock(5, 8, 4, 4)).toBe(false);
  });

  it("single-line anchor after the block does not match", () => {
    expect(anchorOverlapsBlock(5, 8, 9, 9)).toBe(false);
  });

  it("multi-line anchor that overlaps the block matches", () => {
    expect(anchorOverlapsBlock(5, 8, 3, 6)).toBe(true);
  });
});

describe("wrapProseBlock", () => {
  it("wraps each source line in an anchor span using the provided inline parser", () => {
    const inline = (s: string): string => `<em>${s}</em>`;
    const out = wrapProseBlock("foo\nbar baz", 10, "RIGHT", inline);
    expect(out).toBe(
      `<span class="rmd-anchor" data-anchor-line="10" data-anchor-side="RIGHT"><em>foo</em></span>` +
      ` ` +
      `<span class="rmd-anchor" data-anchor-line="11" data-anchor-side="RIGHT"><em>bar baz</em></span>`,
    );
  });

  it("uses LEFT side when requested (for deleted files)", () => {
    const out = wrapProseBlock("x", 5, "LEFT", (s) => s);
    expect(out).toContain(`data-anchor-side="LEFT"`);
    expect(out).toContain(`data-anchor-line="5"`);
  });
});

describe("wrapCodeBlock", () => {
  it("preserves newlines as the join character and HTML-escapes each line", () => {
    const out = wrapCodeBlock("a < b\nc > d", 20, "RIGHT");
    expect(out).toBe(
      `<span class="rmd-anchor" data-anchor-line="20" data-anchor-side="RIGHT">a &lt; b</span>` +
      `\n` +
      `<span class="rmd-anchor" data-anchor-line="21" data-anchor-side="RIGHT">c &gt; d</span>`,
    );
  });

  it("returns an empty string for empty code", () => {
    expect(wrapCodeBlock("", 1, "RIGHT")).toBe("");
  });
});

describe("computeRangeFromSelection", () => {
  function mkBody(html: string): HTMLElement {
    const el = document.createElement("div");
    el.innerHTML = html;
    document.body.appendChild(el);
    return el;
  }

  function selectAcross(startNode: Node, endNode: Node): Selection {
    const range = document.createRange();
    range.setStart(startNode, 0);
    range.setEnd(endNode, endNode.textContent?.length ?? 0);
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
    return sel;
  }

  it("returns null when selection is outside the root", () => {
    const root = mkBody(`<span class="rmd-anchor" data-anchor-line="1" data-anchor-side="RIGHT">x</span>`);
    const outside = document.createElement("p");
    outside.textContent = "out";
    document.body.appendChild(outside);
    const sel = selectAcross(outside.firstChild!, outside.firstChild!);
    expect(computeRangeFromSelection(root, sel)).toBeNull();
  });

  it("resolves a single-span selection to a 1-line range", () => {
    const root = mkBody(
      `<span class="rmd-anchor" data-anchor-line="5" data-anchor-side="RIGHT">hello</span>`,
    );
    const span = root.firstChild as HTMLElement;
    const sel = selectAcross(span.firstChild!, span.firstChild!);
    expect(computeRangeFromSelection(root, sel)).toEqual({
      startLine: 5,
      endLine: 5,
      side: "RIGHT",
    });
  });

  it("resolves a selection across two spans to a 2-line range", () => {
    const root = mkBody(
      `<span class="rmd-anchor" data-anchor-line="5" data-anchor-side="RIGHT">a</span>` +
      ` ` +
      `<span class="rmd-anchor" data-anchor-line="6" data-anchor-side="RIGHT">b</span>`,
    );
    const first = root.querySelector('[data-anchor-line="5"]')!.firstChild!;
    const second = root.querySelector('[data-anchor-line="6"]')!.firstChild!;
    const sel = selectAcross(first, second);
    expect(computeRangeFromSelection(root, sel)).toEqual({
      startLine: 5,
      endLine: 6,
      side: "RIGHT",
    });
  });

  it("returns null when the two ends are on different sides", () => {
    const root = mkBody(
      `<span class="rmd-anchor" data-anchor-line="5" data-anchor-side="LEFT">a</span>` +
      `<span class="rmd-anchor" data-anchor-line="6" data-anchor-side="RIGHT">b</span>`,
    );
    const left = root.querySelector('[data-anchor-side="LEFT"]')!.firstChild!;
    const right = root.querySelector('[data-anchor-side="RIGHT"]')!.firstChild!;
    const sel = selectAcross(left, right);
    expect(computeRangeFromSelection(root, sel)).toBeNull();
  });
});
