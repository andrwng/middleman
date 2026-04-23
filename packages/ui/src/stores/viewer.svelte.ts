import type { MiddlemanClient } from "../types.js";

export interface ViewerStoreOptions {
  client: MiddlemanClient;
}

// The viewer is "who am I according to the configured GitHub
// token". Used to highlight PRs where I'm on the requested
// reviewer list. Fetched once at startup; the server caches it
// too, so the round-trip here is cheap even on a cold cache.
export function createViewerStore(opts: ViewerStoreOptions) {
  const api = opts.client;

  let login = $state<string | null>(null);
  let name = $state<string | null>(null);
  let loading = $state(false);
  let errorMsg = $state<string | null>(null);

  async function load(): Promise<void> {
    if (loading) return;
    loading = true;
    errorMsg = null;
    try {
      const { data, error } = await api.GET("/me", {});
      if (error || !data) {
        errorMsg =
          (error as { detail?: string; title?: string })?.detail ??
          (error as { detail?: string; title?: string })?.title ??
          "Failed to load viewer";
        return;
      }
      login = data.login ?? null;
      name = data.name ?? null;
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  return {
    getLogin: () => login,
    getName: () => name,
    isLoading: () => loading,
    getError: () => errorMsg,
    load,
  };
}

export type ViewerStore = ReturnType<typeof createViewerStore>;
