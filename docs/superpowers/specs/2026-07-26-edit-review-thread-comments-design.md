# Editing Published Review-Thread Comments — Design

Date: 2026-07-26
Status: Approved (design)
Builds on:

- The local review-threads feature (`middleman_review_threads` +
  `middleman_review_thread_comments`, migrations 000021/000022;
  `reviewThreadsStore`, `ReviewThreadCard`, `internal/server/huma_routes_review_threads.go`).
- The worktree doc-review gutter, where these threads render
  (`RenderedMarkdownView` → `CommentGutter` → `ReviewThreadCard variant="gutter"`),
  and the diff view, where they also render (`variant="inline"`).

## Problem

Once a review-thread comment is posted it is immutable — the store exposes
`addComment`/`deleteThread`/`resolve`/etc. but no edit, the server has no edit
route, and `ReviewThreadCard` renders every comment body read-only. A reviewer
who wants to fix a typo or reword a comment after the fact has no option short
of deleting the whole thread and recreating it.

## Goal

Let a reviewer edit their **own** review-thread comments in place, persisting to
the local SQLite store, with a small "(edited)" marker so a changed comment is
visibly changed (these threads are read by the Claude agent, so silent edits
could mislead).

## Decisions (user-approved)

- **Scope: only the reviewer's own comments** (`author == "user"`) are editable
  — the thread's root comment and any user replies. Agent (`"agent"`) replies
  stay read-only; editing them would misrepresent what the agent actually said.
- **Show an "(edited)" marker** on a comment that has been edited. This requires
  a new nullable `edited_at` timestamp on the comment row (chosen over a bare
  boolean so the marker can later carry "when").

## What already exists (reused, not rebuilt)

- **Comment table** `middleman_review_thread_comments` (migration
  `000021_add_review_threads.up.sql`): `id, thread_id, author, body, created_at`,
  plus `sent_to_agent` (`000022`). Highest migration on the branch is `000023`,
  so the new one is `000024`.
- **DB layer** `internal/db/queries_review_threads.go`: `ReviewThreadComment`
  struct (`ID, ThreadID, Author, Body, TurnID, CreatedAt, SentToAgent`),
  `AddReviewThreadComment`, `ListReviewThreadComments`. No update function.
- **Server** `internal/server/huma_routes_review_threads.go`: `huma.Post` create,
  `huma.Post .../{thread_id}/comments` (add), `huma.Delete .../{thread_id}`.
  `reviewThreadCommentResponse` = `{id, author, body, sent_to_agent, created_at}`.
  Comment handlers return `reviewThreadOutput` (the updated thread).
- **Store** `packages/ui/src/stores/reviewThreads.svelte.ts`: mutators call
  `client.<verb>(...)` then `upsert(updatedThread)` on success; on error set
  `error` and return `false`. `addComment(threadID, body, author?)` is the
  pattern to mirror.
- **Card** `packages/ui/src/components/diff/ReviewThreadCard.svelte`: renders
  each comment as an author label (`"You"`/`"Claude"`) + `sent_to_agent` "asked"
  badge + `{@html renderMarkdown(c.body)}`. Header actions
  (Apply/Resolve/Hide/Delete) are the styling precedent; the reply composer is
  the textarea styling precedent.

## Design

### 1. DB — migration `000024` + one update query

- `000024_add_review_thread_comment_edited_at.up.sql`:
  `ALTER TABLE middleman_review_thread_comments ADD COLUMN edited_at DATETIME;`
  (nullable, no default; `NULL` = never edited). `.down.sql` drops the column.
- Add `EditedAt *time.Time` to the `ReviewThreadComment` struct and include
  `edited_at` in the `ListReviewThreadComments` SELECT (and any other SELECT that
  builds a `ReviewThreadComment`).
