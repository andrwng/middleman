<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { getStores } from "../../context.js";
  import type { WorktreeDiffFile } from "../../stores/worktrees.svelte.js";
  import type { DiffFile } from "../../api/types.js";
  import DiffFileCore from "../diff/DiffFileCore.svelte";

  const { worktrees, diff: diffStore } = getStores();
  const layout = $derived(diffStore.getLayout());

  interface Props {
    worktreeId: number;
  }
  const { worktreeId }: Props = $props();

  // Defensive one-time list load. The sidebar usually primes the
  // worktree list, but on a hard refresh to /pulls/worktree/<id>
  // the detail can mount before that fetch completes. Done in
  // onMount rather than an $effect — the prior $effect read
  // isLoading()/getWorktrees() and called loadWorktrees() which
  // mutates them, driving Svelte into an
  // effect_update_depth_exceeded loop.
  onMount(() => {
    if (worktrees.getWorktrees().length === 0 && !worktrees.isLoading()) {
      void worktrees.loadWorktrees();
    }
  });

  // Per-id file fetch. Re-fires when worktreeId changes. The call
  // itself is untracked because loadChangedFiles reads
  // `changedFilesById[id]` before writing it — Svelte 5 tracks
  // reactive reads through function calls, so without untrack the
  // effect would observe the write to changedFilesById and re-run
  // until effect_update_depth_exceeded.
  $effect(() => {
    const id = worktreeId;
    untrack(() => {
      void worktrees.loadChangedFiles(id);
    });
  });

  const w = $derived(worktrees.getById(worktreeId));
  const listLoading = $derived(worktrees.isLoading());
  const listHasItems = $derived(worktrees.getWorktrees().length > 0);
  const entry = $derived(worktrees.getChangedFiles(worktreeId));
  const files = $derived(entry?.files ?? []);
  const loading = $derived(entry?.loading ?? false);
  const error = $derived(entry?.error ?? null);
  const fetchedAt = $derived(entry?.fetchedAt ?? 0);
  const base = $derived(entry?.base ?? null);

  // Lazy-loaded full diff (with hunks). Fetched the first time the
  // user expands any file in this worktree; subsequent expansions
  // reuse the cached payload.
  const diffEntry = $derived(worktrees.getDiff(worktreeId));
  const diffFilesByPath = $derived.by(() => {
    const map = new Map<string, WorktreeDiffFile>();
    for (const f of diffEntry?.files ?? []) {
      map.set(f.path, f);
    }
    return map;
  });
  let expandedPaths = $state<Set<string>>(new Set());

  function toggleExpanded(path: string): void {
    const next = new Set(expandedPaths);
    if (next.has(path)) {
      next.delete(path);
    } else {
      next.add(path);
      // First expand kicks off the diff fetch if not already cached.
      if (!diffEntry || diffEntry.fetchedAt === 0) {
        untrack(() => {
          void worktrees.loadWorktreeDiff(worktreeId);
        });
      }
    }
    expandedPaths = next;
  }
  const baseLabel = $derived(
    base === null
      ? ""
      : base.ref
        ? `vs ${base.ref}`
        : "vs HEAD (no base ref found)",
  );

  function statusColor(s: string): string {
    switch (s) {
      case "added": return "var(--accent-green)";
      case "modified": return "var(--accent-amber)";
      case "deleted": return "var(--accent-red)";
      case "renamed":
      case "copied": return "var(--accent-blue)";
      default: return "var(--text-muted)";
    }
  }

  function statusLetter(s: string): string {
    switch (s) {
      case "added": return "A";
      case "modified": return "M";
      case "deleted": return "D";
      case "renamed": return "R";
      case "copied": return "C";
      default: return "?";
    }
  }

  function fetchedRelative(ts: number): string {
    if (ts === 0) return "";
    const ageMs = Date.now() - ts;
    if (ageMs < 5_000) return "just now";
    if (ageMs < 60_000) return `${Math.floor(ageMs / 1000)}s ago`;
    return `${Math.floor(ageMs / 60_000)}m ago`;
  }
</script>

