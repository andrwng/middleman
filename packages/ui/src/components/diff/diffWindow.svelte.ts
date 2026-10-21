// Viewport window for DiffView: decides, per file, whether its body
// should be mounted, whether its hunks should be prefetched, or whether
// it is far enough away to stay a placeholder. One window per DiffView;
// every DiffFile registers its root element with observe().
//
// Zones are measured in multiples of the scroll root's visible height
// (`root.clientHeight`, one "viewport"), as rootMargins on observers
// rooted at the diff's scroll container:
//
//   mount observer     2 viewports above and below -> body mounts here
//   prefetch observer  4 viewports                 -> hunks requested
//   unmount observer   6 viewports                 -> a mounted body
//                                                      unmounts only once
//                                                      it leaves this
//   visible observer   0                           -> auto-mark-viewed
//
// The mount -> unmount gap (2 vs 6 viewports) is the hysteresis: once a
// body mounts it stays mounted until it is more than ~6 viewports from
// the visible area, so scrolling back and forth near a boundary never
// thrashes mount/unmount. Margins are recomputed (and the observers
// recreated) whenever the root's height changes.

import { getContext, setContext, untrack } from "svelte";

export type Zone = "far" | "prefetch" | "mount";

// Where a file sits relative to the visible area, from the 0-margin
// observer. "unknown" until the first report (or without
// IntersectionObserver).
export type Visibility = "unknown" | "visible" | "above" | "below";

export interface DiffWindow {
  observe(path: string, el: HTMLElement): () => void;
  // Reactive. A pinned file always reports "mount".
  zone(path: string): Zone;
  // Pinned files never drop to "far" for unmount purposes. Pins are
  // counted: each call returns its own idempotent release.
  pin(path: string, reason: string): () => void;
  isPinned(path: string): boolean;
  rememberHeight(path: string, px: number): void;
  height(path: string): number | undefined;
  // Reactive.
  visibility(path: string): Visibility;
  // DiffFile reports whether its body is mounted, so jump code can wait
  // for a file to be renderable (see whenBodyMounted). Reactive.
  setBodyMounted(path: string, mounted: boolean): void;
  isBodyMounted(path: string): boolean;
  // Resolves true once the file's body is mounted (immediately if it
  // already is), or false if the window is destroyed first.
  whenBodyMounted(path: string): Promise<boolean>;
  destroy(): void;
}

export interface DiffWindowOptions {
  prefetchViewports?: number;
  mountViewports?: number;
  unmountViewports?: number;
}

export const DEFAULT_MOUNT_VIEWPORTS = 2;
export const DEFAULT_PREFETCH_VIEWPORTS = 4;
export const DEFAULT_UNMOUNT_VIEWPORTS = 6;

// Fallback viewport height when the root has not been laid out yet.
const FALLBACK_VIEWPORT_PX = 800;

interface Flags {
  inMount: boolean;
  inPrefetch: boolean;
  inKeep: boolean;
  // Latched true on entering the mount zone; cleared on leaving the
  // unmount (keep) zone.
  mounted: boolean;
}

