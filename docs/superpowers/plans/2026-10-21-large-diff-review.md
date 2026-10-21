# Large Diff Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The review pane loads, scrolls, and comments on diffs of any size (target: 805 files / 96k lines) by loading a summary first, fetching hunks on demand, mounting only near-viewport file bodies, and collapsing low-value files by deterministic rules.

**Architecture:** The server's `/files` becomes a scoped summary with counts. A new `POST /diff/hunks` returns hunks for a path subset, and a new per-repo collapse-globs endpoint is added. The frontend diff store gets a lazy mode with a per-frame hunk batcher and a collapse-rule resolver. DiffView gets a shared viewport window that mounts and unmounts DiffFile bodies, and the existing jump helpers learn to wait for a file to load and mount.

**Tech Stack:** Go (huma, modernc sqlite, git CLI), Svelte 5 runes, TypeScript, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-21-large-diff-review-design.md`. Read it before starting any task.

## Global Constraints

- Lazy threshold: total `additions + deletions` > 5000 enables lazy mode. Patchset scope is never lazy.
- `large` collapse threshold: `additions + deletions` > 1500.
- Hunks endpoint: at most 100 paths per request (400 above). The client batches to at most 50 files or 5000 lines per request.
- Glob limits: at most 200 globs, each at most 512 chars, trimmed, non-empty, deduped.
- Collapse rule order: user > pattern > deleted > large > expanded.
- Datetimes are UTC (CLAUDE.md).
- `go test` always gets `-shuffle=on`. Never use `-count=1`, never `-v`. Use testify only; no `t.Fatal`/`t.Error`.
- Bun only, never npm.
- No emojis.
- Migrations follow `context/db-migrations.md`. The next number is `000026`.
- Commit at the end of each task with a conventional commit message ending in `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never amend. Never switch branches. Never bypass hooks.
- After any huma route change, run `make api-generate` and commit the generated artifacts.

## Review Focus

1. Renamed or copied files fetched through `/diff/hunks` must show as rename/copy with the same hunks as the full diff, not as add plus delete. Owned by Task 1/3: test with a rename and with a copy.
2. Paths with spaces, glob metacharacters (`[`, `*`), or a leading `:` must work in hunk requests. Owned by Task 1: use `--literal-pathspecs` and test it.
3. A file the user explicitly expanded must stay expanded after a reload, even when a glob matches it. Owned by Task 5/6: test the override persistence.
4. A jump to a draft or thread in a file that is collapsed, unloaded, and unmounted must land on the line. Owned by Task 8 (vitest) and Task 11 (Playwright).
5. A file with an open comment composer must not lose its draft text when the user scrolls far away. Owned by Task 7: pinning test.

---

### Task 1: gitclone and worktrees primitives (numstat summary, path-filtered patch)

**Files:**
- Modify: `internal/gitclone/diff.go`, `internal/gitclone/parse.go` (numstat `-z` parser)
- Modify: `internal/worktrees/diff.go`
- Test: `internal/gitclone/diff_test.go` (or the existing test file next to diff.go), `internal/worktrees/diff_test.go`

**Interfaces (produces):**
- `func (m *Manager) DiffSummary(ctx, host, owner, name, from, to string, hideWhitespace bool) (*DiffResult, error)`
  - Returns files with path, old_path, status, is_binary, additions, deletions, is_whitespace_only, and `Hunks: []Hunk{}`. Also sets `WhitespaceOnlyCount`.
  - With `hideWhitespace`, whitespace-only files are omitted, exactly as `Diff` does.
- `func (m *Manager) DiffPaths(ctx, host, owner, name, from, to string, hideWhitespace bool, paths []string) (*DiffResult, error)`
  - Same output shape and flags as `Diff`, limited to `paths`.
  - The caller passes both new and old paths.
  - Uses `git --literal-pathspecs diff ... -- <paths>`.
  - Only files whose path or old_path is in `paths` are returned. This drops a stray partner that pathspec might introduce.
