<script lang="ts">
  import { getStores } from "../../context.js";
  import type { DraftComment } from "../../stores/diff.svelte.js";
  import { jumpToDraft } from "./jumpToDraft.js";

  const { diff: diffStore } = getStores();

  const suggestions = $derived(diffStore.getSuggestions());
  const discarded = $derived(diffStore.getLastDiscarded());

  let expanded = $state(true);

  function anchorLabel(c: DraftComment): string {
    const sign = c.side === "LEFT" ? "−" : "+";
    if (c.startLine != null && c.startLine !== c.line) return `${sign}${c.startLine}–${c.line}`;
    return `${sign}${c.line}`;
  }

  function truncate(text: string, n: number): string {
    return text.length <= n ? text : text.slice(0, n).trimEnd() + "…";
  }
</script>

{#if suggestions.length > 0 || discarded.length > 0}
  <div class="suggestions-section">
    <div class="suggestions-section__header">
      <button type="button" class="suggestions-section__toggle" onclick={() => (expanded = !expanded)}>
        <span class="suggestions-section__chevron" class:suggestions-section__chevron--open={expanded}>&#8250;</span>
        <span class="suggestions-section__label">Suggestions</span>
        <span class="suggestions-section__count">{suggestions.length}</span>
      </button>
    </div>

    {#if expanded}
      <div class="suggestions-section__body">
        {#if discarded.length > 0}
          <div class="suggestions-section__undo">
            <span>Discarded {discarded.length} suggestion{discarded.length === 1 ? "" : "s"}</span>
            <button type="button" class="suggestions-section__link" onclick={() => diffStore.undoDiscard()}>Undo</button>
          </div>
        {/if}
        {#if suggestions.length > 0}
          <div class="suggestions-section__bulk">
            <button type="button" class="suggestions-section__btn" onclick={() => diffStore.acceptAllSuggestions()}>Add all to drafts</button>
            <button type="button" class="suggestions-section__btn" onclick={() => diffStore.discardAllSuggestions()}>Discard all</button>
          </div>
          {#each suggestions as s (s.id)}
            <button
              type="button"
              class="suggestion-item"
              onclick={() => void jumpToDraft(s, diffStore)}
              title="Scroll to this suggestion in the diff"
            >
              <span class="suggestion-item__anchor">{anchorLabel(s)}</span>
              <span class="suggestion-item__path">{s.path}</span>
              <span class="suggestion-item__preview">{truncate(s.body, 80)}</span>
            </button>
          {/each}
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .suggestions-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .suggestions-section__header {
    display: flex;
    align-items: center;
    padding: 2px 10px 2px 0;
  }

  .suggestions-section__toggle {
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

  .suggestions-section__chevron {
    display: inline-block;
    transition: transform 0.1s;
    color: var(--text-muted);
  }

  .suggestions-section__chevron--open {
    transform: rotate(90deg);
  }

  .suggestions-section__label {
    font-weight: 600;
  }

  .suggestions-section__count {
    margin-left: auto;
    font-size: 11px;
    color: var(--accent-claude);
  }

  .suggestions-section__body {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 2px 10px 8px;
  }

  .suggestions-section__undo,
  .suggestions-section__bulk {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 0 4px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .suggestions-section__link {
    padding: 0;
    border: none;
    background: none;
    color: var(--accent-blue);
    font-size: 12px;
    cursor: pointer;
  }

  .suggestions-section__btn {
    font-size: 11px;
    padding: 2px 8px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-primary);
    cursor: pointer;
  }

  .suggestion-item {
    display: flex;
    align-items: baseline;
    gap: 6px;
    width: 100%;
    padding: 3px 4px;
    border: none;
    border-left: 2px dashed var(--accent-claude);
    background: none;
    color: var(--text-primary);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }

  .suggestion-item:hover {
    background: var(--bg-surface-hover);
  }

  .suggestion-item__anchor {
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  .suggestion-item__path {
    color: var(--text-muted);
    white-space: nowrap;
  }

  .suggestion-item__preview {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
