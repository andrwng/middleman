import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

export type TreeEntry = components["schemas"]["TreeEntryJSON"];

export type CodeBrowserStatus = "idle" | "loading" | "ready" | "error" | "missing" | "truncated";

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

// ancestorDirs returns every directory (including "", the root) that must
// be expanded/loaded to reveal filePath in the tree -- e.g. "a/b/c.go" ->
// ["", "a", "a/b"]. A root-level file (or an empty/null path) yields just
// [""].
function ancestorDirs(filePath: string | null): string[] {
  const dirs: string[] = [""];
  if (!filePath) return dirs;
  const parts = filePath.split("/").filter((p) => p.length > 0);
  parts.pop(); // drop the filename itself -- only directories are "expanded"
  let acc = "";
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    dirs.push(acc);
  }
  return dirs;
}

// createCodeBrowserStore backs the code browser panel: a directory tree plus
// a single open file, scoped to one PR at one SHA. open() seeds the current
// path from the server's saved bookmark (falling back to the diff's active
// file when nothing is bookmarked yet), auto-expands every ancestor
// directory of that path, then loads those directory listings and the
// seeded file's content. navigateTo() moves to a new file, persisting the
// choice as the new bookmark so the panel reopens where the reader left it.
//
// open() (and everything it kicks off -- loadTree, loadFile) is guarded by
// openSeq: a monotonically increasing counter bumped at the start of every
// open() call. Each async continuation captures the counter's value at the
// moment it started and checks it is still current before writing to store
// state, so a stale open() (e.g. superseded by a second open() fired before
// the first finished -- the normal path when stepping commits changes both
// sha and the active file at once) can never clobber a newer one's result.
export function createCodeBrowserStore(opts: CodeBrowserStoreOptions) {
  const client = opts.client;

  let isOpen = $state(false);
  let path = $state<string | null>(null);
  let entriesByDir = $state<Map<string, TreeEntry[]>>(new Map());
  let expandedDirs = $state<Set<string>>(new Set());
  let content = $state<string | null>(null);
  let status = $state<CodeBrowserStatus>("idle");
  let errorMsg = $state<string | null>(null);

  let owner = "";
  let name = "";
  let number = 0;
  let sha = "";

  // Bumped at the start of every open(); read (never incremented) by
  // loadTree/loadFile to detect that a newer open() has superseded them.
  let openSeq = 0;

  // mySeq defaults to the CURRENT openSeq, evaluated at call time -- correct
  // for call sites with no epoch of their own (toggleDir, navigateTo): they
  // guard only against a brand-new open() starting during their own await.
  // open()'s ancestor-tree loop and its loadFile call instead pass down the
  // epoch open() itself captured at its start, so a call made LATE in an
  // already-superseded open() (openSeq has already moved on by the time
  // this call even begins, not just during its await) is still correctly
  // recognized as stale -- reading "current" openSeq at its own start would
  // just self-reference and catch nothing in that case.
  async function loadTree(dirPath: string, mySeq: number = openSeq): Promise<boolean> {
    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/tree",
      { params: { path: { owner, name, number }, query: { path: dirPath, sha } } },
    );
    if (mySeq !== openSeq) return false; // superseded by a newer open()
    if (err || !data) {
      status = "error";
      errorMsg = err ? detail(err, "Failed to load directory") : "Failed to load directory";
      return false;
    }
    const next = new Map(entriesByDir);
    next.set(dirPath, data.entries ?? []);
    entriesByDir = next;
    return true;
  }

  async function loadFile(filePath: string, mySeq: number = openSeq): Promise<void> {
    status = "loading";
    errorMsg = null;
    const { data, error: err, response } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/blob",
      { params: { path: { owner, name, number }, query: { path: filePath, sha } } },
    );
    if (mySeq !== openSeq) return; // superseded by a newer open()
    if (err || !data) {
      // A 404 means the file genuinely doesn't exist at this SHA -- distinct
      // from any other failure (network, 502, 400), which is a real error
      // and should not be presented as if the file were merely absent.
      status = response?.status === 404 ? "missing" : "error";
      content = null;
      errorMsg = err ? detail(err, "Failed to load file") : "Failed to load file";
      return;
    }
    if (data.truncated) {
      status = "truncated";
      content = null;
      return;
    }
    content = data.content ?? null;
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
    const mySeq = ++openSeq;
    owner = o;
    name = n;
    number = num;
    sha = s;
    isOpen = true;
    entriesByDir = new Map();
    status = "loading";
    errorMsg = null;

    const { data } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      { params: { path: { owner, name, number } } },
    );
    if (mySeq !== openSeq) return; // superseded

    const bookmarked = data?.path;
    const resolved =
      !opts?.forcePath && bookmarked && bookmarked !== "" ? bookmarked : activeFilePath;
    path = resolved;

    const dirs = ancestorDirs(resolved);
    expandedDirs = new Set(dirs.filter((d) => d !== ""));

    for (const dir of dirs) {
      const ok = await loadTree(dir, mySeq);
      if (mySeq !== openSeq) return; // superseded
      if (!ok) return; // loadTree already recorded a real error; don't let loadFile below clobber it
    }

    if (resolved) await loadFile(resolved, mySeq);
  }

  function close(): void {
    isOpen = false;
    path = null;
    entriesByDir = new Map();
    expandedDirs = new Set();
    content = null;
    status = "idle";
    errorMsg = null;
  }

  function toggleDir(dirPath: string): void {
    const next = new Set(expandedDirs);
    if (next.has(dirPath)) {
      next.delete(dirPath);
    } else {
      next.add(dirPath);
      if (!entriesByDir.has(dirPath)) {
        void loadTree(dirPath);
      }
    }
    expandedDirs = next;
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
    get expandedDirs() {
      return expandedDirs;
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
    toggleDir,
  };
}

export type CodeBrowserStore = ReturnType<typeof createCodeBrowserStore>;
