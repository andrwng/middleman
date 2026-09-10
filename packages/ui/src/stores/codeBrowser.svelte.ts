import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

export type TreeEntry = components["schemas"]["TreeEntryJSON"];

export type CodeBrowserStatus = "idle" | "loading" | "ready" | "error" | "missing";

export interface CodeBrowserStoreOptions {
  client: MiddlemanClient;
}

export interface OpenCodeBrowserOptions {
  // Bypasses the saved bookmark, forcing open() to seed at
  // activeFilePath regardless of what (if anything) is already
  // bookmarked for this PR. Set by callers that pass a specific,
  // deliberately-chosen path -- e.g. "browse this hit" from the
  // symbol-refs gutter -- as opposed to callers that pass a mere
  // fallback default (e.g. the diff's active file), for whom resuming
  // at the bookmark is the whole point.
  forcePath?: boolean;
}

function detail(err: unknown, fallback: string): string {
  return (err as { detail?: string } | undefined)?.detail ?? fallback;
}

// createCodeBrowserStore backs the code browser panel: a directory tree plus
// a single open file, scoped to one PR at one SHA. open() seeds the current
// path from the server's saved bookmark (falling back to the diff's active
// file when nothing is bookmarked yet), then loads the root directory listing
// and the seeded file's content. navigateTo() moves to a new file, persisting
// the choice as the new bookmark so the panel reopens where the reader left
// it.
export function createCodeBrowserStore(opts: CodeBrowserStoreOptions) {
  const client = opts.client;

  let isOpen = $state(false);
  let path = $state<string | null>(null);
  let entriesByDir = $state<Map<string, TreeEntry[]>>(new Map());
  let content = $state<string | null>(null);
  let status = $state<CodeBrowserStatus>("idle");
  let errorMsg = $state<string | null>(null);

  let owner = "";
  let name = "";
  let number = 0;
  let sha = "";

  async function loadTree(dirPath: string): Promise<void> {
    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/tree",
      { params: { path: { owner, name, number }, query: { path: dirPath, sha } } },
    );
    if (err || !data) return;
    const next = new Map(entriesByDir);
    next.set(dirPath, data.entries ?? []);
    entriesByDir = next;
  }

  async function loadFile(filePath: string): Promise<void> {
    status = "loading";
    errorMsg = null;
    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/blob",
      { params: { path: { owner, name, number }, query: { path: filePath, sha } } },
    );
    if (err || !data) {
      status = "missing";
      content = null;
      errorMsg = err ? detail(err, "Failed to load file") : "Failed to load file";
      return;
    }
    content = data.content;
    status = "ready";
  }

  async function open(
    o: string,
    n: string,
    num: number,
    s: string,
    activeFilePath: string,
    opts?: OpenCodeBrowserOptions,
  ): Promise<void> {
    owner = o;
    name = n;
    number = num;
    sha = s;
    isOpen = true;
    entriesByDir = new Map();

    const { data } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      { params: { path: { owner, name, number } } },
    );
    const bookmarked = data?.path;
    path = !opts?.forcePath && bookmarked && bookmarked !== "" ? bookmarked : activeFilePath;

    await loadTree("");
    if (path) await loadFile(path);
  }

  function close(): void {
    isOpen = false;
    path = null;
    entriesByDir = new Map();
    content = null;
    status = "idle";
    errorMsg = null;
  }

  async function navigateTo(newPath: string): Promise<void> {
    path = newPath;
    await client.PUT(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      { params: { path: { owner, name, number } }, body: { path: newPath } },
    );
    await loadFile(newPath);
  }

  return {
    get isOpen() {
      return isOpen;
    },
    get path() {
      return path;
    },
    get entriesByDir() {
      return entriesByDir;
    },
    get content() {
      return content;
    },
    get status() {
      return status;
    },
    get error() {
      return errorMsg;
    },
    open,
    close,
    navigateTo,
    loadTree,
  };
}

export type CodeBrowserStore = ReturnType<typeof createCodeBrowserStore>;
