<script lang="ts">
  interface Anchor {
    line: number;
    side: "LEFT" | "RIGHT";
    startLine?: number;
  }

  interface Props {
    selectionPreview?: string;
    anchor?: Anchor;
    error?: string | null;
    submitting?: boolean;
    onsubmit: (question: string) => void;
    oncancel: () => void;
  }

  const {
    selectionPreview,
    anchor,
    error = null,
    submitting = false,
    onsubmit,
    oncancel,
  }: Props = $props();

  function anchorText(a: Anchor): string {
    const sign = a.side === "LEFT" ? "−" : "+";
    if (a.startLine != null && a.startLine !== a.line) {
      return `lines ${sign}${a.startLine}–${a.line}`;
    }
    return `line ${sign}${a.line}`;
  }

  let value = $state("");
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  $effect(() => {
    textareaEl?.focus();
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      const trimmed = value.trim();
      if (trimmed) onsubmit(trimmed);
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      oncancel();
    }
  }
</script>

<div class="ai-ask">
  <div class="ai-ask__header">
    <span class="ai-ask__badge">Ask Claude</span>
    {#if anchor}
      <span class="ai-ask__anchor">about {anchorText(anchor)}</span>
    {/if}
    {#if selectionPreview}
      <span class="ai-ask__selection" title={selectionPreview}>
        “{selectionPreview.length > 60
          ? selectionPreview.slice(0, 60) + "…"
          : selectionPreview}”
      </span>
    {/if}
  </div>
  <textarea
    bind:this={textareaEl}
    bind:value
    class="ai-ask__textarea"
    rows="3"
    placeholder="Ask a question about this code... (⌘/Ctrl+Enter to send, Esc to cancel)"
    onkeydown={onKeydown}
  ></textarea>
  {#if error}
    <div class="ai-ask__error">{error}</div>
  {/if}
  <div class="ai-ask__actions">
    <button
      type="button"
      class="ai-ask__btn ai-ask__btn--primary"
      disabled={!value.trim() || submitting}
      onclick={() => onsubmit(value.trim())}
    >
      {submitting ? "Asking..." : "Ask"}
    </button>
    <button type="button" class="ai-ask__btn" disabled={submitting} onclick={oncancel}>Cancel</button>
  </div>
</div>

<style>
  .ai-ask {
    margin: 4px 12px 8px 68px;
    padding: 8px 10px;
    border: 1px solid var(--accent-claude);
    border-left: 3px solid var(--accent-claude);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
  }

  .ai-ask__header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
  }

  .ai-ask__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .ai-ask__anchor {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--accent-claude);
    padding: 1px 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent-claude) 12%, var(--bg-inset));
  }

  .ai-ask__selection {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .ai-ask__textarea {
    width: 100%;
    font-family: var(--font-sans);
    font-size: 13px;
    padding: 6px 8px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .ai-ask__textarea:focus {
    outline: none;
    border-color: var(--accent-claude);
  }

  .ai-ask__actions {
    display: flex;
    gap: 6px;
    justify-content: flex-end;
    margin-top: 6px;
  }

  .ai-ask__btn {
    font-size: 12px;
    padding: 4px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
    cursor: pointer;
  }

  .ai-ask__btn:hover {
    background: var(--bg-surface-hover);
  }

  .ai-ask__btn--primary {
    border-color: var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
  }

  .ai-ask__btn--primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .ai-ask__btn--primary:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .ai-ask__btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .ai-ask__error {
    margin-top: 6px;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    color: var(--accent-red);
    font-size: 12px;
  }
</style>
