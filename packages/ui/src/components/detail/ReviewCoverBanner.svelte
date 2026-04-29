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
  }

  const { pr, owner, name }: Props = $props();

  // Cover-letter collapse state, persisted so the reviewer's preference
  // survives tab/PR switches.
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-cover-collapsed") === "true",
  );
  function toggle(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-cover-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }
</script>

<div class="review-cover" class:review-cover--collapsed={collapsed}>
  <button
    type="button"
    class="review-cover__toggle"
    onclick={toggle}
    title={collapsed ? "Expand description" : "Collapse description"}
  >
    <svg
      class="review-cover__chevron"
      class:review-cover__chevron--collapsed={collapsed}
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
  {#if !collapsed}
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
    /* Cap prose width for readability — past ~80ch, eye-tracking
       degrades. Matches GitHub/Gerrit/GitLab convention. */
    max-width: 80ch;
  }

  /* Fenced code blocks (commit excerpts, diagrams) escape the prose
     column constraint by scrolling horizontally, never wrapping —
     wrapped code is harder to read than scrolled code. */
  .review-cover__body :global(pre) {
    max-width: 100%;
    overflow-x: auto;
  }

  .review-cover__empty {
    padding: 2px 16px 12px 34px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