- `func ParseNumstatZ(out []byte) map[string]NumstatEntry`, where `NumstatEntry{Additions, Deletions int; Binary bool}`.
  - Keyed by new path.
  - Handles the `-z` rename form: empty path field followed by old\0new.
- In `worktrees`: `type Revspec struct{ Args []string }` plus constructors that map today's four modes to git args:
  - `RevspecSingleCommit(sha)` gives `sha^!`.
  - `RevspecCommitSpan(oldest, newest)`.
  - `RevspecWorkingTreeVsHEAD()` gives `HEAD`.
  - `RevspecAgainstBase(baseSHA)` gives `baseSHA`.
- In `worktrees`: `func SummaryRevspec(ctx, worktreePath string, r Revspec) ([]gitclone.DiffFile, error)` (raw + numstat, no patch) and `func DiffRevspecPaths(ctx, worktreePath string, r Revspec, paths []string) ([]gitclone.DiffFile, error)`.
  - Existing `DiffSingleCommit` and the other mode functions keep their signatures. Refactor them to go through the Revspec path so there's one implementation.

Notes:
- Reuse `diffRevspec`'s flags (`-M -C --find-copies-harder`) for summary and paths so pairing matches the full diff.
- `DiffAgainstBase` currently uses `diff base.SHA` (vs working tree). Keep that.
- `is_whitespace_only` in the PR path comes from the existing `getWhitespaceOnlyFiles`. Reuse it.

- [ ] Write failing tests. Build a temp repo with: a modified file, an added file, a deleted file, a rename with edits, a copy, a binary file, a whitespace-only change, and a path containing a space and `[x]`. Assert, for both the gitclone and worktrees variants:
  - Summary counts equal the counts computed from the full `Diff` hunks (count add and del lines), and statuses and old_paths match.
  - `DiffPaths` for each single file (passing path and old_path) deep-equals that file's entry in the full `Diff`. This covers the rename, the copy and the odd path.
  - `DiffPaths` with a path list that omits the rename's old path still returns only the requested file. Document the result (it may show as added) in a test comment. The client always sends old paths, so this only pins behavior.
  - `hideWhitespace` summary omits the whitespace-only file and sets `WhitespaceOnlyCount`.
- [ ] Run `go test ./internal/gitclone ./internal/worktrees -shuffle=on` and confirm the tests fail.
- [ ] Implement.
- [ ] Run the tests and confirm they pass. Run `make vet`.
- [ ] Commit: `feat(gitclone): summary and path-limited diffs`.

### Task 2: Scoped `/files` summary (PR and local)

**Files:**
- Modify: `internal/server/huma_routes.go` (getDiff scope resolution extracted, getFiles)
- Modify: `internal/server/local_dispatch.go` (getFilesLocal, getDiffLocal use Revspec dispatch)
- Modify: `internal/server/api_types.go` (`filesResponse` gains `whitespace_only_count`, `diff_from`, `diff_to`)
- Test: new `internal/server/diff_summary_e2e_test.go`. Follow existing server e2e test patterns: grep for `SetupDiffRepo` usages in `internal/server/*_test.go` and use the generated client in `internal/apiclient`.

**Interfaces:**
- Consumes the Task 1 functions.
- Produces:
  - `func (s *Server) resolveDiffRange(ctx, input *getDiffInput, host string, shas *db.DiffSHAs) (from, to string, err error)`. This is the switch currently inline in `getDiff` (lines ~3038-3079); `getDiff` now calls it.
  - `func (s *Server) localRevspec(ctx, w db.Worktree, commit, from, to string) (worktrees.Revspec, baseSHA string, err error)`. This is the switch currently in `getDiffLocal`.
  - `getFilesInput` gains `Whitespace`, `Commit`, `From`, `To` query params with the same names and docs as `getDiffInput`.
  - Response fields `diff_from`/`diff_to`:
    - PR: the resolved SHAs.
    - Local against-base: base SHA and `""`.
    - Local commit: `sha^` resolved and the sha.
    - Local span: `oldest^` resolved and newest.
    - Local working tree: HEAD SHA and `""`.

