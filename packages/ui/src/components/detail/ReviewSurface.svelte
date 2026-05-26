<script lang="ts">
  import DiffSidebar from "../diff/DiffSidebar.svelte";
  import DiffView from "../diff/DiffView.svelte";
  import CommitMessageBanner from "../diff/CommitMessageBanner.svelte";
  import PatchsetPicker from "../diff/PatchsetPicker.svelte";
  import ReviewCoverBanner from "./ReviewCoverBanner.svelte";
  import ReviewBriefCard from "./ReviewBriefCard.svelte";
  import PRNotesPanel from "./PRNotesPanel.svelte";
  import TopSectionsStrip from "./TopSectionsStrip.svelte";
  import { isReviewNavCollapsed } from "../../lib/uiState.svelte.js";
  import { getStores } from "../../context.js";
  import type { components } from "../../api/generated/schema.js";

  type MergeRequest = components["schemas"]["MergeRequest"];

  interface Props {
    owner: string;
    name: string;
    number: number;
    pr: MergeRequest | null;
  }
  const { owner, name, number, pr }: Props = $props();

  const { diff: diffStore } = getStores();

  // --- Top-sections consolidation state ------------------------------------
  let topConsolidated = $state(
    typeof localStorage !== "undefined" &&
      localStorage.getItem("pr-top-sections-consolidated") === "true",
  );
  let peeked = $state<string | null>(null);

  function readKey(k: string): boolean {
    try { return localStorage.getItem(k) === "true"; }
    catch { return false; }
  }
  function patchsetPipLabel(): string {
    const list = diffStore.getPatchsets();
    if (!list || list.length === 0) return "patchset";
    const scope = diffStore.getScope();
    const selected =
      scope.kind === "patchsets" ? scope.toNumber : list[list.length - 1]!.number;
    return `patchset ${selected}/${list.length}`;
  }
  type Pip = { id: string; label: string; muted: boolean };
  function computePips(): Pip[] {
    return [
      { id: "cover",    label: "cover",          muted: readKey("pr-cover-collapsed") },
      { id: "msg",      label: "message",        muted: readKey("pr-commit-msg-collapsed") },
      { id: "patchset", label: patchsetPipLabel(), muted: readKey("pr-patchset-collapsed") },
      { id: "brief",    label: "brief",          muted: readKey("pr-brief-collapsed") },
    ];
  }
  let pips = $state<Pip[]>(computePips());

  function setTopConsolidated(next: boolean): void {
    topConsolidated = next;
    peeked = null;
    if (next) {
      pips = computePips();
    }
    try {
      localStorage.setItem("pr-top-sections-consolidated", String(next));
    } catch { /* ignore */ }
  }
  function togglePeek(id: string): void {
    peeked = peeked === id ? null : id;
  }

  // --- Review-nav width state ----------------------------------------------
  const DEFAULT_REVIEW_NAV_WIDTH = 280;
  const MIN_REVIEW_NAV_WIDTH = 180;
  const MAX_REVIEW_NAV_WIDTH = 560;

  function loadReviewNavWidth(): number {
    try {
      const raw = localStorage.getItem("pr-review-nav-width");
      if (!raw) return DEFAULT_REVIEW_NAV_WIDTH;
      const n = Number(raw);
      if (!Number.isFinite(n)) return DEFAULT_REVIEW_NAV_WIDTH;
      return Math.max(MIN_REVIEW_NAV_WIDTH, Math.min(MAX_REVIEW_NAV_WIDTH, Math.round(n)));
    } catch { return DEFAULT_REVIEW_NAV_WIDTH; }
  }
  let reviewNavWidth = $state(loadReviewNavWidth());
  function persistReviewNavWidth(w: number): void {
    try { localStorage.setItem("pr-review-nav-width", String(w)); }
    catch { /* ignore */ }
  }

  let resizing = false;
  let resizeStartX = 0;
  let resizeStartWidth = 0;
  function onResizeStart(e: PointerEvent): void {
    if (isReviewNavCollapsed()) return;
    resizing = true;
    resizeStartX = e.clientX;
    resizeStartWidth = reviewNavWidth;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }
  function onResizeMove(e: PointerEvent): void {
    if (!resizing) return;
    const delta = e.clientX - resizeStartX;
    reviewNavWidth = Math.max(
      MIN_REVIEW_NAV_WIDTH,
      Math.min(MAX_REVIEW_NAV_WIDTH, resizeStartWidth + delta),
    );
  }
  function onResizeEnd(e: PointerEvent): void {
    if (!resizing) return;
    resizing = false;
    (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    persistReviewNavWidth(reviewNavWidth);
  }
</script>

<div class="review-layout">
  <aside
    class="review-sidebar"
    class:review-sidebar--collapsed={isReviewNavCollapsed()}
    style:width={isReviewNavCollapsed() ? "30px" : `${reviewNavWidth}px`}
  >
    <DiffSidebar />
    {#if !isReviewNavCollapsed()}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div
        class="review-sidebar__resize"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize review nav"
        onpointerdown={onResizeStart}
        onpointermove={onResizeMove}
        onpointerup={onResizeEnd}
        onpointercancel={onResizeEnd}
      ></div>
    {/if}
  </aside>
  <div class="review-main">
    <div class="top-sections" class:top-sections--consolidated={topConsolidated}>
      {#if !topConsolidated}
        <button
          type="button"
          class="top-sections__consolidate"
          onclick={() => setTopConsolidated(true)}
          aria-label="Consolidate top sections"
          title="Consolidate top sections into a single strip"
        >
          <svg width="11" height="11" viewBox="0 0 16 16" fill="none"
               stroke="currentColor" stroke-width="1.6">
            <line x1="3" y1="4" x2="13" y2="4" stroke-linecap="round" />
            <line x1="3" y1="8" x2="13" y2="8" stroke-linecap="round" />
            <line x1="3" y1="12" x2="13" y2="12" stroke-linecap="round" />
          </svg>
          Compact
        </button>
        {#if pr}
          <ReviewCoverBanner {pr} {owner} {name} />
        {/if}
        <CommitMessageBanner {owner} {name} {number} />
        <PatchsetPicker />
        <ReviewBriefCard {owner} {name} {number} />
      {:else}
        <TopSectionsStrip
          {pips}
          peeked={peeked}
          onPeek={togglePeek}
          onExpandAll={() => setTopConsolidated(false)}
        />
        {#if peeked === "cover" && pr}
          <ReviewCoverBanner {pr} {owner} {name} forceExpanded={true} />
        {:else if peeked === "msg"}
          <CommitMessageBanner {owner} {name} {number} forceExpanded={true} />
        {:else if peeked === "patchset"}
          <PatchsetPicker forceExpanded={true} />
        {:else if peeked === "brief"}
          <ReviewBriefCard {owner} {name} {number} forceExpanded={true} />
        {/if}
      {/if}
    </div>
    <PRNotesPanel />
    <DiffView {owner} {name} {number} />
  </div>
</div>

<style>
  .review-layout {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .review-sidebar {
    flex-shrink: 0;
    border-right: 1px solid var(--border-default);
    background: var(--bg-surface);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    position: relative;
  }

  .review-sidebar--collapsed {
    width: 30px !important;
    min-width: 30px;
  }

  .review-sidebar__resize {
    position: absolute;
    top: 0;
    right: -3px;
    width: 6px;
    height: 100%;
    cursor: col-resize;
    background: transparent;
    z-index: 2;
  }
  .review-sidebar__resize:hover {
    background: var(--accent-blue);
    opacity: 0.4;
  }

  .review-main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .top-sections {
    position: relative;
    display: flex;
    flex-direction: column;
  }

  .top-sections__consolidate {
    position: absolute;
    top: 6px;
    right: 8px;
    z-index: 5;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 10px;
    font-size: 11px;
    color: var(--text-secondary);
    background: var(--bg-page, var(--bg-inset));
    border: 1px solid var(--accent-blue);
    border-radius: 999px;
    cursor: pointer;
  }
  .top-sections__consolidate:hover {
    color: var(--text-primary);
    background: color-mix(in srgb, var(--accent-blue) 14%, var(--bg-surface));
  }

  /* On narrow viewports the fixed-width sidebar would crush the diff pane.
     Stack the sidebar above the diff with a capped height so the diff stays
     readable. The !important is required to override the inline width style
     when this media query applies. */
  @media (max-width: 720px) {
    .review-layout { flex-direction: column; }
    .review-sidebar {
      width: 100% !important;
      max-height: 35vh;
      border-right: none;
      border-bottom: 1px solid var(--border-default);
    }
    .review-main { flex: 1; min-height: 0; }
  }
</style>
