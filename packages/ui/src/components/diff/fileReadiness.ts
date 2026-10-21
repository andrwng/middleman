import { tick } from "svelte";
import type { DiffWindow } from "./diffWindow.svelte.js";

// Makes a diff file ready for a jump to land on one of its lines.
//
// With large diffs a file's body is not always in the DOM: it can be
// collapsed, its hunks may not be loaded yet (lazy mode), and DiffView
// mounts bodies only near the viewport. ensureFileReady takes care of all
// three before the caller looks the line up:
//
//   1. expand the file if it is collapsed (recorded as a user choice),
//   2. pin it as the jump target, so the window reports it as "mount"
//      wherever it is,
//   3. request its hunks,
//   4. if the body is not mounted yet, scroll its header into view,
//   5. wait (one check per frame) until the body is mounted.
//
// It resolves true once the body is mounted. It resolves false when the
// file is not in the diff, stays collapsed, its hunks fail to load, the
// wait times out (10s by default), or the jump is cancelled by a newer
// jump or a user scroll (releaseJumpPin).
//
// A body that is already mounted resolves true at once with no extra
// scrolling, so a jump in a small diff behaves exactly as before.
//
// The jump-target pin stays after a successful jump, so the file does not
// unmount while the reader looks at the line. It is released by the next
// jump or by releaseJumpPin, which DiffView calls on the next user scroll.

export type FileHunkState = "idle" | "loading" | "loaded" | "error";

export interface FileReadinessDeps {
  getWindow: () => DiffWindow | undefined;
  // undefined while no diff is loaded; true/false once it is.
  hasFile: (path: string) => boolean | undefined;
  isFileCollapsed: (path: string) => boolean;
  expandForJump: (path: string) => void;
  requestHunks: (path: string) => void;
  getFileHunkState: (path: string) => FileHunkState;
  findFileEl: (path: string) => HTMLElement | null;
  // Injected for tests; default to requestAnimationFrame, Date.now and
  // 10s.
  nextFrame?: () => Promise<void>;
  now?: () => number;
  timeoutMs?: number;
}

export interface FileReadiness {
  ensureFileReady: (path: string) => Promise<boolean>;
  // Drops the jump-target pin and cancels a jump that is still waiting.
  releaseJumpPin: () => void;
}

export const JUMP_READY_TIMEOUT_MS = 10_000;

function defaultNextFrame(): Promise<void> {
  return new Promise((resolve) => {
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(() => resolve());
    } else {
      setTimeout(resolve, 16);
    }
  });
}

export function createFileReadiness(deps: FileReadinessDeps): FileReadiness {
  const nextFrame = deps.nextFrame ?? defaultNextFrame;
  const now = deps.now ?? (() => Date.now());
  const timeoutMs = deps.timeoutMs ?? JUMP_READY_TIMEOUT_MS;

  // Bumped by every jump and every release; a waiting jump whose token
  // is no longer current gives up.
  let token = 0;
  let unpin: (() => void) | null = null;

  function releaseJumpPin(): void {
    token++;
    unpin?.();
    unpin = null;
  }

  async function ensureFileReady(path: string): Promise<boolean> {
    releaseJumpPin();
    const mine = token;
    const current = (): boolean => token === mine;
    const deadline = now() + timeoutMs;

    deps.expandForJump(path);
    await tick();

    // The diff (after a scope switch) and the window (created once the
    // scroll root mounts) may both still be on their way.
    let w = deps.getWindow();
    while (!w || deps.hasFile(path) === undefined || !deps.findFileEl(path)) {
      if (deps.hasFile(path) === false) return false;
      if (!current() || now() >= deadline) return false;
      await nextFrame();
      w = deps.getWindow();
    }
    if (!current()) return false;
    if (deps.isFileCollapsed(path)) return false;

    // Pin before waiting: a pinned file reports zone "mount" wherever it
    // is, so DiffFile requests its hunks and mounts its body.
    unpin = w.pin(path, "jump");
    deps.requestHunks(path);
    if (w.isBodyMounted(path)) return true;

    deps.findFileEl(path)?.scrollIntoView({ block: "start", behavior: "instant" });

    for (;;) {
      // Cancelled: whoever cancelled already released the pin.
      if (!current()) return false;
      if (w.isBodyMounted(path)) return true;
      if (
        deps.getWindow() !== w ||
        deps.getFileHunkState(path) === "error" ||
        now() >= deadline
      ) {
        releaseJumpPin();
        return false;
      }
      await nextFrame();
    }
  }

  return { ensureFileReady, releaseJumpPin };
}
