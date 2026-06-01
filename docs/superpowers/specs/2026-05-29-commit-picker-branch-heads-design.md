# Branch-head markers in the commit picker

## Problem

When reviewing a local worktree in the commit picker, there is no
signal that a given commit is also the tip of another local branch.
A maintainer often keeps several local branches (stacked work,
experiments, parked spikes) whose tips coincide with commits already
in the worktree's history. Today that relationship is invisible
unless you drop to a terminal and run `git log --decorate`.

## Goal

In the commit picker, mark any commit that is the **tip (HEAD) of
another local branch** with a small icon. Hovering the icon reveals
the branch name(s). Scoped to the local-worktree review flow.

Companion mockup: `2026-05-29-commit-picker-branch-heads-mockup.html`
(in this directory).

## Non-goals

- **Branch navigation / switching / checkout.** Explicitly deferred;
  this spec only adds a read-only marker. (See Future extension.)
- **Decoration on GitHub PR commits.** The remote review path is
  untouched; the field is simply absent there.
- **Commits merely *contained in* other branches.** Tips only.
- **Tags or remote-tracking branches.** `refs/heads` only.
- **Live polling of branch tips.** Resolved on load/refresh only.

## Architecture

### Branch-tip resolution (new `internal/worktrees/branches.go`)

```go
func BranchHeads(ctx context.Context, worktreePath, excludeBranch string) (map[string][]string, error)
```

- Runs `git -C <worktreePath> for-each-ref --format='%(objectname) %(refname:short)' refs/heads`
  via the package's existing `gitCmd` helper (`changes.go`).
- `refs/heads/*` are shared across all worktrees of a repo, so this
  enumerates every local branch repo-wide — exactly "other local
  branches in the repo."
- Parses line by line: each line is `<full-sha> <branch-name>`. Git
  refnames cannot contain spaces or newlines, so
  `strings.SplitN(line, " ", 2)` is safe and records are
  newline-separated.
- Builds `map[fullSHA] -> []branchName`, names **sorted** for stable
  tooltip order. Any entry equal to `excludeBranch` is dropped.
- Returns an empty (non-nil) map when there are no branches so callers
  index safely.

### Wiring into the local commits path (`getCommitsLocal`, `internal/server/local_dispatch.go:241`)

- After the `commitsResponse.Commits` slice is built, call
  `worktrees.BranchHeads(ctx, w.Path, w.Branch)`.
  - `w.Branch` is the worktree's own current branch (from the worktree
    row; `""` when `w.IsDetached`, so nothing is excluded). This is
    why a commit only lights up for *other* branches.
- For each commit, set `commitResponse.BranchHeads = m[c.SHA]`.
- The synthetic `WorkingTreeSentinel` ("Uncommitted changes") row
  never matches a real SHA, so it stays undecorated.
- The GitHub-PR branch of `getCommits` (`huma_routes.go:2248`) is left
  untouched.

### API surface

- Add to `commitResponse` (`internal/server/api_types.go:151`):

  ```go
  BranchHeads []string `json:"branch_heads,omitempty" doc:"Names of other local branches whose tip is this commit (local worktree review only)"`
  ```

- Regenerate the OpenAPI spec + Go client: `make api-generate`.
- Add to the TS `CommitInfo` (`packages/ui/src/api/types.ts:110`):

  ```ts
  branch_heads?: string[];
  ```

### Frontend rendering (`packages/ui/src/components/diff/CommitListItem.svelte`)

- Render a branch marker when `commit.branch_heads?.length`, placed
  **immediately after the SHA span, before the message** (mockup
  Placement B).
- Marker: an inline-flex span containing an SVG git-branch glyph
  (Octicons `git-branch` path) at ~11px, `flex-shrink: 0`, color
  `--text-muted`, brightening to `--accent-blue` on row hover (mirrors
  the active-SHA treatment). When `branch_heads.length > 1`, append a
  small mono count badge showing the count.
- Tooltip: native `title={commit.branch_heads.join(", ")}` on the
  marker span — consistent with the existing reviewed-check and
  commit-message `title=` usage.
- `CommitListSection.svelte` is unchanged; it already passes the whole
  `commit` through to the item.

## Data flow

1. UI loads commits for the selected local worktree:
   `GET /repos/local/{name}/pulls/{number}/commits` →
   `diff.svelte.ts loadCommits()`.
2. `getCommits` dispatches to `getCommitsLocal` (owner == `local`).
3. `getCommitsLocal` lists commits, resolves branch tips **once** via
   `BranchHeads`, and attaches `branch_heads` per commit.
4. The response carries `commits[].branch_heads`; `CommitListItem`
   renders the marker when present.
5. Refresh (worktree reselect / DiffToolbar refresh) re-runs the same
   path, so tips are re-resolved. No separate channel, no polling.

## Error handling

- **`BranchHeads` failure** (git error, not a repo, etc.) is
  non-fatal: log and return commits **undecorated**. A cosmetic marker
  must never break the commits panel.
- **Detached worktree:** `excludeBranch == ""`, so all local branch
  tips are eligible; the HEAD commit may legitimately show other
  branches that point at it.
- **Sentinel row:** synthetic SHA, never matched, always undecorated.
- **Odd branch names:** refname rules forbid spaces/newlines/control
  characters, so line + first-space parsing is safe; no escaping
  needed.

## Testing

### Go — unit (`internal/worktrees/branches_test.go`)

- Real temp git repo (pattern from `changes_test.go` /
  `scanner_test.go`): commit a chain, create side branches at known
  commits, including two branches at the same commit.
- Assert the `sha -> sorted []name` map; assert `excludeBranch` removes
  the current branch; assert two-branches-on-one-commit lists both
  (sorted); assert the empty-repo / detached (`excludeBranch == ""`)
  case.

### Go — e2e (`internal/server/api_test.go`)

- Stand up a local worktree (existing local-source test setup), create
  a side branch pointing at one of its commits, `GET .../commits` via
  the generated client, and assert:
  - the target commit's `branch_heads` contains the side branch,
  - other commits' `branch_heads` is empty,
  - the worktree's current branch is excluded,
  - the sentinel row (when dirty) has no `branch_heads`.

### Frontend — component (`packages/ui/src/components/diff/CommitListItem.test.ts`)

- Matching `CommitMessageBanner.test.ts`: renders the marker + `title`
  when `branch_heads` is non-empty; renders the count when length > 1;
  renders nothing when absent/empty.

### Manual smoke

- `make dev` + `make frontend-dev`; open a local worktree that has a
  couple of side branches whose tips fall inside the reviewed range;
  confirm the marker appears after the SHA with the right names on
  hover, and updates on refresh after moving a branch in a terminal.

## Future extension (not implemented)

Branch navigation within the worktree flow (switch/browse branches).
The per-commit `branch_heads` data and the `BranchHeads` resolver are a
stepping stone toward it, but are intentionally not generalized now
(YAGNI). Revisit when navigation is specced.
