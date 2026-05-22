# Hide PR review threads from the review window

## Problem

Long-lived PRs accumulate review threads that are functionally done (acked, "wontfix", or just stale chatter), and they clutter both the inline diff view and the activity timeline. The reviewer's eye keeps re-parsing the same resolved discussion. There's no way today to take a thread out of view without leaving middleman for GitHub.

## Goal

Let the reviewer hide a review thread from the PR detail surfaces in middleman, locally. The hide should:

- Apply at thread granularity (parent + all replies), not per-comment.
- Disappear the thread from both the inline diff/rendered-markdown view and the activity timeline.
- Persist across reloads and across sync cycles, but auto-unhide when a new reply arrives on that thread.
- Be reversible via a per-PR "Show hidden (N)" toggle that reveals hidden threads with an Unhide control.
- Not propagate to GitHub. The design leaves a clean seam for a future "propagate to GitHub" extension but does not implement it.

## Non-goals

- No GitHub-side thread resolution. The `resolveReviewThread` GraphQL mutation, and reading back GitHub's `isResolved` state on sync, are out of scope. They are sketched at the end of this document as a future extension.
- No global "hide everywhere" — hide state is scoped to one PR.
- No bulk hide (e.g. "hide all threads I've replied to").

## Data model

New migration `internal/db/migrations/000020_add_hidden_review_threads.{up,down}.sql`:

```sql
CREATE TABLE middleman_hidden_review_threads (
    merge_request_id         INTEGER  NOT NULL,
    root_platform_comment_id INTEGER  NOT NULL,
    hidden_at                DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, root_platform_comment_id),
    FOREIGN KEY (merge_request_id)
        REFERENCES middleman_merge_requests(id) ON DELETE CASCADE
);
```

Notes:

- `root_platform_comment_id` is the **GitHub** comment id of the thread root, not a local autoincrement. GitHub ids are stable across syncs (re-syncs can churn local `MREvent.ID`s).
- `hidden_at` is UTC, consistent with the project-wide convention.
- The composite PK doubles as the only index needed; lookups are always per-PR.
- Re-hiding is `INSERT … ON CONFLICT DO UPDATE SET hidden_at = excluded.hidden_at`.
- No pruning of stale rows in v1 — see "Filter predicate" below for what makes a row "stale." Stale rows are harmless and a future re-hide overwrites the timestamp.

## Filter predicate (auto-unhide on new reply)

A thread is **currently hidden** iff both of these hold:

1. A row exists in `middleman_hidden_review_threads` for `(mr_id, root_platform_comment_id)`.
2. No `review_comment` event in that thread has `created_at > hidden_at`.

Computed server-side inside the existing PR detail handler, after events are loaded:

```
For all review_comment events in this PR:
  root_of[platform_id] = walk in_reply_to chain to root
  max_created_by_root[root] = max(event.created_at)

For each hidden_threads row for this mr:
  if max_created_by_root[row.root] > row.hidden_at:
     row is superseded → skip
  else:
     include row.root in the active hidden set
```

The walk reuses the same logic as the existing `ResolveReviewCommentRootID` (`internal/db/queries.go`), but in memory over the already-loaded events instead of one query per comment.

The active set is returned to the client as `hidden_thread_root_ids: number[]` on `PullDetail`. Events themselves come back unfiltered — the client decides whether to show or hide based on the per-PR "Show hidden" toggle.

Server-side computation is chosen over client-side because the predicate joins user-state (`hidden_at`) with sync-state (event `created_at`), and only the server has both in one place. Pushing it client-side would mean shipping every hidden row plus duplicating the in_reply_to walk in TypeScript.

Stale rows (where a newer reply has arrived since `hidden_at`) stay in the table but read as inactive. A subsequent hide UPSERTs a fresher `hidden_at` past the latest reply and the thread is hidden again.

## API surface

New file `internal/server/huma_routes_hidden_threads.go`. Two endpoints:

```
POST   /api/prs/{owner}/{name}/{number}/hidden-threads
  body:    { "root_comment_id": <int64 GitHub platform id> }
  effect:  UPSERT (mr_id, root_comment_id, hidden_at = now UTC)
  returns: 204 No Content

DELETE /api/prs/{owner}/{name}/{number}/hidden-threads/{root_comment_id}
  effect:  DELETE row
  returns: 204 No Content
```

`PullDetail` (existing GET response) grows one field:

```
hidden_thread_root_ids: number[]   // active hidden set, per the predicate above
```

Errors:

- 404 if the PR doesn't exist.
- 400 if `root_comment_id` doesn't match a `review_comment` event on this PR (cheap sanity check to avoid storing arbitrary numbers).
- No 409 on re-hide; UPSERT is idempotent.

New DB queries file `internal/db/queries_hidden_threads.go`:

- `UpsertHiddenReviewThread(ctx, mrID, rootPlatformID, hiddenAt) error`
- `DeleteHiddenReviewThread(ctx, mrID, rootPlatformID) error`
- `ListActiveHiddenRoots(ctx, mrID, events []MREvent) ([]int64, error)` — runs the predicate against pre-loaded events.

API client regen via `make api-generate` per project convention. The new `hidden_thread_root_ids` field flows into the generated TypeScript `PullDetail` type automatically.

## Frontend store + filter

In `packages/ui/src/stores/detail.svelte.ts`:

- `PullDetail` type gains `hidden_thread_root_ids: number[]` from the regenerated client.
- New store state: `showHiddenThreads = $state(false)`, shared by both surfaces. Lives in memory only; resets to off on PR navigation. "Off" is the safer default — once hidden, stays hidden until the user opts in to see them.
- New helpers:
  - `getHiddenRootSet(): Set<number>` — wraps `detail.hidden_thread_root_ids`.
  - `getReviewCommentRootMap(): Map<number, number>` — `platform_id → root platform_id`, computed once per detail load by walking `in_reply_to` in memory. Mirrors the server walk.
  - `isReviewCommentHidden(event): boolean` — true iff its root is in the hidden set.
