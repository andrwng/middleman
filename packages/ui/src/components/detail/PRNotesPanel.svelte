<script lang="ts">
  import { getStores } from "../../context.js";
  import { timeAgo } from "../../utils/time.js";

  const NOTES_PLACEHOLDER =
    "Private notes for this PR. Auto-saved, synced across devices.\n\nIdeas welcome: open questions, follow-up TODOs, things to bring up in review.";

  const { diff } = getStores();

  let open = $state(false);
  let textarea: HTMLTextAreaElement | undefined = $state();

  const pr = $derived(diff.getCurrentPR());
  const content = $derived(diff.getNotes());
  const updatedAt = $derived(diff.getNotesUpdatedAt());
  const saving = $derived(diff.isNotesSaving());
  const loaded = $derived(diff.isNotesLoaded());
  const errorMsg = $derived(diff.getNotesError());

  // Re-fetch on PR switch so a panel opened for PR #1 doesn't show
  // #1's notes after navigating to #2.
  $effect(() => {
    if (!pr) return;
    void diff.loadNotes();
  });

  // Flush any pending debounced save on unmount — covers the case
  // where the user closes the tab immediately after typing.
  $effect(() => {
    return () => {
      void diff.flushNotes();
    };
  });

  function isTypingTarget(el: EventTarget | null): boolean {
    if (!(el instanceof HTMLElement)) return false;
    const tag = el.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    return el.isContentEditable;
  }

  function handleKeydown(e: KeyboardEvent): void {
    // Don't fight with the textarea — let Escape close it, but all
    // other typing goes to the field.
    if (e.metaKey || e.ctrlKey || e.altKey) return;

    if (open && e.key === "Escape") {
      e.preventDefault();
      open = false;
      void diff.flushNotes();
      return;
    }

    if (e.key === "n" && !isTypingTarget(e.target)) {
      e.preventDefault();
      toggle();
    }
  }

  function toggle(): void {
    open = !open;
    if (open) {
      void diff.loadNotes();
      // Focus after the panel has rendered so the caret lands in the field.
      queueMicrotask(() => textarea?.focus());
    } else {
      void diff.flushNotes();
    }
  }

  function onInput(e: Event): void {
    const val = (e.currentTarget as HTMLTextAreaElement).value;
    diff.updateNotes(val);
  }

  function onBlur(): void {
    void diff.flushNotes();
  }

  $effect(() => {
    window.addEventListener("keydown", handleKeydown);
    return () => window.removeEventListener("keydown", handleKeydown);
  });

  const statusText = $derived.by<string>(() => {
    if (errorMsg) return `Save failed: ${errorMsg}`;
    if (saving) return "Saving…";
    if (updatedAt) return `Saved ${timeAgo(updatedAt)}`;
    if (loaded) return "No notes yet";
    return "Loading…";
  });
</script>

{#if pr}
  {#if open}
    <div class="notes-panel" role="dialog" aria-label="Reviewer notes">
      <div class="notes-panel__header">
        <span class="notes-panel__title">Notes</span>
        <span
          class="notes-panel__status"
          class:notes-panel__status--error={!!errorMsg}
        >
          {statusText}
        </span>
        <button
          class="notes-panel__close"
          onclick={toggle}
          title="Close (Esc or n)"
          aria-label="Close notes"
        >
          &times;
        </button>
      </div>
      <textarea
        bind:this={textarea}
        class="notes-panel__textarea"
        value={content}
        oninput={onInput}
        onblur={onBlur}
        placeholder={NOTES_PLACEHOLDER}
        spellcheck="true"
      ></textarea>
    </div>
  {:else}
    <button
      class="notes-toggle"
      onclick={toggle}
      title="Open reviewer notes (n)"
    >
      <span class="notes-toggle__icon" aria-hidden="true">&#9998;</span>
      <span>Notes</span>
      {#if content}
        <span class="notes-toggle__dot" aria-label="Notes present"></span>
      {/if}
    </button>
  {/if}
{/if}

<style>
  .notes-toggle {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 40;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    border-radius: 999px;
    border: 1px solid var(--border-default);
    background: var(--bg-surface);
    color: var(--text-secondary);
    font-size: 11px;
    cursor: pointer;
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);
  }

  .notes-toggle:hover {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .notes-toggle__icon {
    font-size: 12px;
    color: var(--accent-blue);
  }

  .notes-toggle__dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent-green);
  }

  .notes-panel {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 40;
    display: flex;
    flex-direction: column;
    width: min(420px, calc(100vw - 32px));
    height: min(360px, 50vh);
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
    overflow: hidden;
  }

  .notes-panel__header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 12px;
    border-bottom: 1px solid var(--border-muted);
    background: var(--bg-inset);
  }

  .notes-panel__title {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .notes-panel__status {
    font-size: 10px;
    color: var(--text-muted);
    margin-left: auto;
    font-family: var(--font-mono);
  }

  .notes-panel__status--error {
    color: var(--accent-red);
  }

  .notes-panel__close {
    width: 22px;
    height: 22px;
    border: none;
    background: none;
    color: var(--text-muted);
    font-size: 16px;
    line-height: 1;
    cursor: pointer;
    border-radius: var(--radius-sm);
  }

  .notes-panel__close:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .notes-panel__textarea {
    flex: 1;
    width: 100%;
    padding: 10px 12px;
    border: none;
    outline: none;
    resize: none;
    font-family: inherit;
    font-size: 13px;
    line-height: 1.5;
    color: var(--text-primary);
    background: var(--bg-surface);
  }

  .notes-panel__textarea::placeholder {
    color: var(--text-muted);
    font-style: italic;
    white-space: pre-line;
  }
</style>
