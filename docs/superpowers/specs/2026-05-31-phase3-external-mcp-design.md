# Phase 3 — External-Shell MCP (cwd-default) + Branch-Scoped Reviews — Design

**Date:** 2026-05-31
**Status:** Approved (ready for implementation plan)
**Branch:** `serve-local-repo-comments` (stacked on the unmerged prior phases)
**Depends on:** Phases 1a/1b/2a/2b + Fix A + Phase B (conversational threads) — all on this branch.
**Realizes:** the "Phase 3 additions" sketch in `docs/superpowers/specs/2026-05-29-local-review-threads-design.md` (discovery/cwd-default/external-shell), narrowed by this brainstorm.

## Goal

Let a maintainer run their own terminal `claude` inside a worktree and have it work that worktree's review with zero configuration: it discovers the review from its own directory, reads the threads, reads the PR context, and replies — the same review the in-app agent and the UI see. Along the way, fix a latent identity bug: a review is currently keyed by worktree *path*, so switching branches in one worktree silently shares a thread-set. Make reviews **branch-scoped**, like a GitHub PR tied to its head branch.

## Decisions (from brainstorming)

- **External capability = read + discuss.** A terminal Claude already has its own Edit/Bash, so it edits code directly. Through `middleman mcp` it only reads the review and replies; resolving/hiding threads stays in the app. No edit/apply tools.
- **Addressing = zero-config cwd, single review.** The proxy resolves the review for the worktree it's launched in. No `list_reviews`/`get_review`, no flags, no IDs. (Cross-worktree discovery remains a clean fast-follow on the same endpoint.)
- **Branch identity = branch-scoped, "light" implementation.** A review's threads are scoped to a branch. Implemented by tagging each thread with its branch and filtering by the worktree's current branch — **not** by re-keying the synthetic MR. `number` stays `worktree.id`; `resolveLocalWorktree` and its ~14 callers, the handle, and UI navigation are untouched.
- **Diff is not a tool.** A terminal Claude can `git diff`/read files itself; `get_pull` gives it the base/head SHA so it can diff the exact range under review. So the surface is four tools: `list_threads`, `get_thread`, `reply_to_thread`, `get_pull`.
- **Auth = loopback only** (matches the server today). Token auth stays deferred.

## Scope

**In:** a `branch` column on review threads + stamp-on-create + filter-by-current-branch; a `/local/resolve` endpoint (path → review handle + current branch); a cwd-default mode for `middleman mcp` (resolve when `--owner/--name/--number` are absent); a `get_pull` MCP tool; registration docs (`claude mcp add`); tests.

**Out:** `list_reviews`/`get_review` discovery tools; `get_diff`/`resolve_thread`/`hide_thread`/`apply_thread` external tools; per-`(worktree,branch)` review *identity* (the "full" re-key); token auth; per-`(worktree,branch)` in-app `--resume` session scoping (the in-app session stays per-worktree — a separate follow-up).

## Current touch points (verified)

- `internal/db/migrations/000017_add_worktrees.up.sql` — `middleman_worktrees` is `UNIQUE(repo_id, path)`; `branch`/`head_sha` are refreshed on each scan.
- `internal/server/local_dispatch.go` — `ensureSyntheticMRForWorktree` keys the synthetic MR by `(repo, number=worktree.id)` (`UpsertMergeRequest` conflicts on `(repo_id, number)`); `resolveLocalWorktree(name, number)` maps `number → worktree` (~14 callers, all in `internal/server`); `getPullLocal` (the `get_pull` data); `localOwner = "local"`, `isLocalSource`.
- `internal/db/migrations/…_review_threads.up.sql` — `middleman_review_threads.mr_id → merge_requests(id)` (threads FK the MR by primary key, **not** by number).
- `internal/db/queries_review_threads.go` — `AddReviewThreadComment`, the create/list queries (`loadReviewThreadsResponse`), `scanReviewThread`.
- `internal/server/huma_routes_review_threads.go` — create / list / `/ask` / resolve / hide; `resolveThreadForMR`.
- `internal/mcp/server.go` + `tools.go` — stdio JSON-RPC server; `Config{ServerName, BaseURL, ReviewOwner, ReviewName, ReviewNumber}`; `builtinTools()` (the 3 existing tools); `reviewPath` builds `/api/v1/repos/{owner}/{name}/pulls/{number}{suffix}`.
- `cmd/middleman/main.go` — `runMCP` requires `--owner/--name/--number` today (`baseURL` defaults to `http://127.0.0.1:8091`).
- `internal/worktrees/` — git helpers (toplevel/branch/base resolution) reused for the live branch read.

## Design

### 1. Branch-scoped threads (light)

