<script lang="ts">
  import type { DiffFile, DiffHunk, DiffLine } from "../../api/types.js";
  import { pairHunk } from "../../utils/diffPairing.js";
  import { tokenizeLineDual, langFromPath, type DualToken } from "../../utils/highlight.js";

  // DiffFileCore renders the contents of one DiffFile (hunks +
  // syntax-highlighted lines) in either unified or split layout.
  // It is intentionally "pure rendering" — no store reads, no
  // comment composers, no review UI. The PR review pane wraps
  // this with overlays (DiffFile.svelte); the worktree review
  // surface uses it directly. Keeping the surfaces separate lets
  // either side evolve its own overlays without affecting the
  // shared rendering primitive.

  interface Props {
    file: DiffFile;
    layout?: "unified" | "split";
    lang?: string | undefined;
  }

  const { file, layout = "unified", lang: providedLang }: Props = $props();

  const lang = $derived(providedLang ?? langFromPath(file.path));

  // Per-line tokens for syntax highlight, keyed by `${hunkIdx}:${lineIdx}`.
  // Tokenization is batched + async so a big file doesn't block the
  // main thread.
  let tokens = $state<Map<string, DualToken[]>>(new Map());
  let tokenVersion = 0;

  const BATCH_SIZE = 50;

  $effect(() => {
    const version = ++tokenVersion;
    const currentFile = file;
    const currentLang = lang;

    // Start fresh — file or lang changed.
    tokens = new Map();
    const next = new Map<string, DualToken[]>();

    void (async () => {
      const items: Array<{ key: string; content: string }> = [];
      for (let hi = 0; hi < currentFile.hunks.length; hi++) {
        const hunk = currentFile.hunks[hi]!;
        for (let li = 0; li < hunk.lines.length; li++) {
          items.push({
            key: `${hi}:${li}`,
            content: hunk.lines[li]!.content,
          });
        }
      }

      for (let i = 0; i < items.length; i += BATCH_SIZE) {
        if (version !== tokenVersion) return;
        const batch = items.slice(i, i + BATCH_SIZE);
        const results = await Promise.all(
          batch.map(async (item) => ({
            key: item.key,
            spans: await tokenizeLineDual(item.content, currentLang),
          })),
        );
        if (version !== tokenVersion) return;
        for (const r of results) {
          next.set(r.key, r.spans);
        }
        // Progressive render — refresh the reactive map after each batch.
        tokens = new Map(next);
        if (i + BATCH_SIZE < items.length) {
          await new Promise((r) => requestAnimationFrame(r));
        }
      }
    })();
  });

  function getTokens(hunkIdx: number, lineIdx: number): DualToken[] {
    const key = `${hunkIdx}:${lineIdx}`;
    const cached = tokens.get(key);
    if (cached) return cached;
    return [{ content: file.hunks[hunkIdx]!.lines[lineIdx]!.content }];
  }

  function markerFor(type: string): string {
    if (type === "add") return "+";
    if (type === "delete") return "-";
    return " ";
  }

  function hunkHeader(h: DiffHunk): string {
    const base = `@@ -${h.old_start},${h.old_count} +${h.new_start},${h.new_count} @@`;
    return h.section ? `${base} ${h.section}` : base;
  }

  function cellForLine(line: DiffLine | null, hunkIdx: number, lineIdx: number | null) {
    // Helper for split rendering — returns a small structured value
    // so the template stays readable.
    return { line, hunkIdx, lineIdx };
  }
</script>

