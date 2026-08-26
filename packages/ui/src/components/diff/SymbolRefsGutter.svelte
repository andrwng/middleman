<script lang="ts">
  import { onMount } from "svelte";
  import { getStores } from "../../context.js";
  import { scrollToDiffLine, clearDiffLineHighlight, type DiffJumpDeps } from "./scrollToDiffLine.js";
  import type { SymbolHit } from "../../stores/symbolRefs.svelte.js";

  type SymbolTag = NonNullable<SymbolHit["tag"]>;

  interface Props {
    owner: string;
    name: string;
    number: number;
    // Column width in px, resolved and persisted by DiffView (the
    // resize handle lives there, not here).
    width: number;
  }

  const { owner, name, number, width }: Props = $props();

  const { symbolRefs: symbolRefsStore, diff: diffStore } = getStores();

  // This component's mounted lifetime is exactly the gutter's open
  // state (DiffView only renders it while symbolRefsStore.isActive()),
  // so its teardown is the one place that covers every way the gutter
  // can close — the close button, an auto-close on scope/SHA drift,
  // or the diff view itself going away — without each of those call
  // sites needing to remember to clear the jump highlight themselves.
  onMount(() => {
    return () => clearDiffLineHighlight();
  });

  const query = $derived(symbolRefsStore.getQuery());
  const hits = $derived(symbolRefsStore.getHits());
  const inPrTotal = $derived(symbolRefsStore.getInPrTotal());
  const outsidePrTotal = $derived(symbolRefsStore.getOutsidePrTotal());
  const truncated = $derived(symbolRefsStore.isTruncated());
  const classifier = $derived(symbolRefsStore.getClassifier());
  const status = $derived(symbolRefsStore.getStatus());
  const error = $derived(symbolRefsStore.getError());

  // Classify.go's noisy kinds: real code hits (definition/reference/import)
  // are shown directly; comment/string hits are collapsed behind a toggle
  // so they don't crowd out the signal.
  const NOISY_KINDS = new Set(["comment", "string"]);

  interface FileGroup {
    path: string;
    hits: SymbolHit[];
  }

  // groupConsecutive buckets adjacent same-path hits under one header,
  // WITHOUT gathering all hits for a path across the whole list: the
  // server sorts by kind first, so one file's definition hit and
  // reference hit are not adjacent, and each occurrence of the file
  // becomes its own group in the order the store returned them. Do not
  // "fix" this into a single per-file group — that would re-sort/re-group
  // hits beyond the comments/strings split, which the server's ordering
  // already accounts for.
  function groupConsecutive(list: SymbolHit[]): FileGroup[] {
    const groups: FileGroup[] = [];
    for (const h of list) {
      const last = groups[groups.length - 1];
      if (last && last.path === h.path) {
        last.hits.push(h);
      } else {
        groups.push({ path: h.path, hits: [h] });
      }
    }
    return groups;
  }

  const mainGroups = $derived(
    groupConsecutive(hits.filter((h) => !NOISY_KINDS.has(h.kind))),
  );
  const noisyHits = $derived(hits.filter((h) => NOISY_KINDS.has(h.kind)));
  const noisyGroups = $derived(groupConsecutive(noisyHits));

  // The server partitions hits against the whole PR's changed-file set,
  // but the rendered diff may only cover part of that (a single commit
  // or a range). A hit whose path isn't part of what's on screen right
  // now can never resolve when clicked — findDiffLineEl and the
  // file-header fallback both look for a `.diff-file` that doesn't
  // exist. renderedPaths lets rows say so up front rather than only
  // after a click that silently does nothing.
  const renderedPaths = $derived(
    new Set((diffStore.getDiff()?.files ?? []).map((f) => f.path)),
  );

  // Set to the (path, line) of a hit whose jump just resolved as
  // "missing" (scrollToDiffLine.ts), so a brief explanation can render
  // next to that row. Cleared at the start of every jump attempt.
  let missingJump = $state<{ path: string; line: number } | null>(null);

  function isMissingJump(hit: SymbolHit): boolean {
    return (
      missingJump !== null &&
      missingJump.path === hit.path &&
      missingJump.line === hit.line
    );
  }

  // Collapsed by default; resets whenever the store hands back a fresh
  // result set. The store always reassigns a new `hits` array reference
  // at the start of search() (even for a repeat query), so depending on
  // it (rather than on `query`) resets this on every new search.
  let noisyExpanded = $state(false);
  $effect(() => {
    // Reading `hits` (always truthy — it's an array) is what creates the
    // reactive dependency here; the point is the read, not the value, so
    // this unconditionally re-collapses the section on every new search.
    if (hits) {
      noisyExpanded = false;
      // A fresh result set invalidates any previous jump failure: even
      // if it includes the same (path, line), nobody has clicked it in
      // THIS result set, so no row should show "not part of the
      // rendered diff" yet.
      missingJump = null;
    }
  });

  // A tagged hit's row body shows the qualified name ctags found --
  // scope::symbol plus the signature, e.g. "Foo::bar(int x)" -- rather
  // than the raw matched line. The `::` only belongs between a present
  // scope and the symbol, so an empty scope must not leave one dangling,
  // and a missing signature must not leave a trailing gap either.
  function taggedLabel(tag: SymbolTag, symbol: string): string {
    const scope = tag.scope ? `${tag.scope}::` : "";
    return `${scope}${symbol}${tag.signature ?? ""}`;
  }

  function kindLabel(kind: string): string {
    switch (kind) {
      case "definition":
        return "def";
      case "reference":
        return "ref";
      case "import":
        return "import";
      case "comment":
        return "comment";
      case "string":
        return "string";
      default:
        return kind;
    }
  }

  async function jumpTo(hit: SymbolHit): Promise<void> {
    missingJump = null;
    const deps: DiffJumpDeps = {
      isFileCollapsed: (path) => diffStore.isFileCollapsed(owner, name, number, path),
      toggleFileCollapsed: (path) => diffStore.toggleFileCollapsed(owner, name, number, path),
      requestRevealLine: (path, line) => diffStore.requestRevealLine(path, line),
      clearRevealTarget: () => diffStore.consumeRevealTarget(),
    };
    const outcome = await scrollToDiffLine({ path: hit.path, line: hit.line }, deps);
    if (outcome === "missing") {
      missingJump = { path: hit.path, line: hit.line };
    }
  }

  function toggleNoisy(): void {
    noisyExpanded = !noisyExpanded;
  }
