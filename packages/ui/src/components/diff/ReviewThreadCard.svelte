<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

  interface Props {
    thread: ReviewThread;
  }
  const { thread }: Props = $props();

  const { reviewThreads, worktreeSession } = getStores();
  const busy = $derived(worktreeSession.hasRunningTurn());

  const comments = $derived(thread.comments ?? []);
  const agentReplies = $derived(comments.filter((c) => c.author === "agent").length);
  let reply = $state("");
  let sending = $state(false);
  let confirmingDelete = $state(false);
  const canApply = $derived(thread.status === "open" || thread.status === "discussed");

  // "asking…" feedback: a Discuss turn carries no message comment to
  // badge (unlike Ask Claude), so surface an in-thread indicator from the
  // moment Discuss is pressed until the agent's reply lands (or the turn
  // ends without one).
  let asking = $state(false);
  let askingBaseline = 0;
  let sawBusy = false;
  $effect(() => {
    if (!asking) {
      sawBusy = false;
      return;
    }
    if (busy) sawBusy = true;
    if (agentReplies > askingBaseline || (sawBusy && !busy)) {
      asking = false;
    }
  });

  async function onDelete(): Promise<void> {
    if (!confirmingDelete) {
      confirmingDelete = true;
      return;
    }
    confirmingDelete = false;
    await reviewThreads.deleteThread(thread.id);
  }

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

  async function askClaude(): Promise<void> {
    const text = reply.trim();
    if (!text || sending) return;
    sending = true;
    try {
      const ok = await reviewThreads.ask(thread.id, text);
      if (ok) reply = "";
    } finally {
      sending = false;
    }
  }

  // Empty-composer "Discuss": kick a read-only discuss turn on this
  // thread without a typed message (the agent responds to the thread).
  async function discussThread(): Promise<void> {
    if (sending) return;
    askingBaseline = agentReplies;
    asking = true;
    sending = true;
    try {
      const ok = await reviewThreads.discuss(thread.id);
      if (!ok) asking = false;
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
      {#if asking}
        <span class="review-thread__asking" title="Claude is responding…">asking…</span>
      {/if}
      <span class="review-thread__commit" title="Anchored to this commit">
        {thread.commit_sha.slice(0, 7)}
      </span>
      {#if canApply}
        <button
          type="button"
          class="review-thread__action"
          title={busy ? "Queue an apply turn for this thread" : "Apply this thread's change"}
          onclick={() => void reviewThreads.apply(thread.id)}
        >Apply</button>
      {/if}
      {#if thread.status === "resolved"}
        <button
          type="button"
          class="review-thread__action"
          title="Reopen this thread"
          onclick={() => void reviewThreads.unresolve(thread.id)}
        >Unresolve</button>
      {:else}
        <button
          type="button"
          class="review-thread__action"
          title="Resolve this thread"
          onclick={() => void reviewThreads.resolve(thread.id)}
        >Resolve</button>
      {/if}
      <button
        type="button"
        class="review-thread__action"
        title="Hide this thread"
        onclick={() => void reviewThreads.hide(thread.id)}
      >Hide</button>
      <button
        type="button"
        class="review-thread__action review-thread__action--delete"
        title="Delete this thread permanently"
        onclick={() => void onDelete()}
      >{confirmingDelete ? "Confirm?" : "Delete"}</button>
    </div>

    {#each comments as c (c.id)}
      <div class="review-thread__comment">
        <span class="review-thread__author review-thread__author--{c.author}">
          {c.author === "agent" ? "Claude" : "You"}
          {#if c.author === "user" && c.sent_to_agent}
            <span class="review-thread__sent-badge" title="Sent to Claude">asked</span>
          {/if}
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
        <button
          type="button"
          class="review-thread__send review-thread__ask"
          disabled={sending}
          title={busy
            ? (reply.trim() ? "Queue an Ask turn — the agent is busy" : "Queue a discuss turn — the agent is busy")
            : (reply.trim() ? "Reply and ask Claude to respond" : "Ask Claude to discuss this thread")}
          onclick={() => void (reply.trim() ? askClaude() : discussThread())}
        >{reply.trim() ? "Ask Claude" : "Discuss"}</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .review-thread {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--accent-blue);
    border-left: 3px solid var(--accent-blue);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-blue) 6%, var(--bg-surface));
  }

  .review-thread--hidden {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .review-thread__hidden-label {
    flex: 1;
  }

  .review-thread__unhide {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
  }

  .review-thread__unhide:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
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
    background: var(--accent-blue);
    color: #fff;
  }

  .review-thread__anchor {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
  }

  .review-thread__status {
    font-size: 11px;
    color: var(--text-muted);
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
  }

  .review-thread__commit {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    opacity: 0.8;
  }

  .review-thread__action {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
  }

  .review-thread__action:first-of-type {
    margin-left: auto;
  }

  .review-thread__action:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .review-thread__action--delete:hover {
    color: var(--accent-red);
    border-color: var(--accent-red);
  }

  .review-thread__comment {
    margin-top: 6px;
    padding-top: 6px;
    border-top: 1px solid var(--border-muted);
  }

  .review-thread__comment:first-of-type {
    border-top: none;
    padding-top: 0;
    margin-top: 0;
  }

  .review-thread__author {
    display: block;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 2px;
    color: var(--text-muted);
  }

  .review-thread__author--agent {
    color: var(--accent-blue);
  }

  .review-thread__author--user {
    color: var(--text-muted);
  }

  .review-thread__body {
    font-size: 13px;
    line-height: 1.5;
    color: var(--text-primary);
  }

  .review-thread__reply {
    display: flex;
    gap: 6px;
    margin-top: 8px;
  }

  .review-thread__reply-input {
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

  .review-thread__reply-input:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .review-thread__send {
    font-size: 12px;
    padding: 4px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--accent-blue);
    background: var(--accent-blue);
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

  .review-thread__ask {
    border-color: var(--accent-amber);
    background: var(--accent-amber);
  }

  .review-thread__sent-badge {
    display: inline-block;
    margin-left: 6px;
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0 5px;
    border-radius: 999px;
    color: var(--accent-amber);
    border: 1px solid var(--accent-amber);
  }

  .review-thread__asking {
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0 5px;
    border-radius: 999px;
    color: var(--accent-amber);
    border: 1px solid var(--accent-amber);
    animation: review-thread-asking-pulse 1.2s ease-in-out infinite;
  }

  @keyframes review-thread-asking-pulse {
    0%, 100% { opacity: 0.5; }
    50% { opacity: 1; }
  }
</style>
