<script lang="ts">
  import { getStores } from "../../context.js";
  import SessionToolTimeline from "./SessionToolTimeline.svelte";
  import ReviewWithClaudeForm from "../diff/ReviewWithClaudeForm.svelte";

  // Floating panel for the latest Review with Claude run, docked beside
  // the Notes toggle. The run store owns whether it is open so the
  // toolbar's progress pill can open it too.
  const { aiReviewRun } = getStores();

  const run = $derived(aiReviewRun.getRun());
  const active = $derived(aiReviewRun.isActive());
  const open = $derived(aiReviewRun.isPanelOpen());

  let showActivity = $state(false);
  let rerunning = $state(false);

  const statusLabel = $derived.by(() => {
    switch (run?.status) {
      case "queued":
      case "running":
        return "Reviewing…";
      case "done":
        return "Done";
      case "failed":
        return "Failed";
      case "cancelled":
        return "Stopped";
      default:
        return "";
    }
  });

  const countLabel = $derived.by(() => {
    const n = run?.suggestion_count ?? 0;
    return `${n} suggestion${n === 1 ? "" : "s"}`;
  });

  function onKeydown(e: KeyboardEvent): void {
    // The Re-run form's own Esc cancels the form; leave the panel open.
    if (e.key === "Escape" && !e.defaultPrevented) {
      e.preventDefault();
      aiReviewRun.closePanel();
    }
  }
</script>

{#if run}
  {#if open}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="claude-panel" role="dialog" aria-label="Claude review" tabindex="-1" onkeydown={onKeydown}>
      <div class="claude-panel__header">
        <span class="claude-panel__title">Claude review</span>
        <span
          class="claude-panel__status"
          class:claude-panel__status--active={active}
          class:claude-panel__status--failed={run.status === "failed"}
        >{statusLabel} · {countLabel}</span>
        <button
          type="button"
          class="claude-panel__close"
          onclick={() => aiReviewRun.closePanel()}
          title="Close (Esc)"
          aria-label="Close Claude review"
        >&times;</button>
      </div>

      <div class="claude-panel__body">
        {#if run.instructions}
          <p class="claude-panel__instructions">Instructions: {run.instructions}</p>
        {/if}
        {#if run.status === "done" && run.summary}
          <p class="claude-panel__summary">{run.summary}</p>
        {/if}
        {#if run.status === "failed"}
          <p class="claude-panel__error" role="alert">{run.error}</p>
          {#if run.suggestion_count > 0}
            <p class="claude-panel__partial">
              {run.suggestion_count} suggestion{run.suggestion_count === 1 ? " was" : "s were"} kept from before the failure.
            </p>
          {/if}
        {/if}
        {#if run.raw_json}
          <button type="button" class="claude-panel__link" onclick={() => (showActivity = !showActivity)}>
            {showActivity ? "Hide activity" : "Show activity"}
          </button>
          {#if showActivity}
            <SessionToolTimeline rawJSON={run.raw_json} />
          {/if}
        {/if}
      </div>

      <div class="claude-panel__footer">
        {#if active}
          <button type="button" class="claude-panel__btn" onclick={() => void aiReviewRun.cancel()}>Stop</button>
        {:else if rerunning}
          <ReviewWithClaudeForm
            initial={run.instructions}
            onstarted={() => (rerunning = false)}
            oncancel={() => (rerunning = false)}
          />
        {:else}
          <button type="button" class="claude-panel__btn" onclick={() => (rerunning = true)}>Re-run</button>
        {/if}
      </div>
    </div>
  {:else}
    <button
      type="button"
      class="claude-toggle"
      class:claude-toggle--active={active}
      onclick={() => aiReviewRun.togglePanel()}
      title="Open the Claude review"
    >
      <span class="claude-toggle__badge" aria-hidden="true">Claude</span>
      <span>{statusLabel} · {countLabel}</span>
    </button>
  {/if}
{/if}

<style>
  .claude-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    border-radius: 999px;
    border: 1px solid var(--accent-claude);
    background: var(--bg-surface);
    color: var(--text-secondary);
    font-size: 12px;
    cursor: pointer;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  }

  .claude-toggle:hover {
    background: var(--bg-surface-hover);
  }

  .claude-toggle--active {
    color: var(--accent-claude);
  }

  .claude-toggle__badge {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-claude);
    color: #fff;
  }

  .claude-panel {
    display: flex;
    flex-direction: column;
    width: min(420px, calc(100vw - 32px));
    max-height: min(480px, 60vh);
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
    font-size: 12px;
  }

  .claude-panel__header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border-muted);
  }

  .claude-panel__title {
    font-weight: 600;
    color: var(--text-primary);
  }

  .claude-panel__status {
    color: var(--text-muted);
  }

  .claude-panel__status--active {
    color: var(--accent-claude);
  }

  .claude-panel__status--failed {
    color: var(--accent-red);
  }

  .claude-panel__close {
    margin-left: auto;
    border: none;
    background: none;
    color: var(--text-muted);
    font-size: 16px;
    line-height: 1;
    cursor: pointer;
  }

  .claude-panel__body {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 10px;
    overflow-y: auto;
  }

  .claude-panel__instructions,
  .claude-panel__partial {
    margin: 0;
    color: var(--text-muted);
  }

  .claude-panel__summary {
    margin: 0;
    color: var(--text-primary);
    white-space: pre-wrap;
  }

  .claude-panel__error {
    margin: 0;
    color: var(--accent-red);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .claude-panel__link {
    align-self: flex-start;
    padding: 0;
    border: none;
    background: none;
    color: var(--accent-blue);
    font-size: 12px;
    cursor: pointer;
  }

  .claude-panel__footer {
    padding: 8px 10px;
    border-top: 1px solid var(--border-muted);
  }

  .claude-panel__btn {
    font-size: 12px;
    padding: 3px 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }
</style>
