# Code browser panel — design

Date: 2026-09-09

## Problem

Reviewing a diff often raises "what does the rest of this file look like?" or
"what's in that other file this PR doesn't touch?". Today that means leaving
middleman for a local checkout or GitHub's own file browser, losing the
review context (commit/patchset scope, open threads) in the process.

## Goal

A collapsible panel, styled consistently with the rest of the review surface,
that lets a reviewer browse the full repo tree as it existed at the commit
being reviewed, open any file read-only, and jump there from a symbol-refs
hit. Scope is the code diff surface (`ReviewSurface` -> `DiffView`), serving
both real GitHub PRs and local worktrees through the existing
`owner === "local"` dispatch — same precedent as symbol-refs
(`docs/superpowers/specs/2026-08-24-symbol-refs-gutter-design.md`).

## Decisions

| Question | Decision |
|---|---|
| File scope | Full repo tree at the commit's SHA, not just PR-changed files. |
| Search | No new search UI. Existing symbol-refs hits (`s` / "Refs" button) can open this panel at a path/line. |
| Open behavior | Full-width-from-right overlay, covering the diff area while open (not a resizable split like the symbol-refs gutter). |
| Trigger | New toolbar button plus a keyboard shortcut (`b`), alongside the existing `j`/`k`/`[`/`]`/`m`/`s` single-letter shortcuts. |
| Initial file on open | The diff view's currently active file, only when no path has been browsed yet for this PR. |
| File tracking across commits | The browsed path is sticky per-PR, independent of the diff's active file. Switching commits (`[`/`]`) keeps showing the same path; if it doesn't exist at the new SHA, the panel shows a "missing at this commit" state instead of falling back to another file. |
| Storage | Backend, keyed by `merge_request_id` — same pattern as PR notes, not localStorage. This is PR-scoped state (a bookmark), not a per-viewer UI preference, and it needs to work identically for local worktrees, which only have a backend-resolvable `mr_id`. |

## Facts this design rests on

Verified before writing this document:

- `getBlob`/`getBlobRange` (`internal/server/huma_routes.go:2404,2473`) are the
  precedent for a new PR-scoped, SHA-scoped git-read endpoint: both take
  `Owner/Name/Number` (path) plus `Path`/`SHA` (query), branch first on
  `isLocalSource(input.Owner)` to a `*Local` variant, otherwise require
  `s.clones != nil`, validate `Path`/`SHA` non-empty, confirm the PR exists via
  `s.db.GetMRIDByRepoAndNumber`, then call into `s.clones` and map
  `gitclone.ErrNotFound` to 404, anything else to 502. The local variants
  (`getBlobLocal`/`getBlobRangeLocal`, `internal/server/local_dispatch.go:324,369`)
  resolve a worktree via `s.resolveLocalWorktree` and additionally validate
  `SHA` against `symbolRefsSHA` or `worktrees.WorkingTreeSentinel` before it
  reaches a shell-adjacent git call — the same injection concern a new
  tree-listing endpoint must repeat.
- `internal/gitclone/blob.go` has only `Blob` (whole file, `cat-file -p`) and
  `BlobRange` (line slice) — no directory/tree-listing method exists anywhere
  in `internal/gitclone` or `internal/worktrees`. A tree endpoint is genuinely
  new, not a variant of something already there.
- PR notes are the existing precedent for PR-scoped, `mr_id`-keyed backend
  state: table `middleman_pr_notes` (`internal/db/migrations/000012_add_pr_notes.up.sql`)
  is `mr_id INTEGER PRIMARY KEY REFERENCES middleman_merge_requests(id) ON
  DELETE CASCADE`, `content TEXT`, `updated_at`. `GetPRNotes`/`UpsertPRNotes`
  (`internal/db/queries_notes.go:23,42`) are a plain `SELECT ... WHERE mr_id`
  and an `INSERT ... ON CONFLICT(mr_id) DO UPDATE`. The handlers
  (`getPRNotes`/`putPRNotes`, `huma_routes.go:2832,2844`) resolve
  `resolveOrEnsureMRID(ctx, owner, name, number)` first
  (`internal/server/local_dispatch.go:74`), which for `owner == "local"`
  resolves the worktree and calls `ensureSyntheticMRForWorktree` to upsert a
  synthetic `middleman_merge_requests` row, and for a real PR just looks up
  the existing one. This is exactly the "no separate worktree code path"
  precedent the browsed-path storage should follow.
