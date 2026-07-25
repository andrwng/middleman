# Cross-Repo MCP Review Comments — Design

Date: 2026-07-24
Status: Approved (design)
Builds on:

- The local review-threads / agent-session machinery (branch-scoped local reviews).
- The `middleman mcp` server (`internal/mcp/`) that exposes review tools to a Claude Code
  agent over stdio JSON-RPC.

## Problem

The `middleman mcp` server binds a **single** `{owner, name, number}` review handle at
startup — resolved either from `--owner/--name/--number` (the in-app launcher) or by
self-locating from the cwd (`resolveCwdHandle` → `git rev-parse --show-toplevel` →
`GET /local/resolve?path=`, `cmd/middleman/main.go:148-206`). All five tools
(`list_threads`, `get_thread`, `reply_to_thread`, `start_thread`, `get_pull`) are
implicitly scoped to that frozen handle via `reviewPath()`
(`internal/mcp/tools.go:166-169`).

So a Claude Code agent running in repo A can only ever read/manipulate review comments on
repo A's worktree. Acting on another local repo B requires restarting `middleman mcp` with
a different handle — impossible mid-session.

## Goal

Let a Claude Code agent running in repo A read and manipulate review comments on **another
registered local repo B**, without restarting the MCP server: by naming the target repo
(and, when needed, a branch) per tool call, and discovering what is targetable.

## What already exists (reused, not rebuilt)

The server/data plane is **already multi-repo**; only the MCP layer is single-repo.

- `GET /api/v1/worktrees` → `listWorktrees` (`internal/server/huma_routes.go:1939-1965`) →
  `ListAllActiveWorktrees` (`internal/db/queries_worktrees.go:136-177`) enumerates **every**
  registered local worktree across **all** repos:
  `{id, repo_owner, repo_name, path, branch, head_sha, has_running_turn}`
  (`internal/server/api_types.go:316-333`).
- `GET /api/v1/local/resolve?path=` (`internal/server/local_dispatch.go:420-448`) resolves
  any worktree path → `{owner, name, number, branch}`.
- Every review-thread route
  (`/api/v1/repos/{owner}/{name}/pulls/{number}/review-threads…`,
  `internal/server/huma_routes_review_threads.go`) is addressed **per request** by
  `{owner, name, number}` and dispatched via `resolveLocalWorktree(name, number)`; `owner`
  is always `"local"` for these.
- The single-repo chokepoints are entirely in `internal/mcp`: (a) `mcp.Config` holds one
  frozen `{ReviewOwner, ReviewName, ReviewNumber}` (`internal/mcp/server.go:19-31`);
  (b) `reviewPath()` interpolates that frozen triple with no override
  (`tools.go:166-169`); (c) the five tool schemas expose no repo/worktree argument
  (`tools.go:24-164`).

This feature is therefore an **MCP-layer change only**; no server data-model change is
required to *address* a different registered local repo.

## Design (MCP-layer only)

### 1. Optional `repo` / `branch` args on the review tools

- `list_threads`, `get_thread`, `reply_to_thread`, `start_thread`, and `get_pull` each gain
  two optional string properties: `repo` (the target repo name) and `branch` (to
  disambiguate when that repo has more than one active worktree).
- **Omitting `repo`** = the current bound handle (the startup triple) — exactly today's
  behavior. No existing call changes.
- Same capabilities cross-repo as in-repo — full parity: read threads, read a thread, reply
  to a thread, create a thread, read pull metadata.
- Additionally, `list_threads` gains an optional `path` argument that scopes the result to a
  single file: the returned threads are filtered to those whose `path` matches (client-side,
  the same way `get_thread` filters the list by id). This composes with `repo`/`branch`
  (e.g. "the comments on `x.go` in repo B"). No server change — the list endpoint already
  returns every thread carrying its `path`. `path` is only on `list_threads` (`start_thread`
  already takes a path; `get_thread`/`reply_to_thread` are by id).

### 2. New discovery tool `list_repos`

- No arguments. Returns the registered local repos' active worktrees so the agent can see
  what it can target and which repos need a `branch`.
- Each row = one active worktree: `{repo, branch, path, has_running_turn}`. A repo with two
  active worktrees appears twice (once per branch).
- Backed by `GET /api/v1/worktrees` (`owner` is always `"local"`).
- Always works, even when the startup handle is unresolved — it is pure discovery.

### 3. Per-call target resolution (`internal/mcp`)

A new resolver, `resolveTarget(repo, branch) → (name, number, err)`:

- Fetch `GET /api/v1/worktrees`; keep active worktrees where `repo_name == repo`.
- **0 matches** → error: `no local repo named "<repo>"; available: <sorted repo names>`.
- **1 match** → use it (`number = id`). If `branch` was given and does not equal that
  worktree's current branch → error:
  `repo "<repo>" is on branch "<current>", not "<branch>"`.
