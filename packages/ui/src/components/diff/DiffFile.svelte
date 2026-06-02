<script lang="ts">
  import { onMount } from "svelte";
  import type { DiffFile as DiffFileType, DiffHunk } from "../../api/types.js";
  import { getStores } from "../../context.js";

  const { diff: diffStore, ai: aiStore, detail: detailStore, reviewThreads: reviewThreadsStore } = getStores();
  import { tokenizeLineDual, langFromPath, type DualToken } from "../../utils/highlight.js";
  import { pairHunk } from "../../utils/diffPairing.js";
  import DiffLineComponent from "./DiffLine.svelte";
  import CollapsedRegion from "./CollapsedRegion.svelte";
  import DiffComposer from "./DiffComposer.svelte";
  import PendingCommentCard from "./PendingCommentCard.svelte";
  import AIAskComposer from "./AIAskComposer.svelte";
  import AIThreadCard from "./AIThreadCard.svelte";
  import ReviewThreadCard from "./ReviewThreadCard.svelte";
  import ReviewCommentCard from "./ReviewCommentCard.svelte";
  import RenderedMarkdownView from "./RenderedMarkdownView.svelte";
  import type { DraftComment } from "../../stores/diff.svelte.js";
  import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

  interface Props {
    file: DiffFileType;
    owner: string;
    name: string;
    number: number;
  }

  const { file, owner, name, number }: Props = $props();

  const collapsed = $derived(diffStore.isFileCollapsed(owner, name, number, file.path));
  const lang = $derived(langFromPath(file.path));
  const layout = $derived(diffStore.getLayout());

  // Markdown files get an optional "Rendered" view alongside the
  // standard "Diff" view. Local toggle state so each file's mode is
  // independent and resets to "diff" when the component remounts
  // (which happens on PR / scope change). Reviewers spend most time
  // in Diff; Rendered is the optional second view.
  const isMarkdown = $derived.by<boolean>(() => {
    const p = file.path.toLowerCase();
    return p.endsWith(".md") || p.endsWith(".mdx") || p.endsWith(".markdown");
  });
  let viewMode = $state<"diff" | "rendered">("diff");

  // Which SHA's tree to render the file from. For deleted files we
  // render the "before" tree (old_path / merge_base side); for
  // everything else we render the file at the current diff scope's
  // tree so the rendered view tracks what the reviewer is looking
  // at in the diff body.
  const renderedSHA = $derived.by<string>(() => {
    if (file.status === "deleted") {
      // Best-effort: there's no per-file old-side SHA on the diff
      // response, so fall back to the merge-base when we have it.
      // commits is newest-first; the oldest's parent isn't exposed
      // either. Empty SHA → component shows an error message and
      // the reviewer can still read the diff body for old content.
      return "";
    }
    return currentCommitSha();
  });
  const renderedPath = $derived(
    file.status === "deleted" ? (file.old_path || file.path) : file.path,
  );
  const viewed = $derived(diffStore.isFileReviewed(file.path));

  function toggleViewed(e: Event): void {
    e.stopPropagation();
    diffStore.markFileReviewed(file.path, !viewed);
  }

  // Draft/pending comment state, scoped to this file.
  // openComposer keys by `${line}:${side}`; null means no composer open.
  let openComposer = $state<string | null>(null);

  const pendingComments = $derived<DraftComment[]>(
    diffStore.getDraftCommentsForPath(file.path),
  );

  // Map of pending comments grouped by anchor key for fast lookup while
  // rendering lines.
  const pendingByAnchor = $derived.by(() => {
    const map = new Map<string, DraftComment[]>();
    for (const c of pendingComments) {
      const key = `${c.line}:${c.side}`;
      const arr = map.get(key) ?? [];
      arr.push(c);
      map.set(key, arr);
    }
    return map;
  });

  // Published review comments (synced from GitHub) for this file,
  // grouped by anchor so the render pass can place them inline
  // alongside pending drafts.
  const reviewCommentsByAnchor = $derived.by(() => {
    const byFile = detailStore.getReviewCommentsByFilePath();
    const forFile = byFile.get(file.path) ?? [];
    const map = new Map<string, PublishedReviewComment[]>();
    for (const c of forFile) {
      if (c.line <= 0) continue; // outdated; rendered elsewhere
      const key = `${c.line}:${c.side}`;
      const arr = map.get(key) ?? [];
      arr.push(c);
      map.set(key, arr);
    }
    return map;
  });

  // Count of comments on this file whose anchors don't resolve in
  // the current diff (line is null because GitHub marked the comment
  // outdated after a push). We surface this as a banner at the top
  // of the file so the reviewer knows to look on GitHub.
  const outdatedReviewCount = $derived.by(() => {
    const byFile = detailStore.getReviewCommentsByFilePath();
    const forFile = byFile.get(file.path) ?? [];
    return forFile.filter((c) => c.line <= 0).length;
  });

  // The SHA we anchor new comments against — the newest commit in the
  // currently scoped diff. Falls back to "HEAD" marker for head scope
  // when commits haven't loaded yet, in which case the publish step
  // will attach the actual PR head SHA.
  function currentCommitSha(): string {
    const scope = diffStore.getScope();
    if (scope.kind === "commit") return scope.sha;
    if (scope.kind === "range") return scope.toSha;
    const commits = diffStore.getCommits();
    if (commits && commits.length > 0) return commits[0]!.sha;
    return "";
  }

  // liveSelection continuously tracks the reviewer's text selection
  // inside this file. Updated on every selectionchange event so we
  // never depend on a snapshot taken at click/mousedown time — which
  // could be racing with the browser's own focus-driven selection
  // clearing. Stays non-null as long as a multi-line same-side
  // selection exists.
  let liveSelection = $state<{ startLine: number; endLine: number; side: "LEFT" | "RIGHT" } | null>(null);

  // rangeSnapshot is the range we'll anchor the next comment/question
  // to. snapshotRangeFor copies liveSelection into it at click time.
  let rangeSnapshot = $state<{ startLine: number; endLine: number; side: "LEFT" | "RIGHT" } | null>(null);

  $effect(() => {
    if (typeof document === "undefined") return;
    const handler = (): void => {
      liveSelection = computeSelectionRange();
    };
    document.addEventListener("selectionchange", handler);
    return () => document.removeEventListener("selectionchange", handler);
  });

  function computeSelectionRange(): { startLine: number; endLine: number; side: "LEFT" | "RIGHT" } | null {
    if (typeof window === "undefined") return null;
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
    if (!fileEl) return null;

    const anchorWrap = nearestLineWrap(sel.anchorNode);
    const focusWrap = nearestLineWrap(sel.focusNode);
    if (!anchorWrap || !focusWrap) return null;
    if (!fileEl.contains(anchorWrap) || !fileEl.contains(focusWrap)) return null;

    const aSide = anchorWrap.dataset.anchorSide;
    const fSide = focusWrap.dataset.anchorSide;
    if (aSide !== fSide) return null;
    if (aSide !== "LEFT" && aSide !== "RIGHT") return null;

    const a = parseInt(anchorWrap.dataset.anchorLine ?? "", 10);
    const f = parseInt(focusWrap.dataset.anchorLine ?? "", 10);
    if (!Number.isFinite(a) || !Number.isFinite(f) || a === f) return null;
    const [startLine, endLine] = a < f ? [a, f] : [f, a];
    return { startLine, endLine, side: aSide };
  }

  function snapshotRangeFor(side: "LEFT" | "RIGHT"): void {
    // Try a fresh read from window.getSelection() first — we run
    // inside mousedown, which is early enough that the selection is
    // typically still alive. Fall back to the most recent live
    // selection from the selectionchange listener if the fresh read
    // comes up empty (some browsers collapse before mousedown fires).
    const fresh = computeSelectionRange();
    const pick = fresh ?? liveSelection;
    if (pick && pick.side === side) {
      rangeSnapshot = pick;
    } else {
      rangeSnapshot = null;
    }
  }

  // preserveSelection runs on mousedown. It (1) captures the range
  // before the browser's default focus behavior can collapse the
  // selection, and (2) preventDefault()s so the selection stays
  // visually alive through the click. Both steps are belt-and-braces
  // on top of the continuous selectionchange tracking above.
  function preserveSelection(side: "LEFT" | "RIGHT", e: MouseEvent): void {
    e.preventDefault();
    snapshotRangeFor(side);
    const selText = typeof window !== "undefined"
      ? (window.getSelection()?.toString() ?? "")
      : "";
    selectionSnapshot = selText.trim() || null;
  }

  // composerAnchor packages the anchor for a composer instance so it
  // can render an indicator showing which line(s) will be commented
  // on. Uses rangeSnapshot when a multi-line selection is live; falls
  // back to a single-line anchor on the clicked line.
  function composerAnchor(
    line: number,
    side: "LEFT" | "RIGHT",
  ): { line: number; side: "LEFT" | "RIGHT"; startLine?: number } {
    const r = rangeSnapshot;
    if (r && r.side === side && r.startLine !== r.endLine) {
      return { line: r.endLine, side, startLine: r.startLine };
    }
    return { line, side };
  }

  // rangeTooltip returns a label for the + / ? button indicating the
  // range it'll anchor to, given the anchor side. Null means the
  // button falls back to a single-line anchor (no active selection
  // on this side), so callers can render a normal tooltip.
  function rangeTooltip(side: "LEFT" | "RIGHT"): string | null {
    const sel = liveSelection;
    if (!sel || sel.side !== side) return null;
    return `lines ${sel.startLine}–${sel.endLine}`;
  }

  // Floating toolbar position. When a multi-line selection is active
  // we render a compact floating action bar next to the end of the
  // selection so the reviewer doesn't need to find the hover-only
  // + / ? buttons. Position updates on selection changes and on
  // scroll so the toolbar tracks the selection.
  let toolbarTop = $state(0);
  let toolbarLeft = $state(0);

  function updateToolbarPosition(): void {
    if (typeof window === "undefined" || !liveSelection) return;
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return;
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return;
    toolbarTop = rect.bottom + window.scrollY + 4;
    toolbarLeft = rect.right + window.scrollX - 180;
  }

  $effect(() => {
    // Reposition whenever liveSelection changes. `liveSelection`
    // gates the effect so we only run when it's actually set.
    if (liveSelection) updateToolbarPosition();
  });

  function openComposerFromToolbar(): void {
    if (!liveSelection) return;
    rangeSnapshot = liveSelection;
    openComposer = `${liveSelection.endLine}:${liveSelection.side}`;
  }

  function openAskFromToolbar(): void {
    if (!liveSelection) return;
    rangeSnapshot = liveSelection;
    const selText = typeof window !== "undefined"
      ? (window.getSelection()?.toString() ?? "")
      : "";
    selectionSnapshot = selText.trim() || null;
    askError = null;
    openAsk = `${liveSelection.endLine}:${liveSelection.side}`;
  }

  function nearestLineWrap(node: Node | null): HTMLElement | null {
    let el = node instanceof Element ? node : node?.parentElement ?? null;
    while (el) {
      if (el instanceof HTMLElement && el.dataset.anchorLine != null) return el;
      el = el.parentElement;
    }
    return null;
  }

  function openComposerFor(line: number, side: "LEFT" | "RIGHT"): void {
    // Range was already snapshotted by the button's onmousedown; do
    // not re-snapshot here because the selection is typically gone
    // by click time.
    openComposer = `${line}:${side}`;
  }

  function closeComposer(): void {
    openComposer = null;
    rangeSnapshot = null;
  }

  function saveDraft(line: number, side: "LEFT" | "RIGHT", body: string): void {
    const commitSha = currentCommitSha();
    const range = rangeSnapshot;
    diffStore.addDraftComment({
      path: file.path,
      // GitHub's convention for multi-line comments: `line` is the
      // last line, `startLine` is the first. Respect that even when
      // the reviewer clicked the + on the start line.
      line: range ? range.endLine : line,
      side,
      ...(range && range.startLine !== range.endLine
        ? { startLine: range.startLine }
        : {}),
      commitSha,
      body,
    });
    closeComposer();
  }

  // --- AI Q&A composer state ---

  // openAsk: key "line:side" of a line with an open Ask composer.
  let openAsk = $state<string | null>(null);
  let askError = $state<string | null>(null);
  let askSubmitting = $state(false);
  // selectionSnapshot captures the text selected at the moment Ask
  // was clicked, so the reviewer can keep typing without worrying
  // about losing the selection.
  let selectionSnapshot = $state<string | null>(null);

  function openAskFor(line: number, side: "LEFT" | "RIGHT"): void {
    // Selection text and range were both captured by the button's
    // onmousedown before the browser cleared the selection. Don't
    // try to re-read here — it'll be collapsed.
    openAsk = `${line}:${side}`;
    askError = null;
  }

  function closeAsk(): void {
    openAsk = null;
    selectionSnapshot = null;
    askError = null;
    askSubmitting = false;
    rangeSnapshot = null;
  }

  async function submitAsk(line: number, side: "LEFT" | "RIGHT", question: string): Promise<void> {
    if (askSubmitting) return;
    askSubmitting = true;
    askError = null;

    try {
      // Commits back the anchor SHA. If they haven't been loaded yet
      // (reviewer never expanded the Commits panel), load them now so
      // we can attach a valid commit_sha to the request.
      let commitSha = currentCommitSha();
      if (!commitSha) {
        await diffStore.loadCommits();
        commitSha = currentCommitSha();
      }
      if (!commitSha) {
        askError = "Can't ask yet — commits haven't loaded for this PR.";
        return;
      }

      const range = rangeSnapshot;
      const body: Parameters<typeof aiStore.createThread>[0] = {
        path: file.path,
        anchor_side: side,
        // Use the last line of the range as the primary anchor, matching
        // the review-comment convention. Pass hunk_start_line/hunk_end_line
        // so the backend prompt includes the range in the question
        // context sent to Claude.
        anchor_line: range ? range.endLine : line,
        commit_sha: commitSha,
        question,
      };
      if (range && range.startLine !== range.endLine) {
        body.hunk_start_line = range.startLine;
        body.hunk_end_line = range.endLine;
      }
      if (selectionSnapshot) body.selection_text = selectionSnapshot;

      const result = await aiStore.createThread(body);
      if (!result.ok) {
        askError = result.error;
        return;
      }
      closeAsk();
    } finally {
      askSubmitting = false;
    }
  }

  function getAIThreadsAtAnchor(line: number, side: "LEFT" | "RIGHT") {
    return aiStore.getThreadsAtAnchor(file.path, line, side);
  }

  function getReviewThreadsAtAnchor(line: number, side: "LEFT" | "RIGHT") {
    return reviewThreadsStore.getThreadsAtAnchor(file.path, line, side);
  }

  // Maps a unified-line's type + available line numbers to a (line,
  // side) anchor for a new comment. Context lines anchor to the new
  // file (RIGHT) by convention; this matches GitHub's behavior.
  function anchorFor(
    lineType: "context" | "add" | "delete",
    oldNum?: number,
    newNum?: number,
  ): { line: number; side: "LEFT" | "RIGHT" } | null {
    if (lineType === "delete" && oldNum != null) {
      return { line: oldNum, side: "LEFT" };
    }
    if (newNum != null) return { line: newNum, side: "RIGHT" };
    return null;
  }

  // Auto-mark viewed when the file has been scrolled past (its bottom
  // edge has moved above the viewport). Only fires AFTER the file has
  // been in view at least once, so files that never enter the viewport
  // do not auto-check. Never un-marks — removal is user-initiated.
  let hasBeenSeen = $state(false);
  $effect(() => {
    if (inViewport) hasBeenSeen = true;
  });
  $effect(() => {
    if (hasBeenSeen && !inViewport && !viewed) {
      const rect = fileEl?.getBoundingClientRect();
      if (rect && rect.bottom < 0) {
        diffStore.markFileReviewed(file.path, true);
      }
    }
  });

  // Track viewport visibility so off-screen files skip expensive tokenization
  // on whitespace toggles and theme switches. Starts false so the initial
  // render on large diffs doesn't eagerly tokenize every file before the
  // IntersectionObserver reports visibility — the first observer callback
  // fires synchronously for on-screen files.
  let fileEl: HTMLDivElement | undefined = $state();
  let inViewport = $state(false);

  // Local copy of file data, only synced when expanded AND visible. Collapsed
  // or off-screen files keep stale content so whitespace toggles and theme
  // switches don't trigger expensive re-renders and re-tokenization for
  // content no one can see.
  // svelte-ignore state_referenced_locally — synced from file prop via $effect
  let renderedFile = $state(file);

  $effect(() => {
    if (!collapsed && inViewport) {
      const prev = renderedFile;
      renderedFile = file;
      // Clear stale tokens synchronously so any render before the
      // tokenization effect runs falls through to raw content
      // instead of showing cached tokens from the old file.
      if (file !== prev) {
        tokens = new Map();
      }
    }
  });

  onMount(() => {
    let observer: IntersectionObserver | undefined;
    // Guard for jsdom / SSR-ish test environments where IntersectionObserver
    // is not provided — treat the file as visible so tokenization still runs.
    if (typeof IntersectionObserver === "undefined") {
      inViewport = true;
      return;
    }
    if (fileEl) {
      observer = new IntersectionObserver(
        (entries) => { inViewport = entries[0]!.isIntersecting; },
        { rootMargin: "200px 0px" },
      );
      observer.observe(fileEl);
    }

    return () => { observer?.disconnect(); };
  });

  // Dual-theme token cache — each span carries both colors as CSS custom
  // properties, so theme switch is pure CSS (zero DOM updates, zero
  // re-renders). Tokenization happens once per line using Shiki's native
  // dual-theme API, which guarantees aligned token boundaries across themes.
  let tokens = $state<Map<string, DualToken[]>>(new Map());
  let tokenVersion = 0;

  // Plain (non-reactive) tracking of the last tokenized source and whether
  // tokenization finished. Used to distinguish source changes (which need a
  // fresh cache) from visibility flips (which should reuse the cache).
  let lastSourceFile: DiffFileType | undefined;
  let lastSourceLang: string | undefined;
  let tokenizationComplete = false;

  // Tokenize in small batches to avoid blocking the main thread.
  const BATCH_SIZE = 50;

  // Tokenize for BOTH themes when file data changes.
  // Skipped for collapsed or off-screen files; runs when they become visible.
  // Does NOT depend on `theme` — theme switches just swap which cache is read.
  $effect(() => {
    const version = ++tokenVersion;
    const currentFile = renderedFile;
    const currentLang = lang;
    const sourceChanged =
      currentFile !== lastSourceFile || currentLang !== lastSourceLang;

    if (sourceChanged) {
      lastSourceFile = currentFile;
      lastSourceLang = currentLang;
      tokenizationComplete = false;
    }

    if (collapsed || !inViewport) return;
    // Already fully tokenized for this source — scrolling back into view or
    // re-expanding should reuse the cached tokens, not rebuild them.
    if (tokenizationComplete) return;

    // About to (re)start tokenization for this source — clear any stale or
    // partial entries so the first batch doesn't render a mix of old and
    // new keys while the async tokenization walks the hunks.
    tokens = new Map();
    const next = new Map<string, DualToken[]>();

    void (async () => {
      const items: Array<{ key: string; content: string }> = [];
      for (let hi = 0; hi < currentFile.hunks.length; hi++) {
        const hunk = currentFile.hunks[hi]!;
        for (let li = 0; li < hunk.lines.length; li++) {
          items.push({ key: `${hi}:${li}`, content: hunk.lines[li]!.content });
        }
      }

      for (let i = 0; i < items.length; i += BATCH_SIZE) {
        if (version !== tokenVersion) return;
        const batch = items.slice(i, i + BATCH_SIZE);
        const results = await Promise.all(
          batch.map(async (item) => ({
            key: item.key,
            spans: await tokenizeLineDual(item.content, currentLang),
          })),
        );
        if (version !== tokenVersion) return;
        for (const r of results) {
          next.set(r.key, r.spans);
        }
        // Update reactively after each batch so lines get highlighted progressively.
        tokens = new Map(next);
        // Yield to the browser between batches.
        if (i + BATCH_SIZE < items.length) {
          await new Promise((r) => requestAnimationFrame(r));
        }
      }
      if (version === tokenVersion) {
        tokenizationComplete = true;
      }
    })();
  });

  function getTokens(hunkIdx: number, lineIdx: number): DualToken[] {
    const key = `${hunkIdx}:${lineIdx}`;
    const cached = tokens.get(key);
    if (cached) return cached;
    return [{ content: renderedFile.hunks[hunkIdx]!.lines[lineIdx]!.content }];
  }

  function computeCollapsedLines(hunks: DiffHunk[], hunkIdx: number): number {
    if (hunkIdx === 0) return 0;
    const prev = hunks[hunkIdx - 1]!;
    const curr = hunks[hunkIdx]!;
    const prevEndOld = prev.old_start + prev.old_count;
    const gapOld = curr.old_start - prevEndOld;
    return Math.max(gapOld, 0);
  }

  function toggle(): void {
    diffStore.toggleFileCollapsed(owner, name, number, file.path);
  }

  function displayPath(f: DiffFileType): string {
    if (f.status === "renamed" && f.old_path !== f.path) {
      return `${f.old_path} -> ${f.path}`;
    }
    return f.path;
  }
