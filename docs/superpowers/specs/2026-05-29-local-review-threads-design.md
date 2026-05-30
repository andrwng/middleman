# Local-worktree review threads + agent collaboration

Date: 2026-05-29
Status: Design — approved in brainstorm, pending spec review
Branch: `serve-local-repo-comments`

## Problem

Reviewing a **remote** PR, drafted inline comments persist as hideable
threads (synced from GitHub into `middleman_mr_events`, hidden via
`middleman_hidden_review_threads`).

Reviewing a **local worktree**, the same drafting UI is available, but on
"Submit review" the structured comments are flattened into a single
`review_feedback` turn string (`ReviewPanel.svelte` →
`compileReviewFeedback`) and handed to a one-shot Claude session that
immediately edits the code (`internal/aireview/sessions.go`,
`--allowedTools …,Edit,Write,Bash`). The comments are ephemeral — they
never become threads, can't be revisited, and aren't reachable by any
agent other than that one session.

We want three things:

1. **Persist local review comments as threads** — anchored to file/line,
   hideable, just like remote PRs.
2. **Make immediate action optional.** A submit-time mode picker chooses
   what happens next. The headline new mode is *discuss-first*: one
   review-wide Claude agent reads all the comments, replies inline in the
   threads, and edits code **only when told to ("Apply")**.
3. **Expose threads to an external shell agent** via REST and a thin MCP
   proxy, so a separate `claude` you run yourself can read and act on the
   same threads.

## Goals

- Local worktree review comments persist as anchored, hideable threads.
- Submit offers three modes: **discuss-first**, **act-immediately**,
  **persist-only**.
- One review-wide Claude session (shared `claude_session_id`) reasons
  across all threads, replies inline, and edits only on Apply.
- Per-thread lifecycle (`open → discussed → applied → resolved`) with a
  per-thread **Apply/Go** action and an **Apply all**.
- A thread REST API (used by the frontend and agents) plus a thin
  `middleman mcp` stdio proxy over REST, used by **both** the in-app agent
  and an external shell agent.
- Threads are the curated review surface; the activity window stays the
  agent's working log + free-text steering.

## Non-goals

- No change to the remote PR review/thread flow (GitHub posting, sync,
  `mr_events`, `hidden_review_threads`).
- No long-lived warm agent process. We keep the proven resume-per-turn
  model; a warm `--input-format stream-json` process is a documented
  future upgrade behind the same tools (see "Resume vs. warm").
- No GitHub posting for local threads — they are purely local.

## Key decisions (from brainstorm)

| Decision | Choice |
| --- | --- |
| Submit behavior | Always persist threads; mode picker selects what the agent does next. |
| Agent scope | **One** review-wide session reasons across all threads (not per-thread sessions like ask-claude). |
| Process model | **Resume per turn** (existing worktree-session machinery). Warm process deferred. |
| "Go"/Apply | Per-thread lifecycle + per-thread Apply + Apply all. Apply = "edit the code now." |
| Surfaces | Threads = curated replies; activity window = working log + steering. |
| Agent transport | REST API + thin **MCP proxy** (`middleman mcp`), both in v1, shared by in-app and external agents. |

## Building blocks we reuse

- **Synthetic MR per worktree** — `ensureSyntheticMRForWorktree` /
  `resolveOrEnsureMRID` (`internal/server/local_dispatch.go`) upsert a real
  `middleman_merge_requests` row keyed `(local-repo, worktree.id)`. AI
  threads, briefs, commit analyses and PR notes already FK onto it; review
  threads do the same.
- **Anchoring columns** — `middleman_ai_threads` (migration 000010) is the
  template: `path`, `anchor_side`, `anchor_line`, `hunk_start/end_line`,
  `selection_text`, `commit_sha`.
- **Worktree session runner** — `SessionRunner` / `SubmitTurn` / `runTurn`
  / `spawnTurn` / `buildSessionPrompt` / `ReconcileOnStartup`
  (`internal/aireview/sessions.go`), with `middleman_worktree_sessions` +
  `middleman_worktree_session_turns` (migration 000019).
- **Activity window** — `WorktreeConversation.svelte`,
  `worktreeSession.svelte.ts` (1.5s polling while a turn is queued/running),
  `SessionToolTimeline`.
