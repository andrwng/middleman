# Selective sync: scope the manual Sync button to the selected repo

## Problem

The header's Sync button triggers `POST /sync`, which runs the periodic syncer over every configured repo. One of the tracked repos is large, so this turn takes a long time even when the reviewer only cares about the repo they currently have selected in the header dropdown. Tax: every manual sync is "wait for the slow one." Today there's no way to scope a manual sync.

Two things came up while exploring:
1. The diff toolbar's existing **Refresh** button (which POSTs to `/repos/{owner}/{name}/pulls/{number}/sync`) always fails with `415 Unsupported Media Type`. Root cause: `packages/ui/src/stores/diff.svelte.ts`'s `refresh()` uses a raw `fetch(url, { method: "POST" })` without `Content-Type`. Huma's CSRF guard rejects mutation requests without it. Every other call site uses the typed `apiClient.POST(...)` which sets the header automatically.
2. The visible failure text in the diff toolbar is a hardcoded `"sync failed"` literal with the actual error tucked into the `title` tooltip. Easy to miss.

## Goal

- Make the header Sync button scope its work to the repo currently selected in the global dropdown. When no repo is selected, the button keeps doing what it does today (full sync).
- Background / periodic / watched-PR syncs stay unchanged — they continue to cover every configured repo on the timer.
- Fix the 415 on the diff toolbar's Refresh button.
- Surface the actual refresh error text in the UI instead of the bare "sync failed" literal.

## Non-goals

- Per-repo persistent sync configuration (e.g. excluding a repo from the periodic sync). The dropdown is an ephemeral, per-window scope control for the manual button only.
- A separate "sync everything" affordance beyond "clear the dropdown selection."
- Multi-repo selection (the dropdown is single-select today; this design keeps it that way).
- Promoting a per-PR Sync button to the PR detail header. The existing diff-toolbar Refresh covers the per-PR case adequately once the 415 is fixed; further surfacing is a separate decision.
- Per-repo sync-status indicator UI. The existing top-line "Sync"/"Syncing…" plus the SSE-driven status is sufficient.

## Architecture

### Syncer refactor

Today `Syncer.runOnce(ctx, bypassNextSyncAfter bool)` copies `s.repos` inside the function body and iterates the copy. To support a scoped run, lift the slice to a parameter:

```go
func (s *Syncer) runOnce(
    ctx context.Context,
    bypassNextSyncAfter bool,
    repos []RepoRef,
)
```

Existing callers:

- `TriggerRun(ctx)` — wraps `runOnce` with a snapshot of `s.repos`. No external behavior change.
- `watchLoop` / cadence-driven runs — same: pass `s.repos` snapshot.

New entry point:

```go
// TriggerRunForRepos kicks off an ad-hoc sync limited to the given
// repos. The slice is validated against the tracked set; any entry
// not in s.repos returns an error before the goroutine starts (no
// partial run). Empty slice is a no-op.
//
// Lifecycle matches TriggerRun: lifecycleMu + mergeWithRunCtx +
// s.wg.Add(1); the spawned goroutine runs runOnce(ctx, true, repos)
// and decrements wg/cancel on return. Hard rate-limit pauses and
// Stop semantics carry through unchanged.
func (s *Syncer) TriggerRunForRepos(
    ctx context.Context,
    repos []RepoRef,
) error
```

Validation iterates `s.repos` (under `s.reposMu`) and rejects any input not present, comparing on case-folded owner + name + host. Returning early keeps the caller responsible for surfacing "you can't sync that" rather than silently filtering.

Background sync (`watchLoop`, `watchedSync`, `syncWatchedMRs`) is untouched.

### API surface

New route, registered alongside the existing `triggerSync` in `internal/server/huma_routes.go`:

```
POST /repos/{owner}/{name}/sync
  OperationID:    sync-repo
  DefaultStatus:  202 Accepted
  body:           none
  response:       acceptedOutput (reused)
```

New handler file `internal/server/huma_routes_sync.go` (move both `triggerSync` and the new `syncRepo` here to group sync routes). The new handler:

