import type { LocalWorktree } from "../api/types.js";
import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

export type ChangedFile = components["schemas"]["ChangedFileResponse"];
export type WorktreeBase = components["schemas"]["WorktreeBaseResponse"];
export type WorktreeDiffFile = components["schemas"]["DiffFile"];

export interface WorktreesStoreOptions {
  client: MiddlemanClient;
}

interface ChangedFilesEntry {
  base: WorktreeBase | null;
  files: ChangedFile[];
  loading: boolean;
  error: string | null;
  fetchedAt: number;
}

interface DiffEntry {
  base: WorktreeBase | null;
  files: WorktreeDiffFile[];
  loading: boolean;
  error: string | null;
  fetchedAt: number;
}

function apiErrorMessage(
  error: { detail?: string; title?: string },
  fallback: string,
): string {
  return error.detail ?? error.title ?? fallback;
}

// createWorktreesStore manages the list of git worktrees discovered
// under each enrolled repo's configured local_path. It mirrors the
// shape of createPullsStore: one fetch endpoint, a flat list in
// state, and a derived grouping helper for the sidebar.
export function createWorktreesStore(opts: WorktreesStoreOptions) {
  const apiClient = opts.client;

  let worktrees = $state<LocalWorktree[]>([]);
  let loading = $state(false);
  let storeError = $state<string | null>(null);
  let changedFilesById = $state<Record<number, ChangedFilesEntry>>({});
  let diffById = $state<Record<number, DiffEntry>>({});
  let selectedId = $state<number | null>(null);

  function getWorktrees(): LocalWorktree[] {
    return worktrees;
  }
  function isLoading(): boolean {
    return loading;
  }
  function getError(): string | null {
    return storeError;
  }
  function getById(id: number): LocalWorktree | null {
    return worktrees.find((w) => w.id === id) ?? null;
  }
  function getChangedFiles(id: number): ChangedFilesEntry | null {
    return changedFilesById[id] ?? null;
  }
  function getDiff(id: number): DiffEntry | null {
    return diffById[id] ?? null;
  }
  function getSelectedId(): number | null {
    return selectedId;
  }
  function selectWorktree(id: number | null): void {
    selectedId = id;
  }

  // Group by `${repo_owner}/${repo_name}` so callers can render a
  // section per repo. Insertion order follows the server response,
  // which orders by (owner, name, path) for stability.
  function worktreesByRepo(): Map<string, LocalWorktree[]> {
    const map = new Map<string, LocalWorktree[]>();
    for (const w of worktrees) {
      const key = `${w.repo_owner}/${w.repo_name}`;
      const bucket = map.get(key);
      if (bucket) bucket.push(w);
      else map.set(key, [w]);
    }
    return map;
  }

  async function loadWorktrees(): Promise<void> {
    loading = true;
    storeError = null;
    try {
      const { data, error } = await apiClient.GET("/worktrees", {});
      if (error) {
        throw new Error(
          apiErrorMessage(error, "failed to load worktrees"),
        );
      }
      worktrees = data?.worktrees ?? [];
    } catch (err) {
      storeError =
        err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // pollRunningTurns refreshes only the has_running_turn flag on
  // existing worktree rows. It's much lighter than loadWorktrees
  // (no row scan, no repo join) so the sidebar can poll it on a
  // tight cadence to keep the "Claude is working" indicator fresh.
  async function pollRunningTurns(): Promise<void> {
    try {
      const { data, error } = await apiClient.GET(
        "/worktrees/running-turns",
        {},
      );
      if (error) return; // silent failure — indicator just won't refresh
      const ids = new Set<number>(data?.worktree_ids ?? []);
      let changed = false;
      const next: LocalWorktree[] = worktrees.map((w) => {
        const has = ids.has(w.id);
        if ((w.has_running_turn ?? false) === has) return w;
        changed = true;
        return { ...w, has_running_turn: has };
      });
      if (changed) {
        worktrees = next;
      }
    } catch {
      // swallow — see above
    }
  }

  async function loadChangedFiles(id: number): Promise<void> {
    const prev = changedFilesById[id] ?? {
      base: null,
      files: [],
      loading: false,
      error: null,
      fetchedAt: 0,
    };
    changedFilesById = {
      ...changedFilesById,
      [id]: { ...prev, loading: true, error: null },
    };
    try {
      const { data, error } = await apiClient.GET(
        "/worktrees/{id}/changed-files",
        { params: { path: { id } } },
      );
      if (error) {
        throw new Error(
          apiErrorMessage(error, "failed to load worktree files"),
        );
      }
      changedFilesById = {
        ...changedFilesById,
        [id]: {
          base: data?.base ?? null,
          files: data?.files ?? [],
          loading: false,
          error: null,
          fetchedAt: Date.now(),
        },
      };
    } catch (err) {
      changedFilesById = {
        ...changedFilesById,
        [id]: {
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : String(err),
        },
      };
    }
  }

  async function loadMarkdownFiles(id: number): Promise<string[]> {
    try {
      const { data, error } = await apiClient.GET("/worktrees/{id}/markdown-files", {
        params: { path: { id } },
      });
      if (error) return [];
      return data?.files ?? [];
    } catch {
      return [];
    }
  }

  async function loadWorktreeDiff(id: number): Promise<void> {
    const prev = diffById[id] ?? {
      base: null,
      files: [],
      loading: false,
      error: null,
      fetchedAt: 0,
    };
    diffById = {
      ...diffById,
      [id]: { ...prev, loading: true, error: null },
    };
    try {
      const { data, error } = await apiClient.GET(
        "/worktrees/{id}/diff",
        { params: { path: { id } } },
      );
      if (error) {
        throw new Error(
          apiErrorMessage(error, "failed to load worktree diff"),
        );
      }
      diffById = {
        ...diffById,
        [id]: {
          base: data?.base ?? null,
          files: data?.files ?? [],
          loading: false,
          error: null,
          fetchedAt: Date.now(),
        },
      };
    } catch (err) {
      diffById = {
        ...diffById,
        [id]: {
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : String(err),
        },
      };
    }
  }

  return {
    getWorktrees,
    isLoading,
    getError,
    getById,
    worktreesByRepo,
    loadWorktrees,
    pollRunningTurns,
    loadMarkdownFiles,
    getChangedFiles,
    loadChangedFiles,
    getDiff,
    loadWorktreeDiff,
    getSelectedId,
    selectWorktree,
  };
}

export type WorktreesStore = ReturnType<typeof createWorktreesStore>;
