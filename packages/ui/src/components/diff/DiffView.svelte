<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { getStores } from "../../context.js";
  import { clampGutterWidth } from "./gutterStack.js";
  import type { DiffScope } from "../../stores/diff.svelte.js";

  const {
    diff: diffStore,
    ai: aiStore,
    brief: briefStore,
    reviewThreads: reviewThreadsStore,
    symbolRefs: symbolRefsStore,
  } = getStores();
  import DiffToolbar from "./DiffToolbar.svelte";
  import DiffFileComponent from "./DiffFile.svelte";
  import ReviewPanel from "./ReviewPanel.svelte";
  import SymbolRefsGutter from "./SymbolRefsGutter.svelte";

  interface Props {
    owner: string;
    name: string;
    number: number;
  }

  const { owner, name, number }: Props = $props();

  let diffArea: HTMLDivElement | undefined = $state();
  let diffAreaRow: HTMLDivElement | undefined = $state();
  let scrollRaf = 0;
  let reviewPanelOpen = $state(false);

  // Symbol references gutter width — horizontally resizable, persisted
  // across reloads. The drag handle lives here (not in the gutter
  // component) because clampGutterWidth needs the row's own clientWidth.
  const SYMBOL_REFS_GUTTER_WIDTH_KEY = "symbol-refs-gutter-width";
  const DEFAULT_SYMBOL_REFS_GUTTER_WIDTH = 360;

  function loadSymbolRefsGutterWidth(): number {
    try {
      const v = Number(localStorage.getItem(SYMBOL_REFS_GUTTER_WIDTH_KEY));
      if (Number.isFinite(v) && v > 0) return v;
    } catch {
      /* localStorage unavailable — fall through to default */
    }
    return DEFAULT_SYMBOL_REFS_GUTTER_WIDTH;
  }

  let symbolRefsGutterWidth = $state(loadSymbolRefsGutterWidth());
  let resizingSymbolRefsGutter = false;
  let gutterResizeStartX = 0;
  let gutterResizeStartWidth = 0;

  function onGutterResizeStart(e: PointerEvent): void {
    resizingSymbolRefsGutter = true;
    gutterResizeStartX = e.clientX;
    gutterResizeStartWidth = symbolRefsGutterWidth;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }
  function onGutterResizeMove(e: PointerEvent): void {
    if (!resizingSymbolRefsGutter) return;
    // Dragging the handle left (toward the diff body) widens the gutter.
    const delta = gutterResizeStartX - e.clientX;
    symbolRefsGutterWidth = clampGutterWidth(
      gutterResizeStartWidth + delta,
      diffAreaRow?.clientWidth ?? 0,
    );
  }
  function onGutterResizeEnd(e: PointerEvent): void {
    if (!resizingSymbolRefsGutter) return;
    resizingSymbolRefsGutter = false;
    (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    try {
      localStorage.setItem(SYMBOL_REFS_GUTTER_WIDTH_KEY, String(Math.round(symbolRefsGutterWidth)));
    } catch {
      /* localStorage unavailable — width still applies for this session */
    }
  }

  onMount(() => {
    void diffStore.loadDiff(owner, name, number);
    // Preload commits so currentCommitSha() always has the head to
    // anchor new drafts against. Otherwise drafts made before the
    // Commits panel is expanded carry commitSha = "" and render as
    // "on current" (blue) when they're really unknown.
    void diffStore.loadCommits();
    aiStore.start(owner, name, number);
    void reviewThreadsStore.load(owner, name, number);
    briefStore.start(owner, name, number);

    return () => {
      cancelAnimationFrame(scrollRaf);
      diffStore.clearDiff();
      aiStore.stop();
      reviewThreadsStore.clear();
      briefStore.stop();
      // Closes any open symbol-references search so a stale result set
      // from this PR doesn't reappear (isActive() still true) the
      // instant a different PR's DiffView mounts — symbolRefsStore is
      // an app-wide singleton, not scoped per PR like the diff itself.
      symbolRefsStore.close();
    };
  });

  const diff = $derived(diffStore.getDiff());
  const loading = $derived(diffStore.isDiffLoading());
  const error = $derived(diffStore.getDiffError());
  const tabWidth = $derived(diffStore.getTabWidth());
  const scope = $derived(diffStore.getScope());
  const interdiff = $derived(diffStore.getInterdiff());
  // Tracked alongside `scope` for the symbol-refs staleness watcher
  // below: diffScopeKey collapses every head-scope view to the same
  // "head" string, so the user clicking Refresh while scope stays
  // "head" (refresh() re-fetches commits without changing scope.kind)
  // needs its own signal, distinct from a genuine scope change.
  const currentSha = $derived(diffStore.getCurrentCommitSha());

  function scrollToFile(path: string): void {
    if (!diffArea) return;
    const el = diffArea.querySelector(`[data-file-path="${CSS.escape(path)}"]`);
    if (el) {
      el.scrollIntoView({ behavior: "instant", block: "start" });
    }
    // Clear the scrolling flag after the instant scroll so the next user-initiated
    // scroll event resumes active file tracking.
    scrollRaf = requestAnimationFrame(() => diffStore.clearScrolling());
  }

  // Watch for scroll requests from the sidebar file list (via the store).
  // Only consume the target once diffArea is mounted and diff data is available,
  // so the request is not lost if the user clicks a file before diff renders.
  $effect(() => {
    const target = diffStore.getScrollTarget();
    if (target && diffArea && diff) {
      diffStore.consumeScrollTarget();
      scrollToFile(target);
    }
  });

  // Scroll-based active file tracking.
  // Skipped for one frame after programmatic scroll to avoid re-setting activeFile.
  function onDiffScroll(): void {
    if (!diffArea || !diff) return;
    if (diffStore.isScrolling()) return;
    const rect = diffArea.getBoundingClientRect();
    const threshold = rect.top + 60;

    let current: string | null = null;
    for (const file of diff.files) {
      const el = diffArea.querySelector(`[data-file-path="${CSS.escape(file.path)}"]`);
      if (!el) continue;
      const elRect = el.getBoundingClientRect();
      if (elRect.top <= threshold) {
        current = file.path;
      }
    }
    if (current !== null) {
      diffStore.setActiveFile(current);
    }
  }

  // j/k keyboard navigation between files.
  function handleKeydown(e: KeyboardEvent): void {
    const tag = (e.target as HTMLElement).tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if ((e.target as HTMLElement).isContentEditable) return;
    // Unmodified single-letter shortcuts only — don't swallow
    // Cmd/Ctrl-F, Cmd-J (downloads), etc.
    if (e.metaKey || e.ctrlKey || e.altKey) return;

    if (e.key === "j" || e.key === "k") {
      if (!diff || diff.files.length === 0) return;
      e.preventDefault();
      const paths = diff.files.map((f) => f.path);
      const currentIdx = diffStore.getActiveFile() ? paths.indexOf(diffStore.getActiveFile()!) : -1;
      let nextIdx: number;
      if (e.key === "j") {
        nextIdx = currentIdx < paths.length - 1 ? currentIdx + 1 : currentIdx;
      } else {
        nextIdx = currentIdx > 0 ? currentIdx - 1 : 0;
      }
      const nextPath = paths[nextIdx] ?? null;
      if (nextPath) diffStore.requestScrollToFile(nextPath);
    }

    if (e.key === "[" || e.key === "]") {
      e.preventDefault();
      if (e.key === "[") {
        diffStore.stepPrev();
      } else {
        diffStore.stepNext();
      }
    }

    if (e.key === "m") {
      e.preventDefault();
      diffStore.jumpToNextUnreviewed();
    }

    // Opens the symbol-refs search box. Gated on a resolvable SHA for the
    // same reason the toolbar button is: hits numbered against no SHA
    // cannot be resolved back to rendered lines.
    //
    // Like the other single-letter shortcuts here, this fires even when a
    // modal is open over the diff. That is this handler's established
    // behaviour; making one key modal-aware where j/k/m/[/] are not would
    // be the surprising choice.
    if (e.key === "s") {
      if (currentSha === "") return;
      e.preventDefault();
      symbolRefsStore.openBlank();
    }
  }

  $effect(() => {
    window.addEventListener("keydown", handleKeydown);
    return () => window.removeEventListener("keydown", handleKeydown);
  });

  // Auto-mark commit as reviewed when its diff finishes loading
  $effect(() => {
    if (scope.kind === "commit" && diff && !loading) {
      diffStore.markCommitReviewed(scope.sha);
    }
  });

  // Symbol-ref hits are scope-relative, not PR-relative: the line numbers
  // the server returned only make sense against whichever SHA DiffFile's
  // currentCommitSha() searched (the single commit, the range's newer
  // end, or the PR head). Switching PRs is safe -- PRListView remounts
  // this whole surface inside a {#key}, and our onMount cleanup below
  // already closes any open search -- but changing scope WITHIN the same
  // PR mutates diffStore's scope in place without unmounting DiffView, so
  // nothing else clears a search that no longer matches what's rendered.
  // Left alone, a stale hit whose line number happens to still exist in
  // the new scope's rendering would silently jump to unrelated code.
  //
  // Watches a derived string key, not the scope object itself: every
  // select* method on the diff store reassigns `scope` to a brand-new
  // object literal even when re-selecting the value it already has (e.g.
  // re-clicking the already-selected commit), so comparing raw object
  // identity would close a search the user is still using on a pure no-op
  // reassignment. diffScopeKey mirrors DiffScope's own shape -- kind plus
  // whichever shas/numbers that kind carries -- so two
  // structurally-identical scopes always produce the same string
  // regardless of object identity.
  //
  // The key alone misses one case: every head-scope view maps to the
  // same "head" string, so the user clicking Refresh while scope stays
  // "head" (diffStore.refresh() re-fetching commits) changes nothing
  // the key can see, even though the rendered line numbers just
  // shifted under an active search's feet. currentSha (tracked
  // alongside the key) catches that: it's the same currentCommitSha()
  // derivation DiffFile searched against, so comparing it to the
  // store's own recorded searched-sha detects the drift regardless of
  // whether scope.kind changed.
  function diffScopeKey(s: DiffScope): string {
    switch (s.kind) {
      case "head": return "head";
      case "commit": return `commit:${s.sha}`;
      case "range": return `range:${s.fromSha}..${s.toSha}`;
      case "unreviewed": return "unreviewed";
      case "patchsets": return `patchsets:${s.fromNumber}..${s.toNumber}`;
    }
  }

  // Stays undefined until the effect below has recorded a first key --
  // that first run is the initial mount observing whatever scope it
  // started in, not a "change" to react to, so it must never close a
  // search on its own.
  let lastScopeKey: string | undefined;
  $effect(() => {
    const key = diffScopeKey(scope);
    const sha = currentSha;
    // isActive()/getSearchedSha()/close() read and write the store's own
    // state. untrack keeps this effect from subscribing to it -- same
    // gotcha, avoided here, as the one noted in WorktreeConversation.svelte
    // -- so closing a search doesn't turn around and re-trigger this effect.
    untrack(() => {
      const scopeChanged = lastScopeKey !== undefined && key !== lastScopeKey;
      const searchedSha = symbolRefsStore.getSearchedSha();
      // "" on either side means "not known yet" (no search recorded, or
      // commits mid-reload after e.g. refresh() nulls them out) rather
      // than a real divergence -- wait for a definite new SHA to compare
      // against instead of closing on the transient gap.
      const shaDrifted = searchedSha !== "" && sha !== "" && searchedSha !== sha;
      if (symbolRefsStore.isActive() && (scopeChanged || shaDrifted)) {
        symbolRefsStore.close();
      }
      lastScopeKey = key;
    });
  });
</script>

<div class="diff-view">
  {#if diff?.stale}
    <div class="stale-banner">
      Diff may be outdated -- showing changes as of an earlier version of this PR.
    </div>
  {/if}
  {#if interdiff && interdiff.kind !== "clean"}
    <div class="interdiff-banner" role="status">
      <strong>
        {#if interdiff.kind === "conflicted"}
          Interdiff unavailable — rebase noise could not be subtracted.
        {:else}
          Interdiff unavailable — patchsets have no common ancestor.
        {/if}
      </strong>
      <span class="interdiff-banner__detail">
        {#if interdiff.kind === "conflicted"}
          Showing only files the author touched in the new patchset. The diff
          for each may still include changes from the rebase itself.
        {:else}
          Showing the raw diff between patchset heads.{interdiff.reason ? ` (${interdiff.reason})` : ""}
        {/if}
      </span>
    </div>
  {/if}

  <div class="diff-body">
    {#if loading && !diff}
      <div class="diff-state">
        <svg class="diff-spinner" width="20" height="20" viewBox="0 0 20 20" fill="none">
          <circle cx="10" cy="10" r="8" stroke="currentColor" stroke-opacity="0.2" stroke-width="2" />
          <path d="M18 10a8 8 0 0 0-8-8" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
        <p class="diff-state-msg">Loading diff</p>
      </div>
    {:else if error}
      <div class="diff-state">
        <p class="diff-state-msg diff-state-msg--error">{error}</p>
      </div>
    {:else if diff}
      <div class="diff-main">
        <DiffToolbar
          onReviewClick={() => { reviewPanelOpen = true; }}
          onRefsClick={() => symbolRefsStore.openBlank()}
        />
        <div class="diff-area-row" bind:this={diffAreaRow}>
          <div
            class="diff-area"
            bind:this={diffArea}
            onscroll={onDiffScroll}
            style:tab-size={tabWidth}
          >
            {#each diff.files as file (file.path)}
              <DiffFileComponent
                {file}
                {owner}
                {name}
                {number}
              />
            {/each}
          </div>
          {#if symbolRefsStore.isActive()}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="symref-gutter-resize"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize symbol references gutter"
              title="Drag to resize the symbol references gutter"
              onpointerdown={onGutterResizeStart}
              onpointermove={onGutterResizeMove}
              onpointerup={onGutterResizeEnd}
              onpointercancel={onGutterResizeEnd}
            ></div>
            <SymbolRefsGutter {owner} {name} {number} width={symbolRefsGutterWidth} />
          {/if}
        </div>
      </div>
    {/if}
  </div>
</div>

{#if reviewPanelOpen}
  <ReviewPanel {owner} {name} {number} onclose={() => { reviewPanelOpen = false; }} />
{/if}

<style>
  .diff-view {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
    background: var(--diff-bg);
  }

  .stale-banner {
    padding: 6px 16px;
    background: var(--diff-stale-bg);
    color: var(--diff-stale-text);
    border-bottom: 1px solid var(--diff-stale-border);
    font-size: 12px;
    flex-shrink: 0;
  }

  .interdiff-banner {
    padding: 6px 16px;
    background: color-mix(in srgb, var(--accent-amber) 15%, var(--bg-surface));
    color: var(--text-primary);
    border-bottom: 1px solid var(--accent-amber);
    font-size: 12px;
    flex-shrink: 0;
    display: flex;
    gap: 8px;
    align-items: baseline;
    flex-wrap: wrap;
  }

  .interdiff-banner__detail {
    color: var(--text-secondary);
  }

  .diff-body {
    display: flex;
    flex: 1;
    overflow: hidden;
  }

  .diff-main {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }

  /* Row containing the scrolling diff body and (when active) the
     symbol-references gutter side by side. flex: 1 here does the
     vertical-growth job .diff-area's own flex: 1 used to do alone;
     min-height: 0 keeps it from taking its children's natural height
     instead of the space .diff-main actually has left. */
  .diff-area-row {
    display: flex;
    flex: 1;
    min-height: 0;
  }

  .diff-area {
    flex: 1;
    /* Lets the diff shrink instead of pushing the gutter off-screen
       when the row above also holds a fixed-width gutter. */
    min-width: 0;
    overflow: auto;
  }

  .symref-gutter-resize {
    flex-shrink: 0;
    width: 6px;
    cursor: col-resize;
    background: transparent;
  }

  .symref-gutter-resize:hover {
    background: var(--accent-blue);
    opacity: 0.4;
  }

  .diff-state {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    flex: 1;
  }

  .diff-spinner {
    animation: spin 0.8s linear infinite;
    color: var(--text-muted);
  }

  .diff-state-msg {
    font-size: 13px;
    color: var(--text-muted);
  }

  .diff-state-msg--error {
    color: var(--accent-red);
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
