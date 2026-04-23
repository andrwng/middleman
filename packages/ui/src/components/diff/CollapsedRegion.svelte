<script lang="ts">
  import { getStores } from "../../context.js";
  import DiffLineComponent from "./DiffLine.svelte";

  interface Props {
    // "top" = collapsed section above the first hunk (known
    //         size, one-directional expand from bottom edge).
    // "middle" = between two hunks (known size, two-directional).
    // "bottom" = after the last hunk (unknown size — keep
    //            fetching downward until the backend returns
    //            an empty slice).
    position?: "top" | "middle" | "bottom";
    layout?: "unified" | "split";
    lineCount: number;
    // Kept for interface parity; the diff store already knows
    // the current PR so these props aren't used by the fetch.
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
    position = "middle",
    layout = "unified",
    lineCount,
    path,
    sha,
    gapOldStart,
    gapNewStart,
  }: Props = $props();

  const STEP = 10;                    // lines per row click
  const SCRUB_PIXELS_PER_LINE = 10;   // wheel deltaY threshold per line

  // topCount = lines revealed extending the previous hunk downward.
  // bottomCount = lines revealed extending the next hunk upward.
  let topCount = $state(0);
  let bottomCount = $state(0);
  let topLines = $state<string[]>([]);
  let bottomLines = $state<string[]>([]);
  let loading = $state(false);
  let errorMsg = $state<string | null>(null);
  // For the "bottom" (end-of-file) region we don't know how many
  // lines are left below the last hunk; a short/empty response
  // tells us we hit EOF.
  let bottomExhausted = $state(false);

  // Pending line counts for the coalescing scrub path. The scrub
  // handler can fire dozens of wheel events per second — instead
  // of issuing one fetch per tick (and having most drop because
  // `loading` is set), accumulate here and issue a single bulk
  // fetch that catches up whenever the in-flight one returns.
  let pendingTop = 0;
  let pendingBottom = 0;
  let flushing = false;

  const { diff: diffStore } = getStores();

  // `remaining` is the distance between the two revealed edges in
  // the known-size cases. For a "bottom" region it's unknown, so
  // we just track exhausted vs. not.
  const remaining = $derived(
    position === "bottom"
      ? bottomExhausted
        ? 0
        : Infinity
      : Math.max(0, lineCount - topCount - bottomCount),
  );
  const fullyExpanded = $derived(
    position === "bottom" ? bottomExhausted : remaining === 0,
  );

  async function fetchRange(start: number, end: number): Promise<string[]> {
    if (end < start) return [];
    return diffStore.loadBlobRange(path, sha, start, end);
  }

  // expandTop pulls N more lines starting from where the top
  // reveal currently ends. For "bottom" regions this is how we
  // extend the last hunk downward (only one edge to grow).
  async function expandTop(n: number): Promise<void> {
    if (loading || fullyExpanded) return;
    const take = position === "bottom" ? n : Math.min(n, remaining);
    if (take <= 0) return;
    const start = gapNewStart + topCount;
    const end = start + take - 1;
    loading = true;
    errorMsg = null;
    try {
      const lines = await fetchRange(start, end);
      topLines = [...topLines, ...lines];
      topCount += lines.length;
      if (position === "bottom" && lines.length < take) {
        bottomExhausted = true;
      }
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // expandBottom reveals upward from the bottom edge of the gap,
  // extending the next hunk's context. Only applies to top/middle.
  async function expandBottom(n: number): Promise<void> {
    if (position === "bottom") return; // no lower anchor to grow toward
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

  // requestExpandTop / requestExpandBottom queue scrub-driven line
  // reveals without blocking on the network. The flush loop
  // coalesces bursts of wheel events into a single fetch.
  function requestExpandTop(n: number): void {
    if (fullyExpanded) return;
    pendingTop += n;
    void flushPending();
  }

  function requestExpandBottom(n: number): void {
    if (fullyExpanded || position === "bottom") return;
    pendingBottom += n;
    void flushPending();
  }

  async function flushPending(): Promise<void> {
    if (flushing) return;
    flushing = true;
    try {
      while ((pendingTop > 0 || pendingBottom > 0) && !fullyExpanded) {
        if (pendingTop > 0) {
          const n = pendingTop;
          pendingTop = 0;
          await expandTop(n);
          if (errorMsg) break;
        }
        if (pendingBottom > 0) {
          const n = pendingBottom;
          pendingBottom = 0;
          await expandBottom(n);
          if (errorMsg) break;
        }
      }
    } finally {
      flushing = false;
      pendingTop = 0;
      pendingBottom = 0;
    }
  }

  async function expandAll(): Promise<void> {
    if (loading || fullyExpanded) return;
    if (position === "bottom") {
      // Unknown size — keep pulling chunks until the backend
      // returns fewer lines than we asked for (EOF).
      const CHUNK = 500;
      while (!bottomExhausted) {
        const start = gapNewStart + topCount;
        const end = start + CHUNK - 1;
        loading = true;
        errorMsg = null;
        try {
          const lines = await fetchRange(start, end);
          topLines = [...topLines, ...lines];
          topCount += lines.length;
          if (lines.length < CHUNK) bottomExhausted = true;
        } catch (err) {
          errorMsg = err instanceof Error ? err.message : String(err);
          break;
        } finally {
          loading = false;
        }
      }
      return;
    }
    const start = gapNewStart + topCount;
    const end = gapNewStart + lineCount - 1 - bottomCount;
    if (end < start) return;
    loading = true;
    errorMsg = null;
    try {
      const lines = await fetchRange(start, end);
      // Append the whole middle to the top side so ordering
      // stays stable (top reveals + middle + bottom reveals).
      topLines = [...topLines, ...lines];
      topCount += lines.length;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // Default row click expands by STEP. For top-of-file and
  // between-hunks, that means split across both edges (STEP/2
  // each); for bottom-of-file, the whole STEP extends downward
  // since there's no other edge.
  async function expandStep(): Promise<void> {
    if (position === "bottom") {
      await expandTop(STEP);
      return;
    }
    if (position === "top") {
      // One-directional: reveal STEP lines at the bottom edge
      // of the gap so they sit adjacent to the first hunk, where
      // the reviewer is most likely looking.
      await expandBottom(STEP);
      return;
    }
    // Between-hunks: split evenly so both hunks get more context.
    const half = Math.ceil(STEP / 2);
    await expandTop(half);
    if (!fullyExpanded) {
      await expandBottom(STEP - half);
    }
  }

  function onRowClick(e: MouseEvent): void {
    // Don't treat the click-after-drag as a paginate action — the
    // scrub handler toggles `scrubbing` during pointerdown, so if
    // we've just finished a hold-scrub, let it pass without
    // advancing by a step.
    if (scrubHadWheel) {
      scrubHadWheel = false;
      return;
    }
    if (e.shiftKey) {
      void expandAll();
      return;
    }
    void expandStep();
  }

  // --- Press-and-hold scrub ---

  let scrubbing = $state(false);
  // Accumulate wheel deltas so sub-threshold scrolls eventually
  // trigger a line reveal instead of getting lost.
  let topPixelBuf = 0;
  let bottomPixelBuf = 0;
  // Was a wheel event seen during the current hold? If so the
  // pointerup's subsequent click should be suppressed — the user
  // scrubbed, they didn't intend a click-to-paginate.
  let scrubHadWheel = false;

  function onPointerDown(e: PointerEvent): void {
    if (fullyExpanded) return;
    if (e.button !== 0 && e.pointerType === "mouse") return;
    scrubbing = true;
    topPixelBuf = 0;
    bottomPixelBuf = 0;
    scrubHadWheel = false;
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* ignore — some browsers reject pointer capture on synthetic events */
    }
    window.addEventListener("wheel", onWheel, { passive: false });
    window.addEventListener("pointerup", onPointerUp, { once: true });
    window.addEventListener("pointercancel", onPointerUp, { once: true });
  }

  function onPointerUp(): void {
    scrubbing = false;
    topPixelBuf = 0;
    bottomPixelBuf = 0;
    window.removeEventListener("wheel", onWheel);
  }

  function onWheel(e: WheelEvent): void {
    if (!scrubbing) return;
    e.preventDefault();
    if (fullyExpanded) return;
    scrubHadWheel = true;

    const pxPerUnit = e.deltaMode === 1 ? 16 : 1;
    const dy = e.deltaY * pxPerUnit;

    if (dy > 0) {
      topPixelBuf += dy;
      const lines = Math.floor(topPixelBuf / SCRUB_PIXELS_PER_LINE);
      if (lines > 0) {
        topPixelBuf -= lines * SCRUB_PIXELS_PER_LINE;
        requestExpandTop(lines);
      }
    } else if (dy < 0) {
      if (position === "bottom") return;
      bottomPixelBuf += -dy;
      const lines = Math.floor(bottomPixelBuf / SCRUB_PIXELS_PER_LINE);
      if (lines > 0) {
        bottomPixelBuf -= lines * SCRUB_PIXELS_PER_LINE;
        requestExpandBottom(lines);
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
    return gapOldStart + lineCount - bottomCount + i;
  }
  function newNumForBottom(i: number): number {
    return gapNewStart + lineCount - bottomCount + i;
  }

  // Label copy. Clickable affordance is the whole row; tooltip
  // carries the keyboard/mouse-modifier hints.
  const label = $derived.by<string>(() => {
    if (errorMsg) return errorMsg;
    if (loading) return "Loading…";
    if (position === "bottom") {
      if (bottomExhausted) return "End of file";
      return "More below — click to expand";
    }
    return `${remaining} unchanged ${remaining === 1 ? "line" : "lines"} — click to expand`;
  });

  const tooltip = $derived(
    fullyExpanded
      ? ""
      : "Click to expand · Shift-click to show all · Press and hold, then scroll to scrub",
  );
</script>

{#if topLines.length > 0}
  {#each topLines as content, i (i)}
    {#if layout === "split"}
      <div class="ss-row">
        <div class="ss-cell ss-cell--left">
          <DiffLineComponent
            type="context"
            {content}
            oldNum={oldNumForTop(i)}
            tokens={[{ content }]}
            splitSide="left"
          />
        </div>
        <div class="ss-cell">
          <DiffLineComponent
            type="context"
            {content}
            newNum={newNumForTop(i)}
            tokens={[{ content }]}
            splitSide="right"
          />
        </div>
      </div>
    {:else}
      <DiffLineComponent
        type="context"
        {content}
        oldNum={oldNumForTop(i)}
        newNum={newNumForTop(i)}
        tokens={[{ content }]}
      />
    {/if}
  {/each}
{/if}

{#if !fullyExpanded}
  <div
    class="collapsed-region"
    class:collapsed-region--scrubbing={scrubbing}
    class:collapsed-region--error={!!errorMsg}
    class:collapsed-region--bottom={position === "bottom"}
    onpointerdown={onPointerDown}
    onclick={onRowClick}
    role="button"
    tabindex="0"
    onkeydown={(e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        if (e.shiftKey) void expandAll();
        else void expandStep();
      }
    }}
    title={tooltip}
  >
    <span class="collapsed-gutter"></span>
    <span class="collapsed-gutter"></span>
    <span class="collapsed-label" class:collapsed-label--error={!!errorMsg}>
      {label}
    </span>
  </div>
{/if}

{#if bottomLines.length > 0}
  {#each bottomLines as content, i (i)}
    {#if layout === "split"}
      <div class="ss-row">
        <div class="ss-cell ss-cell--left">
          <DiffLineComponent
            type="context"
            {content}
            oldNum={oldNumForBottom(i)}
            tokens={[{ content }]}
            splitSide="left"
          />
        </div>
        <div class="ss-cell">
          <DiffLineComponent
            type="context"
            {content}
            newNum={newNumForBottom(i)}
            tokens={[{ content }]}
            splitSide="right"
          />
        </div>
      </div>
    {:else}
      <DiffLineComponent
        type="context"
        {content}
        oldNum={oldNumForBottom(i)}
        newNum={newNumForBottom(i)}
        tokens={[{ content }]}
      />
    {/if}
  {/each}
{/if}

<style>
  .collapsed-region {
    display: flex;
    align-items: center;
    border-top: 1px dashed var(--diff-collapsed-border);
    border-bottom: 1px dashed var(--diff-collapsed-border);
    background: var(--diff-collapsed-bg);
    color: var(--diff-line-num);
    line-height: 20px;
    user-select: none;
    cursor: pointer;
  }

  .collapsed-region:hover {
    background: color-mix(in srgb, var(--accent-blue) 8%, var(--diff-collapsed-bg));
    border-top-color: var(--accent-blue);
    border-bottom-color: var(--accent-blue);
  }

  .collapsed-region:focus-visible {
    outline: 2px solid var(--accent-blue);
    outline-offset: -2px;
  }

  .collapsed-region--scrubbing {
    cursor: ns-resize;
    background: color-mix(in srgb, var(--accent-blue) 16%, var(--diff-collapsed-bg));
    border-top-color: var(--accent-blue);
    border-bottom-color: var(--accent-blue);
  }

  .collapsed-region--error {
    border-top-color: var(--accent-red);
    border-bottom-color: var(--accent-red);
  }

  .collapsed-region--bottom {
    /* End-of-file: only the top edge of the bar is "docked" to
       the preceding hunk. A single border reads more as an "end
       stop" than a separator. */
    border-bottom: none;
  }

  .collapsed-gutter {
    width: 50px;
    flex-shrink: 0;
    background: var(--diff-collapsed-bg);
  }

  .collapsed-label {
    padding: 2px 12px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--diff-hunk-text);
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .collapsed-label--error {
    color: var(--accent-red);
  }

  /* Mirrors the split-layout grid in DiffFile so the expanded
     context rows land in the same columns as the diff hunks
     above and below. Component-scoped styles don't leak across
     files, so we repeat the two declarations that matter. */
  .ss-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  .ss-cell {
    min-width: 0;
    overflow-x: auto;
  }

  .ss-cell--left {
    border-right: 1px solid var(--diff-border);
  }
</style>
