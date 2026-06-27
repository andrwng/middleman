<script lang="ts">
  import { tick } from "svelte";
  import { getNavigate, getStores } from "../../context.js";

  interface Props {
    owner: string;
    name: string;
    number: number;
    basePath: string;
    open: boolean;
    onClose: () => void;
  }

  let { owner, name, number, basePath, open, onClose }: Props = $props();

  const navigate = getNavigate();
  const { worktrees } = getStores();

  let files = $state<string[]>([]);
  let query = $state("");
  let highlightIndex = $state(0);
  let inputEl = $state<HTMLInputElement>();

  // Lower score = better; null = no match. Empty query matches everything.
  function fuzzyScore(text: string, q: string): number | null {
    if (q === "") return 0;
    const t = text.toLowerCase();
    const query = q.toLowerCase();
    let from = 0, score = 0, last = -1;
    for (const ch of query) {
      const idx = t.indexOf(ch, from);
      if (idx === -1) return null;
      if (last >= 0) score += idx - last; // reward compact matches
      last = idx;
      from = idx + 1;
    }
    return score;
  }

  const filtered = $derived.by(() => {
    if (!query) {
      return [...files].sort((a, b) => a.localeCompare(b));
    }
    return files
      .map((f) => ({ f, s: fuzzyScore(f, query) }))
      .filter((x) => x.s !== null)
      .sort((a, b) => a.s! - b.s! || a.f.localeCompare(b.f))
      .map((x) => x.f);
  });

  function docRouteFor(f: string): string {
    return (
      "/pulls/" +
      owner +
      "/" +
      name +
      "/" +
      number +
      "/doc?path=" +
      encodeURIComponent(f)
    );
  }

  function docHrefFor(f: string): string {
    return (
      basePath.replace(/\/$/, "") +
      "/pulls/" +
      owner +
      "/" +
      name +
      "/" +
      number +
      "/doc?path=" +
      encodeURIComponent(f)
    );
  }

  function onRowClick(e: MouseEvent, f: string) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button === 1) {
      // Modified click: let the browser open a new tab natively.
      return;
    }
    e.preventDefault();
    navigate(docRouteFor(f));
    onClose();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlightIndex = Math.min(highlightIndex + 1, filtered.length - 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlightIndex = Math.max(highlightIndex - 1, 0);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const f = filtered[highlightIndex];
      if (f) {
        navigate(docRouteFor(f));
        onClose();
      }
    } else if (e.key === "Escape") {
      onClose();
    }
  }

  function handleInput() {
    highlightIndex = 0;
  }

  $effect(() => {
    if (!open) return;
    // Reset state on each open.
    query = "";
    highlightIndex = 0;
    void worktrees.loadMarkdownFiles(number).then((result) => {
      files = result;
    });
    void tick().then(() => inputEl?.focus());
  });
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="palette-backdrop"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label="Open a doc"
    onmousedown={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}
  >
    <div class="palette-panel">
      <input
        bind:this={inputEl}
        class="palette-input"
        type="text"
        bind:value={query}
        oninput={handleInput}
        onkeydown={handleKeydown}
        placeholder="Search docs..."
        aria-label="Search docs"
        autocomplete="off"
      />
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <ul class="palette-list" role="listbox">
        {#each filtered as f, i}
          <li
            class="palette-option"
            class:highlighted={i === highlightIndex}
            role="option"
            aria-selected={i === highlightIndex}
            onmouseenter={() => (highlightIndex = i)}
          >
            <a
              href={docHrefFor(f)}
              class="palette-row-link"
              onclick={(e) => onRowClick(e, f)}
            >
              <span class="palette-row-path">{f}</span>
            </a>
            <a
              href={docHrefFor(f)}
              class="palette-row-newtab"
              target="_blank"
              rel="noopener"
              title="Open in new tab"
              tabindex="-1"
            >
              ↗
            </a>
          </li>
        {:else}
          <li class="palette-empty">No matching docs</li>
        {/each}
      </ul>
    </div>
  </div>
{/if}

<style>
  .palette-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    z-index: 200;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding-top: 80px;
  }

  .palette-panel {
    background: var(--bg-surface);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-md, 6px);
    box-shadow: var(--shadow-lg);
    width: min(560px, 90vw);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .palette-input {
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

  .palette-input::placeholder {
    color: var(--text-muted);
  }

  .palette-list {
    list-style: none;
    padding: 4px;
    margin: 0;
    max-height: 320px;
    overflow-y: auto;
  }

  .palette-option {
    display: flex;
    align-items: center;
    border-radius: 4px;
  }

  .palette-option.highlighted {
    background: var(--bg-surface-hover);
  }

  .palette-row-link {
    flex: 1;
    padding: 6px 8px;
    font-size: 12px;
    color: var(--text-secondary);
    text-decoration: none;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
  }

  .palette-option.highlighted .palette-row-link {
    color: var(--text-primary);
  }

  .palette-row-path {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .palette-row-newtab {
    font-size: 11px;
    color: var(--text-muted);
    padding: 4px 8px;
    text-decoration: none;
    flex-shrink: 0;
    opacity: 0;
    transition: opacity 0.1s;
  }

  .palette-option:hover .palette-row-newtab,
  .palette-option.highlighted .palette-row-newtab {
    opacity: 1;
  }

  .palette-empty {
    padding: 8px 10px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }
</style>
