# Claude review suggestions

## Problem

Reviewing a PR with Claude today means one of three disconnected flows: Ask Claude (anchored Q&A), a PR brief, or, for local worktrees, review threads sent to the worktree agent. None of them produce review comments. To turn Claude's findings into comments the reviewer must copy text by hand into a draft, and there is no way to push back on a finding and have Claude refine the comment itself.

Draft review comments also live only in browser localStorage (`diff-draft-reviews`), so nothing server-side, including Claude, can create or edit them.

## Goal

Let the reviewer ask Claude to review a PR, optionally with instructions. Claude stages its findings as **suggestions**: inline comments anchored to diff lines, visible in the diff next to the reviewer's own drafts. For each suggestion the reviewer can:

- Chat with Claude about it (ask why, challenge it, ask for a rewrite).
- Have Claude revise the comment text from that conversation.
- Edit it by hand.
- Add it to drafts, after which it is an ordinary draft that publishes with the review.
- Discard it.

Claude never publishes anything. The reviewer is the only one who submits.

Works for GitHub PRs and local worktrees in v1.

## Non-goals

- No GitHub pending-review API. Drafts stay in middleman until the reviewer publishes, as today.
- No review-level chat that edits suggestions in bulk. Each suggestion has its own conversation.
- No SSE events for AI work. Polling, matching the existing AI stores.
- No configurable model, binary path, or timeouts (all hardcoded today; unchanged).
- Run history UI. Only the latest run per PR/branch is shown; older runs stay in the DB.

## Vocabulary

User-visible terms reuse what the UI already says ("Draft", "Pending", "Ask Claude", "publish your review"):

| Term | Meaning |
|---|---|
| Suggestion | A comment Claude proposed that the reviewer has not accepted. Never published. |
| Draft | A comment that will publish with the review. Either written by the reviewer, or a suggestion the reviewer added to drafts (shown with a Claude badge). |
| Review run | One "Review with Claude" invocation on a PR (or local branch). |

States: `suggestion`, `draft`, `discarded`. The only transitions:

- `suggestion` -> `draft`: reviewer clicks "Add to drafts".
- `suggestion` | `draft` -> `discarded`: reviewer discards. Soft; undo restores the previous state.
- `draft` -> `suggestion`: never. Nothing silently removes a comment from the review.

Reviewer-authored drafts are created directly in `draft`.

## Data model

One migration per delivery PR (see Delivery), following `context/db-migrations.md`:

- `000027_add_review_drafts` — `middleman_review_drafts`, `middleman_review_draft_meta` (PR 1)
- `000028_add_ai_review_runs` — `middleman_ai_review_runs` (PR 2)
- `000029_add_review_draft_messages` — `middleman_review_draft_messages` (PR 3)

