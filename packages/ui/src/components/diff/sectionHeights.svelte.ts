// sectionHeights.svelte.ts
// Per-section heights for the stacked sections of the review surface.
//
// Both of ReviewSurface's columns -- the review nav (`.review-sidebar`) and
// the main column (`.review-main`) -- are fixed-height flex columns, so their
// children are squeezed to fit rather than overflowing. Which child gives way
// is decided by one flexbox rule: a flex item whose own overflow is not
// `visible` has an automatic minimum size of zero. In the nav that is the file
// list (`overflow-y: auto`); in the main column it is the diff itself
// (`.diff-view`, `overflow: hidden`). Neither the section wrappers nor
// `.top-sections` set an overflow, so their minimum is content-based and they
// refuse to shrink -- which is why the file list and the diff absorbed the
// entire squeeze no matter how tall the sections above them grew. In the main
// column that was literal: an expanded commit message plus an AI summary left
// the diff at zero height, and the column clipped the overflow with no
// scrollbar. `.top-sections` is now bounded and scrollable, and the height it
// is bounded to is one of the values here.
//
// Each resizable body caps itself with `max-height` in CSS so a short one
// wastes no space. This module turns that cap into a value the reader sets by
// dragging a boundary; the elastic pane below takes back whatever is given up.
// Because the cap is a `max-height`, shrinking always tracks the cursor while
// growing something whose content already fits changes nothing -- the trade for
// never letting an empty section reserve space it cannot fill.
//
// localStorage is the canonical persistence layer; this module mirrors it as a
// reactive $state map at first read and on every write.

const KEY_PREFIX = "pr-section-height:";

// What can be resized. In the review nav each stacked section has its own
// boundary and the file list takes the leftover height, so the boundary above
// it is the last section's handle.
//
// The main column works the other way round: one boundary sizes the whole top
// block against the diff. The sections inside it keep the fixed caps in their
// own stylesheets -- per-section boundaries there only decided what showed
// without scrolling, which scrolling already does.
export const SECTION_IDS = [
  // review nav column
  "commits",
  "drafts",
  "review-comments",
  "threads",
  "questions",
  // review-main column: the whole top block, sized against the diff below it
  "top-sections",
] as const;

export type SectionId = (typeof SECTION_IDS)[number];

// A section shorter than this shows less than two rows and is not worth
// having; a drag stops here rather than collapsing the section outright
// (the header's own chevron is how you collapse one).
export const SECTION_MIN_HEIGHT = 80;

// Room a drag always leaves for whatever sits below the section being sized,
// so the reader cannot push the file list entirely off the bottom in one drag.
export const SECTION_RESERVE_BELOW = 120;

// The top block is sized against the diff, and a 120px diff is not worth
// having -- reserve enough that the diff stays readable at any block height.
export const DIFF_RESERVE_BELOW = 220;

export function clampSectionHeight(
  desired: number,
  columnHeight: number,
  reserveBelow: number = SECTION_RESERVE_BELOW,
): number {
  // An unmeasurable column collapses the ceiling onto the floor: better a
  // short section than one sized against a garbage viewport.
  const max = Number.isFinite(columnHeight)
    ? Math.max(SECTION_MIN_HEIGHT, columnHeight - reserveBelow)
    : SECTION_MIN_HEIGHT;
  if (Number.isNaN(desired)) return SECTION_MIN_HEIGHT;
  return Math.max(SECTION_MIN_HEIGHT, Math.min(max, Math.round(desired)));
}

function loadInitial(): Record<string, number> {
  const out: Record<string, number> = {};
  try {
    for (const id of SECTION_IDS) {
      const raw = localStorage.getItem(KEY_PREFIX + id);
      if (raw === null) continue;
      const n = Number(raw);
      // Reject anything a current build would not have written, so a corrupt
      // or hand-edited value falls back to the CSS default instead of
      // pinning a section open at 3px.
      if (Number.isFinite(n) && n >= SECTION_MIN_HEIGHT) out[id] = Math.round(n);
    }
  } catch {
    // storage blocked; every section starts at its CSS default
  }
  return out;
}

const heights = $state<Record<string, number>>(loadInitial());

// null means "no reader-chosen height" -- the caller leaves the inline style
// off entirely so the stylesheet's max-height keeps owning the cap.
export function getSectionHeight(id: SectionId): number | null {
  return heights[id] ?? null;
}

// Live update only. A drag fires a move per pixel, so persistence waits for
// pointerup (mirroring the review-nav width divider in ReviewSurface).
export function setSectionHeight(id: SectionId, px: number): void {
  heights[id] = px;
}

export function persistSectionHeight(id: SectionId): void {
  const px = heights[id];
  if (px === undefined) return;
  try {
    localStorage.setItem(KEY_PREFIX + id, String(px));
  } catch {
    // storage blocked; the live height still stands for this session
  }
}

export function clearSectionHeight(id: SectionId): void {
  delete heights[id];
  try {
    localStorage.removeItem(KEY_PREFIX + id);
  } catch {
    // storage blocked; the live height is already back to its default
  }
}
