<script lang="ts">
  import type { CommitInfo } from "../../api/types.js";
  import {
    localDateLabel,
    parseAPITimestamp,
  } from "../../utils/time.js";

  interface Props {
    commit: CommitInfo;
    active: boolean;
    reviewed: boolean;
    commentCount?: number;
    onclick: (sha: string, shiftKey: boolean) => void;
  }

  const { commit, active, reviewed, commentCount = 0, onclick }: Props = $props();

  function relativeDate(iso: string): string {
    const diff = Date.now() - parseAPITimestamp(iso).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return "now";
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    if (days < 30) return `${days}d ago`;
    return localDateLabel(iso);
  }

  function handleClick(e: MouseEvent): void {
    onclick(commit.sha, e.shiftKey);
  }
</script>

<button
  type="button"
  class="commit-item"
  class:commit-item--active={active}
  data-commit-sha={commit.sha}
  onclick={handleClick}
  title={commit.message}
>
  {#if reviewed}
    <span class="commit-item__reviewed" title="Reviewed">&check;</span>
  {/if}
  <span class="commit-item__sha">{commit.sha.slice(0, 7)}</span>
  <span class="commit-item__msg">{commit.message}</span>
  {#if commentCount > 0}
    <span class="commit-item__comments" title="{commentCount} review comment{commentCount === 1 ? '' : 's'}">
      <svg width="10" height="10" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
        <path d="M2.75 2.5h10.5c.966 0 1.75.784 1.75 1.75v6.5A1.75 1.75 0 0 1 13.25 12.5H9.78l-2.53 2.53a.75.75 0 0 1-1.28-.53v-2H2.75A1.75 1.75 0 0 1 1 10.75v-6.5C1 3.284 1.784 2.5 2.75 2.5z"/>
      </svg>
      <span>{commentCount}</span>
    </span>
  {/if}
  <span class="commit-item__date">{relativeDate(commit.authored_at)}</span>
</button>

<style>
  .commit-item {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 3px 10px 3px 12px;
    color: var(--text-secondary);
    font-size: 11px;
    line-height: 1.4;
    cursor: pointer;
    text-align: left;
    border: none;
    background: none;
  }

  .commit-item:hover {
    background: var(--bg-surface-hover);
  }

  .commit-item--active {
    background: var(--diff-sidebar-active);
    color: var(--text-primary);
  }

  .commit-item__sha {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    width: 52px;
    flex-shrink: 0;
  }

  .commit-item--active .commit-item__sha {
    color: var(--accent-blue);
  }

  .commit-item__msg {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  .commit-item__reviewed {
    font-size: 10px;
    color: var(--accent-green);
    flex-shrink: 0;
    width: 12px;
    text-align: center;
  }

  /* .commit-item__msg already expands with flex:1, so pushing
     date/comments to the right edge happens naturally. */

  .commit-item__comments {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
    font-size: 10px;
    color: var(--accent-purple);
  }

  .commit-item__date {
    font-size: 10px;
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>
