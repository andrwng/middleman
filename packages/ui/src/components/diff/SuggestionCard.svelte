<script lang="ts">
  import { getStores } from "../../context.js";
  import type { DraftComment } from "../../stores/diff.svelte.js";
  import DraftChat from "./DraftChat.svelte";
  import { renderMarkdown } from "../../utils/markdown.js";

  interface Props {
    suggestion: DraftComment;
    // The PR head the reviewer is looking at; a suggestion written
    // against another commit is flagged as outdated.
    currentHeadSha: string;
  }

  const { suggestion, currentHeadSha }: Props = $props();
  const { diff: diffStore, draftChat } = getStores();

  let editing = $state(false);
  const chatOpen = $derived(draftChat.isOpen(suggestion.id));

  function toggleChat(): void {
    if (chatOpen) {
      draftChat.close(suggestion.id);
      return;
    }
    const pr = diffStore.getCurrentPR();
    if (pr) draftChat.open(pr, suggestion.id);
  }
  let draftBody = $state("");
  let textareaEl: HTMLTextAreaElement | undefined = $state();


  // Comments publish as GitHub Markdown; render them the way GitHub will,
  // with #123/@user links. Local worktrees have no GitHub to link to.
  const repoCtx = $derived.by(() => {
    const pr = diffStore.getCurrentPR();
    return pr && pr.owner !== "local" ? { owner: pr.owner, name: pr.name, sha: suggestion.commitSha } : undefined;
  });

  const outdated = $derived(currentHeadSha !== "" && suggestion.commitSha !== currentHeadSha);

  const anchorLabel = $derived.by(() => {
    const sign = suggestion.side === "LEFT" ? "−" : "+";
    if (suggestion.startLine != null && suggestion.startLine !== suggestion.line) {
      return `${sign}${suggestion.startLine}–${suggestion.line}`;
    }
    return `${sign}${suggestion.line}`;
  });

  function startEdit(): void {
    draftBody = suggestion.body;
    editing = true;
    queueMicrotask(() => {
      if (!textareaEl) return;
      textareaEl.focus();
      textareaEl.setSelectionRange(textareaEl.value.length, textareaEl.value.length);
    });
  }

  function saveEdit(): void {
    diffStore.updateSuggestionBody(suggestion.id, draftBody);
    editing = false;
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      editing = false;
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      saveEdit();
    }
  }
</script>

<div class="suggestion" class:suggestion--outdated={outdated}>
  <div class="suggestion__header">
    <span class="suggestion__badge">Suggestion</span>
    <span class="suggestion__from">from Claude</span>
    <span class="suggestion__anchor">{anchorLabel}</span>
    {#if outdated}
      <span
        class="suggestion__outdated"
        title={`Written against ${suggestion.commitSha.slice(0, 7)}; the PR has new commits`}
      >Outdated</span>
    {/if}
  </div>

  {#if editing}
    <textarea
      bind:this={textareaEl}
      bind:value={draftBody}
      class="suggestion__textarea"
      rows="4"
      onkeydown={onKeydown}
    ></textarea>
    <div class="suggestion__edit-actions">
      <span class="suggestion__hint">Ctrl-Enter to save · Esc to cancel</span>
      <button type="button" class="suggestion__btn" onclick={() => (editing = false)}>Cancel</button>
      <button type="button" class="suggestion__btn suggestion__btn--primary" onclick={saveEdit}>Save</button>
    </div>
  {:else}
    <div class="suggestion__body markdown-body">{@html renderMarkdown(suggestion.body, repoCtx)}</div>
  {/if}

  <!-- The chat starts with the rationale, so hide it here while open. -->
  {#if suggestion.rationale && !chatOpen}
    <div class="suggestion__rationale">
      <span class="suggestion__rationale-label">Claude</span>
      <div class="suggestion__rationale-text markdown-body">{@html renderMarkdown(suggestion.rationale, repoCtx)}</div>
    </div>
  {/if}

  {#if !editing}
    <div class="suggestion__actions">
      <button
        type="button"
        class="suggestion__btn suggestion__btn--primary"
        onclick={() => diffStore.acceptSuggestion(suggestion.id)}
      >Add to drafts</button>
      <button type="button" class="suggestion__btn" onclick={startEdit}>Edit</button>
      <button
        type="button"
        class="suggestion__btn"
        aria-expanded={chatOpen}
        onclick={toggleChat}
      >Ask Claude</button>
      <button
        type="button"
        class="suggestion__btn suggestion__btn--discard"
        onclick={() => diffStore.discardSuggestion(suggestion.id)}
      >Discard</button>
    </div>
  {/if}

  {#if chatOpen}
    <DraftChat draftId={suggestion.id} />
  {/if}
</div>

<style>
  .suggestion {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px dashed var(--accent-claude);
    border-left: 3px dashed var(--accent-claude);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-claude) 4%, var(--bg-surface));
  }

  .suggestion--outdated {
    border-color: var(--accent-amber);
  }

  .suggestion__header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
    font-size: 12px;
  }

  .suggestion__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .suggestion__from,
  .suggestion__anchor {
    color: var(--text-muted);
  }

  .suggestion__anchor {
    font-family: var(--font-mono);
  }

  .suggestion__outdated {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-amber);
    color: #000;
  }

  .suggestion__body {
    font-size: 13px;
    color: var(--text-primary);
    word-break: break-word;
    line-height: 1.5;
  }

  .suggestion__textarea {
    width: 100%;
    box-sizing: border-box;
    font: inherit;
    font-size: 13px;
    padding: 6px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .suggestion__rationale {
    margin-top: 8px;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    font-size: 12px;
  }

  .suggestion__rationale-label {
    font-weight: 600;
    color: var(--accent-claude);
  }

  .suggestion__rationale-text {
    margin: 2px 0 0;
    color: var(--text-muted);
    word-break: break-word;
  }

  .suggestion__actions,
  .suggestion__edit-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
  }

  .suggestion__hint {
    margin-right: auto;
    font-size: 11px;
    color: var(--text-muted);
  }

  .suggestion__btn {
    font-size: 12px;
    padding: 3px 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .suggestion__btn:hover {
    background: var(--bg-surface-hover);
  }

  .suggestion__btn--primary {
    border-color: var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
  }

  .suggestion__btn--primary:hover {
    background: color-mix(in srgb, var(--accent-claude) 85%, #000);
  }

  .suggestion__btn--discard:hover {
    color: var(--accent-red);
  }
</style>