</script>

{#if liveSelection}
  <div
    class="selection-toolbar"
    style="top: {toolbarTop}px; left: {toolbarLeft}px"
    onmousedown={(e) => e.preventDefault()}
    role="toolbar"
    tabindex="-1"
    aria-label="Selection actions"
  >
    <span class="selection-toolbar__label">
      Lines {liveSelection.startLine}–{liveSelection.endLine}
    </span>
    <button
      type="button"
      class="selection-toolbar__btn selection-toolbar__btn--comment"
      onclick={openComposerFromToolbar}
      title="Comment on the selected lines"
    >
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M5 2V8M2 5H8" stroke-linecap="round" />
      </svg>
      Comment
    </button>
    <button
      type="button"
      class="selection-toolbar__btn selection-toolbar__btn--ask"
      onclick={openAskFromToolbar}
      title="Ask Claude about the selected lines"
    >
      ? Ask
    </button>
  </div>
{/if}

<div
  class="diff-file"
  class:diff-file--viewed={viewed}
  class:diff-file--selecting={liveSelection != null}
  data-file-path={file.path}
  bind:this={fileEl}
>
  <div class="file-header">
    <button class="file-header__collapse" onclick={toggle} title={collapsed ? "Expand file" : "Collapse file"}>
      <svg class="collapse-chevron" class:collapse-chevron--collapsed={collapsed} width="12" height="12" viewBox="0 0 12 12" fill="none">
        <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <span class="file-path" class:file-path--deleted={file.status === "deleted"}>
        {displayPath(file)}
      </span>
      <span class="file-stats">
        <span class="stat" class:stat--add={file.additions > 0} class:stat--dim={file.additions === 0}>+{file.additions}</span>
        <span class="stat" class:stat--del={file.deletions > 0} class:stat--dim={file.deletions === 0}>-{file.deletions}</span>
      </span>
    </button>
    {#if isMarkdown}
      <div class="file-header__viewmode" role="tablist" aria-label="View mode">
        <button
          type="button"
          class="vm-btn"
          class:vm-btn--active={viewMode === "diff"}
          onclick={() => (viewMode = "diff")}
          role="tab"
          aria-selected={viewMode === "diff"}
          title="Show the unified or split diff"
        >Diff</button>
        <button
          type="button"
          class="vm-btn"
          class:vm-btn--active={viewMode === "rendered"}
          onclick={() => (viewMode = "rendered")}
          role="tab"
          aria-selected={viewMode === "rendered"}
          title="Show the file rendered as Markdown at this diff scope's tree"
        >Rendered</button>
      </div>
    {/if}
    <label class="file-header__viewed" title={viewed ? "Mark as not viewed" : "Mark as viewed"}>
      <input
        type="checkbox"
        checked={viewed}
        onclick={toggleViewed}
      />
      <span>Viewed</span>
    </label>
  </div>
  {#if !collapsed && isMarkdown && viewMode === "rendered"}
    <div class="file-content">
      <RenderedMarkdownView
        {owner}
        {name}
        {number}
        path={renderedPath}
        sha={renderedSHA}
        hunks={(file.hunks ?? []).map((h: DiffHunk) => ({
          new_start: h.new_start,
          new_count: h.new_count,
        }))}
      />
    </div>
  {:else if !collapsed}
    <div class="file-content">
      {#if outdatedReviewCount > 0}
        <div class="outdated-banner" title="These comments were made against an older version of this file; their line numbers don't resolve in the current diff.">
          {outdatedReviewCount} outdated review comment{outdatedReviewCount === 1 ? "" : "s"} on this file — view on GitHub to see them
        </div>
      {/if}
      {#if renderedFile.is_binary}
        <div class="binary-notice">Binary file changed</div>
      {:else}
        <div class="file-rows" class:file-rows--split={layout === "split"}>
          {#if renderedFile.hunks[0] && (renderedFile.hunks[0].new_start > 1 || renderedFile.hunks[0].old_start > 1)}
            {@const firstHunk = renderedFile.hunks[0]}
            <CollapsedRegion
              position="top"
              {layout}
              {lang}
              lineCount={Math.max(firstHunk.new_start - 1, firstHunk.old_start - 1)}
              path={renderedFile.path}
              sha={currentCommitSha()}
              {owner}
              {name}
              {number}
              gapOldStart={1}
              gapNewStart={1}
            />
          {/if}
          {#each renderedFile.hunks as hunk, hunkIdx}
            {#if hunkIdx > 0}
              {@const gap = computeCollapsedLines(renderedFile.hunks, hunkIdx)}
              {@const prev = renderedFile.hunks[hunkIdx - 1]}
              {#if gap > 0 && prev}
                <CollapsedRegion
                  position="middle"
                  {layout}
                  lineCount={gap}
                  path={renderedFile.path}
                  sha={currentCommitSha()}
                  {owner}
                  {name}
                  {number}
                  gapOldStart={prev.old_start + prev.old_count}
                  gapNewStart={prev.new_start + prev.new_count}
                />
              {/if}
            {/if}
            {#if layout === "split"}
              <div class="hunk-header hunk-header--split">
                <span class="hunk-text">@@ -{hunk.old_start},{hunk.old_count} +{hunk.new_start},{hunk.new_count} @@{hunk.section ? ` ${hunk.section}` : ""}</span>
              </div>
              {#each pairHunk(hunk) as row}
                {@const leftAnchor = row.left
                  ? anchorFor(row.left.line.type, row.left.line.old_num ?? undefined, undefined)
                  : null}
                {@const rightAnchor = row.right
                  ? anchorFor(row.right.line.type, undefined, row.right.line.new_num ?? undefined)
                  : null}
                {@const leftKey = leftAnchor ? `${leftAnchor.line}:${leftAnchor.side}` : null}
                {@const rightKey = rightAnchor ? `${rightAnchor.line}:${rightAnchor.side}` : null}
                <div class="ss-row">
                  <div class="ss-cell ss-cell--left">
                    {#if row.left}
                      <div
                        class="line-wrap"
                        class:line-wrap--commentable={!!leftAnchor}
                        {...(leftAnchor
                          ? { "data-anchor-line": leftAnchor.line, "data-anchor-side": leftAnchor.side }
                          : {})}
                      >
                        <DiffLineComponent
                          type={row.left.line.type}
                          content={row.left.line.content}
                          {...(row.left.line.old_num != null ? { oldNum: row.left.line.old_num } : {})}
                          {...(row.left.line.no_newline ? { noNewline: row.left.line.no_newline } : {})}
                          tokens={getTokens(hunkIdx, row.left.lineIdx)}
                          splitSide="left"
                        />
                        {#if leftAnchor}
                          <div class="line-actions">
                            <button
                              type="button"
                              class="add-comment-btn"
                              class:add-comment-btn--range={rangeTooltip(leftAnchor.side) != null}
                              onmousedown={(e) => preserveSelection(leftAnchor.side, e)}
                              onclick={() => openComposerFor(leftAnchor.line, leftAnchor.side)}
                              title={rangeTooltip(leftAnchor.side)
                                ? `Comment on ${rangeTooltip(leftAnchor.side)}`
                                : "Add review comment"}
                            >
                              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                              </svg>
                            </button>
                            <button
                              type="button"
                              class="ask-ai-btn"
                              class:ask-ai-btn--range={rangeTooltip(leftAnchor.side) != null}
                              onmousedown={(e) => preserveSelection(leftAnchor.side, e)}
                              onclick={() => openAskFor(leftAnchor.line, leftAnchor.side)}
                              title={rangeTooltip(leftAnchor.side)
                                ? `Ask Claude about ${rangeTooltip(leftAnchor.side)}`
                                : "Ask Claude about this line"}
                            >
                              ?
                            </button>
                          </div>
                        {/if}
                      </div>
                    {:else}
                      <div class="ss-empty"></div>
                    {/if}
                  </div>
                  <div class="ss-cell ss-cell--right">
                    {#if row.right}
                      <div
                        class="line-wrap"
                        class:line-wrap--commentable={!!rightAnchor}
                        {...(rightAnchor
                          ? { "data-anchor-line": rightAnchor.line, "data-anchor-side": rightAnchor.side }
                          : {})}
                      >
                        <DiffLineComponent
                          type={row.right.line.type}
                          content={row.right.line.content}
                          {...(row.right.line.new_num != null ? { newNum: row.right.line.new_num } : {})}
                          {...(row.right.line.no_newline ? { noNewline: row.right.line.no_newline } : {})}
                          tokens={getTokens(hunkIdx, row.right.lineIdx)}
                          splitSide="right"
                        />
                        {#if rightAnchor}
                          <div class="line-actions">
                            <button
                              type="button"
                              class="add-comment-btn"
                              class:add-comment-btn--range={rangeTooltip(rightAnchor.side) != null}
                              onmousedown={(e) => preserveSelection(rightAnchor.side, e)}
                              onclick={() => openComposerFor(rightAnchor.line, rightAnchor.side)}
                              title={rangeTooltip(rightAnchor.side)
                                ? `Comment on ${rangeTooltip(rightAnchor.side)}`
                                : "Add review comment"}
                            >
                              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                              </svg>
                            </button>
                            <button
                              type="button"
                              class="ask-ai-btn"
                              class:ask-ai-btn--range={rangeTooltip(rightAnchor.side) != null}
                              onmousedown={(e) => preserveSelection(rightAnchor.side, e)}
                              onclick={() => openAskFor(rightAnchor.line, rightAnchor.side)}
                              title={rangeTooltip(rightAnchor.side)
                                ? `Ask Claude about ${rangeTooltip(rightAnchor.side)}`
                                : "Ask Claude about this line"}
                            >
                              ?
                            </button>
                          </div>
                        {/if}
                      </div>
                    {:else}
                      <div class="ss-empty"></div>
                    {/if}
                  </div>
                </div>
                {#if leftKey && openComposer === leftKey && leftAnchor}
                  <DiffComposer
                    anchor={composerAnchor(leftAnchor.line, leftAnchor.side)}
                    onsave={(body) => saveDraft(leftAnchor.line, leftAnchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if rightKey && openComposer === rightKey && rightAnchor}
                  <DiffComposer
                    anchor={composerAnchor(rightAnchor.line, rightAnchor.side)}
                    onsave={(body) => saveDraft(rightAnchor.line, rightAnchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if leftKey && openAsk === leftKey && leftAnchor}
                  <AIAskComposer
                    anchor={composerAnchor(leftAnchor.line, leftAnchor.side)}
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(leftAnchor.line, leftAnchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if rightKey && openAsk === rightKey && rightAnchor}
                  <AIAskComposer
                    anchor={composerAnchor(rightAnchor.line, rightAnchor.side)}
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(rightAnchor.line, rightAnchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if leftKey}
                  {@const published = reviewCommentsByAnchor.get(leftKey) ?? []}
                  {#each published as rc (rc.id)}
                    <ReviewCommentCard
                      comment={rc}
                      repoOwner={owner}
                      repoName={name}
                      currentHeadSha={currentCommitSha()}
                    />
                  {/each}
                  {@const pending = pendingByAnchor.get(leftKey) ?? []}
                  {#each pending as p (p.id)}
                    <PendingCommentCard
                      comment={p}
                      currentHeadSha={currentCommitSha()}
                      ondelete={() => diffStore.removeDraftComment(p.id)}
                    />
                  {/each}
                  {#if leftAnchor}
                    {#each getAIThreadsAtAnchor(leftAnchor.line, leftAnchor.side) as thread (thread.id)}
                      <AIThreadCard {thread} repoOwner={owner} repoName={name} />
                    {/each}
                    {#each getReviewThreadsAtAnchor(leftAnchor.line, leftAnchor.side) as rt (rt.id)}
                      <ReviewThreadCard thread={rt} />
                    {/each}
                  {/if}
                {/if}
                {#if rightKey}
                  {@const published = reviewCommentsByAnchor.get(rightKey) ?? []}
                  {#each published as rc (rc.id)}
                    <ReviewCommentCard
                      comment={rc}
                      repoOwner={owner}
                      repoName={name}
                      currentHeadSha={currentCommitSha()}
                    />
                  {/each}
                  {@const pending = pendingByAnchor.get(rightKey) ?? []}
                  {#each pending as p (p.id)}
                    <PendingCommentCard
                      comment={p}
                      currentHeadSha={currentCommitSha()}
                      ondelete={() => diffStore.removeDraftComment(p.id)}
                    />
                  {/each}
                  {#if rightAnchor}
                    {#each getAIThreadsAtAnchor(rightAnchor.line, rightAnchor.side) as thread (thread.id)}
                      <AIThreadCard {thread} repoOwner={owner} repoName={name} />
                    {/each}
                    {#each getReviewThreadsAtAnchor(rightAnchor.line, rightAnchor.side) as rt (rt.id)}
                      <ReviewThreadCard thread={rt} />
                    {/each}
                  {/if}
                {/if}
              {/each}
            {:else}
              <div class="hunk-header">
                <span class="hunk-gutter"></span>
                <span class="hunk-gutter"></span>
                <span class="hunk-text">@@ -{hunk.old_start},{hunk.old_count} +{hunk.new_start},{hunk.new_count} @@{hunk.section ? ` ${hunk.section}` : ""}</span>
              </div>
              {#each hunk.lines as line, lineIdx}
                {@const anchor = anchorFor(line.type, line.old_num ?? undefined, line.new_num ?? undefined)}
                {@const anchorKey = anchor ? `${anchor.line}:${anchor.side}` : null}
                <div
                  class="line-wrap"
                  class:line-wrap--commentable={!!anchor}
                  {...(anchor
                    ? { "data-anchor-line": anchor.line, "data-anchor-side": anchor.side }
                    : {})}
                >
                  <DiffLineComponent
                    type={line.type}
                    content={line.content}
                    {...(line.old_num != null ? { oldNum: line.old_num } : {})}
                    {...(line.new_num != null ? { newNum: line.new_num } : {})}
                    {...(line.no_newline ? { noNewline: line.no_newline } : {})}
                    tokens={getTokens(hunkIdx, lineIdx)}
                  />
                  {#if anchor}
                    <div class="line-actions">
                      <button
                        type="button"
                        class="add-comment-btn"
                        class:add-comment-btn--range={rangeTooltip(anchor.side) != null}
                        onmousedown={(e) => preserveSelection(anchor.side, e)}
                        onclick={() => openComposerFor(anchor.line, anchor.side)}
                        title={rangeTooltip(anchor.side)
                          ? `Comment on ${rangeTooltip(anchor.side)}`
                          : "Add review comment"}
                      >
                        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                          <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        class="ask-ai-btn"
                        class:ask-ai-btn--range={rangeTooltip(anchor.side) != null}
                        onmousedown={(e) => preserveSelection(anchor.side, e)}
                        onclick={() => openAskFor(anchor.line, anchor.side)}
                        title={rangeTooltip(anchor.side)
                          ? `Ask Claude about ${rangeTooltip(anchor.side)}`
                          : "Ask Claude about this line"}
                      >
                        ?
                      </button>
                    </div>
                  {/if}
                </div>
                {#if anchorKey && openComposer === anchorKey && anchor}
                  <DiffComposer
                    anchor={composerAnchor(anchor.line, anchor.side)}
                    onsave={(body) => saveDraft(anchor.line, anchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if anchorKey && openAsk === anchorKey && anchor}
                  <AIAskComposer
                    anchor={composerAnchor(anchor.line, anchor.side)}
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(anchor.line, anchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if anchorKey}
                  {@const published = reviewCommentsByAnchor.get(anchorKey) ?? []}
                  {#each published as rc (rc.id)}
                    <ReviewCommentCard
                      comment={rc}
                      repoOwner={owner}
                      repoName={name}
                      currentHeadSha={currentCommitSha()}
                    />
                  {/each}
                  {@const pending = pendingByAnchor.get(anchorKey) ?? []}
                  {#each pending as p (p.id)}
                    <PendingCommentCard
                      comment={p}
                      currentHeadSha={currentCommitSha()}
                      ondelete={() => diffStore.removeDraftComment(p.id)}
                    />
                  {/each}
                  {#if anchor}
                    {#each getAIThreadsAtAnchor(anchor.line, anchor.side) as thread (thread.id)}
                      <AIThreadCard {thread} repoOwner={owner} repoName={name} />
                    {/each}
                    {#each getReviewThreadsAtAnchor(anchor.line, anchor.side) as rt (rt.id)}
                      <ReviewThreadCard thread={rt} />
                    {/each}
                  {/if}
                {/if}
              {/each}
            {/if}
          {/each}
          {#if renderedFile.hunks.length > 0}
            {@const lastHunk = renderedFile.hunks[renderedFile.hunks.length - 1]!}
            <CollapsedRegion
              position="bottom"
              {layout}
              {lang}
              lineCount={0}
              path={renderedFile.path}
              sha={currentCommitSha()}
              {owner}
              {name}
              {number}
              gapOldStart={lastHunk.old_start + lastHunk.old_count}
              gapNewStart={lastHunk.new_start + lastHunk.new_count}
            />
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .diff-file {
    border-top: 2px solid var(--diff-border);
  }

  .file-header {
    position: sticky;
    top: 0;
    z-index: 2;
    display: flex;
    align-items: stretch;
    gap: 0;
    width: 100%;
    background: var(--diff-header-bg);
    border-bottom: 1px solid var(--diff-border);
    font-size: 12px;
    color: var(--diff-text);
  }

  .file-header__collapse {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    background: none;
    border: none;
    text-align: left;
    cursor: pointer;
    color: inherit;
    min-width: 0;
  }

  .file-header__collapse:hover {
    background: var(--bg-surface-hover);
  }

  .file-header__viewed {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 12px;
    border-left: 1px solid var(--diff-border);
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 11px;
    user-select: none;
    flex-shrink: 0;
  }

  .file-header__viewed:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .file-header__viewed input {
    cursor: pointer;
    margin: 0;
  }

  .diff-file--viewed > .file-header {
    opacity: 0.6;
  }

  .diff-file--viewed > .file-header .file-path {
    color: var(--text-muted);
  }

  .collapse-chevron {
    transition: transform 0.15s ease-out;
    flex-shrink: 0;
  }

  .collapse-chevron--collapsed {
    transform: rotate(-90deg);
  }

  .file-path {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--diff-text);
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    /* Selectable even though it sits inside the collapse button, so the
       path can be copied; a drag selects, a plain click still toggles. */
    user-select: text;
  }

  .file-path--deleted {
    text-decoration: line-through;
  }

  .file-stats {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }

  .stat {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    min-width: 3.5ch;
    text-align: right;
  }

  /* "Diff | Rendered" pair on .md/.mdx/.markdown files. Sits next
     to the Viewed checkbox in the file header. Quiet at rest; the
     active tab is highlighted with the same accent the file path
     uses elsewhere so it doesn't shout. */
  .file-header__viewmode {
    display: inline-flex;
    gap: 0;
    margin-left: auto;
    margin-right: 12px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    overflow: hidden;
    background: var(--bg-surface);
  }

  .vm-btn {
    padding: 2px 8px;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-secondary);
    background: transparent;
    border: none;
    cursor: pointer;
  }
  .vm-btn:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }
  .vm-btn--active {
    color: #fff;
    background: var(--accent-blue);
  }

  .stat--add {
    color: var(--diff-add-text);
  }

  .stat--del {
    color: var(--diff-del-text);
  }

  .stat--dim {
    opacity: 0.3;
  }

  .file-content {
    overflow-x: auto;
  }

  .file-rows {
    /* Constrain rows to the parent width so DiffLine's pre-wrap can
       wrap long content. Previously width: max-content forced each
       row to its widest content, which made horizontal scrolling
       the only option for long lines. */
    width: 100%;
  }

  .file-rows--split {
    width: 100%;
  }

  .ss-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  .ss-cell {
    min-width: 0;
    overflow-x: auto;
  }

  .ss-cell--left {
    border-right: 1px solid var(--diff-border);
  }

  .ss-empty {
    height: 20px;
    background: var(--diff-empty-bg, var(--bg-inset));
  }

  .hunk-header--split {
    display: block;
  }

  .binary-notice {
    padding: 20px;
    text-align: center;
    color: var(--diff-line-num);
    font-size: 13px;
    font-style: italic;
  }

  .outdated-banner {
    padding: 6px 14px;
    font-size: 11px;
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 8%, var(--bg-inset));
    border-bottom: 1px solid color-mix(in srgb, var(--accent-amber) 30%, var(--diff-border));
    cursor: help;
  }

  .hunk-header {
    display: flex;
    align-items: stretch;
    background: var(--diff-hunk-bg);
    color: var(--diff-hunk-text);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 20px;
  }

  .hunk-gutter {
    width: 50px;
    flex-shrink: 0;
    background: var(--diff-hunk-bg);
  }

  .hunk-text {
    padding: 2px 12px;
    white-space: pre;
  }

  /* Wraps a diff line so the add-comment button can be positioned over
     its left edge. Only appears on hover for commentable lines. */
  .line-wrap {
    position: relative;
  }

  /* Brief flash when the user jumps to a question's anchor from the
     Questions panel — the smooth scroll alone makes it ambiguous
     which line you landed on when threads sit near each other. */
  :global(.line-wrap--flash) {
    animation: line-wrap-flash 1.5s ease-out;
  }

  @keyframes line-wrap-flash {
    0%   { background: color-mix(in srgb, var(--accent-blue) 35%, transparent); }
    100% { background: transparent; }
  }

  .line-actions {
    position: absolute;
    top: 50%;
    left: 2px;
    transform: translateY(-50%);
    display: inline-flex;
    gap: 2px;
    opacity: 0;
    transition: opacity 0.1s;
    z-index: 1;
  }

  .line-wrap--commentable:hover .line-actions,
  .line-actions:focus-within {
    opacity: 1;
  }

  /* While the reviewer has an active multi-line selection, hide the
     per-line hover buttons. The floating toolbar near the selection
     carries the comment/ask actions; keeping the hover buttons
     visible makes them flash on every line the cursor passes while
     the selection is being dragged. */
  .diff-file--selecting .line-actions {
    display: none;
  }

  /* Floating toolbar anchored to the end of a multi-line selection.
     Fixed positioning relative to the document so it survives scroll
     inside the diff container. mousedown preventDefault inside the
     toolbar keeps the selection alive through the click. */
  .selection-toolbar {
    position: absolute;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.22);
    font-size: 11px;
    z-index: 10;
    user-select: none;
  }

  .selection-toolbar__label {
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  .selection-toolbar__btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    font-size: 11px;
    font-weight: 600;
    border-radius: var(--radius-sm);
    border: none;
    cursor: pointer;
    color: #fff;
  }

  .selection-toolbar__btn--comment {
    background: var(--accent-blue);
  }

  .selection-toolbar__btn--ask {
    background: var(--accent-claude);
  }

  .selection-toolbar__btn:hover {
    filter: brightness(1.1);
  }

  .add-comment-btn,
  .ask-ai-btn {
    width: 16px;
    height: 16px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: 3px;
    color: #fff;
    cursor: pointer;
    font-size: 11px;
    font-weight: 700;
    line-height: 1;
    padding: 0;
  }

  .add-comment-btn {
    background: var(--accent-blue);
  }

  .ask-ai-btn {
    background: var(--accent-claude);
  }

  .add-comment-btn:hover,
  .ask-ai-btn:hover {
    filter: brightness(1.1);
  }
</style>
