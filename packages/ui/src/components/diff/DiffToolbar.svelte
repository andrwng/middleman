<script lang="ts">
  import { getStores } from "../../context.js";

  interface Props {
    onReviewClick?: () => void;
  }

  const { onReviewClick }: Props = $props();

  const { diff, detail: detailStore } = getStores();
  const tabOptions = [1, 2, 4, 8] as const;
  const layoutOptions = [
    { value: "unified", label: "Unified" },
    { value: "split", label: "Split" },
  ] as const;

  const pendingCount = $derived(diff.getDraft().comments.length);
  const draftEvent = $derived(diff.getDraft().event);
  const hiddenCount = $derived(detailStore.getHiddenThreadCount());
  const showingHidden = $derived(detailStore.isShowingHiddenThreads());
</script>

<div class="diff-toolbar">
  <div class="toolbar-group">
    <span class="toolbar-label">Layout</span>
    <div class="segmented-control">
      {#each layoutOptions as opt}
        <button
          class="segment"
          class:segment--active={diff.getLayout() === opt.value}
          onclick={() => diff.setLayout(opt.value)}
        >
          {opt.label}
        </button>
      {/each}
    </div>
  </div>
  <div class="toolbar-group">
    <span class="toolbar-label">Tab width</span>
    <div class="segmented-control">
      {#each tabOptions as opt}
        <button
          class="segment"
          class:segment--active={diff.getTabWidth() === opt}
          onclick={() => diff.setTabWidth(opt)}
        >
          {opt}
        </button>
      {/each}
    </div>
  </div>
  <div class="toolbar-group">
    <span class="toolbar-label">Hide whitespace</span>
    <button
      class="toggle-switch"
      class:toggle-switch--on={diff.getHideWhitespace()}
      role="switch"
      aria-checked={diff.getHideWhitespace()}
      title={diff.getHideWhitespace() ? "Show whitespace changes" : "Hide whitespace changes"}
      onclick={() => diff.setHideWhitespace(!diff.getHideWhitespace())}
    >
      <span class="toggle-knob"></span>
    </button>
  </div>
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
      class="refresh-btn"
      onclick={() => void diff.refresh()}
      disabled={diff.isRefreshing()}
      title={diff.isRefreshing()
        ? "Syncing with GitHub..."
        : "Refresh the diff and commits from GitHub"}
    >
      <svg
        width="12" height="12" viewBox="0 0 12 12" fill="none"
        stroke="currentColor" stroke-width="1.5"
        class:refresh-btn__icon--spinning={diff.isRefreshing()}
      >
        <path d="M10 3A4 4 0 1 1 6 3M10 3V1M10 3H8" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      Refresh
    </button>
    {#if diff.getRefreshError()}
      <span class="refresh-error" title={diff.getRefreshError()}>
        {diff.getRefreshError()}
      </span>
    {/if}
  </div>
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
  .diff-toolbar {
    display: flex;
    align-items: center;
    gap: 20px;
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

  .toolbar-label {
    font-size: 11px;
    color: var(--text-secondary);
    user-select: none;
    white-space: nowrap;
  }

  .segmented-control {
    display: flex;
    border: 1px solid var(--diff-border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .segment {
    font-size: 11px;
    font-family: var(--font-mono);
    padding: 2px 8px;
    color: var(--text-secondary);
    background: var(--diff-bg);
    border-right: 1px solid var(--diff-border);
    line-height: 18px;
  }

  .segment:last-child {
    border-right: none;
  }

  .segment:hover {
    background: var(--bg-surface-hover);
  }

  .segment--active {
    background: var(--accent-blue);
    color: #ffffff;
  }

  .segment--active:hover {
    background: var(--accent-blue);
  }

  .toolbar-group--right {
    margin-left: auto;
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

  .toggle-switch {
    position: relative;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--border-default);
    transition: background 0.2s;
    flex-shrink: 0;
  }

  .toggle-switch--on {
    background: var(--accent-blue);
  }

  .toggle-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #ffffff;
    transition: transform 0.2s;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
  }

  .toggle-switch--on .toggle-knob {
    transform: translateX(16px);
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