```sql
CREATE TABLE middleman_ai_review_runs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    merge_request_id  INTEGER  NOT NULL,
    branch            TEXT     NOT NULL DEFAULT '',  -- local only
    head_sha          TEXT     NOT NULL,             -- anchor sha written to suggestions
    read_sha          TEXT     NOT NULL,             -- sha Claude reads: head_sha (GitHub) or snapshot (local)
    instructions      TEXT     NOT NULL DEFAULT '',
    claude_session_id TEXT     NOT NULL DEFAULT '',  -- root of every per-suggestion fork
    worktree_path     TEXT     NOT NULL DEFAULT '',
    status            TEXT     NOT NULL,             -- queued|running|done|failed|cancelled
    summary           TEXT     NOT NULL DEFAULT '',
    raw_json          TEXT     NOT NULL DEFAULT '',  -- stream events for the tool timeline
    error             TEXT     NOT NULL DEFAULT '',
    pid               INTEGER,
    created_at        DATETIME NOT NULL,
    started_at        DATETIME,
    completed_at      DATETIME,
    FOREIGN KEY (merge_request_id)
        REFERENCES middleman_merge_requests(id) ON DELETE CASCADE
);

CREATE TABLE middleman_review_drafts (
    id                TEXT PRIMARY KEY,                -- client-generated UUID
    merge_request_id  INTEGER  NOT NULL,
    branch            TEXT     NOT NULL DEFAULT '',  -- local only
    path              TEXT     NOT NULL,
    side              TEXT     NOT NULL,             -- LEFT|RIGHT
    line              INTEGER  NOT NULL,
    start_line        INTEGER,
    commit_sha        TEXT     NOT NULL,
    in_reply_to       INTEGER,                       -- GitHub comment id for replies
    body              TEXT     NOT NULL,
    origin            TEXT     NOT NULL,             -- user|claude
    state             TEXT     NOT NULL,             -- suggestion|draft|discarded
    discarded_from    TEXT     NOT NULL DEFAULT '',  -- state to restore on undo
    run_id            INTEGER,                       -- soft ref to ai_review_runs; null for user-origin
    claude_session_id TEXT     NOT NULL DEFAULT '',  -- this draft's chat session
    chat_worktree_path TEXT    NOT NULL DEFAULT '',  -- stable cwd for --resume
    submit_error      TEXT     NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    body_edited_at    DATETIME,                      -- last reviewer edit
    FOREIGN KEY (merge_request_id)
        REFERENCES middleman_merge_requests(id) ON DELETE CASCADE
);

CREATE TABLE middleman_review_draft_meta (
    merge_request_id INTEGER NOT NULL,
    branch           TEXT    NOT NULL DEFAULT '',
    body             TEXT    NOT NULL DEFAULT '',
    event            TEXT    NOT NULL DEFAULT '',   -- APPROVE|COMMENT|REQUEST_CHANGES
    updated_at       DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, branch),
    FOREIGN KEY (merge_request_id)
        REFERENCES middleman_merge_requests(id) ON DELETE CASCADE
);

CREATE TABLE middleman_review_draft_messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    draft_id     TEXT     NOT NULL,
    author       TEXT     NOT NULL,                 -- user|claude
    kind         TEXT     NOT NULL,                 -- message|revision
    body         TEXT     NOT NULL DEFAULT '',      -- message text, or revision's new body
    prev_body    TEXT     NOT NULL DEFAULT '',      -- revision only
    applied      BOOLEAN  NOT NULL DEFAULT 0,       -- revision only: applied vs pending suggestion
    status       TEXT     NOT NULL,                 -- queued|running|done|failed|cancelled
    raw_json     TEXT     NOT NULL DEFAULT '',
    error        TEXT     NOT NULL DEFAULT '',
    pid          INTEGER,
    created_at   DATETIME NOT NULL,
    started_at   DATETIME,
    completed_at DATETIME,
    FOREIGN KEY (draft_id)
        REFERENCES middleman_review_drafts(id) ON DELETE CASCADE
);
```

Notes:

- All timestamps UTC, emitted as RFC3339.
- `run_id` has no FK so PR 1 can ship before the runs table exists. Runs are only deleted by the MR cascade, which also removes their drafts, so no dangling refs arise.
- Local rows are scoped by `branch`, matching `middleman_review_threads` (migration 000023). Switching branches hides them without deleting them.
- **Outdated** is computed at read time, not stored: GitHub `commit_sha != PR head`; local `commit_sha != current HEAD`, the same rule working-tree review threads use.
- Claude's rationale for a suggestion is stored as the first chat message (`author=claude, kind=message`), not as a column.
- On a successful publish, the published drafts are deleted. The run row stays.

## Server-side drafts

Replaces the localStorage draft store for both targets. Behavior is unchanged for the reviewer.

Routes (`owner=local` handled by the same routes, as review threads are):

| Method | Path | Purpose |
|---|---|---|
| GET | `/repos/{o}/{n}/pulls/{num}/review-drafts` | drafts + suggestions (+ discarded with `?include=discarded`), meta, latest run |
| POST | `.../review-drafts` | create user draft (state `draft`); accepts a batch for the one-time import |
| PATCH | `.../review-drafts/{id}` | edit body (sets `body_edited_at`), change state |
| DELETE | `.../review-drafts/{id}` | hard delete (user drafts only; suggestions are discarded) |
| PUT | `.../review-draft-meta` | review body and event |

A new `POST .../review-drafts/publish {event, body, commit_id?}` reads `state=draft` rows from the DB and posts them using the same inline-comment core as `submitReview` (`internal/server/huma_routes.go:1208`), which stays for compatibility. Each draft is deleted as soon as GitHub accepts it, so on partial failure only unposted drafts remain, the failing one with `submit_error` set. Local publish stays frontend-driven until PR 4 (drafts to review threads, `ReviewPanel.svelte:87-122`, keeping its `discuss-first` / `act-immediately` / persist-only modes).

Frontend: new `reviewDrafts.svelte.ts` store, API-backed with optimistic updates. The draft functions in `diff.svelte.ts` (`addDraftComment`, `removeDraftComment`, `updateDraftCommentBody`, `setDraftBody`, `setDraftEvent`) delegate to it so callers do not change. On first load per PR the store posts any localStorage drafts as a batch, then deletes the key.

## Review runs

### Starting

`POST /repos/{o}/{n}/pulls/{num}/ai-review-runs {instructions?}`. One active run per PR (or local branch); a second request returns 409. `GET .../ai-review-runs/latest` returns the latest run. `POST .../ai-review-runs/{id}/cancel` cancels it.

