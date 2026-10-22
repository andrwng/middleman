import { createReviewDraftsApi, type PRRef, type ServerReviewRun } from "./reviewDraftsApi.js";

export interface AIReviewRunStoreOptions {
  getBasePath?: () => string;
  // Called on each poll while a run is active and once more when it
  // ends, so the caller can refresh suggestions as Claude adds them.
  onActivity?: () => void;
}

const POLL_MS = 1500;

// createAIReviewRunStore tracks the latest Review with Claude run for
// the PR in view. It polls only while a run is queued or running.
export function createAIReviewRunStore(opts?: AIReviewRunStoreOptions) {
  const api = createReviewDraftsApi(opts?.getBasePath ?? (() => "/"));

  let pr = $state<PRRef | null>(null);
  let run = $state<ServerReviewRun | null>(null);
  let starting = $state(false);
  let errorMsg = $state<string | null>(null);
  // Whether the floating Claude review panel is open. Shared so the
  // toolbar pill and the panel's own toggle drive the same panel.
  let panelOpen = $state(false);
  let pollHandle: ReturnType<typeof setInterval> | null = null;
  // Bumped on PR change so a late response for the old PR is dropped.
  let gen = 0;

  function active(r: ServerReviewRun | null): boolean {
    return r !== null && (r.status === "queued" || r.status === "running");
  }

  async function refresh(): Promise<void> {
    const cur = pr;
    if (!cur) return;
    const myGen = gen;
    try {
      const next = await api.latestRun(cur);
      if (myGen !== gen) return;
      const wasActive = active(run);
      run = next;
      if (active(next) || wasActive) opts?.onActivity?.();
    } catch (err) {
      if (myGen === gen) errorMsg = err instanceof Error ? err.message : String(err);
    }
  }

  function arm(): void {
    if (pollHandle !== null) clearInterval(pollHandle);
    pollHandle = setInterval(() => {
      if (active(run)) void refresh();
    }, POLL_MS);
  }

  function start(owner: string, name: string, number: number): void {
    const changed = !pr || pr.owner !== owner || pr.name !== name || pr.number !== number;
    if (changed) {
      gen++;
      run = null;
      errorMsg = null;
      panelOpen = false;
    }
    pr = { owner, name, number };
    void refresh();
    arm();
  }

  function stop(): void {
    gen++;
    if (pollHandle !== null) clearInterval(pollHandle);
    pollHandle = null;
    pr = null;
    run = null;
    errorMsg = null;
    panelOpen = false;
  }

  async function startRun(instructions: string): Promise<boolean> {
    const cur = pr;
    if (!cur || starting) return false;
    starting = true;
    errorMsg = null;
    try {
      run = await api.startRun(cur, instructions.trim());
      panelOpen = true; // show progress right away
      opts?.onActivity?.();
      return true;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
      return false;
    } finally {
      starting = false;
    }
  }

  async function cancel(): Promise<void> {
    const cur = pr;
    if (!cur || !run) return;
    try {
      run = await api.cancelRun(cur, run.id);
      opts?.onActivity?.();
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    }
  }

  return {
    start,
    stop,
    refresh,
    startRun,
    cancel,
    getRun: (): ServerReviewRun | null => run,
    isActive: (): boolean => active(run),
    isStarting: (): boolean => starting,
    getError: (): string | null => errorMsg,
    isPanelOpen: (): boolean => panelOpen,
    openPanel: (): void => {
      panelOpen = true;
    },
    closePanel: (): void => {
      panelOpen = false;
    },
    togglePanel: (): void => {
      panelOpen = !panelOpen;
    },
  };
}

export type AIReviewRunStore = ReturnType<typeof createAIReviewRunStore>;
