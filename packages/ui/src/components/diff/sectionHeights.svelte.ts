// sectionHeights.svelte.ts
// Per-section heights for the review-nav sidebar's stacked sections.
//
// The sidebar (`.review-sidebar` in ReviewSurface) is a fixed-height flex
// column, so its children are squeezed to fit rather than overflowing it.
// Which child gives way is decided by one flexbox rule: a flex item whose own
// overflow is not `visible` has an automatic minimum size of zero. The file
// list sets `overflow-y: auto`, so it can shrink to nothing; the section
// wrappers set no overflow, so their minimum is content-based and they refuse
// to shrink. The file list therefore absorbed the entire squeeze no matter how
// tall the sections above it grew.
//
// Each section body caps itself with `max-height` (40vh in CSS) so a short
// section wastes no space. This module turns that cap into a value the reader
// sets by dragging the section's bottom boundary, and the file list becomes the
// explicit elastic remainder. Because the cap is a `max-height`, shrinking a
// section always tracks the cursor while growing one whose content is already
// shorter than the cap changes nothing -- the trade for never letting an empty
// section reserve space it cannot fill.
//
// localStorage is the canonical persistence layer; this module mirrors it as a
// reactive $state map at first read and on every write.

const KEY_PREFIX = "pr-section-height:";

// The sections that can be resized, in the order DiffSidebar stacks them. The
// file list is deliberately absent: it owns whatever height is left over, so
// the boundary above it is the last section's handle.
export const SECTION_IDS = [
  "commits",
  "drafts",
  "review-comments",
  "threads",
  "questions",
] as const;

export type SectionId = (typeof SECTION_IDS)[number];

// A section shorter than this shows less than two rows and is not worth
// having; a drag stops here rather than collapsing the section outright
// (the header's own chevron is how you collapse one).
export const SECTION_MIN_HEIGHT = 80;

// Room a drag always leaves for whatever sits below the section being sized,
// so the reader cannot push the file list entirely off the bottom in one drag.
export const SECTION_RESERVE_BELOW = 120;

export function clampSectionHeight(desired: number, sidebarHeight: number): number {
  // An unmeasurable sidebar collapses the ceiling onto the floor: better a
  // short section than one sized against a garbage viewport.
  const max = Number.isFinite(sidebarHeight)
    ? Math.max(SECTION_MIN_HEIGHT, sidebarHeight - SECTION_RESERVE_BELOW)
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
