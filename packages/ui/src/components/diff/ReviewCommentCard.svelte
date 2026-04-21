<script lang="ts">
  import { renderMarkdown } from "../../utils/markdown.js";
  import { timeAgo } from "../../utils/time.js";
  import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

  interface Props {
    comment: PublishedReviewComment;
    repoOwner: string;
    repoName: string;
    // currentHeadSha tells us whether this comment was made against
    // the commit we're reviewing against. When they differ the card
    // gets a small "outdated" hint so the reader knows line numbers
    // may have shifted.
    currentHeadSha: string;
  }

  const { comment, repoOwner, repoName, currentHeadSha }: Props = $props();

  const outdated = $derived(
    currentHeadSha !== "" && comment.commitId !== "" && comment.commitId !== currentHeadSha,
  );

  const anchorLabel = $derived.by(() => {
    const sign = comment.side === "LEFT" ? "−" : "+";
    if (comment.startLine != null && comment.startLine !== comment.line) {
      return `${sign}${comment.startLine}–${comment.line}`;
    }
    return `${sign}${comment.line}`;
  });
</script>

<div class="rc" class:rc--outdated={outdated} class:rc--reply={!!comment.inReplyTo}>
  <div class="rc__header">
    {#if comment.inReplyTo}
      <span class="rc__badge rc__badge--reply">Reply</span>
    {:else}
      <span class="rc__badge">Comment</span>
    {/if}
    <span class="rc__author">{comment.author}</span>
    {#if comment.commitId}
      <span class="rc__commit" title={outdated ? `Made against ${comment.commitId.slice(0, 7)} (not the current head)` : `Made against ${comment.commitId.slice(0, 7)}`}>
        @ {comment.commitId.slice(0, 7)}
      </span>
    {/if}
    <span class="rc__anchor">{anchorLabel}</span>
    {#if outdated}
      <span class="rc__outdated-pill" title="This comment was made against an older commit">outdated</span>
    {/if}
    <span class="rc__time">{timeAgo(comment.createdAt)}</span>
    {#if comment.htmlUrl}
      <a class="rc__link" href={comment.htmlUrl} target="_blank" rel="noopener noreferrer" title="Open on GitHub">
        <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M6 3H3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1v-3" stroke-linecap="round"/>
          <path d="M10 2h4v4" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="M8 8L14 2" stroke-linecap="round"/>
        </svg>
      </a>
    {/if}
  </div>
  <div class="rc__body markdown-body">
    {@html renderMarkdown(comment.body, { owner: repoOwner, name: repoName })}
  </div>
</div>

<style>
  .rc {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--border-muted);
    border-left: 3px solid var(--accent-purple);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
  }

  .rc--reply {
    margin-left: 96px;
  }

  .rc--outdated {
    border-left-color: var(--text-muted);
    opacity: 0.85;
  }

  .rc__header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 4px;
    flex-wrap: wrap;
  }

  .rc__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-purple);
    color: #fff;
  }

  .rc__badge--reply {
    background: color-mix(in srgb, var(--accent-purple) 55%, var(--bg-inset));
  }

  .rc__author {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .rc__commit {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    cursor: help;
  }

  .rc__anchor {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }

  .rc__outdated-pill {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent-amber) 16%, var(--bg-inset));
    color: var(--accent-amber);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 600;
    cursor: help;
  }

  .rc__time {
    font-size: 11px;
    color: var(--text-muted);
    margin-left: auto;
  }

  .rc__link {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
  }

  .rc__link:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .rc__body {
    font-size: 13px;
    color: var(--text-primary);
    line-height: 1.5;
  }
</style>