</script>

{#snippet fileGroup(group: FileGroup)}
  <div class="symref-group">
    <div class="symref-group__header">
      <span class="symref-group__path" title={group.path}>{group.path}</span>
      {#if !renderedPaths.has(group.path)}
        <span
          class="symref-group__not-in-view"
          title="This file isn't part of the diff currently shown — change scope to see it"
        >not in this view</span>
      {/if}
      <span class="symref-group__count">{group.hits.length}</span>
    </div>
    {#each group.hits as hit, i (i)}
      <button
        type="button"
        class="symref-row"
        class:symref-row--definition={hit.kind === "definition"}
        onclick={() => void jumpTo(hit)}
        title={hit.text}
      >
        <span class="symref-row__line">{hit.line}</span>
        <span class="symref-row__kind symref-row__kind--{hit.kind}">{hit.tag ? hit.tag.kind : kindLabel(hit.kind)}</span>
        <span class="symref-row__text">{hit.tag ? taggedLabel(hit.tag, query) : hit.text}</span>
      </button>
      {#if isMissingJump(hit)}
        <div class="symref-row__notice">Not part of the rendered diff — nothing to jump to.</div>
      {/if}
    {/each}
  </div>
{/snippet}

<div
  class="symref-gutter"
  style:width="{width}px"
  role="complementary"
  aria-label="Symbol references"
>
  <div class="symref-header">
    <span class="symref-header__query" title={query}>{query}</span>
    <span class="symref-header__count" title="Occurrences in this PR's changed files">{inPrTotal}</span>
    <button
      type="button"
      class="symref-header__close"
      onclick={() => symbolRefsStore.close()}
      aria-label="Close symbol references"
      title="Close"
    >
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M3 3L13 13M13 3L3 13" stroke-linecap="round" />
      </svg>
    </button>
  </div>

  {#if classifier === "heuristic"}
    <div class="symref-degraded-note">
      Kind labels are heuristic — install universal-ctags for exact kinds.
    </div>
  {/if}

  <div class="symref-body">
    {#if status === "loading"}
      <div class="symref-state">Searching…</div>
    {:else if status === "error"}
      <div class="symref-state symref-state--error">{error ?? "Symbol search failed"}</div>
    {:else}
      {#if hits.length === 0}
        <div class="symref-state">
          No other occurrences of <span class="symref-state__query">{query}</span> in this PR's
          changed files.
        </div>
      {:else}
        {#each mainGroups as group, i (i)}
          {@render fileGroup(group)}
        {/each}

        {#if noisyGroups.length > 0}
          <button type="button" class="symref-toggle" onclick={toggleNoisy}>
            <span
              class="symref-toggle__chevron"
              class:symref-toggle__chevron--open={noisyExpanded}
              aria-hidden="true"
            >&#8250;</span>
            {noisyHits.length} in comments/strings
          </button>
          {#if noisyExpanded}
            {#each noisyGroups as group, i (i)}
              {@render fileGroup(group)}
            {/each}
          {/if}
        {/if}

        {#if truncated}
          <div class="symref-note">The list was capped at 500 matches.</div>
        {/if}
      {/if}

      {#if outsidePrTotal > 0}
        <div class="symref-footer">+{outsidePrTotal} elsewhere in the repo</div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .symref-gutter {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--diff-bg);
    border-left: 1px solid var(--diff-border);
    overflow: hidden;
  }

  .symref-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px 6px 12px;
    background: var(--diff-header-bg);
    border-bottom: 1px solid var(--diff-border);
    flex-shrink: 0;
  }

  .symref-header__query {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-weight: 600;
    font-size: 12px;
    color: var(--diff-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .symref-header__count {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .symref-header__close {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
  }

  .symref-header__close:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .symref-degraded-note {
    flex-shrink: 0;
    padding: 4px 12px;
    font-size: 11px;
    color: var(--accent-amber);
    background: var(--diff-header-bg);
    border-bottom: 1px solid var(--diff-border);
  }

  .symref-body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .symref-state {
    padding: 16px 12px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
  }

  .symref-state--error {
    color: var(--accent-red);
    font-style: normal;
  }

  .symref-state__query {
    font-family: var(--font-mono);
    font-style: normal;
    color: var(--diff-text);
  }

  .symref-group__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px 2px 12px;
    background: var(--bg-inset);
  }

  .symref-group__path {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    /* Ellipsize from the left: an RTL base direction truncates the
       start of the (LTR) path text and keeps the end — the basename —
       visible, instead of the reverse. */
    direction: rtl;
    text-align: left;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .symref-group__count {
    flex-shrink: 0;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }

  /* Mirrors DiffFile's .outdated-banner treatment: an amber, help-cursor
     badge for a row whose target doesn't resolve in the diff on screen. */
  .symref-group__not-in-view {
    flex-shrink: 0;
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding: 0 5px;
    border-radius: 999px;
    color: var(--accent-amber);
    border: 1px solid color-mix(in srgb, var(--accent-amber) 40%, var(--border-muted));
    cursor: help;
    white-space: nowrap;
  }

  .symref-row__notice {
    padding: 1px 8px 4px 12px;
    font-size: 10px;
    font-style: italic;
    color: var(--accent-amber);
  }

  .symref-row {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 2px 8px 2px 12px;
    text-align: left;
    color: var(--text-secondary);
  }

  .symref-row:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .symref-row__line {
    flex-shrink: 0;
    min-width: 3ch;
    text-align: right;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }

  .symref-row__kind {
    flex-shrink: 0;
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding: 0 5px;
    border-radius: 999px;
    color: var(--text-muted);
    border: 1px solid var(--border-muted);
  }

  .symref-row__kind--definition {
    color: #fff;
    background: var(--accent-blue);
    border-color: var(--accent-blue);
  }

  .symref-row__text {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 11px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .symref-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 8px 4px 12px;
    text-align: left;
    color: var(--text-muted);
    font-size: 11px;
  }

  .symref-toggle:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .symref-toggle__chevron {
    display: inline-flex;
    transition: transform 0.15s;
  }

  .symref-toggle__chevron--open {
    transform: rotate(90deg);
  }

  .symref-note {
    padding: 6px 12px;
    font-size: 11px;
    color: var(--accent-amber);
  }

  .symref-footer {
    padding: 6px 12px;
    font-size: 11px;
    color: var(--text-muted);
    border-top: 1px solid var(--border-muted);
  }
</style>
