# Uncommitted-Text Highlight in the Doc View — Design

Date: 2026-07-25
Status: Approved (design)
Builds on:

- The worktree doc-review pane (`DocReviewSurface` → `RenderedMarkdownView`, `commentLayout="gutter"`).
- The existing working-tree-vs-HEAD diff machinery (`worktrees.DiffWorkingTreeVsHEAD`, exposed as
  `/diff?commit=WORKING-TREE`).

## Problem

The doc view renders the **current working-tree content** of a markdown file (fetched via
`/blob?sha=WORKING-TREE`, read off disk), but gives no indication which of that content is
uncommitted. A file can have uncommitted edits at any time, and a reader can't tell committed
text from not-yet-committed text.

## Goal

In the doc view, highlight the lines of the current file that differ from HEAD (added/modified)
with a soft background band, so uncommitted text is visible in place. This is **not** a diff view
— no removed lines, no add/remove gutters — just the current file's content with its uncommitted
lines marked.

## Decisions (user-approved)

- **Granularity: line-level** — highlight the actual uncommitted lines (added/modified vs HEAD),
  via the renderer's per-source-line anchor spans.
- **Uncommitted = differs from HEAD** (`git diff HEAD`; staged AND unstaged both count as
  not-committed-yet). Untracked (never-`git add`ed) files are NOT highlighted.
- **Visual: a soft background tint** on the uncommitted lines, distinct from the blue/amber
  comment-gutter markers. Isolated to a single CSS class so switching to underline / left-bar
  later is a one-line change.

## What already exists (reused, not rebuilt)

- **The comparison is already a server capability.** `/diff?commit=WORKING-TREE` (`getDiffLocal`,
  `internal/server/local_dispatch.go:215-248`) → `worktrees.DiffWorkingTreeVsHEAD`
  (`internal/worktrees/diff.go:106-129`) runs `git diff HEAD` and returns per-file
  `gitclone.DiffFile{ Path, Hunks[]{ Lines[]{ Type, NewNum } } }`
  (`internal/gitclone/types.go:11-39`). The uncommitted current-file lines for a path = every
  `Line` with `Type == "add"`, taking its `NewNum`. (Removed lines have no `NewNum` and are
  irrelevant here.)
- **The renderer emits per-source-line anchor spans.** `RenderedMarkdownView` renders inline
  content through `renderedMarkdownAnchors.ts`, which wraps each source line as
  `<span class="rmd-anchor" data-anchor-line="N" data-anchor-side="RIGHT">…</span>` — accurate for
  paragraphs, headings, and code blocks; inside lists/tables the spans share the block's start
  line (a known pre-existing renderer imprecision).
- **The doc view already fetches the current content** via `/blob?sha=WORKING-TREE` and passes
  `hunks={[]}` (so the existing block-level `.rmd-changed` path is off in the doc view — this
  feature does not turn it back on; see Design §2).

## Design

### 1. Uncommitted line set (frontend; reuse `/diff`)

- `DocReviewSurface` fetches the working-tree diff:
  `GET /api/v1/repos/{owner}/{name}/pulls/{number}/diff?commit=WORKING-TREE`, finds the `DiffFile`
  whose `path` matches the open doc, and collects `NewNum` for every `Line` with `Type == "add"`
  into a `Set<number>` — the doc's uncommitted line numbers.
