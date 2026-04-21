<script lang="ts">
  import { getStores } from "../../context.js";
  import ScopePill from "./ScopePill.svelte";
  import CommitListItem from "./CommitListItem.svelte";

  const { diff: diffStore } = getStores();

  let expanded = $state(false);

  const commits = $derived(diffStore.getCommits());
  const commitsLoading = $derived(diffStore.isCommitsLoading());
  const commitsError = $derived(diffStore.getCommitsError());
  const scope = $derived(diffStore.getScope());
  const commitIndex = $derived(diffStore.getCommitIndex());
  const reviewProgress = $derived(diffStore.getReviewProgress());

  // Reset expand state when selected PR changes so section doesn't stay open
  // with stale/empty commit list after PR switch.
  $effect(() => {
    diffStore.getCurrentPR();
    expanded = false;
  });

  // Auto-expand when user steps into commit mode (via [ / ] keys)
  $effect(() => {
    const s = scope;
    if (s.kind === "commit" || s.kind === "range") {
      if (!expanded) {
        expanded = true;
        void diffStore.loadCommits();
      }
    }
  });

  function toggle(): void {
    expanded = !expanded;
    if (expanded) {
      void diffStore.loadCommits();
    }
  }

  function isActive(sha: string): boolean {
    if (scope.kind === "commit") return scope.sha === sha;
    if (scope.kind === "range") {
      if (!commits) return false;
      const fromIdx = commits.findIndex((c) => c.sha === scope.fromSha);
      const toIdx = commits.findIndex((c) => c.sha === scope.toSha);
      const idx = commits.findIndex((c) => c.sha === sha);
      if (fromIdx === -1 || toIdx === -1 || idx === -1) return false;
      return idx >= toIdx && idx <= fromIdx;
    }
    return false;
  }

  function handleCommitClick(sha: string, shiftKey: boolean): void {
    if (shiftKey && scope.kind === "commit") {
      diffStore.selectRange(scope.sha, sha);
    } else {
      diffStore.selectCommit(sha);
    }
  }
</script>

<div class="commit-section">
  <div class="commit-section__header">
    <button class="commit-section__toggle" onclick={toggle}>
      <span class="commit-section__chevron" class:commit-section__chevron--open={expanded}>&#8250;</span>
      <span class="commit-section__label">Commits</span>
      {#if commits}
        <span class="commit-section__count">{commits.length}</span>
      {/if}
    </button>
    {#if reviewProgress && reviewProgress.total > 0}
      <span class="commit-section__progress">{reviewProgress.reviewed}/{reviewProgress.total}</span>
    {/if}
    <ScopePill {scope} onreset={diffStore.resetToHead} />
    {#if commits && commits.length > 0}
      <div class="commit-section__nav">
        {#if commitIndex}
          <span class="commit-section__pos">{commitIndex.current}/{commitIndex.total}</span>
        {/if}
        <button
          class="commit-section__nav-btn"
          onclick={() => diffStore.stepPrev()}
          title="Previous commit  ["
          disabled={commitIndex !== null && commitIndex.current === 1}
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5">
            <polyline points="6,2 3,5 6,8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
        <button
          class="commit-section__nav-btn"
          onclick={() => diffStore.stepNext()}
          title="Next commit  ]"
          disabled={scope.kind === "head"}
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.5">
            <polyline points="4,2 7,5 4,8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
    {/if}
  </div>

  {#if expanded}
    <div class="commit-section__body">
      {#if commitsLoading}
        <div class="commit-section__state">Loading...</div>
      {:else if commitsError}
        <div class="commit-section__state commit-section__state--error">{commitsError}</div>
      {:else if commits && commits.length > 0}
        {#each commits as commit (commit.sha)}
          <CommitListItem
            {commit}
            active={isActive(commit.sha)}
            reviewed={diffStore.isCommitReviewed(commit.sha)}
            onclick={handleCommitClick}
          />
        {/each}
      {:else if commits}
        <div class="commit-section__state">No commits</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .commit-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .commit-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }

  .commit-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    border-radius: var(--radius-sm);
  }

  .commit-section__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .commit-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }

  .commit-section__chevron--open {
    transform: rotate(90deg);
  }

  .commit-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .commit-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .commit-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }

  .commit-section__state {
    padding: 8px 22px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .commit-section__state--error {
    color: var(--accent-red);
  }

  .commit-section__nav {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
    flex-shrink: 0;
  }

  .commit-section__pos {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    margin-right: 4px;
  }

  .commit-section__nav-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border: none;
    border-radius: var(--radius-sm);
    background: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 0;
  }

  .commit-section__nav-btn:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .commit-section__nav-btn:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .commit-section__progress {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--accent-green);
    flex-shrink: 0;
  }
</style>
