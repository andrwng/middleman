<script lang="ts">
  import { onMount } from "svelte";
  import type { DiffFile as DiffFileType, DiffHunk } from "../../api/types.js";
  import { getStores } from "../../context.js";

  const { diff: diffStore, ai: aiStore } = getStores();
  import { tokenizeLineDual, langFromPath, type DualToken } from "../../utils/highlight.js";
  import { pairHunk } from "../../utils/diffPairing.js";
  import DiffLineComponent from "./DiffLine.svelte";
  import CollapsedRegion from "./CollapsedRegion.svelte";
  import DiffComposer from "./DiffComposer.svelte";
  import PendingCommentCard from "./PendingCommentCard.svelte";
  import AIAskComposer from "./AIAskComposer.svelte";
  import AIThreadCard from "./AIThreadCard.svelte";
  import type { DraftComment } from "../../stores/diff.svelte.js";

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

  function openComposerFor(line: number, side: "LEFT" | "RIGHT"): void {
    openComposer = `${line}:${side}`;
  }

  function closeComposer(): void {
    openComposer = null;
  }

  function saveDraft(line: number, side: "LEFT" | "RIGHT", body: string): void {
    const commitSha = currentCommitSha();
    diffStore.addDraftComment({
      path: file.path,
      line,
      side,
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
    const selText = typeof window !== "undefined"
      ? (window.getSelection()?.toString() ?? "")
      : "";
    selectionSnapshot = selText.trim() || null;
    openAsk = `${line}:${side}`;
    askError = null;
  }

  function closeAsk(): void {
    openAsk = null;
    selectionSnapshot = null;
    askError = null;
    askSubmitting = false;
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

      const body: Parameters<typeof aiStore.createThread>[0] = {
        path: file.path,
        anchor_side: side,
        anchor_line: line,
        commit_sha: commitSha,
        question,
      };
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

<div class="diff-file" class:diff-file--viewed={viewed} data-file-path={file.path} bind:this={fileEl}>
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
    <label class="file-header__viewed" title={viewed ? "Mark as not viewed" : "Mark as viewed"}>
      <input
        type="checkbox"
        checked={viewed}
        onclick={toggleViewed}
      />
      <span>Viewed</span>
    </label>
  </div>
  {#if !collapsed}
    <div class="file-content">
      {#if renderedFile.is_binary}
        <div class="binary-notice">Binary file changed</div>
      {:else}
        <div class="file-rows" class:file-rows--split={layout === "split"}>
          {#each renderedFile.hunks as hunk, hunkIdx}
            {#if hunkIdx > 0}
              {@const gap = computeCollapsedLines(renderedFile.hunks, hunkIdx)}
              {#if gap > 0}
                <CollapsedRegion lineCount={gap} />
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
                      <div class="line-wrap" class:line-wrap--commentable={!!leftAnchor}>
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
                              onclick={() => openComposerFor(leftAnchor.line, leftAnchor.side)}
                              title="Add review comment"
                            >
                              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                              </svg>
                            </button>
                            <button
                              type="button"
                              class="ask-ai-btn"
                              onclick={() => openAskFor(leftAnchor.line, leftAnchor.side)}
                              title="Ask Claude about this line"
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
                      <div class="line-wrap" class:line-wrap--commentable={!!rightAnchor}>
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
                              onclick={() => openComposerFor(rightAnchor.line, rightAnchor.side)}
                              title="Add review comment"
                            >
                              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                              </svg>
                            </button>
                            <button
                              type="button"
                              class="ask-ai-btn"
                              onclick={() => openAskFor(rightAnchor.line, rightAnchor.side)}
                              title="Ask Claude about this line"
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
                    onsave={(body) => saveDraft(leftAnchor.line, leftAnchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if rightKey && openComposer === rightKey && rightAnchor}
                  <DiffComposer
                    onsave={(body) => saveDraft(rightAnchor.line, rightAnchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if leftKey && openAsk === leftKey && leftAnchor}
                  <AIAskComposer
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(leftAnchor.line, leftAnchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if rightKey && openAsk === rightKey && rightAnchor}
                  <AIAskComposer
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(rightAnchor.line, rightAnchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if leftKey}
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
                  {/if}
                {/if}
                {#if rightKey}
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
                <div class="line-wrap" class:line-wrap--commentable={!!anchor}>
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
                        onclick={() => openComposerFor(anchor.line, anchor.side)}
                        title="Add review comment"
                      >
                        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="2">
                          <path d="M5 2V8M2 5H8" stroke-linecap="round" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        class="ask-ai-btn"
                        onclick={() => openAskFor(anchor.line, anchor.side)}
                        title="Ask Claude about this line"
                      >
                        ?
                      </button>
                    </div>
                  {/if}
                </div>
                {#if anchorKey && openComposer === anchorKey && anchor}
                  <DiffComposer
                    onsave={(body) => saveDraft(anchor.line, anchor.side, body)}
                    oncancel={closeComposer}
                  />
                {/if}
                {#if anchorKey && openAsk === anchorKey && anchor}
                  <AIAskComposer
                    {...(selectionSnapshot ? { selectionPreview: selectionSnapshot } : {})}
                    error={askError}
                    submitting={askSubmitting}
                    onsubmit={(q) => void submitAsk(anchor.line, anchor.side, q)}
                    oncancel={closeAsk}
                  />
                {/if}
                {#if anchorKey}
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
                  {/if}
                {/if}
              {/each}
            {/if}
          {/each}
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
    min-width: 100%;
    width: max-content;
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
    background: var(--accent-purple);
  }

  .add-comment-btn:hover,
  .ask-ai-btn:hover {
    filter: brightness(1.1);
  }
</style>
