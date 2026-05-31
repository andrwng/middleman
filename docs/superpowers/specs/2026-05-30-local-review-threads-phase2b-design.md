# Local Review Threads — Phase 2b (Frontend) Design

**Date:** 2026-05-30
**Status:** Approved (ready for implementation plan)
**Branch:** `serve-local-repo-comments` (stacked on unmerged `branch-navigation`)
**Depends on:** Phase 1a/1b (persisted threads + render) and Phase 2a (agent backend: create-`mode`, `apply`/`apply-all`, discuss/apply turns, MCP proxy) — all on this branch.
**Master spec:** `docs/superpowers/specs/2026-05-29-local-review-threads-design.md`

## Goal

Surface the Phase 2a agent backend in the review UI so a reviewer never needs curl: choose what the agent does at submit time, apply individual threads (or all) on demand, see threads update live as the agent works, browse threads from the left drawer, and delete persisted threads. One small backend addition (a delete endpoint) supports the last item; everything else is wiring to endpoints that already exist.

## Scope

**In:** the submit-time mode picker, per-thread Apply + Apply-all, a thread picker in the left drawer, delete for persisted threads (incl. its backend `DELETE` endpoint), and live status reflection while a turn runs.

**Out (Phase 3):** `list_reviews`/`get_review` discovery, cwd-default resolution, external-shell MCP registration. (Posting replies from an *external* Claude session via `middleman mcp` already works manually today — the 2a acceptance smoke check demonstrated it — so only the discovery/registration ergonomics remain, and they are out of scope here.)

## Folded-in observations (from user testing 2a, repo `TODO.md`)

- "Thread picker like the drafts list" → Section 4 (thread picker).
- "Delete option for persistent threads" → Section 5 (delete + backend endpoint).
- "Find a thread's id in the UI" → covered by Section 4 (the picker surfaces it) and largely obviated by per-thread Apply buttons (Section 2).
- "External Claude posting replies" → already works (Phase 3 ergonomics out of scope; noted above).

## Current frontend touch points (verified)

- `packages/ui/src/components/diff/ReviewPanel.svelte` — local Submit path (has a `// mode picker land in Phase 2` placeholder); calls `reviewThreadsStore.createThreads(drafts)`. The non-local summary/verdict controls are already hidden for local.
- `packages/ui/src/components/diff/ReviewThreadCard.svelte` — renders status chip + Resolve/Hide + reply box.
- `packages/ui/src/components/diff/PendingCommentsSection.svelte` — the drafts list in the left drawer; collapsible list, click → `scrollToDraft` via an anchor selector. Model for the thread picker.
- `packages/ui/src/components/diff/DiffSidebar.svelte` — the left-drawer container.
- `packages/ui/src/stores/reviewThreads.svelte.ts` — `load`, `createThreads`, `addComment`, `resolve`, `hide`, `unhide`, `clear`, `getThreads`, `getThreadsAtAnchor`, `isLoading`, `getError`.
- `packages/ui/src/stores/worktreeSession.svelte.ts` — polls session turns every ~1.5s while `hasRunningTurn()`; stops when idle. Reused for live thread updates.
- `packages/ui/src/stores/events.svelte.ts` + `Provider.svelte` — SSE (`/api/v1/events`, `data_changed`/`sync_status`); `onDataChanged` does not currently reload threads.
- Backend: `internal/server/huma_routes_review_threads.go`, `internal/db/queries_review_threads.go`.

## Design

### 1. Mode picker (ReviewPanel, local-only)

A segmented control matching the existing verdict-radio styling, rendered only in the local Submit path at the existing placeholder: **Persist-only** (default) | **Discuss-first** | **Act-immediately**, each with a one-line description of what the agent does. Submit passes the selection into `createThreads(drafts, mode)`. Default **persist-only** preserves today's behavior — the agent is an explicit opt-in, no surprise Claude runs.

### 2. Per-thread Apply + Apply-all

- `ReviewThreadCard` header gains an **Apply** button next to Resolve/Hide, shown when `status` is `open` or `discussed`, hidden for `applied`/`resolved` → `reviewThreadsStore.apply(threadID)`.
- An **Apply all** button in the thread-picker section header (Section 4) → `reviewThreadsStore.applyAll()` (the 2a endpoint filters to visible open/discussed). Disabled while a turn is running (busy → 2a returns 409).

