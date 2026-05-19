<script lang="ts">
  import { Marked, type Token, type Tokens } from "marked";
  import DOMPurify from "dompurify";
  import {
    wrapProseBlock,
    wrapCodeBlock,
    computeRangeFromSelection,
    type AnchorSide,
    type AnchorRange,
  } from "./renderedMarkdownAnchors";
  import DiffComposer from "./DiffComposer.svelte";
  import { getStores } from "../../context.js";

  // Renders a markdown file at a given SHA inside the diff surface,
  // annotated with sparse source-line markers.
  //
  // The whole file is rendered as one HTML blob so block-level
  // typography (h*/p/ul/blockquote/pre margin collapse, list nesting,
  // table layout) matches what any reader expects from a standard
  // markdown renderer. The annotations are layered on top:
  //   - Headings get an inline L<n> badge appended after the title
  //     text — small monospace, faint, right-aligned.
  //   - Top-level blocks whose source-line range overlaps a changed
  //     hunk get a left accent bar via a post-mount class.
  //
  // The "compute changed blocks by walking the lexer separately,
  // then locate them in the DOM by position" trick is what lets us
  // keep natural typography while still surfacing per-block change
  // signal — marked.parser([token]) per block would render the same
  // HTML but break margin collapse, which is what made the earlier
  // version's spacing read as off.

  export interface RenderedHunk {
    new_start: number;
    new_count: number;
  }

  interface Props {
    owner: string;
    name: string;
    number: number;
    path: string;
    sha: string;
    hunks: RenderedHunk[];
  }

  const { owner, name, number, path, sha, hunks }: Props = $props();

  const { diff: diffStore } = getStores();

  let raw = $state<string | null>(null);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let fetchSeq = 0;

  let bodyEl: HTMLDivElement | undefined = $state();

  let liveSelection = $state<AnchorRange | null>(null);
  let rangeSnapshot = $state<AnchorRange | null>(null);
  let toolbarTop = $state(0);
  let toolbarLeft = $state(0);
  let openComposerKey = $state<string | null>(null);

  function refreshSelection(): void {
    if (!bodyEl) return;
    const sel = typeof window !== "undefined" ? window.getSelection() : null;
    liveSelection = computeRangeFromSelection(bodyEl, sel);
  }

  $effect(() => {
    if (typeof document === "undefined") return;
    document.addEventListener("selectionchange", refreshSelection);
    return () => document.removeEventListener("selectionchange", refreshSelection);
  });

  function updateToolbarPosition(): void {
    if (typeof window === "undefined" || !liveSelection) return;
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return;
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return;
    toolbarTop = rect.bottom + window.scrollY + 4;
    toolbarLeft = rect.right + window.scrollX - 90;
  }

  $effect(() => {
    if (liveSelection) updateToolbarPosition();
  });

  function openComposerFromToolbar(): void {
    if (!liveSelection) return;
    rangeSnapshot = liveSelection;
    openComposerKey = `${liveSelection.endLine}:${liveSelection.side}`;
  }

  function closeComposer(): void {
    openComposerKey = null;
    rangeSnapshot = null;
  }

  function saveDraft(body: string): void {
    const range = rangeSnapshot;
    if (!range) return;
    diffStore.addDraftComment({
      path,
      line: range.endLine,
      side: range.side,
      ...(range.startLine !== range.endLine ? { startLine: range.startLine } : {}),
      commitSha: sha,
      body,
    });
    closeComposer();
  }

  // The rendered view always represents the new (right) side of the diff.
  const renderedSide: AnchorSide = "RIGHT";

  $effect(() => {
    void load(path, sha);
  });

  async function load(p: string, s: string): Promise<void> {
    if (!p || !s) return;
    const mySeq = ++fetchSeq;
    loading = true;
    error = null;
    raw = null;
    truncated = false;
    try {
      const url =
        `/api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}` +
        `/pulls/${number}/blob` +
        `?path=${encodeURIComponent(p)}&sha=${encodeURIComponent(s)}`;
      const res = await fetch(url);
      if (mySeq !== fetchSeq) return;
      if (!res.ok) {
        error = `Fetch blob: ${res.status} ${res.statusText}`;
        return;
      }
      const data = (await res.json()) as { content: string; truncated: boolean };
      if (mySeq !== fetchSeq) return;
      truncated = data.truncated;
      raw = data.content;
    } catch (e) {
      if (mySeq !== fetchSeq) return;
      error = e instanceof Error ? e.message : String(e);
    } finally {
      if (mySeq === fetchSeq) loading = false;
    }
  }

  // Build the set of changed source lines (new side) from the hunks.
  const changedLines = $derived.by<Set<number>>(() => {
    const s = new Set<number>();
    for (const h of hunks ?? []) {
      for (let i = 0; i < h.new_count; i++) {
        s.add(h.new_start + i);
      }
    }
    return s;
  });

  // Render the markdown to one HTML blob, plus walk the lexer
  // separately to compute which TOP-LEVEL block indexes correspond
  // to changed source lines. We use index-aligned lookup later
  // because marked emits its parser output in the same order as the
  // tokens it lexed.
  interface RenderedDoc {
    html: string;
    changedIndexes: Set<number>;
  }

  const doc = $derived.by<RenderedDoc>(() => {
    if (raw === null) return { html: "", changedIndexes: new Set() };

    const m = new Marked({ breaks: true, gfm: true });

    let tokens: Token[];
    try {
      tokens = m.lexer(raw);
    } catch {
      return { html: "", changedIndexes: new Set() };
    }

    // Precompute each token's start line by walking the lexer output once.
    const startLineByTokenIdx = new Map<number, number>();
    let cursorLine = 1;
    for (let i = 0; i < tokens.length; i++) {
      startLineByTokenIdx.set(i, cursorLine);
      const rawText = (tokens[i] as { raw?: string }).raw ?? "";
      cursorLine += countNewlines(rawText);
    }

    // Mutable cell consulted by renderer overrides to know which source
    // line the block being rendered started on.
    let currentBlockStart = 1;

    m.use({
      renderer: {
        paragraph({ tokens: _t, raw: rawText }: Tokens.Paragraph): string {
          return `<p>${wrapProseBlock(rawText, currentBlockStart, renderedSide, (s) =>
            m.parseInline(s) as string,
          )}</p>\n`;
        },
        heading({ tokens: _t, raw: rawText, depth }: Tokens.Heading): string {
          const inner = wrapProseBlock(
            rawText.replace(/^#+\s*/, ""),
            currentBlockStart,
            renderedSide,
            (s) => m.parseInline(s) as string,
          );
          const badge = `<span class="rmd-line" title="Line ${currentBlockStart}">L${currentBlockStart}</span>`;
          return `<h${depth}>${inner}${badge}</h${depth}>\n`;
        },
        code({ text, lang }: Tokens.Code): string {
          const langAttr = lang ? ` class="language-${lang}"` : "";
          return `<pre><code${langAttr}>${wrapCodeBlock(text, currentBlockStart, renderedSide)}</code></pre>\n`;
        },
        listitem(item: Tokens.ListItem): string {
          return `<li>${wrapProseBlock(
            item.raw.replace(/^[-*+]\s+|^\d+\.\s+/, ""),
            currentBlockStart,
            renderedSide,
            (s) => m.parseInline(s) as string,
          )}</li>\n`;
        },
      },
    });

    // Parse one block at a time so currentBlockStart is set before
    // each renderer override fires. Track a separate render index
    // (skipping space tokens) so changedIndexes aligns with DOM
    // child positions, matching the post-mount $effect walker.
    let html = "";
    const changedIndexes = new Set<number>();
    let renderIdx = 0;
    for (let i = 0; i < tokens.length; i++) {
      currentBlockStart = startLineByTokenIdx.get(i) ?? 1;
      const tok = tokens[i]!;
      const rawText = (tok as { raw?: string }).raw ?? "";
      const endLine = currentBlockStart + countNewlines(rawText);
      if (tok.type !== "space") {
        if (blockOverlapsChanged(currentBlockStart, endLine, changedLines)) {
          changedIndexes.add(renderIdx);
        }
        renderIdx++;
      }
      html += m.parser([tok]);
    }
    return { html, changedIndexes };
  });

  function countNewlines(s: string): number {
    let n = 0;
    for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) === 10) n++;
    return n;
  }

  function blockOverlapsChanged(start: number, end: number, set: Set<number>): boolean {
    for (let i = start; i < Math.max(start + 1, end); i++) {
      if (set.has(i)) return true;
    }
    return false;
  }

  function sanitize(html: string): string {
    // DOMPurify allows any class attribute by default; the heading
    // injector emits <span class="rmd-line">, which sails through.
    // data-anchor-* are required for computeRangeFromSelection to work
    // after the HTML is mounted.
    return DOMPurify.sanitize(html, {
      ADD_ATTR: ["target", "rel", "title", "data-anchor-line", "data-anchor-side"],
    });
  }

  // After the HTML mounts, walk the body's direct children and mark
  // the ones whose source-line range overlapped a changed hunk. The
  // index alignment relies on the fact that marked's parser emits
  // top-level tokens in source order, so the Nth direct child of
  // the body corresponds to the Nth non-space top-level token we
  // counted while lexing.
  $effect(() => {
    if (!bodyEl) return;
    // Reactive deps: rerun whenever the rendered html or the set
    // changes (e.g., scope switch, hunks update).
    const _ = doc;
    const children = bodyEl.children;
    for (let i = 0; i < children.length; i++) {
      const el = children[i] as HTMLElement;
      if (doc.changedIndexes.has(i)) {
        el.classList.add("rmd-changed");
      } else {
        el.classList.remove("rmd-changed");
      }
    }
  });