- Path not present in the diff (unchanged, or untracked) → empty set → nothing highlighted
  (untracked files aren't in `git diff HEAD`, matching the "skip untracked" decision).
- Pass the set to `RenderedMarkdownView` as a new optional prop
  `uncommittedLines?: Set<number>` (default empty / undefined).

### 2. Line-level highlight (`RenderedMarkdownView`)

- In an `$effect` that runs after the body is mounted and re-runs when `uncommittedLines` or the
  rendered content changes (mirroring how the existing `.rmd-changed` block marks are applied):
  for each `.rmd-anchor[data-anchor-line]` in the body whose numeric `data-anchor-line` is in
  `uncommittedLines`, add the class `rmd-uncommitted`; clear stale marks on re-run.
- The rendered doc's anchor spans are always `data-anchor-side="RIGHT"`, and the uncommitted line
  numbers are current-file (new-side) numbers, so they match directly with no side handling.
- This is **precise line-level**: it uses the ACTUAL added lines (`NewNum`), not the hunk
  envelope (unlike the existing block-level `.rmd-changed`, which adds every line in a hunk's
  `new_start..new_start+new_count` including unchanged context). The new highlight is independent
  of and additive to the `hunks`→`.rmd-changed` path; the doc view uses only `uncommittedLines`,
  and the diff view's Rendered tab is unchanged.

### 3. Styling (isolated for easy swap)

- One CSS rule in `RenderedMarkdownView`'s `<style>`, e.g.
  `.rmd-uncommitted { background: <soft changed/new tint>; border-radius: 2px; }` — soft and
  visually distinct from the comment-gutter markers. Applied per source-line span, so consecutive
  spans on a soft-wrapped line read as one continuous band.
- The **entire** visual treatment lives in this single rule (marked with a comment noting it is
  the one-spot swap point), so switching to underline (`text-decoration` / `border-bottom`) or a
  left-margin bar later is a one-line change with no logic or test impact.

## Data flow

1. Doc opens → fetch `/blob?sha=WORKING-TREE` (existing) → render the current content.
2. `DocReviewSurface` also fetches `/diff?commit=WORKING-TREE` → the open path's `Type=="add"`
   `NewNum`s → `uncommittedLines` Set → passed to `RenderedMarkdownView`.
3. `RenderedMarkdownView` marks the matching anchor spans with `.rmd-uncommitted` → the soft band.
4. Committed lines carry no class → unstyled.

## Non-goals

- Removed-line indicators / a full diff view — this shows only the current file's content, with
  its uncommitted lines marked.
- Word/char-level highlighting (git diff is line-based; the renderer exposes no sub-line source
  positions).
- Highlighting untracked (never-added) files.
- Fixing the renderer's list/table per-line anchoring (uncommitted lines there highlight coarsely
  — a pre-existing imprecision, accepted).
- Changing the diff view's existing block-level `.rmd-changed` behavior.

## Edge cases / error handling

- Path not in the working-tree diff (unchanged / untracked) → empty set → no highlight.
- Doc edited after load → the highlight reflects the diff at fetch time and refreshes when the doc
  view reloads/refetches (same as the content itself). Live-refresh on every keystroke/file-watch
  is out of scope; a reload picks it up.
- The `/diff` fetch fails → fail soft: no highlight, the doc content still renders (do not block
  rendering on the diff fetch).
- Lists/tables → coarse highlight per the known limitation above.

## Testing

- **vitest (`RenderedMarkdownView`):** given `uncommittedLines = {2, 3}`, the spans with
  `data-anchor-line` 2 and 3 get `.rmd-uncommitted` and other lines do not; empty/absent set →
  no `.rmd-uncommitted` anywhere.
- **vitest (`DocReviewSurface`):** fetches the working-tree diff, extracts the open path's
  `Type=="add"` `NewNum`s into the set, and passes it to `RenderedMarkdownView`; a path absent
  from the diff → empty set (uses the existing store/context test harness for the component).
- **e2e (Playwright, mocked):** doc view of a worktree whose open doc has an uncommitted edit →
  the edited line's rendered span carries `.rmd-uncommitted`; a fully-committed doc → none.
  Extend `tests/e2e/support/mockApi.ts` to mock `/diff?commit=WORKING-TREE` for the doc's path,
  and extend `tests/e2e/worktree-doc-review.spec.ts`.
- **Green bars:** `make frontend-check` 0/0, vitest, the doc-review e2e.

## Open questions / future

- A dedicated per-path endpoint (just the uncommitted line numbers for one path) if fetching the
  whole-worktree diff ever becomes heavy for the doc view.
- Live refresh of the highlight on file-watch/poll (currently refreshes on reload).
- Extending the same precise line-level highlight to the diff view's Rendered tab (replacing its
  coarser block-level hunk-envelope `.rmd-changed` path).
