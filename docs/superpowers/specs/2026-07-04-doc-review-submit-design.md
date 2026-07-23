# Doc-Review Submit — Design

Date: 2026-07-04
Status: Approved (design)
Builds on:

- The merged worktree doc-review pane (`DocReviewSurface` → `RenderedMarkdownView`
  in `commentLayout="gutter"` mode).
- The merged local review-threads / agent-session machinery ("branch-scoped
  reviews").

## Problem

The doc-review pane lets you add review comments on a rendered markdown doc, but
there is no way to **submit** them from the doc pane. The comments are stranded:

- They are client-side `localStorage` drafts (`diffStore.draftReviews`), never
  persisted server-side.
- They will not appear in the diff view, because a living doc usually is not part
  of a PR diff — so the diff view's "Review" button is not a usable path for them.

So a doc reviewer can annotate a doc but cannot persist those notes, hand them to
Claude to act on, or ever see them again.

## Goal

Add a **Review** action to the doc pane that submits the doc's pending comments
exactly the way the diff view does:

- **Save** them as branch-scoped review threads in SQLite, or
- **Act**: additionally hand them to Claude, which edits the worktree in place —
  the edits surface as the live branch diff.

Submitted threads then render in the doc gutter as first-class, actionable cards
(Apply / Discuss / Ask + live agent replies), so doc comments become durable and
accessible right where they were written.

## What already exists (reused, not rebuilt)

The entire submit + thread + agent engine is present on `main` and is
local-worktree-scoped. This feature reuses it; it does not change the data model
or the agent engine.

- **Doc comments already land in the shared draft store.**
  `RenderedMarkdownView.saveDraft()` calls `diffStore.addDraftComment({ path,
  line, side, startLine?, commitSha: "WORKING-TREE", body })`. Drafts are keyed by
  the worktree PR identity `owner/name#number` (`draftKey()` in
  `stores/diff.svelte.ts`), persisted to `localStorage` under
  `"diff-draft-reviews"`. `DocReviewSurface` sets that identity via
  `diffStore.setActivePR(owner, name, number)` on mount.
- **The local submit path already persists + optionally acts.**
  `ReviewPanel.onSubmit()` (local branch, `owner === "local"`) reads
  `diffStore.getDraft().comments`, maps root comments to thread drafts, and calls
  `reviewThreadsStore.createThreads(drafts, engageAgent ? "act-immediately" :
  undefined)`. The panel's local UI is exactly the two modes we want: a single
  checkbox *"Have Claude apply these changes"* (checked → `act-immediately`,
  unchecked → persist-only). → `POST /repos/{owner}/{name}/pulls/{number}/review-threads`
  → `createReviewThreads` (`internal/server/huma_routes_review_threads.go`) →
  `db.CreateReviewThreadsOnBranch` (branch-scoped rows in
  `middleman_review_threads` + `middleman_review_thread_comments`).
- **The action engine already produces the branch diff.** For `act-immediately`,
  `kickoffReviewTurn(..., "apply", ...)` → `SessionRunner.SubmitTurn` → a one-shot
  `claude -p … --permission-mode bypassPermissions` spawned with
  `cmd.Dir = worktreePath`, MCP config exposing the `middleman mcp` tools. Claude
  edits files **in place** (no separate worktree, no commit); the uncommitted
  edits are the live `git diff` / the synthetic "Uncommitted changes"
  (`WORKING-TREE`) entry.
- **Scoping.** `owner="local"`, `name`=repo, `number`=worktree row id.
  `resolveLocalWorktree(name, number)` → worktree; `ensureSyntheticMRForWorktree`
  → the synthetic MR the threads FK to; the branch is resolved live via
  `currentWorktreeBranch`. Sessions are keyed `(worktree_id, branch, active)`.
- **Live updates.** The app polls `reviewThreadsStore.refresh()` every ~1.5s while
  a worktree turn is running (`Provider.svelte`), and the session conversation
  store polls the session, so status flips (`applied`/`discussed`) and agent
  replies land live.
- **Thread UI to reuse.** `reviewThreads` store (`stores/reviewThreads.svelte.ts`:
  `createThreads`, `apply`, `applyAll`, `discuss`, `ask`, `refresh`, …),
  `ReviewThreadCard.svelte`, `ReviewThreadsSection.svelte`.

## Design (three additions)

### 1. Submit UI in the doc pane

- Add a **Review** control to the `DocReviewSurface` header (alongside the back
  button, path label, and new-tab link), showing a pending-comment count.
- Clicking opens the **existing** `ReviewPanel` (mounted from `DocReviewSurface`,
  the way `DiffView` mounts it), passing `owner`/`name`/`number`. Its local mode
  already renders the two modes; no new panel or submit logic is written.
- On submit, `ReviewPanel` already calls `reviewThreadsStore.createThreads(...)`.
  Because doc drafts are in the shared `diffStore` bucket, they are picked up with
  no store change.
- **Submit scope:** submits *all* pending comments in this worktree's draft bucket
  (across docs + any diff drafts), matching the diff view's "finish review". The
  drafts are a single per-worktree bucket, and a review is per-branch, so this is
  the natural model. The pending count reflects that bucket.

### 2. `WORKING-TREE` sentinel → HEAD (server)

- In `createReviewThreads` the per-thread `commit_sha` is canonicalized: an empty
  value resolves to HEAD, a non-empty value is peeled via `ResolveCommitSHA` and
  kept verbatim on failure. `WORKING-TREE` is non-empty and does not resolve, so
  today it is stored verbatim — a non-commit anchor.
- Change: treat the `WORKING-TREE` sentinel like the empty case → resolve to HEAD.
  Doc threads get a real commit anchor consistent with the rest of the system.
- This does **not** affect doc-pane placement: the gutter matches threads to
  blocks by **line** (via `anchorOverlapsBlock`), not by sha. HEAD == the rendered
  working-tree content in the common "living doc, unchanged vs HEAD" case; when the
  worktree has uncommitted edits the line-based match still places the card
  correctly. A well-formed sha also lets the thread surface in the diff view if the
  file happens to be in a diff.

### 3. Render review threads in the doc gutter (approach B — gutter-native card)

- `RenderedMarkdownView` adds `reviewThreads` to its `getStores()` destructure and
  adds review threads (for the current `path`, matched by line) as a source in
  `cardsForRange`, alongside drafts, GitHub-published comments, and AI threads.
- A **gutter-native review-thread card** (new component, e.g.
  `ReviewThreadGutterCard.svelte`, reusing `ReviewThreadCard`'s internals/actions)
  renders through the existing `CommentGutter` card set. It supports per-thread
  **Apply / Discuss / Ask** (calling `reviewThreadsStore.apply/discuss/ask`) and
  renders the comment list including agent replies.
- **Draft → thread transition:** on submit, `ReviewPanel` clears the draft (draft
  card disappears) and `reviewThreadsStore` reloads (thread card appears) — the same
  transition the diff view shows.
- **Live:** `DocReviewSurface` already calls `reviewThreadsStore.load()` on mount
  (currently unused for rendering; now it feeds the gutter), and the existing
  1.5s poll updates status + agent replies in the doc gutter too.

## Data flow

1. Add comments in the doc gutter → `diffStore` drafts (existing).
2. Click **Review** → `ReviewPanel` → `createThreads(drafts, act-immediately |
   persist-only)`.
3. Server `createReviewThreads` → `CreateReviewThreadsOnBranch` (branch-scoped;
   sha → HEAD) → SQLite. If `act-immediately`: `kickoffReviewTurn("apply")` →
   `SubmitTurn` → `claude -p` edits the worktree.
4. Doc gutter: draft cards → thread cards; the poll surfaces agent replies and
   status; the agent's edits show as the live branch diff (viewable in the
   commits/diff view as the "Uncommitted changes" entry).

## Decisions

- **Submit scope** = all pending comments in the worktree (matches the diff view).
- **Sha** = HEAD (translate the `WORKING-TREE` sentinel server-side).
- **Gutter card** = approach B (gutter-native card reusing `ReviewThreadCard`
  internals).
- **Modes** = the existing two (`act-immediately` / persist-only). No
  `discuss-first` in the doc-pane UI (matches the current diff UI; the mode still
  exists at the API layer).
- **Action security** — the apply agent runs with
  `--permission-mode bypassPermissions` inside the worktree. This is **existing,
  inherited** behavior (not introduced here); noted for reviewers.

## Non-goals

- Changing the review-thread data model, the agent/session engine, or the
  diff-view review flow.
- GitHub PR review submission from the doc pane (docs are local-worktree only).
- Committing or pushing the agent's changes (the engine intentionally leaves
  uncommitted worktree edits).
