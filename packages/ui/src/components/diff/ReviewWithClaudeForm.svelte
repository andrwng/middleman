<script lang="ts">
  import { getStores } from "../../context.js";

  interface Props {
    // Prefills the instructions box (Re-run uses the last instructions).
    initial?: string;
    onstarted?: () => void;
    oncancel: () => void;
  }

  const { initial = "", onstarted, oncancel }: Props = $props();
  const { aiReviewRun } = getStores();

  // svelte-ignore state_referenced_locally
  let instructions = $state(initial);
  let failed = $state(false);

  async function start(): Promise<void> {
    failed = false;
    const ok = await aiReviewRun.startRun(instructions);
    if (ok) onstarted?.();
    else failed = true;
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      oncancel();
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void start();
    }
  }
</script>

<div class="rwc-form">
  <label class="rwc-form__label" for="rwc-instructions">Instructions for Claude (optional)</label>
  <textarea
    id="rwc-instructions"
    class="rwc-form__input"
    rows="3"
    placeholder="e.g. focus on error handling and concurrency"
    bind:value={instructions}
    onkeydown={onKeydown}
  ></textarea>
  <p class="rwc-form__hint">
    Claude reads the PR and stages suggestions. Nothing is posted until you add them to drafts and publish.
  </p>
  {#if failed && aiReviewRun.getError()}
    <p class="rwc-form__error" role="alert">{aiReviewRun.getError()}</p>
  {/if}
  <div class="rwc-form__actions">
    <button type="button" class="rwc-form__btn" onclick={oncancel}>Cancel</button>
    <button
      type="button"
      class="rwc-form__btn rwc-form__btn--primary"
      disabled={aiReviewRun.isStarting()}
      onclick={() => void start()}
    >{aiReviewRun.isStarting() ? "Starting…" : "Start review"}</button>
  </div>
</div>

<style>
  .rwc-form {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .rwc-form__label {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .rwc-form__input {
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

  .rwc-form__hint {
    margin: 0;
    font-size: 11px;
    color: var(--text-muted);
  }

  .rwc-form__error {
    margin: 0;
    padding: 6px 8px;
    font-size: 12px;
    color: var(--accent-red);
    background: color-mix(in srgb, var(--accent-red) 8%, var(--bg-inset));
    border: 1px solid var(--accent-red);
    border-radius: var(--radius-sm);
  }

  .rwc-form__actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
  }

  .rwc-form__btn {
    font-size: 12px;
    padding: 4px 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .rwc-form__btn--primary {
    border-color: var(--accent-claude);
    background: var(--accent-claude);
    color: #fff;
  }

  .rwc-form__btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
