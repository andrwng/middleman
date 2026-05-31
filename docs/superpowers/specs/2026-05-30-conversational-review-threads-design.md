# Conversational Review Threads ("Ask Claude" in a thread) — Design

**Date:** 2026-05-30
**Status:** Approved (ready for implementation plan)
**Branch:** `serve-local-repo-comments` (stacked on unmerged `branch-navigation`)
**Depends on:** Phases 1a/1b/2a/2b + the SessionRunner per-turn timeout fix — all on this branch.
**Related spec:** `docs/superpowers/specs/2026-05-29-local-review-threads-design.md` (this realizes the "steer" turn it envisioned but never built).

## Goal

Let a reviewer continue the conversation with the agent *inside a review thread*. Today, replying in a thread (`addReviewThreadComment`) only persists the comment — it never reaches Claude; the agent runs only on submit-mode and Apply. This adds an explicit **"Reply & ask Claude"** action that sends the thread's conversation to the agent as a read-only **steer turn**, with Claude's response appearing as a normal thread reply so the conversation flows naturally.

## Decisions (from brainstorming)

- **Explicit engage**, not automatic: a plain **Reply** stays a silent note (today's behavior); a separate **Reply & ask Claude** action engages the agent. Predictable, no surprise agent runs, and notes-without-agent stay possible.
- **Discussion-only**: "Ask Claude" is a **read-only** turn (Claude reads + replies, never edits). **Apply** remains the separate edit action and still sees the full conversation via the shared `--resume` session.
- **Natural flow**: Claude's answers are ordinary agent comments in the thread (via `reply_to_thread`, exactly like discuss replies today). User comments that were asks get a subtle **orange marker**; nothing else changes visually.

## Scope

**In:** the `/ask` endpoint + a read-only `steer` runner turn; a persisted `sent_to_agent` marker on comments + its UI badge; disabling Ask/Apply while a turn runs (surfacing "agent busy" — folds in the deferred 2b item); the store + card wiring; client regen; tests.

**Out (Phase 3, unchanged):** external-MCP discovery (`list_reviews`/`get_review`), cwd-default, external-shell registration. Also out: auto-engage modes, queuing asks while busy (we reject + surface, consistent with 2a's "Apply-while-busy rejected, not queued").

## Current touch points (verified)

- `internal/server/huma_routes_review_threads.go` — `addReviewThreadComment` (pure persist today), `kickoffReviewTurn` (maps `action`→verb + per-action status; `discuss`→`review_feedback`, `apply`→`user_message`), `resolveThreadForMR`, `registerReviewThreadRoutes`, `reviewThreadActionInput`, `reviewThreadCommentResponse`, `oneReviewThreadOutput`.
- `internal/aireview/sessions.go` — `SubmitTurnInput.Action`; `runTurn` per-phase `--allowedTools` (`if in.Action == "apply" || in.Action == "" { + Edit,Write,MultiEdit,Bash }` — so any other action, incl. `steer`, is read-only + the `mcp__middleman__*` tools); `buildSessionPrompt` action switch; the shared session via `--resume`.
- `internal/db/queries_review_threads.go` — `AddReviewThreadComment(ctx, threadID, author, body, turnID)`, `ListReviewThreadComments`, the `review_thread_comments` table; `middleman_review_thread_comments` columns.
- `packages/ui/src/components/diff/ReviewThreadCard.svelte` — the reply box (`sendReply` → `reviewThreads.addComment`), comment rendering, Apply/Delete (Apply gated on status), `worktreeSession.hasRunningTurn()` available via `getStores()`.
- `packages/ui/src/stores/reviewThreads.svelte.ts` — `addComment`, `apply`, etc.; openapi-fetch client.

## Design

### 1. Backend — `steer` turn (read-only)

Add `action:"steer"` support to the runner path:
- **Tool-gating:** `steer` already lands in the read-only bucket — `runTurn`'s edit-tools branch only fires for `apply`/`""`, so `steer` gets `Read,Glob,Grep` + the three `mcp__middleman__*` tools (incl. `reply_to_thread`), **no** Edit/Write/Bash. (Confirm the gating during impl; no change needed beyond passing `Action:"steer"`.)
- **Prompt:** `buildSessionPrompt` gains a `steer` branch — a short, read-only prompt that carries the worktree context, names the thread (path:line), includes the reviewer's latest message, and instructs the agent to respond and continue the discussion via `reply_to_thread`, **not** editing files. It reuses the `--resume` session, so the agent already remembers the review (and can `get_thread`/`list_threads` to re-read the full thread).
- **The reviewer's message** is the turn's `UserTurnContent` (verb `user_message`). `kickoffReviewTurn` is extended to carry an explicit message for `steer` (today it builds a generic `actionMessage`); for `steer` it does **not** change the thread's status (a continuation, not a new discuss/apply).

### 2. Backend — `/ask` endpoint

`POST /repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask`, body `{ body: string }`:
1. `isLocalSource` gate; `resolveThreadForMR` (ownership/404); reject empty body.
2. Persist the reviewer's comment (`author:"user"`).
3. Kick off a `steer` turn over that thread (the reviewer's message as `UserTurnContent`).
4. Ordering (explicit): persist the comment **unmarked**; attempt the `steer` kickoff; on success set **`sent_to_agent=true`** on that comment; if the agent is **busy** (the existing `sessionHasRunningTurn` 409 gate), leave it a plain note and surface the busy state. The reviewer's message is never lost, and `sent_to_agent` means a turn was actually kicked off.
5. Return the reloaded thread (the live poll then streams Claude's reply in).

### 3. The `sent_to_agent` marker

A numbered **migration** adds `sent_to_agent BOOLEAN NOT NULL DEFAULT 0` to `middleman_review_thread_comments` (pick the next free migration number at impl time; per project memory, mind the 000021 collision note if branches ever combine). The comment-insert path sets it (`true` for asks that engaged the agent, `false` for plain replies). `reviewThreadCommentResponse` gains `sent_to_agent bool`. The frontend renders a subtle **orange marker/badge** on user comments where `sent_to_agent` is true — purely indicative, the conversation still flows.

### 4. Busy UX (folds in the deferred 2b item)

While `worktreeSession.hasRunningTurn()` is true, **disable** the thread's **Ask** action and the per-thread **Apply** button (tooltip "the review agent is busy"), matching how `ReviewThreadsSection`'s Apply-all already behaves — so the reviewer can't click into a silent 409. If an Ask/Apply still races a 409, the comment is saved (for Ask) and the busy message is surfaced rather than swallowed.

### 5. Frontend

- `reviewThreads` store: `ask(threadID, body)` → `POST …/ask`; replaces state from the returned thread list (like `apply`).
- `ReviewThreadCard`: the reply box gains a **Reply & ask Claude** action next to plain **Reply**; both disabled while a turn runs. Render the orange marker on `sent_to_agent` user comments.
- Claude's replies need no new rendering — they arrive as `author:"agent"` comments the card already shows, refreshed by the existing live poll.

### 6. Data flow

Reply & ask → `/ask` persists the comment (marked sent) + kicks a read-only `steer` turn (shared `--resume` session) → agent reads + `reply_to_thread` → live poll streams the agent comment into the card → conversation continues. Apply still available for edits, with the conversation as context.

## Testing

- **Go runner** (`internal/aireview`): a `steer` turn test (mirrors the discuss test) asserting read-only gating (`--allowedTools` has the mcp tools, no `Edit/Write/Bash`) + MCP wiring; the steer prompt carries the reviewer's message.
- **Go e2e** (`internal/server`, generated client): `/ask` persists a `sent_to_agent` user comment and kicks a turn (fake claude), the thread reloads; busy → comment saved + busy surfaced; ownership 404.
- **Vitest** (from `frontend/`): store `ask()` (path/params/state); `ReviewThreadCard` Reply-&-ask action calls `ask`, plain Reply still calls `addComment`, both disabled while busy; the orange marker renders for `sent_to_agent` user comments.
- Conventions: `-shuffle=on`, testify, run the server suite unsandboxed (tmux); `make api-generate` sandbox-off + `GOCACHE`; vitest from `frontend/`.

## Risks / notes

- **Busy race:** Ask is disabled while busy, so a 409 race is rare; the invariant (never lose the message; mark sent only when actually kicked off) keeps state coherent.
- **Read-only enforcement:** `steer` must stay out of the edit-tools branch in `runTurn` — a steer test asserting no `Edit/Write/Bash` in the spawned argv guards this.
- **Status:** `steer` deliberately doesn't change thread status (continuation); discuss/apply status semantics are unchanged.
- **Migration discipline:** follow `context/db-migrations.md`; the marker column is additive with a default, so existing rows backfill to `false`.
