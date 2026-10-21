// Framework-free batcher for lazy diff hunks.
//
// Callers request files one at a time (typically as they scroll into
// view); requests made within one scheduled tick (default: one animation
// frame) are coalesced and split into POST /diff/hunks batches of at most
// `maxFiles` files and `maxLines` changed lines. A single file larger than
// `maxLines` is sent alone. Each file contributes at most two paths (path
// and old_path), so 50 files stay within the server's 100-path cap.

import type { DiffFile } from "../api/types.js";

export interface HunkRequestFile {
  path: string;
  old_path?: string;
  lines: number;
}

export type HunkState = "idle" | "loading" | "loaded" | "error";

export interface HunkLoaderOptions {
  fetchBatch: (files: HunkRequestFile[], signal: AbortSignal) => Promise<DiffFile[]>;
  schedule?: (cb: () => void) => void;
  maxFiles?: number;
  maxLines?: number;
  onLoaded: (files: DiffFile[]) => void;
  onError: (paths: string[], err: unknown) => void;
}

// Raised (through onError) for requested paths the server did not return.
export class MissingHunksError extends Error {
  constructor(paths: string[]) {
    super(`hunks missing from response: ${paths.join(", ")}`);
    this.name = "MissingHunksError";
  }
}

function defaultSchedule(cb: () => void): void {
  if (typeof requestAnimationFrame === "function") {
    requestAnimationFrame(() => cb());
  } else {
    setTimeout(cb, 0);
  }
}

export function splitBatches(
  files: HunkRequestFile[],
  maxFiles: number,
  maxLines: number,
): HunkRequestFile[][] {
  const batches: HunkRequestFile[][] = [];
  let current: HunkRequestFile[] = [];
  let lines = 0;
  for (const f of files) {
    const full = current.length >= maxFiles;
    const overLines = current.length > 0 && lines + f.lines > maxLines;
    if (full || overLines) {
      batches.push(current);
      current = [];
      lines = 0;
    }
    current.push(f);
    lines += f.lines;
  }
  if (current.length > 0) batches.push(current);
  return batches;
}

export class HunkLoader {
  private readonly fetchBatch: HunkLoaderOptions["fetchBatch"];
  private readonly schedule: (cb: () => void) => void;
  private readonly maxFiles: number;
  private readonly maxLines: number;
  private readonly onLoaded: HunkLoaderOptions["onLoaded"];
  private readonly onError: HunkLoaderOptions["onError"];

  // "loading" covers both queued and in-flight paths.
  private states = new Map<string, HunkState>();
  private queue: HunkRequestFile[] = [];
  private flushScheduled = false;
  private controllers = new Set<AbortController>();
  // Bumped by reset() so stale scheduled flushes and late responses
  // from a previous generation are ignored.
  private generation = 0;

  constructor(opts: HunkLoaderOptions) {
    this.fetchBatch = opts.fetchBatch;
    this.schedule = opts.schedule ?? defaultSchedule;
    this.maxFiles = opts.maxFiles ?? 50;
    this.maxLines = opts.maxLines ?? 5000;
    this.onLoaded = opts.onLoaded;
    this.onError = opts.onError;
  }

  request(file: HunkRequestFile): void {
    const s = this.states.get(file.path);
    if (s === "loading" || s === "loaded") return;
    this.states.set(file.path, "loading");
    this.queue.push(file);
    if (this.flushScheduled) return;
    this.flushScheduled = true;
    const gen = this.generation;
    this.schedule(() => {
      if (gen !== this.generation) return;
      this.flushScheduled = false;
      this.flush();
    });
  }

  state(path: string): HunkState {
    return this.states.get(path) ?? "idle";
  }

  reset(): void {
    this.generation++;
    for (const ac of this.controllers) ac.abort();
    this.controllers.clear();
    this.states.clear();
    this.queue = [];
    this.flushScheduled = false;
  }

  private flush(): void {
    const pending = this.queue;
    this.queue = [];
    for (const batch of splitBatches(pending, this.maxFiles, this.maxLines)) {
      void this.run(batch);
    }
  }

  private async run(batch: HunkRequestFile[]): Promise<void> {
    const gen = this.generation;
    const ac = new AbortController();
    this.controllers.add(ac);
    const paths = batch.map((f) => f.path);
    try {
      const result = await this.fetchBatch(batch, ac.signal);
      if (gen !== this.generation || ac.signal.aborted) return;
      const wanted = new Set(paths);
      const loaded: DiffFile[] = [];
      for (const f of result) {
        if (!wanted.has(f.path)) continue;
        wanted.delete(f.path);
        this.states.set(f.path, "loaded");
        loaded.push(f);
      }
      if (loaded.length > 0) this.onLoaded(loaded);
      if (wanted.size > 0) {
        const missing = [...wanted];
        for (const p of missing) this.states.set(p, "error");
        this.onError(missing, new MissingHunksError(missing));
      }
    } catch (err) {
      if (gen !== this.generation || ac.signal.aborted) return;
      for (const p of paths) this.states.set(p, "error");
      this.onError(paths, err);
    } finally {
      this.controllers.delete(ac);
    }
  }
}
