import { Marked } from "marked";
import type { TokenizerAndRendererExtension } from "marked";
import DOMPurify from "dompurify";

interface RepoContext {
  owner: string;
  name: string;
  // Optional commit/tree SHA (or branch/tag name) to anchor file:line
  // links at. When absent, file references are not linked. Anchoring
  // at a specific SHA matters because file content drifts: a line
  // number mentioned by Claude is meaningful only at the snapshot it
  // was reasoning over.
  sha?: string;
  // Optional resolver for bare filenames. Returns the unique full
  // path for a basename if the caller has resolved it (typically
  // via a server-side `git ls-tree` lookup), null when the basename
  // is ambiguous or missing, undefined when resolution hasn't
  // happened yet. Multi-segment paths are passed through unchanged
  // when the resolver returns the same string. When omitted, only
  // multi-segment paths are linked.
  resolveBareFile?: (basename: string) => string | null | undefined;
  // Cache-bust token. Callers using a resolver should bump this
  // when resolutions arrive so renderMarkdown's cached HTML doesn't
  // mask the new state.
  cacheBust?: string;
}

// Extensions Claude commonly mentions in review prose. Whitelisted
// to avoid grabbing false positives like "version 1.2.3:5" or time
// strings. Intentionally narrow — better to miss a few rare exts
// than to mangle prose by linking non-paths.
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
];

const FILE_REF_EXT_GROUP = FILE_REF_EXTS.join("|");

// Captures: 1=path (with required extension), 2=line, 3=optional end line.
// Both bare filenames (e.g. "huma_routes.go:42") and multi-segment
// paths are matched here. Bare filenames are only LINKED if a resolver
// is provided (and finds a unique match) — otherwise they render as
// plain text. This avoids the 404 trap from blindly guessing the
// directory.
const FILE_REF_RE = new RegExp(
  String.raw`([\w./\-]+\.(?:` +
    FILE_REF_EXT_GROUP +
    String.raw`))(?::(\d+)(?:[-:](\d+))?)`,
);

const FILE_REF_RE_ANCHORED = new RegExp(
  "^" + FILE_REF_RE.source,
);