- A cross-doc "review overview" across a worktree (YAGNI for now).
- `discuss-first` mode in the doc-pane UI.

## Edge cases / error handling

- **Empty review** (no pending comments): the Review button is disabled;
  `ReviewPanel`'s existing `canSubmit` guard applies.
- **HEAD unresolvable** (worktree with no commits / detached): keep the existing
  `ResolveCommitSHA` fallback behavior — if HEAD cannot resolve, store the value
  as the empty/HEAD path already does today (do not regress non-doc reviews).
- **Doc changed after submit** (a thread's line no longer maps to a block): reuse
  `RenderedMarkdownView`'s existing outdated handling (the outdated-comment banner)
  so the thread is surfaced as outdated rather than dropped.
- **Non-local owner:** the whole flow is local-only (`reviewThreads` store
  early-returns for non-local; the doc pane only appears for local worktrees) — no
  change.
- **Turn already running:** submit while a turn runs is serialized by the existing
  per-session FIFO (`SubmitTurn`); the Review action may reflect a busy state
  (optional polish, not required).

## Testing

- **Server (Go):** `createReviewThreads` translates a `WORKING-TREE` thread sha to
  HEAD; `persist-only` saves without a turn while `act-immediately` kicks one.
  Exercise through the `review-threads` route with a `WORKING-TREE` sha (prefer the
  generated API client).
- **Frontend (vitest):** `RenderedMarkdownView` renders a review thread as a gutter
  card (`cardsForRange` includes `reviewThreads`); the draft → thread transition;
  the Review button shows the pending count and opens `ReviewPanel`.
- **e2e (Playwright, mocked):** in the doc pane, add a comment → Review →
  (persist-only) the thread persists and renders as a gutter card; (act-immediately)
  a turn is kicked (session mocked) and the thread reflects applied / an agent
  reply. Reuse the `tests/e2e/support/mockApi.ts` harness; add review-threads +
  session mocks.
- **Green bars:** `go test ./... -short`, `make frontend-check` 0/0, vitest,
  doc-review e2e.

## Open questions / future

- Show, in the doc gutter, review threads that were created from the **diff view**
  on the same file (natural — same store, matched by path + line; likely yes).
- A per-worktree "review summary" across docs (deferred).
- Committing the agent's edits from the UI (deferred; the engine leaves them
  uncommitted by design).
