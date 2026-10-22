# Local Review with Claude Implementation Plan (PR 4 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring Review with Claude, suggestion chat, and server-side publishing to local worktrees, and finish the run lifecycle (auto-cancel on PR close, snapshot cleanup).

**Architecture:**
- **Snapshot:** a local run snapshots the live tree into a real commit without touching the reviewer's index or files (temp `GIT_INDEX_FILE`, `add -A`, `write-tree`, `commit-tree -p HEAD`). The snapshot is pinned by `refs/middleman/review-snapshots/<run_id>`, and Claude reads a detached worktree of it, added from the reviewer's own repo.
- **Anchoring:** suggestions anchor to HEAD (`head_sha`); the snapshot is only what Claude reads (`read_sha`).
- **Diff baseline:** runs now record their diff base (`base_sha`), so anchor validation and the prompt use the same diff for the whole run, on both targets.
- **Local publish:** moves server-side and turns drafts into review threads.

**Tech Stack:** Go, Huma v2, SQLite, testify; Svelte 5, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md` (Delivery PR 4; "Worktree" local bullet; "Lifecycle"). Builds on PRs 1-3 (committed).

## Global Constraints

- Same as PR 2's plan, Global Constraints.
- The snapshot must leave `git status --porcelain` output and the reviewer's index byte-identical.
- Local suggestions anchor to HEAD at run start; line numbers are working-tree lines at snapshot time, the same convention as working-tree review comments.
- Never delete the reviewer's own worktree. Only `<ai-review>/run-<id>` and `<ai-review>/draft-<id>` checkouts are managed.

## Spec deviations (decided while planning)

- Local runs skip the per-commit `git show` cache (local briefs do the same). The prompt's changed-file list and ranges come from the snapshot diff.
- Chats on reviewer-authored local drafts run in the reviewer's worktree, matching Ask Claude on local. They read the live tree, not a snapshot.

## Review Focus

1. The snapshot changes nothing the reviewer can see: index, staged/unstaged/untracked state are all identical before and after. (Task 2 `TestSnapshotWorkingTreeLeavesTreeUntouched`)
2. Local suggestions are not "outdated" the moment they are created: `commit_sha` is HEAD, not the snapshot. (Task 3 `TestAPILocalReviewRun`)
3. Uncommitted lines are commentable on a local run, while lines outside the snapshot diff are rejected. (Task 3)
4. Local publish creates review threads and removes only the drafts it turned into threads; the single-path scope (doc pane) publishes only that path. (Task 3 `TestAPILocalPublishCreatesThreads`)
5. A run on a PR that gets closed is cancelled, and snapshot refs are removed once nothing references them. (Task 3 lifecycle tests)

---

### Task 1: Migration 000029 and DB additions

**Files:** `internal/db/migrations/000029_add_review_run_base_and_local.{up,down}.sql`, `internal/db/queries_ai_review_runs.go` (+ test).

```sql
-- up
ALTER TABLE middleman_ai_review_runs ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE middleman_ai_review_runs ADD COLUMN local_worktree_path TEXT NOT NULL DEFAULT '';
-- down
ALTER TABLE middleman_ai_review_runs DROP COLUMN local_worktree_path;
ALTER TABLE middleman_ai_review_runs DROP COLUMN base_sha;
```

`AIReviewRun` and `NewAIReviewRun` gain `BaseSHA` and `LocalWorktreePath`, which are inserted and scanned. New queries:

```go
// Active runs whose MR is closed or merged.
func (d *DB) ListActiveAIReviewRunsOnClosedPRs(ctx context.Context) ([]AIReviewRun, error)
// Finished local runs (local_worktree_path != '') with no draft rows of
// state suggestion|draft still pointing at them: their snapshot refs
// can be deleted.
func (d *DB) ListLocalRunsWithoutLiveDrafts(ctx context.Context) ([]AIReviewRun, error)
```

Tests:
- round-trip of the new fields;
- the closed-PR query returns only active runs on closed or merged MRs;
- the no-live-drafts query: a local done run with a live suggestion is excluded, and the same run after discarding the suggestion is included; GitHub runs and active runs are never included.

Commit `feat(db): record review run base and local worktree`.

### Task 2: Snapshot and local provisioning in aireview

**Files:** create `internal/aireview/snapshot.go` (+ test); modify `runner.go`, `reviewruns.go`, `draftchat.go` (+ tests).

```go
// SnapshotWorkingTree commits the worktree's current state (tracked,
// staged, unstaged, untracked; ignored files excluded) with HEAD as
// parent, without touching the real index, and pins it at
// refs/middleman/review-snapshots/<runID>.
func SnapshotWorkingTree(ctx context.Context, worktreePath string, runID int64) (string, error)
func DeleteSnapshotRef(ctx context.Context, worktreePath string, runID int64) error
func SnapshotRef(runID int64) string // "refs/middleman/review-snapshots/<id>"
```

Implementation:
1. Create a temp file path for the index.
2. `GIT_INDEX_FILE=<tmp> git read-tree HEAD`.
3. `GIT_INDEX_FILE=<tmp> git add -A`.
4. `tree=$(GIT_INDEX_FILE=<tmp> git write-tree)`.
5. `git commit-tree <tree> -p HEAD -m "middleman review snapshot <id>"`, with `GIT_AUTHOR_*` and `GIT_COMMITTER_*` set to `middleman <middleman@localhost>` so a missing user config cannot break it.
6. `git update-ref <ref> <sha>`.
7. Remove the temp index.

Use `exec.CommandContext` with `cmd.Dir = worktreePath` and `GIT_INDEX_FILE` appended to `os.Environ()`.

Provisioning from the reviewer's repo:
- `ReviewRunInput` and `DraftTurnInput` gain `RepoDir string`: the git dir to add worktrees from, defaulting to the bare clone.
- `DraftTurnInput` also gains `UseExistingDir bool`: run in `WorktreePath` as-is, with no lease and no provisioning or removal.
- `acquireWorktree` and `releaseWorktree` take a `repoDir` argument instead of owner/name. Callers compute `repoDir = in.RepoDir`, falling back to `r.cloneDirFor(owner, name)`.
- `provisionWorktreeAt` gains a repoDir-based variant used by `acquireWorktree`.
- For local runs (`RepoDir != ""`), `StartReviewRun` skips `cachePRCommits`.

Tests:
- `TestSnapshotWorkingTreeLeavesTreeUntouched` (table: staged change, unstaged change, untracked file, deleted tracked file, gitignored file):
  - Before: `git status --porcelain=v1 -z` and `git ls-files -s` output.
  - Snapshot.
  - After: both outputs identical.
  - `git show <sha>:<file>` has the working-tree content for tracked and untracked files.
  - The ignored file is absent.
  - The ref resolves to the sha.
  - `DeleteSnapshotRef` removes it.
- `TestReviewRunFromLocalRepoDir`: a local repo with an uncommitted change. The snapshot plus `StartReviewRun` with `RepoDir` produce a run worktree at `run-<id>` containing the uncommitted content (the fake claude does `cat changed.txt > $ARGS.content`). After the run, the reviewer's worktree list (`git worktree list`) no longer shows the run path.
- `TestDraftChatUseExistingDir`: a turn with `UseExistingDir` runs in the given dir (records `pwd`), and the dir still exists afterwards.

Commit `feat(aireview): snapshot local worktrees for Claude review runs`.

### Task 3: Server: local runs, local chat, local publish, lifecycle

**Files:** modify `huma_routes_ai_review_runs.go`, `huma_routes_draft_chat.go`, `huma_routes_review_drafts.go`, `server.go` (+ tests `local_review_runs_e2e_test.go`).

**Start run** for `owner=local`:
- Resolve the worktree `w`.
- `base := worktrees.ResolveBase(ctx, w.Path, s.lookupBaseRefForWorktree(ctx, *w))`.
- `head := worktrees.ResolveCommitSHA(ctx, w.Path, "HEAD")`.
- Insert the run with `Branch = s.currentWorktreeBranch(ctx, w)`, `HeadSHA = head`, `BaseSHA = base.SHA`, `LocalWorktreePath = w.Path`. Use a temporary `ReadSHA = head` until the snapshot exists, then update it (add `SetAIReviewRunReadSHA`).
- Snapshot with `aireview.SnapshotWorkingTree(ctx, w.Path, run.ID)`. On failure, mark the run failed with the error and return 502.
- Prompt context (`localContextBlock`):
  - repo name, branch, base ref (short SHA);
  - `git log --format='%H %s' base..HEAD`, reusing `runGitLogForBrief(ctx, w.Path, base, head)`;
  - "Uncommitted changes are included";
  - changed files from `worktrees.DiffRange(ctx, w.Path, base.SHA, snapshot)` via `changedFilesContext`.
- `StartReviewRun` with `RepoDir = w.Path`, `HeadSHA = snapshot` (what is checked out), and the MCP config for owner `local`.

GitHub runs now also store `BaseSHA = shas.MergeBaseSHA`.

**Proposals** use `run.BaseSHA` on both targets, which fixes PR 2's deferred "merge base re-read" minor; fall back to the current merge base when `BaseSHA` is empty, for runs created before migration 000029. For local runs, the anchor file comes from `worktrees.DiffRange(ctx, run.LocalWorktreePath, run.BaseSHA, run.ReadSHA)`, picking `Path == in.Path`. Rename handling is inherent because that diff is unfiltered. Suggestions store `CommitSHA = run.HeadSHA`, as now.

**Chat** for `owner=local`:
- Drop the 400.
- Fork or resume turns on a local run use `RepoDir = run.LocalWorktreePath` and `CheckoutSHA = run.ReadSHA`.
- In PR 3's code, change the GitHub fork/resume checkout to `run.ReadSHA` too; it equals `HeadSHA` there, so behavior does not change.
- Fresh turns on local drafts set `UseExistingDir = true`, `WorktreePath = w.Path`.

**Publish** for `owner=local`:
- `publishReviewDrafts` accepts body fields `mode` (`act-immediately` | `persist-only`, default persist-only) and `path` (optional single-path scope).
- It builds `createReviewThreadsInput{Owner, Name, Number, Body{Mode: mode-or-empty, Threads: ...}}` from `state='draft'` rows (top-level only, `InReplyTo == nil`, filtered to `path` when given). It calls `s.createReviewThreads(ctx, &in)` and, on success, deletes exactly those drafts. Meta is deleted only when no path scope is given.
- It returns `published`.
- Mode mapping: persist-only -> `""`.

**Lifecycle:**
- New `s.CancelReviewRunsForClosedPRs()`, called from the same post-sync hook as `AutoCloseAIThreadsForClosedPRs` and at startup. It cancels each run from `ListActiveAIReviewRunsOnClosedPRs` through `s.aiReview.CancelReviewRun`.
- New `s.pruneSnapshotRefs(ctx)` runs `aireview.DeleteSnapshotRef` for each run from `ListLocalRunsWithoutLiveDrafts`, ignoring errors from vanished worktrees. Call it at startup, after publish, and after the discard and delete-drafts routes, and only for local scopes, so that it stays cheap.

**E2E tests** (fixture: `seedReviewWorktreeGit`-style local repo with a committed file plus an uncommitted edit; the fake claude reused from PR 2 tests):
1. `TestAPILocalReviewRun`:
   - Start a run -> 200.
   - The run's `head_sha` is HEAD and `suggestion_count` is 0.
   - Propose on an uncommitted added line -> 200, and the suggestion's `commit_sha` equals HEAD.
   - Propose on a line outside the snapshot diff -> 400.
   - The snapshot ref exists in the local repo.
   - `git status --porcelain` is unchanged.
2. `TestAPILocalDraftChat`:
   - Chat on the local suggestion -> fork with `--resume sess-root --fork-session`, cwd `run-<id>`.
   - Chat on a reviewer-authored local draft -> no `--resume`, cwd = the reviewer's worktree.
3. `TestAPILocalPublishCreatesThreads`:
   - Two local drafts on different paths plus one suggestion; publish with `path` set to one of them and `mode=persist-only`.
   - One review thread is created (`GET review-threads`).
   - That draft is gone; the other draft and the suggestion remain.
   - Publishing the rest creates the second thread.
4. `TestAPIReviewRunsCancelledWhenPRCloses` (GitHub fixture):
   - A running run (sleep fake); mark the MR closed in the DB (`UpdateMergeRequestState` or the equivalent the auto-close tests use).
   - Call `s.CancelReviewRunsForClosedPRs()` -> the run is `cancelled`.
5. `TestAPISnapshotRefsPrunedWhenUnused`:
   - Finish a local run with one suggestion, then discard it -> the ref is deleted.
   - A run whose suggestion is still live keeps its ref.

Commit `feat(server): Review with Claude, chat, and publish on local worktrees`.

### Task 4: Frontend for local

**Files:**
- `DiffToolbar.svelte` (+ test): drop the `owner !== "local"` guard on Review with Claude, but still hide it when `getCurrentPR()` is null (PR 2 deferred minor).
- `PendingCommentCard.svelte` (+ test): Ask Claude on local drafts too; only replies are excluded.
- `stores/reviewDraftsApi.ts`: `publish` accepts `mode?` and `path?`.
- `stores/diff.svelte.ts`: `publishDraft(req: { event; body; commitId?; mode?: "act-immediately" | "persist-only"; path?: string })`. A path-scoped success drops only that path's drafts locally.
- `ReviewPanel.svelte` (+ test): the local branch calls `diffStore.publishDraft({ event: "COMMENT", body: "", mode: engageAgent ? "act-immediately" : "persist-only", ...(scopePath ? { path: scopePath } : {}) })` instead of `reviewThreadsStore.createThreads` + `clearDraft`. On success it calls `void reviewThreadsStore.refresh()` so the new threads appear. The "N suggestions not added" notice now shows for local too.
- Update `ReviewPanel.test.ts` local tests: they assert `publishDraft` is called with the mode and path (and `createThreads` is no longer called). The scopePath test asserts `path: "a.md"`.

Commit `feat(ui): Review with Claude and server-side publish on local worktrees`.

### Task 5: Full verification

Same as PR 2 Task 9. Additionally run the snapshot test with `-count=5`.