- [ ] Write failing e2e tests (real SQLite and a real git repo via `testutil.SetupDiffRepo`; for local, follow existing local worktree tests in `internal/server`). Cover:
  - head scope;
  - commit scope;
  - from/to scope;
  - whitespace=hide;
  - local worktree against base, including an uncommitted edit;
  - local commit scope.

  For each, assert the `/files` file set, statuses and additions/deletions equal those derived from `/diff` with the same params, and `diff_from`/`diff_to` are as specified. Also assert invalid scope combos return 400, exactly like `/diff`.
- [ ] Run and confirm the tests fail.
- [ ] Implement. Local `/files` must no longer call the full-patch `DiffAgainstBase`.
- [ ] Run `go test ./internal/server -shuffle=on`. Fix any existing tests broken by the new fields, without weakening them.
- [ ] Run `make api-generate`. Commit: `feat(server): scoped file summary with line counts`.

### Task 3: `POST .../diff/hunks`

**Files:**
- Modify: `internal/server/huma_routes.go` (register `huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/diff/hunks", s.getDiffHunks)`)
- Modify: `internal/server/local_dispatch.go` (`getDiffHunksLocal`)
- Modify: `internal/server/api_types.go`
- Test: `internal/server/diff_hunks_e2e_test.go`

**Interfaces:**
- Request body:
  ```go
  type diffHunksRequest struct {
      Commit     string   `json:"commit,omitempty"`
      From       string   `json:"from,omitempty"`
      To         string   `json:"to,omitempty"`
      Whitespace string   `json:"whitespace,omitempty"`
      ExpectFrom string   `json:"expect_from"`
      ExpectTo   string   `json:"expect_to"`
      Paths      []string `json:"paths" minItems:"1" maxItems:"100"`
  }
  ```
- Response body: `{ "files": DiffFile[] }`, the same `gitclone.DiffFile` JSON as `/diff`.
- Errors:
  - 409 when the resolved from/to differ from `expect_from`/`expect_to`, with message `diff revisions changed`.
  - 400 for invalid scope or more than 100 paths.
  - 404 like `/diff`.
- The server must not expand paths itself. The client sends new and old paths, and the server passes them through to `DiffPaths`/`DiffRevspecPaths`.

- [ ] Write failing e2e tests:
  - For each file in the fixture PR, a hunks request with `[path, old_path]` deep-equals the `/diff` entry.
  - Requesting all paths at once equals `/diff` files, ignoring order.
  - A stale `expect_to` gives 409.
  - 101 paths gives 400.
  - A local worktree with an uncommitted edit returns that edit.
  - A local commit scope works.
  - Paths with spaces and brackets work. Extend the fixture repo, or create a local temp repo for this case.
- [ ] Run and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass.
- [ ] Run `make api-generate`. Commit: `feat(server): fetch diff hunks for a subset of paths`.

### Task 4: Per-repo collapse globs

**Files:**
- Create: `internal/db/migrations/000026_add_repo_diff_collapse_globs.up.sql` / `.down.sql`
- Modify: `internal/db/queries.go` (or a new focused file `internal/db/diff_collapse_globs.go` if queries.go conventions allow), `internal/db/types.go` if needed
- Modify: `internal/server/huma_routes.go`, `internal/server/api_types.go`
- Test: `internal/db/diff_collapse_globs_test.go`, `internal/server/diff_collapse_globs_e2e_test.go`

**Schema:**
```sql
CREATE TABLE repo_diff_collapse_globs (
    repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    glob       TEXT    NOT NULL,
    PRIMARY KEY (repo_id, position)
);
```
Check the actual repos table name and the id column in earlier migrations before writing this.

