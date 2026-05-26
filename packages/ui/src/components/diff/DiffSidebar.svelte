<script lang="ts">
  import type { DiffFile } from "../../api/types.js";
  import { getStores } from "../../context.js";
  import {
    isReviewNavCollapsed,
    toggleReviewNavCollapsed,
  } from "../../lib/uiState.svelte.js";
  import CommitListSection from "./CommitListSection.svelte";
  import QuestionsSection from "./QuestionsSection.svelte";
  import PendingCommentsSection from "./PendingCommentsSection.svelte";

  // Reusable file-tree + commit-list panel for the diff Files view.
  // Mounted by PRListView and PullDetail as the left pane of the
  // Files tab.
  const { diff, pulls, ai } = getStores();

  // Persisted collapse-to-rail state. When collapsed we render a
  // narrow vertical rail with a tiny counts label. The outer
  // <aside class="files-sidebar"> wrapper in PullDetail reads from
  // the same shared $state module so its width shrinks in lockstep.

  // Counts for the rail label so the collapsed sidebar still gives
  // a glanceable signal of "how much is in here right now".
  const commitCount = $derived((diff.getCommits() ?? []).length);
  const draftCount = $derived((diff.getDraft()?.comments ?? []).length);
  const questionCount = $derived(ai.all().questions.length);
  const fileCount = $derived(diff.getFileList()?.files?.length ?? 0);

  function filename(path: string): string {
    const i = path.lastIndexOf("/");
    return i >= 0 ? path.slice(i + 1) : path;
  }

  interface FileGroup { dir: string; files: DiffFile[] }

  function groupByDir(files: DiffFile[]): FileGroup[] {
    // Bucket consecutive same-dir files under a directory header. The
    // store sorts files by path before we see them, so this preserves
    // order and the buckets line up 1:1 with DiffView's render order.
    const map = new Map<string, DiffFile[]>();
    for (const f of files) {
      const i = f.path.lastIndexOf("/");
      const dir = i > 0 ? f.path.slice(0, i) : "";
      const bucket = map.get(dir);
      if (bucket) bucket.push(f);
      else map.set(dir, [f]);
    }
    const result: FileGroup[] = [];
    for (const [dir, dirFiles] of map) {
      result.push({ dir, files: dirFiles });
    }
    return result;
  }

  function statusLetter(s: string): string {
    switch (s) {
      case "modified": return "M";
      case "added": return "A";
      case "deleted": return "D";
      case "renamed": return "R";
      case "copied": return "C";
      default: return "?";
    }
  }

  function statusColor(s: string): string {
    switch (s) {
      case "modified": return "var(--accent-amber)";
      case "added": return "var(--accent-green)";
      case "deleted": return "var(--accent-red)";
      case "renamed":
      case "copied": return "var(--accent-blue)";
      default: return "var(--text-muted)";
    }
  }

  // Per-diff file filter input (shown when 10+ files in diff).
  let fileFilterText = $state("");
  // Reset filter whenever selected PR changes so stale filter text
  // does not silently hide files in the next PR.
  $effect(() => {
    pulls.getSelectedPR();
    fileFilterText = "";
  });
  const showFileFilter = $derived(
    (diff.getFileList()?.files.length ?? 0) >= 10,
  );
  const filteredDiffFiles = $derived.by(() => {
    const list = diff.getFileList();
    if (!list) return null;
    // Only apply filter when the filter UI is visible to avoid
    // silent hiding when the next PR has fewer files.
    if (!showFileFilter) return list.files;
    const q = fileFilterText.trim().toLowerCase();
    if (!q) return list.files;
    return list.files.filter((f) => f.path.toLowerCase().includes(q));
  });
</script>

