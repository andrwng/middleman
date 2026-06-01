# Promote an Ask-Claude session to a review thread (TODO #14)

> **Status:** Design, approved in conversation 2026-06-01. Part of the local-comments
> rework (branch `serve-local-repo-comments-rework`). Implements TODO.md #14.

## Problem

The AI "ask Claude about this code" thread (`AIThreadCard`) is a Q&A session anchored on a
diff line. Today its only export is per-answer **"Promote to comment"**, which copies a single
answer into a **remote PR draft comment** (`diffStore.addDraftComment`). On a **local worktree**
that draft goes nowhere useful, and you can't capture the whole discussion.

We want, on local worktrees, to **promote a whole Ask-Claude session into a first-party review
thread** — the local dual of the remote "promote to comment". The remote per-answer behavior is
unchanged.

## Approach (B — structured)

Map each answered turn of the session to an authored comment in one new review thread:
`Q1` → root `user` comment, then `A1` (`agent`), `Q2` (`user`), `A2` (`agent`), … The thread then
renders as a real conversation in the existing `ReviewThreadCard` (which already labels
`user`→"You" / `agent`→"Claude"). One atomic create call.

This requires the review-thread **create** path to accept extra authored comments beyond the
single root `body` it takes today.

## API contract change

`reviewThreadDraft` (in `internal/server/huma_routes_review_threads.go`) gains an optional ordered
list of comments appended after the root `body`:

```go
type reviewThreadDraftComment struct {
    Author string `json:"author" doc:"user | agent"`
    Body   string `json:"body"`
}

type reviewThreadDraft struct {
    Path      string  `json:"path"`
    Side      string  `json:"side"`
    Line      int     `json:"line"`
    StartLine *int    `json:"start_line,omitempty"`
    CommitSHA string  `json:"commit_sha"`
    Body      string  `json:"body" doc:"the reviewer's root comment"`
    Comments  []reviewThreadDraftComment `json:"comments,omitempty" doc:"additional comments appended after the root, in order"`
}
```

- The root `body` stays the first (`user`) comment; `comments[]` are inserted **in order** after it.
- The create handler **validates** `author ∈ {user, agent}` (400 otherwise).
- **No migration** — the `middleman_review_thread_comments` table already has `author`/`body`.
- Existing callers (the ReviewPanel checkbox flow) omit `comments`, so behavior is unchanged.

## DB change

`db.NewReviewThread` gains `Comments []NewReviewThreadComment` (`{Author, Body}`).
`CreateReviewThreadsOnBranch` inserts the root `user` comment (as today), then each
`Comments[i]` with its `author`/`body`, all in the same transaction. If a thread has any
`agent` comment, its status is set to **`discussed`** (it already carries Claude's input);
otherwise the default `open` is unchanged.

## Frontend

- **Store** (`reviewThreads.svelte.ts`): `ReviewThreadDraftInput` gains optional
  `comments?: { author: "user" | "agent"; body: string }[]`; `createThreads` forwards it as
  `comments` in the POST body when present. No new store method — promote reuses `createThreads`
  with `persist-only` (no `mode`, so no agent turn is kicked).
- **`AIThreadCard.svelte`**: add a session-level **"Promote to review thread"** button, shown only
  when `repoOwner === "local"` **and** there is ≥1 answered (`status: "done"` + non-empty `answer`)
  question. On click it builds one draft:
  - `path` = `thread.path`, `side` = `thread.anchor_side`, `commitSha` = `thread.commit_sha`
  - `line` = `thread.anchor_line`; `startLine` = `thread.hunk_start_line` **only if** present and
    `< anchor_line` (guarantees a valid `start ≤ line` range; otherwise omit)
  - `body` = first answered question's text; `comments` = `[{agent, A1}, {user, Q2}, {agent, A2}, …]`
    over answered turns, in id order
  - calls `reviewThreadsStore.createThreads([draft])`.
- The new `ReviewThreadCard` appears inline at the same anchor; the AI thread is left intact.

## Decisions (approved)

- **Keep the AI thread** after promoting (no auto-close; no "remove worktree"). There is no local
  "submit review" action to hook cleanup to, so nothing auto-closes.
- **Only answered turns** are promoted; in-flight/failed questions are skipped.
- **Remote unchanged**: the per-answer "Promote to comment" → remote draft stays as-is.
- New thread **status = `discussed`** (it carries agent comments).

## Tasks

1. **API + DB + validation**: add `reviewThreadDraftComment`/`Comments[]`, thread the comments
   through the create handler (with author validation) into `CreateReviewThreadsOnBranch`
   (append-in-order + `discussed` when agent comments present).
2. **Regenerate clients**: `make api-generate` then `go generate ./internal/apiclient/generated`
   (stage all generated artifacts).
3. **Store**: extend `ReviewThreadDraftInput` + `createThreads` to forward `comments`.
4. **AIThreadCard**: the local-only "Promote to review thread" button + mapping.

## Test plan

- **Go e2e** (`review_threads_e2e_test.go`): create a thread with `comments[]` → assert the thread
  has the root `user` comment followed by the appended comments in order with correct authors, and
  status `discussed` when an agent comment is present; invalid `author` → 400.
- **vitest** (`AIThreadCard.test.ts`): promoting a 2-turn done session calls `createThreads` with
  the expected draft (anchor mapping) and `[user Q1] + [agent A1, user Q2, agent A2]`; button hidden
  for `repoOwner !== "local"` and when there are no answered questions; in-flight/failed turns
  excluded.
- **vitest** (`reviewThreads.svelte.test.ts`): `createThreads` forwards `comments` in the POST body.

## Out of scope

- Per-answer "promote single answer → review thread" (session-level only).
- Dedup / "already promoted" tracking (re-promote just creates another thread; deletable).
- Any change to the remote draft-comment path.