Implementation: new `internal/aireview/reviewruns.go`. The stream-json parsing and process-group kill in `sessions.go` move into `helpers.go`, shared by both runners.

### Worktree

- GitHub: detached worktree from the bare clone at the PR head SHA, as briefs do. `head_sha = read_sha = PR head`.
- Local: snapshot the live tree without touching the reviewer's index or files: temp `GIT_INDEX_FILE`, `git add -A`, `git write-tree`, `git commit-tree -p HEAD`. Pin it with `refs/middleman/review-snapshots/<run_id>` so gc keeps it. Detached worktree at the snapshot. `read_sha = snapshot`, `head_sha = HEAD` at run start, so suggestions anchor exactly as working-tree comments do today and do not read as outdated.
- Path is deterministic: `<ai-review>/run-<id>`. Claude Code stores sessions per cwd, so every fork of the run's session must run there.
- The worktree is removed when the run goes idle and recreated at the same path on demand. The snapshot ref is deleted when the run's last non-discarded suggestion is published or discarded, or the PR closes.

### Invocation

```
claude -p <prompt> --output-format stream-json --verbose
  --permission-mode bypassPermissions
  --mcp-config <tmp> --strict-mcp-config
  --allowedTools Read,Glob,Grep,mcp__middleman__propose_comment,mcp__middleman__list_drafts,mcp__middleman__get_pull
  --disallowedTools Edit,Write,NotebookEdit,Bash,Agent
```

Prompt contains: PR title and body, base..head file list, the reviewer's instructions, and the directive to call `propose_comment` once per finding and end with a short summary (stored in `summary`). Existing drafts, suggestions, and published comments are reachable through `list_drafts`; the prompt tells Claude not to duplicate them. Re-runs are additive.

Timeout 15 minutes, then the process group is killed. Cancel uses the same kill. On startup, runs and messages still `running` are marked failed (same pattern as `Runner.ReconcileOnStartup`).

### MCP tools

`middleman mcp` gains `--run <id>` and `--draft <id>` flags that select the tool set. Tools stay thin proxies over the loopback REST API (`internal/mcp/tools.go`).

| Mode | Tool | Effect |
|---|---|---|
| run | `propose_comment(path, side, line, start_line?, body, rationale)` | insert draft `origin=claude, state=suggestion`; rationale becomes its first chat message |
| run | `list_drafts` | drafts, live suggestions, published review comments |
| run, draft | `get_pull` | PR metadata |
| draft | `get_draft` | current body, state, anchor, outdated flag |
| draft | `revise_comment(body)` | revise this draft only (enforced server-side) |

The server validates every anchor against the diff at `read_sha`. An anchor outside a hunk is rejected with an error naming the valid ranges for that file, so Claude can retry. Rejections appear in the run's tool timeline.

## Chat on a draft

`POST /repos/{o}/{n}/pulls/{num}/review-drafts/{id}/messages {body}`; `POST .../messages/{mid}/cancel`. Revisions: `POST .../messages/{mid}/apply` and `POST .../messages/{mid}/revert`.

Sessions:

- Claude-origin: first message runs `--resume <run.claude_session_id> --fork-session` in the run's worktree; the new session id is stored on the draft. Later messages `--resume` the draft's own session. Suggestions never discussed cost nothing.
- User-origin: first message starts a fresh session seeded with PR context and the draft. cwd: GitHub `<ai-review>/draft-<id>` at `commit_sha`; local, the reviewer's worktree (matching Ask Claude on local).
- Every turn's prompt opens with which draft is under discussion (anchor and current body), and, if the reviewer edited the body since the last turn, the new text.
- Prompt rule: answer questions without revising; call `revise_comment` only when the reviewer asks for a change.

Revision rules (`revise_comment` writes a `kind=revision` message):

- Draft is a `suggestion` and the reviewer has not edited it since the turn started: applied immediately (`applied=1`); the chat offers Revert.
- Draft is a `draft`, or the reviewer edited it during the turn: not applied (`applied=0`); the chat shows old/new with Apply. Text the reviewer accepted only changes by the reviewer's hand.
- Draft is `discarded`: rejected.

Concurrency: turns on one draft run FIFO. Globally at most 3 `claude` processes for chats; turns past the cap show as queued.

## UI

All in the diff view; identical for GitHub and local.