<div class="wt-detail">
  {#if !w && (listLoading || !listHasItems)}
    <div class="wt-detail__state">
      <p>Loading worktree…</p>
    </div>
  {:else if !w}
    <div class="wt-detail__state">
      <p>Worktree not found.</p>
    </div>
  {:else}
    <header class="wt-detail__header">
      <div class="wt-detail__title">
        <span class="wt-detail__repo">{w.repo_owner}/{w.repo_name}</span>
        <span class="wt-detail__branch">{w.branch || "(detached)"}</span>
      </div>
      <div class="wt-detail__path" title={w.path}>{w.path}</div>
      <div class="wt-detail__meta">
        {#if w.head_sha}
          <span class="wt-detail__meta-item" title="HEAD commit">
            <span class="wt-detail__meta-label">HEAD</span>
            <code>{w.head_sha.slice(0, 12)}</code>
          </span>
        {/if}
        {#if w.is_detached}<span class="wt-detail__chip">detached</span>{/if}
        {#if w.is_locked}<span class="wt-detail__chip wt-detail__chip--warn">locked</span>{/if}
        {#if w.is_prunable}<span class="wt-detail__chip wt-detail__chip--warn">prunable</span>{/if}
        <button
          class="wt-detail__refresh"
          onclick={() => void worktrees.loadChangedFiles(worktreeId)}
          title="Refresh changed files"
          disabled={loading}
        >
          {loading ? "Refreshing…" : "Refresh"}
        </button>
        {#if fetchedAt > 0}
          <span class="wt-detail__staleness" title={`Fetched at ${new Date(fetchedAt).toLocaleString()}`}>
            updated {fetchedRelative(fetchedAt)}
          </span>
        {/if}
      </div>
    </header>

    <section class="wt-detail__section">
      <div class="wt-detail__section-head">
        <h2 class="wt-detail__section-title">Changes</h2>
        {#if baseLabel}
          <span class="wt-detail__section-hint">{baseLabel}</span>
        {/if}
        {#if base && base.fallback}
          <span
            class="wt-detail__base-warn"
            title="No origin/main, origin/master, origin/develop, or origin/dev found — diff is computed against the worktree's own HEAD instead."
          >
            no base ref
          </span>
        {/if}
        <div class="wt-detail__layout-toggle">
          <button
            type="button"
            class="wt-detail__layout-btn"
            class:wt-detail__layout-btn--active={layout === "unified"}
            onclick={() => diffStore.setLayout("unified")}
            title="Unified diff"
          >Unified</button>
          <button
            type="button"
            class="wt-detail__layout-btn"
            class:wt-detail__layout-btn--active={layout === "split"}
            onclick={() => diffStore.setLayout("split")}
            title="Side-by-side diff"
          >Split</button>
        </div>
      </div>

      {#if loading && files.length === 0}
        <p class="wt-detail__state-msg">Loading…</p>
      {:else if error}
        <p class="wt-detail__state-msg wt-detail__state-msg--error">
          {error}
        </p>
      {:else if files.length === 0}
        <p class="wt-detail__state-msg">No changes vs base.</p>
      {:else}
        <ul class="wt-detail__files">
          {#each files as f (f.path)}
            {@const expanded = expandedPaths.has(f.path)}
            {@const diff = diffFilesByPath.get(f.path)}
            {@const canExpand = !f.is_binary && (f.additions > 0 || f.deletions > 0)}
            <li class="wt-detail__file-row">
              <button
                type="button"
                class="wt-detail__file"
                class:wt-detail__file--expanded={expanded}
                class:wt-detail__file--disabled={!canExpand}
                disabled={!canExpand}
                onclick={() => canExpand && toggleExpanded(f.path)}
                title={f.path}
              >
                <span class="wt-detail__file-chevron" class:wt-detail__file-chevron--open={expanded}>
                  {canExpand ? "▸" : ""}
                </span>
                <span class="wt-detail__file-status" style:color={statusColor(f.status)}>
                  {statusLetter(f.status)}
                </span>
                <span class="wt-detail__file-path">{f.path}</span>
                {#if f.is_binary}
                  <span class="wt-detail__file-churn wt-detail__file-churn--bin">bin</span>
                {:else}
                  <span class="wt-detail__file-churn">
                    <span class="wt-detail__file-add">+{f.additions}</span>
                    <span class="wt-detail__file-del">&minus;{f.deletions}</span>
                  </span>
                {/if}
              </button>
              {#if expanded}
                <div class="wt-detail__hunks">
                  {#if diffEntry?.loading && !diff}
                    <p class="wt-detail__hunks-msg">Loading diff…</p>
                  {:else if diffEntry?.error}
                    <p class="wt-detail__hunks-msg wt-detail__hunks-msg--error">
                      {diffEntry.error}
                    </p>
                  {:else if !diff || !diff.hunks || diff.hunks.length === 0}
                    <p class="wt-detail__hunks-msg">No diff content (untracked or no hunks).</p>
                  {:else}
                    <DiffFileCore file={diff as unknown as DiffFile} {layout} />
                  {/if}
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}
</div>

<style>
  .wt-detail {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    background: var(--bg-canvas);
  }

  .wt-detail__header {
    padding: 16px 20px;
    border-bottom: 1px solid var(--border-default);
    background: var(--bg-surface);
  }

  .wt-detail__title {
    display: flex;
    align-items: baseline;
    gap: 12px;
    margin-bottom: 4px;
  }

  .wt-detail__repo {
    font-size: 16px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .wt-detail__branch {
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--text-secondary);
  }

  .wt-detail__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
    margin-bottom: 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .wt-detail__meta {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    font-size: 11px;
    color: var(--text-muted);
  }

  .wt-detail__meta-item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .wt-detail__meta-label {
    text-transform: uppercase;
    letter-spacing: 0.05em;
    font-weight: 600;
  }

  .wt-detail__meta-item code {
    font-size: 11px;
    color: var(--text-secondary);
  }

  .wt-detail__chip {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--bg-inset);
    color: var(--text-muted);
  }

  .wt-detail__chip--warn {
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 10%, transparent);
  }

  .wt-detail__refresh {
    margin-left: auto;
    font-size: 11px;
    padding: 3px 10px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .wt-detail__refresh:hover:not(:disabled) {
    background: var(--bg-surface-hover);
  }

  .wt-detail__refresh:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .wt-detail__staleness {
    font-size: 10px;
    color: var(--text-muted);
    font-style: italic;
  }

  .wt-detail__section {
    padding: 16px 20px;
  }

  .wt-detail__section-head {
    display: flex;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 10px;
  }

  .wt-detail__section-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
    margin: 0;
  }

  .wt-detail__section-hint {
    font-size: 11px;
    color: var(--text-muted);
    font-family: var(--font-mono);
  }

  .wt-detail__base-warn {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent-amber);
    padding: 1px 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent-amber) 12%, transparent);
  }

  .wt-detail__state {
    padding: 24px 20px;
    color: var(--text-muted);
  }

  .wt-detail__state-msg {
    font-size: 13px;
    color: var(--text-muted);
    margin: 0;
    padding: 16px 0;
  }

  .wt-detail__state-msg--error {
    color: var(--accent-red);
  }

  .wt-detail__files {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .wt-detail__file-row {
    border-bottom: 1px solid var(--border-muted);
  }

  .wt-detail__file-row:last-child {
    border-bottom: none;
  }

  .wt-detail__file {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    font-size: 12px;
    background: var(--bg-surface);
    width: 100%;
    text-align: left;
    border: none;
    cursor: pointer;
    color: inherit;
  }

  .wt-detail__file:hover:not(.wt-detail__file--disabled) {
    background: var(--bg-surface-hover);
  }

  .wt-detail__file--expanded {
    background: color-mix(in srgb, var(--accent-blue) 8%, var(--bg-surface));
  }

  .wt-detail__file--disabled {
    cursor: default;
  }

  .wt-detail__file-chevron {
    font-size: 10px;
    color: var(--text-muted);
    transition: transform 120ms ease;
    width: 10px;
    display: inline-block;
    text-align: center;
  }

  .wt-detail__file-chevron--open {
    transform: rotate(90deg);
  }

  .wt-detail__hunks {
    border-top: 1px solid var(--border-muted);
  }

  .wt-detail__hunks-msg {
    padding: 8px 12px;
    color: var(--text-muted);
    margin: 0;
  }

  .wt-detail__hunks-msg--error {
    color: var(--accent-red);
  }

  .wt-detail__layout-toggle {
    display: inline-flex;
    margin-left: auto;
    background: var(--bg-inset);
    border-radius: var(--radius-sm);
    padding: 2px;
  }

  .wt-detail__layout-btn {
    font-size: 10px;
    font-weight: 600;
    padding: 2px 8px;
    border: none;
    border-radius: 3px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }

  .wt-detail__layout-btn--active {
    background: var(--bg-surface);
    color: var(--text-primary);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
  }

  .wt-detail__file-status {
    font-family: var(--font-mono);
    font-weight: 700;
    width: 14px;
    text-align: center;
  }

  .wt-detail__file-path {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .wt-detail__file-churn {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
    flex-shrink: 0;
    display: inline-flex;
    gap: 4px;
  }

  .wt-detail__file-churn--bin {
    color: var(--text-muted);
    font-style: italic;
  }

  .wt-detail__file-add {
    color: var(--accent-green);
  }

  .wt-detail__file-del {
    color: var(--accent-red);
  }
</style>