function fileRefExtension(repo?: RepoContext): TokenizerAndRendererExtension {
  return {
    name: "fileRef",
    level: "inline",
    start(src: string): number | undefined {
      // Quick reject: file refs always have a colon followed by a
      // digit. Bail before running the full regex if neither shows up.
      const colonIdx = src.indexOf(":");
      if (colonIdx < 0) return undefined;
      const m = src.match(FILE_REF_RE);
      if (!m || m.index === undefined) return undefined;
      return m.index;
    },
    tokenizer(
      this: { lexer: { state: { inLink: boolean } } },
      src: string,
    ):
      | { type: string; raw: string; path: string; line: number; endLine?: number; text: string }
      | undefined {
      if (this.lexer.state.inLink) return undefined;
      if (!repo?.sha) return undefined;
      const m = src.match(FILE_REF_RE_ANCHORED);
      if (!m) return undefined;
      const path = m[1]!;
      const line = parseInt(m[2]!, 10);
      // Defensive: skip path-like text that's actually a URL hash or
      // similar — paths starting with a dot-only segment ("./" or
      // "../") are common in prose; allow those. Reject things that
      // look like ".some.thing.go" with no slash and a leading dot.
      if (path.startsWith(".") && !path.startsWith("./") && !path.startsWith("../")) {
        return undefined;
      }
      const base = {
        type: "fileRef",
        raw: m[0],
        path,
        line,
        text: m[0],
      };
      return m[3] ? { ...base, endLine: parseInt(m[3], 10) } : base;
    },
    renderer(token): string {
      const t = token as unknown as {
        path: string;
        line: number;
        endLine?: number;
        text: string;
      };
      const r = repo;
      if (!r?.sha) return t.text;
      const cleanPath = t.path.replace(/^\.\//, "");

      // Bare filename (no slash): only link when the caller's
      // resolver gives us a unique path. Without a resolver, leave
      // as plain text — guessing the directory produces 404s.
      let target: string | null = cleanPath;
      if (!cleanPath.includes("/")) {
        const resolved = r.resolveBareFile?.(cleanPath);
        target = typeof resolved === "string" && resolved !== "" ? resolved : null;
      } else if (r.resolveBareFile) {
        // Multi-segment paths run through the resolver too so the
        // server can verify the path exists at this SHA. A non-string
        // result means "couldn't verify" — fall back to plain text
        // rather than emit a known-bad link.
        const resolved = r.resolveBareFile(cleanPath);
        if (resolved === null) {
          target = null;
        } else if (typeof resolved === "string" && resolved !== "") {
          target = resolved;
        }
        // undefined = not yet resolved → keep cleanPath optimistically
        // until the resolution arrives and re-render replaces it.
      }
      if (target === null) return t.text;

      const fragment = t.endLine
        ? `L${t.line}-L${t.endLine}`
        : `L${t.line}`;
      const href =
        `https://github.com/${r.owner}/${r.name}/blob/` +
        `${encodeURIComponent(r.sha)}/${target}#${fragment}`;
      return `<a class="file-ref" href="${href}" target="_blank" rel="noopener">${t.text}</a>`;
    },
  };
}

function itemRefExtension(repo?: RepoContext): TokenizerAndRendererExtension {
  return {
    name: "itemRef",
    level: "inline",
    start(src: string): number | undefined {
      // Cross-repo: look for word chars before #
      const crossIdx = src.search(/[\w.-]+\/[\w.-]+#\d/);
      // Bare: look for # preceded by start or non-word
      const bareIdx = src.search(/(^|[^\w])#\d/);
      const adjusted = bareIdx >= 0 && src[bareIdx] !== "#"
        ? bareIdx + 1
        : bareIdx;
      if (crossIdx >= 0 && (adjusted < 0 || crossIdx <= adjusted)) {
        return crossIdx;
      }
      return adjusted >= 0 ? adjusted : undefined;
    },
    tokenizer(this: { lexer: { state: { inLink: boolean } } }, src: string): { type: string; raw: string; owner: string; name: string; number: number; text: string } | undefined {
      // Don't tokenize inside markdown link/image labels
      // to avoid producing invalid nested <a> elements.
      if (this.lexer.state.inLink) return undefined;

      // Cross-repo: owner/name#123 (with trailing word boundary)
      const crossMatch = src.match(
        /^([\w.-]+)\/([\w.-]+)#(\d+)(?!\w)/,
      );
      if (crossMatch) {
        return {
          type: "itemRef",
          raw: crossMatch[0],
          owner: crossMatch[1]!,
          name: crossMatch[2]!,
          number: parseInt(crossMatch[3]!, 10),
          text: crossMatch[0],
        };
      }
      // Bare ref: #123 (with trailing word boundary)
      const bareMatch = src.match(/^#(\d+)(?!\w)/);
      if (bareMatch && repo) {
        return {
          type: "itemRef",
          raw: bareMatch[0],
          owner: repo.owner,
          name: repo.name,
          number: parseInt(bareMatch[1]!, 10),
          text: bareMatch[0],
        };
      }
      return undefined;
    },
    renderer(token): string {
      const t = token as unknown as { owner: string; name: string; number: number; text: string };
      const href = `https://github.com/${t.owner}/${t.name}/issues/${t.number}`;
      return `<a class="item-ref" href="${href}" data-owner="${t.owner}" data-name="${t.name}" data-number="${t.number}">${t.text}</a>`;
    },
  };
}

const htmlCache = new Map<string, string>();
const markedCache = new Map<string, Marked>();

// We can't safely cache the Marked instance when a resolver
// closure is involved — different cards pass different closures
// reading from different state. Build a fresh instance per call
// in that case; it's cheap enough.
function getMarked(repo?: RepoContext): Marked {
  if (repo?.resolveBareFile) {
    const m = new Marked({ breaks: true, gfm: true });
    m.use({
      extensions: [itemRefExtension(repo), fileRefExtension(repo)],
    });
    return m;
  }
  const key = repo
    ? `${repo.owner}/${repo.name}@${repo.sha ?? ""}`
    : "";
  let instance = markedCache.get(key);
  if (!instance) {
    instance = new Marked({ breaks: true, gfm: true });
    instance.use({
      extensions: [itemRefExtension(repo), fileRefExtension(repo)],
    });
    markedCache.set(key, instance);
  }
  return instance;
}

export function renderMarkdown(
  raw: string,
  repo?: RepoContext,
): string {
  if (!raw) return "";
  // Skip the HTML cache when a resolver is in play — its output
  // depends on resolver state that the (raw, repo) tuple doesn't
  // capture. cacheBust still buys us caching when the caller
  // explicitly bumps it.
  const skipCache = !!repo?.resolveBareFile && !repo.cacheBust;
  const key = repo
    ? `${repo.owner}/${repo.name}@${repo.sha ?? ""}#${repo.cacheBust ?? ""}\0${raw}`
    : raw;
  if (!skipCache) {
    const cached = htmlCache.get(key);
    if (cached !== undefined) return cached;
  }

  const html = DOMPurify.sanitize(
    getMarked(repo).parse(raw) as string,
    { ADD_ATTR: ["target", "data-owner", "data-name", "data-number"] },
  );
  if (!skipCache) {
    if (htmlCache.size > 500) htmlCache.clear();
    htmlCache.set(key, html);
  }
  return html;
}
