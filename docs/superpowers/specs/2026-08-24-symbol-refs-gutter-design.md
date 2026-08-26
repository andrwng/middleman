# Symbol references gutter — design

Date: 2026-08-24

## Problem

Reviewing a diff constantly raises the question "where else does this appear?".
Answering it today means leaving middleman for an editor or a shell `git grep`.

## Goal

Select a symbol in the code diff, get its other occurrences in the pull request
listed in a right-hand gutter, and click one to land on that line.

Scope is the code diff surface only (`ReviewSurface` -> `DiffView` -> `DiffFile`),
which serves both real GitHub PRs and local worktrees through one component tree
(local worktrees are PRs with `owner === "local"`). The rendered-markdown doc
surface is out of scope: it addresses markdown blocks, not code lines.

## Decisions

| Question | Decision |
|---|---|
| Search scope | The PR's changed files are listed. Repo-wide results appear as a count badge only ("+23 elsewhere in the repo"); the full repo-wide list waits for a read-only file viewer. |
| Matching | `git grep -w -F` (word-boundary, fixed-string) plus light per-line classification. |
| Role of classification | Sorts the list, and collapses comment/string hits behind an expandable "N in comments/strings" row. |
| Trigger | A third button on the existing floating selection toolbar. |
| Hits outside a rendered hunk | Auto-expand the collapsed context region containing the line, then scroll and flash. |

Consequence worth stating up front: because repo-wide results are count-only and
off-hunk lines auto-expand, every row in the list is clickable. There are no
dead or disabled rows to build.

## Facts this design rests on

Verified before writing this document:

- `git grep -n -z -w -F -I --no-color -e <sym> <rev>` emits
  `rev:path\0line\0content`; with no rev (working tree) it emits
  `path\0line\0content`. `-e` keeps a leading-dash symbol from being read as a
  flag. Measured on the redpanda tree (6,838 tracked files): about 0.15 s for a
  whole-tree search. That is cheap enough to always grep the whole tree once and
  partition the results in Go, which is what makes the repo-wide count free.
- Every diff line already carries a stable identity:
  `.diff-file[data-file-path=P] .line-wrap[data-anchor-line=N][data-anchor-side=RIGHT]`
  (`DiffFile.svelte:622-628`, `754-760`, `806-811`, `955-961`).
- `anchorFor()` (`DiffFile.svelte:397-407`): add and context lines anchor `RIGHT`
  with the new-side number; only deletes anchor `LEFT`. A grep hit at the head
  tree can only land on a context or add line, so the side is always `RIGHT`.
- `currentCommitSha()` in `DiffFile` is documented as the new-side SHA of the
  current diff scope, matching hunk `new_num` numbering. Using that same value as
  the search SHA makes line numbers line up by construction under every scope
  (commit, patchset, base..working-tree). It may be the working-tree sentinel.
- `CollapsedRegion` holds its expansion state locally (`topCount`/`bottomCount`/
  `topLines`, `CollapsedRegion.svelte:44-70`) but receives its own gap bounds as
  props (`lineCount`, `gapNewStart`, `position`). The region containing a target
  line can therefore self-expand from a prop, with no state lifting.
- Full file text is already served by `GET .../pulls/{n}/blob` and
  `.../blob-range`, so the future repo-wide-list extension needs a viewer
  component rather than new backend work.

## Backend

One endpoint, shaped like the existing `getBlobRange`/`getBlob` pair: PR-scoped
path, `isLocalSource` dispatch, `GetMRIDByRepoAndNumber` existence check, one
implementation per mode.

```
GET /repos/{owner}/{name}/pulls/{number}/symbol-refs?q=<symbol>&sha=<new-side sha>

{ "query": "heartbeat_req",
  "hits": [ { "path": "src/v/kafka/protocol/hb.h", "line": 12,
              "text": "class heartbeat_req final {", "kind": "definition" } ],
  "in_pr_total": 7, "outside_pr_total": 23, "truncated": false }
```

