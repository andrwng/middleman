<script lang="ts">
  import { getStores } from "../../context.js";

  // Pinned commit-message banner for the Review surface. Mirrors
  // ReviewCoverBanner's pattern: a chevron-toggleable header with a
  // collapsible body, persisted across PRs/sessions in localStorage.
  // Renders only when the diff scope is a single commit — that's the
  // only state where a commit-level subject/body is meaningful.
  //
  // Lives outside the diff scroll area so it stays visible while the
  // reviewer scrolls the diff.

  interface Props {
    number: number;
  }

  const { number }: Props = $props();

  const { diff: diffStore } = getStores();

  const scope = $derived(diffStore.getScope());
  const activeCommit = $derived(diffStore.getActiveCommit());
  const commitIndex = $derived(diffStore.getCommitIndex());
  const visible = $derived(
    scope.kind === "commit" && activeCommit !== null && commitIndex !== null,
  );

  let collapsed = $state(
    typeof localStorage !== "undefined" &&
      localStorage.getItem("pr-commit-msg-collapsed") === "true",
  );
  function toggle(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-commit-msg-collapsed", String(collapsed));
    } catch {
      /* ignore */
    }
  }
</script>

{#if visible && activeCommit && commitIndex}
  <div class="commit-banner" class:commit-banner--collapsed={collapsed}>
    <button
      type="button"
      class="commit-banner__toggle"
      onclick={toggle}
      title={collapsed ? "Expand commit message" : "Collapse commit message"}
    >
      <svg
        class="commit-banner__chevron"
        class:commit-banner__chevron--collapsed={collapsed}
        width="10" height="10" viewBox="0 0 10 10" fill="none"
        stroke="currentColor" stroke-width="1.6"
      >
        <polyline points="2,3.5 5,6.5 8,3.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <span class="commit-banner__crumb commit-banner__crumb--pr">PR #{number}</span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__crumb commit-banner__crumb--pos">
        Commit {commitIndex.current}/{commitIndex.total}
      </span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__crumb commit-banner__crumb--sha">
        {activeCommit.sha.slice(0, 7)}
      </span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__subject">{activeCommit.message}</span>
      <span class="commit-banner__author">{activeCommit.author_name}</span>
    </button>
    {#if !collapsed && activeCommit.body}
      <div class="commit-banner__body">{activeCommit.body}</div>
    {/if}
  </div>
{/if}

<style>
  .commit-banner {
    display: flex;
    flex-direction: column;
    border-bottom: 1px solid var(--diff-border);
    background: var(--bg-inset);
    flex-shrink: 0;
  }

  .commit-banner--collapsed {
    border-bottom-color: var(--border-muted);
  }

  .commit-banner__toggle {
    display: flex;
    align-items: baseline;
    gap: 6px;
    padding: 8px 16px;
    width: 100%;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    color: var(--text-muted);
    font-size: 11px;
    min-width: 0;
  }

  .commit-banner__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .commit-banner__chevron {
    flex-shrink: 0;
    transition: transform 0.15s;
    color: var(--text-muted);
    align-self: center;
  }

  .commit-banner__chevron--collapsed {
    transform: rotate(-90deg);
  }

  .commit-banner__crumb {
    min-width: 0;
    flex-shrink: 0;
  }

  .commit-banner__crumb--pr {
    font-weight: 600;
    color: var(--text-secondary);
  }

  .commit-banner__crumb--pos {
    font-weight: 600;
    color: var(--accent-blue);
  }

  .commit-banner__crumb--sha {
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .commit-banner__sep {
    color: var(--text-muted);
    font-size: 12px;
    user-select: none;
    flex-shrink: 0;
  }

  .commit-banner__subject {
    font-size: 13px;
    color: var(--text-primary);
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .commit-banner__author {
    margin-left: auto;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .commit-banner__body {
    padding: 4px 16px 14px 34px;
    max-height: 30vh;
    overflow-y: auto;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
  }
</style>