**Interfaces (produces):**
- `func (d *DB) GetDiffCollapseGlobs(ctx, repoID int64) ([]string, error)`. Returns `[]string{}` when there are none.
- `func (d *DB) SetDiffCollapseGlobs(ctx, repoID int64, globs []string) error`. Runs in a transaction, deletes and inserts, and preserves order.
- `GET` and `PUT /repos/{owner}/{name}/pulls/{number}/diff-collapse-globs`, with body/response `{ "globs": string[] }`.
  - The repo is resolved from owner/name for PRs. For local owners, use `resolveLocalWorktree` and then `w.RepoID`.
  - GET on a repo with no globs returns `{globs: []}`.
  - PUT validation: trim each entry; reject an empty entry, one over 512 chars, or more than 200 entries with 400; dedupe preserving first occurrence.

- [ ] Write failing tests:
  - DB: round trip, order preserved, replace semantics, cascade on repo delete.
  - E2e: PR GET/PUT round trip.
  - E2e: a worktree and a PR of the same repo see the same globs.
  - E2e: validation errors.
  - E2e: dedupe.
- [ ] Run and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass.
- [ ] Run `make api-generate`. Commit: `feat(server): per-repo diff collapse globs`.

### Task 5: Collapse rules and glob matcher (pure frontend modules)

**Files:**
- Create: `packages/ui/src/utils/diffCollapse.ts`
- Test: `packages/ui/src/utils/diffCollapse.test.ts`

**Interfaces (produces):**
```ts
export type CollapseReason = "user" | "pattern" | "deleted" | "large";
export const LARGE_FILE_LINES = 1500;
export const LAZY_DIFF_LINES = 5000;
export function compileGlob(glob: string): RegExp; // * within segment, ** any segments (incl. zero), ? one non-slash char; anchored; other regex chars escaped
export function matchesAnyGlob(globs: RegExp[], path: string, oldPath?: string): boolean;
export interface CollapseInput {
  path: string; old_path?: string; status: string; additions: number; deletions: number;
}
// override: true = user collapsed, false = user expanded, undefined = no choice
export function resolveCollapse(
  file: CollapseInput, override: boolean | undefined, globs: RegExp[],
): { collapsed: boolean; reason: CollapseReason | null };
```
`reason` is `"user"` only when `override === true`. It is `null` when the file is expanded, whether by default or by user choice.

- [ ] Write failing tests. Cover glob cases:
  - `proto/gen/**` matches `proto/gen/a/b.go` and `proto/gen/x.go`, but not `proto/generated/x.go`.
  - `**/*.pb.go` matches `a.pb.go` and `x/y/a.pb.go`.
  - `*.lock` matches `yarn.lock` but not `a/yarn.lock`.
  - `?` behaves as specified.
  - Regex metacharacters are taken literally (`a+b.go`).
  - Renames match on old_path.

  Cover each rule and the precedence: an override of false beats a matching glob; an override of true on a small file gives reason `user`; a deleted file over 1500 lines gives `deleted`; a glob plus large gives `pattern`.
- [ ] Run `cd packages/ui && bun run vitest run src/utils/diffCollapse.test.ts` (check `packages/ui/package.json` for the right test script) and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass.
- [ ] Commit: `feat(ui): diff collapse rules and glob matcher`.

### Task 6: Diff store lazy mode, hunk batcher, collapse state, globs

**Files:**
- Create: `packages/ui/src/stores/hunkLoader.ts` (framework-free batcher class)
- Modify: `packages/ui/src/stores/diff.svelte.ts`
- Modify: `packages/ui/src/api/types.ts` (only if needed; prefer the generated types)
- Test: `packages/ui/src/stores/hunkLoader.test.ts`, `packages/ui/src/stores/diff.lazy.test.ts` (follow `diff.test.ts` / `diff.refresh.test.ts` fetch-mocking patterns)