- **Entry**: `DiffToolbar` gets "Review with Claude", opening a popover with an optional instructions textarea and Start. While a run is active it becomes a status pill (elapsed, suggestions so far, Cancel).
- **Sidebar**: `DiffSidebar` gets a "Claude review" section above `PendingCommentsSection` showing the latest run only: status, instructions, live tool timeline (`SessionToolTimeline`), summary, and Re-run (prefilled with the last instructions).
- **Suggestion card** (new `SuggestionCard.svelte`): in the existing card stack (`gutterStack.ts`) in `DiffFile` and `CommentGutter`. Claude badge, dashed border. Shows the current body and an outdated badge when applicable. Actions: Add to drafts, Edit, Ask Claude, Discard.
- **Chat**: expands inline under the card, reusing `AIThreadCard`'s layout and keyboard handling. The first message is Claude's rationale. Revision entries show old/new with Revert or Apply.
- **Drafts**: Claude-origin drafts render as `PendingCommentCard` with a Claude badge. Every draft (both origins) gets Ask Claude, which opens the same chat.
- **Pending comments section**: splits into Suggestions (n) and Drafts (n); jump-to via `jumpToDraft.ts`. Bulk "Add all to drafts" and "Discard all suggestions". Discards show an undo toast.
- **Publish (`ReviewPanel`)**: counts drafts only. Non-blocking notices "N suggestions not added" and "M drafts outdated", each with a jump link. Per-draft `submit_error` shown on the card after a partial failure.
- **Stores**: `reviewDrafts.svelte.ts` (above) and `aiReviewRun.svelte.ts`. Both poll at 1.5s only while a run or chat turn is active.

## Error handling

| Case | Behavior |
|---|---|
| `claude` not on PATH | Run fails immediately: "claude not found in PATH". |
| Crash, timeout, cancel mid-run | Suggestions already created stay. Run marked failed with error and "partial: N suggestions". |
| Anchor outside diff | MCP error to Claude; nothing stored unless it retries. |
| Fork/resume fails (session gone) | Turn fails with "review session unavailable" and offers "Start fresh chat" (user-origin path). |
| Worktree cannot be recreated (GitHub head SHA gone after force-push) | Chat disabled with that reason; draft remains editable and publishable. Local cannot hit this (snapshot ref). |
| Reviewer edits during a Claude revision | Revision stored unapplied with Apply. |
| `revise_comment` on discarded draft | Rejected. |
| Partial publish failure | Posted drafts deleted; others keep `submit_error`. |
| PR closed or merged | Active runs and turns cancelled, worktrees and snapshot refs removed, drafts kept. |
| Server restart | Running runs and messages marked failed. |

## Testing

Per CLAUDE.md: testify, `-shuffle=on`, generated API client for API tests, `openTestDB(t)`, `t.TempDir()`.

- **Fake `claude`**: Go helper binary (re-exec pattern) swapped in with `SetBinaryForTest`. It parses `--mcp-config`, spawns the real `middleman mcp` server from it, issues scripted `tools/call` requests, emits stream-json, and records argv so tests assert `--fork-session` and `--resume <id>`.
- **DB**: table-driven state transitions (including the absence of `draft` -> `suggestion`), discard/undo, outdated computation, branch scoping.
- **Anchor validation**: table-driven: RIGHT/LEFT, multi-line ranges, outside hunks, deleted and renamed files.
- **Snapshot**: captures staged, unstaged, and untracked changes; `git status --porcelain` and the index are identical before and after; snapshot ref created and removed.
- **MCP**: extend `tools_test.go`; draft mode cannot read or revise other drafts.
- **Revision rules**: table-driven over draft state x reviewer-edited-during-turn.
- **E2E** (`internal/server`, real SQLite):
  - GitHub: start run, suggestions appear, chat forks, revise, add to drafts, publish (extend `testutil/fixture_client.go` with working `CreateInlineComment`).
  - Local: same flow, publish creates review threads; suggestions are not outdated on creation.
  - 409 on concurrent run; restart recovery; localStorage batch import; partial publish failure; user-origin draft chat.
- **Frontend**: Vitest for `SuggestionCard`, the chat, `reviewDrafts` and `aiReviewRun` stores, and the `diff.svelte.ts` delegation. One Playwright flow against the fake binary.

## Delivery

Four PRs, all in v1:

1. Server-side drafts: migration (drafts + meta tables), routes, `submitReview` reading from DB, store delegation, localStorage import. No AI; no visible change.
2. Review runs: runs table, `reviewruns.go`, `propose_comment` / `list_drafts`, anchor validation, suggestion cards, sidebar section, toolbar entry. GitHub only.
3. Chat: messages table, forks, `revise_comment`, revision rules, chat UI, Ask Claude on all drafts.
4. Local target: snapshot commit and ref lifecycle, local anchoring, local publish to review threads.

Each PR carries its own e2e tests.
