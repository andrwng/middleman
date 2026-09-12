# Review thread drift: never lose a thread, and put it where the code went

## The problem, as reported

> sometimes when the file moves around the comment it still shows up, maybe pointing to the
> same numeric line but the code has moved, so the comment points far away from what it
> originally referred to. or sometimes (maybe when the code goes away completely or the
> original line ref is no longer in range) the review thread can't be viewed at all, even if
> i click on it. [...] bare minimum not losing access to any review threads ever would be a
> great start.

Two distinct failures with one shared cause.

**Misplacement.** `getThreadsAtAnchor` (`packages/ui/src/stores/reviewThreads.svelte.ts:54`)
matches on `(path, line, side)` and ignores `commit_sha` entirely. A thread created at line
200 attaches to whatever line 200 is now, in any scope, however unrelated the code there.

**Unreachability.** `scrollToThread` (`packages/ui/src/components/diff/ReviewThreadsSection.svelte:50`)
looks for the line element, falls back to the file header, and if the file is not in the
current diff at all it returns having done nothing — no navigation, no message. It is also
one of the five hand-rolled copies of the jump logic that predate
`scrollToDiffLine.ts`, so unlike the refs gutter it cannot reveal a line hiding inside a
collapsed context region.

**The shared cause.** Nothing about the anchored *code* is recorded — the table stores
`(path, side, line, start_line, commit_sha)` and a `branch`, with no line text, no context,
and no blob hash. So there is no way to tell whether line 200 is still the right line, let
alone find where it went.

There is a tension to respect: tightening the match to fix misplacement makes threads
*disappear* more often. The reachability floor therefore has to exist first, or fixing one
failure worsens the other.

## Verified facts this design rests on

Each of these was checked against the code or a probe, not assumed.

- **Review threads are local-worktree only.** `huma_routes_review_threads.go:217` rejects a
  non-local owner with `400 "review threads are local-worktree only"`, and the store carries
  the same note. So the mapping needs the `internal/worktrees` backend **only** —
  `internal/gitclone` is out of scope here. (middleman has two parallel diff backends and
  they have drifted before; this one was checked deliberately.)
- **`ReviewThreadCard` needs no diff anchor.** Its props are `{ thread, variant }`
  (`ReviewThreadCard.svelte:6`); it renders the conversation and reply box from the thread
  object alone. The floor needs no new card component.
- **A file at an arbitrary commit is already readable.** `worktrees.Blob(ctx, path, sha, file)`
  exists (`internal/worktrees/blob.go:23`), so the mapping needs no new plumbing to reach
  historical content and **no schema change**.
- **One git call yields both the rename and the line mapping.** Probed on a synthetic repo:
  `git diff -U0 -M <src> <dst>` emits `diff --git a/a.txt b/b.txt` plus
  `rename from`/`rename to` for a renamed file, and `@@` headers for the arithmetic.
- **A deleted anchor is detectable.** In the same probe, deleting the anchored line produced
  `@@ -5 +7,0 @@` — old line 5, new count `0`.
- **`git blame --reverse` is not usable here.** It aborted with
  `fatal: could not open object name list: .git-blame-ignore-revs`, because a global
  `blame.ignoreRevsFile` setting applies to repositories that have no such file. `git diff`
  carries no equivalent user-config dependency.
- **The commits panel only offers commits with parents.** `worktrees.ListCommits` lists
  `baseSHA..HEAD` (`internal/worktrees/changes.go:198`), so `commit_sha^` always resolves for
  a LEFT-side anchor.

## Stage 1: the reachability floor

**The invariant: every non-hidden thread is openable from the threads list, always.** No
anchor state, no missing file, no rebased-away commit can take a conversation away.

Clicking a thread row attempts, in order:

1. Place it in the diff — via `scrollToDiffLine.ts`, so a line inside a collapsed region is
   revealed rather than missed. This replaces the hand-rolled copy in
   `ReviewThreadsSection.svelte` and removes one of the five duplicates.
2. Failing that, expand the thread **in place in the list**, rendering the existing
   `ReviewThreadCard` inline under its row, with the conversation and the reply box.

The row states why placement failed, in the reader's terms rather than git's. Stage 1 can
only distinguish what it can see without a mapping; the rest arrives with Stage 2 and is
listed here so the vocabulary is designed once:

