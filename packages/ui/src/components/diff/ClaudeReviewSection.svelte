<script lang="ts">
  import { getStores } from "../../context.js";
  import SessionToolTimeline from "../detail/SessionToolTimeline.svelte";
  import ReviewWithClaudeForm from "./ReviewWithClaudeForm.svelte";

  const { aiReviewRun } = getStores();

  const run = $derived(aiReviewRun.getRun());
  const active = $derived(aiReviewRun.isActive());

  let expanded = $state(true);
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
</script>

{#if run}
  <div class="claude-review">
    <div class="claude-review__header">
      <button type="button" class="claude-review__toggle" onclick={() => (expanded = !expanded)}>
        <span class="claude-review__chevron" class:claude-review__chevron--open={expanded}>&#8250;</span>
        <span class="claude-review__label">Claude review</span>
        <span
          class="claude-review__status"
          class:claude-review__status--active={active}
          class:claude-review__status--failed={run.status === "failed"}
        >{statusLabel}</span>
        <span class="claude-review__count">{run.suggestion_count}</span>
      </button>
    </div>

    {#if expanded}
      <div class="claude-review__body">
        {#if run.instructions}
          <p class="claude-review__instructions">Instructions: {run.instructions}</p>
        {/if}
        {#if run.status === "done" && run.summary}
          <p class="claude-review__summary">{run.summary}</p>
        {/if}
        {#if run.status === "failed"}
          <p class="claude-review__error" role="alert">{run.error}</p>
          {#if run.suggestion_count > 0}
            <p class="claude-review__partial">
              {run.suggestion_count} suggestion{run.suggestion_count === 1 ? " was" : "s were"} kept from before the failure.
            </p>
          {/if}
        {/if}
        {#if run.raw_json}
          <button type="button" class="claude-review__link" onclick={() => (showActivity = !showActivity)}>
            {showActivity ? "Hide activity" : "Show activity"}
          </button>
          {#if showActivity}
            <SessionToolTimeline rawJSON={run.raw_json} />
          {/if}
        {/if}

        {#if active}
          <div class="claude-review__actions">
            <button type="button" class="claude-review__btn" onclick={() => void aiReviewRun.cancel()}>Stop</button>
          </div>
        {:else if rerunning}
          <ReviewWithClaudeForm
            initial={run.instructions}
            onstarted={() => (rerunning = false)}
            oncancel={() => (rerunning = false)}
          />
        {:else}
          <div class="claude-review__actions">
            <button type="button" class="claude-review__btn" onclick={() => (rerunning = true)}>Re-run</button>
          </div>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .claude-review {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .claude-review__header {
    display: flex;
    align-items: center;
    padding: 2px 10px 2px 0;
  }

  .claude-review__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    color: var(--text-primary);
    font-size: 12px;
    cursor: pointer;
    text-align: left;
  }

  .claude-review__chevron {
    display: inline-block;
    transition: transform 0.1s;
    color: var(--text-muted);
  }

  .claude-review__chevron--open {
    transform: rotate(90deg);
  }

  .claude-review__label {
    font-weight: 600;
  }

  .claude-review__status {
    font-size: 11px;
    color: var(--text-muted);
  }

  .claude-review__status--active {
    color: var(--accent-claude);
  }

  .claude-review__status--failed {
    color: var(--accent-red);
  }

  .claude-review__count {
    margin-left: auto;
    font-size: 11px;
    color: var(--text-muted);
  }

  .claude-review__body {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 4px 10px 10px;
    font-size: 12px;
  }

  .claude-review__instructions,
  .claude-review__partial {
    margin: 0;
    color: var(--text-muted);
  }

  .claude-review__summary {
    margin: 0;
    color: var(--text-primary);
    white-space: pre-wrap;
  }

  .claude-review__error {
    margin: 0;
    color: var(--accent-red);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .claude-review__link {
    align-self: flex-start;
    padding: 0;
    border: none;
    background: none;
    color: var(--accent-blue);
    font-size: 12px;
    cursor: pointer;
  }

  .claude-review__actions {
    display: flex;
    gap: 6px;
  }

  .claude-review__btn {
    font-size: 12px;
    padding: 3px 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }
</style>
