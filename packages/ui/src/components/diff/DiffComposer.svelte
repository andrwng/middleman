<script lang="ts">
  interface Anchor {
    line: number;
    side: "LEFT" | "RIGHT";
    startLine?: number;
  }

  interface Props {
    initialValue?: string;
    anchor?: Anchor;
    onsave: (body: string) => void;
    oncancel: () => void;
  }

  const { initialValue = "", anchor, onsave, oncancel }: Props = $props();

  function anchorLabel(a: Anchor): string {
    const sign = a.side === "LEFT" ? "−" : "+";
    if (a.startLine != null && a.startLine !== a.line) {
      return `${sign}${a.startLine}–${a.line}`;
    }
    return `${sign}${a.line}`;
  }

  // svelte-ignore state_referenced_locally
  let value = $state(initialValue);
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  // Focus the textarea as soon as it mounts so the user can type
  // without an extra click.
  $effect(() => {
    textareaEl?.focus();
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      if (value.trim()) onsave(value);
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      oncancel();
      return;
    }
  }
</script>

<div class="composer">
  {#if anchor}
    <div class="composer__anchor">
      <span class="composer__anchor-label">
        {anchor.startLine != null && anchor.startLine !== anchor.line
          ? `Commenting on lines ${anchorLabel(anchor)}`
          : `Commenting on line ${anchorLabel(anchor)}`}
      </span>
    </div>
  {/if}
  <textarea
    bind:this={textareaEl}
    bind:value
    class="composer__textarea"
    placeholder="Leave a review comment... (⌘/Ctrl+Enter to save, Esc to cancel)"
    rows="3"
    onkeydown={onKeydown}
  ></textarea>
  <div class="composer__actions">
    <button
      type="button"
      class="composer__btn composer__btn--primary"
      disabled={!value.trim()}
      onclick={() => onsave(value)}
    >
      Save draft
    </button>
    <button
      type="button"
      class="composer__btn"
      onclick={oncancel}
    >
      Cancel
    </button>
  </div>
</div>

<style>
  .composer {
    border: 1px solid var(--accent-blue);
    border-radius: var(--radius-sm);
    margin: 4px 12px 8px 68px;
    padding: 8px;
    background: var(--bg-surface);
  }

  .composer__anchor {
    display: flex;
    margin-bottom: 6px;
  }

  .composer__anchor-label {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--accent-blue);
    padding: 2px 8px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent-blue) 10%, var(--bg-inset));
    border: 1px solid color-mix(in srgb, var(--accent-blue) 40%, transparent);
  }

  .composer__textarea {
    width: 100%;
    min-height: 60px;
    font-family: var(--font-sans);
    font-size: 13px;
    padding: 6px 8px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-primary);
    resize: vertical;
  }

  .composer__textarea:focus {
    outline: none;
    border-color: var(--accent-blue);
  }

  .composer__actions {
    display: flex;
    gap: 6px;
    justify-content: flex-end;
    margin-top: 6px;
  }

  .composer__btn {
    font-size: 12px;
    padding: 4px 10px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-primary);
    cursor: pointer;
  }

  .composer__btn:hover {
    background: var(--bg-surface-hover);
  }

  .composer__btn--primary {
    background: var(--accent-blue);
    color: #fff;
    border-color: var(--accent-blue);
  }

  .composer__btn--primary:hover {
    background: var(--accent-blue);
    filter: brightness(1.1);
  }

  .composer__btn--primary:disabled {
    background: var(--border-muted);
    color: var(--text-muted);
    cursor: not-allowed;
    filter: none;
  }
</style>
