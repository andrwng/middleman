<script lang="ts">
  import { getStores } from "../../context.js";
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
  const { diff: diffStore, detail: detailStore } = getStores();

  let replying = $state(false);
  let replyBody = $state("");
  let replyEl: HTMLTextAreaElement | undefined = $state();

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

  const isRoot = $derived(comment.inReplyTo === 0);
  const isHidden = $derived(comment.isHidden === true);

  function onHideClick(): void {
    if (!isRoot) return;
    if (isHidden) {
      void detailStore.unhideReviewThread(comment.id);
    } else {
      void detailStore.hideReviewThread(comment.id);
    }
  }

  function startReply(): void {
    replying = true;
    replyBody = "";
    queueMicrotask(() => replyEl?.focus());
  }

  function cancelReply(): void {
    replying = false;
    replyBody = "";
  }

  function saveReply(): void {
    const body = replyBody.trim();
    if (body === "") return;
    // Anchor fields mirror the parent so the draft lands at the
    // same line-wrap visually. GitHub will re-resolve the anchor
    // server-side via the replies endpoint, so these are just for
    // the local render.
    const commits = diffStore.getCommits();
    const headSha = commits && commits.length > 0 ? commits[0]!.sha : comment.commitId;
    diffStore.addDraftComment({
      path: comment.path,
      line: comment.line,
      side: comment.side,
      ...(comment.startLine != null ? { startLine: comment.startLine } : {}),
      commitSha: headSha,
      body,
      inReplyTo: comment.id,
    });
    replying = false;
    replyBody = "";
  }

  function onReplyKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      cancelReply();
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      saveReply();
    }
  }
</script>

<div class="rc" class:rc--outdated={outdated} class:rc--reply={!!comment.inReplyTo} class:rc--hidden={isHidden}>
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
    {#if !replying}
      <button
        type="button"
        class="rc__action"
        onclick={startReply}
        title="Draft a reply"
        aria-label="Draft a reply"
      >
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
          <path d="M8 3L3 8l5 5" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M3 8h7a3 3 0 0 1 3 3v2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    {/if}
    {#if isRoot}
      <button
        type="button"
        class="rc__action rc__action--hide"
        onclick={onHideClick}
        title={isHidden ? "Show this thread again" : "Hide this thread from the review window"}
        aria-label={isHidden ? "Unhide thread" : "Hide thread"}
      >
        {#if isHidden}
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M2 8s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" stroke-linecap="round" stroke-linejoin="round"/>
            <circle cx="8" cy="8" r="2" />
          </svg>
        {:else}
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M3 3l10 10" stroke-linecap="round"/>
            <path d="M2 8s2.5-4 6-4c1.5 0 2.7.5 3.6 1.2M14 8s-2.5 4-6 4c-1.4 0-2.6-.4-3.5-1" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        {/if}
      </button>
    {/if}
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

  {#if replying}
    <div class="rc__reply-composer">
      <textarea
        bind:this={replyEl}
        class="rc__reply-input"
        bind:value={replyBody}
        onkeydown={onReplyKeydown}
        placeholder="Reply to {comment.author}…"
        rows="3"
      ></textarea>
      <div class="rc__reply-actions">
        <span class="rc__reply-hint">Reply stays as a draft until you publish your review · Ctrl-Enter to save · Esc to cancel</span>
        <button type="button" class="rc__btn" onclick={cancelReply}>Cancel</button>
        <button
          type="button"
          class="rc__btn rc__btn--primary"
          onclick={saveReply}
          disabled={replyBody.trim() === ""}
        >
          Save draft
        </button>
      </div>
    </div>
  {/if}
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

  .rc__action {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: var(--radius-sm);
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .rc__action:hover {
    background: var(--bg-surface-hover);
    color: var(--accent-blue);
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

  .rc__reply-composer {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px dashed var(--border-muted);
  }

  .rc__reply-input {
    width: 100%;
    min-height: 64px;
    padding: 6px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    resize: vertical;
    box-sizing: border-box;
  }

  .rc__reply-input:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .rc__reply-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 6px;
  }

  .rc__reply-hint {
    margin-right: auto;
    font-size: 10px;
    color: var(--text-muted);
  }

  .rc__btn {
    padding: 3px 10px;
    font-size: 11px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .rc__btn:hover {
    background: var(--bg-surface-hover);
  }

  .rc__btn--primary {
    background: var(--accent-blue);
    border-color: var(--accent-blue);
    color: #fff;
  }

  .rc__btn--primary:hover {
    background: color-mix(in srgb, var(--accent-blue) 90%, #000);
  }

  .rc__btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .rc--hidden {
    opacity: 0.55;
    border-left-color: var(--text-muted);
  }
</style>