</script>

<div class="rmd-view">
  {#if loading && raw === null}
    <div class="rmd-state">Loading…</div>
  {:else if error}
    <div class="rmd-state rmd-state--error">{error}</div>
  {:else if truncated}
    <div class="rmd-state rmd-state--error">File too large to render inline.</div>
  {:else if raw !== null}
    <div class="rmd-body markdown-body" bind:this={bodyEl}>
      {@html sanitize(doc.html)}
    </div>
  {/if}

  {#if liveSelection}
    <div
      class="rmd-toolbar"
      style:top="{toolbarTop}px"
      style:left="{toolbarLeft}px"
    >
      <button type="button" class="rmd-tb-btn" onclick={openComposerFromToolbar}
        title="Comment on lines {liveSelection.startLine}–{liveSelection.endLine}">+</button>
    </div>
  {/if}

  {#if openComposerKey && rangeSnapshot}
    <div class="rmd-composer-wrap">
      <DiffComposer
        anchor={{ line: rangeSnapshot.endLine, side: rangeSnapshot.side, startLine: rangeSnapshot.startLine }}
        onsave={saveDraft}
        oncancel={closeComposer}
      />
    </div>
  {/if}
</div>

<style>
  .rmd-view {
    position: relative;
    padding: 16px 24px;
    background: var(--diff-bg);
  }

  .rmd-state {
    padding: 10px;
    color: var(--text-muted);
    font-size: 12px;
    font-style: italic;
  }
  .rmd-state--error {
    color: var(--accent-red);
  }

  /* GitHub-style markdown typography. The body is one continuous
     block — no per-block wrappers — so adjacent paragraphs' margins
     collapse naturally and the rhythm matches every other markdown
     renderer the reviewer is used to. */
  .rmd-body {
    font-size: 14px;
    line-height: 1.5;
    color: var(--text-primary);
    max-width: 80ch;
  }

  /* First-child top margin is removed so the rendered content
     starts flush with the top of the view rather than gaining the
     first heading/paragraph's full margin-top of empty space. */
  .rmd-body :global(> :first-child) {
    margin-top: 0;
  }

  .rmd-body :global(h1) {
    margin: 24px 0 16px;
    padding-bottom: 0.3em;
    font-size: 1.75em;
    font-weight: 600;
    line-height: 1.25;
    border-bottom: 1px solid var(--border-muted);
  }
  .rmd-body :global(h2) {
    margin: 24px 0 16px;
    padding-bottom: 0.3em;
    font-size: 1.4em;
    font-weight: 600;
    line-height: 1.25;
    border-bottom: 1px solid var(--border-muted);
  }
  .rmd-body :global(h3) {
    margin: 24px 0 16px;
    font-size: 1.2em;
    font-weight: 600;
    line-height: 1.25;
  }
  .rmd-body :global(h4) {
    margin: 24px 0 16px;
    font-size: 1em;
    font-weight: 600;
    line-height: 1.25;
  }
  .rmd-body :global(h5) {
    margin: 24px 0 16px;
    font-size: 0.9em;
    font-weight: 600;
    line-height: 1.25;
  }
  .rmd-body :global(h6) {
    margin: 24px 0 16px;
    font-size: 0.85em;
    font-weight: 600;
    line-height: 1.25;
    color: var(--text-muted);
  }

  .rmd-body :global(p) {
    margin: 0 0 16px;
  }
  .rmd-body :global(ul),
  .rmd-body :global(ol) {
    margin: 0 0 16px;
    padding-left: 2em;
  }
  .rmd-body :global(li + li) {
    margin-top: 4px;
  }
  .rmd-body :global(li > ul),
  .rmd-body :global(li > ol) {
    margin: 4px 0 0;
  }

  .rmd-body :global(blockquote) {
    margin: 0 0 16px;
    padding: 0 1em;
    color: var(--text-muted);
    border-left: 0.25em solid var(--border-muted);
  }

  .rmd-body :global(pre) {
    margin: 0 0 16px;
    padding: 12px 14px;
    background: var(--bg-inset);
    border-radius: var(--radius-md);
    line-height: 1.45;
    overflow-x: auto;
  }
  .rmd-body :global(code) {
    font-family: var(--font-mono);
    font-size: 0.85em;
    background: var(--bg-inset);
    padding: 0.15em 0.4em;
    border-radius: var(--radius-sm);
  }
  .rmd-body :global(pre code) {
    background: transparent;
    padding: 0;
    border-radius: 0;
    font-size: inherit;
  }

  .rmd-body :global(table) {
    margin: 0 0 16px;
    border-collapse: collapse;
  }
  .rmd-body :global(th),
  .rmd-body :global(td) {
    padding: 6px 12px;
    border: 1px solid var(--border-muted);
  }
  .rmd-body :global(th) {
    background: var(--bg-inset);
    font-weight: 600;
  }

  .rmd-body :global(hr) {
    margin: 24px 0;
    border: 0;
    border-top: 1px solid var(--border-muted);
  }

  .rmd-body :global(a) {
    color: var(--accent-blue);
    text-decoration: none;
  }
  .rmd-body :global(a:hover) {
    text-decoration: underline;
  }

  /* L<n> badge appended inside each heading. Small, faint,
     right-aligned so it doesn't compete with the heading text but
     is locatable when the reviewer wants to jump back to the diff. */
  .rmd-body :global(.rmd-line) {
    margin-left: 12px;
    font-family: var(--font-mono);
    font-size: 0.55em;
    font-weight: 500;
    color: var(--text-muted);
    vertical-align: middle;
    letter-spacing: 0.04em;
    user-select: none;
  }

  /* Changed-block accent — applied to direct children of .rmd-body
     by the post-mount $effect. Uses :global() because the class is
     added imperatively after Svelte's render, so it isn't visible
     to the scoping pass. A calm green bar on the left edge plus a
     faint tint, matching the diff's add-line color family. */
  .rmd-body :global(.rmd-changed) {
    border-left: 3px solid color-mix(in srgb, var(--diff-add-text) 60%, transparent);
    padding-left: 10px;
    margin-left: -13px;            /* re-flow back to original x so prose alignment stays consistent */
    background: color-mix(in srgb, var(--diff-add-bg) 30%, transparent);
  }
  /* When the changed block is itself a heading, the heading's own
     bottom border (for h1/h2) overlaps awkwardly with the accent.
     Trim a touch of room so they read as separate signals. */
  .rmd-body :global(h1.rmd-changed),
  .rmd-body :global(h2.rmd-changed) {
    padding-bottom: 0.4em;
  }
  /* The base .rmd-changed rule's padding-left:10px would override
     the list's own 2em (where outside markers hang), crushing
     bullets up against the accent bar. Layer the accent's gutter on
     top of the list's existing padding instead so markers keep
     their column. The base margin-left:-13px is already correct
     because lists otherwise have margin-left:0. */
  .rmd-body :global(ul.rmd-changed),
  .rmd-body :global(ol.rmd-changed) {
    padding-left: calc(2em + 10px);
  }

  .rmd-toolbar {
    position: absolute;
    display: flex;
    gap: 4px;
    padding: 4px;
    background: var(--bg-elevated);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-md);
    box-shadow: 0 2px 8px rgba(0,0,0,0.15);
    z-index: 5;
  }
  .rmd-tb-btn {
    width: 24px;
    height: 24px;
    background: transparent;
    border: 0;
    cursor: pointer;
    color: var(--text-primary);
  }
  .rmd-composer-wrap {
    position: relative;
    margin-top: 12px;
  }
</style>
