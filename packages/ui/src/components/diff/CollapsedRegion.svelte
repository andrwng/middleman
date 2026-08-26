<script lang="ts">
  import { getStores } from "../../context.js";
  import DiffLineComponent from "./DiffLine.svelte";
  import { tokenizeLineDual, type DualToken } from "../../utils/highlight.js";

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
    // Language for syntax highlighting; undefined falls back to
    // plain text (DualToken with no color).
    lang?: string | undefined;
    // A new-side (RIGHT) line number this region should reveal, or
    // null/undefined if nothing is being sought here. Only the region
    // whose gap window contains the line acts on it — every other
    // region must do nothing at all: no fetch, no callback.
    revealNewLine?: number | null;
    // Called exactly once, after revealNewLine has rendered. Never
    // called for a target outside this region's window, and never
    // called again for a target that already fired it.
    onrevealed?: () => void;
  }

  const {
    position = "middle",
    layout = "unified",
    lineCount,
    path,
    sha,
    gapOldStart,
    gapNewStart,
    lang,
    revealNewLine = null,
    onrevealed,
  }: Props = $props();

  const STEP = 10;                    // lines per row click
  const SCRUB_PIXELS_PER_LINE = 10;   // wheel deltaY threshold per line
  // Safe per-request line span for a single blob-range fetch. Must stay
  // under the server's hard cap (blobRangeMaxLines in
  // internal/server/huma_routes.go and local_dispatch.go, currently
  // 2000) — a request that exceeds it gets a 400, not a truncated
  // response. expandAll's "bottom" handling and the reveal-and-jump
  // effect below both chunk against this same constant.
  const CHUNK = 500;

  // topCount = lines revealed extending the previous hunk downward.
  // bottomCount = lines revealed extending the next hunk upward.
  let topCount = $state(0);
  let bottomCount = $state(0);
  let topLines = $state<string[]>([]);
  let bottomLines = $state<string[]>([]);
  // Matching tokens per line; same length as topLines/bottomLines.
  // When tokenization is in-flight we render with a plain-text
  // fallback so the line shows immediately.
  let topTokens = $state<DualToken[][]>([]);
  let bottomTokens = $state<DualToken[][]>([]);
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
  // $state (not a plain flag) so the reveal effect below — which
  // reads it as a coalescing guard — re-runs the instant a flush
  // finishes, rather than depending on `loading` happening to change
  // at the same moment in whatever the effect scheduler's microtask
  // ordering turns out to be.
  let flushing = $state(false);

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

  // Returns a plain-text fallback immediately and fires a
  // tokenization in the background — Shiki's single-line pass is
  // fast but still async, and we don't want reveal to block on it.
  // When unknown lang, don't tokenize at all.
  function plainTokens(content: string): DualToken[] {
    return [{ content }];
  }

  async function tokenizeBatch(contents: string[]): Promise<DualToken[][]> {
    if (!lang) return contents.map(plainTokens);
    return Promise.all(contents.map((c) => tokenizeLineDual(c, lang)));
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
      // Seed plain-text tokens so the row paints immediately;
      // the tokenized versions swap in when Shiki finishes.
      const placeholder = lines.map(plainTokens);
      topLines = [...topLines, ...lines];
      topTokens = [...topTokens, ...placeholder];
      const baseIdx = topCount;
      topCount += lines.length;
      if (position === "bottom" && lines.length < take) {
        bottomExhausted = true;
      }
      void tokenizeBatch(lines).then((tokens) => {
        // Splice tokenized results into the same slots we seeded.
        // Use a local copy to avoid the case where another reveal
        // raced in between.
        const copy = [...topTokens];
        for (let i = 0; i < tokens.length; i++) {
          if (copy[baseIdx + i]) copy[baseIdx + i] = tokens[i]!;
        }
        topTokens = copy;
      });
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
      const placeholder = lines.map(plainTokens);
      bottomLines = [...lines, ...bottomLines];
      bottomTokens = [...placeholder, ...bottomTokens];
      bottomCount += lines.length;
      void tokenizeBatch(lines).then((tokens) => {
        // Tokens align with the lines we just prepended, so they
        // sit at the front of the current array.
        const copy = [...bottomTokens];
        for (let i = 0; i < tokens.length; i++) {
          if (copy[i]) copy[i] = tokens[i]!;
        }
        bottomTokens = copy;
      });
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
      while (!bottomExhausted) {
        const start = gapNewStart + topCount;
        const end = start + CHUNK - 1;
        loading = true;
        errorMsg = null;
        try {
          const lines = await fetchRange(start, end);
          const placeholder = lines.map(plainTokens);
          const baseIdx = topCount;
          topLines = [...topLines, ...lines];
          topTokens = [...topTokens, ...placeholder];
          topCount += lines.length;
          if (lines.length < CHUNK) bottomExhausted = true;
          void tokenizeBatch(lines).then((tokens) => {
            const copy = [...topTokens];
            for (let i = 0; i < tokens.length; i++) {
              if (copy[baseIdx + i]) copy[baseIdx + i] = tokens[i]!;
            }
            topTokens = copy;
          });
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
      const placeholder = lines.map(plainTokens);
      const baseIdx = topCount;
      topLines = [...topLines, ...lines];
      topTokens = [...topTokens, ...placeholder];
      topCount += lines.length;
      void tokenizeBatch(lines).then((tokens) => {
        const copy = [...topTokens];
        for (let i = 0; i < tokens.length; i++) {
          if (copy[baseIdx + i]) copy[baseIdx + i] = tokens[i]!;
        }
        topTokens = copy;
      });
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // --- Reveal-and-jump: expand toward an externally-requested line ---
  //
  // revealNewLine names a RIGHT-side line something outside this
  // component wants brought into view (see scrollToDiffLine.ts). Only
  // the region whose gap window contains that line does anything;
  // every other region must ignore it entirely — no fetch, no
  // callback. revealedFor guards onrevealed so it fires once per
  // target even though this effect re-runs on every loading/topCount/
  // bottomCount change while it drives the expansion forward.
  let revealedFor: number | null = null;

  function newLineIsCovered(target: number): boolean {
    if (target <= gapNewStart + topCount - 1) return true;
    // bottomCount can never leave 0 for a "bottom" region (see the
    // note on oldNumForBottom/newNumForBottom below), so this half is
    // a no-op there — excluded explicitly for clarity rather than
    // relying on that invariant silently.
    if (position !== "bottom" && target >= gapNewStart + lineCount - bottomCount) {
      return true;
    }
    return false;
  }

  $effect(() => {
    const target = revealNewLine;
    if (target == null) return;

    const inWindow =
      position === "bottom"
        ? target >= gapNewStart
        : target >= gapNewStart && target <= gapNewStart + lineCount - 1;
    if (!inWindow) return;

    if (revealedFor === target) return;
    if (errorMsg) return; // a fetch already failed here; don't retry silently

    if (newLineIsCovered(target)) {
      revealedFor = target;
      onrevealed?.();
      return;
    }

    if (fullyExpanded) return; // EOF, or the whole gap is revealed — target unreachable
    if (loading || flushing) return; // a fetch is already in flight; retry once it settles

    if (position === "bottom") {
      // Unknown extent: the full remaining distance to the target can
      // easily exceed the server's per-request cap — a "bottom"
      // region's tail is unbounded by design, so a target can sit
      // arbitrarily far past the last known edge. Chunk toward it the
      // same way expandAll's bottom branch does rather than asking
      // for the whole span in one shot. Each pass strictly grows
      // topCount by at least one line (see expandTop), and the effect
      // re-runs whenever topCount changes, so this converges: either
      // newLineIsCovered flips true once topCount reaches the target,
      // or a short response flips bottomExhausted (true EOF, caught
      // by the fullyExpanded guard above on the next pass) — whichever
      // comes first. BlobRange (internal/worktrees/blob.go) clamps at
      // EOF instead of paginating, so a short response unambiguously
      // means the target is past end-of-file.
      const needed = target - (gapNewStart + topCount) + 1;
      void expandTop(Math.min(needed, CHUNK));
      return;
    }

    // Known-size gap: grow from whichever edge is nearer so the
    // reveal costs as few lines as possible. Chunk toward it the same
    // way the "bottom" branch above does — the nearer-edge distance
    // can still exceed the server's per-request cap for a large gap,
    // and the effect re-runs on topCount/bottomCount changes so this
    // converges over successive passes just like that branch.
    const needViaTop = target - (gapNewStart + topCount - 1);
    const needViaBottom = gapNewStart + lineCount - bottomCount - target;
    if (needViaTop <= needViaBottom) {
      void expandTop(Math.min(needViaTop, CHUNK));
    } else {
      void expandBottom(Math.min(needViaBottom, CHUNK));
    }
  });

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
  // oldNumForBottom/newNumForBottom are only ever used by top/middle
  // regions today: expandBottom and requestExpandBottom (above) both
  // return unconditionally for position === "bottom", so bottomLines/
  // bottomCount never leave []/0 there — a bottom region's revealed
  // lines render exclusively through the newNumForTop path instead.
  // DiffFile also passes lineCount={0} for bottom regions, so if those
  // guards were ever lifted this formula (which reads lineCount) would
  // need revisiting first.
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
    {@const tokens = topTokens[i] ?? [{ content }]}
    {#if layout === "split"}
      <div class="ss-row">
        <div class="ss-cell ss-cell--left">
          <DiffLineComponent
            type="context"
            {content}
            oldNum={oldNumForTop(i)}
            {tokens}
            splitSide="left"
          />
        </div>
        <div class="ss-cell">
          <DiffLineComponent
            type="context"
            {content}
            newNum={newNumForTop(i)}
            {tokens}
            splitSide="right"
            anchorLine={newNumForTop(i)}
            anchorSide="RIGHT"
          />
        </div>
      </div>
    {:else}
      <DiffLineComponent
        type="context"
        {content}
        oldNum={oldNumForTop(i)}
        newNum={newNumForTop(i)}
        {tokens}
        anchorLine={newNumForTop(i)}
        anchorSide="RIGHT"
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
    {@const tokens = bottomTokens[i] ?? [{ content }]}
    {#if layout === "split"}
      <div class="ss-row">
        <div class="ss-cell ss-cell--left">
          <DiffLineComponent
            type="context"
            {content}
            oldNum={oldNumForBottom(i)}
            {tokens}
            splitSide="left"
          />
        </div>
        <div class="ss-cell">
          <DiffLineComponent
            type="context"
            {content}
            newNum={newNumForBottom(i)}
            {tokens}
            splitSide="right"
            anchorLine={newNumForBottom(i)}
            anchorSide="RIGHT"
          />
        </div>
      </div>
    {:else}
      <DiffLineComponent
        type="context"
        {content}
        oldNum={oldNumForBottom(i)}
        newNum={newNumForBottom(i)}
        {tokens}
        anchorLine={newNumForBottom(i)}
        anchorSide="RIGHT"
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