**Interfaces:**
- Consumes: Task 2 `/files` (scoped, with counts, `diff_from`/`diff_to`), Task 3 hunks endpoint, Task 4 globs endpoint, Task 5 module.
- `hunkLoader.ts`:
  ```ts
  export interface HunkRequestFile { path: string; old_path?: string; lines: number }
  export class HunkLoader {
    constructor(opts: {
      fetchBatch: (files: HunkRequestFile[], signal: AbortSignal) => Promise<DiffFile[]>;
      schedule?: (cb: () => void) => void; // default requestAnimationFrame; tests inject
      maxFiles?: number; // 50
      maxLines?: number; // 5000
      onLoaded: (files: DiffFile[]) => void;
      onError: (paths: string[], err: unknown) => void;
    });
    request(file: HunkRequestFile): void;   // idempotent while queued/inflight/loaded
    state(path: string): "idle" | "loading" | "loaded" | "error";
    reset(): void;                          // aborts in-flight, clears all state
  }
  ```
  - The batch splitter never exceeds `maxFiles`.
  - It exceeds `maxLines` only when a single file alone is larger.
  - Each path sends `[path, old_path]` to the server. The 100-path server cap is safe because 50 files give at most 100 paths.
- Store additions (exported on the store object):
  - `isLazy(): boolean`
  - `getFileHunkState(path): "idle" | "loading" | "loaded" | "error"`. Always `"loaded"` when not lazy.
  - `requestHunks(path): void`. A no-op when not lazy.
  - `getCollapseReason(path): CollapseReason | null`.
  - `isFileCollapsed(owner, name, number, path)`: keeps its signature and now resolves the rules.
  - `toggleFileCollapsed(...)`: keeps its signature and writes an explicit override (true or false).
  - `setFilesCollapsed(paths: string[], collapsed: boolean): void`, for directory actions.
  - `expandForJump(path): void`. Sets the override to false if the file is collapsed.
  - `getCollapseGlobs(): string[]` and `saveCollapseGlobs(globs: string[]): Promise<void>`.
  - `isWorktreeChanged(): boolean`. Set when hunk counts differ from the summary, or on a 409.
- Override storage:
  - Replace the `diff-collapsed-files` format with `diff-collapse-overrides`: `Record<prKey, Record<path, boolean>>`.
  - Migrate old entries on load. Each listed path becomes `true`.
- Load flow (`loadDiff`, `reloadDiffOnly`, scope changes, whitespace toggle):
  1. Fetch the scoped `/files` with the same params as `/diff`, except patchsets. Fetch globs once per repo key and cache them.
  2. If scope is patchsets, or total lines <= `LAZY_DIFF_LINES`, fetch `/diff` as today and set `diff`.
  3. Otherwise set `diff = { stale, whitespace_only_count, files: summary.files }` with empty hunks, mark the store lazy, and create a `HunkLoader` whose `fetchBatch` POSTs to `/diff/hunks` with the scope, `expect_from`/`expect_to` and paths.
  4. `onLoaded`:
     - Replace the matching `diff.files[i]` with the loaded file. Replace by index and keep sort order; do not re-sort.
     - If additions or deletions differ from the summary entry, set `worktreeChanged`.
     - On a 409, set `worktreeChanged`.
  - Abort semantics match today's AbortController handling. A scope change calls `loader.reset()`.
- Keep `getFileList()` working. It returns the summary files in both modes.

- [ ] Write failing tests:
  - `HunkLoader`:
    - Coalesces requests made in one scheduled tick into one fetch.
    - Splits at 50 files and at 5000 lines.
    - Sends a single file over 5000 lines alone.
    - Repeat requests are ignored.
    - `reset` aborts and clears.
    - Errors mark paths `error`, and a later `request` retries.
  - Store:
    - A small diff makes one `/files` call and one `/diff` call, and no hunks call.
    - A large summary makes no `/diff` call. `requestHunks` triggers a POST with the right body, and the loaded file appears in `getDiff().files`.
    - A count mismatch sets `isWorktreeChanged`, and so does a 409.
    - A user expand of a glob-matched file persists across a store re-creation (localStorage).
    - The old `diff-collapsed-files` data migrates.
    - Patchset scope never goes lazy.
