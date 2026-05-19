<script lang="ts">
  import { Marked, type Token, type Tokens } from "marked";
  import DOMPurify from "dompurify";

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

  let raw = $state<string | null>(null);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let fetchSeq = 0;

  let bodyEl: HTMLDivElement | undefined = $state();

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

    // Custom renderer: only the heading renderer is customised so we
    // can inline the L<n> badge. Everything else flows through
    // marked's defaults, which produces standard markdown HTML.
    const m = new Marked({ breaks: true, gfm: true });

    // We need per-heading source-line numbers, so we precompute by
    // walking the lexer once, mapping (heading-token-index) → line.
    let tokens: Token[];
    try {
      tokens = m.lexer(raw);
    } catch {
      return { html: "", changedIndexes: new Set() };
    }

    const headingLineByIdx = new Map<number, number>();
    const changedIndexes = new Set<number>();
    // Top-level "renderable" tokens — excluding pure whitespace —
    // are what get mapped to the DOM children of the rendered body.
    // We track their cursor in `renderIdx`.
    let line = 1;
    let renderIdx = 0;
    let headingPos = 0;
    for (const t of tokens) {
      const rawText = (t as { raw?: string }).raw ?? "";
      const startLine = line;
      const newlines = countNewlines(rawText);
      const endLine = line + newlines;

      if (t.type === "heading") {
        // marked's heading renderer is called once per heading;
        // it processes them in document order so an index-into-
        // headings counter aligns with the renderer invocations.
        headingLineByIdx.set(headingPos, startLine);
        headingPos++;
      }
      if (t.type !== "space") {
        if (blockOverlapsChanged(startLine, endLine, changedLines)) {
          changedIndexes.add(renderIdx);
        }
        renderIdx++;
      }
      line += newlines;
    }

    let headingRender = 0;
    m.use({
      renderer: {
        heading(this: { parser: { parseInline(tokens: Tokens.Generic[]): string } }, token: Tokens.Heading): string {
          const text = this.parser.parseInline(token.tokens ?? []);
          const level = token.depth;
          const myLine = headingLineByIdx.get(headingRender) ?? 0;
          headingRender++;
          const label = myLine > 0
            ? `<span class="rmd-line" title="Line ${myLine}">L${myLine}</span>`
            : "";
          return `<h${level}>${text}${label}</h${level}>\n`;
        },
      },
    });

    let html: string;
    try {
      html = m.parser(tokens);
    } catch {
      html = "";
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
    return DOMPurify.sanitize(html, {
      ADD_ATTR: ["target", "rel", "title"],
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
</div>

<style>
  .rmd-view {
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
    margin-top: 8px;
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
</style>