- `UpdateReviewThreadComment(ctx, commentID int64, newBody string) (ReviewThreadComment, error)`:
  `UPDATE middleman_review_thread_comments SET body = ?, edited_at = datetime('now') WHERE id = ? AND author = 'user'`,
  then return the updated row. If no row matched (wrong id, or the comment is
  an agent comment), return a sentinel the handler maps to 403/404 — the
  `author = 'user'` guard is enforced in SQL so an agent comment can never be
  edited even if the id is valid.

### 2. Server — `POST .../review-threads/{thread_id}/comments/{comment_id}/edit`

- Register `huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments/{comment_id}/edit", s.editReviewThreadComment)`.
  POST (not PATCH) keeps the edit consistent with the existing all-POST mutation
  routes (`resolve`/`hide`/`ask`/`apply` are all `POST …/{thread_id}/{action}`);
  no new REST verb enters the API. The edit still persists server-side — the
  comment lives in SQLite — but that server touch is one small POST handler; the
  substance of the feature is the card UI.
- Input: path `{owner, name, number, thread_id, comment_id}` + body `{ body string }`.
- Handler `editReviewThreadComment`:
  1. Resolve the MR (same owner/name/number → id path the other handlers use).
  2. Reject empty/whitespace-only body → **422**.
  3. Call `UpdateReviewThreadComment(ctx, comment_id, body)`; the SQL guard
     (`author = 'user'`) means a non-existent id **or** an agent comment updates
     0 rows → map to **404** (no editable comment with that id). One status
     covers both cases — the UI only shows Edit on the reviewer's own comments,
     so this path is purely defensive and need not distinguish them.
  4. Return the updated thread as `reviewThreadOutput` (reload the thread's
     comments and build the response, exactly like `addReviewThreadComment`).
- Add `EditedAt *string json:"edited_at,omitempty"` (UTC RFC3339, or omitted when
  `NULL`) to `reviewThreadCommentResponse`, populated wherever the response is
  built (list + add + edit).
- Regenerate API artifacts (`make api-generate`) so the OpenAPI spec, the
  generated Go client, and the frontend client types pick up the new route and
  the `edited_at` field.

### 3. Store — `editComment`

```
async function editComment(
  threadID: number, commentID: number, body: string,
): Promise<boolean>
```

Mirrors `addComment`: `client.POST(".../comments/{comment_id}/edit", { params:{ path:{ owner, name, number, thread_id: threadID, comment_id: commentID } }, body:{ body } })`;
on success `upsert(data)` (the returned updated thread) and return `true`; on
error set `error` and return `false`. Export it from the store object.

### 4. Card — inline edit + "(edited)" marker

- Ensure the comment type carries `edited_at` so the card can read it — it flows
  from the regenerated client response type via `make api-generate`; add it
  explicitly if the store hand-declares the comment shape.
- In the `{#each comments}` block, for a comment with `c.author === "user"`,
  render a small **Edit** affordance (hover-revealed, styled like the header
  `review-thread__action` buttons). Agent comments render no Edit.
