<script lang="ts">
  interface Pip {
    id: string;
    label: string;
    muted: boolean;
  }
  interface Props {
    pips: Pip[];
    peeked: string | null;
    onPeek: (id: string) => void;
    onExpandAll: () => void;
  }
  const { pips, peeked, onPeek, onExpandAll }: Props = $props();
</script>

<div class="top-strip" role="toolbar" aria-label="Consolidated top sections">
  <button
    type="button"
    class="top-strip__chevron"
    onclick={onExpandAll}
    aria-label="Expand all sections"
    title="Expand all sections"
  >
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
         stroke="currentColor" stroke-width="1.6">
      <polyline points="3.5,2 6.5,5 3.5,8" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>
  {#each pips as pip (pip.id)}
    <button
      type="button"
      data-id={pip.id}
      class="pip"
      class:pip--muted={pip.muted}
      class:pip--peeked={peeked === pip.id}
      onclick={() => onPeek(pip.id)}
      aria-pressed={peeked === pip.id}
    >
      {pip.label}
    </button>
  {/each}
</div>

<style>
  .top-strip {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 6px 12px;
    background: var(--bg-inset);
    border: 1px dashed var(--accent-blue);
    border-radius: 999px;
    margin: 4px 0;
  }
  .top-strip__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--accent-blue);
    cursor: pointer;
  }
  .top-strip__chevron:hover {
    background: var(--bg-surface-hover);
  }
  .pip {
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
    color: var(--text-primary);
    cursor: pointer;
    font-size: 11px;
    line-height: 1.4;
  }
  .pip:hover {
    border-color: var(--accent-blue);
  }
  .pip--muted {
    opacity: 0.55;
  }
  .pip--peeked {
    border-color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 12%, var(--bg-surface));
  }
</style>
