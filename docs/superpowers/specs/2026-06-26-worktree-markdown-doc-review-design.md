# Worktree Markdown Doc Review — Design

Date: 2026-06-26
Status: Approved (design); spec under review

## Problem

A maintainer keeps living design/analysis notes (markdown) in a local repo
enrolled as a `local_path` worktree. These docs get pushed to origin
whenever, but stay "living" — they remain open for human review and
annotation even after they're committed and on `main`.

Today the only way to view a worktree file fully rendered (and comment on
it) is `RenderedMarkdownView`, reached via the per-file "diff | rendered"
toggle in `DiffFile`. That toggle only appears for files **in the diff**
(changed vs the worktree's base). An unchanged doc — already committed, not
part of the current change set — cannot be opened or annotated at all.

## Goal

Let the user open **any** markdown file in a worktree, fully rendered, and
leave/read comments on it — independent of whether it changed vs base.

## Non-goals

- No new config knob (no "docs directory" setting). Scope is the whole
  worktree tree.
- No chrome-less / focused standalone layout in v1; the doc renders in the
  normal app frame.
- No new comment-anchoring mechanism. We reuse the existing review-thread
  system and its working-tree behavior, drift characteristics included.
- No content-hash / fuzzy re-anchoring to make comments survive edits more
  gracefully than diff comments already do.

## Key insight: most of this already exists

`RenderedMarkdownView` takes `(owner, name, number, path, sha, hunks)`,
fetches the whole file via the blob endpoint, renders it as one natural
HTML blob, tags **every source line** with an anchor span
(`data-anchor-line`), maps text selections back to 1-based source lines
(`computeRangeFromSelection`), and creates review-thread / AI-thread
comments anchored to `(path, line, side, commit_sha)` — the same store as
diff comments, so Apply / agent replies work. Passing `hunks: []` renders a
pristine doc with no change accents.

The **only** missing pieces are:

1. A way to discover and open an arbitrary doc (entry point).
2. A route that renders that doc outside the diff file list.

## Decisions

### Content + anchoring: working tree

The doc viewer renders the **working-tree** state (on-disk now, including
uncommitted edits) — consistent with the rest of worktree review, whose
default diff scope is explicitly `base..working-tree (committed +
uncommitted + untracked)`.

Comments anchor exactly the way the existing worktree review already
anchors when a worktree is dirty: `commit_sha` is the working-tree sentinel
(`getCommitsLocal` prepends an "Uncommitted changes" entry; `DiffFile`'s
`currentCommitSha()` returns it as the newest in-scope commit). We inherit
that behavior rather than introduce a new one.

Accepted tradeoff: working-tree anchoring has no commit boundary to diff
against, so edits above a comment can silently shift what its line points
at, with no "outdated" flag — unlike commit-anchored review, which detects
drift across commits. This matches existing dirty-worktree behavior and is
accepted knowingly.

### Discovery: fuzzy command palette

A palette overlay modeled on `RepoTypeahead`'s interaction (filter-as-you-
type, Arrow/Enter/Esc nav, match highlighting):

- Empty query → full doc list (doubles as a browser).
- Typing → fuzzy filter (substring is acceptable for v1).
- Scoped to the current worktree (it needs a worktree id).
- Summoned by a button in the worktree review pane; optional keybinding.

### Render location: dedicated route

`/pulls/local/{name}/{id}/doc?path=<url-encoded path>` renders the doc in
the main content area with its comment rail; a back affordance returns to
the worktree diff. The route is deep-linkable (cold-load renders standalone,
loading its own blob + threads on mount), consistent with existing
`/pulls/local/{name}/{id}/files` routes served via SPA fallback.

### Open in new tab

Palette rows and the open doc's title render as real `<a href={docRoute}>`:

- Plain click → in-app SPA navigation (`preventDefault` + `navigate`).
- ⌘/Ctrl/middle-click → browser opens the route in a new tab natively.
- An explicit ↗ button on each palette row for discoverability.

Bookmarking and link-sharing fall out of the same deep-linkable route.

## Components

### Backend (one small endpoint)

`GET /worktrees/{id}/markdown-files` → `{ "files": ["docs/a.md", ...] }`

- Resolve the worktree by id (reject removed), mirroring
  `/worktrees/{id}/changed-files` and `/worktrees/{id}/diff`.
- List via `git -C <path> ls-files --cached --others --exclude-standard --
  '*.md' '*.mdx' '*.markdown'` (tracked + untracked, ignored excluded —
  matches the "show WIP" decision). Sorted, deduped.
- Content serving (`getBlobLocal`) and review threads are unchanged.

### Frontend

- **Doc palette component** (`packages/ui`): fetches the markdown-files list
  for the current worktree, fuzzy-filters, opens the doc route. Rows are
  anchors (see new-tab) with a ↗ button.
- **Palette trigger**: a button in the worktree review pane; optional
  keybinding via the existing shortcut machinery.
- **Doc route + view**: parse `/pulls/local/{name}/{id}/doc?path=…` in the
  router; render a thin wrapper that mounts `RenderedMarkdownView` with
  `(owner="local", name, number=id, path, sha=working-tree, hunks=[])`,
  plus a back affordance. View loads the worktree's synthetic MR detail +
  threads on mount so cold-load / new-tab works.

## Data flow

1. User is in a worktree review (`/pulls/local/{name}/{id}/files`).
2. Opens the doc palette → `GET /worktrees/{id}/markdown-files`.
3. Picks a doc → navigates (or new-tab) to
   `/pulls/local/{name}/{id}/doc?path=…`.
4. Doc view mounts `RenderedMarkdownView`; it fetches the blob at the
   working-tree scope and loads existing threads for that path.
5. Selecting rendered text opens the composer; saving creates a review
   thread anchored to `(path, source line(s), RIGHT, working-tree sentinel)`
   on the worktree's synthetic MR — same path as diff comments.

## Testing

- **Go e2e**: `/worktrees/{id}/markdown-files` lists `.md`/`.mdx`/`.markdown`
  (tracked + untracked), excludes ignored files, rejects a removed/missing
  worktree, and resolves via local-worktree dispatch.
- **Frontend**: palette filter + empty-shows-all + fuzzy match; doc route
  renders an unchanged doc (`hunks: []`); commenting on a selected line
  creates a thread anchored to the expected `(path, line, side)`; palette
  rows expose a working `href` for new-tab.

## Open questions / future

- Chrome-less focused layout for dedicated doc tabs (reuse the existing
  `focus` page layout) — deferred.
- Better drift handling for living docs (content-snapshot re-anchoring) —
  deferred; matches existing review behavior for now.