export function createDiffWindow(
  root: HTMLElement,
  opts: DiffWindowOptions = {},
): DiffWindow {
  const mountVp = opts.mountViewports ?? DEFAULT_MOUNT_VIEWPORTS;
  const prefetchVp = opts.prefetchViewports ?? DEFAULT_PREFETCH_VIEWPORTS;
  const unmountVp = opts.unmountViewports ?? DEFAULT_UNMOUNT_VIEWPORTS;

  // Reactive per-path state. Proxied records give per-key fine-grained
  // reactivity: a DiffFile reading zones[path] re-renders only when its
  // own entry changes.
  const zones = $state<Record<string, Zone>>({});
  const pinCounts = $state<Record<string, number>>({});
  const visibilities = $state<Record<string, Visibility>>({});
  const bodyMounted = $state<Record<string, boolean>>({});

  const flags = new Map<string, Flags>();
  const pathByEl = new Map<Element, string>();
  const elByPath = new Map<string, HTMLElement>();
  const heights = new Map<string, number>();
  const waiters = new Map<string, Array<(ok: boolean) => void>>();
  const hasIO = typeof IntersectionObserver !== "undefined";
  let destroyed = false;

  let observers: IntersectionObserver[] = [];
  let viewportPx = 0;

  // Mutators are called from DiffFile effects and read the state they
  // write. Those reads go through untrack: otherwise the calling effect
  // would depend on, and re-run for, its own writes.
  function setZone(path: string, z: Zone): void {
    if (untrack(() => zones[path]) !== z) zones[path] = z;
  }

  function recompute(path: string): void {
    const f = flags.get(path);
    if (!f) return;
    if (f.inMount) f.mounted = true;
    else if (!f.inKeep) f.mounted = false;
    setZone(path, f.mounted ? "mount" : f.inPrefetch ? "prefetch" : "far");
  }

  function handler(key: "inMount" | "inPrefetch" | "inKeep"): IntersectionObserverCallback {
    return (entries) => {
      for (const e of entries) {
        const path = pathByEl.get(e.target);
        if (path === undefined) continue;
        const f = flags.get(path);
        if (!f) continue;
        f[key] = e.isIntersecting;
        recompute(path);
      }
    };
  }

  const visibleHandler: IntersectionObserverCallback = (entries) => {
    for (const e of entries) {
      const path = pathByEl.get(e.target);
      if (path === undefined) continue;
      let v: Visibility;
      if (e.isIntersecting) v = "visible";
      else {
        const top = e.rootBounds?.top ?? 0;
        v = e.boundingClientRect.bottom <= top ? "above" : "below";
      }
      if (untrack(() => visibilities[path]) !== v) visibilities[path] = v;
    }
  };

  function margin(viewports: number): string {
    return `${Math.round(viewports * viewportPx)}px 0px`;
  }

  function buildObservers(): void {
    for (const o of observers) o.disconnect();
    observers = [];
    if (!hasIO || destroyed) return;
    viewportPx = root.clientHeight || FALLBACK_VIEWPORT_PX;
    observers = [
      new IntersectionObserver(handler("inMount"), { root, rootMargin: margin(mountVp) }),
      new IntersectionObserver(handler("inPrefetch"), { root, rootMargin: margin(prefetchVp) }),
      new IntersectionObserver(handler("inKeep"), { root, rootMargin: margin(unmountVp) }),
      new IntersectionObserver(visibleHandler, { root, rootMargin: margin(0) }),
    ];
    for (const el of elByPath.values()) {
      for (const o of observers) o.observe(el);
    }
  }

  buildObservers();

  let resizeObserver: ResizeObserver | undefined;
  if (hasIO && typeof ResizeObserver !== "undefined") {
    resizeObserver = new ResizeObserver(() => {
      const h = root.clientHeight || FALLBACK_VIEWPORT_PX;
      if (h !== viewportPx) buildObservers();
    });
    resizeObserver.observe(root);
  }

  function resolveWaiters(path: string, ok: boolean): void {
    const list = waiters.get(path);
    if (!list) return;
    waiters.delete(path);
    for (const r of list) r(ok);
  }

  const api: DiffWindow = {
    observe(path, el) {
      const prev = elByPath.get(path);
      if (prev) {
        for (const o of observers) o.unobserve(prev);
        pathByEl.delete(prev);
      }
      elByPath.set(path, el);
      pathByEl.set(el, path);
      flags.set(path, { inMount: false, inPrefetch: false, inKeep: false, mounted: false });
      if (!hasIO) {
        // No IntersectionObserver (jsdom, very old browsers): everything
        // is treated as near the viewport.
        setZone(path, "mount");
      } else {
        setZone(path, "far");
        for (const o of observers) o.observe(el);
      }
      return () => {
        if (elByPath.get(path) !== el) return;
        for (const o of observers) o.unobserve(el);
        elByPath.delete(path);
        pathByEl.delete(el);
        flags.delete(path);
        resolveWaiters(path, false);
        delete zones[path];
        delete visibilities[path];
      };
    },

    zone(path) {
      if ((pinCounts[path] ?? 0) > 0) return "mount";
      return zones[path] ?? "far";
    },

    pin(path, _reason) {
      pinCounts[path] = untrack(() => pinCounts[path] ?? 0) + 1;
      let released = false;
      return () => {
        if (released) return;
        released = true;
        const n = untrack(() => pinCounts[path] ?? 1) - 1;
        if (n <= 0) delete pinCounts[path];
        else pinCounts[path] = n;
      };
    },

    isPinned(path) {
      return (pinCounts[path] ?? 0) > 0;
    },

    rememberHeight(path, px) {
      // A detached or not-yet-laid-out body measures 0; keep the last
      // real measurement instead.
      if (px > 0) heights.set(path, px);
    },

    height(path) {
      return heights.get(path);
    },

    visibility(path) {
      return visibilities[path] ?? "unknown";
    },

    setBodyMounted(path, mounted) {
      if (untrack(() => bodyMounted[path] ?? false) !== mounted) bodyMounted[path] = mounted;
      if (mounted) resolveWaiters(path, true);
    },

    isBodyMounted(path) {
      return bodyMounted[path] ?? false;
    },

    whenBodyMounted(path) {
      if (untrack(() => bodyMounted[path])) return Promise.resolve(true);
      if (destroyed) return Promise.resolve(false);
      return new Promise<boolean>((resolve) => {
        const list = waiters.get(path) ?? [];
        list.push(resolve);
        waiters.set(path, list);
      });
    },

    destroy() {
      destroyed = true;
      for (const o of observers) o.disconnect();
      observers = [];
      resizeObserver?.disconnect();
      for (const path of [...waiters.keys()]) resolveWaiters(path, false);
      pathByEl.clear();
      elByPath.clear();
      flags.clear();
    },
  };
  return api;
}

