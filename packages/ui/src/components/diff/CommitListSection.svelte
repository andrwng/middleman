<script lang="ts">
  import { getStores } from "../../context.js";
  import ScopePill from "./ScopePill.svelte";
  import CommitListItem from "./CommitListItem.svelte";

  const { diff: diffStore } = getStores();

  // Open by default — the commit list is the main way reviewers
  // navigate a PR, so it's more annoying to hide it than to show
  // an empty "Loading..." placeholder briefly on PR switch.
  let expanded = $state(true);

  const commits = $derived(diffStore.getCommits());
  const commitsLoading = $derived(diffStore.isCommitsLoading());
  const commitsError = $derived(diffStore.getCommitsError());
  const scope = $derived(diffStore.getScope());
  const commitIndex = $derived(diffStore.getCommitIndex());
  const reviewProgress = $derived(diffStore.getReviewProgress());

  let bodyEl: HTMLDivElement | undefined = $state();

  // Scroll the active commit into view whenever the selection changes.
  // Runs after the {#if expanded} branch mounts, so the first activation
  // via [ / ] (which auto-expands the section) still finds the element.
  $effect(() => {
    const s = scope;
    if (!expanded || !bodyEl) return;
    let sha: string | null = null;
    if (s.kind === "commit") sha = s.sha;
    else if (s.kind === "range") sha = s.toSha;
    else if (s.kind === "unreviewed" && commits) {
      // Scroll to the newest unreviewed commit so the first highlighted
      // row is in view.
      const newest = commits.find((c) => !diffStore.isCommitReviewed(c.sha));
      sha = newest?.sha ?? null;
    }
    if (!sha) return;
    requestAnimationFrame(() => {
      const el = bodyEl?.querySelector<HTMLElement>(
        `[data-commit-sha="${sha}"]`,
      );
      if (el) el.scrollIntoView({ block: "nearest" });
    });
  });

  // On PR switch, keep the section open and trigger a fresh
  // load. The body shows "Loading..." while the new commits
  // arrive so the reviewer sees the section expand-and-fill
  // rather than having to click to reveal it every time.
  $effect(() => {
    diffStore.getCurrentPR();
    expanded = true;
    void diffStore.loadCommits();
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
    if (scope.kind === "unreviewed") {
      // Highlight every commit that's part of the unreviewed range.
      return !diffStore.isCommitReviewed(sha);
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
      {#if reviewProgress && reviewProgress.total > 0}
        <span class="commit-section__progress">{reviewProgress.reviewed}/{reviewProgress.total}</span>
      {/if}
    </button>
    <ScopePill {scope} onreset={diffStore.resetToHead} />
    {#if commits && commits.length > 0}
      <div class="commit-section__nav">
        {#if commitIndex}
          <span class="commit-section__pos">{commitIndex.current}/{commitIndex.total}</span>
        {/if}
        <button
          class="commit-section__nav-btn commit-section__nav-btn--wide"
          onclick={() => diffStore.selectUnreviewed()}
          title="Diff everything not yet reviewed"
          disabled={!diffStore.hasUnreviewed() || scope.kind === "unreviewed"}
        >
          Unseen
        </button>
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
    <div class="commit-section__body" bind:this={bodyEl}>
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
    /* Without overflow:hidden a narrow sidebar can't compress the
       inner flex children enough, so label + count + progress
       overflow past the button's right edge and paint on top of
       the adjacent ScopePill / nav. */
    overflow: hidden;
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

  .commit-section__nav-btn--wide {
    width: auto;
    padding: 0 8px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.02em;
  }

  .commit-section__progress {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--accent-green);
    flex-shrink: 0;
  }
</style>
