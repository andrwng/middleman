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
}

// "line" landed on the line; "pending" asked a collapsed region to
// reveal it and moved to the file header meanwhile; "missing" could not
// find the file at all.
export type DiffJumpOutcome = "line" | "pending" | "missing";

// Reuses DiffFile's existing :global(.line-wrap--flash) rule, which
// applies document-wide and so also covers a line revealed inside a
// CollapsedRegion.
const FLASH_CLASS = "line-wrap--flash";
const FLASH_MS = 1500;

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

export function flashDiffLine(el: HTMLElement): void {
  el.scrollIntoView({ block: "center", behavior: "smooth" });
  el.classList.add(FLASH_CLASS);
  window.setTimeout(() => el.classList.remove(FLASH_CLASS), FLASH_MS);
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
    return "line";
  }
  deps.requestRevealLine(target.path, target.line);
  return scrollToFileHeader(target.path) ? "pending" : "missing";
}
