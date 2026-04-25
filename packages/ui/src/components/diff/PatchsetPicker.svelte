<script lang="ts">
  import { getStores } from "../../context.js";
  import { timeAgo } from "../../utils/time.js";

  // Gerrit-style patchset picker. Chip strip of PS1..PSn with click
  // to view a specific patchset (via the interdiff endpoint) and
  // shift-click to set a compare base — that pair drives the diff
  // store's "patchsets" scope, producing a rebase-subtracted diff.
  // Loads lazily on first mount so single-push PRs never pay the
  // round-trip.

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

  function applyScope(): void {
    if (selectedNumber === null) return;
    const list = patchsets;
    if (!list || list.length === 0) return;
    const latest = list[list.length - 1]!.number;
    // No base and viewing the latest = "the normal PR diff"; ask the
    // store for HEAD scope so we don't pay for an interdiff round-trip
    // on the common case.
    if (baseNumber === null && selectedNumber === latest) {
      diff.resetToHead();
      return;
    }
    const from = baseNumber ?? 0;
    if (from === 0) {
      // Showing "just this patchset" — compare PS(n-1) → PS(n) if
      // possible, otherwise fall back to HEAD scope.
      const idx = list.findIndex((p) => p.number === selectedNumber);
      if (idx > 0) {
        diff.selectPatchsets(list[idx - 1]!.number, selectedNumber);
      } else {
        diff.resetToHead();
      }
      return;
    }
    if (from === selectedNumber) return;
    diff.selectPatchsets(from, selectedNumber);
  }

  function pick(n: number, e?: MouseEvent): void {
    if (e?.shiftKey && selectedNumber !== null && n !== selectedNumber) {
      baseNumber = n;
      applyScope();
      return;
    }
    selectedNumber = n;
    if (baseNumber !== null && baseNumber === n) {
      baseNumber = null;
    }
    applyScope();
  }

  function clearBase(): void {
    baseNumber = null;
    applyScope();
  }
</script>

{#if patchsets && patchsets.length > 1}
  <div class="ps-picker" role="toolbar" aria-label="Patchsets">
    <span class="ps-picker__label">Patchsets</span>
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
            "Click to view; shift-click to set as compare base."
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
  </div>
{:else if loading}
  <div class="ps-picker ps-picker--idle">Loading patchsets…</div>
{:else if errorMsg}
  <div class="ps-picker ps-picker--error">Failed to load patchsets: {errorMsg}</div>
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
</style>