{#if isReviewNavCollapsed()}
  <div class="diff-sidebar--rail">
    <button
      type="button"
      class="diff-sidebar__rail-nav"
      onclick={() => diff.stepNext()}
      disabled={diff.getScope().kind === "head"}
      aria-label="Next commit"
      title="Next commit  ]"
    >
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
           stroke="currentColor" stroke-width="1.5">
        <polyline points="4,2 7,5 4,8" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <button
      type="button"
      class="diff-sidebar__rail-body"
      onclick={toggleReviewNavCollapsed}
      aria-label="Expand review nav"
      title={`Expand review nav · ${commitCount} commits, ${draftCount} pending drafts, ${questionCount} AI questions, ${fileCount} files`}
    >
      <span class="diff-sidebar__rail-label">
        {commitCount}c &middot; {draftCount}d &middot; {questionCount}q &middot; {fileCount}f
      </span>
    </button>
    <button
      type="button"
      class="diff-sidebar__rail-nav"
      onclick={() => diff.stepPrev()}
      disabled={(diff.getCommitIndex()?.current ?? 0) === 1}
      aria-label="Previous commit"
      title="Previous commit  ["
    >
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
           stroke="currentColor" stroke-width="1.5">
        <polyline points="6,2 3,5 6,8" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
  </div>
{:else}
  <button
    type="button"
    class="diff-sidebar__collapse"
    onclick={toggleReviewNavCollapsed}
    aria-label="Collapse review nav"
    title="Collapse review nav"
  >
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
         stroke="currentColor" stroke-width="1.6">
      <polyline points="6.5,2 3.5,5 6.5,8" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>
  <CommitListSection />
  <PendingCommentsSection />
  <QuestionsSection />
  <div class="diff-files">
    {#if diff.isFileListLoading() && !diff.getFileList()}
      <div class="diff-files-state diff-files-state--loading">Loading files</div>
    {:else if filteredDiffFiles}
      {#if showFileFilter}
        <div class="diff-files-filter">
          <input
            type="text"
            class="diff-files-filter__input"
            placeholder="Filter files..."
            bind:value={fileFilterText}
          />
        </div>
      {/if}
      {@const progress = diff.getFileReviewProgress()}
      {#if progress && progress.total > 0}
        <div class="diff-files-progress" title="Files viewed in the current diff scope">
          <span class="diff-files-progress__count">{progress.reviewed}/{progress.total}</span>
          <span class="diff-files-progress__label">viewed</span>
        </div>
      {/if}
      {@const grouped = groupByDir(filteredDiffFiles)}
      {#each grouped as group, gi (gi)}
        {#if group.dir}
          <div class="diff-dir-header">{group.dir}/</div>
        {/if}
        {#each group.files as f (f.path)}
          <button
            class="diff-file-row"
            class:diff-file-row--active={diff.getActiveFile() === f.path}
            class:diff-file-row--nested={!!group.dir}
            class:diff-file-row--viewed={diff.isFileReviewed(f.path)}
            onclick={() => diff.requestScrollToFile(f.path)}
            title={f.path}
          >
            <span class="diff-file-status" style="color: {statusColor(f.status)}">{statusLetter(f.status)}</span>
            <span class="diff-file-name" class:diff-file-name--deleted={f.status === "deleted"}>{filename(f.path)}</span>
            {#if f.is_binary}
              <span class="diff-file-churn diff-file-churn--binary" title="Binary file">bin</span>
            {:else}
              <span class="diff-file-churn" title="+{f.additions} / -{f.deletions}">
                <span class="diff-file-add">+{f.additions}</span>
                <span class="diff-file-del">&minus;{f.deletions}</span>
              </span>
            {/if}
            {#if diff.isFileReviewed(f.path)}
              <span class="diff-file-check" aria-hidden="true">&check;</span>
            {/if}
          </button>
        {/each}
      {/each}
    {/if}
  </div>
{/if}

<style>
  .diff-files {
    border-bottom: 1px solid var(--border-muted);
    padding: 4px 0;
    overflow-y: auto;
  }

  .diff-files-filter {
    padding: 4px 10px 6px 24px;
  }

  .diff-files-filter__input {
    width: 100%;
    font-size: 11px;
    padding: 3px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
  }

  .diff-files-filter__input:focus {
    border-color: var(--accent-blue);
    outline: none;
  }

  .diff-files-state {
    padding: 6px 24px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .diff-files-state--loading {
    animation: pulse 1.5s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .diff-dir-header {
    padding: 5px 12px 2px 24px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .diff-file-row {
    display: flex;
    align-items: center;
    gap: 5px;
    width: 100%;
    padding: 2px 12px 2px 24px;
    text-align: left;
    color: var(--text-secondary);
    transition: background 0.15s ease;
  }

  .diff-file-row--nested {
    padding-left: 36px;
  }

  .diff-file-row:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .diff-file-row--active {
    background: color-mix(in srgb, var(--accent-blue) 10%, transparent);
    color: var(--text-primary);
  }

  .diff-file-status {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 700;
    width: 12px;
    flex-shrink: 0;
    text-align: center;
  }

  .diff-file-name {
    font-family: var(--font-mono);
    font-size: 11px;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .diff-file-name--deleted {
    text-decoration: line-through;
    opacity: 0.7;
  }

  .diff-files-progress {
    display: flex;
    align-items: baseline;
    gap: 4px;
    padding: 4px 12px 6px 24px;
    font-size: 10px;
    color: var(--text-muted);
  }

  .diff-files-progress__count {
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--accent-green);
  }

  .diff-file-row--viewed .diff-file-name {
    color: var(--text-muted);
  }

  .diff-file-check {
    color: var(--accent-green);
    font-size: 10px;
    flex-shrink: 0;
  }

  .diff-file-churn {
    margin-left: auto;
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
    font-family: var(--font-mono);
    font-size: 10px;
    flex-shrink: 0;
  }

  .diff-file-churn--binary {
    color: var(--accent-purple);
    font-weight: 600;
  }

  .diff-file-add {
    color: var(--diff-add-text, var(--accent-green));
  }

  .diff-file-del {
    color: var(--diff-del-text, var(--accent-red));
  }

  .diff-sidebar--rail {
    width: 30px;
    height: 100%;
    min-height: 200px;
    border: none;
    background: var(--bg-inset);
    color: var(--text-muted);
    display: flex;
    flex-direction: column;
    align-items: stretch;
  }

  .diff-sidebar__rail-nav {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    height: 24px;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .diff-sidebar__rail-nav:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .diff-sidebar__rail-nav:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .diff-sidebar__rail-body {
    flex: 1 1 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
    padding: 6px 0;
    min-height: 60px;
  }

  .diff-sidebar__rail-body:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .diff-sidebar__rail-label {
    writing-mode: vertical-rl;
    transform: rotate(180deg);
    text-orientation: mixed;
    font-size: 10px;
    white-space: nowrap;
  }

  .diff-sidebar__collapse {
    position: sticky;
    top: 0;
    margin-left: auto;
    margin-right: 4px;
    margin-top: 4px;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    z-index: 1;
  }

  .diff-sidebar__collapse:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }
</style>