- **Thread rendering** — the diff already renders inline thread cards
  (`AIThreadCard.svelte`, `PendingCommentsSection.svelte`) anchored on the
  worktree diff; review-thread cards follow the same shape.
- **Hide model** — `middleman_hidden_review_threads` (migration 000020)
  for the remote analogue.
- **Submit seam** — `ReviewPanel.svelte:91-119` (`onSubmit`, `isLocal`
  branch) and the inline draft array `draft.comments`
  (`path/line/startLine/side/body/commitSha/inReplyTo`).

## Data model

New migration `000021_add_review_threads` (verify the next free number at
implementation time — branches in flight may stack).

### `middleman_review_threads`

One row per review comment thread on a (local) merge request.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | INTEGER PK | local id; also the thread id used everywhere |
| `mr_id` | INTEGER NOT NULL | → `middleman_merge_requests(id)` ON DELETE CASCADE (the synthetic MR) |
| `path` | TEXT NOT NULL | file path |
| `side` | TEXT NOT NULL | `'LEFT'` \| `'RIGHT'` |
| `line` | INTEGER NOT NULL | anchor line |
| `start_line` | INTEGER | nullable; multi-line selection start |
| `commit_sha` | TEXT NOT NULL | worktree commit the comment was drafted against |
| `status` | TEXT NOT NULL DEFAULT `'open'` | `'open'` \| `'discussed'` \| `'applied'` \| `'resolved'` |
| `hidden_at` | DATETIME | nullable; non-null ⇒ hidden |
| `created_at` | DATETIME NOT NULL | |
| `updated_at` | DATETIME NOT NULL | bumped on new comment / status change |

Indexes: `(mr_id)`, `(mr_id, status)`.

### `middleman_review_thread_comments`

Comments (the root user comment + all replies) within a thread.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | INTEGER PK | |
| `thread_id` | INTEGER NOT NULL | → `middleman_review_threads(id)` ON DELETE CASCADE |
| `author` | TEXT NOT NULL | `'user'` \| `'agent'` |
| `body` | TEXT NOT NULL | markdown |
| `turn_id` | INTEGER | nullable; the `worktree_session_turns.id` that produced an agent reply (links a reply back to its working-log turn) |
| `created_at` | DATETIME NOT NULL | |

Index: `(thread_id)`.

The reviewer's drafted comment becomes the thread's first
`author='user'` comment. Agent replies are `author='agent'`. In-thread
user follow-ups are `author='user'`.

### Why a new table (not `mr_events` or `ai_threads`)

- `mr_events` is GitHub-shaped (`platform_id`, `in_reply_to` over platform
  ids) and **sync-only** — comments are pulled from GitHub, never authored
  locally. Authoring + a local lifecycle would overload it and risk the
  remote path.
- `ai_threads` is **Q&A** with a **per-thread `claude_session_id`**. Our
  threads are comment/reply with a status lifecycle and share **one**
  review session. Different enough to warrant their own tables — but we
  copy the anchoring columns verbatim.
- A dedicated table keeps the remote thread machinery untouched.

### Hide

`hidden_at` directly on the thread. Unlike the remote case, we own every
write locally, so the remote "a newer reply unhides the thread" staleness
logic isn't needed. The UI hide/show affordance still mirrors remote.

## The review session

All thread activity flows through the **existing worktree session** (one
`middleman_worktree_sessions` row per worktree, one `claude_session_id`).
No per-thread sessions. We do **not** change the turns schema; instead a
turn's `metadata_json` carries the structured intent:

```json
{ "action": "discuss" | "apply" | "steer", "thread_ids": [12, 13, 14] }
```

- **discuss** — kickoff (or re-discuss): reply to the listed threads, no
  edits.
- **apply** — make the code changes for the listed threads (or `"all"`).
- **steer** — free-text follow-up from the composer (no thread ids).

`runTurn` reads `action` to choose the prompt template **and the
`--allowedTools` set** (next section). Turn label in the activity window
derives from `action`.

### Tool-gating — the "only act on Go" guarantee

Because every turn is a fresh `claude -p --resume` with its own
`--allowedTools`, we gate tools by phase. This makes "discuss-first can't
touch code" a hard guarantee, not a prompt we hope the model honors:

- **discuss / steer turn**:
  `Read,Glob,Grep,mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread`
  — read-only + reply. **No** Edit/Write/Bash.
