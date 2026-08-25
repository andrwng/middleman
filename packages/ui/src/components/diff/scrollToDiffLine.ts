import { tick } from "svelte";

// A diff line addressed the way the DOM addresses it. The side defaults
// to RIGHT: hits from a symbol search always land on an add or context
// line, which anchor RIGHT with their new-side number.
export interface DiffJumpTarget {
  path: string;
  line: number;
  side?: "LEFT" | "RIGHT";
}

// The bits of diff state the jump needs. Injected rather than imported
// so this module is testable without a store instance.
export interface DiffJumpDeps {
  isFileCollapsed: (path: string) => boolean;
  toggleFileCollapsed: (path: string) => void;
  requestRevealLine: (path: string, line: number) => void;
  // Discards the store's reveal target outright, independent of
  // whatever it currently holds. Called on a direct hit (the target
  // line was already rendered, so no reveal was needed for THIS jump)
  // to sweep away any earlier, unrelated target left over from a
  // previous jump that never resolved — otherwise it can sit armed and
  // later fire against a different file that happens to reuse the
  // same line number.
  clearRevealTarget: () => void;
}

// "line" landed on the line; "pending" asked a collapsed region to
// reveal it and moved to the file header meanwhile; "missing" could not
// find the file at all.
export type DiffJumpOutcome = "line" | "pending" | "missing";

// Reuses DiffFile's :global(.line-wrap--jump-highlight) rule, which
// applies document-wide and so also covers a line revealed inside a
// CollapsedRegion. Unlike the timed .line-wrap--flash class the other
// (non-symbol-refs) jump-to-line implementations add and remove on
// their own, this class is persistent — see flashDiffLine below.
const HIGHLIGHT_CLASS = "line-wrap--jump-highlight";

// The one element currently carrying HIGHLIGHT_CLASS, so a later jump
// can clear it before highlighting its own target. Module-level rather
// than per-call state because the highlight is a single, page-wide
// "this is where you last jumped to" indicator, not something scoped
// to one DiffFile instance.
let highlightedEl: HTMLElement | null = null;

// CSS.escape isn't implemented by every environment this module runs
// in — notably jsdom, which this file's own test suite runs under —
// so guard it the same way ReviewThreadsSection.svelte already does
// rather than calling the bare global. A path's other characters (as
// opposed to a literal `"` or `\`) don't need escaping to sit safely
// inside a quoted attribute-selector value, so the unescaped fallback
// is correct, not just a test-only workaround.
function escapeForSelector(value: string): string {
  return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(value) : value;
}

// findDiffLineEl locates a diff line by (path, line, side). The selector
// deliberately does not require the .line-wrap class: DiffFile wraps its
// lines in one, while a context line revealed inside a CollapsedRegion
// carries the attributes on the line element itself.
export function findDiffLineEl(target: DiffJumpTarget): HTMLElement | null {
  const side = target.side ?? "RIGHT";
  const path = escapeForSelector(target.path);
  return document.querySelector<HTMLElement>(
    `.diff-file[data-file-path="${path}"] ` +
      `[data-anchor-line="${target.line}"][data-anchor-side="${side}"]`,
  );
}

// flashDiffLine scrolls to el and marks it as the current jump target
// with a persistent highlight, clearing whichever element previously
// held it first. "Persistent" (rather than a timed flash) is
// deliberate: a jump can land many lines down the page, and a
// time-based flash would routinely finish decaying before a long
// smooth-scroll even arrives — this has no such race, since nothing
// times it out.
export function flashDiffLine(el: HTMLElement): void {
  el.scrollIntoView({ block: "center", behavior: "smooth" });
  if (highlightedEl && highlightedEl !== el && highlightedEl.isConnected) {
    highlightedEl.classList.remove(HIGHLIGHT_CLASS);
  }
  highlightedEl = el;
  el.classList.add(HIGHLIGHT_CLASS);
}

// clearDiffLineHighlight removes the persistent highlight left by the
// most recent flashDiffLine call, if any. Callers use this when the
// context that made the highlight meaningful goes away — e.g. the
// symbol-refs gutter closing — rather than leaving a highlighted line
// with nothing on screen explaining why.
export function clearDiffLineHighlight(): void {
  if (highlightedEl && highlightedEl.isConnected) {
    highlightedEl.classList.remove(HIGHLIGHT_CLASS);
  }
  highlightedEl = null;
}

function scrollToFileHeader(path: string): boolean {
  const fileEl = document.querySelector<HTMLElement>(
    `.diff-file[data-file-path="${escapeForSelector(path)}"]`,
  );
  if (!fileEl) return false;
  fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
  return true;
}

// scrollToDiffLine jumps to a line in the rendered diff, expanding a
// collapsed file first. When the line is not rendered it sits in an
// unexpanded context gap: request a reveal and move to the file header
// in the meantime, so the view goes somewhere immediately and the
// precise landing follows once the region mounts its lines.
export async function scrollToDiffLine(
  target: DiffJumpTarget,
  deps: DiffJumpDeps,
): Promise<DiffJumpOutcome> {
  if (deps.isFileCollapsed(target.path)) {
    deps.toggleFileCollapsed(target.path);
    await tick();
  }
  const el = findDiffLineEl(target);
  if (el) {
    flashDiffLine(el);
    deps.clearRevealTarget();
    return "line";
  }
  deps.requestRevealLine(target.path, target.line);
  return scrollToFileHeader(target.path) ? "pending" : "missing";
}