Named `symbol-refs` rather than `references`, because "refs" alone collides with
git refs.

### Components

`internal/gitclone/grep.go` (new) holds the shared core, following the existing
precedent that `worktrees` reuses `gitclone`'s parsers (`ParseRawZ`, `ParsePatch`):

- `type SymbolHit struct { Path string; Line int; Text string; Kind string }`
- `ParseGrepZ(data []byte, revPrefix string) []SymbolHit` — pure, table-tested.
- `Classify(symbol, text string) string` — pure, table-tested. Kinds:
  `definition` (the line carries a `class|struct|enum|union|interface|trait|type|
  func|fn|def|const|var|let|using|typedef|namespace` keyword before the symbol),
  `import` (`#include`, `import`, `use`, `from ... import`, `require(`),
  `comment` (the symbol sits after a `//`, `#` or `--` starter, or inside a
  single-line `/* */`), `string` (an odd number of unescaped quotes precedes the
  symbol), otherwise `reference`. The classifier is line-local and has no
  multi-line comment state; its doc comment says so.
- `(*Manager).GrepSymbol(ctx, host, owner, name, sha, symbol)` — reuses `m.git`.
  A rev is always supplied, so this works against the bare clone.

`internal/worktrees/grep.go` (new) provides
`GrepSymbol(ctx, worktreePath, sha, symbol)`, reusing `gitCmd` plus
`gitclone.ParseGrepZ`/`Classify`. A `sha` equal to `WorkingTreeSentinel` greps
the working tree with no rev (and an empty `revPrefix`); any other value greps
that rev.

`internal/server/huma_routes.go` gains `getSymbolRefs`, and
`internal/server/local_dispatch.go` gains `getSymbolRefsLocal`. Both validate
`q` (non-empty, at most 128 bytes, no newline or NUL) and `sha`, grep once,
partition the hits against the changed-file set, classify only the retained
hits, sort by kind (`definition` -> `reference` -> `import` -> comment/string)
then path then line, and cap the list at `symbolRefsMaxHits = 500` with a
`truncated` flag. The totals are computed before truncation so the counts stay
accurate.

The changed-file set reuses each mode's existing path: `GetDiffSHAs` plus
`s.clones.DiffFiles(...)` for PR mode, and
`worktrees.DiffAgainstBase(ctx, w.Path, baseRef)` for local mode — the same call
`getFilesLocal` already makes. Matching is on the new-side path (`f.Path`), so
renames and copies work, and deleted files cannot match at the head tree anyway.

### Error handling

The grep runs under `context.WithTimeout(ctx, 10*time.Second)`. This is the
first genuinely unbounded git operation driven by user-supplied input; no other
git call in the codebase sets a timeout today, so this one adds it rather than
inheriting the gap. A missing PR is a 404, a bad query a 400, and a git failure
a 502, matching the neighbouring blob endpoints.

`make api-generate` regenerates `frontend/openapi/openapi.json`,
`internal/apiclient/spec/openapi.json`,
`packages/ui/src/api/generated/{schema,client}.ts`, and
`internal/apiclient/generated/client.gen.go`.

## Frontend

