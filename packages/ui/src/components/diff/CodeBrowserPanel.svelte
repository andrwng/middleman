<script lang="ts">
  import { getStores } from "../../context.js";
  import { tokenizeLineDual, langFromPath, type DualToken } from "../../utils/highlight.js";

  interface Props {
    owner: string;
    name: string;
    number: number;
    sha: string;
    initialPath: string;
    onclose: () => void;
  }

  const { owner, name, number, sha, initialPath, onclose }: Props = $props();

  const { codeBrowser: browser } = getStores();

  $effect(() => {
    browser.open(owner, name, number, sha, initialPath);
  });

  // Dual-theme token cache, keyed by line index. Mirrors DiffFile.svelte's
  // approach: each span carries both colors as CSS custom properties, so
  // theme switch is pure CSS (zero DOM updates, zero re-renders).
  // Tokenization happens once per line using Shiki's native dual-theme API.
  let lines = $state<string[]>([]);
  let tokens = $state<Map<number, DualToken[]>>(new Map());
  let tokenVersion = 0;

  // Tokenize in small batches to avoid blocking the main thread. Matches
  // DiffFile.svelte's BATCH_SIZE.
  const BATCH_SIZE = 50;

  // Re-tokenize whenever the browser's file content or path changes.
  $effect(() => {
    const version = ++tokenVersion;
    const content = browser.content;
    if (content == null) {
      lines = [];
      tokens = new Map();
      return;
    }
    const currentLines = content.split("\n");
    lines = currentLines;
    const lang = langFromPath(browser.path ?? "");
    tokens = new Map();
    void tokenizeAllLines(currentLines, lang, version);
  });

  async function tokenizeAllLines(
    currentLines: string[],
    lang: string | undefined,
    version: number,
  ): Promise<void> {
    const next = new Map<number, DualToken[]>();
    for (let i = 0; i < currentLines.length; i += BATCH_SIZE) {
      // Bail out if a newer tokenization run has started (e.g. the reader
      // navigated to a different file mid-tokenize).
      if (version !== tokenVersion) return;
      const batch = currentLines.slice(i, i + BATCH_SIZE);
      const results = await Promise.all(
        batch.map(async (line, bi) => ({
          idx: i + bi,
          spans: await tokenizeLineDual(line, lang),
        })),
      );
      if (version !== tokenVersion) return;
      for (const r of results) {
        next.set(r.idx, r.spans);
      }
      // Update reactively after each batch so lines get highlighted progressively.
      tokens = new Map(next);
      // Yield to the browser between batches.
      if (i + BATCH_SIZE < currentLines.length) {
        await new Promise((r) => requestAnimationFrame(r));
      }
    }
  }

  // Directories the reader has expanded. The store lazily loads a
  // directory's children the first time it is expanded (loadTree keys
  // entriesByDir by that directory's path); re-expanding one whose
  // children are already cached must not refetch them.
  let expandedDirs = $state<Set<string>>(new Set());

  function toggleDir(path: string) {
    const next = new Set(expandedDirs);
    if (next.has(path)) {
      next.delete(path);
    } else {
      next.add(path);
      if (!browser.entriesByDir.has(path)) {
        browser.loadTree(path);
      }
    }
    expandedDirs = next;
  }
</script>

{#snippet tree(dirPath: string, depth: number)}
  {#each browser.entriesByDir.get(dirPath) ?? [] as entry (entry.path)}
    <div class="code-browser-row" style="padding-left: {depth * 14}px">
      {#if entry.type === "dir"}
        <button
          type="button"
          class="code-browser-entry code-browser-entry--dir"
          onclick={() => toggleDir(entry.path)}
        >
          <span class="code-browser-caret">{expandedDirs.has(entry.path) ? "▾" : "▸"}</span>
          {entry.name}
        </button>
        {#if expandedDirs.has(entry.path)}
          {@render tree(entry.path, depth + 1)}
        {/if}
      {:else}
        <button
          type="button"
          class="code-browser-entry code-browser-entry--file"
          class:code-browser-entry--active={browser.path === entry.path}
          onclick={() => browser.navigateTo(entry.path)}
        >
          {entry.name}
        </button>
      {/if}
    </div>
  {/each}
{/snippet}

<div class="code-browser-overlay">
  <div class="code-browser-panel">
    <div class="code-browser-header">
      <span class="code-browser-title">{browser.path ?? "Browse files"}</span>
      <button type="button" onclick={onclose}>Close</button>
    </div>
    <div class="code-browser-body">
      <nav class="code-browser-tree">
        {@render tree("", 0)}
      </nav>
      <div class="code-browser-content">
        {#if browser.status === "missing"}
          <p class="code-browser-empty">This file doesn't exist at this commit.</p>
        {:else if browser.status === "loading"}
          <p class="code-browser-empty">Loading…</p>
        {:else if browser.status === "error"}
          <p class="code-browser-empty">{browser.error ?? "Failed to load file"}</p>
        {:else}
          <div class="code-browser-file">
            {#each lines as line, i (i)}
              <pre class="code-browser-line">{#each tokens.get(i) ?? [{ content: line }] as span}<span style:--dc={span.darkColor} style:--lc={span.lightColor}>{span.content}</span>{/each}</pre>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .code-browser-overlay {
    position: absolute;
    inset: 0;
    z-index: 60;
    display: flex;
    justify-content: flex-end;
    background: rgba(0, 0, 0, 0.35);
  }
  .code-browser-panel {
    width: 100%;
    max-width: 100%;
    height: 100%;
    background: var(--diff-bg);
    display: flex;
    flex-direction: column;
    box-shadow: -2px 0 12px rgba(0, 0, 0, 0.2);
  }
  .code-browser-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 8px 12px;
    border-bottom: 1px solid var(--diff-border);
  }
  .code-browser-title {
    font-family: var(--font-mono);
  }
  .code-browser-body {
    flex: 1;
    display: flex;
    overflow: hidden;
  }
  .code-browser-tree {
    width: 260px;
    flex-shrink: 0;
    overflow-y: auto;
    border-right: 1px solid var(--diff-border);
    display: flex;
    flex-direction: column;
  }
  .code-browser-row {
    display: flex;
    flex-direction: column;
  }
  .code-browser-entry {
    text-align: left;
    padding: 4px 12px;
    background: none;
    border: none;
    cursor: pointer;
    font-family: var(--font-mono);
    color: inherit;
  }
  .code-browser-entry:hover {
    background: var(--diff-stale-bg);
  }
  .code-browser-entry--active {
    font-weight: 600;
  }
  .code-browser-caret {
    display: inline-block;
    width: 1em;
  }
  .code-browser-content {
    flex: 1;
    overflow: auto;
    padding: 8px 12px;
  }
  .code-browser-empty {
    font-family: var(--font-mono);
  }
  .code-browser-file {
    font-family: var(--font-mono);
  }
  .code-browser-line {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    margin: 0;
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 20px;
    color: var(--diff-text);
    background: transparent;
    border: none;
  }
  /* Token colors via CSS custom properties — theme switch is pure CSS,
     no JS re-renders needed. Each span carries --dc (dark) and --lc (light). */
  .code-browser-line span {
    color: var(--lc, inherit);
  }
  :global(html.dark) .code-browser-line span {
    color: var(--dc, inherit);
  }
</style>