- [ ] Run `cd packages/ui && bun run vitest run src/stores` and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass. Run `bun run check` or the svelte-check script in packages/ui and frontend.
- [ ] Commit: `feat(ui): lazy diff loading with batched hunk fetch`.

### Task 7: Viewport window and DiffFile body states

**Files:**
- Create: `packages/ui/src/components/diff/diffWindow.svelte.ts` (window controller)
- Modify: `packages/ui/src/components/diff/DiffView.svelte`, `packages/ui/src/components/diff/DiffFile.svelte`
- Test: `packages/ui/src/components/diff/diffWindow.test.ts`, plus a DiffFile component test if a pattern exists (look for `*.test.ts` using `@testing-library/svelte` under packages/ui)

**Interfaces:**
```ts
export type Zone = "far" | "prefetch" | "mount";
export interface DiffWindow {
  observe(path: string, el: HTMLElement): () => void;  // returns unobserve
  zone(path: string): Zone;                              // reactive ($state-backed)
  pin(path: string, reason: string): () => void;         // returns unpin; pinned => never "far" for unmount purposes
  isPinned(path: string): boolean;
  rememberHeight(path: string, px: number): void;
  height(path: string): number | undefined;
  destroy(): void;
}
export function createDiffWindow(root: HTMLElement, opts?: { prefetchViewports?: number; mountViewports?: number; unmountViewports?: number }): DiffWindow;
```
- Implementation:
  - Use two IntersectionObservers on `root`. The mount observer has a rootMargin of about 2 viewports. The prefetch observer's rootMargin is about 4 viewports and also drives unmount hysteresis: a body unmounts only after leaving roughly 6 viewports. Implement the hysteresis by keeping "mount" until the prefetch observer reports non-intersecting, and size that margin accordingly. Document the exact numbers in code.
  - Compute margins in px from `root.clientHeight`, and recreate the observers when it changes (ResizeObserver).
- DiffView creates one window, passes it to each DiffFile (prop or context), and destroys it on unmount.
- DiffFile:
  - If collapsed: header only, as today.
  - If the zone is prefetch or mount and the store is lazy, call `diffStore.requestHunks(path)`.
  - Mount the body only when `zone === "mount"` (or pinned) and `getFileHunkState(path) === "loaded"`. Otherwise render a placeholder div with `style:height` set to the remembered height or an estimate: `(additions + deletions + 3 * estimatedHunks) * rowHeightPx`. Measure `rowHeightPx` once from a rendered line, with 20px as the fallback. Show a small spinner when loading, and an error with a retry button when the state is error.
  - Before unmount, measure the body height with `rememberHeight`, using an `$effect` teardown or a `beforeUpdate`-equivalent.
  - Pin the file while any of these hold:
    - a comment composer is open in it (find the existing composer state in DiffFile);
    - the document selection is inside it;
    - a CollapsedRegion in it has been expanded;
    - it is the current jump target (Task 8 sets this through `window.pin`).
  - Auto-mark-viewed: require that the body is mounted.
  - Keep the existing `inViewport` tokenization gating, but derive it from `zone === "mount"` instead of a per-file IntersectionObserver. Remove the per-file observer.
