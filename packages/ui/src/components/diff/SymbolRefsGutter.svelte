<script lang="ts">
  import { onMount } from "svelte";
  import { getStores } from "../../context.js";
  import {
    scrollToDiffLine,
    clearDiffLineHighlight,
    currentDiffPosition,
    highlightedDiffPosition,
    type DiffJumpDeps,
  } from "./scrollToDiffLine.js";
  import { isSymbolQuery, type SymbolHit } from "../../stores/symbolRefs.svelte.js";
  import { langFromPath } from "../../utils/highlight.js";

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

  const focusSeq = $derived(symbolRefsStore.getFocusSeq());
  const canGoBack = $derived(symbolRefsStore.canGoBack());

  // The text being typed, kept separate from the store's committed query
  // so an abandoned edit never changes what is displayed as searched.
  // Seeded from the store on mount and re-seeded whenever a search
  // commits a different query (e.g. the selection-toolbar Refs button
  // searching while this gutter is already open).
  let draft = $state(symbolRefsStore.getQuery());
  let invalidReason = $state<string | null>(null);
  let inputEl = $state<HTMLInputElement>();

  $effect(() => {
    const committed = symbolRefsStore.getQuery();
    if (committed === "") return;
    draft = committed;
    // A newly committed query makes both of this gutter's notices stale:
    // they describe a refused query or a failed Back from before, and
    // leaving either pinned above a fresh result set reads as a complaint
    // about the results now on screen.
    invalidReason = null;
    backNote = null;
  });

  // Focus and select on every focusSeq change, so pressing `s` (or the
  // toolbar button) with results already showing puts the cursor in the
  // box with the old query selected -- ready to be replaced by typing,
  // without the results having been discarded.
  //
  // The last-seen guard is what makes focusSeq a read with meaning: a bare
  // `focusSeq;` expression statement would register the dependency too,
  // but eslint's no-unused-expressions would reject it and fail
  // make frontend-check.
  //
  // Only openBlank() asks for focus, and the seed is what keeps the
  // effect's unavoidable first run (at mount) from counting as a request.
  // Seeding it from the store rather than from -1 is the whole point: a
  // mount caused by the selection-side Refs button -- DiffFile's floating
  // toolbar calls search() directly and never bumps focusSeq -- must leave
  // focus where the reader put it. Stealing it there would put an INPUT
  // under DiffView's window keydown handler, which ignores keys while one
  // is focused, silently dropping j/k/[/]/m/s until the reader clicks away.
  let lastFocusSeq =
    symbolRefsStore.getStatus() === "prompt"
      ? -1 // this mount IS an openBlank (the only thing that sets "prompt")
      : symbolRefsStore.getFocusSeq(); // someone else opened us; do not focus
  $effect(() => {
    if (focusSeq === lastFocusSeq) return;
    lastFocusSeq = focusSeq;
    inputEl?.focus();
    inputEl?.select();
  });

  // whyInvalid explains a rejected query in the terms isSymbolQuery
  // actually enforces. Whitespace is called out by name because it is the
  // only rule a reasonable person would trip by accident.
  function whyInvalid(text: string): string | null {
    const t = text.trim();
    if (t.length === 0) return null;
    if (/\s/.test(t)) return "A symbol cannot contain whitespace.";
    if (!isSymbolQuery(t)) return "That symbol is too long to search.";
    return null;
  }

  function submitSearch(): void {
    const t = draft.trim();
    if (t.length === 0) return;
    const reason = whyInvalid(t);
    if (reason !== null) {
      invalidReason = reason;
      return;
    }
    // The toolbar's Refs button and the `s` key both refuse to open
    // without a resolvable commit (a Refresh in flight leaves the SHA
    // empty), so Enter refuses for the same reason and says the same
    // thing -- rather than issuing a request the server rejects with a
    // bare "sha is required" that lands in the gutter verbatim.
    const sha = diffStore.getCurrentCommitSha();
    if (sha === "") {
      invalidReason = "This diff scope has no resolvable commit to search.";
      return;
    }
    invalidReason = null;
    void symbolRefsStore.search(owner, name, number, sha, t);
  }

  function onSearchKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter") {
      e.preventDefault();
      submitSearch();
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      symbolRefsStore.close();
    }
  }

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

  // A short note shown when Back's popped target no longer resolves in
  // the rendered diff. Distinct from missingJump above, which marks a
  // specific row -- Back has no row of its own, so its failure gets a
  // one-line note in the header area instead.
  let backNote = $state<string | null>(null);

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
  // Which punctuation joins an enclosing scope to the symbol it
  // contains is the language's business, not ours: C++ qualifies with
  // "::" while Go, Python, TypeScript and the rest use ".". ctags
  // already formats the scope string itself per language (C++ gives
  // "kafka::handler_template", Go gives "main.Cache"), so the only
  // choice left is the separator we add between scope and name.
  //
  // langFromPath routes every C-family extension to "cpp", so this one
  // comparison covers .c/.h/.cc/.cpp/.cxx/.hpp and friends. Rust also
  // qualifies with "::" but maps to "rust"; add it here if Rust ever
  // lands in the tree.
  function scopeSeparator(path: string): string {
    return langFromPath(path) === "cpp" ? "::" : ".";
  }

  // ctags names an anonymous namespace — and an anonymous struct or
  // union — with a mangled placeholder: "__anon" plus a hex run derived
  // from the file, e.g. "__anon373dda250111". That placeholder lands in
  // the scope string verbatim ("redpanda::__anondf446e100111::varint"),
  // where it carries nothing a reader wants.
  //
  // Matched as a whole component, with a floor of 8 hex digits so a real
  // identifier cannot be taken for one: "__anonymous_helper" fails on the
  // "y", and a hypothetical "__anonface" has only 4 hex digits. Observed
  // output uses 12; the floor tolerates that changing.
  const ANON_SCOPE_COMPONENT = /^__anon[0-9a-f]{8,}$/;

  // sanitizeScope rewrites the anonymous components of a scope.
  //
  // The FIRST component becomes empty, leaving the scope with a leading
  // separator — "::do_transform" — which is already how C++ spells an
  // unqualified name, and is the most common shape in practice. A
  // component anywhere else becomes "<anon>", because an empty one there
  // would render "redpanda::::varint" and read as a rendering bug rather
  // than as a scope.
  //
  // Splitting on the language's own separator makes this a no-op for
  // languages that never produce these placeholders: a Go scope like
  // "main.Cache" has no component that can match. The substring check
  // short-circuits that overwhelmingly common case before any splitting.
  function sanitizeScope(scope: string, separator: string): string {
    if (!scope.includes("__anon")) return scope;
    return scope
      .split(separator)
      .map((part, i) => (ANON_SCOPE_COMPONENT.test(part) ? (i === 0 ? "" : "<anon>") : part))
      .join(separator);
  }

  function taggedLabel(tag: SymbolTag, symbol: string, path: string): string {
    const separator = scopeSeparator(path);
    // Test the ORIGINAL scope, not the sanitized one. A scope that is
    // nothing but an anonymous component sanitizes to "", and dropping
    // the separator along with it would silently lose the leading "::"
    // that marks the symbol as file-local.
    const scope = tag.scope ? `${sanitizeScope(tag.scope, separator)}${separator}` : "";
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

  // The deps scrollToDiffLine needs, shared by row clicks and Back so the
  // two land identically -- expanding a collapsed file, revealing a
  // collapsed context region, scrolling, and highlighting.
  function jumpDeps(): DiffJumpDeps {
    return {
      isFileCollapsed: (path) => diffStore.isFileCollapsed(owner, name, number, path),
      toggleFileCollapsed: (path) => diffStore.toggleFileCollapsed(owner, name, number, path),
      requestRevealLine: (path, line) => diffStore.requestRevealLine(path, line),
      clearRevealTarget: () => diffStore.consumeRevealTarget(),
    };
  }

  async function jumpTo(hit: SymbolHit): Promise<void> {
    missingJump = null;
    backNote = null;
    // Record where we are leaving from, so Back can return here. The
    // position has to be READ before the jump (afterwards it is the
    // destination) but PUSHED after it, because a jump that resolves
    // "missing" never moves the reader -- pushing then would enable Back
    // on an entry that returns to where they already stand.
    //
    // Skipped when the position is unknown, and when it is the row being
    // clicked -- stacking the position you are already parked on would
    // make Back look broken. Side is part of that identity: a deletion's
    // LEFT line 40 and the new file's RIGHT line 40 are different places,
    // and hits always land RIGHT (see DiffJumpTarget), so a LEFT departure
    // is a real jump worth recording.
    // A search launched from a highlighted symbol records that symbol's own
    // line as the departure point, because the reader means "put me back on
    // that symbol" no matter where on screen it sat. Only the FIRST jump uses
    // it -- cleared once a departure point is actually recorded, after which
    // the reader is parked on a line a jump sent them to and the viewport
    // speaks for itself. Read without consuming, so a jump that resolves
    // "missing" (which never moves the reader) does not lose the launch point.
    const origin = symbolRefsStore.getOrigin();
    // Deliberate positions beat incidental ones: the symbol a search was
    // launched from, else the line the last jump landed on, else -- when
    // neither exists, i.e. the reader scrolled here themselves -- the viewport.
    const from = origin ?? highlightedDiffPosition() ?? currentDiffPosition();
    const selfRef =
      from !== null &&
      from.path === hit.path &&
      from.line === hit.line &&
      (from.side ?? "RIGHT") === "RIGHT";
    const outcome = await scrollToDiffLine({ path: hit.path, line: hit.line }, jumpDeps());
    if (outcome === "missing") {
      missingJump = { path: hit.path, line: hit.line };
      return;
    }
    if (from !== null && !selfRef) {
      symbolRefsStore.pushPosition(from);
      if (origin !== null) symbolRefsStore.clearOrigin();
    }
  }

  // goBack pops one departure point and returns to it. It never pushes
  // what it leaves: the stack is a one-way trail out, so it drains.
  //
  // A popped entry that no longer resolves has already left the stack, so
  // pressing Back again tries the next one -- with a note, rather than a
  // click that appears to do nothing.
  async function goBack(): Promise<void> {
    missingJump = null;
    backNote = null;
    const target = symbolRefsStore.popPosition();
    if (target === null) return;
    const outcome = await scrollToDiffLine(target, jumpDeps());
    // "pending" normally means "a collapsed region was asked to reveal the
    // line, and the landing follows once it mounts" -- but that reveal is
    // side-blind (requestRevealLine takes only a path and a line) and
    // CollapsedRegion only ever anchors the context lines it reveals as
    // RIGHT. So an unrendered LEFT target -- a deleted line, reachable
    // with no scope or SHA change at all via hide-whitespace dropping
    // whitespace-only deletions -- can never resolve: it would sit
    // "pending" forever, with no note and a reveal target left armed to
    // fire later against an unrelated region that happens to cover that
    // new-side line number. Treat it as the failure it is.
    const leftSidePendingForever = target.side === "LEFT" && outcome !== "line";
    if (outcome === "missing" || leftSidePendingForever) {
      backNote = `${target.path}:${target.line} is no longer in the rendered diff.`;
      diffStore.consumeRevealTarget();
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
        <span class="symref-row__text">{hit.tag ? taggedLabel(hit.tag, query, hit.path) : hit.text}</span>
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
    <button
      type="button"
      class="symref-header__back"
      onclick={() => void goBack()}
      disabled={!canGoBack}
      aria-label="Back to previous position"
      title="Back to previous position"
    >
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <input
      class="symref-header__query"
      data-testid="symref-search"
      type="text"
      spellcheck="false"
      autocomplete="off"
      placeholder="find a symbol..."
      aria-label="Symbol to find references for"
      bind:this={inputEl}
      bind:value={draft}
      oninput={() => (invalidReason = null)}
      onkeydown={onSearchKeydown}
    />
    <!-- Nothing has been searched in the prompt state, so a count would
         only ever read a meaningless 0. -->
    {#if status !== "prompt"}
      <span class="symref-header__count" title="Occurrences in this PR's changed files">{inPrTotal}</span>
    {/if}
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

  <!-- role="status" (matching DiffView's interdiff banner) so a refused
       query or a failed Back is announced, not just coloured. -->
  {#if invalidReason}
    <div class="symref-invalid" role="status">{invalidReason}</div>
  {/if}

  {#if backNote}
    <div class="symref-back-note" role="status">{backNote}</div>
  {/if}

  {#if classifier === "heuristic"}
    <div class="symref-degraded-note">
      Kind labels are heuristic — install universal-ctags for exact kinds.
    </div>
  {/if}

  <div class="symref-body">
    {#if status === "prompt"}
      <div class="symref-prompt">Type a symbol name and press Enter.</div>
    {:else if status === "loading"}
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

  .symref-header__back {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: var(--radius-sm);
    color: var(--text-muted);
  }

  .symref-header__back:hover:not(:disabled) {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .symref-header__back:disabled {
    opacity: 0.35;
    cursor: default;
  }

  .symref-header__query {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-weight: 600;
    font-size: 12px;
    color: var(--diff-text);
    background: var(--bg-inset);
    border: 1px solid var(--diff-border);
    border-radius: var(--radius-sm);
    padding: 2px 6px;
  }

  .symref-header__query:focus {
    outline: none;
    border-color: var(--accent-blue);
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

  .symref-invalid {
    flex-shrink: 0;
    padding: 4px 12px;
    font-size: 11px;
    color: var(--accent-red);
    background: var(--diff-header-bg);
    border-bottom: 1px solid var(--diff-border);
  }

  .symref-back-note {
    flex-shrink: 0;
    padding: 4px 12px;
    font-size: 11px;
    color: var(--accent-amber);
    background: var(--diff-header-bg);
    border-bottom: 1px solid var(--diff-border);
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

  .symref-prompt {
    padding: 16px 12px;
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
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
