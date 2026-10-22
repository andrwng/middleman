<script lang="ts">
  import { getStores } from "../../context.js";
  import type { DraftComment } from "../../stores/diff.svelte.js";

  interface Props {
    comment: DraftComment;
    // Retained for the tooltip so we can describe what the publish
    // path will do with this draft. Not used for drift detection
    // anymore — see below.
    currentHeadSha: string;
    ondelete: () => void;
  }

  const { comment, currentHeadSha, ondelete }: Props = $props();
  const { diff: diffStore } = getStores();

  let editing = $state(false);
  // draftBody is only meaningful while editing === true; startEdit()
  // seeds it from the current comment.body at entry time.
  let draftBody = $state("");
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  // When the sidebar asks us to open the editor (requestEditDraft),
  // enter edit mode and acknowledge the signal so re-opens work.
  $effect(() => {
    if (diffStore.getEditRequest() === comment.id) {
      startEdit();
      diffStore.ackEditRequest(comment.id);
    }
  });

  function startEdit(): void {
    draftBody = comment.body;
    editing = true;
    // Focus + caret-to-end after the textarea mounts.
    queueMicrotask(() => {
      if (!textareaEl) return;
      textareaEl.focus();
      textareaEl.setSelectionRange(textareaEl.value.length, textareaEl.value.length);
    });
  }

  function cancelEdit(): void {
    editing = false;
  }

  function saveEdit(): void {
    diffStore.updateDraftCommentBody(comment.id, draftBody);
    editing = false;
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      cancelEdit();
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      saveEdit();
    }
  }

  // Drift now means the draft's anchor commit isn't in the PR at
  // all (force-pushed away, or commit_sha was never captured). The
  // publish path posts each inline comment with its OWN commit_id,
  // so a draft anchored to an earlier still-present commit in the
  // PR series publishes fine — no reason to amber-flag it.
  const drifted = $derived.by(() => {
    if (comment.commitSha === "") return true;
    const commits = diffStore.getCommits();
    if (!commits || commits.length === 0) {
      // Commits haven't loaded; don't flag prematurely. Fall back to
      // the head comparison if we know it.
      return currentHeadSha !== "" && comment.commitSha !== currentHeadSha;
    }
    return !commits.some((c) => c.sha === comment.commitSha);
  });

  const chipTitle = $derived.by(() => {
    if (!comment.commitSha) {
      return "Anchor commit unknown (drafted before the commit list loaded). Delete and redraft, or the publish may fail.";
    }
    if (drifted) {
      return `Anchor commit ${comment.commitSha.slice(0, 7)} is no longer in this PR (force-pushed away?). This comment will be rejected at publish.`;
    }
    return `Drafted against ${comment.commitSha.slice(0, 7)} — posts with this commit_id.`;
  });
</script>

<div class="pending" class:pending--drifted={drifted} class:pending--editing={editing} class:pending--reply={!!comment.inReplyTo}>
  <div class="pending__header">
    {#if comment.inReplyTo}
      <span class="pending__badge pending__badge--reply" title="Draft reply to an existing comment — will thread under the parent on publish">Pending reply</span>
    {:else}
      <span class="pending__badge">Pending</span>
    {/if}
    {#if comment.origin === "claude"}
      <span class="pending__from-claude" title="Started as a Claude suggestion">Claude</span>
    {/if}
    {#if !comment.inReplyTo}
      <span
        class="pending__commit"
        class:pending__commit--drifted={drifted}
        title={chipTitle}
      >
        @ {comment.commitSha ? comment.commitSha.slice(0, 7) : "???"}
      </span>
    {/if}
    <span class="pending__anchor">
      {comment.side === "LEFT" ? "−" : "+"}{comment.startLine != null && comment.startLine !== comment.line
        ? `${comment.startLine}–${comment.line}`
        : comment.line}
    </span>
    {#if !editing}
      <button
        type="button"
        class="pending__action"
        onclick={startEdit}
        title="Edit pending comment"
        aria-label="Edit pending comment"
      >
        <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M8.5 1.5l2 2L4 10H2v-2L8.5 1.5z" stroke-linejoin="round" />
        </svg>
      </button>
    {/if}
    <button
      type="button"
      class="pending__action pending__action--delete"
      onclick={ondelete}
      title="Delete pending comment"
      aria-label="Delete pending comment"
    >
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
      </svg>
    </button>
  </div>

  {#if editing}
    <textarea
      bind:this={textareaEl}
      class="pending__textarea"
      bind:value={draftBody}
      onkeydown={onKeydown}
      placeholder="Edit comment…"
    ></textarea>
    <div class="pending__edit-actions">
      <span class="pending__hint">Ctrl-Enter to save · Esc to cancel</span>
      <button type="button" class="pending__btn" onclick={cancelEdit}>Cancel</button>
      <button
        type="button"
        class="pending__btn pending__btn--primary"
        onclick={saveEdit}
        disabled={draftBody.trim() === ""}
      >
        Save
      </button>
    </div>
  {:else}
    <!-- svelte-ignore a11y_no_static_element_interactions, a11y_click_events_have_key_events -->
    <div
      class="pending__body"
      ondblclick={startEdit}
      title="Double-click to edit"
    >{comment.body}</div>
  {/if}
  {#if comment.syncError}
    <p class="pending__submit-error" role="alert">
      Not saved to the server: {comment.syncError}. Kept here; it is saved again when you publish.
    </p>
  {:else if comment.submitError}
    <p class="pending__submit-error" role="alert">{comment.submitError}</p>
  {/if}
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

  .pending__from-claude {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--accent-claude);
    color: var(--accent-claude);
  }

  .pending__badge--reply {
    background: color-mix(in srgb, var(--accent-blue) 55%, var(--bg-inset));
  }

  .pending--reply {
    margin-left: 96px;
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

  .pending__action {
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

  .pending__action:first-of-type {
    margin-left: auto;
  }

  .pending__action:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .pending__action--delete:hover {
    color: var(--accent-red);
  }

  .pending__submit-error {
    margin: 6px 0 0;
    padding: 6px 8px;
    font-size: 12px;
    color: var(--accent-red);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    border: 1px solid var(--accent-red);
    border-radius: var(--radius-sm);
    word-break: break-word;
  }

  .pending__body {
    font-size: 13px;
    color: var(--text-primary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
    cursor: text;
  }

  .pending__textarea {
    width: 100%;
    min-height: 72px;
    padding: 6px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    resize: vertical;
    box-sizing: border-box;
  }

  .pending__textarea:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .pending__edit-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 6px;
  }

  .pending__hint {
    margin-right: auto;
    font-size: 10px;
    color: var(--text-muted);
  }

  .pending__btn {
    padding: 3px 10px;
    font-size: 11px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .pending__btn:hover {
    background: var(--bg-surface-hover);
  }

  .pending__btn--primary {
    background: var(--accent-blue);
    border-color: var(--accent-blue);
    color: #fff;
  }

  .pending__btn--primary:hover {
    background: color-mix(in srgb, var(--accent-blue) 90%, #000);
  }

  .pending__btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
