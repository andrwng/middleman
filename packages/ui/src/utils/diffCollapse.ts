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

// Compiles a collapse glob into a RegExp anchored to the full path.
//
// Syntax:
//   *   matches within a single path segment (not /)
//   **  matches any number of segments, including zero
//   ?   matches exactly one non-slash character
// All other regex metacharacters are escaped and taken literally.
export function compileGlob(glob: string): RegExp {
  let pattern = "";
  let i = 0;
  while (i < glob.length) {
    const ch = glob[i];
    if (ch === "*") {
      if (glob[i + 1] === "*") {
        // "**" consumes any number of path segments, including zero.
        // Also swallow an adjacent "/" on either side so that
        // "proto/gen/**" matches "proto/gen/x.go" (zero extra segments)
        // and "**/*.pb.go" matches "a.pb.go" (zero leading segments).
        let j = i + 2;
        if (glob[j] === "/") {
          j++;
          if (pattern.endsWith("/")) {
            pattern = pattern.slice(0, -1);
          }
          pattern += "(?:.*/)?";
        } else {
          pattern += ".*";
        }
        i = j;
        continue;
      }
      pattern += "[^/]*";
      i++;
      continue;
    }
    if (ch === "?") {
      pattern += "[^/]";
      i++;
      continue;
    }
    // Escape regex metacharacters; everything else is literal.
    pattern += ch!.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    i++;
  }
  return new RegExp(`^${pattern}$`);
}

// Returns true if path, or oldPath (for renames/copies), matches any glob.
export function matchesAnyGlob(
  globs: RegExp[],
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
  globs: RegExp[],
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
