<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import type { AIThread, AIQuestion } from "../../stores/ai.svelte.js";
  import type { ReviewThreadDraftInput } from "../../stores/reviewThreads.svelte.js";
  import { extractFileRefs } from "../../stores/fileResolver.svelte.js";

  interface Props {
    thread: AIThread;
    repoOwner: string;
    repoName: string;
  }

  const { thread, repoOwner, repoName }: Props = $props();

  const { ai: aiStore, diff: diffStore, fileResolver, reviewThreads } = getStores();

  const questions = $derived(aiStore.getQuestionsForThread(thread.id));

  // Promote-to-review-thread is local-only (review threads exist only on
  // worktrees) and needs at least one answered turn to capture.
  const isLocal = $derived(repoOwner === "local");
  const answered = $derived(questions.filter((q) => q.status === "done" && !!q.answer));

  // Kick off filename resolution whenever a question's answer arrives,
  // so basenames Claude mentions get deep-linked when the server can
  // confirm the unique path at the thread's commit SHA.
  $effect(() => {
    if (!thread.commit_sha) return;
    const refs: string[] = [];
    for (const q of questions) {
      if (q.answer) refs.push(...extractFileRefs(q.answer));
    }
    if (refs.length === 0) return;
    void fileResolver.resolve(repoOwner, repoName, thread.commit_sha, refs);
  });

  const resolverVersion = $derived(
    thread.commit_sha ? fileResolver.getVersion(thread.commit_sha) : 0,
  );

  const repoCtx = $derived({
    owner: repoOwner,
    name: repoName,
    sha: thread.commit_sha,
    resolveBareFile: (basename: string) =>
      thread.commit_sha
        ? fileResolver.lookup(thread.commit_sha, basename)
        : undefined,
    cacheBust: String(resolverVersion),
  });
  let followUp = $state("");
  let sending = $state(false);

  async function sendFollowUp(): Promise<void> {
    const text = followUp.trim();
    if (!text || sending) return;
    sending = true;
    try {
      await aiStore.addFollowUp(thread.id, text);
      followUp = "";
    } finally {
      sending = false;
    }
  }

  async function removeThread(): Promise<void> {
    const ok = await aiStore.deleteThread(thread.id);
    if (!ok) {
      // deleteThread sets aiStore's errorMsg; surface it in a
      // quick alert so the reviewer knows the server rejected
      // the close instead of silently hiding the card.
      const msg = aiStore.getError() ?? "Close failed";
      alert(msg);
    }
  }

  async function cancelQuestion(q: AIQuestion): Promise<void> {
    const ok = await aiStore.deleteQuestion(thread.id, q.id);
    if (!ok) {
      alert(aiStore.getError() ?? "Cancel failed");
    }
  }

  // Promote the latest completed answer into a draft review comment
  // via the existing draft infrastructure. The Q&A thread stays
  // intact; only the answer text is copied into a new draft.
  function promoteToComment(q: AIQuestion): void {
    diffStore.addDraftComment({
      path: thread.path,
      line: thread.anchor_line,
      side: thread.anchor_side as "LEFT" | "RIGHT",
      commitSha: thread.commit_sha,
      body: q.answer,
    });
  }

  // Promote the whole Q&A session into one persisted review thread: the
  // first answered question becomes the root comment, then each answered
  // turn lands as agent/user comments. The thread is created 'discussed'
  // (it already carries agent input) and keeps its own Apply / Ask
  // buttons, so a code change can be made from it async. Once captured,
  // the ephemeral Q&A thread (and its worktree) is removed.
  async function promoteSession(): Promise<void> {
    if (answered.length === 0) return;
    const comments: { author: "user" | "agent"; body: string }[] = [
      { author: "agent", body: answered[0]!.answer },
    ];
    for (let i = 1; i < answered.length; i++) {
      comments.push({ author: "user", body: answered[i]!.question });
      comments.push({ author: "agent", body: answered[i]!.answer });
    }
    const draft: ReviewThreadDraftInput = {
      path: thread.path,
      side: thread.anchor_side as "LEFT" | "RIGHT",
      line: thread.anchor_line,
      ...(thread.hunk_start_line != null && thread.hunk_start_line < thread.anchor_line
        ? { startLine: thread.hunk_start_line }
        : {}),
      commitSha: thread.commit_sha,
      body: answered[0]!.question,
      comments,
    };
    const ok = await reviewThreads.createThreads([draft]);
    if (ok) await aiStore.deleteThread(thread.id);
  }

  function statusLabel(q: AIQuestion): string {
    switch (q.status) {
      case "queued":
        return "Queued";
      case "running":
        return "Thinking...";
      case "done":
        return "Answered";
      case "cancelled":
        return "Cancelled";
      case "failed":
        return "Failed";
      default:
        return q.status;
    }
  }

  function statusClass(q: AIQuestion): string {
    return `ai-thread__status ai-thread__status--${q.status}`;
  }

  function onFollowUpKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void sendFollowUp();
    }
  }
</script>

