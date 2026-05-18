<script lang="ts">
  import { getStores } from "../../context.js";

  const { worktrees } = getStores();

  interface Props {
    worktreeId: number;
  }
  const { worktreeId }: Props = $props();

  // Load the worktree list and the per-id file change list on mount.
  // The sidebar normally primes the list, but on a hard refresh to
  // /pulls/worktree/<id> the WorktreeDetail can mount before that
  // fetch completes — without a defensive call here we'd flash a
  // "not found" message until the list arrives.
  $effect(() => {
    if (worktrees.getWorktrees().length === 0 && !worktrees.isLoading()) {
      void worktrees.loadWorktrees();
    }
    void worktrees.loadChangedFiles(worktreeId);
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
            <li class="wt-detail__file" title={f.path}>
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

  .wt-detail__file {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    font-size: 12px;
    background: var(--bg-surface);
    border-bottom: 1px solid var(--border-muted);
  }

  .wt-detail__file:last-child {
    border-bottom: none;
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