- **apply turn**: the above **plus** `Edit,Write,MultiEdit,Bash`.

(This is also why resume-per-turn is preferable to a warm process here: a
single long-lived process can't easily change its tool grants mid-stream.)

### Prompts (`buildSessionPrompt` extended)

- **discuss kickoff**: "You are reviewing this worktree. Here are N review
  comments as threads `[id, path:line, side, body, hunk context]`. For
  each, read the relevant code and `reply_to_thread` with your reading and
  a proposed approach or a clarifying question. Do not edit code."
- **apply (thread X)**: "Apply the change discussed in thread X `[thread
  comments]`. Make the edits, then `reply_to_thread` summarizing what
  changed."
- **apply all**: same over all `discussed` threads, sequencing interacting
  changes coherently; reply in each.
- First turn still primes worktree context (branch/base/head) as today.

### Status transitions (server-driven)

The server owns status based on the turn's `action` and outcome — the
agent only needs to reply:

- create ⇒ `open`
- discuss turn succeeds, thread got an agent reply ⇒ `discussed`
- apply turn succeeds for a thread ⇒ `applied`
- user resolves ⇒ `resolved`
- apply turn **fails** ⇒ thread stays `discussed`; error shown in the
  activity log (and optionally an `author='agent'` system note in-thread).

## Submit flow (the seam change)

`ReviewPanel.svelte` `onSubmit`, `isLocal` branch, replaces the
`compileReviewFeedback → submitTurn(review_feedback)` call with:

1. `POST …/review-threads` with the chosen **mode** and `draft.comments` —
   the server creates one thread per comment (+ its first `user` comment)
   in one transaction, then, by mode:
   - **persist-only** — returns; no session/turn.
   - **discuss-first** — ensures the worktree session and submits a
     `{action:"discuss", thread_ids:[…all new…]}` turn.
   - **act-immediately** — submits a `{action:"apply", thread_ids:"all"}`
     turn.
   `draft.body` (the review summary) rides on the kickoff turn, not as a
   thread. One round-trip; turn submission is server-side.
2. The client `diffStore.clearDraft()`s and closes the panel. Threads
   render inline on the diff; the activity window shows the agent working.

The mode picker is a split control on the Review button:
**Submit ▾ → Discuss / Apply now / Save only**.

Free-text from the composer submits `{action:"steer"}` and is **read-only
by default** — chatting never edits. Edits happen only through Apply
(per-thread / all). (Optional: an "apply mode" toggle on the composer for
power users — flagged for review, default off.)

## Frontend

- **`ReviewThreadCard.svelte`** (new, modeled on `AIThreadCard.svelte`):
  anchor + the user comment + agent/user replies, a status chip, a Hide
  toggle, a reply box, and an **Apply** button when status is
  `open`/`discussed`. Renders inline on the worktree diff like existing
  thread cards.
- **Review summary / toolbar**: **Apply all** + a hidden-threads filter.
- **Store** (`reviewThreads.svelte.ts`, modeled on the AI thread store):
  load threads, create from drafts, post a reply, hide/resolve, trigger
  apply. While a turn runs, reuse the session polling cadence to pick up
  new replies + status flips.
- Live worktree: when an apply turn edits files, the diff view (which reads
  the worktree) updates — the reviewer sees the fix land, then resolves /
  hides or replies for another round.

## REST API

PR-shaped paths, gated by `isLocalSource`; `mr_id` resolved via
`resolveOrEnsureMRID`. New types in `api_types.go`; regenerate with
`make api-generate`.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `…/pulls/{number}/review-threads` | list threads (+comments, status, hidden) |
| POST | `…/pulls/{number}/review-threads` | bulk-create from drafts; takes `mode`, submits the discuss/apply kickoff turn server-side |
| POST | `…/review-threads/{thread_id}/comments` | add a comment (`user` from UI, `agent` from MCP) |
| POST | `…/review-threads/{thread_id}/apply` | submit an apply turn for this thread |
| POST | `…/review-threads/apply-all` | submit an apply turn for all open/discussed threads |
| POST | `…/review-threads/{thread_id}/hide` / `…/unhide` | set/clear `hidden_at` |
| POST | `…/review-threads/{thread_id}/resolve` | status → `resolved` |

The `discuss` kickoff is folded into the bulk-create call when mode is
discuss-first. Apply/discuss endpoints internally call
`SessionRunner.SubmitTurn` with the right `action` metadata.

## MCP proxy (`middleman mcp`)

A new CLI subcommand `middleman mcp` starts a **stdio MCP server** that is
a thin HTTP client of the running middleman REST server (base URL from a
flag/env, default `http://127.0.0.1:8091`). REST stays the single source
of truth; MCP is ergonomic tool-calling on top.

