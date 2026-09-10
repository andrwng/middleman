<script lang="ts">
  import { tick, untrack } from "svelte";
  import { getStores } from "../../context.js";
  import { tokenizeLineDual, langFromPath, type DualToken } from "../../utils/highlight.js";
  import { fuzzyFilter } from "../../utils/fuzzy.js";

  // A request to scroll to and flash a specific line once `path` matches
  // the panel's currently-open file. `nonce` lets DiffView issue a second
  // request for the same path/line (e.g. clicking the same symbol-refs hit
  // twice) and still have it re-trigger, since {path, line} alone wouldn't
  // change.
  export interface RevealRequest {
    path: string;
    line: number;
    nonce: number;
  }

  interface Props {
    owner: string;
    name: string;
    number: number;
    sha: string;
    initialPath: string;
    // Bypasses any saved bookmark so the panel opens at initialPath
    // regardless -- set by DiffView when opening via the symbol-refs
    // gutter's "browse this hit" action, where initialPath is a
    // specific, deliberately-chosen file rather than a mere fallback
    // default. Left false for the ordinary toolbar/`b`-hotkey open,
    // where resuming at the bookmark is the whole point.
    forcePath?: boolean;
    // See RevealRequest. Consulted once per nonce via onRevealed below.
    reveal?: RevealRequest | undefined;
    onRevealed?: () => void;
    onclose: () => void;
  }

  const {
    owner,
    name,
    number,
    sha,
    initialPath,
    forcePath = false,
    reveal,
    onRevealed,
    onclose,
  }: Props = $props();

  const { codeBrowser: browser } = getStores();

  // Only `sha` is a tracked dependency here -- stepping a commit while the
  // panel stays open is the one case that must re-seed the browser (same
  // sticky path, new SHA). `initialPath`/`forcePath` are read through
  // untrack() so they are consulted exactly once per DiffView-initiated
  // "open" (DiffView only reassigns its seed-path state on an explicit
  // open trigger -- the `b` key, the toolbar button, or a symbol-refs
  // browse action -- never as a side effect of j/k file navigation or any
  // other unrelated diff-state change). Without untrack() here, this
  // effect would depend on `diffStore.getActiveFile()` transitively
  // whenever DiffView passed a live expression for initialPath, and j/k
  // (which updates activeFile) would silently re-seed the panel to a
  // different file out from under the reader.
  $effect(() => {
    const s = sha;
    const p = untrack(() => initialPath);
    const fp = untrack(() => forcePath);
    if (fp) {
      void browser.open(owner, name, number, s, p, { forcePath: true });
    } else {
      void browser.open(owner, name, number, s, p);
    }
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
    // A new file means any highlighted line belonged to the previous one
    // -- clear it here rather than on a timer (see the persistent-vs-timed
    // rationale on flashedLine below). If a reveal request is pending for
    // THIS file, the reveal effect (which runs after `lines` updates)
    // re-sets it immediately after.
    flashedLine = null;
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

  // --- Directory browsing (GitHub-style breadcrumb drill-down) ---
  //
  // One directory's entries are shown at a time, with a clickable
  // breadcrumb path above -- matching GitHub's own repo browser, and
  // replacing an earlier fully-nested, indented tree that read as
  // visually noisy at any real depth.
  //
  // currentDir tracks the directory currently listed; it is local state,
  // not stored on the browser store, since drilling into a folder isn't
  // "opening a file" and has no bookmark/reveal semantics of its own.
  // Whenever the OPEN FILE changes (browser.path), currentDir follows it
  // to that file's containing directory -- open()'s ancestor-loading loop
  // already populated entriesByDir for every ancestor of the opened path,
  // so this never needs its own fetch on that path. Pure folder browsing
  // (clicking a directory entry or a breadcrumb segment) only changes
  // currentDir locally and never touches browser.path.
  let currentDir = $state("");

  $effect(() => {
    const p = browser.path;
    if (p == null) return;
    const idx = p.lastIndexOf("/");
    const dir = idx === -1 ? "" : p.slice(0, idx);
    if (dir === currentDir) return;
    currentDir = dir;
    if (!browser.entriesByDir.has(dir)) void browser.loadTree(dir);
  });

  function openDir(path: string): void {
    currentDir = path;
    if (!browser.entriesByDir.has(path)) void browser.loadTree(path);
  }

  // Breadcrumb segments from the repo root (labeled with the repo name,
  // matching GitHub's own breadcrumb) down to currentDir.
  const breadcrumb = $derived.by(() => {
    const segs: { label: string; path: string }[] = [{ label: name, path: "" }];
    if (currentDir === "") return segs;
    let acc = "";
    for (const part of currentDir.split("/")) {
      acc = acc ? `${acc}/${part}` : part;
      segs.push({ label: part, path: acc });
    }
    return segs;
  });

  const currentEntries = $derived(browser.entriesByDir.get(currentDir));

  // --- Jump-to-line reveal (symbol-refs "browse this hit") ---
  //
  // Matches scrollToDiffLine.ts's flashDiffLine as closely as this
  // panel's flat (non-virtualized) rendering allows: smooth scrolling,
  // and a highlight that's cleared by the next navigation event rather
  // than a timer. flashDiffLine's own comment explains why a timed flash
  // is wrong here too: a long smooth-scroll can still be travelling when
  // a fixed timeout fires, so the highlight decays before -- or during --
  // the landing it was meant to mark.

  let flashedLine = $state<number | null>(null);
  let contentEl = $state<HTMLDivElement | undefined>();
  let lastHandledRevealNonce = -1;

  // Waits for `reveal`'s target path to actually be the open file (it may
  // be issued before navigateTo's fetch resolves) and for that file's
  // lines to be in the DOM, then scrolls to and flashes the line. Doesn't
  // wait for tokenizing -- the row exists in the DOM as soon as `lines` is
  // populated, well before tokens finish.
  $effect(() => {
    if (!reveal || reveal.nonce === lastHandledRevealNonce) return;
    if (reveal.path !== browser.path || lines.length === 0) return;
    lastHandledRevealNonce = reveal.nonce;
    const line = reveal.line;
    flashedLine = line;
    void tick().then(() => {
      const el = contentEl?.querySelector(`[data-line="${line}"]`);
      el?.scrollIntoView?.({ block: "center", behavior: "smooth" });
    });
    onRevealed?.();
  });

  // --- Fuzzy file finder ("Go to file") ---

  let paletteOpen = $state(false);
  let paletteQuery = $state("");
  let paletteHighlight = $state(0);
  let paletteFiles = $state<string[]>([]);
  let paletteInputEl = $state<HTMLInputElement>();

  const paletteFiltered = $derived.by(() => fuzzyFilter(paletteFiles, paletteQuery));

  function openPalette(): void {
    paletteOpen = true;
    paletteQuery = "";
    paletteHighlight = 0;
    void browser.listAllFiles().then((files) => {
      paletteFiles = files;
    });
    void tick().then(() => paletteInputEl?.focus());
  }

  function closePalette(): void {
    paletteOpen = false;
  }

  function goToFile(path: string): void {
    void browser.navigateTo(path);
    closePalette();
  }

  function onPaletteKeydown(e: KeyboardEvent): void {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (paletteFiltered.length === 0) return;
      paletteHighlight = Math.min(paletteHighlight + 1, paletteFiltered.length - 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (paletteFiltered.length === 0) return;
      paletteHighlight = Math.max(paletteHighlight - 1, 0);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const f = paletteFiltered[paletteHighlight];
      if (f) goToFile(f);
    } else if (e.key === "Escape") {
      closePalette();
    }
  }

  // Bound to window (like DiffView's own j/k/[/]/m/s/b handling), not
  // just this component's root element: an onkeydown attribute only
  // fires when focus is already somewhere inside this subtree, and
  // opening the panel doesn't focus anything inside it, so a plain
  // element-scoped listener silently never saw the keypress. `t` mirrors
  // GitHub's own repo file-finder shortcut; `/` is the more common
  // "search/find" convention and was requested alongside it. DiffView's
  // own shortcuts don't use either key, so there's no conflict despite
  // both listening on window.
  function onWindowKeydown(e: KeyboardEvent): void {
    if (paletteOpen) return;
    const tag = (e.target as HTMLElement).tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "t" || e.key === "/") {
      e.preventDefault();
      openPalette();
    }
  }

  $effect(() => {
    window.addEventListener("keydown", onWindowKeydown);
    return () => window.removeEventListener("keydown", onWindowKeydown);
  });
</script>

<div class="code-browser-panel">
  <div class="code-browser-header">
    <span class="code-browser-title">{browser.path ?? "Browse files"}</span>
    <div class="code-browser-header-actions">
      <button type="button" class="code-browser-goto-btn" onclick={openPalette} title="Go to file (t or /)">
        Go to file
      </button>
      <button type="button" onclick={() => { browser.close(); onclose(); }}>Close</button>
    </div>
  </div>
  <div class="code-browser-body">
    <nav class="code-browser-tree">
      <div class="code-browser-breadcrumb">
        {#each breadcrumb as seg, i (seg.path)}
          {#if i > 0}<span class="code-browser-breadcrumb-sep">/</span>{/if}
          <button
            type="button"
            class="code-browser-breadcrumb-seg"
            disabled={seg.path === currentDir}
            onclick={() => openDir(seg.path)}
          >
            {seg.label}
          </button>
        {/each}
      </div>
      {#if currentEntries === undefined}
        <p class="code-browser-tree-empty">Loading…</p>
      {:else}
        {#each currentEntries as entry (entry.path)}
          {#if entry.type === "dir"}
            <button
              type="button"
              class="code-browser-entry code-browser-entry--dir"
              onclick={() => openDir(entry.path)}
            >
              <span class="code-browser-entry-icon">📁</span>
              <span class="code-browser-entry-name">{entry.name}</span>
            </button>
          {:else}
            <button
              type="button"
              class="code-browser-entry code-browser-entry--file"
              class:code-browser-entry--active={browser.path === entry.path}
              onclick={() => browser.navigateTo(entry.path)}
            >
              <span class="code-browser-entry-icon">📄</span>
              <span class="code-browser-entry-name">{entry.name}</span>
            </button>
          {/if}
        {:else}
          <p class="code-browser-tree-empty">Empty directory.</p>
        {/each}
      {/if}
    </nav>
    <div class="code-browser-content" bind:this={contentEl}>
      {#if browser.status === "missing"}
        <p class="code-browser-empty">This file doesn't exist at this commit.</p>
      {:else if browser.status === "loading"}
        <p class="code-browser-empty">Loading…</p>
      {:else if browser.status === "truncated"}
        <p class="code-browser-empty">This file is too large to display.</p>
      {:else if browser.status === "error"}
        <p class="code-browser-empty">{browser.error ?? "Failed to load file"}</p>
      {:else}
        <div class="code-browser-file">
          {#each lines as line, i (i)}
            <div
              class="code-browser-line-row"
              class:code-browser-line-row--flash={i + 1 === flashedLine}
              data-line={i + 1}
            >
              <span class="code-browser-line-num">{i + 1}</span>
              <pre class="code-browser-line">{#each tokens.get(i) ?? [{ content: line }] as span}<span style:--dc={span.darkColor} style:--lc={span.lightColor}>{span.content}</span>{/each}</pre>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>

{#if paletteOpen}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="code-browser-palette-backdrop"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label="Go to file"
    onmousedown={(e) => {
      if (e.target === e.currentTarget) closePalette();
    }}
  >
    <div class="code-browser-palette-panel">
      <input
        bind:this={paletteInputEl}
        class="code-browser-palette-input"
        type="text"
        bind:value={paletteQuery}
        oninput={() => (paletteHighlight = 0)}
        onkeydown={onPaletteKeydown}
        placeholder="Go to file…"
        aria-label="Go to file"
        autocomplete="off"
      />
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <ul class="code-browser-palette-list" role="listbox">
        {#each paletteFiltered as f, i (f)}
          <li
            class="code-browser-palette-option"
            class:highlighted={i === paletteHighlight}
            role="option"
            aria-selected={i === paletteHighlight}
            onmouseenter={() => (paletteHighlight = i)}
          >
            <button type="button" class="code-browser-palette-row" onclick={() => goToFile(f)}>
              {f}
            </button>
          </li>
        {:else}
          <li class="code-browser-palette-empty">No matching files</li>
        {/each}
      </ul>
    </div>
  </div>
{/if}

<style>
  .code-browser-panel {
    /* Fills the same slot the diff area occupied -- it replaces the
       diff (hidden while the panel is open), it does not shrink to
       share space with it. */
    flex: 1;
    min-width: 0;
    height: 100%;
    background: var(--diff-bg);
    display: flex;
    flex-direction: column;
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
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .code-browser-header-actions {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }
  .code-browser-goto-btn {
    font-size: 11px;
  }
  .code-browser-body {
    flex: 1;
    display: flex;
    overflow: hidden;
  }
  .code-browser-tree {
    width: 220px;
    flex-shrink: 0;
    overflow-y: auto;
    border-right: 1px solid var(--diff-border);
    display: flex;
    flex-direction: column;
    padding: 4px 0;
  }
  .code-browser-breadcrumb {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px;
    padding: 4px 8px 8px;
    margin-bottom: 4px;
    border-bottom: 1px solid var(--diff-border);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .code-browser-breadcrumb-sep {
    color: var(--text-muted);
  }
  .code-browser-breadcrumb-seg {
    background: none;
    border: none;
    cursor: pointer;
    padding: 1px 3px;
    border-radius: 3px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
  }
  .code-browser-breadcrumb-seg:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }
  .code-browser-breadcrumb-seg:disabled {
    color: var(--text-primary);
    font-weight: 600;
    cursor: default;
  }
  .code-browser-tree-empty {
    padding: 4px 8px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
  }
  .code-browser-entry {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    text-align: left;
    padding: 3px 8px;
    background: none;
    border: none;
    cursor: pointer;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    transition: background 0.15s ease;
  }
  .code-browser-entry:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }
  .code-browser-entry--active {
    background: color-mix(in srgb, var(--accent-blue) 10%, transparent);
    color: var(--text-primary);
    font-weight: 600;
  }
  .code-browser-entry-icon {
    flex-shrink: 0;
    font-size: 12px;
    line-height: 1;
  }
  .code-browser-content {
    flex: 1;
    overflow: auto;
    padding: 8px 0;
  }
  .code-browser-empty {
    font-family: var(--font-mono);
    padding: 0 12px;
  }
  .code-browser-file {
    font-family: var(--font-mono);
  }
  .code-browser-line-row {
    display: flex;
    align-items: stretch;
    transition: background 0.2s ease;
  }
  .code-browser-line-row--flash {
    background: color-mix(in srgb, var(--accent-amber) 25%, transparent);
  }
  .code-browser-line-num {
    width: 44px;
    flex-shrink: 0;
    text-align: right;
    padding: 0 8px 0 0;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--diff-line-num);
    user-select: none;
    line-height: 20px;
    background: var(--diff-bg);
  }
  .code-browser-line {
    flex: 1;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    margin: 0;
    padding: 0 12px 0 8px;
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

  .code-browser-palette-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    z-index: 200;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding-top: 80px;
  }
  .code-browser-palette-panel {
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md, 6px);
    box-shadow: var(--shadow-lg);
    width: min(560px, 90vw);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .code-browser-palette-input {
    padding: 10px 14px;
    font-size: 13px;
    color: var(--text-primary);
    background: transparent;
    border: none;
    border-bottom: 1px solid var(--border-default);
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }
  .code-browser-palette-list {
    list-style: none;
    padding: 4px;
    margin: 0;
    max-height: 320px;
    overflow-y: auto;
  }
  .code-browser-palette-option {
    border-radius: 4px;
  }
  .code-browser-palette-option.highlighted {
    background: var(--bg-surface-hover);
  }
  .code-browser-palette-row {
    width: 100%;
    text-align: left;
    padding: 6px 8px;
    font-size: 12px;
    font-family: var(--font-mono);
    color: var(--text-secondary);
    background: none;
    border: none;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
  }
  .code-browser-palette-option.highlighted .code-browser-palette-row {
    color: var(--text-primary);
  }
  .code-browser-palette-empty {
    padding: 8px 10px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