`packages/ui/src/stores/symbolRefs.svelte.ts` (new) is a rune store holding
`{query, hits, inPrTotal, outsidePrTotal, truncated, status, error}` with
`search(owner, name, number, sha, query)` and `close()`. It uses the generated
typed `client.GET` (the `reviewThreads` store's pattern) rather than the diff
store's hand-rolled `fetchJSON`, and registers in `Provider.svelte` and
`StoreInstances`. It clears on PR change and on diff-scope change, so stale line
numbers can never be clicked.

`DiffFile.svelte` gains a third button on the existing `.selection-toolbar`
(`:588-620`) beside Comment and Ask, reusing `preserveSelection` (`:196-203`)
and the existing `selectionSnapshot` for the query text. The button appears only
for a single-line selection whose trimmed text has no whitespace and is 1 to 128
characters. It passes `currentCommitSha()` as the SHA.

`packages/ui/src/components/diff/SymbolRefsGutter.svelte` (new) renders the
header (query, count, close), rows grouped by file with a kind badge, the
collapsed "N in comments/strings" expander, the "+N elsewhere in the repo"
badge, and the loading, empty, error and truncated states.

`DiffView.svelte` becomes a flex row of `.diff-area` plus the gutter column,
shown only while a search is active, with a resizable divider. It reuses
`clampGutterWidth`, `MIN_GUTTER_WIDTH` and `MIN_BODY_WIDTH` from
`gutterStack.ts:27-32` and the pointer-capture resize handler shape from
`ReviewSurface.svelte:93-113`, persisting the width in `localStorage`. This is a
column that narrows the diff, not a fixed overlay like `ReviewPanel` — the point
of the feature is to see the code you just jumped to. `resolveStack` is not
needed, because this is a flat list rather than line-anchored margin cards.

`packages/ui/src/components/diff/scrollToDiffLine.ts` (new) extracts the
canonical jump from `QuestionsSection.svelte:84-121`: expand a collapsed file,
`await tick()`, build a `CSS.escape`d selector, `scrollIntoView`, add
`.line-wrap--flash`, and fall back to the file header. The new gutter uses it.
The five existing copies of that logic stay where they are; migrating them is a
separate follow-up.

### Reveal and jump

This is the one novel mechanism.

1. Clicking a row calls `jumpToRef(path, line)`. If the file is collapsed, toggle
   it and `await tick()`.
2. Try the `RIGHT`-side selector. If it resolves, scroll and flash. Done.
3. Otherwise the line sits in an unexpanded gap, so call
   `diffStore.requestRevealLine(path, line)`, mirroring the existing
   `requestScrollToFile`/`getScrollTarget`/`consumeScrollTarget` trio
   (`stores/diff.svelte.ts:350-362`).
4. `DiffFile` passes that target down as a new optional `revealNewLine` prop to
   each `CollapsedRegion` it renders. The region whose
   `[gapNewStart, gapNewStart+lineCount-1]` window contains the line expands from
   the nearer edge through its existing coalesced fetch path, then fires a new
   `onrevealed` callback prop. A `bottom` region, whose size is unknown, expands
   downward until the line appears or it reaches EOF.
5. `onrevealed` retries the selector, scrolls, flashes, and consumes the target.
6. If nothing resolves — no containing region, EOF, or a fetch error — the
   existing file-header fallback applies.

## Testing

- Go table tests for `ParseGrepZ` (both the rev-prefixed and working-tree output
  forms) and `Classify` (a case per kind).
- `GrepSymbol` tested against a real bare clone through the existing
  `internal/testutil/diff_repo.go` `SetupDiffRepo` fixture, which also proves
  `git grep` works in a bare repo, and against a worktree through
  `runGitT`/`setupRepoWithRemote`.
- A Go e2e over the generated apiclient for both modes
  (`internal/server/symbol_refs_e2e_test.go`), using `setupTestServerWithClones`.
- Vitest for the store (with `stubClient`) and the gutter component (grouping,
  kind sort, comment/string expander, repo-count badge, states), plus the
  selection-toolbar button in `DiffFile.test.ts`.
- Reveal-and-jump tests: an in-hunk hit jumps directly, an off-hunk hit expands
  then jumps, an unresolvable hit falls back to the file header.
- A Playwright e2e against the real server
  (`frontend/tests/e2e-full/symbol-refs.spec.ts`): select a symbol, click the
  button, assert the rows, click a row, assert the line is revealed and flashed.

## Out of scope

- The repo-wide list and clickable off-diff hits, which need the read-only file
  viewer. The `blob` endpoint already exists, so that extension is a viewer
  component plus swapping the count badge for a list.
- Highlighting every occurrence inline in the diff (not requested, and it
  interacts with Shiki's token spans).
- An editable query box or a keyboard shortcut for the lookup.
- Language-aware resolution (tree-sitter, clangd).
- Migrating the five existing jump-to-line copies onto the new shared helper.