- `ReviewPanel.svelte` (`packages/ui/src/components/diff/ReviewPanel.svelte:401-424`)
  is **not** a full-width overlay — its `.panel` is a fixed 420px box pinned
  to the top-right corner with a dimming `.overlay` backdrop behind it. The
  chosen "full-width-from-right, covers the diff" behavior has no existing
  component to copy CSS from; it's a new layout, though `reviewPanelOpen`'s
  role as boolean open-state owned by `DiffView.svelte:30` and toggled via a
  toolbar callback (`DiffView.svelte:341,381-382`) is the state-wiring
  pattern to reuse.
- `DiffToolbar.svelte` takes `onRefsClick?: () => void` / `onReviewClick?: () =>
  void` props (`DiffToolbar.svelte:5-9`) and renders a button calling each
  (`:123,136` for Refs) — adding a `onBrowseClick` prop and button follows the
  same shape.
- `DiffView.svelte`'s `handleKeydown` (`:162-217`) ignores modified keys and
  focus inside inputs/contenteditable, then checks each letter in its own
  `if` block (not exclusive `else if`) so a new `if (e.key === "b")` block
  can be appended without disturbing `j`/`k`/`[`/`]`/`m`/`s`. The `s` handler
  is gated on `currentSha !== ""`; the browse shortcut should use the same
  gate, since a path with no resolvable SHA can't be fetched either.
- `diffStore.getActiveFile()`/`setActiveFile()` (`packages/ui/src/stores/diff.svelte.ts:323,342`)
  is the existing source for "currently active file," used to seed the
  panel's first-open path.

## Backend

Two additions, both following the `getBlob`/`getBlobRange` shape.

### Tree listing

```
GET /repos/{owner}/{name}/pulls/{number}/tree?sha=<sha>&path=<dir path, "" for root>

{ "path": "src/kafka",
  "entries": [
    { "name": "protocol", "path": "src/kafka/protocol", "type": "dir" },
    { "name": "server.cc", "path": "src/kafka/server.cc", "type": "file" }
  ] }
```

Lazy, one directory per request — not a recursive full-tree fetch, so it
stays cheap on large repos and matches how a file tree is actually browsed
(expand one level at a time).

