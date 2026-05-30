<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

  interface Props {
    thread: ReviewThread;
  }
  const { thread }: Props = $props();

  const { reviewThreads } = getStores();

  const comments = $derived(thread.comments ?? []);
  let reply = $state("");
  let sending = $state(false);

  async function sendReply(): Promise<void> {
    const text = reply.trim();
    if (!text || sending) return;
    sending = true;
    try {
      const ok = await reviewThreads.addComment(thread.id, text);
      if (ok) reply = "";
    } finally {
      sending = false;
    }
  }

  function onReplyKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void sendReply();
    }
  }
</script>

{#if thread.hidden}
  <div class="review-thread review-thread--hidden">
    <span class="review-thread__hidden-label">Hidden thread</span>
    <button
      type="button"
      class="review-thread__unhide"
      onclick={() => void reviewThreads.unhide(thread.id)}
    >Show</button>
  </div>
{:else}
  <div class="review-thread">
    <div class="review-thread__header">
      <span class="review-thread__badge">Review</span>
      <span class="review-thread__anchor">
        {thread.side === "LEFT" ? "−" : "+"}{thread.start_line != null &&
        thread.start_line !== thread.line
          ? `${thread.start_line}–${thread.line}`
          : thread.line}
      </span>
      <span class="review-thread__status">{thread.status}</span>
      <span class="review-thread__commit" title="Anchored to this commit">
        {thread.commit_sha.slice(0, 7)}
      </span>
      <button
        type="button"
        class="review-thread__action"
        title="Resolve this thread"
        onclick={() => void reviewThreads.resolve(thread.id)}
      >Resolve</button>
      <button
        type="button"
        class="review-thread__action"
        title="Hide this thread"
        onclick={() => void reviewThreads.hide(thread.id)}
      >Hide</button>
    </div>

    {#each comments as c (c.id)}
      <div class="review-thread__comment">
        <span class="review-thread__author review-thread__author--{c.author}">
          {c.author === "agent" ? "Claude" : "You"}
        </span>
        <div class="review-thread__body markdown-body">
          {@html renderMarkdown(c.body, undefined)}
        </div>
      </div>
    {/each}

    {#if thread.status !== "resolved"}
      <div class="review-thread__reply">
        <textarea
          bind:value={reply}
          class="review-thread__reply-input"
          placeholder="Reply... (⌘/Ctrl+Enter to send)"
          rows="2"
          onkeydown={onReplyKeydown}
        ></textarea>
        <button
          type="button"
          class="review-thread__send"
          disabled={sending || !reply.trim()}
          onclick={() => void sendReply()}
        >Send</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .review-thread {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--accent-claude);
    border-left: 3px solid var(--accent-claude);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-claude) 6%, var(--bg-surface));
  }

  .review-thread__header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
  }

  .review-thread__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .review-thread__anchor {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
  }

  .review-thread__commit {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    opacity: 0.8;
  }

  .review-thread__close {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    border: none;
    background: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .review-thread__close:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .review-thread__qa {
    margin-top: 6px;
    padding-top: 6px;
    border-top: 1px solid var(--border-muted);
  }

  .review-thread__qa:first-of-type {
    border-top: none;
    padding-top: 0;
    margin-top: 0;
  }

  .review-thread__question {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    font-size: 13px;
    color: var(--text-primary);
  }

  .review-thread__q-prefix {
    font-weight: 700;
    color: var(--accent-blue);
    font-family: var(--font-mono);
    font-size: 11px;
    padding-top: 1px;
  }

  .review-thread__q-body {
    flex: 1;
    min-width: 0;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .review-thread__cancel {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
  }

  .review-thread__cancel:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .review-thread__status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-top: 4px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .review-thread__status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
  }

  .review-thread__status--running .review-thread__status-dot,
  .review-thread__status--queued .review-thread__status-dot {
    background: var(--accent-amber);
    animation: review-thread-pulse 1.2s ease-in-out infinite;
  }

  .review-thread__status--done .review-thread__status-dot {
    background: var(--accent-green);
  }

  .review-thread__status--failed .review-thread__status-dot {
    background: var(--accent-red);
  }

  @keyframes review-thread-pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .review-thread__answer {
    margin-top: 6px;
    padding: 8px 10px;
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    font-size: 13px;
    color: var(--text-primary);
    line-height: 1.5;
  }

  .review-thread__answer-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 4px;
  }

  .review-thread__promote {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .review-thread__promote:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .review-thread__error {
    margin-top: 6px;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    color: var(--accent-red);
    font-size: 12px;
    white-space: pre-wrap;
  }

  .review-thread__followup {
    display: flex;
    gap: 6px;
    margin-top: 8px;
  }

  .review-thread__followup-input {
    flex: 1;
    font-family: var(--font-sans);
    font-size: 13px;
    padding: 6px 8px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .review-thread__followup-input:focus {
    outline: none;
    border-color: var(--accent-claude);
  }

  .review-thread__send {
    font-size: 12px;
    padding: 4px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
    cursor: pointer;
    align-self: flex-end;
  }

  .review-thread__send:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .review-thread__send:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .review-thread--hidden {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .review-thread__author {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    color: var(--text-muted);
  }
</style>
