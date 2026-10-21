// Default collapse rules and glob matching for the diff review pane.
//
// Pure module, no Svelte/store dependencies: see
// docs/superpowers/specs/2026-10-21-large-diff-review-design.md,
// section "Default collapse rules" / "Collapse globs".

export type CollapseReason = "user" | "pattern" | "deleted" | "large";

// Collapse threshold: files with more than this many changed lines
// (additions + deletions) collapse by default under rule 4.
export const LARGE_FILE_LINES = 1500;

// Lazy-diff threshold: diffs whose summary totals more than this many
// changed lines load in lazy mode instead of fetching the full /diff payload.
export const LAZY_DIFF_LINES = 5000;

const DELETED_STATUS = "deleted";

// A compiled collapse glob. RegExp satisfies this shape too.
export interface GlobMatcher {
  test(path: string): boolean;
}

type GlobToken =
  | { kind: "lit"; ch: string }
  | { kind: "one" } // ?    -> one non-slash character
  | { kind: "star" } // *   -> zero or more non-slash characters
  | { kind: "any" } // **   -> zero or more characters of any kind
  | { kind: "segs" }; // **/ -> zero or more whole segments, i.e. (.*/)?

function tokenizeGlob(glob: string): GlobToken[] {
  const tokens: GlobToken[] = [];
  let i = 0;
  while (i < glob.length) {
    const ch = glob[i]!;
    if (ch === "*") {
      if (glob[i + 1] === "*") {
        // "**" consumes any number of path segments, including zero.
        // When followed by "/" (leading, e.g. "**/*.pb.go", or interior,
        // e.g. "a/**/b"), swallow that "/" into an optional "zero or more
        // segments" token so the single separator already emitted for the
        // preceding literal (if any) is preserved exactly once: "a/**/b"
        // matches "a/b" and "a/x/b" but not "ab" or "a/b/c". A trailing
        // "**" with nothing after it (e.g. "proto/gen/**" or "x/**")
        // becomes a plain "any characters" token, which requires the
        // preceding literal "/" to be present, so it does not match the
        // bare prefix itself (e.g. "x/**" matches "x/y" but not bare "x").
        if (glob[i + 2] === "/") {
          // Consecutive "**/" segments are equivalent to one.
          if (tokens[tokens.length - 1]?.kind !== "segs") {
            tokens.push({ kind: "segs" });
          }
          i += 3;
        } else {
          tokens.push({ kind: "any" });
          i += 2;
        }
        continue;
      }
      tokens.push({ kind: "star" });
      i++;
      continue;
    }
    if (ch === "?") {
      tokens.push({ kind: "one" });
      i++;
      continue;
    }
    tokens.push({ kind: "lit", ch });
    i++;
  }
  return tokens;
}

// Compiles a collapse glob into a matcher anchored to the full path.
//
// Syntax:
//   *   matches within a single path segment (not /)
//   **  matches any number of segments, including zero
//   ?   matches exactly one non-slash character
// All other characters are literal.
//
// Matching simulates the glob as an NFA (a set of live positions advanced
// one path character at a time) instead of compiling to a backtracking
// RegExp. Saved globs are user input that runs against every file of every
// diff in the repo, so a pattern like "**/**/**/...zz" must not be able to
// freeze the tab: cost is O(path length x glob length) for any input.
export function compileGlob(glob: string): GlobMatcher {
  const tokens = tokenizeGlob(glob);
  const n = tokens.length;
  // State 2*i: about to match token i. State 2*i+1: inside the ".*/" body
  // of a "segs" token i. State 2*n: accept.
  const size = 2 * n + 1;
  const accept = 2 * n;

  const addClosure = (set: Uint8Array, state: number): void => {
    // Follow epsilon edges: star/any/segs may all match the empty string.
    let s = state;
    while (!set[s]) {
      set[s] = 1;
      if (s === accept || s % 2 === 1) return;
      const tok = tokens[s / 2]!;
      if (tok.kind !== "star" && tok.kind !== "any" && tok.kind !== "segs") {
        return;
      }
      s += 2;
    }
  };

  return {
    test(path: string): boolean {
      let cur = new Uint8Array(size);
      let next = new Uint8Array(size);
      addClosure(cur, 0);
      for (let p = 0; p < path.length; p++) {
        const c = path[p]!;
        next.fill(0);
        let live = false;
        for (let s = 0; s < accept; s++) {
          if (!cur[s]) continue;
          const i = s >> 1;
          const after = 2 * (i + 1);
          if (s % 2 === 1) {
            // Inside "segs": any character continues; "/" may also end it.
            next[s] = 1;
            if (c === "/") addClosure(next, after);
            live = true;
            continue;
          }
          const tok = tokens[i]!;
          switch (tok.kind) {
            case "lit":
              if (c === tok.ch) {
                addClosure(next, after);
                live = true;
              }
              break;
            case "one":
              if (c !== "/") {
                addClosure(next, after);
                live = true;
              }
              break;
            case "star":
              if (c !== "/") {
                addClosure(next, s);
                live = true;
              }
              break;
            case "any":
              addClosure(next, s);
              live = true;
              break;
            case "segs":
              next[s + 1] = 1;
              if (c === "/") addClosure(next, after);
              live = true;
              break;
          }
        }
        if (!live) return false;
        [cur, next] = [next, cur];
      }
      return cur[accept] === 1;
    },
  };
}

// Returns true if path, or oldPath (for renames/copies), matches any glob.
export function matchesAnyGlob(
  globs: GlobMatcher[],
  path: string,
  oldPath?: string,
): boolean {
  for (const re of globs) {
    if (re.test(path)) return true;
    if (oldPath !== undefined && re.test(oldPath)) return true;
  }
  return false;
}

export interface CollapseInput {
  path: string;
  old_path?: string;
  status: string;
  additions: number;
  deletions: number;
}

// Resolves whether a file should start collapsed, and why, per the
// "Default collapse rules" in the design spec. First match wins:
//   1. explicit user override, always wins
//   2. path (or old_path) matches a collapse glob -> "pattern"
//   3. whole-file deletion -> "deleted"
//   4. additions + deletions > LARGE_FILE_LINES -> "large"
//   5. otherwise expanded
export function resolveCollapse(
  file: CollapseInput,
  override: boolean | undefined,
  globs: GlobMatcher[],
): { collapsed: boolean; reason: CollapseReason | null } {
  if (override === true) {
    return { collapsed: true, reason: "user" };
  }
  if (override === false) {
    return { collapsed: false, reason: null };
  }
  if (matchesAnyGlob(globs, file.path, file.old_path)) {
    return { collapsed: true, reason: "pattern" };
  }
  if (file.status === DELETED_STATUS) {
    return { collapsed: true, reason: "deleted" };
  }
  if (file.additions + file.deletions > LARGE_FILE_LINES) {
    return { collapsed: true, reason: "large" };
  }
  return { collapsed: false, reason: null };
}
