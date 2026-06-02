# Promote an Ask-Claude session to a review thread (TODO #14)

> **Status:** Design, converged in conversation 2026-06-01. Part of the local-comments rework
> (branch `serve-local-repo-comments-rework`). Implements TODO.md #14.

## Problem

The AI "ask Claude about this code" thread (`AIThreadCard`) is a Q&A session anchored on a diff
line. Today its only export is per-answer **"Promote to comment"**, which copies a single answer
into a **remote PR draft comment** (`diffStore.addDraftComment`). That can't capture the whole
discussion, and on a local worktree a draft is the wrong target (drafts hold a single comment — no
threading).

We want, on local worktrees, to **promote a whole Ask-Claude session into a first-party review
thread**, optionally running it through the review agent — the same persist-vs-act choice the
ReviewPanel submit now offers. The remote per-answer promote is unchanged.

## Approach (structured thread + optional agent)

Create one review thread from the session's answered turns: `Q1` → root `user` comment, then `A1`
(`agent`), `Q2` (`user`), `A2` (`agent`), … It renders as a real conversation in the existing
`ReviewThreadCard` (which already labels `user`→"You" / `agent`→"Claude").

A checkbox on the promote control — **the same one as the ReviewPanel, ticked by default** —
decides what happens on promote:

- **ticked** → create the thread **and** engage the review agent (`act-immediately`: the agent
  applies the discussed change). Same default and semantics as the submit flow.
- **unticked** → just persist the thread (`persist-only`). The resulting thread still has its own
  Apply / Ask buttons for engaging the agent later.

This reuses the structured-comments create payload (below) plus `createThreads`'s existing `mode`.

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
`CreateReviewThreadsOnBranch` inserts the root `user` comment (as today), then each `Comments[i]`
with its `author`/`body`, all in one transaction. If a thread has any `agent` comment, its status
is set to **`discussed`** at insert (it already carries Claude's input). For the `act-immediately`
path the existing kickoff then overrides status to `applied` as usual; for `persist-only` it stays
`discussed`.

## Frontend

- **Store** (`reviewThreads.svelte.ts`): `ReviewThreadDraftInput` gains optional
  `comments?: { author: "user" | "agent"; body: string }[]`; `createThreads` forwards it as
  `comments` in the POST body when present. No new store method.
- **`AIThreadCard.svelte`**: a session-level **"Promote to review thread"** control, shown only
  when `repoOwner === "local"` **and** there is ≥1 answered (`status: "done"` + non-empty `answer`)
  question. It includes an **"engage agent" checkbox, ticked by default**, mirroring the
  ReviewPanel ("Have Claude apply these changes"). On promote it builds one draft:
  - `path` = `thread.path`, `side` = `thread.anchor_side`, `commitSha` = `thread.commit_sha`
  - `line` = `thread.anchor_line`; `startLine` = `thread.hunk_start_line` **only if** present and
    `< anchor_line` (guarantees a valid `start ≤ line` range; otherwise omit)
  - `body` = first answered question's text; `comments` = `[{agent, A1}, {user, Q2}, {agent, A2}, …]`
    over answered turns, in id order
  - calls `reviewThreadsStore.createThreads([draft], engageAgent ? "act-immediately" : undefined)`.
- The new `ReviewThreadCard` appears inline at the same anchor; the AI thread is left intact.

## Decisions (approved)

- Promote target is a **full structured review thread**, not a draft (drafts can't thread).
- **Agent engagement is optional, ticked by default** — identical control/semantics to the
  ReviewPanel submit.
- **Local-only**: review threads only exist on worktrees. On remote PRs the per-answer "Promote to
  comment" → draft stays exactly as-is.
- **Keep the AI thread** after promoting (no auto-close / worktree removal).
- **Only answered turns** are promoted; in-flight/failed questions are skipped.

## Tasks

1. **DB**: add `NewReviewThreadComment` + `NewReviewThread.Comments`; append-in-order in
   `CreateReviewThreadsOnBranch` + `discussed` when an agent comment is present.
2. **API + validation**: add `reviewThreadDraftComment`/`Comments[]`, map through the create
   handler into the DB call, validate `author`.
3. **Regenerate clients**: `make api-generate` then `go generate ./internal/apiclient/generated`
   (stage all generated artifacts).
4. **Store**: extend `ReviewThreadDraftInput` + `createThreads` to forward `comments`.
5. **AIThreadCard**: the local-only "Promote to review thread" control + ticked-by-default agent
   checkbox + the anchor/comments mapping.

## Test plan

- **DB** (`queries_review_threads` test): `CreateReviewThreadsOnBranch` persists root + appended
  comments in order with correct authors, and sets `discussed` when an agent comment is present.
- **Go e2e** (`review_threads_e2e_test.go`): create with `comments[]` → thread has the root `user`
  comment followed by the appended comments in order/authors; invalid `author` → 400.
- **vitest** (`reviewThreads.svelte.test.ts`): `createThreads` forwards `comments` in the POST body.
- **vitest** (`AIThreadCard.test.ts`): promoting a 2-turn done session calls `createThreads` with
  the expected draft (anchor mapping), `[user Q1] + [agent A1, user Q2, agent A2]`, and the mode
  from the checkbox (default `act-immediately`; unticked → `undefined`); the control is hidden for
  `repoOwner !== "local"` and when there are no answered questions; in-flight/failed turns excluded.

## Out of scope

- Per-answer "promote single answer → review thread" (session-level only).
- Dedup / "already promoted" tracking (re-promote just creates another thread; deletable).
- Any change to the remote draft-comment path.
