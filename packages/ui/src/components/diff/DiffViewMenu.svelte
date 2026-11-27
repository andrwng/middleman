<script lang="ts">
  import { getStores } from "../../context.js";

  // The diff's view settings, folded behind one toolbar button so the
  // toolbar fits beside both sidebars. Each change applies at once and
  // leaves the popover open, so several can be made in one visit.
  const { diff } = getStores();

  const layoutOptions = [
    { value: "unified", label: "Unified" },
    { value: "split", label: "Split" },
  ] as const;
  const tabOptions = [1, 2, 4, 8] as const;

  let open = $state(false);
  let menuEl = $state<HTMLDivElement>();

  // Hidden whitespace persists across sessions and silently drops changes
  // from the diff, so the closed button carries a marker while it is on.
  const hideWhitespace = $derived(diff.getHideWhitespace());

  $effect(() => {
    if (!open) return;

    function onMousedown(event: MouseEvent): void {
      if (menuEl?.contains(event.target as Node)) return;
      open = false;
    }

    // Claim the Escape: App sends any unclaimed one back to the PR list,
    // which would leave the diff and drop an unsaved inline comment.
    function onKeydown(event: KeyboardEvent): void {
      if (event.key !== "Escape") return;
      event.preventDefault();
      open = false;
    }

    document.addEventListener("mousedown", onMousedown);
    document.addEventListener("keydown", onKeydown);
    return () => {
      document.removeEventListener("mousedown", onMousedown);
      document.removeEventListener("keydown", onKeydown);
    };
  });
</script>

<div class="toolbar-group view-menu" bind:this={menuEl}>
  <button
    type="button"
    class="view-btn"
    aria-expanded={open}
    title={hideWhitespace ? "View options (whitespace changes are hidden)" : "View options"}
    onclick={() => (open = !open)}
  >
    <svg
      width="12" height="12" viewBox="0 0 12 12" fill="none"
      stroke="currentColor" stroke-width="1.5" aria-hidden="true"
    >
      <path d="M1.5 3.5h9M1.5 8.5h9" stroke-linecap="round" />
      <circle cx="4" cy="3.5" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8.5" r="1.5" fill="currentColor" stroke="none" />
    </svg>
    View
    <svg
      width="8" height="8" viewBox="0 0 8 8" fill="none"
      stroke="currentColor" stroke-width="1.5" aria-hidden="true"
    >
      <path d="M1.5 3l2.5 2.5L6.5 3" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
    {#if hideWhitespace}
      <span class="view-btn__marker" aria-hidden="true"></span>
    {/if}
  </button>
  {#if open}
    <div class="view-popover" role="group" aria-label="View options">
      <div class="view-row">
        <span class="toolbar-label">Layout</span>
        <div class="segmented-control">
          {#each layoutOptions as opt}
            <button
              class="segment"
              class:segment--active={diff.getLayout() === opt.value}
              onclick={() => diff.setLayout(opt.value)}
            >
              {opt.label}
            </button>
          {/each}
        </div>
      </div>
      <div class="view-row">
        <span class="toolbar-label">Tab width</span>
        <div class="segmented-control">
          {#each tabOptions as opt}
            <button
              class="segment"
              class:segment--active={diff.getTabWidth() === opt}
              onclick={() => diff.setTabWidth(opt)}
            >
              {opt}
            </button>
          {/each}
        </div>
      </div>
      <div class="view-row">
        <span class="toolbar-label">Hide whitespace</span>
        <button
          class="toggle-switch"
          class:toggle-switch--on={hideWhitespace}
          role="switch"
          aria-checked={hideWhitespace}
          title={hideWhitespace ? "Show whitespace changes" : "Hide whitespace changes"}
          onclick={() => diff.setHideWhitespace(!hideWhitespace)}
        >
          <span class="toggle-knob"></span>
        </button>
      </div>
    </div>
  {/if}
</div>

<style>
  .view-menu {
    position: relative;
    display: flex;
    align-items: center;
  }

  .view-btn {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 3px 10px;
    font-size: 11px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .view-btn:hover,
  .view-btn[aria-expanded="true"] {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .view-btn__marker {
    position: absolute;
    top: -3px;
    right: -3px;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent-amber);
  }

  /* max-content: an absolutely positioned box otherwise shrinks to its
     containing block, which here is only as wide as the button.
     z-index 41: one above the Review with Claude form, which can be open
     at the same time. View closes on any click outside it, so clicking
     back into the form dismisses View and leaves the form as it was. */
  .view-popover {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    z-index: 41;
    width: max-content;
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 10px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md, 6px);
    background: var(--bg-surface);
    box-shadow: 0 6px 18px rgba(0, 0, 0, 0.18);
  }

  .view-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }

  .toolbar-label {
    font-size: 11px;
    color: var(--text-secondary);
    user-select: none;
    white-space: nowrap;
  }

  .segmented-control {
    display: flex;
    border: 1px solid var(--diff-border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .segment {
    font-size: 11px;
    font-family: var(--font-mono);
    padding: 2px 8px;
    color: var(--text-secondary);
    background: var(--diff-bg);
    border-right: 1px solid var(--diff-border);
    line-height: 18px;
  }

  .segment:last-child {
    border-right: none;
  }

  .segment:hover {
    background: var(--bg-surface-hover);
  }

  .segment--active {
    background: var(--accent-blue);
    color: #ffffff;
  }

  .segment--active:hover {
    background: var(--accent-blue);
  }

  .toggle-switch {
    position: relative;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--border-default);
    transition: background 0.2s;
    flex-shrink: 0;
  }

  .toggle-switch--on {
    background: var(--accent-blue);
  }

  .toggle-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #ffffff;
    transition: transform 0.2s;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
  }

  .toggle-switch--on .toggle-knob {
    transform: translateX(16px);
  }
</style>
