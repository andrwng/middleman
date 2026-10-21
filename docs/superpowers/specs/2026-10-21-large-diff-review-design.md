# Large diff review

## Problem

The review pane fetches the whole diff as one JSON payload and mounts every
line of every file into the DOM. A large diff (the motivating case: a local
cloudv2 worktree, 805 files / 95,806 changed lines / 4.4MB patch / 57 commits)
does not load usably. Git is not the bottleneck (full patch with
`--find-copies-harder` is 0.28s); the cost is the per-line JSON (est. 10-12MB),
and above all the DOM (per line: wrapper, gutter spans, token spans, two
buttons with SVGs).

Measured on the motivating diff:

- 116 files / 54,915 lines under `proto/gen/**` (generated).
- 6 more generated files (2.4k lines) elsewhere (`*.pb.go`, `*.gen.ts`, lockfile).
- 1 whole-file deletion of 2,009 lines.
- After a `proto/gen/**` glob and the size rule: 688 files / 38,882 lines still
  expanded. Still far too much to mount at once.

## Goals

- Any diff size loads, scrolls smoothly, and supports commenting.
- Cost scales with what is on screen, not with diff size.
- Low-value files start collapsed by deterministic rules; the reviewer can always
  expand any file and see its full diff.
- Small diffs behave exactly as today.

## Non-goals

- Detecting generated code by content. Rejected as unreliable.
- Line-level virtualization within a file.
- Server-side truncation or partial file views.
- Lazy loading for patchset/interdiff scope (stays full-payload).
- gzip (loopback only).

## Behavior

### Default collapse rules

First match wins:

1. Explicit per-file user choice in this PR/worktree (expand or collapse),
   persisted in localStorage as today. Always wins.
2. Path matches a repo collapse glob: collapsed, badge `pattern`.
3. Whole-file deletion (`status == "deleted"`): collapsed, badge `deleted`.
4. `additions + deletions > 1500`: collapsed, badge `large`.
5. Otherwise expanded.

Rules 2-4 only decide the initial state. They never hide a file, filter it from
the sidebar, truncate it, or affect viewed state, comments, or review
submission. Expanding a collapsed file loads and renders its full diff.

Any jump to a file or line (sidebar click, j/k, comment, thread, draft, symbol
hit) expands the target file if collapsed. That expansion is recorded as an
explicit user choice (rule 1).

### Collapse globs

- Stored per repo in SQLite. Local worktrees resolve to their repo via
  `RepoID`, so a repo's globs apply to its PRs and its worktrees.
- Glob syntax: `*` matches within a path segment, `**` matches any number of
  segments, `?` one character. Matched against the file's new path (and old path
  for renames/deletes). Implemented client-side.
- Edited from the sidebar: a list with add/remove and a match count. Directory
  rows offer "collapse `dir/**` in this repo".

### Directory collapse

Directory rows in the sidebar offer collapse all / expand all for files under
that directory in the current PR. Recorded as explicit per-file choices.

### Sidebar

Collapsed files remain listed, dimmed, with their reason badge. The sidebar is
not virtualized.

## Server API

### `GET .../pulls/{number}/files` (extended)

Accepts the same scope query params as `/diff`: `commit`, `from`/`to`,
`whitespace`. (Patchset params are not accepted; patchset scope uses `/diff`.)
Returns per file: existing fields plus `additions`, `deletions`,
`is_whitespace_only`; and top level: `stale`, `whitespace_only_count`,
`diff_from`, `diff_to` (resolved revisions; empty for the working tree side of
local diffs). With `whitespace=hide`, whitespace-only files are omitted, matching
`/diff`.

Counts come from `git diff --numstat -z` with the same rename/copy flags.
Local worktrees: `/files` no longer runs the full patch; raw + numstat only, per
the same scope dispatch `getDiffLocal` uses.

### `POST .../pulls/{number}/diff/hunks` (new)

Body: scope (`commit`, `from`, `to`, `whitespace`), `expect_from`,
`expect_to` (from the summary), `paths[]`. Server resolves the scope exactly as
`/diff` does. If resolved revisions differ from `expect_*`, responds 409 so the
client reloads. Runs the patch diff limited to the requested paths plus their
old paths (literal pathspecs) and returns `{files: DiffFile[]}` in the
existing shape, including per-file counts.

