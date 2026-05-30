# Handoff — Local Review Threads, Phase 2a execution

**For the new session:** Execute the Phase 2a plan
`docs/superpowers/plans/2026-05-30-local-review-threads-phase2a-agent-backend.md`
using **superpowers:subagent-driven-development** (fresh subagent per task +
two-stage spec→quality review, the same way Phases 1a/1b were executed).
Branch is `serve-local-repo-comments` — **commit per task, do NOT push / PR /
merge / change branches** without explicit user approval.

Kickoff prompt to paste into the new session:
> "Execute the Phase 2a plan for local review threads
> (`docs/superpowers/plans/2026-05-30-local-review-threads-phase2a-agent-backend.md`),
> subagent-driven, on the current branch. Read this handoff first:
> `docs/superpowers/plans/2026-05-30-local-review-threads-phase2a-HANDOFF.md`."

---

## Project

`middleman` — a local-first GitHub PR dashboard (Go + SQLite via
modernc.org/sqlite, Huma v2 API, Svelte 5 SPA embedded in the Go binary).
The feature: persist a reviewer's inline comments on a **local git worktree**
as anchored, hideable threads; let one review-wide Claude agent discuss them
inline and apply fixes only on "Go"; expose threads over REST + an MCP proxy
for an external shell agent.

## Status — branch `serve-local-repo-comments`

⚠ This branch is **stacked on the unmerged `branch-navigation` work** (not
off `main`), and the whole feature is **kept unmerged** by the user's choice.

- **Phase 1a — backend: DONE.** Migration `000021` (`review_threads` +
  `review_thread_comments` on the synthetic MR), DB query layer, REST routes
  (list/create/comment/hide/unhide/resolve, local-gated), generated client,
  e2e. Persist-only create.
- **Phase 1b — frontend: DONE.** `reviewThreads` store, `ReviewThreadCard`
  inline on the diff, diff-view load lifecycle, local Submit → create threads
  (persist-only; remote verdict/summary controls hidden for local).
- **Phase 2 — design DONE.** Spec
  `docs/superpowers/specs/2026-05-29-local-review-threads-design.md`. Resolved
  the MCP-phasing contradiction: MCP **core** (list/get/reply, on a
  runner-passed handle) ships in **2a**; discovery (`list_reviews`/
  `get_review`) + cwd-default + external-shell registration are **Phase 3**.
  Phase 2 split into **2a (agent backend)** + **2b (frontend)**.
- **Phase 2a — PLANNED, NOT executed.** ← the task. 5 tasks: hand-rolled
  stdio MCP server (`internal/mcp`, no dep) + `middleman mcp` subcommand;
  `SessionRunner` discuss/apply/steer turns (per-phase `--allowedTools` +
  temp `--mcp-config`/`--strict-mcp-config`); create-`mode` + `apply`/
  `apply-all` endpoints; server-driven status; 409-on-busy.
- **Phase 2b (frontend mode picker + Apply buttons + polling)** and
  **Phase 3 (external MCP discovery + registration)** — not planned yet.

## THE key risk for 2a

The hand-rolled MCP server's JSON-RPC **handshake must be byte-compatible
with the live `claude --mcp-config`** (the `initialize` protocol-version echo
and the `capabilities.tools` shape). The plan's protocol unit tests are
deterministic, but they don't prove Claude will connect. **Do a real-Claude
smoke check** — drive one discuss turn on an actual local worktree and
confirm the agent's `reply_to_thread` call lands — before calling 2a done.

## Environment gotchas (learned the hard way this session)

- **Frontend tests run from `frontend/`, not `packages/ui/`.** `frontend/`'s
  vite config has the Svelte plugin and globs `../packages/ui/src/**/*.test.*`;
  running from `packages/ui/` fails to compile `$state` runes and gives
  misleading errors. Use `cd frontend && bunx vitest run [filter]`.
- **`make api-generate` needs the sandbox DISABLED** (bun hits "Unexpected
  accessing temporary directory" / "Operation not permitted") **and**
  `GOCACHE="$HOME/.cache/go-build"` (the Makefile otherwise defaults GOCACHE
  to a non-writable `/tmp/middleman-gocache`). Then run
  `go generate ./internal/apiclient/generated` for the Go client.
- **The Go `internal/server` test suite needs `tmux`, which the sandbox
  blocks.** Run the full server suite **unsandboxed**. `TestWorkspaceDeleteDirty`
  only passes with tmux available (pre-existing, unrelated to this feature).
- **IDE/tool diagnostics are noisy false-positives here.** Svelte `$state`
  flagged "undefined", `range`-over-int and `strings.SplitSeq` flagged as
  errors, cascading "X is not a type" — all from a diagnostic toolchain that
  doesn't match the real Go/Svelte versions. **Trust `go build` / `go test` /
  `bun run build`, not the diagnostics panel.** After API regen, diagnostics
  lag the regenerated client (more false "undefined"s).
- **Never `git add -A`.** The working tree surfaces untracked HOME dotfiles
  (`.bashrc`, `.gitconfig`, `.claude/…`, etc.). Stage explicit paths only.
- A pre-existing stale-date test (`TestAPIActivityReturnsUTCCreatedAt`) was
  fixed this session (commit `7def4bc`); don't be surprised by it.

## Conventions in force

- **Commit every task** (conventional message). End every commit message with
  the trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
  Never push / branch-change / merge without explicit OK.
- Tests: `-shuffle=on`, never `-v`, never `-count=1`, testify (`require` for
  preconditions, `assert` otherwise). e2e via the generated client in
  `internal/apiclient`. Fake `claude` via `aireview.SetBinaryForTest` (or the
  package `claudeBinary` var); to assert spawn args, have the fake script
  write `"$@"` to a file (see the Phase 2a plan Task 4).
- **Adjudicate code-review findings with evidence** — accept real issues, but
  decline with reasoning where the nearest analog justifies the choice
  (`internal/db/queries_ai.go`, `internal/aireview/sessions.go`,
  `worktreeSession.svelte.ts` are the canonical patterns). Don't reflexively
  apply every nit. Re-run the relevant tests after any fix.
- Datetimes UTC at storage/API boundaries; local only in the Svelte layer.

## Canonical pointers

- Spec: `docs/superpowers/specs/2026-05-29-local-review-threads-design.md`
- Plan to execute: `docs/superpowers/plans/2026-05-30-local-review-threads-phase2a-agent-backend.md`
- Done-phase plans (reference): `…/2026-05-29-local-review-threads-phase1-backend.md`,
  `…/2026-05-30-local-review-threads-phase1b-frontend.md`
- Key existing code to model on: `internal/aireview/sessions.go` (runner),
  `internal/db/queries_review_threads.go`, `internal/server/huma_routes_review_threads.go`,
  `cmd/middleman/main.go` (`runCLI` switch), `internal/aireview/sessions_test.go`
  (fake-claude harness).
- Project memory (`local_review_threads.md`) auto-loads this state.