| Condition | Row says | Needs |
|---|---|---|
| Anchored line present | (nothing — it is placed) | Stage 1 |
| File not in the current diff | `file unchanged here` | Stage 1 |
| Line not rendered in the current diff | `line not in this diff` | Stage 1 |
| Anchor commit not in this branch | `commit rebased away` (today's orphan marker) | Stage 1 |
| Anchored line mapped elsewhere | `moved from −65` | Stage 2 |
| Anchored line deleted by later commits | `line removed` | Stage 2 |
| Old content unavailable | `position unknown` | Stage 2 |

The expand-in-place card shows the recorded anchor (`old_api.go −65 at 4f2a1c9`) so the
reader can see what it referred to even when the code is gone.

Stage 1 changes no data and no API. It is worth landing on its own merits even if Stage 2 is
later reverted.

## Stage 2: put the card where the code went

**Rule: never hide a thread, always label it.** Hiding is what makes threads vanish;
silence is what makes them mislead. A thread whose position cannot be confirmed keeps its
inline card at its recorded line and says so, rather than disappearing.

### Resolving an anchor

For a thread anchored at `(commit_sha, path, line, side)`, viewed against a target revision
`at`:

- **Source revision.** A `RIGHT` line numbers the file *at* `commit_sha`, so `src = commit_sha`.
  A `LEFT` line numbers the pre-image, so `src = commit_sha^`.
- **Target revision.** `at` is the new-side revision of the reader's current scope — the same
  value the refs finder uses (`getCurrentCommitSha`). For the working-tree scope, `at` is the
  working tree (an empty destination in the git call).
- **Mapping.** One `git diff -U0 -M <src> <dst> --` in the worktree. Parse the
  `diff --git a/OLD b/NEW` header for the path (following renames) and the `@@` headers for
  the arithmetic.

The arithmetic is a pure function over hunks, and is the piece that gets a table test:

```
// Returns the line's new number, or Removed when the line itself was deleted.
// Whether that counts as "moved" is the caller's comparison, not a state here.
mapLine(hunks, old) -> (new int, removed bool)
  offset := 0
  for each hunk (oldStart, oldCount, newStart, newCount) in order:
      if old < oldStart:                        // before this hunk: only shifted
          return old + offset, false
      if old < oldStart + oldCount:             // inside it
          if newCount == 0:  return 0, true     // the line itself is gone
          return newStart + min(old-oldStart, newCount-1), false
      offset += newCount - oldCount
  return old + offset, false                    // past every hunk
```

`MapLine` reports only the number and whether the line was deleted. The thread's state is
then a comparison the resolver makes: same number and same path → `current`; different
number or path → `moved`; `removed` → `removed`. Keeping the arithmetic free of UI vocabulary
is what makes it a clean table test.

### Resolution states

| State | Meaning | UI |
|---|---|---|
| `current` | mapped line equals the recorded line | placed, unlabelled |
| `moved` | mapped to a different line, or a different path | placed at the mapped line, labelled `moved from −65` |
| `removed` | the anchored line was deleted | list-only, labelled `line removed` |
| `unmappable` | `src` unreadable (rebased away and collected), or the file is absent at `src` | list-only, labelled `position unknown` |

`unmappable` is the state that lands on Stage 1's floor, which is why Stage 1 comes first.

### API

The threads list gains a target revision and returns a resolution per thread:

```
GET /repos/local/{name}/pulls/{number}/review-threads?at=<sha>

{ "threads": [
    { "id": 7, "path": "internal/api/old.go", "side": "RIGHT", "line": 65,
      "commit_sha": "4f2a1c9...", ...,
      "resolved": { "state": "moved", "path": "internal/api/api.go", "line": 81 } }
] }
```

`at` is optional; omitted, threads come back exactly as they do today with `resolved` absent,
so every existing caller — including the MCP tools external agents use — keeps working
unchanged. `resolved.path` differing from `path` is how a rename is reported.

Resolution is computed per distinct `(src, dst, path)` and memoised for the request, so N
threads in one file cost one git call, not N.

## Files

| Path | Change |
|---|---|
| `internal/worktrees/anchor.go` (new) | `ParseHunks`, `MapLine` — pure, table-tested |
| `internal/worktrees/anchor_map.go` (new) | `ResolveAnchor(ctx, worktreePath, src, dst, path, line)` running the git call |
| `internal/server/huma_routes_review_threads.go` | `at` param; per-request resolution cache; `resolved` on the response |
| `internal/server/api_types.go` | `resolved` response type |
| `packages/ui/src/stores/reviewThreads.svelte.ts` | pass `at`; `getThreadsAtAnchor` keys off `resolved` when present; expose resolution state |
| `packages/ui/src/components/diff/ReviewThreadsSection.svelte` | jump via `scrollToDiffLine`; expand-in-place fallback; per-row status |
| `packages/ui/src/components/diff/ReviewThreadCard.svelte` | show the recorded anchor in the expanded-in-place variant |
| `packages/ui/src/components/diff/DiffFile.svelte` | place cards by resolved line |

`make api-generate` after the response type changes.

## Testing

- **Go table tests** for `MapLine`: insertion above, insertion below, deletion of the line,
  deletion around it, replacement, multiple hunks with accumulating offset, line past the
  last hunk, line 1, and an empty hunk list.
- **Go integration test** (`-tags integration`) for `ResolveAnchor` against a real repo built
  exactly like the probe above: insert-above (5 → 8), rename (path follows), delete the line
  (`removed`), and an unreadable `src` (`unmappable`).
- **Server e2e** over the generated client: `at` omitted returns today's shape; `at` supplied
  returns resolutions; a rename is reported via `resolved.path`.
- **Frontend unit tests**: `getThreadsAtAnchor` prefers the resolved line; falls back to the
  recorded line when `resolved` is absent.
- **Playwright e2e**, the invariant made executable: for a thread in each state — placed,
  moved, removed, unmappable — clicking its row opens the conversation and the reply box.
  The `unmappable` and `removed` cases must expand in place. Per this session's lesson, the
  test drives the real click path rather than reconstructing it, and must be run against a
  build without the fix to confirm it fails.

## Out of scope

- **Storing a snippet at creation** as a durable fallback for `unmappable` threads (a
  migration adding the anchored line plus context, then searching for it). Worth adding only
  if rebased-away anchors prove common in practice; `unmappable` is already safe, just
  uninformative.
- **`internal/gitclone`** — review threads are local-only, so PRs need none of this.
- **Re-anchoring the stored row.** Resolution stays a read-time computation; the recorded
  anchor is the reader's original intent and is not rewritten.
- **Draft comments**, which have their own separate drift marker (`isDrifted`).
- **The refs finder's partitioning for spans**, noted earlier and unrelated.
- **Migrating the remaining hand-rolled jump copies** onto `scrollToDiffLine.ts`. This spec
  converts the one it touches.