<div class="ai-thread">
  <div class="ai-thread__header">
    <span class="ai-thread__badge">Claude</span>
    <span class="ai-thread__anchor">
      {thread.anchor_side === "LEFT" ? "−" : "+"}{thread.hunk_start_line != null &&
      thread.hunk_end_line != null &&
      thread.hunk_start_line !== thread.hunk_end_line
        ? `${thread.hunk_start_line}–${thread.hunk_end_line}`
        : thread.anchor_line}
    </span>
    <span class="ai-thread__commit" title="Asked against this commit">
      {thread.commit_sha.slice(0, 7)}
    </span>
    <button
      type="button"
      class="ai-thread__close"
      onclick={removeThread}
      title="Close thread and remove worktree (cancels any in-flight question; never posted to the PR)"
      aria-label="Close thread and remove worktree"
    >
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
      </svg>
    </button>
  </div>

  {#each questions as q (q.id)}
    <div class="ai-thread__qa">
      <div class="ai-thread__question">
        <span class="ai-thread__q-prefix">Q:</span>
        <span class="ai-thread__q-body">{q.question}</span>
        {#if q.status === "queued" || q.status === "running"}
          <button
            type="button"
            class="ai-thread__cancel"
            onclick={() => cancelQuestion(q)}
            title="Cancel this question"
          >
            Cancel
          </button>
        {:else}
          <button
            type="button"
            class="ai-thread__cancel"
            onclick={() => cancelQuestion(q)}
            title="Remove this question from the thread"
          >
            Remove
          </button>
        {/if}
      </div>
      <div class={statusClass(q)}>
        <span class="ai-thread__status-dot"></span>
        {statusLabel(q)}
      </div>
      {#if q.status === "done" && q.answer}
        <div class="ai-thread__answer markdown-body">
          {@html renderMarkdown(q.answer, repoCtx)}
        </div>
        <div class="ai-thread__answer-actions">
          <button
            type="button"
            class="ai-thread__promote"
            onclick={() => promoteToComment(q)}
            title="Copy the answer into a draft review comment at this line (does not send this Q&A to the PR author)"
          >
            Promote to comment
          </button>
        </div>
      {:else if q.status === "failed" && q.error}
        <div class="ai-thread__error">{q.error}</div>
      {/if}
    </div>
  {/each}

  {#if isLocal && answered.length > 0}
    <div class="ai-thread__promote-session">
      <button
        type="button"
        class="ai-thread__promote"
        onclick={() => void promoteSession()}
        title="Create a review thread from this whole discussion, then close this Q&A"
      >
        Promote to review thread
      </button>
    </div>
  {/if}

  {#if thread.status === "active"}
    <div class="ai-thread__followup">
      <textarea
        bind:value={followUp}
        class="ai-thread__followup-input"
        placeholder="Ask a follow-up... (⌘/Ctrl+Enter to send)"
        rows="2"
        onkeydown={onFollowUpKeydown}
      ></textarea>
      <button
        type="button"
        class="ai-thread__send"
        disabled={sending || !followUp.trim()}
        onclick={() => void sendFollowUp()}
      >
        Send
      </button>
    </div>
  {/if}
</div>

<style>
  .ai-thread {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--accent-claude);
    border-left: 3px solid var(--accent-claude);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-claude) 6%, var(--bg-surface));
  }

  .ai-thread__header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
  }

  .ai-thread__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .ai-thread__anchor {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
  }

  .ai-thread__commit {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    opacity: 0.8;
  }

  .ai-thread__close {
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

  .ai-thread__close:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .ai-thread__qa {
    margin-top: 6px;
    padding-top: 6px;
    border-top: 1px solid var(--border-muted);
  }

  .ai-thread__qa:first-of-type {
    border-top: none;
    padding-top: 0;
    margin-top: 0;
  }

  .ai-thread__question {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    font-size: 13px;
    color: var(--text-primary);
  }

  .ai-thread__q-prefix {
    font-weight: 700;
    color: var(--accent-blue);
    font-family: var(--font-mono);
    font-size: 11px;
    padding-top: 1px;
  }

  .ai-thread__q-body {
    flex: 1;
    min-width: 0;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .ai-thread__cancel {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
  }

  .ai-thread__cancel:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .ai-thread__status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-top: 4px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .ai-thread__status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
  }

  .ai-thread__status--running .ai-thread__status-dot,
  .ai-thread__status--queued .ai-thread__status-dot {
    background: var(--accent-amber);
    animation: ai-thread-pulse 1.2s ease-in-out infinite;
  }

  .ai-thread__status--done .ai-thread__status-dot {
    background: var(--accent-green);
  }

  .ai-thread__status--failed .ai-thread__status-dot {
    background: var(--accent-red);
  }

  @keyframes ai-thread-pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .ai-thread__answer {
    margin-top: 6px;
    padding: 8px 10px;
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    font-size: 13px;
    color: var(--text-primary);
    line-height: 1.5;
  }

  .ai-thread__answer-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 4px;
  }

  .ai-thread__promote {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .ai-thread__promote:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .ai-thread__promote-session {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border-muted);
  }


  .ai-thread__error {
    margin-top: 6px;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    color: var(--accent-red);
    font-size: 12px;
    white-space: pre-wrap;
  }

  .ai-thread__followup {
    display: flex;
    gap: 6px;
    margin-top: 8px;
  }

  .ai-thread__followup-input {
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

  .ai-thread__followup-input:focus {
    outline: none;
    border-color: var(--accent-claude);
  }

  .ai-thread__send {
    font-size: 12px;
    padding: 4px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
    cursor: pointer;
    align-self: flex-end;
  }

  .ai-thread__send:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .ai-thread__send:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