Caps: at most 100 paths per request (400 above that). The client batches to
about 50 files or 5k lines.

Working-tree scopes cannot be pinned by SHA. The client compares returned
per-file counts against the summary; on mismatch it shows a "worktree changed,
reload" banner and does not mix versions silently.

### `GET`/`PUT .../pulls/{number}/diff-collapse-globs` (new)

`{globs: string[]}`. PR-shaped so both PRs and local worktrees
(`local/{repoName}/{worktreeId}`) resolve to a repo ID server-side. Reads and
writes are per repo, not per PR. PUT replaces the list. Validation: each glob
non-empty after trimming, at most 512 chars, at most 200 globs, duplicates
removed. New table `repo_diff_collapse_globs` via migration.

### Unchanged

`GET .../diff` keeps its contract. The client uses it when the summary totals
5,000 changed lines or fewer, and always for patchset scope.

## Frontend

### Store

- `loadDiff` fetches the summary first. If total lines are at most 5,000, or
  scope is patchsets, it then fetches the full `/diff` (today's path).
  Otherwise the diff is in lazy mode: `diff.files` comes from the summary, and
  hunks are filled in per path as loaded.
- A hunk batcher collects requested paths and flushes once per animation frame
  into `POST /diff/hunks` calls, split at the caps. Results are cached per path
  for the current scope and dropped on scope or whitespace change.
- `getFileHunkState(path)`: `"loaded" | "loading" | "idle" | "error"`.
- Collapse state: `isFileCollapsed` resolves the rules above.
  `getCollapseReason(path)` returns `null | "user" | "pattern" | "deleted" | "large"`.
  Persisted overrides record both explicit collapse and explicit expand.
- Globs are loaded per repo and cached.

### Rendering

Each DiffFile has four states:

- **Collapsed**: header only. No hunk fetch.
- **Placeholder**: expanded, body not mounted. A block of last measured height,
  or an estimate from line counts × row height.
- **Loading**: in the prefetch zone, hunks requested. Placeholder with a spinner.
- **Rendered**: today's body.

The window is computed with a single shared IntersectionObserver per DiffView:

- Prefetch zone (about 4 viewports): request hunks.
- Mount zone (about 2 viewports): mount the body once hunks are loaded.
- Unmount beyond about 6 viewports: measure height, then unmount.
- Pinned files never unmount: open comment composer, active selection inside
  the file, expanded context region, current jump target.

In full (non-lazy) mode, the mount window still applies, so DOM stays bounded
even for diffs under the threshold that have very long lines. Below the
threshold all hunks are already present, so only mount and unmount apply.

`overflow-anchor` handles height changes above the viewport.

### Jumps

`scrollToDiffLine` gains a step: ensure the file is expanded, hunks loaded, and
the body mounted, then reveal the line. `jumpToDraft`, `ReviewCommentsSection`,
`QuestionsSection`, and `DiffView.scrollToFile` route through it.

### Performance fixes

- Active-file tracking via IntersectionObserver on file headers, replacing
  per-scroll `querySelector` plus `getBoundingClientRect` over all files.
- Thread lookups per line: per-file Map keyed by anchor, replacing a linear
  `filter` per line.
- One shared document `selectionchange` listener, replacing one per file.
- Tokenization builds the token Map incrementally without copying the whole
  Map per batch.
- Auto-mark-viewed only for files whose body rendered.

### Tradeoff

Browser find-in-page only matches mounted files. Symbol search and the sidebar
filter cover the whole diff.

## Testing

- Go:
  - `/files` counts equal `/diff` counts for head, commit, range, whitespace
    hide, PR and local.
  - `/diff/hunks` output equals the same files sliced from `/diff`, including
    renames and copies.
  - 409 on moved revisions.
  - Path cap.
  - Glob CRUD and validation, plus worktree to repo resolution.
- Vitest:
  - Collapse rule resolver.
  - Glob matcher.
  - Batcher: coalescing, caps, scope eviction.
  - Mount window and pinning.
- Playwright e2e against a synthetic large repo:
  - The file list appears before any hunks arrive.
  - The `.line-wrap` count stays bounded while scrolling top to bottom.
  - Jump to a comment in an unloaded file lands on the line.
  - A glob persists across reload.
  - Expanding a `pattern` file shows its full diff.
  - A small diff issues exactly one `/diff` call.