1. Resolves the repo's host via `Syncer.hostFor(owner, name)` (existing helper) — returns `"github.com"` for unknown owner/name pairs.
2. Validates the resulting `RepoRef` is tracked via `Syncer.IsTrackedRepoOnHost(owner, name, host)`. Returns **403** with the same "is not tracked" message convention used by `syncPR`. (We use 403 over 404 to match the existing untracked-repo response.)
3. Calls `s.syncer.TriggerRunForRepos(context.WithoutCancel(ctx), []RepoRef{ref})` — fire-and-forget, matching `triggerSync`.
4. Returns 202.

Existing routes (`POST /sync`, `POST /repos/{owner}/{name}/pulls/{number}/sync`) are unchanged.

Regenerate OpenAPI + clients via `make api-generate`. The generated Go client gains `SyncRepoWithResponse`; the TS client gains the path under `paths["/repos/{owner}/{name}/sync"]`.

### Frontend wiring

`packages/ui/src/stores/sync.svelte.ts` gets a new action mirroring `triggerSync`:

```ts
async function triggerSyncForRepo(
  owner: string,
  name: string,
): Promise<void>
```

It does the same optimistic state update (`running: true`, `last_error: ""`, polling speed-up) and then `apiClient.POST("/repos/{owner}/{name}/sync", { params: { path: { owner, name } } })`. On error, sets `last_error` from the response and throws, matching `triggerSync`'s contract.

`frontend/src/lib/components/layout/AppHeader.svelte` updates the click handler:

```ts
const stores = getStores();
const { sync } = stores;

async function handleSync(): Promise<void> {
  if (sync.getSyncState()?.running) return;
  const repo = getGlobalRepo();  // { owner, name } | null
  if (repo) {
    await sync.triggerSyncForRepo(repo.owner, repo.name);
  } else {
    await sync.triggerSync();
  }
}
```

Button label reflects the scope so the user can see what's about to happen:

