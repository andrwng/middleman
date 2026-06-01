# middleman

A local tool for reviewing code. You do the reviewing; middleman is the surface — a diff viewer, anchored comments, threaded discussions — and an integration layer for Claude as an optional assistant (summarising a PR, answering questions about specific lines, etc.). It does not produce reviews on your behalf.

Two things it tries to make easier: reviewing pull requests on GitHub, and reviewing your own local git worktrees before you push.

Forked from [wesm/middleman](https://github.com/wesm/middleman), which is a local GitHub notifier / PR dashboard. This fork has diverged: the central feature is now code review — both of others' PRs and of local drafts — with the GitHub-inbox and triage features kept as supporting context. Some of those upstream features may be removed over time.

Runs on your machine. One Go binary, one SQLite database, one TOML config. No hosted service or account required.

## Quick start

Requirements: Go 1.26+, [Bun](https://bun.sh/), a GitHub token (env var or the `gh` CLI), and `claude` on `PATH` for the AI features.

```sh
git clone https://github.com/andrwng/middleman
cd middleman
make build
export MIDDLEMAN_GITHUB_TOKEN=ghp_…   # or `gh auth login` and skip this
./middleman
```

Open <http://localhost:8091>. On first run middleman writes `~/.config/middleman/config.toml` and starts with no repos. Add them from the Settings page, or edit the file:

```toml
[[repos]]
owner = "your-org"
name = "your-repo"
```

`make install` drops the binary in `~/.local/bin`. `middleman version` prints version, commit, build date.

## Features

### Reviewing GitHub PRs

The diff viewer is the main surface. Each line is anchorable: hover a line to get a `+` button for a review comment or a `?` button to ask Claude about that line. Selections work across multiple lines. Comments are buffered locally as drafts and published as a single GitHub review when you click Review.

Threaded review comments appear inline next to their anchor. You can hide threads locally to clear them off your screen; hidden state is per-PR, keyed by GitHub comment id, and survives re-sync. A new reply on a hidden thread auto-unhides it on the next sync. Nothing about hiding is propagated to GitHub.

Markdown files are reviewable in **rendered mode**: the `.md` is rendered to HTML and each source line is still anchorable, so highlighting a paragraph and clicking `?` produces a Claude thread anchored to those source lines. The thread appears inline below the rendered block.

### Reviewing your own local worktrees

Add a local checkout to your config:

```toml
[[repos]]
local_path = "~/code/myproject"
base_ref = "origin/dev"
```

Middleman scans that directory for git worktrees on each sync and surfaces each one as a PR-shaped review surface — same diff viewer, same per-line comments, same AI Ask, same rendered-markdown mode. The diff defaults to `merge-base vs. working tree`, so committed work and uncommitted edits are both reviewable in one pass.

Each worktree also gets an **interactive Claude session**. The Review tab's Submit button doesn't post to GitHub (there's nothing upstream); it pushes a turn into a Claude session running with `cwd = <worktree_path>`. The Activity tab streams the back-and-forth, including Claude's tool calls. You can chat with Claude about your draft and have it edit the worktree directly, then re-review the result. The session persists across turns via `--resume`; killing it ends the conversation but leaves the history.

### Use your own terminal Claude on a worktree's review

Register middleman's MCP server once:

```sh
claude mcp add middleman -- middleman mcp
```

Then run `claude` from inside any worktree middleman tracks. It auto-discovers that worktree's review for the current branch (no flags, no IDs) and exposes four read-and-discuss tools: `list_threads`, `get_thread`, `get_pull`, and `reply_to_thread` — it reads the review and replies in threads. Resolving/hiding threads and applying edits stay in the middleman app, and `middleman` must be running (the proxy talks to its loopback API).

### AI features in detail

- **Brief.** A staff-engineer-style overview of a PR, generated from the diff, commits, and description. Useful as a starting point before scrolling the diff.
- **Commit analysis.** Per-commit Claude summary, accessible from the commits panel. Separate from the cumulative diff brief.
- **Ask.** Per-line Claude threads, anchored like review comments. Each thread supports follow-up questions; conversation persists via `--resume`.
- **Auto-close.** When a PR closes or merges, any active Ask threads on it are closed automatically and any in-flight subprocesses are killed. The thread + history stays in SQLite for later reference.

All AI threads are local-only. Questions and answers live in your middleman SQLite database, not on GitHub.

### Dashboard

The original GitHub-dashboard surface is still present and useful for keeping track of multiple repos:

- Activity feed across all enrolled repos, with threaded or flat view, filter by time range / author / item type / repo / free text, and toggles to hide closed items and bots.
- PR list with grouping by repo, search, and review-decision/CI status badges.
- Issue tracking with the same filters and a detail view supporting comment / close / reopen.
- Kanban board (New / Reviewing / Waiting / Awaiting Merge) with drag-and-drop. Columns are local-only.

These are kept because they're useful, not because they're the focus. The review experience is.

### Keyboard

| Key | Action |
|-----|--------|
| `j` / `k` | Move through the list (or between files in the diff) |
| `1` / `2` | Switch between list and kanban views |
| `Escape` | Close detail view / clear selection |
| `Cmd/Ctrl+Enter` | Submit a comment, AI question, or review summary |

---

## Implementation notes

### AI runner

Each AI surface (Brief, Commit Analysis, Ask, Worktree session) spawns the `claude` CLI as a subprocess via `internal/aireview`. First-turn prompts pass diff/hunk/context inline; follow-ups use Claude's `--resume <session-id>` to keep conversation context without replaying the whole prompt history. The `Runner` tracks `pid` plus a per-question `context.CancelFunc` in a map, so cancellation (or PR-close auto-cancel) actually kills the subprocess. On startup, a reconciler walks any turns left in `queued` or `running` state and marks them failed, so an interrupted turn doesn't haunt the UI after a restart.

### Local worktrees

`internal/worktrees` reconciles `git worktree list` output against the DB on each sync of a `local_path` repo entry. Worktrees route through PR-shaped URLs (`/repos/local/<name>/pulls/<worktree_id>/…`) via a synthetic `merge_request` row, so the diff viewer, sidebar, draft comments, and AI threads reuse the existing PR code path. Dispatch happens at the request boundary in `internal/server/local_dispatch.go`; downstream code doesn't need to know which "PRs" are actually local checkouts. The diff endpoint resolves `(base, working_tree)` via git refs; `?commit=WORKING-TREE` is a synthetic sentinel for the uncommitted slice.

### Per-line anchors in rendered markdown

A custom `marked` block renderer (`packages/ui/src/components/diff/renderedMarkdownAnchors.ts`) splits each block's `raw` source on `\n`, runs each segment through `marked.parseInline()`, and wraps it in `<span class="rmd-anchor" data-anchor-line=N data-anchor-side=…>`. Selections resolve to a source-line range by walking from `window.getSelection()` to the nearest anchor span. Block ranges are half-open `[start, end)` so adjacent blocks don't double-claim their boundary line. Threads and comments mount imperatively after the rendered HTML commits; teardown runs on component unmount.

### Hidden review threads

Per-PR set of "I've seen this" markers kept in `middleman_hidden_review_threads`, keyed by GitHub's comment id so they survive re-sync. The reveal toggle in `DiffToolbar` and `EventTimeline` brings hidden threads back, dimmed. New replies that arrive after the hide timestamp re-show the thread on the next sync — computed in SQL via `max(created_at)` per thread.

## Architecture

```
middleman binary
  ├── Config loader (TOML)
  ├── Sync engine ─────────► GitHub API (go-github)
  │      ├── PR/issue/comment sync
  │      ├── Worktree scanner (per local_path entry)
  │      └── Post-sync hooks: stack reconciler, AI auto-close
  ├── SQLite (WAL, modernc.org/sqlite — pure Go, no CGO)
  │      └── numbered SQL migrations in internal/db/migrations
  ├── AI runner (internal/aireview)
  │      └── claude subprocesses, --resume sessions, kill control,
  │         startup reconciler for interrupted turns
  └── HTTP server (Huma) ─► REST API (codegen'd OpenAPI client)
                          + embedded Svelte 5 SPA
                          (loopback only — 127.0.0.1 by default)
```

Frontend lives in `packages/ui` (shared Svelte components) plus `frontend` (the Vite app that wraps and embeds them). The `packages/ui` modules also export reusable views, stores, and a context Provider, so the same UI can be mounted inside a host app — see Embedding.

## Develop

```sh
make air-install    # one-time: install air for live reload
make dev            # Go server on :8091 with live reload
make frontend-dev   # Vite on :5174, proxies /api → Go
```

Tests:

```sh
make test           # All Go tests (with -shuffle=on)
make test-short     # Fast tests only
make lint           # golangci-lint
make frontend-check # svelte-check + tsc --noEmit
make api-generate   # Regenerate OpenAPI spec and Go/TS clients
```

Pre-commit hooks via [prek](https://github.com/j178/prek): `brew install prek && prek install`.

A Docker Compose dev stack is available via `mise run dev-compose`. It uses `docker/dev-config.toml`, persists SQLite in a Docker volume, exposes the backend on `:18090` and the frontend dev server on `:15173`, and reads the token from your host's `gh auth token`. Override the config file with `MIDDLEMAN_CONFIG=/path/to/config.toml make dev` (and the same env var for `make frontend-dev`).

## Database

SQLite with embedded migrations, applied on startup via `golang-migrate`. Three startup states matter:

- Fresh DB: all migrations apply.
- Legacy DB without `schema_migrations`: assumed baseline v1, migrated forward.
- Dirty / failed / newer than the binary: startup fails with a message telling you to either upgrade middleman or delete the file.

Deleting `~/.config/middleman/middleman.db` is always safe — sync data repopulates from GitHub on the next run. Local-only state (kanban columns, stars, hidden threads, AI thread history, worktree links) is lost.

## Embedding

Middleman can be embedded as a Go library inside another application. The host creates an `Instance`, which exposes an `http.Handler`:

```go
inst, err := middleman.New(middleman.Options{
    Token:        os.Getenv("GITHUB_TOKEN"),
    DBPath:       "/path/to/middleman.db",
    BasePath:     "/middleman/",
    SyncInterval: 5 * time.Minute,
    Repos: []middleman.Repo{
        {Owner: "org", Name: "repo"},
    },
})
if err != nil { log.Fatal(err) }
defer inst.Close()
inst.StartSync(ctx)
mux.Handle("/middleman/", inst.Handler())
```

`EmbedConfig` controls theming and UI defaults. `EmbedHooks` exposes `OnMRSynced` / `OnSyncCompleted` for sync events. The frontend is also available as the `@middleman/ui` Svelte workspace package, exporting individual views, store factories, and a context Provider that accepts an action registry for injecting custom buttons.

## Configuration

All fields optional. Repos can be added in the config file or through the Settings UI.

| Field | Default | Description |
|-------|---------|-------------|
| `sync_interval` | `"5m"` | How often to pull from GitHub |
| `github_token_env` | `"MIDDLEMAN_GITHUB_TOKEN"` | Env var holding your token (falls back to `gh auth token`) |
| `host` | `"127.0.0.1"` | Listen address; non-loopback is rejected |
| `port` | `8091` | Listen port |
| `base_path` | `"/"` | URL prefix for reverse-proxy deployments |
| `data_dir` | `"~/.config/middleman"` | Directory for the SQLite database |
| `sync_budget_per_hour` | `0` | Per-host hourly GitHub API budget; 0 = unlimited |
| `sync_recent_days` | `7` | How far back the closed-item backfill goes |
| `activity.view_mode` | `"threaded"` | `"flat"` or `"threaded"` |
| `activity.time_range` | `"7d"` | `"24h"`, `"7d"`, `"30d"`, or `"90d"` |
| `activity.hide_closed` | `false` | Hide closed/merged items in the feed |
| `activity.hide_bots` | `false` | Hide bot activity |
| `roborev.endpoint` | `""` | Endpoint of an external roborev daemon, if you use one. Middleman proxies `/api/roborev/*` to this address when set. |
| `tmux.command` | `[]` | Custom command middleman shells out to when launching tmux-backed terminal workspaces. Defaults to plain `tmux`. |

### Local worktree review

```toml
[[repos]]
local_path = "~/code/myproject"
# base_ref = "origin/main"   # optional override
```

| Field | Description |
|-------|-------------|
| `local_path` | Directory middleman scans for git worktrees on each sync. Mutually exclusive with `owner`/`name`/`platform_host`/`token_env`. |
| `base_ref` | Optional. Overrides the auto-detected base ref used to compute each worktree's change set. Defaults to whichever of `origin/main`, `origin/master`, `origin/develop`, `origin/dev` resolves first. Set this when the auto-pick isn't what you'd diff against (e.g. `origin/main` is a release branch and you develop off `origin/develop`). Only valid on local-only entries. |

### GitHub Enterprise

```toml
[[repos]]
owner = "team"
name = "internal-app"
platform_host = "github.corp.example.com"
token_env = "GHE_TOKEN"
```

Each distinct host can use a separate token env var. Repos without `platform_host` default to `github.com`.

### Network exposure

The default `host = "127.0.0.1"` is enforced — middleman refuses to bind a non-loopback address. To reach the UI from another machine, put a reverse proxy (Caddy, nginx, socat) in front of the loopback port. Middleman has no built-in auth, so a proxy is also the right place to add HTTP basic auth, TLS, or an IP allowlist.

## License

MIT
