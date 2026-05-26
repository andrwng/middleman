<script lang="ts">
  import { getStores } from "../../context.js";
  import { timeAgo } from "../../utils/time.js";

  // Gerrit-style patchset picker. Chip strip of PS1..PSn with click
  // to view a specific patchset (via the interdiff endpoint) and
  // shift-click to set a compare base — that pair drives the diff
  // store's "patchsets" scope, producing a rebase-subtracted diff.
  // Loads lazily on first mount so single-push PRs never pay the
  // round-trip.

  interface Props {
    // Render the chips regardless of the persisted `collapsed` flag.
    // Used by the consolidated top-sections "peek" flow so a previously
    // collapsed picker still reveals its chips when the user clicks the
    // pip. Defaults to false so the stacked picker honors localStorage.
    forceExpanded?: boolean;
  }

  const { forceExpanded = false }: Props = $props();

  const { diff } = getStores();

  const patchsets = $derived(diff.getPatchsets());
  const loading = $derived(diff.isPatchsetsLoading());
  const errorMsg = $derived(diff.getPatchsetsError());
  const scope = $derived(diff.getScope());

  // Local selection state; default selected = newest (and "HEAD-aligned" —
  // i.e. no interdiff applied until the user explicitly picks a base or
  // a non-latest patchset).
  let selectedNumber = $state<number | null>(null);
  let baseNumber = $state<number | null>(null);

  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-patchset-collapsed") === "true",
  );
  // Effective collapsed state: persisted collapse is overridden by the
  // peek-flow's forceExpanded prop. The chevron rotation, aria-label,
  // and title text must reflect what's actually visible — otherwise a
  // peeked picker shows a "collapsed" chevron over an expanded chip
  // strip.
  const effectivelyCollapsed = $derived(collapsed && !forceExpanded);
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-patchset-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }

  $effect(() => {
    void diff.loadPatchsets();
  });

  // Default the selection once data arrives. Reflect the live scope
  // back into local state so the chip strip stays in sync when the
  // user resets to HEAD via other controls.
  $effect(() => {
    const list = patchsets;
    if (!list || list.length === 0) return;
    const latest = list[list.length - 1]!.number;
    if (scope.kind === "patchsets") {
      selectedNumber = scope.toNumber;
      baseNumber = scope.fromNumber;
    } else if (selectedNumber === null) {
      selectedNumber = latest;
      baseNumber = null;
    } else if (scope.kind === "head") {
      // A reset elsewhere (refresh, j/k navigation) should clear the
      // compare base so the chip strip isn't lying about the diff
      // currently on screen.
      baseNumber = null;
      selectedNumber = latest;
    }
  });

  // Plain-click a chip = view that patchset (auto-derives the
  // previous patchset as the base so something visible happens).
  // Shift-click = explicitly set/toggle the compare base. ✕ snaps
  // the selection back to the latest chip and clears the base, so
  // the user always lands on the full PR diff.
  function applyScope(): void {
    if (selectedNumber === null) return;
    const list = patchsets;
    if (!list || list.length === 0) return;
    const latest = list[list.length - 1]!.number;

    // Latest selected with no explicit base = the normal PR diff.
    if (selectedNumber === latest && baseNumber === null) {
      diff.resetToHead();
      return;
    }
    if (baseNumber === selectedNumber) {
      // Degenerate compare; treat as no comparison.
      diff.resetToHead();
      return;
    }
    if (baseNumber !== null) {
      diff.selectPatchsets(baseNumber, selectedNumber);
      return;
    }
    // Plain-click on a non-latest chip: auto-derive base = previous
    // patchset so the click produces a visible delta.
    const idx = list.findIndex((p) => p.number === selectedNumber);
    if (idx > 0) {
      diff.selectPatchsets(list[idx - 1]!.number, selectedNumber);
      return;
    }
    // Oldest patchset clicked alone — no prior patchset to compare
    // against. Fall back to full PR diff.
    diff.resetToHead();
  }

  function pick(n: number, e?: MouseEvent): void {
    if (e?.shiftKey) {
      // Shift-click: set/toggle the compare base. Clicking the
      // already-set base clears it.
      if (baseNumber === n) {
        baseNumber = null;
      } else {
        baseNumber = n;
      }
      applyScope();
      return;
    }
    selectedNumber = n;
    // Selecting the chip that's currently the base clears the base —
    // a chip can't be both sides of a comparison.
    if (baseNumber !== null && baseNumber === n) {
      baseNumber = null;
    }
    applyScope();
  }

  function clearBase(): void {
    // Snap selected back to latest so applyScope's auto-derive
    // doesn't immediately re-create the comparison the user is
    // trying to exit.
    const list = patchsets;
    if (list && list.length > 0) {
      selectedNumber = list[list.length - 1]!.number;
    }
    baseNumber = null;
    applyScope();
  }
