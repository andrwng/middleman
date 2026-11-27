<script lang="ts">
  import { getStores } from "../../context.js";
  import DiffViewMenu from "./DiffViewMenu.svelte";
  import ReviewWithClaudeForm from "./ReviewWithClaudeForm.svelte";

  interface Props {
    onReviewClick?: () => void;
    onRefsClick?: () => void;
    onBrowseClick?: () => void;
  }

  const { onReviewClick, onRefsClick, onBrowseClick }: Props = $props();

  const { diff, detail: detailStore, reviewThreads, aiReviewRun } = getStores();

  const canReviewWithClaude = $derived(diff.getCurrentPR() !== null);
  let claudeFormOpen = $state(false);
  const suggestionCount = $derived(diff.getSuggestions().length);

  const pendingCount = $derived(diff.getDraft().comments.length);
  const draftEvent = $derived(diff.getDraft().event);
  const hiddenCount = $derived(detailStore.getHiddenThreadCount());
  const showingHidden = $derived(detailStore.isShowingHiddenThreads());
  // Pulled fresh from the store so the toolbar reflects the
  // currently-selected PR. Empty for local worktrees (which have no
  // GitHub-side URL) — the link is rendered conditionally so it
  // doesn't navigate to "" / the current page.
  const githubURL = $derived(detailStore.getDetail()?.merge_request.URL ?? "");

  // On a local worktree there's no GitHub side, so the GitHub /sync that
  // diff.refresh() runs errors. Route Refresh there to a thread sync from
  // the DB instead (the diff itself is the worktree's live git state).
  const isLocal = $derived(diff.getCurrentPR()?.owner === "local");
  function onRefresh(): void {
    if (isLocal) void reviewThreads.refresh();
    else void diff.refresh();
  }

  // A symbol search numbers its hits against a SHA. getCurrentCommitSha()
  // returns "" when the current scope has no resolvable head (an
  // unresolved patchset pair, or a PR with no commits yet), and hits
  // found against no SHA cannot be resolved back to rendered lines --
  // so the button is disabled there rather than searching into nothing.
  const canSearchSymbols = $derived(diff.getCurrentCommitSha() !== "");
</script>

