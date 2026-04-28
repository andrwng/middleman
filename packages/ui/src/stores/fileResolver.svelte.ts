// Resolves bare filenames in AI-generated markdown (e.g. "huma_routes.go")
// to their unique full path at a given SHA so the renderMarkdown
// extension can deep-link them. Resolution happens server-side via
// POST /repos/{owner}/{name}/resolve-files (which runs `git ls-tree`
// against the bare clone); the store batches and caches results so
// repeat lookups within a session are free.
//
// Usage from a card that renders AI markdown:
//   $effect(() => {
//     const names = extractBasenames(brief.content);
//     void fileResolver.resolve(owner, name, brief.head_sha, names);
//   });
//   <renderMarkdown ... resolveBareFile={(n) => fileResolver.lookup(brief.head_sha, n)} />

export interface FileResolverStoreOptions {
  getBasePath?: () => string;
}

// Lookup return contract:
//   string  → unique full path; render as a link.
//   null    → resolved but ambiguous / missing; render as plain text.
//   undef   → not yet resolved; render as plain text for now (the
//             component should re-render once resolution arrives,
//             which Svelte does automatically because lookup reads
//             a $state-backed map).
export type ResolutionState = string | null | undefined;

export function createFileResolverStore(
  opts?: FileResolverStoreOptions,
) {
  const getBasePath = opts?.getBasePath ?? (() => "/");

  // sha → (basename → unique-path | null). Null is "we asked, no
  // unique match"; absence is "haven't asked yet."
  let cache = $state<Record<string, Record<string, string | null>>>({});

  // In-flight requests, keyed by `${sha}::${name}` so we don't
  // double-fetch the same name from multiple cards in the same
  // render pass.
  const inflight = new Map<string, Promise<void>>();

  async function resolve(
    owner: string,
    name: string,
    sha: string,
    names: string[],
  ): Promise<void> {
    if (!owner || !name || !sha || names.length === 0) return;

    // Filter to names we don't already have a result (or in-flight
    // request) for. Dedupe within the request as well.
    const seen = new Set<string>();
    const todo: string[] = [];
    const sub = cache[sha] ?? {};
    for (const raw of names) {
      const n = raw.trim();
      if (!n || seen.has(n)) continue;
      seen.add(n);
      if (n in sub) continue;
      if (inflight.has(`${sha}::${n}`)) continue;
      todo.push(n);
    }
    if (todo.length === 0) return;

    // Mark each name as in-flight under one shared promise so a
    // late caller for the same name awaits the same fetch.
    const promise = (async () => {
      try {
        const url =
          `${getBasePath()}api/v1/repos/` +
          `${encodeURIComponent(owner)}/` +
          `${encodeURIComponent(name)}/resolve-files`;
        const res = await fetch(url, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ sha, names: todo }),
        });
        if (!res.ok) {
          // On failure, mark every requested name as "ambiguous"
          // (null) so we don't keep retrying the same fetch on
          // every re-render. The caller falls back to plain text.
          markUnresolved(sha, todo);
          return;
        }
        const body = (await res.json()) as {
          resolutions?: Record<string, string>;
        };
        const next: Record<string, string | null> = {
          ...(cache[sha] ?? {}),
        };
        const found = body.resolutions ?? {};
        for (const n of todo) {
          const v = found[n];
          next[n] = typeof v === "string" && v !== "" ? v : null;
        }
        cache = { ...cache, [sha]: next };
      } catch {
        markUnresolved(sha, todo);
      } finally {
        for (const n of todo) inflight.delete(`${sha}::${n}`);
      }
    })();

    for (const n of todo) inflight.set(`${sha}::${n}`, promise);
    await promise;
  }

  function markUnresolved(sha: string, todo: string[]): void {
    const next: Record<string, string | null> = {
      ...(cache[sha] ?? {}),
    };
    for (const n of todo) {
      if (!(n in next)) next[n] = null;
    }
    cache = { ...cache, [sha]: next };
  }

  function lookup(sha: string, n: string): ResolutionState {
    if (!sha || !n) return undefined;
    const sub = cache[sha];
    if (!sub) return undefined;
    return sub[n];
  }

  // Version counter — increments whenever the cache mutates, so
  // callers (renderMarkdown) can include it in their cache keys.
  // This is just `cache` boxed for cheap dependency tracking.
  function getVersion(sha: string): number {
    const sub = cache[sha];
    if (!sub) return 0;
    return Object.keys(sub).length;
  }

  return { resolve, lookup, getVersion };
}

export type FileResolverStore = ReturnType<
  typeof createFileResolverStore
>;

// extractBasenames pulls out plausible file references from AI
// markdown — same shape the linker recognizes — so a card can hand
// them to the resolver without re-parsing the markdown twice. Both
// bare filenames AND multi-segment paths are returned so callers can
// also verify multi-segment refs against the actual tree.
const FILE_REF_EXTS = [
  "go",
  "svelte",
  "ts",
  "tsx",
  "js",
  "jsx",
  "mjs",
  "cjs",
  "sql",
  "yaml",
  "yml",
  "md",
  "py",
  "rs",
  "c",
  "cc",
  "cpp",
  "h",
  "hpp",
  "sh",
  "bash",
  "zsh",
  "toml",
  "json",
  "proto",
  "bzl",
  "lock",
  "mod",
  "sum",
  "kt",
  "kts",
  "java",
  "rb",
  "php",
  "html",
  "css",
  "scss",
  "less",
  "conf",
  "ini",
  "env",
] as const;

const EXTRACT_RE = new RegExp(
  String.raw`([\w./\-]+\.(?:` +
    FILE_REF_EXTS.join("|") +
    String.raw`))(?::\d+)`,
  "g",
);

export function extractFileRefs(markdown: string): string[] {
  if (!markdown) return [];
  const out = new Set<string>();
  for (const m of markdown.matchAll(EXTRACT_RE)) {
    const path = m[1]!.replace(/^\.\//, "");
    // Skip leading-dot weirdness like ".foo.go" with no slash.
    if (path.startsWith(".") && !path.startsWith("../")) continue;
    out.add(path);
  }
  return [...out];
}
