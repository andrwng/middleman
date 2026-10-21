import { describe, expect, it, vi } from "vitest";
import { HunkLoader, type HunkRequestFile } from "./hunkLoader.js";
import type { DiffFile } from "../api/types.js";

function diffFile(path: string): DiffFile {
  return {
    path,
    old_path: path,
    status: "modified",
    is_binary: false,
    is_whitespace_only: false,
    additions: 1,
    deletions: 0,
    hunks: [],
  };
}

// Manual scheduler: callbacks queue up until tick() runs them, standing
// in for requestAnimationFrame.
function manualSchedule() {
  let pending: Array<() => void> = [];
  return {
    schedule: (cb: () => void) => {
      pending.push(cb);
    },
    tick: () => {
      const run = pending;
      pending = [];
      for (const cb of run) cb();
    },
    pendingCount: () => pending.length,
  };
}

interface Deferred {
  files: HunkRequestFile[];
  signal: AbortSignal;
  resolve: (files: DiffFile[]) => void;
  reject: (err: unknown) => void;
}

function setup(opts: { maxFiles?: number; maxLines?: number } = {}) {
  const sched = manualSchedule();
  const calls: Deferred[] = [];
  const onLoaded = vi.fn();
  const onError = vi.fn();
  const loader = new HunkLoader({
    fetchBatch: (files, signal) =>
      new Promise<DiffFile[]>((resolve, reject) => {
        calls.push({ files, signal, resolve, reject });
      }),
    schedule: sched.schedule,
    onLoaded,
    onError,
    ...opts,
  });
  return { loader, sched, calls, onLoaded, onError };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("HunkLoader", () => {
  it("coalesces requests made in one scheduled tick into one fetch", async () => {
    const { loader, sched, calls, onLoaded } = setup();
    loader.request({ path: "a.ts", lines: 10 });
    loader.request({ path: "b.ts", lines: 10 });
    loader.request({ path: "c.ts", old_path: "old-c.ts", lines: 10 });

    expect(sched.pendingCount()).toBe(1);
    expect(calls).toHaveLength(0);
    expect(loader.state("a.ts")).toBe("loading");

    sched.tick();
    expect(calls).toHaveLength(1);
    expect(calls[0]!.files.map((f) => f.path)).toEqual(["a.ts", "b.ts", "c.ts"]);
    expect(calls[0]!.files[2]!.old_path).toBe("old-c.ts");

    calls[0]!.resolve([diffFile("a.ts"), diffFile("b.ts"), diffFile("c.ts")]);
    await flush();

    expect(loader.state("a.ts")).toBe("loaded");
    expect(loader.state("c.ts")).toBe("loaded");
    expect(onLoaded).toHaveBeenCalledTimes(1);
    expect(onLoaded.mock.calls[0]![0]).toHaveLength(3);
  });

  it("splits batches at 50 files", () => {
    const { loader, sched, calls } = setup();
    for (let i = 0; i < 120; i++) loader.request({ path: `f${i}.ts`, lines: 1 });
    sched.tick();
    expect(calls.map((c) => c.files.length)).toEqual([50, 50, 20]);
  });

  it("splits batches at 5000 lines", () => {
    const { loader, sched, calls } = setup();
    // 3000 + 1500 = 4500 fits; adding another 1500 would exceed 5000.
    loader.request({ path: "a", lines: 3000 });
    loader.request({ path: "b", lines: 1500 });
    loader.request({ path: "c", lines: 1500 });
    loader.request({ path: "d", lines: 3500 });
    sched.tick();
    expect(calls.map((c) => c.files.map((f) => f.path))).toEqual([
      ["a", "b"],
      ["c", "d"],
    ]);
    for (const c of calls) {
      const total = c.files.reduce((n, f) => n + f.lines, 0);
      expect(total).toBeLessThanOrEqual(5000);
    }
  });

  it("sends a single file over 5000 lines alone", () => {
    const { loader, sched, calls } = setup();
    loader.request({ path: "small1", lines: 10 });
    loader.request({ path: "huge", lines: 20000 });
    loader.request({ path: "small2", lines: 10 });
    sched.tick();
    expect(calls.map((c) => c.files.map((f) => f.path))).toEqual([
      ["small1"],
      ["huge"],
      ["small2"],
    ]);
  });

  it("ignores repeat requests while queued, in flight, or loaded", async () => {
    const { loader, sched, calls } = setup();
    loader.request({ path: "a", lines: 1 });
    loader.request({ path: "a", lines: 1 });
    sched.tick();
    expect(calls).toHaveLength(1);
    expect(calls[0]!.files).toHaveLength(1);

    // In flight.
    loader.request({ path: "a", lines: 1 });
    expect(sched.pendingCount()).toBe(0);

    calls[0]!.resolve([diffFile("a")]);
    await flush();
    // Loaded.
    loader.request({ path: "a", lines: 1 });
    sched.tick();
    expect(calls).toHaveLength(1);
  });

  it("reset aborts in-flight fetches and clears all state", async () => {
    const { loader, sched, calls, onLoaded } = setup();
    loader.request({ path: "a", lines: 1 });
    sched.tick();
    loader.request({ path: "b", lines: 1 }); // queued, not yet flushed

    loader.reset();
    expect(calls[0]!.signal.aborted).toBe(true);
    expect(loader.state("a")).toBe("idle");
    expect(loader.state("b")).toBe("idle");

    // The stale scheduled flush must not fire a fetch for "b".
    sched.tick();
    expect(calls).toHaveLength(1);

    // A late response from the aborted fetch is ignored.
    calls[0]!.resolve([diffFile("a")]);
    await flush();
    expect(onLoaded).not.toHaveBeenCalled();
    expect(loader.state("a")).toBe("idle");

    // After reset, requests work again.
    loader.request({ path: "a", lines: 1 });
    sched.tick();
    expect(calls).toHaveLength(2);
  });

  it("marks paths error on failure, and a later request retries", async () => {
    const { loader, sched, calls, onError, onLoaded } = setup();
    loader.request({ path: "a", lines: 1 });
    loader.request({ path: "b", lines: 1 });
    sched.tick();
    const err = new Error("boom");
    calls[0]!.reject(err);
    await flush();

    expect(loader.state("a")).toBe("error");
    expect(loader.state("b")).toBe("error");
    expect(onError).toHaveBeenCalledWith(["a", "b"], err);

    loader.request({ path: "a", lines: 1 });
    expect(loader.state("a")).toBe("loading");
    sched.tick();
    expect(calls).toHaveLength(2);
    expect(calls[1]!.files.map((f) => f.path)).toEqual(["a"]);
    calls[1]!.resolve([diffFile("a")]);
    await flush();
    expect(loader.state("a")).toBe("loaded");
    expect(onLoaded).toHaveBeenCalledTimes(1);
  });

  it("reports requested files missing from the response as errors", async () => {
    const { loader, sched, calls, onError, onLoaded } = setup();
    loader.request({ path: "a", lines: 1 });
    loader.request({ path: "gone", lines: 1 });
    sched.tick();
    calls[0]!.resolve([diffFile("a")]);
    await flush();

    expect(loader.state("a")).toBe("loaded");
    expect(loader.state("gone")).toBe("error");
    expect(onLoaded.mock.calls[0]![0].map((f: DiffFile) => f.path)).toEqual(["a"]);
    expect(onError.mock.calls[0]![0]).toEqual(["gone"]);
  });
});
