<script lang="ts">
  import type { IssueEvent, PREvent } from "../../api/types.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import { timeAgo } from "../../utils/time.js";
  import { copyToClipboard } from "../../utils/clipboard.js";
  import { getStores } from "../../context.js";

  interface Props {
    events: Array<PREvent | IssueEvent>;
    repoOwner?: string;
    repoName?: string;
  }

  const { events, repoOwner, repoName }: Props = $props();
  const { detail: detailStore } = getStores();

  const typeLabels: Record<string, string> = {
    issue_comment: "Comment",
    review: "Review",
    commit: "Commit",
    force_push: "Force-pushed",
    review_comment: "Review Comment",
  };

  const typeColors: Record<string, string> = {
    issue_comment: "var(--accent-blue)",
    review: "var(--accent-purple)",
    review_comment: "var(--accent-purple)",
    commit: "var(--accent-green)",
    force_push: "var(--accent-red)",
  };

  function shouldRenderMarkdown(eventType: string): boolean {
    return eventType === "issue_comment" || eventType === "review" || eventType === "review_comment";
  }

  interface ReviewCommentMeta {
    path?: string;
    line?: number;
    start_line?: number;
    side?: string;
    in_reply_to?: number;
  }

  function parseReviewCommentMeta(metadataJSON: string): ReviewCommentMeta | null {
    if (!metadataJSON) return null;
    try {
      return JSON.parse(metadataJSON) as ReviewCommentMeta;
    } catch {
      return null;
    }
  }

  function reviewCommentLocation(meta: ReviewCommentMeta | null): string {
    if (!meta?.path) return "";
    if (meta.start_line && meta.line && meta.start_line !== meta.line) {
      return `${meta.path}:${meta.start_line}-${meta.line}`;
    }
    if (meta.line) return `${meta.path}:${meta.line}`;
    return meta.path;
  }

  // "Mechanics" = commit & force-push events. They're noisy relative
  // to actual discussion. Default collapsed; user can opt in.
  const MECHANICS_TYPES = new Set(["commit", "force_push"]);
  function isMechanics(eventType: string): boolean {
    return MECHANICS_TYPES.has(eventType);
  }

  let showMechanics = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("activity-show-mechanics") === "true",
  );
  function toggleMechanics(): void {
    showMechanics = !showMechanics;
    try {
      localStorage.setItem("activity-show-mechanics", String(showMechanics));
    } catch { /* ignore */ }
  }

  const mechanicsCount = $derived(events.filter((e) => isMechanics(e.EventType)).length);

  const hiddenSet = $derived(detailStore.getHiddenRootSet());
  const showingHidden = $derived(detailStore.isShowingHiddenThreads());
  const hiddenCount = $derived(detailStore.getHiddenThreadCount());

  function isReviewCommentInHiddenThread(event: PREvent | IssueEvent): boolean {
    if (event.EventType !== "review_comment") return false;
    if (event.PlatformID == null) return false;
    const root = detailStore.getReviewCommentRootForPlatformID(event.PlatformID as number);
    return hiddenSet.has(root);
  }

  const visibleEvents = $derived(
    events.filter((e) => {
      if (!showMechanics && isMechanics(e.EventType)) return false;
      if (!showingHidden && isReviewCommentInHiddenThread(e)) return false;
      return true;
    }),
  );

  // threadStarts[i] is the path that a review-comment thread begins at
  // index i of visibleEvents. Undefined means index i is either not a
  // review_comment or is a continuation of the previous file's thread.
  const threadStarts = $derived.by(() => {
    const starts = new Map<number, string>();
    let prevPath: string | null = null;
    let prevWasComment = false;
    visibleEvents.forEach((e, i) => {
      if (e.EventType !== "review_comment") {
        prevPath = null;
        prevWasComment = false;
        return;
      }
      const meta = parseReviewCommentMeta(e.MetadataJSON);
      const path = meta?.path ?? "";
      if (!prevWasComment || path !== prevPath) {
        starts.set(i, path);
      }
      prevPath = path;
      prevWasComment = true;
    });
    return starts;
  });

  let copiedId = $state<string | null>(null);
  let copyTimeout: ReturnType<typeof setTimeout> | null = null;

  function copyText(id: string, text: string): void {
    void copyToClipboard(text).then((ok) => {
      if (!ok) return;
      copiedId = id;
      if (copyTimeout !== null) clearTimeout(copyTimeout);
      copyTimeout = setTimeout(() => {
        copiedId = null;
        copyTimeout = null;
      }, 1500);
    });
  }
