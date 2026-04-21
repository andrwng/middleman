<script lang="ts">
  import type { DraftComment } from "../../stores/diff.svelte.js";

  interface Props {
    comment: DraftComment;
    // The SHA the reviewer would publish against now. When it differs
    // from comment.commitSha, the anchor chip switches to a warning
    // colour to flag the reviewer that line numbers may have shifted
    // since the draft was written. Empty string means "don't know
    // yet" — render neutrally.
    currentHeadSha: string;
    ondelete: () => void;
  }

  const { comment, currentHeadSha, ondelete }: Props = $props();

  const drifted = $derived(
    currentHeadSha !== "" &&
      comment.commitSha !== "" &&
      comment.commitSha !== currentHeadSha,
  );

  const chipTitle = $derived(
    drifted
      ? `Drafted against ${comment.commitSha.slice(0, 7)}. Publish will use ${currentHeadSha.slice(0, 7)} as the review's base; GitHub may reject this comment if line numbers have shifted.`
      : `Drafted against ${comment.commitSha.slice(0, 7)}.`,
  );
</script>

<div class="pending" class:pending--drifted={drifted}>
  <div class="pending__header">
    <span class="pending__badge">Pending</span>
    {#if comment.commitSha}
      <span
        class="pending__commit"
        class:pending__commit--drifted={drifted}
        title={chipTitle}
      >
        @ {comment.commitSha.slice(0, 7)}
      </span>
    {/if}
    <span class="pending__anchor">
      {comment.side === "LEFT" ? "−" : "+"}{comment.startLine != null && comment.startLine !== comment.line
        ? `${comment.startLine}–${comment.line}`
        : comment.line}
    </span>
    <button type="button" class="pending__delete" onclick={ondelete} title="Delete pending comment">
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
      </svg>
    </button>
  </div>
  <div class="pending__body">{comment.body}</div>
</div>

<style>
  /* On-current: the draft is anchored to the PR head, so it should
     publish cleanly. Use the primary accent (blue) to read as
     "ready to go". Drifted: switch to amber to read as "warning —
     this may not resolve at publish time". */
  .pending {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--accent-blue);
    border-left: 3px solid var(--accent-blue);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-blue) 6%, var(--bg-surface));
  }

  .pending--drifted {
    border-color: var(--accent-amber);
    border-left-color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 8%, var(--bg-surface));
  }

  .pending__header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 4px;
  }

  .pending__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-blue);
    color: #fff;
  }

  .pending--drifted .pending__badge {
    background: var(--accent-amber);
    color: #000;
  }

  .pending__commit {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    cursor: help;
  }

  .pending__commit--drifted {
    color: var(--accent-amber);
    border-color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 12%, var(--bg-inset));
  }

  .pending__anchor {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
  }

  .pending__delete {
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

  .pending__delete:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .pending__body {
    font-size: 13px;
    color: var(--text-primary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
  }
</style>
