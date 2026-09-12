<script lang="ts">
  import type { DiffScope } from "../../stores/diff.svelte.js";

  interface Props {
    scope: DiffScope;
    onreset: () => void;
    // How many commits a span covers, for the tooltip. Omitted where the
    // caller has no commit list to count against.
    spanCount?: number | null;
  }

  const { scope, onreset, spanCount = null }: Props = $props();

  const dirty = $derived(scope.kind !== "head");

  const label = $derived.by(() => {
    if (scope.kind === "head") return "HEAD";
    if (scope.kind === "commit") return scope.sha.slice(0, 7);
    // A span is diffed as ParentOf(from)..to, so it INCLUDES `from`. Labelling
    // it "from..to" said the opposite in git's own syntax, and read as if the
    // base commit were being left out; `from^..to` is the range actually
    // being diffed.
    if (scope.kind === "range") {
      return `${scope.fromSha.slice(0, 7)}^..${scope.toSha.slice(0, 7)}`;
    }
    return "Since last review";
  });

  const hint = $derived.by(() => {
    if (!dirty) return "Viewing full diff";
    if (scope.kind === "range") {
      const from = scope.fromSha.slice(0, 7);
      const to = scope.toSha.slice(0, 7);
      const count = spanCount === null ? "" : `${spanCount} commits: `;
      return `${count}${from} through ${to}, both included \u2014 click to reset`;
    }
    return "Reset to full diff";
  });
</script>

<button
  class="scope-pill"
  class:scope-pill--dirty={dirty}
  onclick={dirty ? onreset : undefined}
  disabled={!dirty}
  title={hint}
>
  <span class="scope-pill__dot"></span>
  <span class="scope-pill__label">{label}</span>
  {#if dirty}
    <span class="scope-pill__reset" aria-hidden="true">&times;</span>
  {/if}
</button>

<style>
  .scope-pill {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 10px;
    padding: 2px 8px;
    border-radius: 999px;
    line-height: 1.4;
    cursor: default;
    color: var(--text-secondary);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    font-family: var(--font-sans);
    user-select: none;
  }

  .scope-pill__dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
    background: var(--accent-green);
  }

  .scope-pill--dirty {
    cursor: pointer;
    color: var(--text-primary);
    background: rgba(251, 191, 36, 0.08);
    border-color: rgba(251, 191, 36, 0.35);
  }

  .scope-pill--dirty:hover {
    background: rgba(251, 191, 36, 0.15);
    border-color: rgba(251, 191, 36, 0.55);
  }

  .scope-pill--dirty .scope-pill__dot {
    background: var(--accent-amber);
  }

  .scope-pill__label {
    font-family: var(--font-mono);
    font-size: 9.5px;
  }

  .scope-pill__reset {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1;
    margin-left: 2px;
  }
</style>
