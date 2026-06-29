<script lang="ts">
  import { Marked, type Token, type Tokens } from "marked";
  import DOMPurify from "dompurify";
  import {
    wrapProseBlock,
    wrapCodeBlock,
    anchorOverlapsBlock,
    type AnchorSide,
    type AnchorRange,
  } from "./renderedMarkdownAnchors";
  import { mount, unmount } from "svelte";
  import DiffComposer from "./DiffComposer.svelte";
  import AIAskComposer from "./AIAskComposer.svelte";
  import ReviewCommentCard from "./ReviewCommentCard.svelte";
  import PendingCommentCard from "./PendingCommentCard.svelte";
  import AIThreadCard from "./AIThreadCard.svelte";
  import CommentGutter, { type GutterEntry, type CardSpec as GutterCardSpec } from "./CommentGutter.svelte";
  import { clampGutterWidth } from "./gutterStack";
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
  //   - Top-level blocks whose source-line range overlapped a changed
  //     hunk get a left accent bar via a post-mount class.
  //
  // The "compute changed blocks by walking the lexer separately,
  // then locate them in the DOM by position" trick is what lets us
  // keep natural typography while still surfacing per-block change
  // signal — marked.parser([token]) per block would render the same
  // HTML but break margin collapse, which is what made the earlier
  // version's spacing read as off.
  //
  // commentLayout controls how comment cards are rendered:
  //   "inline" (default): cards are injected as .rmd-thread-wrap elements
  //                       directly after each block in the body.
  //   "gutter": cards are rendered in a right-side CommentGutter column.
  //             Below max-width:720px the view falls back to inline mode.

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
    commentLayout?: "inline" | "gutter";
  }

  const { owner, name, number, path, sha, hunks, commentLayout = "inline" }: Props = $props();

  const { diff: diffStore, ai: aiStore, detail: detailStore } = getStores();

  let raw = $state<string | null>(null);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let fetchSeq = 0;

  let bodyEl: HTMLDivElement | undefined = $state();
  // Root element — used for bounding-rect offset math in gutter mode.
  let viewEl: HTMLDivElement | undefined = $state();

  // The rendered view always represents the new (right) side of the diff.
  const renderedSide: AnchorSide = "RIGHT";

  let rangeSnapshot = $state<AnchorRange | null>(null);
  let openComposerKey = $state<string | null>(null);
  // Tracks which block index (into bodyEl.children) is the active target for
  // the composer so we can position the overlay next to it.
  let activeBlockIdx = $state<number | null>(null);
  // CSS top offset (px) for the positioned composer overlay, relative to
  // .rmd-view. Updated by the positioning effect below.
  let composerTop = $state<number | null>(null);

  // Narrow-mode flag: true when the view is too narrow to show a right gutter
  // (below ~720px). In narrow mode, gutter layout falls back to inline.
  let narrowMode = $state(false);

  // GutterEntry[] for gutter mode — rebuilt whenever cards or the doc changes.
  let gutterEntries = $state<GutterEntry[]>([]);

  // Horizontally-resizable gutter width. Persisted across reloads; the divider
  // (drag handle) drives this and the .rmd-view --rmd-gutter-width CSS var.
  const GUTTER_WIDTH_KEY = "rmd-gutter-width";
  const DEFAULT_GUTTER_WIDTH = 280;
  function loadGutterWidth(): number {
    try {
      const v = Number(localStorage.getItem(GUTTER_WIDTH_KEY));
      if (Number.isFinite(v) && v > 0) return v;
    } catch {
      /* localStorage unavailable — fall through to default */
    }
    return DEFAULT_GUTTER_WIDTH;
  }
  let gutterWidth = $state(loadGutterWidth());
  let resizing = $state(false);
  let resizeStartX = 0;
  let resizeStartWidth = 0;

  function startResize(e: PointerEvent) {
    resizing = true;
    resizeStartX = e.clientX;
    resizeStartWidth = gutterWidth;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    e.preventDefault();
  }
  function moveResize(e: PointerEvent) {
    if (!resizing) return;
    // Dragging the divider left (toward the body) widens the gutter.
    const delta = resizeStartX - e.clientX;
    gutterWidth = clampGutterWidth(resizeStartWidth + delta, viewEl?.clientWidth ?? 0);
  }
  function endResize(e: PointerEvent) {
    if (!resizing) return;
    resizing = false;
    try {
      localStorage.setItem(GUTTER_WIDTH_KEY, String(Math.round(gutterWidth)));
    } catch {
      /* localStorage unavailable — width still applies for this session */
    }
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
  }

  function findBlockIdx(start: number, end: number): number | null {
    for (const [idx, range] of doc.blockRangeByIdx) {
      if (range[0] === start && range[1] === end) return idx;
    }
    return null;
  }

  // Computes the CSS top offset (px) for the composer overlay / gutter entry,
  // relative to .rmd-view. Uses a bounding-rect delta so the offset is correct
  // regardless of intermediate positioning contexts — avoids the double-count
  // bug that arose from adding bodyEl.offsetTop + target.offsetTop when
  // .rmd-body is statically positioned (target.offsetTop is already relative
  // to .rmd-view in that case).
  function computeBlockBottom(idx: number | null): number | null {
    if (idx === null || !bodyEl || !viewEl) return null;
    const allChildren = Array.from(bodyEl.children) as HTMLElement[];
    let original = 0;
    let target: HTMLElement | null = null;
    for (const child of allChildren) {
      if (child.classList.contains("rmd-thread-wrap")) continue;
      if (original === idx) {
        target = child;
        break;
      }
      original++;
    }
    if (!target) return null;
    // Bounding-rect delta relative to .rmd-view, accounting for scroll.
    const targetRect = target.getBoundingClientRect();
    const viewRect = viewEl.getBoundingClientRect();
    return targetRect.top - viewRect.top + viewEl.scrollTop + target.offsetHeight;
  }

  function openComposerForBlock(start: number, end: number): void {
    rangeSnapshot = { startLine: start, endLine: end, side: renderedSide };
    openComposerKey = `${end}:${renderedSide}`;
    activeBlockIdx = findBlockIdx(start, end);
    // Compute composerTop synchronously so the composer renders already
    // positioned on its first frame, avoiding a flash to the document bottom
    // followed by DiffComposer's scrollIntoView jumping the page.
    composerTop = computeBlockBottom(activeBlockIdx);
  }

  function closeComposer(): void {
    openComposerKey = null;
    rangeSnapshot = null;
    activeBlockIdx = null;
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

  let openAskKey = $state<string | null>(null);
  let askError = $state<string | null>(null);
  let askSubmitting = $state(false);

  function openAskForBlock(start: number, end: number): void {
    rangeSnapshot = { startLine: start, endLine: end, side: renderedSide };
    openAskKey = `${end}:${renderedSide}`;
    askError = null;
    activeBlockIdx = findBlockIdx(start, end);
    // Same synchronous positioning as openComposerForBlock.
    composerTop = computeBlockBottom(activeBlockIdx);
  }

  function closeAsk(): void {
    openAskKey = null;
    rangeSnapshot = null;
    askError = null;
    askSubmitting = false;
    activeBlockIdx = null;
  }

  async function submitAsk(question: string): Promise<void> {
    const range = rangeSnapshot;
    if (!range || askSubmitting) return;
    askSubmitting = true;
    askError = null;
    try {
      const body: Parameters<typeof aiStore.createThread>[0] = {
        path,
        anchor_side: range.side,
        anchor_line: range.endLine,
        commit_sha: sha,
        question,
      };
      if (range.startLine !== range.endLine) {
        body.hunk_start_line = range.startLine;
        body.hunk_end_line = range.endLine;
      }
      const result = await aiStore.createThread(body);
      if (result.ok) {
        closeAsk();
      } else {
        askError = result.error;
      }
    } finally {
      askSubmitting = false;
    }
  }

  const drafts = $derived(diffStore.getDraftCommentsForPath(path));
  const publishedForFile = $derived(
    detailStore.getReviewCommentsByFilePath().get(path) ?? [],
  );
  const aiThreadsForFile = $derived(aiStore.getThreadsForFile(path));

  const outdatedCount = $derived(
    publishedForFile.filter((c: { line: number }) => c.line <= 0).length,
  );

  type CardSpec =
    | { kind: "draft"; key: string; comment: (typeof drafts)[number] }
    | { kind: "published"; key: string; comment: (typeof publishedForFile)[number] }
    | { kind: "ai"; key: string; thread: (typeof aiThreadsForFile)[number] };

  // start..end is the block's half-open source-line range
  // [start, end) — matches blockRangeByIdx and blockOverlapsChanged.
  // Using inclusive end here would double-count anchors that fall on
  // the boundary between two adjacent blocks; that's the
  // anchorOverlapsBlock helper's job.
  function cardsForRange(start: number, end: number): CardSpec[] {
    const out: CardSpec[] = [];
    for (const c of drafts) {
      const cStart = c.startLine ?? c.line;
      if (c.side === renderedSide && anchorOverlapsBlock(start, end, cStart, c.line)) {
        out.push({ kind: "draft", key: `d:${c.id ?? `${c.line}:${c.side}`}`, comment: c });
      }
    }
    for (const c of publishedForFile) {
      if (c.line <= 0) continue;
      const cStart = (c as { startLine?: number }).startLine ?? c.line;
      if (c.side === renderedSide && anchorOverlapsBlock(start, end, cStart, c.line)) {
        out.push({ kind: "published", key: `p:${c.id}`, comment: c });
      }
    }
    for (const t of aiThreadsForFile) {
      const tStart = t.hunk_start_line ?? t.anchor_line;
      const tEnd = t.hunk_end_line ?? t.anchor_line;
      if (t.anchor_side === renderedSide && anchorOverlapsBlock(start, end, tStart, tEnd)) {
        out.push({ kind: "ai", key: `a:${t.id}`, thread: t });
      }
    }
    return out;
  }

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
    blockRangeByIdx: Map<number, [number, number]>;
  }

  const doc = $derived.by<RenderedDoc>(() => {
    if (raw === null) return { html: "", changedIndexes: new Set(), blockRangeByIdx: new Map() };

    const m = new Marked({ breaks: true, gfm: true });

    let tokens: Token[];
    try {
      tokens = m.lexer(raw);
    } catch {
      return { html: "", changedIndexes: new Set(), blockRangeByIdx: new Map() };
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
          // Walk item.tokens rather than re-parsing item.raw as inline.
          // The old approach swallowed every block child — nested lists,
          // fenced code, blockquotes — into a single inline string, so
          // `- top\n  - nested` rendered as inline text with a literal
          // dash. Recursing through m.parser for non-text/paragraph
          // tokens lets marked re-enter our overrides and emit a real
          // nested <ul>/<ol>.
          //
          // Anchor lines on items within a list still share the list's
          // start line (currentBlockStart is set once per top-level
          // token). That's a known imprecision, separate from nested
          // rendering — fix it when per-item anchoring matters.
          const tokens = (item.tokens ?? []) as Token[];
          let out = "";
          for (const tok of tokens) {
            if (tok.type === "text") {
              const t = tok as Tokens.Text;
              out += wrapProseBlock(
                t.raw.replace(/\n$/, ""),
                currentBlockStart,
                renderedSide,
                (s) => m.parseInline(s) as string,
              );
            } else if (tok.type === "paragraph") {
              const p = tok as Tokens.Paragraph;
              out += `<p>${wrapProseBlock(
                p.raw.replace(/\n$/, ""),
                currentBlockStart,
                renderedSide,
                (s) => m.parseInline(s) as string,
              )}</p>`;
            } else {
              out += m.parser([tok]);
            }
          }
          return `<li>${out}</li>\n`;
        },
      },
    });

    // Parse one block at a time so currentBlockStart is set before
    // each renderer override fires. Track a separate render index
    // (skipping space tokens) so changedIndexes aligns with DOM
    // child positions, matching the post-mount $effect walker.
    let html = "";
    const changedIndexes = new Set<number>();
    const blockRangeByIdx = new Map<number, [number, number]>();
    let renderIdx = 0;
    for (let i = 0; i < tokens.length; i++) {
      currentBlockStart = startLineByTokenIdx.get(i) ?? 1;
      const tok = tokens[i]!;
      const rawText = (tok as { raw?: string }).raw ?? "";
      // Trailing blank lines belong to the separator, not the block. Excluding
      // them keeps a block's [start, end) range from abutting the next block,
      // which previously caused a boundary comment to match two blocks.
      const contentRaw = rawText.replace(/\n+$/, "");
      const endLine = currentBlockStart + countNewlines(contentRaw) + 1;
      if (tok.type !== "space") {
        if (blockOverlapsChanged(currentBlockStart, endLine, changedLines)) {
          changedIndexes.add(renderIdx);
        }
        blockRangeByIdx.set(renderIdx, [currentBlockStart, endLine]);
        renderIdx++;
      }
      html += m.parser([tok]);
    }
    return { html, changedIndexes, blockRangeByIdx };
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
  // the ones whose source-line range overlapped a changed hunk. Also
  // mount inline thread cards (drafts, published comments, AI threads)
  // anchored to each block's source-line range.
  //
  // The index alignment relies on the fact that marked's parser emits
  // top-level tokens in source order, so the Nth direct child of
  // the body corresponds to the Nth non-space top-level token we
  // counted while lexing.
  //
  // In gutter mode (commentLayout === "gutter" and not narrowMode), we skip
  // inline .rmd-thread-wrap injection and instead build GutterEntry[] that
  // is rendered by <CommentGutter> in the right column. Blocks with cards
  // receive .rmd-block--commented for a gutter-edge accent.
  const mountedInstances = new Set<ReturnType<typeof mount>>();

  $effect(() => {
    if (!bodyEl) return;
    // Touch all reactive deps so the effect re-runs when any changes.
    const _ = doc;
    const __ = drafts;
    const ___ = publishedForFile;
    const ____ = aiThreadsForFile;
    const _____ = commentLayout;
    const ______ = narrowMode;

    for (const inst of mountedInstances) unmount(inst);
    mountedInstances.clear();
    bodyEl.querySelectorAll(".rmd-thread-wrap").forEach((el) => el.remove());
    bodyEl.querySelectorAll(".rmd-line-actions").forEach((el) => el.remove());

    // Determine effective layout for this render pass.
    const useGutter = commentLayout === "gutter" && !narrowMode;

    const newGutterEntries: GutterEntry[] = [];

    const children = Array.from(bodyEl.children) as HTMLElement[];
    for (let i = 0; i < children.length; i++) {
      const el = children[i]!;
      if (doc.changedIndexes.has(i)) {
        el.classList.add("rmd-changed");
      } else {
        el.classList.remove("rmd-changed");
      }

      const range = doc.blockRangeByIdx.get(i);
      if (!range) continue;

      // Per-block hover affordance — matches the diff view's
      // .line-actions pattern (blue +, brown ?, opacity 0 until
      // hover). Each block hosts its own absolutely-positioned
      // button group on its right edge.
      el.classList.add("rmd-block");
      const actions = document.createElement("div");
      actions.className = "rmd-line-actions";
      const blockStart = range[0];
      const blockEnd = range[1];
      const commentBtn = document.createElement("button");
      commentBtn.type = "button";
      commentBtn.className = "rmd-add-comment-btn";
      commentBtn.title = blockStart === blockEnd
        ? `Comment on line ${blockStart}`
        : `Comment on lines ${blockStart}–${blockEnd}`;
      commentBtn.innerHTML =
        '<svg width="10" height="10" viewBox="0 0 10 10" fill="none" ' +
        'stroke="currentColor" stroke-width="2">' +
        '<path d="M5 2V8M2 5H8" stroke-linecap="round" /></svg>';
      commentBtn.addEventListener("click", () => openComposerForBlock(blockStart, blockEnd));
      const askBtn = document.createElement("button");
      askBtn.type = "button";
      askBtn.className = "rmd-ask-ai-btn";
      askBtn.title = blockStart === blockEnd
        ? `Ask Claude about line ${blockStart}`
        : `Ask Claude about lines ${blockStart}–${blockEnd}`;
      askBtn.textContent = "?";
      askBtn.addEventListener("click", () => openAskForBlock(blockStart, blockEnd));
      actions.appendChild(commentBtn);
      actions.appendChild(askBtn);
      el.appendChild(actions);

      const cards = cardsForRange(blockStart, blockEnd);

      if (useGutter) {
        // Gutter mode: add marker class to blocks with cards, build GutterEntry.
        if (cards.length > 0) {
          el.classList.add("rmd-block--commented");
          const top = computeBlockBottom(i) ?? 0;
          newGutterEntries.push({
            kind: "cards",
            key: `block:${blockStart}:${blockEnd}`,
            desiredTop: top,
            cards: cards as unknown as GutterCardSpec[],
          });
        } else {
          el.classList.remove("rmd-block--commented");
        }
      } else {
        // Inline mode: inject .rmd-thread-wrap after the block.
        el.classList.remove("rmd-block--commented");
        if (cards.length === 0) continue;

        const wrap = document.createElement("div");
        wrap.className = "rmd-thread-wrap";
        for (const spec of cards) {
          const host = document.createElement("div");
          host.className = "rmd-thread-host";
          wrap.appendChild(host);
          if (spec.kind === "ai") {
            const inst = mount(AIThreadCard, {
              target: host,
              props: { thread: spec.thread, repoOwner: owner, repoName: name },
            });
            mountedInstances.add(inst);
          } else if (spec.kind === "published") {
            const inst = mount(ReviewCommentCard, {
              target: host,
              props: {
                comment: spec.comment,
                repoOwner: owner,
                repoName: name,
                currentHeadSha: sha,
              },
            });
            mountedInstances.add(inst);
          } else {
            const inst = mount(PendingCommentCard, {
              target: host,
              props: {
                comment: spec.comment,
                currentHeadSha: sha,
                ondelete: () => diffStore.removeDraftComment(spec.comment.id),
              },
            });
            mountedInstances.add(inst);
          }
        }
        el.after(wrap);
      }
    }

    // In gutter mode, add the open composer as a gutter entry at the
    // active block's position, mirroring the inline overlay.
    if (useGutter) {
      if (openComposerKey && rangeSnapshot) {
        const top = computeBlockBottom(activeBlockIdx) ?? 0;
        newGutterEntries.push({
          kind: "composer-diff",
          key: `composer:${openComposerKey}`,
          desiredTop: top,
          anchor: {
            line: rangeSnapshot.endLine,
            side: rangeSnapshot.side,
            startLine: rangeSnapshot.startLine,
          },
          onsave: saveDraft,
          oncancel: closeComposer,
        });
      } else if (openAskKey && rangeSnapshot) {
        const top = computeBlockBottom(activeBlockIdx) ?? 0;
        newGutterEntries.push({
          kind: "composer-ask",
          key: `ask:${openAskKey}`,
          desiredTop: top,
          anchor: {
            line: rangeSnapshot.endLine,
            side: rangeSnapshot.side,
            startLine: rangeSnapshot.startLine,
          },
          error: askError,
          submitting: askSubmitting,
          onsubmit: (q: string) => void submitAsk(q),
          oncancel: closeAsk,
        });
      }
      gutterEntries = newGutterEntries;
    } else {
      gutterEntries = [];
    }

    // Cleanup on component teardown so the imperatively-mounted
    // cards don't keep their reactive effects alive after the
    // rendered view unmounts.
    return () => {
      for (const inst of mountedInstances) unmount(inst);
      mountedInstances.clear();
    };
  });

  // Re-runs whenever activeBlockIdx changes or the DOM layout changes
  // (e.g., after the card-injection $effect inserts .rmd-thread-wrap
  // nodes that shift block offsets). The open-composer functions above
  // already call computeBlockBottom synchronously on open, so this
  // effect is a correctness fallback — it keeps composerTop accurate
  // after any post-open DOM mutations rather than the primary
  // positioning source.
  $effect(() => {
    const idx = activeBlockIdx;
    composerTop = computeBlockBottom(idx);
  });

  // Narrow-mode detection via ResizeObserver. Below 720px the view is
  // too narrow to show a right gutter column, so we fall back to inline.
  // Guard for environments (jsdom) where ResizeObserver is unavailable.
  $effect(() => {
    if (!viewEl || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        narrowMode = entry.contentRect.width < 720;
      }
    });
    ro.observe(viewEl);
    return () => ro.disconnect();
  });