</script>

{#if loading}
  <div class="ps-picker ps-picker--idle">Loading patchsets…</div>
{:else if errorMsg}
  <div class="ps-picker ps-picker--error">Failed to load patchsets: {errorMsg}</div>
{:else if patchsets && patchsets.length > 1}
  <div class="ps-picker" role="toolbar" aria-label="Patchsets">
    <button
      type="button"
      class="ps-picker__chevron-btn"
      onclick={toggleCollapsed}
      aria-label={effectivelyCollapsed ? "Expand patchsets" : "Collapse patchsets"}
      title={effectivelyCollapsed ? "Expand patchsets" : "Collapse patchsets"}
    >
      <svg
        class="ps-picker__chevron"
        class:ps-picker__chevron--collapsed={effectivelyCollapsed}
        width="10" height="10" viewBox="0 0 10 10" fill="none"
        stroke="currentColor" stroke-width="1.6"
      >
        <polyline points="2,3.5 5,6.5 8,3.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <span class="ps-picker__label">Patchsets</span>
    {#if !collapsed || forceExpanded}
      <div class="ps-picker__chips">
        {#each patchsets as p (p.id)}
          {@const isSelected = p.number === selectedNumber}
          {@const isBase = p.number === baseNumber}
          <button
            type="button"
            class="ps-chip"
            class:ps-chip--selected={isSelected}
            class:ps-chip--base={isBase}
            onclick={(e) => pick(p.number, e)}
            title={
              (isBase ? "Comparing FROM this patchset.\n" : "") +
              (isSelected ? "Currently viewing this patchset.\n" : "") +
              `Head: ${p.head_sha.slice(0, 7)}\n` +
              `Observed: ${timeAgo(p.observed_at)}\n\n` +
              "Click to view this patchset (compares with the previous one).\n" +
              "Shift-click to set as compare base for an explicit pair."
            }
          >
            PS{p.number}
          </button>
        {/each}
      </div>
      {#if baseNumber !== null}
        <button
          type="button"
          class="ps-picker__reset"
          onclick={clearBase}
          title="Clear compare base"
        >
          compare PS{baseNumber} → PS{selectedNumber} ✕
        </button>
      {:else}
        <span class="ps-picker__hint">shift-click a chip to compare</span>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .ps-picker {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 12px;
    background: var(--bg-inset);
    border-bottom: 1px solid var(--border-muted);
    font-size: 11px;
    color: var(--text-muted);
    flex-wrap: wrap;
  }

  .ps-picker--idle,
  .ps-picker--error {
    color: var(--text-muted);
    font-style: italic;
  }

  .ps-picker--error {
    color: var(--accent-red);
  }

  .ps-picker__label {
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
  }

  .ps-picker__chips {
    display: inline-flex;
    gap: 4px;
    align-items: center;
  }

  .ps-chip {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    color: var(--text-secondary);
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
    border-radius: 999px;
    cursor: pointer;
  }

  .ps-chip:hover {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .ps-chip--selected {
    background: var(--accent-blue);
    border-color: var(--accent-blue);
    color: #fff;
  }

  .ps-chip--base {
    border-color: var(--accent-amber);
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 10%, var(--bg-surface));
  }

  .ps-chip--base.ps-chip--selected {
    background: var(--accent-amber);
    color: #fff;
  }

  .ps-picker__reset {
    margin-left: auto;
    font-size: 10px;
    color: var(--accent-amber);
    background: none;
    border: none;
    cursor: pointer;
    font-family: var(--font-mono);
  }

  .ps-picker__reset:hover {
    color: var(--accent-blue);
  }

  .ps-picker__hint {
    margin-left: auto;
    font-size: 10px;
    color: var(--text-muted);
    font-style: italic;
  }

  .ps-picker__chevron-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .ps-picker__chevron-btn:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .ps-picker__chevron {
    transition: transform 0.15s;
  }

  .ps-picker__chevron--collapsed {
    transform: rotate(-90deg);
  }
</style>