- Existing `getReviewCommentsByFilePath()` filters out hidden events when `showHiddenThreads` is false; includes them with an `isHidden: true` flag on each `PublishedReviewComment` when on.
- New actions:
  - `hideReviewThread(rootPlatformID: number): Promise<void>` — optimistic: mutate `hidden_thread_root_ids` immediately, then POST. Revert on failure and surface via the existing flash store.
  - `unhideReviewThread(rootPlatformID: number): Promise<void>` — symmetric: remove locally, then DELETE.

In `packages/ui/src/components/detail/EventTimeline.svelte`:

- Read the hidden set + `showHiddenThreads` flag (via prop or via `getStores()` — match the pattern used by `ReviewCommentCard.svelte`, which calls `getStores()`).
- `threadStarts` is computed over the *filtered* visible events so a hidden thread's synthetic "Comments on `<path>`" header doesn't render orphaned above nothing.

## UI affordances

### Hide / Unhide controls

`ReviewCommentCard.svelte`:

- Add a Hide icon button to the header, **only on the thread root** (`comment.inReplyTo === 0`). Place between the existing reply icon and the GitHub link. Style as a new `.rc__action`-style button; tooltip "Hide thread."
- Replies don't render their own button — they share the root's fate.
- When `isHidden` is true (only visible because Show Hidden is on), the card gets an `rc--hidden` modifier: reduced opacity plus a small "hidden" pill near the existing badge. The button becomes "Unhide."

`EventTimeline.svelte`:

- Each thread-root `review_comment` event gets the same Hide/Unhide icon button next to the existing copy-icon.
- When a thread is hidden and Show Hidden is off, every event in that thread is skipped, including the synthetic "Comments on `<path>`" header.

### Show Hidden (N) toggle

The toggle reads and writes the shared `showHiddenThreads` store flag. It needs to be reachable from any surface that renders threads — that's the diff view, the rendered-markdown view, and the activity timeline. Two render locations:

1. The `EventTimeline` toggle row, next to the existing "Show / Hide mechanics" pill.
2. The review-controls row used by the diff and rendered-markdown sub-views. The exact placement is implementation-driven: prefer a single location at a parent level shared by both sub-views (so one button covers both), falling back to duplicate toggles in `DiffToolbar.svelte` and the rendered-markdown toolbar if no shared parent exists. The implementation plan resolves this after reading the current toolbar layout.

Both toggles render only when `hidden_thread_root_ids.length > 0`. Label format: `Show hidden (N)` when off, `Hide hidden (N)` when on.

## Testing

### Go

`internal/db/queries_hidden_threads_test.go`:

- Upsert inserts a new row.
- Re-upsert overwrites `hidden_at`.
- Delete removes the row; deleting a non-existent row is a no-op.
- `ListActiveHiddenRoots` returns rows where no thread reply is newer than `hidden_at`; filters out rows superseded by a newer reply.
- Multi-level reply chains: a reply-to-a-reply still resolves to the original root, so its `created_at` correctly supersedes a hide on the root.

`internal/server/hidden_threads_e2e_test.go` (via the generated apiclient):

- POST hide on a known root → 204; subsequent GET PullDetail has `hidden_thread_root_ids` containing the root, and `events` still includes the comment.
- DELETE unhide → 204; subsequent GET has empty `hidden_thread_root_ids`.
- Auto-unhide: seed root + a reply with `created_at > hidden_at` → GET returns empty `hidden_thread_root_ids` even though the DB row exists.
- Re-hide after the new reply → root reappears in `hidden_thread_root_ids` with the fresh timestamp.
- POST with a `root_comment_id` that doesn't match any `review_comment` on this PR → 400.
- POST on a non-existent PR → 404.

### Frontend

`packages/ui/src/stores/detail.test.ts` (or co-located):

- `getReviewCommentsByFilePath` excludes hidden events when toggle is off.
- `getReviewCommentsByFilePath` includes hidden events with `isHidden: true` when toggle is on.
- `getReviewCommentRootMap` resolves multi-level replies to the correct root.
- `hideReviewThread` updates state optimistically; revert on API failure (mock).

`packages/ui/src/components/diff/ReviewCommentCard.test.ts`:

- Hide button absent on reply cards.
- Click Hide on a root card calls the store action with the right `rootPlatformID`.
- `rc--hidden` class is applied when `isHidden` prop is true; button label flips to Unhide.

`packages/ui/src/components/detail/EventTimeline.test.ts`:

- Hidden thread's events and synthetic "Comments on `<path>`" header are skipped when toggle is off.
- Toggling Show Hidden on reveals them with the hidden styling.

## Future extension (not implemented)

GitHub-side thread resolution is a clean extension on top of this schema:

- Add a `propagate_to_remote BOOLEAN DEFAULT 0` column and a `remote_resolved_at DATETIME NULL` column to `middleman_hidden_review_threads`.
- A worker dequeues rows where `propagate_to_remote = 1 AND remote_resolved_at IS NULL` and fires the GraphQL `resolveReviewThread` mutation, then stamps `remote_resolved_at`.
- Inbound: sync reads GitHub's per-thread `isResolved` (exposed via the GraphQL `pullRequest.reviewThreads` field, not REST `pulls/{n}/comments`) and reconciles into the local hidden set.

These notes are not part of this work — they exist to confirm that the chosen schema doesn't paint us into a corner.
