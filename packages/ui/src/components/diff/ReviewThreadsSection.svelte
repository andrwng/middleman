<script lang="ts">
  import { getStores } from "../../context.js";
  import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

  const { reviewThreads, worktreeSession } = getStores();

  const threads = $derived(reviewThreads.getThreads().filter((t) => !t.hidden));
  const applicable = $derived(
    threads.filter((t) => t.status === "open" || t.status === "discussed"),
  );
  const busy = $derived(worktreeSession.hasRunningTurn());

  let expanded = $state(false);
  let userCollapsed = $state(false);
  $effect(() => {
    if (threads.length > 0 && !userCollapsed) expanded = true;
  });
  function toggle(): void {
    expanded = !expanded;
    userCollapsed = !expanded;
  }

  function anchorLabel(t: ReviewThread): string {
    const sign = t.side === "LEFT" ? "−" : "+";
    if (t.start_line != null && t.start_line !== t.line) {
      return `${sign}${t.start_line}–${t.line}`;
    }
    return `${sign}${t.line}`;
  }
  function rootBody(t: ReviewThread): string {
    return t.comments?.[0]?.body ?? "";
  }
  function truncate(s: string, n: number): string {
    return s.length <= n ? s : s.slice(0, n).trimEnd() + "…";
  }
  function scrollToThread(t: ReviewThread): void {
    const selector =
      `.diff-file[data-file-path="${CSS.escape(t.path)}"] ` +
      `.line-wrap[data-anchor-line="${t.line}"]` +
      `[data-anchor-side="${t.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) el.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  async function onApplyAll(): Promise<void> {
    if (busy) return;
    await reviewThreads.applyAll();
  }
</script>

{#if threads.length > 0}
  <div class="threads-section">
    <div class="threads-section__header">
      <button class="threads-section__toggle" onclick={toggle}>
        <span class="threads-section__chevron" class:threads-section__chevron--open={expanded}>&#8250;</span>
        <span class="threads-section__label">Review threads</span>
        <span class="threads-section__count">{threads.length}</span>
      </button>
      {#if applicable.length > 0}
        <button
          type="button"
          class="threads-section__apply-all"
          disabled={busy}
          title={busy ? "The review agent is busy" : `Apply ${applicable.length} thread(s)`}
          onclick={() => void onApplyAll()}
        >Apply all</button>
      {/if}
    </div>

    {#if expanded}
      <div class="threads-section__body">
        {#each threads as t (t.id)}
          <button
            type="button"
            class="thread-item"
            onclick={() => scrollToThread(t)}
            title="Scroll to this thread in the diff"
          >
            <span class="thread-item__anchor">{anchorLabel(t)}</span>
            <span class="thread-item__status">{t.status}</span>
            <span class="thread-item__path">{t.path}</span>
            <span class="thread-item__preview">{truncate(rootBody(t), 80)}</span>
            <span class="thread-item__count" title="comments">{(t.comments ?? []).length}c</span>
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .threads-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }
  .threads-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }
  .threads-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    border-radius: var(--radius-sm);
  }
  .threads-section__toggle:hover { background: var(--bg-surface-hover); }
  .threads-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }
  .threads-section__chevron--open { transform: rotate(90deg); }
  .threads-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }
  .threads-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }
  .threads-section__apply-all {
    margin-left: auto;
    font-size: 10px;
    font-weight: 600;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
    color: var(--accent-blue);
    cursor: pointer;
  }
  .threads-section__apply-all:hover:not(:disabled) { filter: brightness(1.1); }
  .threads-section__apply-all:disabled { opacity: 0.5; cursor: not-allowed; }
  .threads-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }
  .thread-item {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 10px 4px 12px;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    color: inherit;
  }
  .thread-item:hover { background: var(--bg-surface-hover); }
  .thread-item__anchor {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
    padding: 1px 6px;
    border-radius: 999px;
    flex-shrink: 0;
  }
  .thread-item__status {
    font-size: 10px;
    color: var(--text-muted);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    padding: 1px 6px;
    flex-shrink: 0;
  }
  .thread-item__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }
  .thread-item__preview {
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
    min-width: 0;
  }
  .thread-item__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>