### 3. reviewThreads store additions (`reviewThreads.svelte.ts`)

- `createThreads(drafts, mode?)` — thread `mode` into the create body (default `""`/persist-only).
- `apply(threadID)` → POST `.../{thread_id}/apply`; replace state from the returned thread list.
- `applyAll()` → POST `.../apply-all`; replace state.
- `delete(threadID)` → `DELETE .../{thread_id}`; replace state from the returned thread list (the endpoint returns the remaining threads, mirroring `apply`).
- `refresh()` — reload the current `(owner, name, number)` (remembered from the last `load`); used by the live-update poll and the SSE catch-all. Silent (no loading flag flicker).

Thread/comment shapes are unchanged (statuses + agent comments already arrive on the thread objects, so the card renders discuss/apply replies with no new types).

### 4. Thread picker (left drawer)

A new collapsible **"Review threads"** section component (mirroring `PendingCommentsSection`) mounted in `DiffSidebar`, listing persisted threads: anchor (`path:line[/start_line]`), status chip, comment count, and a short preview of the root comment. Click scrolls to the thread on the diff (same anchor-selector pattern as `scrollToDraft`). It coexists with the drafts list (drafts = unsubmitted, threads = persisted). The section header holds the **Apply all** button. The picker is where a thread's id is surfaced for discoverability.

### 5. Delete (the backend slice)

- **Backend:** register `DELETE /repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}` → a `deleteReviewThread` handler (local-gated; `resolveThreadForMR` for ownership; then `db.DeleteReviewThread`; return the reloaded thread list). Add `DeleteReviewThread(ctx, threadID)` to `queries_review_threads.go` — delete the thread and its comments in one transaction (explicit delete of comments then thread, or rely on an `ON DELETE CASCADE` FK; follow whatever the existing schema uses). Regenerate the client.
- **UI:** a **Delete** button on `ReviewThreadCard` and in the picker, with an inline confirm (click → "confirm?") because it is permanent. Hide/Resolve are unchanged (hide = temporary, resolve = done, delete = gone).

### 6. Live status (approach 5A)

While the worktree session has a running turn, threads must update without a manual refresh (the current gap). Reuse `worktreeSession`'s running-turn lifecycle: on each poll tick where a turn is `queued`/`running`, also call `reviewThreadsStore.refresh()` for the active review; stop when no turn is running. Additionally hook `reviewThreadsStore.refresh()` into `Provider.svelte`'s `onDataChanged` (SSE `data_changed`) as a cheap catch-all. Net effect: threads flip `open → discussed → applied`, and agent reply comments appear, live during a turn. The poll must be scoped to the active review and torn down cleanly (no leaked intervals) — reuse the session store's existing lifecycle rather than adding a parallel one.

## Data flow

Submit (mode) → 2a create + optional kickoff → live poll while the turn runs → threads/comments update → cards + picker reflect `discussed`/`applied` and show the agent's replies. Per-thread Apply / Apply-all → 2a apply endpoints → same live update. Delete → `DELETE` → thread removed from state.

## Testing

- **Vitest** (run from `frontend/`, `cd frontend && bunx vitest run`): store `apply`/`applyAll`/`delete`/`createThreads(mode)` and refresh-while-running; `ReviewThreadCard` Apply/Delete (incl. inline confirm + status-gated visibility); the new thread-picker section (list render + click-to-scroll); `ReviewPanel` mode picker (local-only, default persist-only, wires `mode`).
- **Go e2e** (`internal/server`, via the generated client): the delete endpoint — create → delete → gone; ownership 404 on a foreign thread id; comments cascade. Conventions: `-shuffle=on`, testify, run the server suite unsandboxed (tmux).

## Risks / notes

- **Poll lifecycle:** scope thread refresh to the active review and tear it down with the session poll to avoid leaked intervals or cross-review refreshes.
- **Delete during an in-flight turn:** if the agent is mid-reply on a thread that gets deleted, its `reply_to_thread` call 404s harmlessly (surfaces only in the activity log). Acceptable for a local single-user tool.
- **Optimistic status (from 2a):** status is set at kickoff, so a *failed* turn leaves a thread reading `discussed`/`applied`; the live poll surfaces the agent's actual replies and the session/activity pane shows the failure. 2b does not change this (a stricter on-completion reconciliation remains a possible later refinement).
