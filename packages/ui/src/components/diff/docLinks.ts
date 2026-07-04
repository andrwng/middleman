// docLinks.ts
// Resolve markdown cross-document links to worktree-relative paths so the
// rendered doc view can open the target doc in docs mode instead of doing a
// raw browser navigation. Pure (no DOM) for testability.

// Resolve a relative target against the directory of the current doc path.
//   resolveDocPath("docs/design.md", "notes.md")    === "docs/notes.md"  (same dir)
//   resolveDocPath("docs/design.md", "../README.md") === "README.md"
//   resolveDocPath("docs/design.md", "sub/x.md")     === "docs/sub/x.md"
//   resolveDocPath("README.md", "notes.md")          === "notes.md"      (root sibling)
export function resolveDocPath(currentPath: string, target: string): string {
  const dir = currentPath.includes("/")
    ? currentPath.slice(0, currentPath.lastIndexOf("/"))
    : "";
  const parts = (dir ? dir.split("/") : []).concat(target.split("/"));
  const out: string[] = [];
  for (const seg of parts) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      out.pop();
      continue;
    }
    out.push(seg);
  }
  return out.join("/");
}

export interface CrossDocLink {
  // Resolved worktree path of the target document.
  path: string;
  // The link's #fragment without the leading "#" ("" when there is none).
  fragment: string;
}

// If href is a relative link to a markdown document (bare same-directory name,
// ./x.md, ../x.md, sub/x.md, optionally with a #fragment), return the resolved
// worktree path and the fragment. Otherwise (external URL, absolute path,
// anchor-only, or a non-markdown target) return null so the link is untouched.
export function crossDocTarget(currentPath: string, href: string | null): CrossDocLink | null {
  if (!href) return null;
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return null; // has a scheme: http:, mailto:, ...
  if (href.startsWith("#") || href.startsWith("/")) return null; // anchor-only or absolute
  const hashIdx = href.indexOf("#");
  const pathPart = hashIdx >= 0 ? href.slice(0, hashIdx) : href;
  const fragment = hashIdx >= 0 ? href.slice(hashIdx + 1) : "";
  if (!pathPart || !/\.(md|mdx|markdown)$/i.test(pathPart)) return null;
  let decoded = pathPart;
  try {
    decoded = decodeURIComponent(pathPart);
  } catch {
    // Malformed escape — fall back to the raw path.
  }
  return { path: resolveDocPath(currentPath, decoded), fragment };
}
