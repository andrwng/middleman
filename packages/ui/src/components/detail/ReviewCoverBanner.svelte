<script lang="ts">
  import type { components } from "../../api/generated/schema.js";
  import { renderMarkdown } from "../../utils/markdown.js";

  // MergeRequest is the inner DB type exposed via PullDetail.merge_request
  // — fewer fields than the list-response wrapper, but has everything the
  // banner needs (Title, Author, Number, Body).
  type MergeRequest = components["schemas"]["MergeRequest"];

  interface Props {
    pr: MergeRequest;
    owner: string;
    name: string;
    // Render the body regardless of the persisted `collapsed` flag.
    // Used by the consolidated top-sections "peek" flow so a previously
    // collapsed banner still reveals its body when the user clicks the
    // pip. Defaults to false so stacked banners honor localStorage.
    forceExpanded?: boolean;
  }

  const { pr, owner, name, forceExpanded = false }: Props = $props();

  // Cover-letter collapse state, persisted so the reviewer's preference
  // survives tab/PR switches.
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-cover-collapsed") === "true",
  );
  // Effective collapsed state: persisted collapse is overridden by the
  // peek-flow's forceExpanded prop. The chevron rotation, the wrapper
  // modifier, and the title text all must reflect what's actually
  // visible — otherwise a peeked banner shows a "collapsed" chevron
  // pointing the wrong way over an expanded body.
  const effectivelyCollapsed = $derived(collapsed && !forceExpanded);
  function toggle(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-cover-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }
</script>

<div class="review-cover" class:review-cover--collapsed={effectivelyCollapsed}>
  <button
    type="button"
    class="review-cover__toggle"
    onclick={toggle}
    title={effectivelyCollapsed ? "Expand description" : "Collapse description"}
  >
    <svg
      class="review-cover__chevron"
      class:review-cover__chevron--collapsed={effectivelyCollapsed}
      width="10" height="10" viewBox="0 0 10 10" fill="none"
      stroke="currentColor" stroke-width="1.6"
    >
      <polyline points="2,3.5 5,6.5 8,3.5" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
    <span class="review-cover__title">{pr.Title}</span>
    <span class="review-cover__meta">
      <span class="review-cover__author">{pr.Author}</span>
      <span class="review-cover__sep">&middot;</span>
      <span class="review-cover__num">#{pr.Number}</span>
    </span>
  </button>
  {#if !collapsed || forceExpanded}
    {#if pr.Body}
      <div class="review-cover__body markdown-body">
        {@html renderMarkdown(pr.Body, { owner, name })}
      </div>
    {:else}
      <div class="review-cover__empty">No description</div>
    {/if}
  {/if}
</div>

<style>
  .review-cover {
    display: flex;
    flex-direction: column;
    border-bottom: 1px solid var(--diff-border);
    background: var(--bg-inset);
    flex-shrink: 0;
  }

  .review-cover--collapsed {
    border-bottom-color: var(--border-muted);
  }

  .review-cover__toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 16px;
    width: 100%;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    color: var(--text-primary);
    font-size: 13px;
  }

  .review-cover__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .review-cover__chevron {
    flex-shrink: 0;
    transition: transform 0.15s;
    color: var(--text-muted);
  }

  .review-cover__chevron--collapsed {
    transform: rotate(-90deg);
  }

  .review-cover__title {
    font-weight: 600;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .review-cover__meta {
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
    font-size: 11px;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .review-cover__sep {
    color: var(--text-muted);
  }

  .review-cover__body {
    padding: 4px 16px 14px 34px;
    max-height: 30vh;
    overflow-y: auto;
    font-size: 13px;
    line-height: 1.55;
    color: var(--text-primary);
  }

  /* Cap readability on prose elements only; fenced code, tables,
     and log lines (which authors paste verbatim) keep full width
     because alignment and scanning matter more than line length
     for those. Past ~80ch prose loses eye-tracking; code doesn't. */
  .review-cover__body :global(p),
  .review-cover__body :global(ul),
  .review-cover__body :global(ol),
  .review-cover__body :global(blockquote),
  .review-cover__body :global(h1),
  .review-cover__body :global(h2),
  .review-cover__body :global(h3),
  .review-cover__body :global(h4),
  .review-cover__body :global(h5),
  .review-cover__body :global(h6) {
    max-width: 80ch;
  }

  .review-cover__body :global(pre) {
    overflow-x: auto;
  }

  .review-cover__empty {
    padding: 2px 16px 12px 34px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