- Editing state is per-comment (keyed by `c.id`): clicking Edit swaps that
  comment's rendered body for a `<textarea>` prefilled with `c.body` plus
  **Save** / **Cancel** buttons, styled like the existing reply composer.
  - **Save** is disabled when the trimmed text is empty or unchanged. On click →
    `reviewThreads.editComment(thread.id, c.id, text)`; on success collapse back
    to the rendered (now-updated) body; on failure stay in edit mode with the
    text intact (the store's `error` surfaces).
  - **Cancel** discards and restores the rendered body.
  - Only one comment is in edit mode at a time (opening Edit on another closes
    the first).
- When `c.edited_at` is set (truthy — a non-empty RFC3339 string), show a small
  `(edited)` marker beside the author label, next to the existing "asked" badge
  (a muted `review-thread__edited` span). Use a truthy check, not `!== null`, so
  an omitted/undefined field reads as "not edited".

## Settled defaults

- **Sent-to-agent comments are still editable.** Editing a comment already sent
  to Claude (the "asked" badge) updates its body and marks it "(edited)" but does
  **not** re-send it or start an agent turn. The marker keeps the change visible.
- **Any thread status.** Edit is available on user comments regardless of thread
  status (open/discussed/applied/resolved), even when the reply composer is
  hidden (resolved). Hidden threads render only a stub (no comments), so Edit is
  not reachable there.
- **No per-comment delete.** Out of scope; thread-level Delete already exists.
  Editing to an empty body is blocked (use Delete to remove a thread).

## Data flow

1. Reviewer clicks Edit on their comment → card enters edit mode for that
   `c.id`, textarea prefilled with `c.body`.
2. Save → `editComment(threadID, commentID, text)` → `POST …/comments/{id}/edit`.
3. Server validates (non-empty, user-authored) → `UpdateReviewThreadComment`
   sets `body` + `edited_at` → returns the updated thread.
4. Store `upsert`s the thread → the card re-renders the comment with the new
   body and the "(edited)" marker; edit mode collapses.

## Error handling

- Fail-soft, matching the other mutators. On any API error the store sets
  `error` and returns `false`; the card **stays in edit mode with the typed text
  intact** — an edit is never lost to a failed request.
- 422 (empty body) is also guarded client-side (Save disabled), so it is a
  belt-and-suspenders server check.
- 404 (no editable comment with that id — non-existent, or an agent comment)
  cannot normally occur from the UI (Edit is only shown on user comments) but is
  enforced server-side regardless (the `author = 'user'` SQL guard).

## Non-goals

- Editing agent (Claude) replies.
- Per-comment delete, or editing a comment's anchor/line/side.
- Re-sending an edited comment to the agent, or any diff/version history of edits
  beyond the single "(edited)" marker.
- Propagating edits anywhere outside the local SQLite store (these are local
  review threads, not GitHub PR comments).
- Editing the thread's non-body attributes (status/hidden/etc. already have their
  own actions).

## Edge cases

- **Empty/whitespace body** → Save disabled (client) + 422 (server); body
  unchanged.
- **Unchanged body** → Save disabled; no request.
- **Concurrent thread refresh** (the store polls) landing mid-edit → the open
  textarea's local state is not clobbered (edit state is component-local, keyed
  by `c.id`); `upsert` only refreshes the rendered bodies of comments not in edit
  mode.
- **Comment deleted out from under an open editor** (thread deleted) → the card
  unmounts; the pending edit request, if any, resolves to an error handled fail-soft.
- **Markdown body** → same `renderMarkdown` path as today once collapsed.

## Testing

- **DB** (`queries_review_threads` test): `UpdateReviewThreadComment` sets
  `body` + a non-null `edited_at`; updating a non-existent id or an `agent`
  comment updates 0 rows / returns the not-found sentinel; `ListReviewThreadComments`
  round-trips `edited_at`.
- **Server e2e** (generated client, real SQLite): edit a user comment → 200,
  body changed, `edited_at` populated in the returned thread; edit an agent
  comment → 404, body unchanged; edit with an empty body → 422; edit an unknown
  comment_id → 404.
- **Store** (vitest): `editComment` POSTs the edit and `upsert`s the returned thread on
  success; sets `error` and returns `false` on failure.
- **Card** (vitest): Edit affordance shows only on `author === "user"` comments;
  entering edit mode prefills the textarea; Save calls `editComment` and renders
  the updated body + "(edited)"; Cancel restores; Save disabled when empty or
  unchanged.
- **Playwright** (extend `worktree-doc-review.spec.ts`): create/persist a review
  thread in the gutter, edit its comment, assert the body updates and "(edited)"
  appears; the mock review-threads store must honor the edit POST.
- **Green bars:** `go test ./... -short`, `make frontend-check` 0/0, vitest,
  doc-review e2e.

## Open questions / future

- Show "(edited) <relative time>" via a tooltip on `edited_at` (data is already
  there).
- Per-comment delete, if it turns out to be wanted alongside edit.
- Extend edit to the diff-view rendering of these threads if that surface grows
  its own affordances (same `ReviewThreadCard`, so it comes for free).