- CSS: `overflow-anchor: auto` on the diff area (it's the default, but make sure nothing disables it), and `overflow-anchor: none` on placeholder spinners.

- [ ] Write failing tests for `diffWindow` with a mocked IntersectionObserver:
  - Zone transitions.
  - Hysteresis: a body stays mounted until it leaves the prefetch zone.
  - A pinned file never drops to far for unmount purposes.
  - Height memory.
  - `destroy` disconnects.

  If a DiffFile component-test pattern exists, add these tests:
  - The placeholder renders when the zone is far.
  - The body renders when the zone is mount and the hunks are loaded.
  - An open composer pins the file, and its draft text survives a zone change to far.
- [ ] Run and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass. Run svelte-check.
- [ ] Commit: `feat(ui): mount diff file bodies only near the viewport`.

### Task 8: Jumps wait for load and mount

**Files:**
- Modify: `packages/ui/src/components/diff/scrollToDiffLine.ts`, `jumpToDraft.ts`, `ReviewCommentsSection.svelte`, `QuestionsSection.svelte`, `DiffView.svelte` (`scrollToFile`), plus any other `.line-wrap[data-anchor-line=` lookups (grep for them)
- Test: the existing `scrollToDiffLine` and `jumpToDraft` tests (find them with grep), extended

**Interfaces:**
- Extend `DiffJumpDeps`:
  ```ts
  ensureFileReady: (path: string) => Promise<boolean>; // expand (store.expandForJump), requestHunks, scroll file header into view, pin as jump target, resolve true once body mounted (or false on hunk error / timeout 10s)
  ```
- Add `ensureFileReady` to the DiffView-owned helper. It combines the store and the window, and polls each frame or uses a reactive wait.
- Every jump path calls `ensureFileReady(path)` before `findDiffLineEl`, then continues with the existing reveal logic (`requestRevealLine` for collapsed context regions).
- Remove the jump-target pin on the next user scroll or the next jump.

- [ ] Write failing tests:
  - A jump to a line in a file that is collapsed, unloaded and unmounted calls expand, then requestHunks, and lands on the line once the body appears.
  - Hunk error gives the `missing` outcome.
  - A small (non-lazy) diff jump behaves exactly as before.
- [ ] Run and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass.
- [ ] Commit: `fix(ui): jumps load and mount the target file first`.

### Task 9: Per-line and per-scroll cost fixes

**Files:**
- Modify: `packages/ui/src/components/diff/DiffView.svelte` (active-file tracking)
- Modify: `packages/ui/src/components/diff/DiffFile.svelte` (thread maps, selectionchange, token map)
- Modify: `packages/ui/src/stores/ai.svelte.ts` (`getAIThreadsAtAnchor`), `packages/ui/src/stores/reviewThreads.svelte.ts` (`getReviewThreadsAtAnchor`)
- Test: the existing store tests for those functions, extended

**Changes:**
- Active file tracking:
  - Each file header is observed by the Task 7 window, or by a dedicated IntersectionObserver with `rootMargin: "-60px 0px -100% 0px"` (reports headers crossing a line 60px from the top).
  - The active file is the last file, in list order, whose header is above that line.
  - Keep a sorted set of crossed indices; never call `getBoundingClientRect` in the scroll handler.
  - Remove `onDiffScroll`'s loop.
- `getAIThreadsAtAnchor` / `getReviewThreadsAtAnchor`:
  - Keep the signatures.
  - Back them with a `$derived` Map keyed by `path|side|line` that is rebuilt when the threads change. Each lookup is O(1).
- `selectionchange`:
  - One module-level listener, created lazily and reference-counted, that notifies subscribed DiffFiles.
  - Each DiffFile subscribes only while its body is mounted.
  - Subscribers only re-evaluate when the selection is inside their element, or was before.
- Tokenization:
  - Accumulate into one non-reactive Map across batches.
  - Assign `tokens = next` only once per batch, without copying the old Map. Use `SvelteMap` or a version counter so each batch costs O(batch) rather than O(file).

- [ ] Write failing tests:
  - The anchor-lookup Maps return the same results as the old filter for threads on multiple files, both sides and edited anchors.
  - The tokens accumulate without a quadratic copy (unit-test the helper if extracted).
- [ ] Run and confirm the tests fail.
- [ ] Implement.
- [ ] Run and confirm the tests pass. Run svelte-check.
- [ ] Commit: `perf(ui): constant-time diff line overlays and scroll tracking`.

### Task 10: Sidebar collapse UI

**Files:**
- Modify: `packages/ui/src/components/diff/DiffSidebar.svelte`
- Create: `packages/ui/src/components/diff/CollapseGlobsSection.svelte`
- Modify: `packages/ui/src/components/diff/DiffFile.svelte` (reason badge in the header)
- Modify: `packages/ui/src/components/diff/DiffView.svelte` (worktree-changed banner with a Reload button that calls the existing refresh/reload)
- Test: component tests if the pattern exists. Otherwise this is covered by Task 11.

**Behavior:**
- File rows:
  - Collapsed files get the class `file-row--collapsed`, dimmed with `opacity: .6`.
  - They show a badge with the reason text (`pattern`, `deleted`, `large`). There is no badge for `user`.
- Directory rows: two icon buttons, shown on hover or focus, with aria-labels:
  - `Collapse files in <dir>` calls `setFilesCollapsed(paths under dir, true)`.
  - `Expand files in <dir>` calls the same with `false`.
  - A menu or third button, `Always collapse <dir>/** in this repo`, appends to the globs and saves.
- `CollapseGlobsSection`:
  - Lists globs with remove buttons and an add input. Enter adds the glob, and invalid or duplicate input shows an inline error.
  - Each glob shows `N files` matching in the current diff.
  - It's collapsible, and closed by default when there are no globs.
- DiffFile header: shows the same badge. The expand toggle works as today.
- Banner: shown when `isWorktreeChanged()` and reads `The worktree changed since this diff loaded.`, with a `Reload` button.
- Use the design tokens from `frontend/src/app.css` and existing sidebar styling. No new colors.

- [ ] Implement.
- [ ] If component tests exist for DiffSidebar, extend them: directory collapse calls `setFilesCollapsed` with the right paths; the glob add flow calls `saveCollapseGlobs`.
- [ ] Run svelte-check and vitest.
- [ ] Commit: `feat(ui): sidebar controls for collapsing diff files`.

### Task 11: Large-diff fixture and Playwright e2e

**Files:**
- Modify: `internal/testutil/diff_repo.go`, or create `internal/testutil/large_diff_repo.go`. Add a second seeded PR (for example acme/widgets #2, or whatever number is free in fixtures) with a generated diff:
  - 700 files, each with 80 added lines, in `src/pkg<i%20>/file<i>.go`;
  - 40 files under `gen/` with 400 lines each;
  - one 2000-line deleted file;
  - one 1800-line modified file.
  Wire it into `cmd/e2e-server` seeding the same way `SetupDiffRepo` is wired.
- Create: `frontend/tests/e2e-full/large-diff.spec.ts`
- Modify: existing e2e specs that break because `config.yaml` (deleted in the fixture) now starts collapsed. Update their expectations, and do not weaken their intent.

**Tests (Playwright, chromium is enough locally; leave the config's projects as they are):**
1. The sidebar shows all files before any `/diff/hunks` response resolves. Use `page.route` to delay hunk responses.
2. No `/diff` (full) request is made for the large PR. A small PR (#1) makes exactly one `/diff` request and no hunks requests.
3. Scroll the diff area to the bottom in steps. After each step, `page.locator(".line-wrap").count()` stays under 3000.
4. The `gen/**` files, the deleted file and the 1800-line file start collapsed with their badges (`deleted`, `large`). After adding the glob `gen/**` through the sidebar and reloading, the gen files show `pattern`.
5. Expanding a `pattern` file shows all of its lines (scroll within it and check the last line number is present).
6. Create a draft comment on a line in the last file through the API or UI, scroll to the top, click the draft in the sidebar, and check the line is visible and highlighted.
7. Directory collapse: collapse `src/pkg3`, reload, and check its files are still collapsed.

- [ ] Write the fixture and its Go test (counts in `/files` match the expected totals).
- [ ] Write the Playwright spec.
- [ ] Run `make test-e2e`, or run the single spec through the e2e config, and fix failures.
- [ ] Run `make test-short` and `cd frontend && bun run test` (and the packages/ui tests) to check for regressions.
- [ ] Commit: `test(e2e): large diff review`.