- No repo selected: `Sync` / `Syncing…` (today's labels).
- Repo selected: `Sync <owner>/<name>` / `Syncing <owner>/<name>…`.
- The repo segment is wrapped in a span with `max-width`, `overflow: hidden`, `text-overflow: ellipsis`, `white-space: nowrap` so long names don't blow out the header. Hover gives the full string via `title`.

No change to the sync-status indicator. The existing SSE/poll-driven `getSyncState()` already reflects the most recent run, scoped or not.

### Refresh-button fix (the 415)

In `packages/ui/src/stores/diff.svelte.ts` (`refresh()` function, lines 379-409 today):

- Replace the raw `fetch(syncURL, { method: "POST" })` block (including the `getBasePath()` + manual URL encoding) with the typed call:

  ```ts
  const { error } = await apiClient.POST(
    "/repos/{owner}/{name}/pulls/{number}/sync",
    { params: { path: { owner: currentOwner, name: currentName, number: currentNumber } } },
  );
  if (error) {
    refreshError = apiErrorMessage(error, "sync failed");
    return;
  }
  ```

- The `apiErrorMessage` helper already exists in the store layer (`detail.svelte.ts` uses it). Either import it from a shared location or reproduce its 4-line shape locally.
- Drop the `basePath`/`syncURL` plumbing — it's dead once the typed client owns the URL.

In `packages/ui/src/components/diff/DiffToolbar.svelte` (lines 96-98 today):

- Replace the hardcoded `"sync failed"` text with the actual error string:

  ```svelte
  {#if diff.getRefreshError()}
    <span class="refresh-error" title={diff.getRefreshError()}>
      {diff.getRefreshError()}
    </span>
  {/if}
  ```

- Style the span with `max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;` so a long server message stays readable and the tooltip carries the full text on hover.

The 415 disappears because openapi-fetch sets `Content-Type: application/json` on POST. The UX fix is independent value — even after the 415 fix, any future server error surfaces visibly instead of hiding behind a tooltip.

## Data flow

```
[user] clicks Sync (with "acme/widget" selected in dropdown)
    ↓
AppHeader.handleSync → sync.triggerSyncForRepo("acme","widget")
    ↓
apiClient.POST("/repos/{owner}/{name}/sync", { params })
    ↓
Server: syncRepo handler
    ├─ Syncer.hostFor("acme","widget")          (resolves host)
    ├─ Syncer.IsTrackedRepoOnHost(...)          (403 if not)
    └─ Syncer.TriggerRunForRepos(ctx,[ref])
        ↓
        Syncer.runOnce(ctx, true, [ref])
        ├─ existing parallelism / rate-limit guards
        ├─ existing onSyncCompleted hook
        └─ publishes status to /sync/status SSE
            ↓
        Frontend SSE → sync.setSyncStatus(...) → UI cycles
```

The full-repo path is identical except the slice passed to `runOnce` is the snapshot of `s.repos`.

## Error handling

| Surface | Failure | Response |
|---------|---------|----------|
| `POST /repos/{owner}/{name}/sync` | unknown / untracked repo | **403** with "repo X/Y on Z is not tracked" |
| `POST /repos/{owner}/{name}/sync` | empty repo list passed to syncer | shouldn't happen (handler always sends one); guard returns 500 |
| `TriggerRunForRepos` | repo not in `s.repos` | error returned synchronously; handler maps to 403 |
| `TriggerRunForRepos` | call during shutdown (`s.stopped`) | no-op, like `TriggerRun` |
| Sync store | network error / non-2xx | `last_error` set from `apiErrorMessage(error, ...)`; spinner clears |
| Diff toolbar refresh | (after fix) server error | `refreshError` set; pill renders the actual text |

The 415 root cause is gone after the refresh fix; new failures surface as readable strings.

## Testing

### Go

`internal/github/sync_test.go`:

- `TestTriggerRunForReposIteratesOnlyPassedSlice` — builds a Syncer with two tracked repos + a mock client; calls `TriggerRunForRepos(ctx, []RepoRef{repos[1]})`; awaits the goroutine via `s.wg.Wait()` (or the existing test helper). Asserts the mock recorded a `GetPullsForRepo` (or equivalent) call only for `repos[1]`.
- `TestTriggerRunForReposEmptySliceIsNoOp` — empty input runs no work and returns no error.
- `TestTriggerRunForReposRejectsUntrackedRepo` — passing a repo not in `s.repos` returns an error and does not start a goroutine.

`internal/server/sync_repo_e2e_test.go`:

- `TestSyncRepoTriggers202` — POST to `/repos/acme/widget/sync` via the generated client, assert 202. Confirm via the mock that the run touched only `acme/widget`.
- `TestSyncRepoIs403ForUntrackedRepo` — POST to an unknown owner/name, assert 403 with the "is not tracked" body.

The 415 itself doesn't need a new test (the test client always sets `Content-Type`, so the regression is invisible at the Go layer). The fix is exercised end-to-end in the frontend test below.

### Frontend (vitest)

`packages/ui/src/stores/sync.test.ts` (new):

- `triggerSyncForRepo` POSTs to `/repos/{owner}/{name}/sync` with correct path params.
- On API error, `last_error` is set with the server's `detail` (or fallback).

`packages/ui/src/stores/diff.test.ts` (new or existing):

- `refresh()` calls `apiClient.POST("/repos/{owner}/{name}/pulls/{number}/sync", ...)` rather than `fetch(...)`. Mock the client; assert the call shape.
- On client error, `refreshError` is set with the server message.

`frontend/src/lib/components/layout/AppHeader.test.ts` (existing — extend):

- With `getGlobalRepo()` returning `{ owner, name }`, clicking Sync calls `sync.triggerSyncForRepo` with those args.
- With `getGlobalRepo()` returning `null`, clicking Sync calls `sync.triggerSync` (today's behavior).

### Manual smoke

In two terminals:

```
make dev
make frontend-dev
```

1. Select a tracked repo in the dropdown → click Sync → Network tab shows `POST /repos/<owner>/<name>/sync`; status indicator cycles; only that repo's PRs refresh in the sidebar.
2. Clear the dropdown selection → click Sync → Network tab shows `POST /sync`; full-repo behavior unchanged.
3. Open any PR's diff view → click the Refresh button in the diff toolbar → request now returns 200 (previously 415); diff + commits re-fetch.
4. (Negative) Force a server error (e.g. point the client at a wrong port) and confirm the toolbar pill renders the real error text instead of "sync failed".

## Future extension (not implemented)

- Persistent per-repo "skip from periodic sync" setting. Lives in user config; the watchLoop would consult it. Out of scope here because the daily ask is the manual button, not the timer.
- Multi-select in the dropdown to scope to N repos. The new `TriggerRunForRepos` already accepts a slice, so the API and syncer are forward-compatible — only the frontend dropdown component would change.
- Promote a per-PR Sync button to the PR detail header. Possible once the diff-toolbar Refresh is reliable; we'll see if it's needed in practice.