// Placeholder height for a body that has never been measured:
// (changed lines + 3 rows per hunk) * row height. The 3 rows cover the
// hunk header plus typical context and gap rows. Before a lazy file's
// hunks load, assume one hunk per 20 changed lines.
export function estimateBodyHeight(
  file: { additions: number; deletions: number; hunks: readonly unknown[] | null | undefined },
  rowHeightPx: number,
): number {
  const lines = file.additions + file.deletions;
  const hunks = file.hunks && file.hunks.length > 0
    ? file.hunks.length
    : Math.max(1, Math.ceil(lines / 20));
  return (lines + 3 * hunks) * rowHeightPx;
}

// One rendered diff line's height, measured once from the first body that
// mounts (see measureRowHeight). 20px until then, matching DiffLine's
// line-height.
const FALLBACK_ROW_HEIGHT_PX = 20;
let measuredRowHeight: number | undefined;

export function rowHeightPx(): number {
  return measuredRowHeight ?? FALLBACK_ROW_HEIGHT_PX;
}

export function measureRowHeight(body: HTMLElement): void {
  if (measuredRowHeight !== undefined) return;
  const line = body.querySelector<HTMLElement>(".line-wrap");
  const h = line?.getBoundingClientRect().height ?? 0;
  if (h > 0) measuredRowHeight = h;
}

// True while any text editor inside `root` (textarea, text input or
// contenteditable) is focused or holds text. DiffFile pins its body
// while this holds, so component-local drafts (comment edits, replies,
// follow-ups) are never destroyed by an unmount.
export function hasActiveEditor(root: Element): boolean {
  const active = root.ownerDocument.activeElement;
  const editors = root.querySelectorAll<HTMLElement>(
    'textarea, input:not([type]), input[type="text"], input[type="search"], [contenteditable]:not([contenteditable="false"])',
  );
  for (const el of editors) {
    if (el === active) return true;
    if (el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) {
      if (el.value !== "") return true;
    } else if ((el.textContent ?? "") !== "") {
      return true;
    }
  }
  return false;
}

// Context plumbing so components under DiffView (and Task 8's jump
// helpers) can reach the window. The getter is read lazily because
// DiffView creates the window only once its scroll root has mounted.
const DIFF_WINDOW_KEY = Symbol("diff-window");

export function setDiffWindowContext(get: () => DiffWindow | undefined): void {
  setContext(DIFF_WINDOW_KEY, get);
}

export function getDiffWindow(): DiffWindow | undefined {
  const get = getContext<(() => DiffWindow | undefined) | undefined>(DIFF_WINDOW_KEY);
  return get?.();
}