</script>

{#if mechanicsCount > 0 || hiddenCount > 0}
  <div class="timeline-toggles">
    {#if mechanicsCount > 0}
      <button
        type="button"
        class="toggle-pill"
        class:toggle-pill--on={showMechanics}
        onclick={toggleMechanics}
        title="Show or hide commit and force-push events"
      >
        {showMechanics ? "Hide" : "Show"} mechanics
        <span class="toggle-pill__count">{mechanicsCount}</span>
      </button>
    {/if}
    {#if hiddenCount > 0}
      <button
        type="button"
        class="toggle-pill hidden-toggle"
        class:toggle-pill--on={showingHidden}
        onclick={() => detailStore.setShowHiddenThreads(!showingHidden)}
        title={showingHidden ? "Hide these threads again" : "Show threads you've hidden"}
      >
        {showingHidden ? "Hide hidden" : "Show hidden"}
        <span class="toggle-pill__count">{hiddenCount}</span>
      </button>
    {/if}
  </div>
{/if}
{#if events.length === 0}
  <p class="empty">No activity yet</p>
{:else}
  <ol class="timeline">
    {#each visibleEvents as event, i (event.ID)}
      {#if threadStarts.has(i)}
        <li class="thread-header" aria-hidden="true">
          <span class="thread-header__dot"></span>
          <span class="thread-header__label">Comments on</span>
          <span class="thread-header__path">{threadStarts.get(i)}</span>
        </li>
      {/if}
      <li class="event" class:event--threaded={event.EventType === "review_comment"}>
        <div class="event-rail">
          <span
            class="dot"
            style="background: {typeColors[event.EventType] ?? 'var(--text-muted)'}"
          ></span>
          <span class="rail-line"></span>
        </div>
        <div class="event-card">
          <div class="event-header">
            <span
              class="event-type"
              style="color: {typeColors[event.EventType] ?? 'var(--text-muted)'}"
            >
              {typeLabels[event.EventType] ?? event.EventType}
            </span>
            {#if event.Author}
              <span class="event-author">{event.Author}</span>
            {/if}
            <span class="event-time">{timeAgo(event.CreatedAt)}</span>
          </div>
          {#if event.Summary && (event.EventType === "commit" || event.EventType === "force_push")}
            <p class="event-summary">{event.Summary}</p>
          {/if}
          {#if event.EventType === "review_comment"}
            {@const meta = parseReviewCommentMeta(event.MetadataJSON)}
            {@const location = reviewCommentLocation(meta)}
            {#if location}
              <p class="event-summary">
                {#if meta?.in_reply_to}
                  <span class="reply-indicator">Reply</span>
                {/if}
                {location}
              </p>
            {/if}
          {/if}
          {#if event.Body}
            <div class="event-body-wrap">
              <button
                class="copy-icon-btn"
                class:copied={copiedId === String(event.ID)}
                onclick={() => copyText(String(event.ID), event.Body)}
                title={copiedId === String(event.ID) ? "Copied!" : "Copy to clipboard"}
              >
                {#if copiedId === String(event.ID)}
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M13.78 4.22a.75.75 0 010 1.06l-7.25 7.25a.75.75 0 01-1.06 0L2.22 9.28a.75.75 0 011.06-1.06L6 10.94l6.72-6.72a.75.75 0 011.06 0z"/>
                  </svg>
                {:else}
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 010 1.5h-1.5a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-1.5a.75.75 0 011.5 0v1.5A1.75 1.75 0 019.25 16h-7.5A1.75 1.75 0 010 14.25v-7.5z"/>
                    <path d="M5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0114.25 11h-7.5A1.75 1.75 0 015 9.25v-7.5zm1.75-.25a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-7.5a.25.25 0 00-.25-.25h-7.5z"/>
                  </svg>
                {/if}
              </button>
              <div class="event-body {shouldRenderMarkdown(event.EventType) ? 'markdown-body' : ''}">
                {#if shouldRenderMarkdown(event.EventType)}
                  {@html renderMarkdown(event.Body, repoOwner && repoName ? { owner: repoOwner, name: repoName } : undefined)}
                {:else}
                  {event.Body}
                {/if}
              </div>
            </div>
          {/if}
        </div>
      </li>
    {/each}
  </ol>
{/if}

<style>
  .empty {
    font-size: 13px;
    color: var(--text-muted);
    padding: 16px 0;
  }

  .timeline-toggles {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    padding: 0 0 8px;
  }

  .toggle-pill {
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

  .toggle-pill:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .toggle-pill--on {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .toggle-pill__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }

  .thread-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 0 4px 0;
    font-size: 11px;
    color: var(--text-muted);
    list-style: none;
  }

  .thread-header__dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--accent-purple);
    opacity: 0.4;
    margin-left: 7px;
    flex-shrink: 0;
  }

  .thread-header__label {
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 600;
  }

  .thread-header__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
  }

  /* Slightly inset threaded events so the thread visually groups. */
  .event--threaded .event-card {
    margin-left: 16px;
  }

  .timeline {
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .event {
    display: flex;
    gap: 0;
  }

  /* Left rail: dot + connector line */
  .event-rail {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 24px;
    flex-shrink: 0;
    padding-top: 14px;
  }

  .dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
    z-index: 1;
    box-shadow: 0 0 0 3px var(--bg-primary);
  }

  .rail-line {
    width: 2px;
    flex: 1;
    background: var(--border-default);
    margin-top: 2px;
  }

  .event:last-child .rail-line {
    display: none;
  }

  /* Right side: card */
  .event-card {
    flex: 1;
    min-width: 0;
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-md);
    padding: 10px 12px;
    margin: 4px 0 4px 8px;
  }

  .event-header {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .event-type {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .event-author {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-primary);
  }

  .event-time {
    font-size: 11px;
    color: var(--text-muted);
    margin-left: auto;
  }

  .event-summary {
    font-size: 12px;
    color: var(--text-secondary);
    margin-top: 4px;
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .reply-indicator {
    display: inline-block;
    padding: 0 4px;
    margin-right: 4px;
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-muted);
    font-family: var(--font-primary, inherit);
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  /* Body wrap for copy button positioning */
  .event-body-wrap {
    position: relative;
    margin-top: 8px;
  }

  .copy-icon-btn {
    position: absolute;
    top: 6px;
    right: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
    opacity: 0;
    transition: opacity 0.15s, background 0.15s, color 0.15s;
    z-index: 1;
  }

  .event-body-wrap:hover .copy-icon-btn,
  .copy-icon-btn:focus-visible {
    opacity: 1;
  }

  .copy-icon-btn:hover {
    background: var(--bg-surface-hover);
    color: var(--text-secondary);
  }

  .copy-icon-btn:active {
    transform: scale(0.92);
  }

  .copy-icon-btn.copied {
    opacity: 1;
    color: var(--accent-green);
    background: color-mix(in srgb, var(--accent-green) 12%, transparent);
  }

  @media (hover: none) {
    .copy-icon-btn {
      opacity: 1;
    }
  }

  .event-body {
    font-size: 12px;
    color: var(--text-primary);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    padding: 8px 36px 8px 10px;
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.6;
  }

  .event-body.markdown-body {
    white-space: normal;
  }
</style>
