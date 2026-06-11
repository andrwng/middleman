# QoL Grab-Bag — Design

**Date:** 2026-06-10
**Status:** Approved (ready for implementation plan)
**Branch:** `qol-grab-bag`
**Source:** `qol-suggestions.md` at repo root (5 items, kept verbatim in §Decisions)
**Builds on:** the local-review-threads stack (merged via PR #7) — see `docs/superpowers/specs/2026-05-29-local-review-threads-design.md` and `…/2026-05-31-phase3-external-mcp-design.md`.

## Goal

Sand down five friction points in the local-review-threads workflow that surfaced after living with the feature: the agent's "busy" state should queue work instead of rejecting it; clicking a thread in the picker should take you to it even when its commit isn't checked out; an external agent should be able to *start* a review thread (not just reply); applying a thread should leave it write-capable for follow-up; and "Send"-only comments should be carried to the agent on the next engage instead of staying invisible. Net effect: fewer dead-ends, less typing, less re-typing.

## Decisions (from brainstorming)

Verbatim items + the design choice locked in for each.

1. **Queue engage clicks while the activity session is busy.** Per-session FIFO in-memory queue inside `SessionRunner`. The HTTP-layer 409 ("agent is busy") goes away — `SubmitTurn` always accepts, enqueues, returns. Latent race in the conversation-pane submit gets serialized for free.
2. **Click a thread → take me there.** Copy the `QuestionsSection` scope-switch pattern into the review-threads picker. Anchor commits not in the list (rebased away) fall back to HEAD scope + the row is marked orphan.
3. **MCP `start_thread` tool.** Minimal shape: `path`/`side`/`line`/`body`, optional `start_line` + `commit_sha` (defaults to worktree HEAD). Re-uses the existing create endpoint with `mode=""` (persist-only). No `mode` arg — cross-agent auto-engage is a footgun.
4. **"Changes allowed" mode per thread, after Apply.** Reuse the existing `status` column — `writes_allowed = (status == "applied")`. No new column, no migration. One tweak in the optimistic status setter prevents Discuss from downgrading `applied → discussed`. Steer turns on an applied thread get the apply-grade tool list.
5. **"Send"-only comments are flushed to the agent on the next engage.** At engage time (server, synchronous), gather every `author=user && !sent_to_agent` comment on each engaged thread, include them in the prompt as a stacked "Reviewer's notes since the last engage" block, mark them all sent. Per-thread, not session-wide.

## Scope

**In:** the five items above, exactly. One spec, one branch.

**Out:**
- A visible "queued: N" counter in the conversation pane header (rely on the existing turn list).
- A UI badge/icon for the `writes_allowed` state on a thread (the API field is there; UI can hint later if useful).
- `start_thread` accepting a `mode` for cross-agent auto-engage.
- Cross-worktree MCP discovery (`list_reviews`/`get_review`) — already cut per Phase 3.
- Token auth on REST/MCP (loopback only).
- Persistence of the in-memory queue across server restarts (startup reaper handles zombies instead).

## Current touch points (verified 2026-06-10)

- `internal/server/huma_routes_review_threads.go` — `createReviewThreads` (~L209), `addReviewThreadComment` (~L310), `askReviewThread` (~L338, `MarkReviewThreadCommentSentToAgent` at L360 — to remove), `kickoffReviewTurn` (~L478, busy gate at L493 — to remove; optimistic status setter at ~L529 — needs the "don't downgrade applied" tweak), `applyReviewThread` (~L559), `discussReviewThread` (~L587).
- `internal/server/huma_routes_sessions.go` — `submitWorktreeSessionTurn` (~L136, currently NO busy check — latent race the queue fixes), `sessionHasRunningTurn` (~L278, only caller becomes the UI predicate path; helper itself can stay for now or be removed if unused after the refactor).
- `internal/aireview/sessions.go` — `SubmitTurn` (~L145), `spawnTurn` (~L198), `runTurn` (~L215, allowed-tools assembly ~L235–246), `CancelTurn` (~L184), `buildSessionPrompt` (~L626 + `formatThreads` ~L609), `ThreadContext`/`SubmitTurnInput` structs at the top of the file.
- `internal/mcp/tools.go` — `builtinTools()` map (the 4 existing tools); `restJSON` helper; `reviewPath`.
- `internal/db/queries_review_threads.go` — `SetReviewThreadStatus` (~L313); `MarkReviewThreadCommentSentToAgent` (existing); thread + comment scanners. New query `ListUnsentUserComments` lands here.
- `internal/db/queries_sessions.go` — `ListWorktreeSessionTurns` (used by reaper); a small `MarkQueuedAndRunningTurnsCancelled` (new, used once at runner startup).
- `packages/ui/src/components/diff/ReviewThreadsSection.svelte` — `scrollToThread` (L32), `selectThread` (L46), orphan-dot CSS.
- `packages/ui/src/components/diff/ReviewThreadCard.svelte` — `busy` derived var (L12); the disables on Apply (L126), Ask Claude (L191), Discuss/empty-composer (L191 same button); tooltips at L192–196.
- `packages/ui/src/components/diff/QuestionsSection.svelte` (L57–L122) — the canonical scroll-to-thread implementation to copy for item 2.
- `packages/ui/src/stores/reviewThreads.svelte.ts` — `ReviewThreadResponse` type (touches API regen if §4 field lands); no API surface change otherwise.
- `packages/ui/src/stores/worktreeSession.svelte.ts` — `hasRunningTurn` (L60); semantics unchanged (queued|running still both count as "in flight"), but the predicate's *meaning* shifts from "click would 409" to "click would queue" — wire the UI accordingly.
- API schema: `internal/server/api_types.go` (or wherever `ReviewThreadResponse` lives) — optional additive `writes_allowed bool` field for §4.

## Design

### Item 1 — Per-session FIFO queue in `SessionRunner`

**Goal.** Two clicks back-to-back while a turn is running should both run, in order. The "agent is busy" 409 disappears from the user-facing surface.

**Runner data model.**
```go
type sessionQueue struct {
    items   []queuedTurn // pending dispatches, FIFO
    running bool         // a turn is currently in flight on this session
}
type queuedTurn struct {
    in       SubmitTurnInput      // captured engage descriptor
    respTurn db.WorktreeSessionTurn
}
// SessionRunner gains:
queues map[int64]*sessionQueue
qmu    sync.Mutex
```

**`SubmitTurn` flow.** Same row inserts as today (user turn `done`, response turn `queued`). Then:
```
qmu.Lock()
q := queues[sess.ID]   // create if nil
q.items = append(q.items, queuedTurn{in, respTurn})
should := !q.running
if should { q.running = true }
qmu.Unlock()
if should { go r.dispatchNext(sess.ID) }
return SubmitResult{userTurn, respTurn}
```

**`dispatchNext`.** Locks the queue, pops the head item, unlocks; rehydrates per-dispatch state (see below); calls `runTurn`; on completion locks again and either pops the next item (recurse via `go`) or sets `running = false` and exits. Tail-recursion in goroutine form — no thread pool, one in-flight per session.

**Per-dispatch rehydration.** Some `SubmitTurnInput` fields are time-sensitive and must refresh just before spawn:
- `IsFirstTurn` — re-read `session.ClaudeSessionID == ""` from `GetWorktreeSession`. (Today it's captured at submit time; with queuing, three submits on a fresh session would all wrongly think they're first.)
- `BaseRef`/`BaseSHA`/`HeadSHA` — re-resolve from the worktree (`ResolveBase`, latest head). Worktree state may have moved since the submit.

The queued `SubmitTurnInput` carries the things that are NOT time-sensitive: `Action`, `Threads` (id + path/line/side/root-comment + the new stacked-comments slice from §5), `MCP` config, `UserTurnType`, `UserTurnContent`.

**`CancelTurn`.** Today it kills the PID + flips the turn row to `cancelled`. Adds:
1. `qmu.Lock()` and walk the queue for this session — if the cancelled turn is queued (not yet running), drop it from `items`. (Identify by `respTurn.ID`.)
2. If it was running (in `r.running` map), proceed as today.
3. Always flip the row to `cancelled` (no PID is fine — column is nullable).

**Server-restart reaper.** New runner method `ReapInterrupted(ctx)` called once from `NewSessionRunner` (or from the server's `Init` immediately after `NewSessionRunner`). It does a single DB sweep: `UPDATE middleman_worktree_session_turns SET status='cancelled', pid=NULL, error_message='interrupted by server restart' WHERE turn_type='claude_response' AND status IN ('queued','running')`. Idempotent. Logs the affected row count at `INFO`. This clears DB zombies from the previous process's in-memory queue.

**HTTP layer changes.**
- `kickoffReviewTurn` (`huma_routes_review_threads.go` L478): remove the `sessionHasRunningTurn` check + the 409 return. SubmitTurn just enqueues.
- `submitWorktreeSessionTurn` (`huma_routes_sessions.go` L136): no change required — it already calls `SubmitTurn` without a busy check, which now serializes correctly behind the queue.
- The server-side `sessionHasRunningTurn` helper in `huma_routes_sessions.go` (L278) has only one caller today — `kickoffReviewTurn` — which is removed by this refactor. Delete the helper at impl time. The frontend predicate `worktreeSession.hasRunningTurn()` (`packages/ui/src/stores/worktreeSession.svelte.ts` L60) is independent and stays — its semantics shift from "click would 409" to "click would queue", but the predicate value itself (any `queued|running` turn exists) is unchanged.

**Frontend changes.**
- `ReviewThreadCard.svelte`:
  - Apply button (`disabled={busy}` → drop the disable; tooltip changes from "Apply this thread's change" → "Apply this thread's change" when idle / "Queue an apply turn" when `busy`).
  - The shared Send/Ask Claude/Discuss button (currently `disabled={sending || busy}` for Ask, `disabled={sending}` for Send): drop the `|| busy` from the Ask/Discuss path. Sending stays gated (avoid double-click). Tooltip mirror: idle → as today; busy → "Queue an Ask turn" / "Queue a discuss turn".
  - The "asking…" badge effect (L28–37) keeps working — it fires when `discussThread` runs and clears when an agent reply lands. Queued state extends the pulse window naturally (`busy` stays true while queued).
- `ReviewThreadsSection.svelte`: Apply-all button (`disabled={busy}` → drop the disable; tooltip becomes "Queue Apply for N thread(s)" when busy).
- No new dedicated UI counter for queued depth — the conversation pane's existing turn list renders `queued` rows already.

### Item 2 — Click thread in picker → switch scope + scroll

**File:** `packages/ui/src/components/diff/ReviewThreadsSection.svelte`. Frontend-only.

**`selectThread(t)` becomes async** and follows the same logic `QuestionsSection.svelte` already uses (L57–122):
1. `const commits = diffStore.getCommits();`
2. Determine target scope:
   - If `commits && commits.length > 0 && commits[0].sha === t.commit_sha` and `scope.kind !== "head"` → `await diffStore.resetToHead();`
   - Else if `commits.some(c => c.sha === t.commit_sha)` and `!(scope.kind === "commit" && scope.sha === t.commit_sha)` → `await diffStore.selectCommit(t.commit_sha);`
   - Else if `commits && !commits.some(c => c.sha === t.commit_sha)` (orphan — rebased away) → `await diffStore.resetToHead();`
   - Else (already in the right scope, or commits still loading) → no scope switch.
3. If file is collapsed, toggle it open (reuse `diffStore.toggleFileCollapsed`).
4. `await tick();` then `document.querySelector(...)` on the same anchor selector the current `scrollToThread` builds (`.diff-file[data-file-path="…"] .line-wrap[data-anchor-line="…"][data-anchor-side="…"]`). On hit: `scrollIntoView({block: "center", behavior: "smooth"})` + `el.classList.add("line-wrap--flash")` for 1.5s (matches QuestionsSection). On miss: scroll the file header into view as a fallback.

**Orphan indicator.** Compute `const isOrphan = commits && !commits.some(c => c.sha === t.commit_sha);` (in the row's `$derived`). Add a `thread-item__dot--orphan` CSS variant (grey/dimmed; the existing `--resolved` style is close — use a distinct subtler shade so resolved-vs-orphan stays readable). The row's tooltip becomes "anchored to a commit no longer in this branch" when orphan. If `commits` hasn't loaded yet (`commits == null`), `isOrphan` is false — avoid false-positive flagging during initial mount.

**No new exports** from `reviewThreads.svelte.ts`; everything reads `diffStore` directly. The store is already in scope via `getStores()`.

### Item 3 — MCP `start_thread` tool

**File:** `internal/mcp/tools.go`. No new REST endpoint.

**Tool registration** (add to `builtinTools()` map):
```
start_thread:
  description: "Create a new review thread anchored to a line in the current review.
                Use this to flag code for the reviewer (or another agent) to see."
  inputSchema:
    type: object
    required: [path, side, line, body]
    properties:
      path:       { type: string }
      side:       { type: string, enum: ["LEFT", "RIGHT"] }
      line:       { type: integer, minimum: 1 }
      body:       { type: string }
      start_line: { type: integer, minimum: 1 }  // optional
      commit_sha: { type: string }               // optional; defaults to current HEAD
```

**`call` implementation.**
1. Parse + validate args (let server-side validation handle range checks; do the cheap "required" checks here for clearer errors).
2. If `commit_sha` is missing, resolve HEAD: call `s.restJSON("GET", s.reviewPath(""), nil)` (the existing `get_pull` route), parse `head.sha`. On error, surface the error as the tool's `isError` result and stop.
3. Build the payload (single-draft array, `mode=""`):
   ```json
   {
     "mode": "",
     "threads": [
       { "path": "...", "side": "...", "line": N, "start_line": M?, "commit_sha": "...", "body": "..." }
     ]
   }
   ```
4. `POST {reviewPath}/review-threads` with that body. Server returns `{threads: [...]}` (all threads on the branch after creation).
5. Return the newly-created thread's JSON. The server response doesn't tag which is new, so the MCP layer picks it by `max(id)` from the `threads` array.

**Branch.** Implicit — server already stamps the worktree's current branch on create.

**Allowed-tools allowlist (in-app runner).** The in-app `SessionRunner` allowlist at `sessions.go:239` currently exposes three MCP tools (`list_threads`, `get_thread`, `reply_to_thread`) — note this is narrower than the four-tool external surface (`get_pull` is omitted in-app because the in-app prompt already carries worktree/base/head context via `writeWorktreeContext`). Add `mcp__middleman__start_thread` to this allowlist (bringing in-app to four). An in-app agent shouldn't *normally* create new threads — the reviewer creates threads via the UI — but allowing it keeps the in-app and external surfaces symmetric and avoids a future surprise. (Discuss-only steer turns can therefore start a thread; this is fine — `start_thread` writes a DB row, not code.)

### Item 4 — "Changes allowed" mode per thread, after Apply

**Storage.** None. Reuse `middleman_review_threads.status`. `writes_allowed` is derived as `(status == "applied")`.

**Server tweak** (`huma_routes_review_threads.go`, optimistic status setter at ~L529–537):
```go
if action != "steer" {
    target := "discussed"
    if action == "apply" { target = "applied" }
    for _, t := range threads {
        // Don't downgrade applied → discussed when a subsequent Discuss fires.
        if action == "discuss" && t.Status == "applied" { continue }
        _ = s.db.SetReviewThreadStatus(ctx, t.ID, target)
    }
}
```

**Runner plumbing.**
1. `ThreadContext` (in `internal/aireview/sessions.go`) gains `WritesAllowed bool`.
2. Server populates it from the just-loaded `db.ReviewThread.Status` when building `tcs` in `kickoffReviewTurn`.
3. `SubmitTurnInput` gains `AllowWrites bool`. Server sets it to:
   ```go
   in.AllowWrites = in.Action == "steer" &&
                    len(threads) > 0 &&
                    all(threads, func(t) { return t.Status == "applied" })
   ```
   Strict opt-in: a mixed batch (any not-applied thread) falls back to read-only. In practice steer is always single-thread today, so this is a clean generalization.
4. `runTurn` allowed-tools (~L243–246) gains a branch:
   ```go
   if in.Action == "apply" || in.Action == "" || in.AllowWrites {
       allowed += ",Edit,Write,MultiEdit,Bash"
   }
   ```
5. `buildSessionPrompt` steer branch (~L650–660): when `in.AllowWrites`, replace `"Do not change any files — this is discussion only."` with `"You may edit files in the worktree if the reviewer's message asks for a change. Use reply_to_thread to summarize what you changed."`.

**API surface.** Add `writes_allowed bool` to `ReviewThreadResponse` (additive; non-breaking). Backed by `status == "applied"` server-side — no DB read needed beyond what's already loaded. UI doesn't render a badge in this branch (deferred); the field is there for later.

**Resolve/unresolve interaction.** `unresolveReviewThread` sets status to `"open"` (no change here). A sequence apply→resolve→unresolve clears `writes_allowed`. Documented as intentional — "unresolve = start fresh on this thread."

### Item 5 — Stack "Send"-only comments to the next engage

**Goal.** A reviewer can type "sounds good, do it" + Send, "but also this" + Send, then Ask — the agent sees all three messages, not just the last.

**The flush step.** Runs inside `kickoffReviewTurn`, just before `r.sessionRunner.SubmitTurn(...)`:
1. For each thread in the engaged batch, query unsent user comments:
   ```go
   unsent, _ := s.db.ListUnsentUserComments(ctx, t.ID)
   // returns []db.ReviewThreadComment in id-ASC order,
   // filtered by author='user' AND sent_to_agent=0
   ```
2. Split: the first comment of the *thread* (comment[0], the root, already surfaced as `ThreadContext.RootComment` via `firstThreadCommentBody`) is excluded from the stacked-prompt block to avoid duplicating the headline. Capture the rest into `ThreadContext.StackedComments []string` (new field, body strings in order). Concretely: get the thread's overall first-comment id once (cheap — `firstThreadCommentBody` already reads it), then iterate `unsent` skipping the entry whose id matches it.
3. After `SubmitTurn` returns successfully (no error from the runner), call `MarkReviewThreadCommentSentToAgent(ctx, id)` for every comment id in the full `unsent` list (including the root — its "asked" badge should reflect that it was conveyed to the agent as the thread headline).

If `SubmitTurn` errors (shouldn't with the queue refactor, but defensively), do NOT mark sent — the engage didn't take.

**Prompt construction.** Extend `formatThreads` in `sessions.go` (~L609) to render the stacked block per thread when non-empty:
```
- thread 42 - foo.go:120 (after): {root body}
  Reviewer's notes since the last engage:
  1) {body 1}
  2) {body 2}
  3) {body 3}
```

For `steer` specifically (~L650–660), drop the existing tail:
```
The reviewer's message:
{in.UserTurnContent}
```
The reviewer's just-typed body is part of the stacked list already (it was added to the thread via `askReviewThread`'s `AddReviewThreadComment` call before kickoff, and the flush picks it up). The stacked list supersedes the explicit "reviewer's message" footer.

**`askReviewThread` simplification.** Current code (`huma_routes_review_threads.go` ~L352–360) adds the comment, calls kickoff, then marks just-the-new-comment sent. With §5, drop the trailing `MarkReviewThreadCommentSentToAgent(ctx, comment.ID)` — the flush inside kickoff covers it as part of the unsent batch.

**Side effect (positive — flagged).** Today the root user comment on threads created with `mode=discuss-first` or `mode=act-immediately` is never marked sent (kickoff goes through but the mark logic only runs for `/ask`). With §5, the flush picks it up at create-time kickoff and the "asked" badge correctly appears on first engage. Note this in the PR description; no behavior the user explicitly relies on changes.

**Queue interaction (Item 1 × Item 5).** Mark-sent happens at engage time (synchronous, before the SubmitTurn enqueue returns), NOT at dispatch. Consequences:
- If the user cancels the queued turn before it dispatches, the flushed comments stay marked sent. Right semantic ("I sent it"); a re-engage sends only what was typed since.
- If `SubmitTurn` itself fails synchronously (rare — e.g. session ensure fails), the comments are NOT marked sent.

### Cross-cutting

- **One spec, one branch.** Implementation order: 2 → 3 → 4 → 1 → 5 (smallest blast radius first; queue + comment-stacking as the heavy tail since they share the engage path).
- **Commits.** One per item, conventional commit messages (`feat(ui): …`, `feat(mcp): …`, `feat(review-threads): …`, `feat(aireview): …`). Items 1+5 may land as one commit since they share `kickoffReviewTurn`/`buildSessionPrompt` edits.
- **API regen.** A single `make api-generate && go generate ./internal/apiclient/generated` at the end. Only schema delta is item 4's optional `writes_allowed bool` on `ReviewThreadResponse` — additive, non-breaking. Stage all four generated artifacts.
- **No DB migration.** Items 4 + 5 reuse existing columns (`status` + `sent_to_agent`).
- **Memory update.** On merge/ship, append a 2026-06-10 addendum to `local_review_threads.md` noting: 409 busy gate → FIFO queue; writes_allowed = status==applied; pure-comment stacking via existing `sent_to_agent`; MCP `start_thread` added. Not before merge.

## Error handling

- **Queued turn whose worktree disappears at dispatch.** `dispatchNext` re-resolves the worktree/branch/base/head before spawn. On a hard failure (worktree row gone, path missing), mark the response turn `failed` with a clear error message and skip — keep the queue moving.
- **Cancel a queued turn that's racing into dispatch.** `qmu` brackets both ops; a cancel-while-dispatch either finds the item still in `items` (drop it before dispatch fires) or finds it in `r.running` (existing path, kill PID). The window between "popped from queue" and "added to `r.running`" is held under `qmu` to close the race.
- **MCP `start_thread` with no HEAD resolvable.** Worktree HEAD lookup goes through the existing `get_pull` REST path; existing `isError` plumbing surfaces the failure verbatim ("rest GET …: status 404: …"). The user sees the message in the agent's tool-result block.
- **Orphan thread click during `commits` loading.** `commits == null` short-circuits to not-orphan; the existing scroll-without-switch path runs (effectively today's behavior). When commits load, the orphan dot appears.

## Testing

Conventions: testify (`require` for setup, `assert` for non-blocking), `-shuffle=on`, no `-v` / no `-count=1`. Frontend: vitest + svelte-check from `frontend/`. E2E is non-negotiable for every item — these are user-visible behaviors.

### Item 1 — queue

- `internal/aireview/sessions_test.go`: with a fake `claude` binary that blocks on a signal:
  - Two `SubmitTurn` calls back-to-back → assert process #2 doesn't start until process #1 exits.
  - Cancel a queued (not-yet-spawned) turn → status flips to `cancelled`; the next queued item dispatches normally; no PID was ever recorded.
  - Restart reaper: seed `queued` + `running` rows in the DB, call `NewSessionRunner` (or `ReapInterrupted` directly), assert all flipped to `cancelled` with the marker error.
- E2E (`internal/server/review_threads_agent_e2e_test.go`): with a slow fake-claude, kick `apply` on thread A and immediately `discuss` on thread B; both response turns reach `done`; both threads end with the expected status; activity-log ordering is A then B.

### Item 2 — picker click

- Vitest (`packages/ui/src/components/diff/ReviewThreadsSection.test.ts`): mount with a thread-set + stubbed `diffStore`.
  - Click thread anchored to the head commit → `resetToHead` called.
  - Click thread anchored to a non-head commit that's in `commits` → `selectCommit(sha)` called.
  - Click thread anchored to a sha NOT in `commits` → `resetToHead` called AND the row has the orphan class + tooltip.
  - During load (`commits == null`) → no orphan flag (avoid flicker).

### Item 3 — MCP `start_thread`

- `internal/mcp/server_test.go` (or sibling): with `httptest` standing in for the REST server:
  - Happy path with explicit `commit_sha` → POSTs to `/review-threads` with the right payload; response shape parsed; tool returns the new thread JSON.
  - `commit_sha` omitted → MCP first GETs `/pulls/{n}` to resolve HEAD, then POSTs with that sha.
  - Missing `body` → tool returns `isError` with the server's 400 message.
  - Invalid `side` → same.
- E2E in `internal/server/review_threads_agent_e2e_test.go`: spawn `middleman mcp` against a live test server, call `tools/list` → `start_thread` present; call `start_thread` → followed by `list_threads` shows the new thread on the worktree's current branch.

### Item 4 — `writes_allowed`

- `internal/server/review_threads_agent_e2e_test.go`:
  - Apply on thread A, then `/ask` on A with a message → spawn args include `Edit,Write,MultiEdit,Bash`.
  - `/ask` on a never-applied thread B → spawn args do NOT include those.
  - Apply on A, then `/discuss` on A → thread A's status stays `applied` (the "don't downgrade" tweak).
  - Apply on A, resolve, unresolve, `/ask` → spawn args do NOT include edit tools (status reverted to `open`).
- `internal/db/queries_review_threads_test.go`: no change needed unless `ListUnsentUserComments` lands here (it does — see Item 5).

### Item 5 — comment stacking

- `internal/db/queries_review_threads_test.go`: round-trip `ListUnsentUserComments` — returns user comments with `sent_to_agent=0` in id ASC; ignores agent comments + already-sent comments.
- `internal/server/review_threads_agent_e2e_test.go`:
  - Create a thread, add two pure comments via `/comments`, call `/discuss` → spawn args contain the root + 2 pure bodies in the prompt (use the existing prompt-capture test seam); all three comments now `sent_to_agent=true`.
  - Same setup + `/ask` with body `X` → prompt has root + 2 pure + X; all marked sent.
  - Same setup + `/apply` → apply prompt carries the stacked block.
  - Create with `mode=discuss-first` → root comment is marked sent after the kickoff completes (the noted positive side-effect).
- Vitest on the store: `addComment` doesn't mark sent; the "asked" badge flips on after `/ask`/`/apply`/`/discuss` and the refresh.

## Risks / notes

- **In-memory queue lost on server restart.** Acceptable for a local single-user tool. The DB reaper at startup cancels orphan `queued|running` rows so the conversation pane shows a clean state; the user re-clicks. If this becomes annoying in practice, the follow-up is to serialize the `SubmitTurnInput` to the response turn's `metadata_json` so a fresh runner can resume — out of scope here.
- **Optimistic `status="applied"` carries `writes_allowed` even if the apply turn fails.** Inherits today's optimism (the design doc that landed `status` explicitly accepted this for a local single-user tool). A failed apply surfaces in the activity log; the user can resolve+unresolve to reset.
- **Apply-grade tool list on a steer turn unlocks Bash.** Same reach as today's apply turns; the gate is per-thread (Apply already ran here) so the surface doesn't widen beyond what the reviewer authorized via Apply.
- **Cancel-while-dispatch race** is held under one mutex (`qmu`) by design; the test "cancel a queued turn just as the previous turn ends" should be a deliberate case to exercise.
- **`get_pull` round-trip on `start_thread` when `commit_sha` omitted** is one extra HTTP hop per call. Cheap (loopback, ~ms), and a one-time cost per agent tool call. No caching — keeps the tool stateless and the cwd-default model coherent.
- **Root-comment duplication is handled explicitly.** Mode `discuss-first`/`act-immediately` makes the root unsent at create-time kickoff; without the flush's split, it would appear both as the thread headline AND in the "Reviewer's notes since the last engage" block. The split in §Item 5 step 2 strips the root from the stacked block by id-match against the thread's first comment, then still marks it sent.