`internal/gitclone/tree.go` (new): `(*Manager) Tree(ctx, host, owner, name,
sha, path string) ([]TreeEntry, error)`, using `git ls-tree <sha>:<path>`— the
`:path` form (mirroring `Blob`'s `cat-file -p <sha>:<path>`) lists that
subtree directly rather than filtering a full recursive listing. Empty
`path` lists the root. Non-existent path/sha maps to `gitclone.ErrNotFound`,
same sentinel `Blob`/`BlobRange` already use.

`internal/worktrees/tree.go` (new): equivalent `Tree(ctx, worktreePath, sha,
path string)`, `sha == WorkingTreeSentinel` listing the working tree
(`ls-files` or `ls-tree HEAD` — whichever the existing `worktrees.Blob`
counterpart already does for the sentinel case; follow that, don't invent a
second convention).

`huma_routes.go` gains `getTree(ctx, input *getTreeInput)`, branching on
`isLocalSource` to a `local_dispatch.go` `getTreeLocal`, both repeating the
existing validation: non-empty `sha`, `symbolRefsSHA`-or-sentinel check on
the local path (same injection defense as `getBlobLocal`), PR-existence
check via `GetMRIDByRepoAndNumber`/`resolveLocalWorktree`, `ErrNotFound` ->
404, other errors -> 502.

### Browsed-file bookmark

Reuses the PR-notes shape exactly:

```sql
CREATE TABLE IF NOT EXISTS middleman_code_browser_state (
    mr_id      INTEGER PRIMARY KEY REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path       TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

`GetCodeBrowserPath(ctx, mrID) (string, error)` / `SetCodeBrowserPath(ctx,
mrID, path string) error` in a new `internal/db/queries_code_browser.go`,
same `SELECT`/`INSERT ... ON CONFLICT` pair as `GetPRNotes`/`UpsertPRNotes`.
An empty result (no row) means "no path browsed yet" — the frontend falls
back to the active diff file in that case, not the backend.

`getCodeBrowserState`/`putCodeBrowserState` in `huma_routes.go`, both calling
`resolveOrEnsureMRID` first (exactly like `getPRNotes`/`putPRNotes`), so
local worktrees get a synthetic `mr_id` for free and need no separate code
path.

`make api-generate` regenerates the OpenAPI spec and generated clients after
both additions.

## Frontend

`packages/ui/src/components/diff/CodeBrowserPanel.svelte` (new):

- Props: `{ owner, name, number, sha, initialPath, onclose }`.
- Layout: fixed overlay covering the diff area from the right edge inward
  (a new CSS treatment — no existing component to copy, per the
  `ReviewPanel` fact above). Left column: lazy-loaded file tree (calls
  `getTree` per expanded directory, root fetched on open). Right column:
  read-only file viewer, fetching via the existing `blob`/`blob-range`
  endpoints, syntax-highlighted the same way `DiffFile`/`DiffLine` already
  highlight diff content.
- On open with no stored path: seeds from `diffStore.getActiveFile()`. With a
  stored path (fetched via the new `getCodeBrowserState` call on panel open):
  opens that file directly, tree scrolled/expanded to reveal it.
- Selecting a file in the tree updates local state, calls
  `putCodeBrowserState`, and does *not* touch `diffStore`'s active file —
  the two stay independent, per the file-tracking decision above.
- SHA is pinned to whatever the panel was opened with; stepping commits
  (`[`/`]`) while the panel is open re-fetches the same path at the new SHA.
  A 404 from `blob`/`tree` renders a "This file doesn't exist at this
  commit" empty state rather than picking another file.

`DiffView.svelte` gains `codeBrowserOpen` boolean state (same pattern as
`reviewPanelOpen`, `:30`), a new `if (e.key === "b" && currentSha !== "")`
block in `handleKeydown` opening it, and a rendered
`{#if codeBrowserOpen}<CodeBrowserPanel .../>{/if}` block.

`DiffToolbar.svelte` gains an `onBrowseClick?: () => void` prop and a button
next to Refs, following the existing `onRefsClick` shape (`:5-9,123-136`).

Symbol-refs integration: `SymbolRefsGutter.svelte` rows gain a secondary
action (or the existing row click, if product wants that instead of the
current reveal-in-diff behavior — out of scope to decide here, flag for
implementation) that opens `CodeBrowserPanel` at that hit's path/line rather
than only scrolling the diff.

## Testing

- Go table tests for the new `gitclone.Tree`/`worktrees.Tree` parsers.
- `Tree` tested against the real bare-clone and worktree fixtures already
  used by `blob_test.go` / symbol-refs tests (`internal/testutil/diff_repo.go`,
  `setupRepoWithRemote`).
- Go e2e over the generated apiclient for `tree` and
  `code-browser-state` GET/PUT, both PR mode and local mode
  (`setupTestServerWithClones`), covering: root listing, subdirectory
  listing, nonexistent path (404), nonexistent sha (404).
- Go tests for `GetCodeBrowserPath`/`SetCodeBrowserPath` (no-row default,
  upsert-overwrites), following the existing PR-notes query tests.
- Vitest for `CodeBrowserPanel`: initial-path seeding from active file vs.
  stored bookmark, tree lazy-expand, missing-at-commit empty state, path
  persisting across a simulated commit step.
- A Playwright e2e (`frontend/tests/e2e-full/code-browser.spec.ts`): open via
  toolbar button and via `b`, browse into a subdirectory, open a file, step
  commits and confirm the same path stays open (or shows the missing state),
  reopen the panel after closing and confirm it resumes at the last path.

## Out of scope

- Any search UI inside the panel (Ctrl+F-style find, or a new full-text repo
  search). Symbol-refs remains the only search entry point; this panel is a
  browse/read surface it can jump into.
- Syntax highlighting for languages the diff viewer doesn't already handle.
- Editing file content from the panel.
- Recursive/eager full-tree fetch or client-side tree caching across PRs.
- Deciding whether a symbol-refs row click should open this panel by default
  instead of (rather than alongside) the current in-diff reveal — left for
  implementation/product judgment, noted above.