Tools (each maps to one REST call):

- `list_threads` / `get_thread`
- `reply_to_thread(thread_id, body)` → POST `…/comments` (`author=agent`)
- `resolve_thread(thread_id)`, `hide_thread(thread_id)`
- read-only context helpers that proxy existing endpoints:
  `get_pull`, `get_diff` (so an agent can see the code)
- `apply_thread(thread_id)` — exposed for the external agent; the in-app
  agent doesn't need it (the human drives Apply).

**In-app use**: when `SessionRunner` spawns a discuss/apply turn it adds
`--mcp-config <generated>` pointing at `middleman mcp` and includes the
relevant `mcp__middleman__*` names in `--allowedTools`.

**External use**: the user registers it once
(`claude mcp add middleman -- middleman mcp`, or a project `.mcp.json`) and
their own `claude` gets the same tools.

Implementation note (flag for plan): implement the stdio JSON-RPC 2.0 loop
(`initialize`, `tools/list`, `tools/call`) hand-rolled to honor the
"prefer stdlib / minimal deps" convention, given the small tool set — vs.
adopting a minimal MCP Go SDK. Decide in the implementation plan.

Auth: loopback only, matching the existing server. Optional shared-token
env var as later hardening (flag for review; default none).

## Error handling

- **Turn failures**: existing `markFailed` + `ReconcileOnStartup` (orphaned
  turns → failed on restart; session stays resumable). A failed apply
  leaves the thread `discussed`.
- **Bulk thread create**: single transaction.
- **Concurrency**: serialize turns per session (one in-flight). If a turn
  is running and the user hits Apply, reject with "agent is busy" using the
  existing `hasRunningTurn` gate (or queue — decide in plan; reject is
  simpler for v1).
- **Worktree removed**: threads persist (FK to the MR row, which persists),
  but apply/diff context may be stale; guard via `resolveLocalWorktree`
  (already returns "no longer exists").
- **MCP proxy**: REST errors surface as MCP tool errors; a clear message if
  the server is unreachable.

## Testing (e2e non-negotiable)

- **DB**: thread + comment CRUD, status transitions, hide/unhide
  (`openTestDB`, shuffled).
- **API e2e** (generated client in `internal/apiclient`): create-from-drafts,
  list, reply, hide/resolve; the three submit modes; apply triggers a turn.
- **Runner** (`fakeClaude` harness, cf. `sessions_test.go`): assert
  `--allowedTools` has **no** Edit/Write/Bash on a discuss turn and **does**
  on an apply turn; status set on completion; reply tool routes to the
  right thread.
- **MCP proxy**: `tools/list`; `tools/call` mapping to REST against an
  `httptest` server; `reply_to_thread` creates an `agent` comment.
- **Frontend**: `ReviewThreadCard` rendering + Apply gating, mode picker,
  hide; store behavior under polling.

## Build phases (one spec, staged build order)

Each phase is independently testable; they are not separate specs.

1. **Persist + render.** Migration, thread tables, REST CRUD + hide/resolve,
   the submit seam in *persist-only* mode, `ReviewThreadCard` + store. End
   to end: drafts become hideable threads on the diff. No agent yet.
2. **Discuss / Apply.** Extend `SessionRunner`/`buildSessionPrompt` with the
   `action` metadata + per-phase tool-gating; discuss & apply endpoints;
   the in-app agent replies via the reply tool; per-thread Apply + Apply
   all; *discuss-first* and *act-immediately* modes.
3. **MCP proxy.** `middleman mcp` over REST; wire the in-app agent's spawn
   to use it; document the external-shell setup.

## Deferred / future

- Warm persistent agent process (`--input-format stream-json`) behind the
  same tools, if whole-review context reload latency ever bites.
- Token auth on REST/MCP.
- Optional composer "apply mode" for chat-driven edits.