- **Migration** `000023` (the branch's last is `000022`; confirm it's still free at impl time): `ALTER TABLE middleman_review_threads ADD COLUMN branch TEXT NOT NULL DEFAULT ''`. Backfill existing rows from their worktree's scanned branch via `mr_id → merge_requests.platform_id → middleman_worktrees.branch` (a correlated `UPDATE`); rows that resolve to nothing stay `''`.
- **Current branch is server-derived and authoritative.** A helper reads the worktree's live branch (`git -C <path> rev-parse --abbrev-ref HEAD`, via `internal/worktrees`), falling back to the scanned `worktrees.branch` on error. This is the single source of truth so the in-app UI and the external proxy always agree, and a branch switch takes effect immediately (no wait for the periodic scan).
- **Create** (in-app only — external Claude can't create, only reply): stamp `branch` = the worktree's current branch.
- **List** (`loadReviewThreadsResponse`): filter to the current branch; legacy `''` rows remain visible (migration cushion). `get_thread` (by id) and `reply_to_thread` (by id) need no branch logic — ids come from the already-filtered list.
- `number` stays `worktree.id`; `resolveLocalWorktree`, the handle, the synthetic MR keying, and UI navigation are unchanged. The in-app UI inherits branch-correct threads for free.

### 2. `/local/resolve` endpoint

`GET /api/v1/local/resolve?path=<abs worktree path>` → `{ "owner": "local", "name": "<repo>", "number": <worktree.id>, "branch": "<current branch>" }`.
- Match `path` (canonicalized) against `middleman_worktrees.path` for an active (`removed_at IS NULL`) row; `404` with a clear message when nothing matches.
- `name` is the worktree's parent repo name; `branch` is the live current branch (§1 helper) — informational for the proxy.
- Loopback only, same as the rest of the API.

### 3. `middleman mcp` cwd-default mode

When `--owner/--name/--number` are omitted, the proxy self-locates:
1. `git rev-parse --show-toplevel` in its cwd → worktree path.
2. `GET {base-url}/api/v1/local/resolve?path=…` → the handle.
3. Pin that handle and serve the existing tools against it.

Resolution is lazy (on first tool call) and cached for the process; failures (not a git worktree, no matching review, server unreachable) surface as a **clear MCP tool error** (`isError: true`) rather than killing the server, so Claude can read and report them. When the flags *are* present (the in-app runner), behavior is exactly as today — the runner path is untouched.

### 4. Tool surface (`get_pull` added)

`builtinTools()` gains `get_pull` (no args) → `GET …/pulls/{number}` (the existing `getPullLocal`), returning the synthesized PR metadata incl. base/head branch + SHAs + title. Combined with the existing `list_threads` / `get_thread` / `reply_to_thread`, that's the four-tool read+discuss surface. The MCP tool-name allowlist used by the in-app runner is unaffected (the runner still gates its own subset).

### 5. Registration & data flow

Documented one-liner — no per-review config: `claude mcp add middleman -- middleman mcp`.

```
cd <worktree>; claude
  → middleman mcp (no flags) ──git toplevel──▶ GET /api/v1/local/resolve ──▶ {local, repo, worktree.id}
  → list_threads ─▶ GET .../review-threads     (server filters to the worktree's current branch)
  → get_pull     ─▶ GET .../pulls/{number}      (base/head SHA → Claude diffs the right range itself)
  → reply_to_thread ─▶ POST .../review-threads/{id}/comments  (author=agent; lands as a normal thread reply)
```

## Error handling

- **Not in a known worktree / resolve 404:** tool error "no middleman review for this directory: `<path>`".
- **Server unreachable:** tool error surfacing the connection failure (existing `restJSON` behavior).
- **Empty review:** `list_threads` returns an empty list — distinct from a resolve 404.
- **Branch with no threads yet:** empty list (the filter simply matches nothing) — not an error.

## Testing (e2e non-negotiable)

- **DB:** create stamps the current branch; list filters by branch (two branches in one worktree → disjoint thread-sets); the backfill `UPDATE` populates legacy rows.
- **API e2e** (generated client): `/local/resolve` returns the handle for a seeded worktree path and `404`s for an unknown path; creating threads on one branch then listing after a branch switch returns the other branch's set (drive the branch via the `internal/worktrees`/git seam used by `seedReviewWorktree`).
- **MCP** (`httptest` server, cf. `tools_test.go`): `tools/list` includes the four tools; `get_pull` maps to the pull endpoint; cwd-default mode resolves a handle from a path via `/local/resolve` and a tool call routes correctly; an unresolvable cwd yields an `isError` tool result.
- **Conventions:** `-shuffle=on`, testify; server suite sandboxed with `-short` (real-tmux workspace e2e excluded, per the sandbox note); `make api-generate` regenerates the Go + TS clients for the new endpoint.

## Risks / notes

- **Per-request git read for the current branch.** A `rev-parse` per list/create is cheap (local, ~ms) and correctness-critical for "switch branch → immediately scoped"; the plan may cache it briefly if the 1.5s in-app poll proves chatty. Fallback to the scanned row keeps it robust if git fails.
- **`''` legacy threads** stay visible across branches until re-stamped; acceptable — nothing is shipped, so the only such rows are local test data.
- **In-app session is still per-worktree**, so the `--resume` conversation carries across an in-app branch switch. Out of scope here (each external terminal Claude is its own session); flagged as a separate follow-up.
- **Read-only by construction:** the external surface exposes no edit/state-mutation tool; code edits are the terminal Claude's own doing, and review-state changes stay in the app.
