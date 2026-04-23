<script lang="ts">
  import { getStores } from "../../context.js";

  interface Props {
    // Total number of unchanged lines in the gap between hunks.
    lineCount: number;
    // Kept for interface parity with the old call site; the diff
    // store already knows the current PR so these props are
    // unused by the fetch itself.
    owner: string;
    name: string;
    number: number;
    // File + commit SHA to read the blob from. SHA is the NEW-side
    // SHA of the current diff scope, matching hunk new_num numbering.
    path: string;
    sha: string;
    // First unchanged line of the gap, 1-based, in old and new files.
    gapOldStart: number;
    gapNewStart: number;
  }

  const {
    lineCount,
    path,
    sha,
    gapOldStart,
    gapNewStart,
  }: Props = $props();

  const { diff: diffStore } = getStores();

  const STEP = 10;               // lines per +N button click
  const SCRUB_PIXELS_PER_LINE = 18; // wheel deltaY threshold per line

  // topCount = lines revealed extending the previous hunk downward.
  // bottomCount = lines revealed extending the next hunk upward.
  // They grow toward each other; when their sum reaches lineCount the
  // collapsed region disappears entirely.
  let topCount = $state(0);
  let bottomCount = $state(0);
  let topLines = $state<string[]>([]);
  let bottomLines = $state<string[]>([]);
  let loading = $state(false);
  let errorMsg = $state<string | null>(null);

  const remaining = $derived(Math.max(0, lineCount - topCount - bottomCount));
  const fullyExpanded = $derived(remaining === 0);

  async function fetchRange(start: number, end: number): Promise<string[]> {
    if (end < start) return [];
    return diffStore.loadBlobRange(path, sha, start, end);
  }

  // expandTop pulls N more lines starting from where the top
  // reveal currently ends (gapNewStart + topCount onward).
  async function expandTop(n: number): Promise<void> {
    if (loading || fullyExpanded) return;
    const take = Math.min(n, remaining);
    if (take <= 0) return;
    const start = gapNewStart + topCount;
    const end = start + take - 1;
    loading = true;
    errorMsg = null;
    try {
      const lines = await fetchRange(start, end);
      topLines = [...topLines, ...lines];
      topCount += lines.length;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // expandBottom pulls N lines upward from the bottom edge of the
  // gap, which is gapNewStart + lineCount - 1 - bottomCount.
  async function expandBottom(n: number): Promise<void> {
    if (loading || fullyExpanded) return;
    const take = Math.min(n, remaining);
    if (take <= 0) return;
    const end = gapNewStart + lineCount - 1 - bottomCount;
    const start = end - take + 1;
    loading = true;
    errorMsg = null;
    try {
      const lines = await fetchRange(start, end);
      bottomLines = [...lines, ...bottomLines];
      bottomCount += lines.length;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function expandAll(): Promise<void> {
    if (loading || fullyExpanded) return;
    const start = gapNewStart + topCount;
    const end = gapNewStart + lineCount - 1 - bottomCount;
    if (end < start) return;
    loading = true;
    errorMsg = null;
    try {
      const lines = await fetchRange(start, end);
      // Append the whole middle to the top side so the ordering
      // stays stable (top reveals + middle + bottom reveals).
      topLines = [...topLines, ...lines];
      topCount += lines.length;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // --- Press-and-hold scrub ---

  let scrubbing = $state(false);
  // Accumulate wheel deltas so sub-threshold scrolls eventually
  // trigger a line reveal instead of getting lost.
  let topPixelBuf = 0;
  let bottomPixelBuf = 0;
  let pointerId: number | null = null;

  function onPointerDown(e: PointerEvent): void {
    if (fullyExpanded) return;
    // Ignore right-clicks / secondary buttons.
    if (e.button !== 0 && e.pointerType === "mouse") return;
    scrubbing = true;
    pointerId = e.pointerId;
    topPixelBuf = 0;
    bottomPixelBuf = 0;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    window.addEventListener("wheel", onWheel, { passive: false });
    window.addEventListener("pointerup", onPointerUp, { once: true });
    window.addEventListener("pointercancel", onPointerUp, { once: true });
  }

  function onPointerUp(): void {
    scrubbing = false;
    pointerId = null;
    topPixelBuf = 0;
    bottomPixelBuf = 0;
    window.removeEventListener("wheel", onWheel);
  }

  function onWheel(e: WheelEvent): void {
    if (!scrubbing) return;
    e.preventDefault();
    if (fullyExpanded) return;

    // Normalize deltaMode to pixels — line-mode wheels (Firefox on
    // some platforms) report deltaY in "lines" not px.
    const pxPerUnit = e.deltaMode === 1 ? 16 : 1;
    const dy = e.deltaY * pxPerUnit;

    if (dy > 0) {
      // Scroll down: feed lines into the top edge of the gap.
      topPixelBuf += dy;
      while (topPixelBuf >= SCRUB_PIXELS_PER_LINE && !fullyExpanded) {
        topPixelBuf -= SCRUB_PIXELS_PER_LINE;
        void expandTop(1);
      }
    } else if (dy < 0) {
      bottomPixelBuf += -dy;
      while (bottomPixelBuf >= SCRUB_PIXELS_PER_LINE && !fullyExpanded) {
        bottomPixelBuf -= SCRUB_PIXELS_PER_LINE;
        void expandBottom(1);
      }
    }
  }

  function oldNumForTop(i: number): number {
    return gapOldStart + i;
  }
  function newNumForTop(i: number): number {
    return gapNewStart + i;
  }
  function oldNumForBottom(i: number): number {
    // The bottom edge sits at gapOldStart + lineCount - 1; when we
    // have `bottomCount` lines revealed, they map to old numbers
    // [gapOldStart + lineCount - bottomCount, ..., gapOldStart + lineCount - 1].
    return gapOldStart + lineCount - bottomCount + i;
  }
  function newNumForBottom(i: number): number {
    return gapNewStart + lineCount - bottomCount + i;
  }
</script>

{#if topLines.length > 0}
  {#each topLines as content, i (i)}
    <div class="expanded-line">
      <span class="expanded-gutter">{oldNumForTop(i)}</span>
      <span class="expanded-gutter">{newNumForTop(i)}</span>
      <span class="expanded-marker"></span>
      <pre class="expanded-code">{content}</pre>
    </div>
  {/each}
{/if}

{#if !fullyExpanded}
  <div
    class="collapsed-region"
    class:collapsed-region--scrubbing={scrubbing}
    class:collapsed-region--error={!!errorMsg}
    onpointerdown={onPointerDown}
    role="button"
    tabindex="-1"
    title="Press and hold, then scroll to expand context lines"
  >
    <span class="collapsed-gutter"></span>
    <span class="collapsed-gutter"></span>
    <div class="collapsed-controls">
      <button
        type="button"
        class="collapsed-btn"
        title="Show {Math.min(STEP, remaining)} lines below the hunk above"
        onclick={(e) => {
          e.stopPropagation();
          void expandTop(STEP);
        }}
        disabled={loading}
        aria-label="Expand down by {STEP} lines"
      >
        <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6">
          <path d="M6 2v8M3 7l3 3 3-3" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        +{Math.min(STEP, remaining)}
      </button>
      <span class="collapsed-label">
        {#if errorMsg}
          <span class="collapsed-err">{errorMsg}</span>
        {:else if loading}
          loading…
        {:else}
          {remaining} unchanged {remaining === 1 ? "line" : "lines"} · hold + scroll to expand
        {/if}
      </span>
      <button
        type="button"
        class="collapsed-btn"
        title="Show {Math.min(STEP, remaining)} lines above the hunk below"
        onclick={(e) => {
          e.stopPropagation();
          void expandBottom(STEP);
        }}
        disabled={loading}
        aria-label="Expand up by {STEP} lines"
      >
        <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6">
          <path d="M6 10V2M3 5l3-3 3 3" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        +{Math.min(STEP, remaining)}
      </button>
      <button
        type="button"
        class="collapsed-btn collapsed-btn--all"
        title="Show all {remaining} unchanged lines"
        onclick={(e) => {
          e.stopPropagation();
          void expandAll();
        }}
        disabled={loading}
      >
        all
      </button>
    </div>
  </div>
{/if}

{#if bottomLines.length > 0}
  {#each bottomLines as content, i (i)}
    <div class="expanded-line">
      <span class="expanded-gutter">{oldNumForBottom(i)}</span>
      <span class="expanded-gutter">{newNumForBottom(i)}</span>
      <span class="expanded-marker"></span>
      <pre class="expanded-code">{content}</pre>
    </div>
  {/each}
{/if}

<style>
  .collapsed-region {
    display: flex;
    align-items: stretch;
    border-top: 1px dashed var(--diff-collapsed-border);
    border-bottom: 1px dashed var(--diff-collapsed-border);
    background: var(--diff-collapsed-bg);
    color: var(--diff-line-num);
    line-height: 20px;
    user-select: none;
    cursor: ns-resize;
  }

  .collapsed-region:hover {
    background: color-mix(in srgb, var(--accent-blue) 5%, var(--diff-collapsed-bg));
  }

  .collapsed-region--scrubbing {
    background: color-mix(in srgb, var(--accent-blue) 12%, var(--diff-collapsed-bg));
    border-top-color: var(--accent-blue);
    border-bottom-color: var(--accent-blue);
  }

  .collapsed-region--error {
    border-top-color: var(--accent-red);
    border-bottom-color: var(--accent-red);
  }

  .collapsed-gutter {
    width: 50px;
    flex-shrink: 0;
    background: var(--diff-collapsed-bg);
  }

  .collapsed-controls {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 12px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--diff-hunk-text);
    flex: 1;
    min-width: 0;
  }

  .collapsed-label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .collapsed-err {
    color: var(--accent-red);
  }

  .collapsed-btn {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 2px 6px;
    font-family: inherit;
    font-size: 10px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    background: var(--bg-surface);
    color: var(--text-secondary);
    cursor: pointer;
    flex-shrink: 0;
  }

  .collapsed-btn:hover:not(:disabled) {
    border-color: var(--accent-blue);
    color: var(--accent-blue);
  }

  .collapsed-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .collapsed-btn--all {
    padding: 2px 10px;
  }

  .expanded-line {
    display: flex;
    align-items: stretch;
    line-height: 20px;
    font-size: 12px;
    background: var(--diff-bg);
    color: var(--diff-text);
  }

  .expanded-gutter {
    flex-shrink: 0;
    box-sizing: border-box;
    width: 50px;
    padding-right: 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--diff-line-num);
    text-align: right;
    user-select: none;
    background: var(--diff-bg);
  }

  .expanded-marker {
    width: 16px;
    flex-shrink: 0;
    background: var(--diff-bg);
  }

  .expanded-code {
    margin: 0;
    padding: 0 8px;
    font-family: var(--font-mono);
    font-size: 12px;
    white-space: pre;
    overflow: visible;
  }
</style>