- **N matches** → if `branch` given, pick the worktree whose branch equals it (error listing
  the available branches if none match); if `branch` omitted → error:
  `repo "<repo>" has multiple worktrees (branches: <list>); pass "branch"`.

Each tool handler computes its effective handle: if `repo` is present →
`("local", resolvedName, resolvedNumber)`; otherwise → the `Config` triple.
`reviewPath()` builds the REST path from **that** handle rather than the frozen one (i.e.
it takes the handle as an argument, or a per-call sibling does).

The `Unresolved` gate (`tools.go:242-247`) moves from "every `tools/call` errors" to "only
a call that **falls back to the default handle** errors when the default is unresolved." An
explicit `repo` bypasses it; `list_repos` is never gated.

### 4. In-app parity

The in-app review sessions (`writeMCPConfig`, `internal/aireview/sessions.go:668-700`) get
the same tool set + args; their default target stays their own review's worktree (the
injected triple), but they can also target another repo. Uniform with external agents. The
injected Claude allow-list (`sessions.go:331`) adds `list_repos`.

## Data flow

1. Agent (in repo A) calls `list_repos` → MCP `GET /api/v1/worktrees` → rows
   `{repo, branch, path, has_running_turn}`.
2. Agent calls e.g. `list_threads {repo: "B", branch: "feat"}` → MCP
   `resolveTarget("B", "feat")` → `("B", <number>)` →
   `GET /api/v1/repos/local/B/pulls/<number>/review-threads` → threads.
3. `reply_to_thread` / `start_thread` with `repo` / `branch` → resolve →
   `POST …/review-threads[/{id}/comments]` on repo B's worktree.
4. Omitting `repo` → the startup handle, unchanged.

## Decisions

- Target = per-call `repo` (name) + optional `branch`; discovery via `list_repos`.
  (User-approved.)
- Resolution lives in the MCP layer (filter the existing `/worktrees`), so **no server
  change** is required. (Alternative considered: a server `resolve-by-name` endpoint
  symmetric with `/local/resolve`; deferred — the MCP-side filter is smaller and the data
  is already exposed.)
- `get_pull` is included in the cross-repo set (context parity).
- `list_threads` also takes an optional `path` to file-scope the result (client-side filter,
  no server change). (User-approved 2026-07-24.)
- In-app sessions get the same tools (uniform); default target unchanged.
- Backward-compatible: no `repo` arg → identical to today.

## Non-goals

- Cross-repo agent kickoff / "apply" (launching a review/apply turn on repo B from repo A) —
  this is comment read/reply/create only, matching today's tools.
- Remote/GitHub repos — review threads remain local-worktree only (the `isLocalSource` gate
  is unchanged).
- Auth/authorization changes — loopback + CSRF unchanged; no new trust boundary (the REST
  API already serves every local repo to any loopback caller, so cross-repo MCP only
  surfaces what is already permitted).
- Historical-branch threads within one worktree — `branch` selects **which worktree** (by
  its current branch), not a past branch state of a worktree.

## Edge cases / error handling

- **Ambiguous repo** (multiple worktrees, no `branch`) → error listing the branches.
- **repo / branch not found** → error listing valid repos (or that repo's branches).
- **Two worktrees of one repo on the same branch** (rare) → resolution is still ambiguous;
  the error includes the paths. Addressing by path is a possible future disambiguator (out
  of scope now).
- **Default handle unresolved** (agent in an unregistered dir) → calls **with** an explicit
  `repo` still work; calls **without** one return the existing unresolved error;
  `list_repos` always works.
- **`/worktrees` fetch fails** → the tool returns a clear `isError` result (cannot reach the
  middleman server), not a panic.

## Testing

- **Go unit** (`internal/mcp`): `resolveTarget` — 0/1/N worktree matches, branch
  match/mismatch, ambiguous-no-branch, not-found (table-driven, against a stubbed
  `/worktrees` payload via the `mcp.Config.httpDoer` seam).
- **Go unit** (`internal/mcp`): `list_threads` with a `path` arg filters the returned threads
  to that file; omitting `path` returns all (backward-compat).
- **Go e2e** (`internal/mcp` against a real server + a multi-repo test DB seeded with 2+
  local repos/worktrees): `list_repos` lists all; `list_threads` / `start_thread` /
  `reply_to_thread` with `repo` / `branch` act on repo B while the server is bound to repo
  A; omitting `repo` still acts on A (backward-compat); an explicit `repo` works when the
  default handle is unresolved; the error cases above.
- Prefer the existing MCP test harness + the `httpDoer` seam / the generated API client.
- Green bars: `go test ./internal/mcp/... ./internal/server/... -short -shuffle=on`,
  `go vet`, golangci-lint.

## Open questions / future

- A server-side `resolve-by-name` endpoint (symmetric with `/local/resolve`) if the
  resolution logic grows or other callers need it.
- Addressing a specific worktree by path when two worktrees of one repo share a branch.
- `get_thread` still lists-and-filters client-side (there is no single-thread GET route) —
  unchanged here; a real single-thread route is a separate optimization.