</script>

<div
  class="rmd-view"
  class:rmd-view--gutter={commentLayout === "gutter" && !narrowMode}
  style="--rmd-gutter-width: {gutterWidth}px"
  bind:this={viewEl}
>
  {#if loading && raw === null}
    <div class="rmd-state">Loading…</div>
  {:else if error}
    <div class="rmd-state rmd-state--error">{error}</div>
  {:else if truncated}
    <div class="rmd-state rmd-state--error">File too large to render inline.</div>
  {:else if raw !== null}
    {#if outdatedCount > 0}
      <div class="outdated-banner" title="These comments don't resolve in the current rendered view.">
        {outdatedCount} outdated review comment{outdatedCount === 1 ? "" : "s"} on this file
      </div>
    {/if}
    <div class="rmd-body markdown-body" bind:this={bodyEl}>
      {@html sanitize(doc.html)}
    </div>
  {/if}

  {#if commentLayout === "inline" || narrowMode}
    {#if openComposerKey && rangeSnapshot}
      <div
        class="rmd-composer-wrap"
        class:rmd-composer-wrap--positioned={composerTop !== null}
        style={composerTop !== null ? `top: ${composerTop}px` : undefined}
        data-rmd-block-idx={activeBlockIdx !== null ? String(activeBlockIdx) : undefined}
      >
        <DiffComposer
          anchor={{ line: rangeSnapshot.endLine, side: rangeSnapshot.side, startLine: rangeSnapshot.startLine }}
          onsave={saveDraft}
          oncancel={closeComposer}
        />
      </div>
    {/if}

    {#if openAskKey && rangeSnapshot}
      <div
        class="rmd-composer-wrap"
        class:rmd-composer-wrap--positioned={composerTop !== null}
        style={composerTop !== null ? `top: ${composerTop}px` : undefined}
        data-rmd-block-idx={activeBlockIdx !== null ? String(activeBlockIdx) : undefined}
      >
        <AIAskComposer
          anchor={{ line: rangeSnapshot.endLine, side: rangeSnapshot.side, startLine: rangeSnapshot.startLine }}
          error={askError}
          submitting={askSubmitting}
          onsubmit={(q) => void submitAsk(q)}
          oncancel={closeAsk}
        />
      </div>
    {/if}
  {/if}

  {#if commentLayout === "gutter" && !narrowMode}
    <div class="rmd-gutter-col">
      <div
        class="rmd-gutter-resize"
        class:rmd-gutter-resize--active={resizing}
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize comment gutter"
        title="Drag to resize the comment gutter"
        onpointerdown={startResize}
        onpointermove={moveResize}
        onpointerup={endResize}
      ></div>
      <CommentGutter
        entries={gutterEntries}
        repoOwner={owner}
        repoName={name}
        currentHeadSha={sha}
        ondelete={(id) => diffStore.removeDraftComment(id)}
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

  /* Gutter layout: content + right gutter side by side. */
  .rmd-view--gutter {
    display: grid;
    grid-template-columns: 1fr var(--rmd-gutter-width, 280px);
    grid-template-rows: auto;
    column-gap: 16px;
    align-items: start;
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

  /* Gutter-mode block marker: a faint left bar + comment glyph hint.
     Applied imperatively in gutter mode when a block has cards. */
  .rmd-body :global(.rmd-block--commented) {
    border-left: 3px solid color-mix(in srgb, var(--accent-blue) 50%, transparent);
    padding-left: 10px;
    margin-left: -13px;
  }

  .outdated-banner {
    padding: 6px 14px;
    font-size: 11px;
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 8%, var(--bg-inset));
    border-bottom: 1px solid color-mix(in srgb, var(--accent-amber) 30%, var(--diff-border));
    cursor: help;
    /* Span both columns in gutter grid mode so the banner sits above the
       full-width row rather than becoming a stray first-column item that
       displaces .rmd-body to col 2 and .rmd-gutter-col onto a new row.
       grid-column is ignored in non-grid (inline) mode. */
    grid-column: 1 / -1;
  }

  /* Per-block hover affordance, mirroring DiffFile.svelte's
     .line-actions / .add-comment-btn / .ask-ai-btn pattern so the
     rendered view's comment + Ask buttons look and behave the same
     as the diff view's per-line buttons. */
  .rmd-body :global(.rmd-block) {
    position: relative;
  }
  .rmd-body :global(.rmd-line-actions) {
    position: absolute;
    top: 4px;
    right: -52px;
    display: inline-flex;
    gap: 4px;
    opacity: 0;
    transition: opacity 0.1s;
    z-index: 1;
  }
  .rmd-body :global(.rmd-block:hover .rmd-line-actions),
  .rmd-body :global(.rmd-line-actions:focus-within) {
    opacity: 1;
  }
  .rmd-body :global(.rmd-add-comment-btn),
  .rmd-body :global(.rmd-ask-ai-btn) {
    width: 20px;
    height: 20px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: 3px;
    color: #fff;
    cursor: pointer;
    font-size: 12px;
    font-weight: 700;
    line-height: 1;
    padding: 0;
  }
  .rmd-body :global(.rmd-add-comment-btn) {
    background: var(--accent-blue);
  }
  .rmd-body :global(.rmd-ask-ai-btn) {
    background: var(--accent-claude);
  }
  .rmd-body :global(.rmd-add-comment-btn:hover),
  .rmd-body :global(.rmd-ask-ai-btn:hover) {
    filter: brightness(1.1);
  }

  .rmd-composer-wrap {
    position: relative;
    margin-top: 12px;
  }
  /* When we have a computed block offset, switch to absolute positioning
     so the composer appears at the active block's bottom edge rather than
     at the document's bottom. The z-index keeps it above thread cards.
     margin-top is cleared because the top property drives placement. */
  .rmd-composer-wrap--positioned {
    position: absolute;
    left: 24px;
    right: 24px;
    margin-top: 0;
    z-index: 10;
  }

  /* Right gutter column for gutter layout mode. Positioned relative
     to .rmd-view so the CommentGutter can use absolute placement. */
  .rmd-gutter-col {
    position: relative;
    min-height: 100%;
    align-self: stretch;
  }

  /* Draggable divider on the gutter's left edge. Sits in the column-gap and
     gives the gutter a visible boundary; drag horizontally to resize. */
  .rmd-gutter-resize {
    position: absolute;
    top: 0;
    bottom: 0;
    left: -16px;
    width: 16px;
    cursor: col-resize;
    z-index: 2;
    touch-action: none;
    /* A 2px line centered in the 16px hit area. */
    background: linear-gradient(
      to right,
      transparent 7px,
      var(--diff-border) 7px,
      var(--diff-border) 9px,
      transparent 9px
    );
  }
  .rmd-gutter-resize:hover,
  .rmd-gutter-resize--active {
    background: linear-gradient(
      to right,
      transparent 7px,
      var(--text-muted) 7px,
      var(--text-muted) 9px,
      transparent 9px
    );
  }
</style>
