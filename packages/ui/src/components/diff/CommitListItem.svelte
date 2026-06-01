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
    onclick: (sha: string, shiftKey: boolean) => void;
  }

  const { commit, active, reviewed, onclick }: Props = $props();

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
  {#if commit.branch_heads && commit.branch_heads.length > 0}
    <span class="commit-item__branches" title={commit.branch_heads.join(", ")}>
      <svg class="commit-item__branch-ic" viewBox="0 0 16 16" aria-hidden="true">
        <path fill="currentColor" d="M9.5 3.25a2.25 2.25 0 1 1 3 2.122V6A2.5 2.5 0 0 1 10 8.5H6a1 1 0 0 0-1 1v1.128a2.251 2.251 0 1 1-1.5 0V5.372a2.25 2.25 0 1 1 1.5 0v1.836A2.493 2.493 0 0 1 6 7h4a1 1 0 0 0 1-1v-.628A2.25 2.25 0 0 1 9.5 3.25Zm-6 0a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Zm8.25-.75a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5ZM4.25 12a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z" />
      </svg>
      {#if commit.branch_heads.length > 1}
        <span class="commit-item__branch-count">{commit.branch_heads.length}</span>
      {/if}
    </span>
  {/if}
  <span class="commit-item__msg">{commit.message}</span>
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

  .commit-item__branches {
    display: inline-flex;
    align-items: center;
    gap: 1px;
    flex-shrink: 0;
    color: var(--text-muted);
  }

  .commit-item:hover .commit-item__branches,
  .commit-item--active .commit-item__branches {
    color: var(--accent-blue);
  }

  .commit-item__branch-ic {
    width: 11px;
    height: 11px;
    display: block;
  }

  .commit-item__branch-count {
    font-family: var(--font-mono);
    font-size: 9px;
    line-height: 1;
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

  /* .commit-item__msg already expands with flex:1, so pushing date
     to the right edge happens naturally. */

  .commit-item__date {
    font-size: 10px;
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>