<div class="dfc dfc--{layout}">
  {#if file.is_binary}
    <div class="dfc__binary">Binary file</div>
  {:else}
    {#each file.hunks as hunk, hi (hi)}
      <div class="dfc__hunk-head">{hunkHeader(hunk)}</div>
      {#if layout === "split"}
        {#each pairHunk(hunk) as row, ri (ri)}
          {@const leftCell = cellForLine(row.left?.line ?? null, hi, row.left?.lineIdx ?? null)}
          {@const rightCell = cellForLine(row.right?.line ?? null, hi, row.right?.lineIdx ?? null)}
          <div class="dfc__split-row">
            <div class="dfc__split-cell dfc__split-cell--left">
              {#if leftCell.line && leftCell.lineIdx !== null}
                <span class="dfc__gutter">{leftCell.line.old_num || ""}</span>
                <span class="dfc__marker dfc__marker--{leftCell.line.type}">{markerFor(leftCell.line.type)}</span>
                <span class="dfc__content dfc__content--{leftCell.line.type}">{#each getTokens(leftCell.hunkIdx, leftCell.lineIdx) as t, ti (ti)}<span style:color={`light-dark(${t.lightColor ?? "inherit"}, ${t.darkColor ?? "inherit"})`}>{t.content}</span>{/each}</span>
              {:else}
                <span class="dfc__empty-half"></span>
              {/if}
            </div>
            <div class="dfc__split-cell dfc__split-cell--right">
              {#if rightCell.line && rightCell.lineIdx !== null}
                <span class="dfc__gutter">{rightCell.line.new_num || ""}</span>
                <span class="dfc__marker dfc__marker--{rightCell.line.type}">{markerFor(rightCell.line.type)}</span>
                <span class="dfc__content dfc__content--{rightCell.line.type}">{#each getTokens(rightCell.hunkIdx, rightCell.lineIdx) as t, ti (ti)}<span style:color={`light-dark(${t.lightColor ?? "inherit"}, ${t.darkColor ?? "inherit"})`}>{t.content}</span>{/each}</span>
              {:else}
                <span class="dfc__empty-half"></span>
              {/if}
            </div>
          </div>
        {/each}
      {:else}
        {#each hunk.lines as line, li (li)}
          <div class="dfc__row dfc__row--{line.type}">
            <span class="dfc__gutter">{line.old_num || ""}</span>
            <span class="dfc__gutter">{line.new_num || ""}</span>
            <span class="dfc__marker dfc__marker--{line.type}">{markerFor(line.type)}</span>
            <span class="dfc__content dfc__content--{line.type}">{#each getTokens(hi, li) as t, ti (ti)}<span style:color={`light-dark(${t.lightColor ?? "inherit"}, ${t.darkColor ?? "inherit"})`}>{t.content}</span>{/each}</span>
          </div>
        {/each}
      {/if}
    {/each}
  {/if}
</div>

<style>
  .dfc {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-inset);
    overflow-x: auto;
  }

  .dfc__binary {
    padding: 12px;
    color: var(--text-muted);
    font-style: italic;
  }

  .dfc__hunk-head {
    padding: 2px 12px;
    color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 10%, transparent);
    font-weight: 600;
  }

  /* Unified layout: single column, old-num | new-num | marker | content */
  .dfc--unified .dfc__row {
    display: grid;
    grid-template-columns: 36px 36px 14px 1fr;
    padding-right: 8px;
    white-space: pre;
  }

  .dfc__row--add {
    background: color-mix(in srgb, var(--accent-green) 12%, transparent);
  }

  .dfc__row--delete {
    background: color-mix(in srgb, var(--accent-red) 12%, transparent);
  }

  /* Split layout: two equal-width cells side by side */
  .dfc--split .dfc__split-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  .dfc__split-cell {
    display: grid;
    grid-template-columns: 36px 14px 1fr;
    padding-right: 8px;
    white-space: pre;
    min-width: 0;
    border-right: 1px solid var(--border-muted);
  }

  .dfc__split-cell--right {
    border-right: none;
  }

  .dfc__split-cell .dfc__content--add {
    background: color-mix(in srgb, var(--accent-green) 12%, transparent);
  }

  .dfc__split-cell .dfc__content--delete {
    background: color-mix(in srgb, var(--accent-red) 12%, transparent);
  }

  .dfc__gutter {
    color: var(--text-muted);
    text-align: right;
    padding: 0 4px;
    user-select: none;
  }

  .dfc__marker {
    text-align: center;
    color: var(--text-secondary);
    user-select: none;
  }

  .dfc__marker--add {
    color: var(--accent-green);
  }

  .dfc__marker--delete {
    color: var(--accent-red);
  }

  .dfc__content {
    overflow-x: auto;
  }

  .dfc__empty-half {
    display: block;
    grid-column: 1 / -1;
    background: color-mix(in srgb, var(--text-muted) 4%, transparent);
  }
</style>