<div class="diff-toolbar">
  <DiffViewMenu />
  <div class="toolbar-group toolbar-group--right">
    {#if hiddenCount > 0}
      <button
        type="button"
        class="hidden-toggle"
        class:hidden-toggle--on={showingHidden}
        title={showingHidden ? "Hide these threads again" : "Show threads you've hidden"}
        onclick={() => detailStore.setShowHiddenThreads(!showingHidden)}
      >
        {showingHidden ? "Hide hidden" : "Show hidden"}
        <span class="hidden-toggle__count">{hiddenCount}</span>
      </button>
    {/if}
    <button
      type="button"
      class="refresh-btn refresh-btn--icon"
      aria-label="Refresh"
      onclick={onRefresh}
      disabled={diff.isRefreshing()}
      title={isLocal
        ? "Sync review threads from the database"
        : diff.isRefreshing()
          ? "Syncing with GitHub..."
          : "Refresh the diff and commits from GitHub"}
    >
      <svg
        width="12" height="12" viewBox="0 0 12 12" fill="none"
        stroke="currentColor" stroke-width="1.5" aria-hidden="true"
        class:refresh-btn__icon--spinning={diff.isRefreshing()}
      >
        <path d="M10 3A4 4 0 1 1 6 3M10 3V1M10 3H8" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <button
      type="button"
      class="refresh-btn refresh-btn--icon"
      aria-label="Refs"
      onclick={onRefsClick}
      disabled={!canSearchSymbols}
      title={canSearchSymbols
        ? "Find references to a symbol (s)"
        : "This diff scope has no resolvable commit to search"}
    >
      <svg
        width="12" height="12" viewBox="0 0 12 12" fill="none"
        stroke="currentColor" stroke-width="1.5" aria-hidden="true"
      >
        <circle cx="5" cy="5" r="3.5" />
        <path d="M7.6 7.6L11 11" stroke-linecap="round" />
      </svg>
    </button>
    <button
      type="button"
      class="refresh-btn refresh-btn--icon"
      aria-label="Browse"
      onclick={onBrowseClick}
      disabled={!canSearchSymbols}
      title={canSearchSymbols
        ? "Browse files at this commit (b)"
        : "This diff scope has no resolvable commit to browse"}
    >
      <svg
        width="12" height="12" viewBox="0 0 12 12" fill="none"
        stroke="currentColor" stroke-width="1.5" aria-hidden="true"
      >
        <path d="M2 2.5h8v7H2z" stroke-linejoin="round" />
        <path d="M2 4.5h8" />
      </svg>
    </button>
    {#if diff.getRefreshError()}
      <span class="refresh-error" title={diff.getRefreshError()}>
        {diff.getRefreshError()}
      </span>
    {/if}
    {#if githubURL}
      <a
        class="gh-link"
        href={githubURL}
        target="_blank"
        rel="noopener noreferrer"
        title="Open this PR on GitHub"
        aria-label="Open this PR on GitHub"
      >↗</a>
    {/if}
  </div>
  {#if canReviewWithClaude}
    <div class="toolbar-group claude-entry">
      {#if aiReviewRun.isActive()}
        <button
          type="button"
          class="claude-pill"
          onclick={() => aiReviewRun.openPanel()}
          title="Show the Claude review"
        >
          Claude is reviewing · {suggestionCount} suggestion{suggestionCount === 1 ? "" : "s"}
        </button>
        <button type="button" class="claude-pill__stop" onclick={() => void aiReviewRun.cancel()}>Stop</button>
      {:else}
        <button
          type="button"
          class="claude-btn"
          aria-expanded={claudeFormOpen}
          onclick={() => (claudeFormOpen = !claudeFormOpen)}
        >Review with Claude</button>
        {#if claudeFormOpen}
          <div class="claude-popover">
            <ReviewWithClaudeForm onstarted={() => (claudeFormOpen = false)} oncancel={() => (claudeFormOpen = false)} />
          </div>
        {/if}
      {/if}
    </div>
  {/if}
  {#if onReviewClick}
    <div class="toolbar-group">
      <button
        type="button"
        class="review-btn"
        class:review-btn--has-pending={pendingCount > 0}
        onclick={onReviewClick}
        title={pendingCount > 0
          ? `Finish review (${pendingCount} pending comment${pendingCount === 1 ? "" : "s"})`
          : "Finish review"}
      >
        Review
        {#if pendingCount > 0}
          <span class="review-btn__count">{pendingCount}</span>
        {/if}
        {#if draftEvent !== "COMMENT"}
          <span
            class="review-btn__event"
            class:review-btn__event--approve={draftEvent === "APPROVE"}
            class:review-btn__event--changes={draftEvent === "REQUEST_CHANGES"}
          >
            {draftEvent === "APPROVE" ? "✓" : "✗"}
          </span>
        {/if}
      </button>
    </div>
  {/if}
</div>

<style>
  .claude-btn {
    font-size: 12px;
    padding: 3px 10px;
    border: 1px solid var(--accent-claude);
    border-radius: var(--radius-sm);
    background: none;
    color: var(--accent-claude);
    cursor: pointer;
    white-space: nowrap;
  }

  .claude-btn:hover {
    background: color-mix(in srgb, var(--accent-claude) 10%, transparent);
  }

  /* Anchored to the toolbar, not to its button: once the toolbar wraps,
     the button can sit at the left of a line, and a form hung off it
     would run past the left edge of the diff column. */
  .claude-popover {
    position: absolute;
    top: calc(100% + 6px);
    right: 16px;
    z-index: 40;
    width: min(360px, calc(100% - 32px));
    padding: 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md, 6px);
    background: var(--bg-surface);
    box-shadow: 0 6px 18px rgba(0, 0, 0, 0.18);
  }

  .claude-pill {
    font-size: 12px;
    padding: 3px 10px;
    border: none;
    cursor: pointer;
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent-claude) 14%, var(--bg-surface));
    color: var(--accent-claude);
    white-space: nowrap;
  }

  .claude-pill__stop {
    font-size: 12px;
    padding: 2px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  /* Wraps rather than overflowing: .diff-main clips with overflow:
     hidden, so anything past its right edge would be out of reach.
     Every line packs to the right, so wrapped controls stay at the right
     edge; View alone keeps the left. It is also the Claude form's
     containing block. */
  .diff-toolbar {
    position: relative;
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    align-items: center;
    gap: 6px 20px;
    padding: 6px 16px;
    background: var(--diff-toolbar-bg);
    border-bottom: 1px solid var(--diff-border);
    flex-shrink: 0;
  }

  .toolbar-group {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .diff-toolbar > :global(.view-menu) {
    margin-right: auto;
  }

  .toolbar-group--right {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .refresh-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 3px 10px;
    font-size: 11px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .refresh-btn--icon {
    padding: 3px 6px;
  }

  .refresh-btn:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .refresh-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .refresh-btn__icon--spinning {
    animation: refresh-spin 0.9s linear infinite;
  }

  @keyframes refresh-spin {
    to { transform: rotate(360deg); }
  }

  .refresh-error {
    display: inline-block;
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 10px;
    color: var(--accent-red);
    font-style: italic;
  }

  /* Matches ActivityFeed's .link-btn: muted glyph that picks up
     accent-blue on hover. The arrow is the same ↗ the activity
     rows use so reviewers reach for the same affordance in both
     surfaces. */
  .gh-link {
    color: var(--text-muted);
    font-size: 13px;
    line-height: 1;
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    text-decoration: none;
  }
  .gh-link:hover {
    color: var(--accent-blue);
    background: var(--bg-surface-hover);
  }
  .gh-link:focus-visible {
    outline: 2px solid var(--accent-blue);
    outline-offset: 2px;
  }

  .review-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 12px;
    font-size: 12px;
    font-weight: 600;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
    cursor: pointer;
  }

  .review-btn:hover {
    background: var(--bg-surface-hover);
  }

  .review-btn--has-pending {
    border-color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 8%, var(--bg-inset));
  }

  .review-btn__count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 16px;
    padding: 0 5px;
    font-size: 10px;
    font-family: var(--font-mono);
    border-radius: 999px;
    background: var(--accent-amber);
    color: #000;
  }

  .review-btn__event {
    font-size: 11px;
  }

  .review-btn__event--approve {
    color: var(--accent-green);
  }

  .review-btn__event--changes {
    color: var(--accent-red);
  }

  .hidden-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    padding: 3px 10px;
    border-radius: 999px;
    color: var(--text-muted);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    cursor: pointer;
  }

  .hidden-toggle:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .hidden-toggle--on {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .hidden-toggle__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }
</style>
