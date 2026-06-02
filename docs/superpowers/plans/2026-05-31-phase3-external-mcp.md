# Phase 3 — External-Shell MCP + Branch-Scoped Reviews Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a maintainer's terminal `claude`, launched in a worktree, work that worktree's review with zero config (discover → read threads → read PR → reply), and make a worktree's review threads and in-app `--resume` session branch-scoped so switching branches no longer shares state.

**Architecture:** A `branch` column is added to `middleman_review_threads` and `middleman_worktree_sessions`; the server stamps each thread/session with the worktree's *live* current branch (read via `git rev-parse --abbrev-ref HEAD`, falling back to the scanned `worktrees.branch`) and filters listings by it. A new loopback `GET /api/v1/local/resolve?path=` maps an absolute worktree path to its `{owner,name,number,branch}` handle; `middleman mcp` gains a cwd-default mode that self-locates via `git rev-parse --show-toplevel` + `/local/resolve` when `--owner/--name/--number` are absent, and a fourth `get_pull` MCP tool exposes the synthesized PR metadata.

**Tech Stack:** Go (huma, modernc.org/sqlite), hand-rolled stdio MCP, Svelte 5 (regen only).

---

## Conventions (apply to every task)

- **TDD:** write the failing test first, watch it fail, write the minimal implementation, watch it pass. Use `superpowers:test-driven-development`.
- **testify:** `require` for setup/preconditions, `assert` for checks. When a test has more than 3 assertions, create `assert := assert.New(t)` (or `Assert.New(t)` where the package aliases the import as `Assert`) and use the helper methods. NEVER `t.Fatal/Fatalf/Error/Errorf/Fail/FailNow`. Table-driven where natural.
- **Go test commands:** always `-shuffle=on`; NEVER pass `-count=1`; never `-v`. Run sandboxed FIRST (the repo auto-approves sandboxed bash). For the server package use `go test ./internal/server -short -shuffle=on` (the `-short` skips the real-tmux workspace e2e, which are orthogonal to Phase 3) plus a focused `-run` for new tests. Only suggest `dangerouslyDisableSandbox` if a command genuinely fails on a sandbox/permission/temp-dir error.
- **DB tests** use `openTestDB(t)`. **Server review-thread e2e** use the generated client in `internal/apiclient` and the `seedReviewWorktree(t, database)` helper (or the new `seedReviewWorktreeGit` helper added in Task 5).
- **Client regen** (Task 6 only): run `make api-generate` (writes `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, and the TS schema `packages/ui/src/api/generated/schema.ts`) THEN `go generate ./internal/apiclient/generated` (regenerates the Go client `internal/apiclient/generated/client.gen.go` from `internal/apiclient/spec/openapi.json` via oapi-codegen). Then `cd frontend && bun run check` must stay clean. If either hits a GOCACHE/temp error, prefix `GOCACHE=$HOME/.cache/go-build` (the `api-generate` target already defaults `GOCACHE` to `/tmp/middleman-gocache`, which is sandbox-writable).
- **Commits:** one commit per task. Stage EXPLICIT paths only (NEVER `git add -A`/`git add .` — the tree has untracked HOME dotfiles). Conventional commit messages. End every commit message with this trailer line:

  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```

  No push / no PR / no branch changes.

---

## Task 1 — Migration 000023: `branch` column on review threads + sessions

Add `branch TEXT NOT NULL DEFAULT ''` to both `middleman_review_threads` and `middleman_worktree_sessions`, backfilling existing rows from their worktree's scanned branch. The synthetic MR's `platform_id` equals `worktree.id` (see `internal/server/local_dispatch.go:113`), so review threads backfill via `mr_id → merge_requests.platform_id → middleman_worktrees.branch`. Sessions backfill directly via `worktree_id`. The down migration drops both columns (modernc.org/sqlite supports `ADD COLUMN` and `DROP COLUMN`). The current latest migration is `000022`; `000023` is the next free number.

- [ ] **Write the failing test.** Append to `internal/db/queries_review_threads_test.go`:

  ```go
  // TestBranchColumnsMigrationApplied proves migration 000023 added the
  // branch column to both review threads and worktree sessions, defaulting
  // to ''.
  func TestBranchColumnsMigrationApplied(t *testing.T) {
  	require := require.New(t)
  	d := openTestDB(t)
  	ctx := context.Background()

  	mrID := insertTestMRLocal(t, d)
  	threads, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
  		{Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "hi"},
  	})
  	require.NoError(err)
  	require.Len(threads, 1)

  	var threadBranch string
  	require.NoError(d.ReadDB().QueryRowContext(ctx,
  		`SELECT branch FROM middleman_review_threads WHERE id = ?`,
  		threads[0].ID).Scan(&threadBranch))
  	require.Equal("", threadBranch)

  	repoID, err := d.UpsertLocalRepo(ctx, "demo")
  	require.NoError(err)
  	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
  		Path: "/code/demo", Branch: "feat", HeadSHA: "aaaa",
  	})
  	require.NoError(err)
  	sess, err := d.CreateWorktreeSession(ctx, w.ID)
  	require.NoError(err)

  	var sessBranch string
  	require.NoError(d.ReadDB().QueryRowContext(ctx,
  		`SELECT branch FROM middleman_worktree_sessions WHERE id = ?`,
  		sess.ID).Scan(&sessBranch))
  	require.Equal("", sessBranch)
  }
  ```

  Note: `internal/db/db_test.go:TestOpenCreatesSchemaMigrationsTable` already asserts the schema version equals `latestMigrationVersion()` (computed from the embedded glob), so it picks up `000023` automatically once the files exist — no count constant to bump.

- [ ] **Run it — expect FAIL** (compiles, but `CreateWorktreeSession`/`CreateReviewThreads` insert into tables without the column only after migration runs; before the migration files exist the `SELECT branch` scan errors `no such column: branch`):

  ```
  go test ./internal/db -run 'TestBranchColumnsMigrationApplied' -shuffle=on
  ```

  Expected: `FAIL` with `no such column: branch`.

- [ ] **Write the up migration** `internal/db/migrations/000023_add_branch_to_review_threads_and_sessions.up.sql`:

  ```sql
  -- Branch-scope a worktree's review threads and its in-app --resume
  -- session. Phase 3 makes a "review" tied to its head branch (like a
  -- GitHub PR), so switching branches in one worktree no longer shares a
  -- thread-set or an agent conversation.
  ALTER TABLE middleman_review_threads
      ADD COLUMN branch TEXT NOT NULL DEFAULT '';

  ALTER TABLE middleman_worktree_sessions
      ADD COLUMN branch TEXT NOT NULL DEFAULT '';

  -- Backfill existing review-thread rows from their worktree's scanned
  -- branch. The synthetic MR's platform_id is the worktree id
  -- (see internal/server/local_dispatch.go), so:
  --   review_thread.mr_id -> merge_request.platform_id -> worktree.branch
  -- Rows that resolve to nothing keep ''.
  UPDATE middleman_review_threads
     SET branch = (
         SELECT w.branch
           FROM middleman_merge_requests mr
           JOIN middleman_worktrees w ON w.id = mr.platform_id
          WHERE mr.id = middleman_review_threads.mr_id
            AND mr.repo_id = w.repo_id
     )
   WHERE EXISTS (
         SELECT 1
           FROM middleman_merge_requests mr
           JOIN middleman_worktrees w ON w.id = mr.platform_id
          WHERE mr.id = middleman_review_threads.mr_id
            AND mr.repo_id = w.repo_id
   );

  -- Backfill active session rows from their worktree's scanned branch.
  UPDATE middleman_worktree_sessions
     SET branch = (
         SELECT w.branch
           FROM middleman_worktrees w
          WHERE w.id = middleman_worktree_sessions.worktree_id
     )
   WHERE EXISTS (
         SELECT 1
           FROM middleman_worktrees w
          WHERE w.id = middleman_worktree_sessions.worktree_id
   );
  ```

- [ ] **Write the down migration** `internal/db/migrations/000023_add_branch_to_review_threads_and_sessions.down.sql`:

  ```sql
  ALTER TABLE middleman_worktree_sessions DROP COLUMN branch;
  ALTER TABLE middleman_review_threads DROP COLUMN branch;
  ```

- [ ] **Run it — expect PASS:**

  ```
  go test ./internal/db -run 'TestBranchColumnsMigrationApplied|TestOpenCreatesSchemaMigrationsTable' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/db`.

- [ ] **Commit:**

  ```
  git add internal/db/migrations/000023_add_branch_to_review_threads_and_sessions.up.sql internal/db/migrations/000023_add_branch_to_review_threads_and_sessions.down.sql internal/db/queries_review_threads_test.go
  git commit -m "$(cat <<'EOF'
  feat(db): migration 000023 adds branch column to review threads + sessions

  Backfills review-thread rows via mr_id -> merge_request.platform_id ->
  worktree.branch and session rows via worktree_id. Down drops both columns.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 2 — DB layer: review-thread `Branch` field, stamp-on-create, filter-on-list

Add `Branch` to the `ReviewThread` struct, thread it through `scanReviewThread`, write it on create, and add a branch-filtered list query. Legacy `''` rows stay visible (migration cushion).

- [ ] **Write the failing test.** Append to `internal/db/queries_review_threads_test.go`:

  ```go
  func TestListReviewThreadsForBranchFiltersAndKeepsLegacy(t *testing.T) {
  	require := require.New(t)
  	assert := Assert.New(t)
  	d := openTestDB(t)
  	ctx := context.Background()
  	mrID := insertTestMRLocal(t, d)

  	// Two branch-stamped threads and one legacy ('') thread.
  	_, err := d.CreateReviewThreadsOnBranch(ctx, mrID, "a", []NewReviewThread{
  		{Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "on a"},
  	})
  	require.NoError(err)
  	_, err = d.CreateReviewThreadsOnBranch(ctx, mrID, "b", []NewReviewThread{
  		{Path: "b.go", Side: "RIGHT", Line: 2, CommitSHA: "abc", Body: "on b"},
  	})
  	require.NoError(err)
  	legacy, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
  		{Path: "c.go", Side: "RIGHT", Line: 3, CommitSHA: "abc", Body: "legacy"},
  	})
  	require.NoError(err)
  	assert.Equal("", legacy[0].Branch)

  	onA, err := d.ListReviewThreadsForMRBranch(ctx, mrID, "a")
  	require.NoError(err)
  	paths := make([]string, 0, len(onA))
  	for _, th := range onA {
  		paths = append(paths, th.Path)
  	}
  	// "a" branch threads plus the legacy '' thread; never "b".
  	assert.ElementsMatch([]string{"a.go", "c.go"}, paths)

  	got, err := d.GetReviewThread(ctx, onA[0].ID)
  	require.NoError(err)
  	assert.Contains([]string{"a", ""}, got.Branch)
  }
  ```

- [ ] **Run it — expect FAIL** (undefined: `CreateReviewThreadsOnBranch`, `ListReviewThreadsForMRBranch`, and `ReviewThread.Branch`):

  ```
  go test ./internal/db -run 'TestListReviewThreadsForBranchFiltersAndKeepsLegacy' -shuffle=on
  ```

  Expected: `FAIL` (build error: undefined methods/field).

- [ ] **Add the `Branch` field** to the `ReviewThread` struct in `internal/db/types.go` (this struct is currently in `internal/db/queries_review_threads.go`; the map says `types.go` holds the struct — it is actually defined at the top of `queries_review_threads.go`, lines 14-26). Edit that struct:

  ```go
  // ReviewThread is one anchored review-comment thread on a (local)
  // merge request. The "review" for a worktree is the living set of these
  // threads on the worktree's synthetic MR.
  type ReviewThread struct {
  	ID             int64
  	MergeRequestID int64
  	Path           string
  	Side           string // "LEFT" | "RIGHT"
  	Line           int
  	StartLine      *int // nullable; multi-line selection start
  	CommitSHA      string
  	Status         string // "open" | "discussed" | "applied" | "resolved"
  	Branch         string // worktree branch this thread is scoped to ("" = legacy/unscoped)
  	HiddenAt       *time.Time
  	CreatedAt      time.Time
  	UpdatedAt      time.Time
  }
  ```

- [ ] **Thread `Branch` through `scanReviewThread`** in `internal/db/queries_review_threads.go`. Replace the function body so it selects and scans `branch`:

  ```go
  func scanReviewThread(row scanner) (ReviewThread, error) {
  	var t ReviewThread
  	var startLine sql.NullInt64
  	var hiddenAt sql.NullTime
  	err := row.Scan(
  		&t.ID, &t.MergeRequestID, &t.Path, &t.Side, &t.Line,
  		&startLine, &t.CommitSHA, &t.Status, &t.Branch, &hiddenAt,
  		&t.CreatedAt, &t.UpdatedAt,
  	)
  	if err != nil {
  		if errors.Is(err, sql.ErrNoRows) {
  			return ReviewThread{}, err
  		}
  		return ReviewThread{}, fmt.Errorf("scan review thread: %w", err)
  	}
  	if startLine.Valid {
  		v := int(startLine.Int64)
  		t.StartLine = &v
  	}
  	if hiddenAt.Valid {
  		t.HiddenAt = &hiddenAt.Time
  	}
  	return t, nil
  }
  ```

- [ ] **Update the two SELECTs that feed `scanReviewThread`** to include `branch` (the column order must match the scan above: after `status`, before `hidden_at`).

  In `GetReviewThread`:

  ```go
  // GetReviewThread returns a single review thread by its ID.
  func (d *DB) GetReviewThread(ctx context.Context, id int64) (ReviewThread, error) {
  	return scanReviewThread(d.ro.QueryRowContext(ctx, `
  		SELECT id, mr_id, path, side, line, start_line, commit_sha,
  		       status, branch, hidden_at, created_at, updated_at
  		  FROM middleman_review_threads WHERE id = ?`, id))
  }
  ```

  In `ListReviewThreadsForMR`:

  ```go
  // ListReviewThreadsForMR returns all threads for an MR, oldest-first.
  // Hidden threads are included (the response carries a hidden_at field);
  // the UI filters them. Comments are loaded separately.
  func (d *DB) ListReviewThreadsForMR(ctx context.Context, mrID int64) ([]ReviewThread, error) {
  	rows, err := d.ro.QueryContext(ctx, `
  		SELECT id, mr_id, path, side, line, start_line, commit_sha,
  		       status, branch, hidden_at, created_at, updated_at
  		  FROM middleman_review_threads
  		 WHERE mr_id = ?
  		 ORDER BY id ASC`, mrID)
  	if err != nil {
  		return nil, fmt.Errorf("list review threads: %w", err)
  	}
  	defer rows.Close()
  	var out []ReviewThread
  	for rows.Next() {
  		t, err := scanReviewThread(rows)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, t)
  	}
  	return out, rows.Err()
  }
  ```

- [ ] **Refactor create to carry a branch.** Replace `CreateReviewThreads` so the existing zero-arg signature is preserved (stamps `''`) and a branch-aware variant does the real work. In `internal/db/queries_review_threads.go`, replace the whole `CreateReviewThreads` function with:

  ```go
  // CreateReviewThreads inserts a batch of threads on the unscoped ('')
  // branch. Retained for callers/tests that don't supply a branch.
  func (d *DB) CreateReviewThreads(ctx context.Context, mrID int64, in []NewReviewThread) ([]ReviewThread, error) {
  	return d.CreateReviewThreadsOnBranch(ctx, mrID, "", in)
  }

  // CreateReviewThreadsOnBranch inserts a batch of threads (each with its
  // root 'user' comment) for one MR in a single transaction, stamping each
  // with branch, and returns the created thread rows in input order.
  func (d *DB) CreateReviewThreadsOnBranch(ctx context.Context, mrID int64, branch string, in []NewReviewThread) ([]ReviewThread, error) {
  	tx, err := d.rw.BeginTx(ctx, nil)
  	if err != nil {
  		return nil, fmt.Errorf("begin tx: %w", err)
  	}
  	defer func() { _ = tx.Rollback() }()

  	ids := make([]int64, 0, len(in))
  	for _, t := range in {
  		res, err := tx.ExecContext(ctx, `
  			INSERT INTO middleman_review_threads
  				(mr_id, path, side, line, start_line, commit_sha, branch)
  			VALUES (?, ?, ?, ?, ?, ?, ?)`,
  			mrID, t.Path, t.Side, t.Line, intPtrToNullable(t.StartLine), t.CommitSHA, branch,
  		)
  		if err != nil {
  			return nil, fmt.Errorf("insert thread: %w", err)
  		}
  		threadID, err := res.LastInsertId()
  		if err != nil {
  			return nil, fmt.Errorf("last insert id: %w", err)
  		}
  		if _, err := tx.ExecContext(ctx, `
  			INSERT INTO middleman_review_thread_comments (thread_id, author, body)
  			VALUES (?, 'user', ?)`,
  			threadID, t.Body,
  		); err != nil {
  			return nil, fmt.Errorf("insert root comment: %w", err)
  		}
  		ids = append(ids, threadID)
  	}
  	if err := tx.Commit(); err != nil {
  		return nil, fmt.Errorf("commit: %w", err)
  	}

  	out := make([]ReviewThread, 0, len(ids))
  	for _, id := range ids {
  		th, err := d.GetReviewThread(ctx, id)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, th)
  	}
  	return out, nil
  }
  ```

- [ ] **Add the branch-filtered list query.** Add to `internal/db/queries_review_threads.go` (just after `ListReviewThreadsForMR`):

  ```go
  // ListReviewThreadsForMRBranch returns the MR's threads scoped to branch,
  // oldest-first. Legacy rows with branch = '' are always included so a
  // pre-migration thread never silently disappears.
  func (d *DB) ListReviewThreadsForMRBranch(ctx context.Context, mrID int64, branch string) ([]ReviewThread, error) {
  	rows, err := d.ro.QueryContext(ctx, `
  		SELECT id, mr_id, path, side, line, start_line, commit_sha,
  		       status, branch, hidden_at, created_at, updated_at
  		  FROM middleman_review_threads
  		 WHERE mr_id = ? AND (branch = ? OR branch = '')
  		 ORDER BY id ASC`, mrID, branch)
  	if err != nil {
  		return nil, fmt.Errorf("list review threads for branch: %w", err)
  	}
  	defer rows.Close()
  	var out []ReviewThread
  	for rows.Next() {
  		t, err := scanReviewThread(rows)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, t)
  	}
  	return out, rows.Err()
  }
  ```

- [ ] **Run it — expect PASS:**

  ```
  go test ./internal/db -run 'TestListReviewThreadsForBranchFiltersAndKeepsLegacy|TestCreateAndListReviewThreads|TestReviewThreadCommentsAndState' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/db`.

- [ ] **Commit:**

  ```
  git add internal/db/queries_review_threads.go internal/db/queries_review_threads_test.go
  git commit -m "$(cat <<'EOF'
  feat(db): branch-scope review threads (stamp on create, filter on list)

  ReviewThread.Branch + scan/SELECT plumbing; CreateReviewThreadsOnBranch
  and ListReviewThreadsForMRBranch (legacy '' rows stay visible).

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 3 — DB layer: session `Branch` field, branch-keyed active session

Add `Branch` to `WorktreeSession`, thread it through `scanWorktreeSession`, and make `GetActiveWorktreeSession`/`CreateWorktreeSession` branch-aware so two branches in one worktree get two distinct active sessions.

- [ ] **Write the failing test.** Append to `internal/db/queries_sessions_test.go`:

  ```go
  func TestActiveWorktreeSessionIsBranchScoped(t *testing.T) {
  	require := require.New(t)
  	assert := assert.New(t)
  	d := openTestDB(t)
  	ctx := context.Background()

  	repoID := insertTestRepo(t, d, "o", "r")
  	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
  		Path: "/code/o/r", Branch: "a", HeadSHA: "aaaa",
  	})
  	require.NoError(err)

  	// No active session on either branch yet.
  	_, err = d.GetActiveWorktreeSession(ctx, w.ID, "a")
  	assert.True(errors.Is(err, sql.ErrNoRows))

  	sessA, err := d.CreateWorktreeSession(ctx, w.ID, "a")
  	require.NoError(err)
  	sessB, err := d.CreateWorktreeSession(ctx, w.ID, "b")
  	require.NoError(err)
  	assert.NotEqual(sessA.ID, sessB.ID)
  	assert.Equal("a", sessA.Branch)
  	assert.Equal("b", sessB.Branch)

  	gotA, err := d.GetActiveWorktreeSession(ctx, w.ID, "a")
  	require.NoError(err)
  	assert.Equal(sessA.ID, gotA.ID)

  	gotB, err := d.GetActiveWorktreeSession(ctx, w.ID, "b")
  	require.NoError(err)
  	assert.Equal(sessB.ID, gotB.ID)
  }
  ```

- [ ] **Run it — expect FAIL** (build error: `GetActiveWorktreeSession`/`CreateWorktreeSession` take 2 args, not 3; `WorktreeSession.Branch` undefined):

  ```
  go test ./internal/db -run 'TestActiveWorktreeSessionIsBranchScoped' -shuffle=on
  ```

  Expected: `FAIL` (build error).

- [ ] **Add the `Branch` field** to `WorktreeSession` in `internal/db/types.go`:

  ```go
  // WorktreeSession is an interactive Claude session bound to one
  // worktree. The session is the agent loop the user drives from the
  // Activity tab; review-feedback submissions and free-text follow-ups
  // land here as turns.
  type WorktreeSession struct {
  	ID              int64
  	WorktreeID      int64
  	Branch          string // worktree branch this session is scoped to ("" = legacy)
  	ClaudeSessionID string // populated after the first claude --output-format=json reply
  	Status          string // "active" | "killed" | "closed"
  	StartedAt       time.Time
  	LastActivityAt  time.Time
  }
  ```

- [ ] **Make the session queries branch-aware** in `internal/db/queries_sessions.go`. Replace `GetActiveWorktreeSession`, `GetWorktreeSession`, `CreateWorktreeSession`, and `scanWorktreeSession`:

  ```go
  // GetActiveWorktreeSession returns the live (status='active')
  // session row for a (worktree, branch), or sql.ErrNoRows when there
  // isn't one. Callers use this to decide whether to start a new
  // session or resume the existing one.
  func (d *DB) GetActiveWorktreeSession(
  	ctx context.Context, worktreeID int64, branch string,
  ) (WorktreeSession, error) {
  	row := d.ro.QueryRowContext(ctx,
  		`SELECT id, worktree_id, branch, claude_session_id, status,
  		        started_at, last_activity_at
  		   FROM middleman_worktree_sessions
  		  WHERE worktree_id = ? AND branch = ? AND status = 'active'
  		  ORDER BY id DESC
  		  LIMIT 1`,
  		worktreeID, branch,
  	)
  	return scanWorktreeSession(row)
  }

  // GetWorktreeSession returns a session by id regardless of status.
  func (d *DB) GetWorktreeSession(
  	ctx context.Context, id int64,
  ) (WorktreeSession, error) {
  	row := d.ro.QueryRowContext(ctx,
  		`SELECT id, worktree_id, branch, claude_session_id, status,
  		        started_at, last_activity_at
  		   FROM middleman_worktree_sessions
  		  WHERE id = ?`,
  		id,
  	)
  	return scanWorktreeSession(row)
  }

  // CreateWorktreeSession opens a fresh active session for a
  // (worktree, branch).
  func (d *DB) CreateWorktreeSession(
  	ctx context.Context, worktreeID int64, branch string,
  ) (WorktreeSession, error) {
  	now := time.Now().UTC()
  	res, err := d.rw.ExecContext(ctx,
  		`INSERT INTO middleman_worktree_sessions
  		    (worktree_id, branch, status, started_at, last_activity_at)
  		 VALUES (?, ?, 'active', ?, ?)`,
  		worktreeID, branch, now, now,
  	)
  	if err != nil {
  		return WorktreeSession{}, fmt.Errorf("create worktree session: %w", err)
  	}
  	id, err := res.LastInsertId()
  	if err != nil {
  		return WorktreeSession{}, err
  	}
  	return d.GetWorktreeSession(ctx, id)
  }
  ```

  And update the scanner (add `&s.Branch` between `WorktreeID` and `ClaudeSessionID` to match the SELECT order):

  ```go
  func scanWorktreeSession(row rowScanner) (WorktreeSession, error) {
  	var s WorktreeSession
  	err := row.Scan(
  		&s.ID, &s.WorktreeID, &s.Branch, &s.ClaudeSessionID, &s.Status,
  		&s.StartedAt, &s.LastActivityAt,
  	)
  	if errors.Is(err, sql.ErrNoRows) {
  		return WorktreeSession{}, err
  	}
  	if err != nil {
  		return WorktreeSession{}, fmt.Errorf("scan worktree session: %w", err)
  	}
  	return s, nil
  }
  ```

- [ ] **Update the existing same-package test caller.** `internal/db/queries_sessions_test.go:TestWorktreeSessionLifecycle` calls `CreateWorktreeSession(ctx, w.ID)` and `GetActiveWorktreeSession(ctx, w.ID)`. Add the branch arg `"feat"` to match `w.Branch` in that test (both call sites; the `GetActiveWorktreeSession` after the kill must also pass `"feat"`):

  ```go
  	sess, err := d.CreateWorktreeSession(ctx, w.ID, "feat")
  ```
  ```go
  	live, err := d.GetActiveWorktreeSession(ctx, w.ID, "feat")
  ```
  ```go
  	_, err = d.GetActiveWorktreeSession(ctx, w.ID, "feat")
  ```

  And `TestWorktreeSessionTurns` calls `CreateWorktreeSession(ctx, w.ID)` (its worktree `Branch: "feat"`); change to:

  ```go
  	sess, err := d.CreateWorktreeSession(ctx, w.ID, "feat")
  ```

- [ ] **Run it — expect PASS** (the server package will not compile yet because its `ensureWorktreeSession`/handlers still call the 2-arg form; that is fixed in Task 5. Run only the db package here):

  ```
  go test ./internal/db -run 'TestActiveWorktreeSessionIsBranchScoped|TestWorktreeSessionLifecycle|TestWorktreeSessionTurns' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/db`.

- [ ] **Commit:**

  ```
  git add internal/db/types.go internal/db/queries_sessions.go internal/db/queries_sessions_test.go
  git commit -m "$(cat <<'EOF'
  feat(db): branch-scope the active worktree session

  GetActiveWorktreeSession/CreateWorktreeSession take a branch; scan reads
  it. Two branches in one worktree get two distinct active sessions.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 4 — `worktrees.CurrentBranch` + server `currentWorktreeBranch` helper

Add a git helper that reads the worktree's live branch (`git rev-parse --abbrev-ref HEAD`), returning `""` for a detached HEAD (mirroring how `ensureSyntheticMRForWorktree` treats detached). Add a thin server helper that calls it and falls back to the scanned `w.Branch` on error.

- [ ] **Write the failing test** `internal/worktrees/branches_current_test.go`:

  ```go
  package worktrees

  import (
  	"context"
  	"os/exec"
  	"testing"

  	"github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  )

  func TestCurrentBranch(t *testing.T) {
  	if _, err := exec.LookPath("git"); err != nil {
  		t.Skip("git not available on PATH")
  	}
  	require := require.New(t)
  	assert := assert.New(t)
  	ctx := context.Background()

  	dir := t.TempDir()
  	runGitT(t, "", "init", "--initial-branch=main", dir)
  	runGitT(t, dir, "config", "user.email", "test@example.com")
  	runGitT(t, dir, "config", "user.name", "Test")
  	runGitT(t, dir, "commit", "--allow-empty", "-m", "c1")

  	got, err := CurrentBranch(ctx, dir)
  	require.NoError(err)
  	assert.Equal("main", got)

  	runGitT(t, dir, "checkout", "-b", "feat/x")
  	got, err = CurrentBranch(ctx, dir)
  	require.NoError(err)
  	assert.Equal("feat/x", got)

  	// Detached HEAD reports as "" (matches the synthetic-MR convention).
  	sha := gitHeadT(t, dir)
  	runGitT(t, dir, "checkout", sha)
  	got, err = CurrentBranch(ctx, dir)
  	require.NoError(err)
  	assert.Equal("", got)
  }

  func TestCurrentBranchErrorsOnNonRepo(t *testing.T) {
  	require := require.New(t)
  	_, err := CurrentBranch(context.Background(), t.TempDir())
  	require.Error(err)
  }
  ```

  (`runGitT` and `gitHeadT` already exist in the package's `changes_test.go` / `branches_test.go`.)

- [ ] **Run it — expect FAIL** (undefined: `CurrentBranch`):

  ```
  go test ./internal/worktrees -run 'TestCurrentBranch' -shuffle=on
  ```

  Expected: `FAIL` (build error: undefined `CurrentBranch`).

- [ ] **Implement `CurrentBranch`.** Append to `internal/worktrees/branches.go` (it already imports `context`, `fmt`, `strings`; `gitCmd` lives in `changes.go`, same package):

  ```go
  // CurrentBranch returns the worktree's live checked-out branch via
  // `git rev-parse --abbrev-ref HEAD`. A detached HEAD prints "HEAD",
  // which we normalize to "" — the same convention the synthetic MR uses
  // for a detached worktree. Returns an error when the path is not a git
  // worktree (callers fall back to the scanned branch).
  func CurrentBranch(ctx context.Context, worktreePath string) (string, error) {
  	if worktreePath == "" {
  		return "", fmt.Errorf("worktreePath is required")
  	}
  	out, err := gitCmd(ctx, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
  	if err != nil {
  		return "", fmt.Errorf("rev-parse --abbrev-ref HEAD: %w", err)
  	}
  	branch := strings.TrimSpace(string(out))
  	if branch == "HEAD" {
  		return "", nil // detached
  	}
  	return branch, nil
  }
  ```

- [ ] **Add the server helper.** Add to `internal/server/local_dispatch.go` (just after `ensureSyntheticMRForWorktree`, ~L130). The package already imports `worktrees` and `db`:

  ```go
  // currentWorktreeBranch reads the worktree's live current branch so a
  // branch switch takes effect immediately (no wait for the periodic
  // scan). On any git error it falls back to the scanned worktrees.branch
  // so the path stays robust. This is the single source of truth the
  // in-app UI and the external proxy both agree on.
  func (s *Server) currentWorktreeBranch(ctx context.Context, w *db.Worktree) string {
  	if branch, err := worktrees.CurrentBranch(ctx, w.Path); err == nil {
  		return branch
  	}
  	return w.Branch
  }
  ```

- [ ] **Run it — expect PASS:**

  ```
  go test ./internal/worktrees -run 'TestCurrentBranch|TestCurrentBranchErrorsOnNonRepo' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/worktrees`. (The server package won't build until Task 5 wires the helper in; that's fine — only the worktrees package is run here.)

- [ ] **Commit:**

  ```
  git add internal/worktrees/branches.go internal/worktrees/branches_current_test.go internal/server/local_dispatch.go
  git commit -m "$(cat <<'EOF'
  feat(worktrees): CurrentBranch helper + server currentWorktreeBranch

  git rev-parse --abbrev-ref HEAD (detached -> ""); the server helper
  falls back to the scanned branch on git error.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 5 — Server wiring: create stamps branch, list filters, sessions branch-scoped

Wire the live branch through the review-thread create/list handlers and the session machinery. `createReviewThreads` stamps the current branch; `loadReviewThreadsResponse` filters by it; `ensureWorktreeSession` and all session endpoints pass the current branch.

- [ ] **Write the failing e2e test** `internal/server/review_threads_branch_e2e_test.go`. It seeds a *real* git worktree (so `CurrentBranch` works), creates threads on `feat/a`, switches the worktree to `feat/b`, and asserts the list now returns only the legacy-or-`feat/b` set (i.e. not the `feat/a` threads):

  ```go
  package server

  import (
  	"context"
  	"net/http"
  	"os/exec"
  	"testing"

  	Assert "github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  	"github.com/wesm/middleman/internal/apiclient/generated"
  	"github.com/wesm/middleman/internal/db"
  )

  // seedReviewWorktreeGit registers a local repo + a worktree backed by a
  // REAL git repo (so the server's currentWorktreeBranch can read a live
  // branch). Returns the worktree id (PR-shaped "number") and its on-disk
  // path so the test can switch branches.
  func seedReviewWorktreeGit(t *testing.T, database *db.DB) (int64, string) {
  	t.Helper()
  	ctx := context.Background()
  	dir := t.TempDir()
  	runGit(t, dir, "init", "--initial-branch=feat/a", dir)
  	runGit(t, dir, "config", "user.email", "test@example.com")
  	runGit(t, dir, "config", "user.name", "Test")
  	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")

  	repoID, err := database.UpsertLocalRepo(ctx, "demo")
  	require.NoError(t, err)
  	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
  		Path: dir, Branch: "feat/a", HeadSHA: "deadbeef",
  	})
  	require.NoError(t, err)
  	return w.ID, dir
  }

  func TestAPIReviewThreadsBranchScoped(t *testing.T) {
  	if _, err := exec.LookPath("git"); err != nil {
  		t.Skip("git not available on PATH")
  	}
  	require := require.New(t)
  	assert := Assert.New(t)
  	srv, database := setupTestServer(t)
  	client := setupTestClient(t, srv)
  	ctx := context.Background()
  	num, dir := seedReviewWorktreeGit(t, database)

  	// Create one thread while on feat/a.
  	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
  		ctx, "local", "demo", num,
  		generated.CreateReviewThreadsInputBody{
  			Threads: &[]generated.ReviewThreadDraft{
  				{Path: "a.go", Side: "RIGHT", Line: 1, CommitSha: "abc", Body: "on a"},
  			},
  		},
  	)
  	require.NoError(err)
  	require.Equal(http.StatusOK, createResp.StatusCode())

  	// Listing on feat/a sees the thread.
  	listA, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
  		ctx, "local", "demo", num)
  	require.NoError(err)
  	require.Equal(http.StatusOK, listA.StatusCode())
  	require.NotNil(listA.JSON200)
  	require.NotNil(listA.JSON200.Threads)
  	assert.Len(*listA.JSON200.Threads, 1)

  	// Switch the worktree to feat/b.
  	runGit(t, dir, "checkout", "-b", "feat/b")

  	// Now listing returns the feat/b set (empty) — the feat/a thread is
  	// scoped out.
  	listB, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
  		ctx, "local", "demo", num)
  	require.NoError(err)
  	require.Equal(http.StatusOK, listB.StatusCode())
  	require.NotNil(listB.JSON200)
  	threadsB := []generated.ReviewThread{}
  	if listB.JSON200.Threads != nil {
  		threadsB = *listB.JSON200.Threads
  	}
  	assert.Empty(threadsB)

  	// Creating on feat/b, then switching back to feat/a, shows the
  	// original feat/a thread again (and not the feat/b one).
  	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
  		ctx, "local", "demo", num,
  		generated.CreateReviewThreadsInputBody{
  			Threads: &[]generated.ReviewThreadDraft{
  				{Path: "b.go", Side: "RIGHT", Line: 2, CommitSha: "abc", Body: "on b"},
  			},
  		},
  	)
  	require.NoError(err)
  	runGit(t, dir, "checkout", "feat/a")
  	listA2, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
  		ctx, "local", "demo", num)
  	require.NoError(err)
  	require.NotNil(listA2.JSON200)
  	require.NotNil(listA2.JSON200.Threads)
  	paths := make([]string, 0)
  	for _, th := range *listA2.JSON200.Threads {
  		paths = append(paths, th.Path)
  	}
  	assert.Equal([]string{"a.go"}, paths)
  }
  ```

  (`runGit` is the existing server-package git test helper used by `setupTestServerForAIReview`; the generated client method names mirror `TestAPIReviewThreadsLifecycle` in `review_threads_e2e_test.go`.)

- [ ] **Run it — expect FAIL** (the create/list handlers still ignore branch, so the `feat/b` listing returns the `feat/a` thread → `assert.Empty(threadsB)` fails):

  ```
  go test ./internal/server -short -run 'TestAPIReviewThreadsBranchScoped' -shuffle=on
  ```

  Expected: `FAIL` on `Should be empty, but was [...]`.

- [ ] **Make `loadReviewThreadsResponse` branch-aware.** In `internal/server/huma_routes_review_threads.go`, change its signature to take a branch and use the filtered query. Replace the function:

  ```go
  // loadReviewThreadsResponse lists an MR's threads (scoped to branch) with
  // their comments grouped in. Shared by the list and create handlers.
  func (s *Server) loadReviewThreadsResponse(ctx context.Context, mrID int64, branch string) ([]reviewThreadResponse, error) {
  	threads, err := s.db.ListReviewThreadsForMRBranch(ctx, mrID, branch)
  	if err != nil {
  		return nil, err
  	}
  	comments, err := s.db.ListReviewThreadCommentsForMR(ctx, mrID)
  	if err != nil {
  		return nil, err
  	}
  	byThread := map[int64][]reviewThreadCommentResponse{}
  	for _, c := range comments {
  		byThread[c.ThreadID] = append(byThread[c.ThreadID], reviewThreadCommentResponse{
  			ID:          c.ID,
  			Author:      c.Author,
  			Body:        c.Body,
  			SentToAgent: c.SentToAgent,
  			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
  		})
  	}
  	out := make([]reviewThreadResponse, 0, len(threads))
  	for _, t := range threads {
  		out = append(out, toReviewThreadResponse(t, byThread[t.ID]))
  	}
  	return out, nil
  }
  ```

  (`ListReviewThreadCommentsForMR` returns comments across all the MR's threads keyed by thread_id; comments for threads filtered out are simply never referenced. No extra filtering needed.)

- [ ] **Thread the current branch through the handlers in `huma_routes_review_threads.go`.** Each call site that currently does `loadReviewThreadsResponse(ctx, mrID)` must resolve the worktree and pass its current branch. The handlers already have `input.Owner/Name/Number`. Update each:

  In `listReviewThreads`:

  ```go
  func (s *Server) listReviewThreads(ctx context.Context, input *listReviewThreadsInput) (*listReviewThreadsOutput, error) {
  	if !isLocalSource(input.Owner) {
  		return nil, huma.Error400BadRequest("review threads are local-worktree only")
  	}
  	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
  	if err != nil {
  		return nil, huma.Error404NotFound("worktree not found")
  	}
  	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
  	if err != nil {
  		return nil, huma.Error404NotFound("worktree not found")
  	}
  	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
  	if err != nil {
  		return nil, huma.Error500InternalServerError("list review threads: " + err.Error())
  	}
  	out := &listReviewThreadsOutput{}
  	out.Body.Threads = threads
  	return out, nil
  }
  ```

  In `createReviewThreads`: resolve the worktree, compute the branch once, stamp on create, and reload with the branch. Replace the body from the `resolveOrEnsureMRID` line through the end:

  ```go
  	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
  	if err != nil {
  		return nil, huma.Error404NotFound("worktree not found")
  	}
  	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
  	if err != nil {
  		return nil, huma.Error404NotFound("worktree not found")
  	}
  	branch := s.currentWorktreeBranch(ctx, w)

  	in := make([]db.NewReviewThread, 0, len(input.Body.Threads))
  	for _, t := range input.Body.Threads {
  		if t.Side != "LEFT" && t.Side != "RIGHT" {
  			return nil, huma.Error400BadRequest("side must be LEFT or RIGHT")
  		}
  		if t.Path == "" {
  			return nil, huma.Error400BadRequest("path is required")
  		}
  		if t.Line < 1 {
  			return nil, huma.Error400BadRequest("line must be >= 1")
  		}
  		if t.CommitSHA == "" {
  			return nil, huma.Error400BadRequest("commit_sha is required")
  		}
  		if t.Body == "" {
  			return nil, huma.Error400BadRequest("each thread needs a comment body")
  		}
  		in = append(in, db.NewReviewThread{
  			Path: t.Path, Side: t.Side, Line: t.Line,
  			StartLine: t.StartLine, CommitSHA: t.CommitSHA, Body: t.Body,
  		})
  	}
  	created, err := s.db.CreateReviewThreadsOnBranch(ctx, mrID, branch, in)
  	if err != nil {
  		return nil, huma.Error500InternalServerError("create review threads: " + err.Error())
  	}
  	switch input.Body.Mode {
  	case "discuss-first":
  		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "discuss", created, ""); err != nil {
  			return nil, err
  		}
  	case "act-immediately":
  		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "apply", created, ""); err != nil {
  			return nil, err
  		}
  	}
  	threads, err := s.loadReviewThreadsResponse(ctx, mrID, branch)
  	if err != nil {
  		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
  	}
  	out := &createReviewThreadsOutput{}
  	out.Body.Threads = threads
  	return out, nil
  ```

  (`created` already holds the freshly inserted rows for the kickoff calls; the `mrID`/validation switch above is unchanged from the original except for using `CreateReviewThreadsOnBranch` and the branch-aware reload. Keep the existing mode-validation block — the lines that reject an invalid `input.Body.Mode` before persisting — directly above the worktree resolve, unchanged.)

  In `deleteReviewThread`, `applyReviewThread`, and `applyAllReviewThreads`, the trailing `s.loadReviewThreadsResponse(ctx, mrID)` calls need the branch. Each already resolves `mrID`; resolve the worktree for the branch. The simplest, consistent edit: at the top of each (after the `isLocalSource` guard), resolve the worktree and compute `branch := s.currentWorktreeBranch(ctx, w)`, then pass it. For `deleteReviewThread`:

  ```go
  func (s *Server) deleteReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
  	if !isLocalSource(input.Owner) {
  		return nil, huma.Error400BadRequest("review threads are local-worktree only")
  	}
  	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
  	if err != nil {
  		return nil, huma.Error404NotFound("worktree not found")
  	}
  	mrID, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID)
  	if err != nil {
  		return nil, err
  	}
  	if err := s.db.DeleteReviewThread(ctx, input.ThreadID); err != nil {
  		return nil, huma.Error500InternalServerError("delete thread: " + err.Error())
  	}
  	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
  	if err != nil {
  		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
  	}
  	out := &listReviewThreadsOutput{}
  	out.Body.Threads = threads
  	return out, nil
  }
  ```

  For `applyReviewThread`, add the same `w` resolve at the top and change the final reload to `s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))`. For `applyAllReviewThreads`, add the `w` resolve at the top (after the `isLocalSource` guard, before `resolveOrEnsureMRID`), compute `branch := s.currentWorktreeBranch(ctx, w)`, and **scope the eligible set to the current branch**: change the eligible-set query (currently `threads, err := s.db.ListReviewThreadsForMR(ctx, mrID)` at ~L133) to `s.db.ListReviewThreadsForMRBranch(ctx, mrID, branch)`, then pass `branch` to the final reload. Apply-all must apply only the current branch's open/discussed threads, never another branch's — those threads' `commit_sha`/line anchors don't exist on the current checkout, so applying them would be wrong.

- [ ] **Make `ensureWorktreeSession` branch-aware.** In `internal/server/huma_routes_sessions.go`, replace the helper so it takes the branch and forwards it to the branch-aware DB calls:

  ```go
  // ensureWorktreeSession returns the active session for a (worktree,
  // branch), creating one if none exists. The bool is isFirstTurn: true
  // when the session was just created, or when it exists but Claude hasn't
  // ack'd a claude_session_id yet (so the prompt re-primes worktree
  // context).
  func (s *Server) ensureWorktreeSession(ctx context.Context, worktreeID int64, branch string) (db.WorktreeSession, bool, error) {
  	sess, err := s.db.GetActiveWorktreeSession(ctx, worktreeID, branch)
  	if errors.Is(err, sql.ErrNoRows) {
  		sess, err = s.db.CreateWorktreeSession(ctx, worktreeID, branch)
  		if err != nil {
  			return db.WorktreeSession{}, false, err
  		}
  		return sess, true, nil
  	}
  	if err != nil {
  		return db.WorktreeSession{}, false, err
  	}
  	if sess.ClaudeSessionID == "" {
  		return sess, true, nil // exists but Claude hasn't ack'd → re-prime
  	}
  	return sess, false, nil
  }
  ```

- [ ] **Update the session-handler call sites** in `internal/server/huma_routes_sessions.go`. All four (`getWorktreeSession`, `submitWorktreeSessionTurn`, `killWorktreeSession`, and `ensureWorktreeSession`'s callers) already resolve `w` via `resolveLocalWorktree`. Pass `s.currentWorktreeBranch(ctx, w)`:

  In `getWorktreeSession`:

  ```go
  	sess, err := s.db.GetActiveWorktreeSession(ctx, w.ID, s.currentWorktreeBranch(ctx, w))
  ```

  In `submitWorktreeSessionTurn`:

  ```go
  	sess, isFirstTurn, err := s.ensureWorktreeSession(ctx, w.ID, s.currentWorktreeBranch(ctx, w))
  ```

  In `killWorktreeSession`:

  ```go
  	sess, err := s.db.GetActiveWorktreeSession(ctx, w.ID, s.currentWorktreeBranch(ctx, w))
  ```

- [ ] **Update `kickoffReviewTurn`** in `internal/server/huma_routes_review_threads.go`. Its `ensureWorktreeSession(ctx, w.ID)` call (~L435) gains the branch. The function already resolves `w`:

  ```go
  	sess, isFirst, err := s.ensureWorktreeSession(ctx, w.ID, s.currentWorktreeBranch(ctx, w))
  ```

- [ ] **Run it — expect PASS** (and run the existing review-thread + session e2e to confirm no regression):

  ```
  go test ./internal/server -short -run 'TestAPIReviewThreadsBranchScoped|TestAPIReviewThreads|TestMCPProxyReplyHitsRealAPIPath|TestWorktreeSession|Session' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/server`. Then a full package compile/run:

  ```
  go test ./internal/server -short -shuffle=on
  ```

  Expected: `ok` (existing `seedReviewWorktree`-based tests use a non-git tempdir, so `currentWorktreeBranch` falls back to the scanned `"feat/x"` — those threads still list because they're stamped `"feat/x"` on create and listed for `"feat/x"`).

- [ ] **Commit:**

  ```
  git add internal/server/huma_routes_review_threads.go internal/server/huma_routes_sessions.go internal/server/review_threads_branch_e2e_test.go
  git commit -m "$(cat <<'EOF'
  feat(server): branch-scope review threads + in-app session

  Create stamps the live current branch; list filters by it;
  ensureWorktreeSession and the session endpoints pass the branch so a
  branch switch starts a fresh --resume conversation.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 6 — `/local/resolve` endpoint + client regen

Add `GET /api/v1/local/resolve?path=<abs worktree path>` → `{owner:"local", name, number, branch}`, matching the canonicalized path against an active worktree across repos, 404 when none matches. Then regenerate the Go + TS clients.

- [ ] **Add a DB lookup by path.** Append to `internal/db/queries_worktrees.go` (mirrors `ListAllActiveWorktrees`'s join + scan style; returns the worktree joined with its repo so the handler gets the parent repo name):

  ```go
  // GetActiveWorktreeByPath finds an active (removed_at IS NULL) worktree
  // by exact path, joined with its repo for the parent name. Returns
  // sql.ErrNoRows when no active worktree matches.
  func (d *DB) GetActiveWorktreeByPath(ctx context.Context, path string) (WorktreeWithRepo, error) {
  	row := d.ro.QueryRowContext(ctx,
  		`SELECT w.id, w.repo_id, w.path, w.branch, w.head_sha,
  		        w.is_detached, w.is_locked, w.is_prunable,
  		        w.discovered_at, w.last_seen_at, w.removed_at,
  		        r.owner, r.name
  		   FROM middleman_worktrees w
  		   JOIN middleman_repos r ON r.id = w.repo_id
  		  WHERE w.path = ? AND w.removed_at IS NULL
  		  LIMIT 1`,
  		path,
  	)
  	var wr WorktreeWithRepo
  	var removedAt sql.NullTime
  	var isDetached, isLocked, isPrunable int
  	if err := row.Scan(
  		&wr.ID, &wr.RepoID, &wr.Path, &wr.Branch, &wr.HeadSHA,
  		&isDetached, &isLocked, &isPrunable,
  		&wr.DiscoveredAt, &wr.LastSeenAt, &removedAt,
  		&wr.RepoOwner, &wr.RepoName,
  	); err != nil {
  		return WorktreeWithRepo{}, err
  	}
  	wr.IsDetached = isDetached != 0
  	wr.IsLocked = isLocked != 0
  	wr.IsPrunable = isPrunable != 0
  	if removedAt.Valid {
  		t := removedAt.Time
  		wr.RemovedAt = &t
  	}
  	return wr, nil
  }
  ```

- [ ] **Write the failing DB test.** Append to `internal/db/queries_worktrees_test.go`:

  ```go
  func TestGetActiveWorktreeByPath(t *testing.T) {
  	require := require.New(t)
  	assert := assert.New(t)
  	d := openTestDB(t)
  	ctx := context.Background()

  	repoID, err := d.UpsertLocalRepo(ctx, "demo")
  	require.NoError(err)
  	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
  		Path: "/code/demo-feat", Branch: "feat", HeadSHA: "aaaa",
  	})
  	require.NoError(err)

  	got, err := d.GetActiveWorktreeByPath(ctx, "/code/demo-feat")
  	require.NoError(err)
  	assert.Equal(w.ID, got.ID)
  	assert.Equal("demo", got.RepoName)
  	assert.Equal("local", got.RepoOwner)

  	_, err = d.GetActiveWorktreeByPath(ctx, "/code/does-not-exist")
  	assert.True(errors.Is(err, sql.ErrNoRows))
  }
  ```

  (Add `"database/sql"` and `"errors"` to the test file's imports if not present — the current `queries_worktrees_test.go` imports only `context`, `testing`, testify; add the two stdlib imports.)

- [ ] **Run the DB test — expect FAIL** then **PASS** after the query is added:

  ```
  go test ./internal/db -run 'TestGetActiveWorktreeByPath' -shuffle=on
  ```

  Expected: `ok` once `GetActiveWorktreeByPath` exists.

- [ ] **Write the failing API e2e test** `internal/server/local_resolve_e2e_test.go`:

  ```go
  package server

  import (
  	"context"
  	"net/http"
  	"testing"

  	Assert "github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  	"github.com/wesm/middleman/internal/apiclient/generated"
  	"github.com/wesm/middleman/internal/db"
  )

  func TestAPILocalResolveHitAnd404(t *testing.T) {
  	require := require.New(t)
  	assert := Assert.New(t)
  	srv, database := setupTestServer(t)
  	client := setupTestClient(t, srv)
  	ctx := context.Background()

  	repoID, err := database.UpsertLocalRepo(ctx, "demo")
  	require.NoError(err)
  	dir := t.TempDir()
  	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
  		Path: dir, Branch: "feat/x", HeadSHA: "deadbeef",
  	})
  	require.NoError(err)

  	hit, err := client.HTTP.GetLocalResolveWithResponse(ctx, &generated.GetLocalResolveParams{Path: dir})
  	require.NoError(err)
  	require.Equal(http.StatusOK, hit.StatusCode())
  	require.NotNil(hit.JSON200)
  	assert.Equal("local", hit.JSON200.Owner)
  	assert.Equal("demo", hit.JSON200.Name)
  	assert.Equal(w.ID, hit.JSON200.Number)
  	// Non-git tempdir → CurrentBranch errors → falls back to scanned branch.
  	assert.Equal("feat/x", hit.JSON200.Branch)

  	miss, err := client.HTTP.GetLocalResolveWithResponse(ctx, &generated.GetLocalResolveParams{Path: "/code/nope"})
  	require.NoError(err)
  	assert.Equal(http.StatusNotFound, miss.StatusCode())
  }
  ```

  > NOTE for the implementer: this test references `generated.GetLocalResolveParams{Path: ...}` and `GetLocalResolveWithResponse`, which oapi-codegen emits deterministically from `GET /local/resolve` with the `query:"path"` input. They do not exist until the regen step below runs — so write the rest of the test now, run the regen, then confirm the emitted symbol names match (oapi-codegen v2.6.0's naming for a single `path` query param is `GetLocalResolveParams` with field `Path string` and a `GetLocalResolveWithResponse(ctx, *GetLocalResolveParams)` method; if the toolchain emits slightly different casing, adjust the two references — the test logic is unchanged). The `JSON200` fields (`Owner`, `Name`, `Number`, `Branch`) come straight from `localResolveResponse`.

- [ ] **Add the input/output shapes.** Add to `internal/server/api_types.go` (mirroring an existing query-param input + `Body` output; `localResolveInput` uses a `query:"path"` tag like `listActivityInput` fields):

  ```go
  // localResolveInput addresses a worktree by its absolute on-disk path.
  type localResolveInput struct {
  	Path string `query:"path"`
  }

  // localResolveResponse is the review handle for a worktree path: the
  // PR-shaped (owner, name, number) plus the live current branch.
  type localResolveResponse struct {
  	Owner  string `json:"owner" doc:"always \"local\""`
  	Name   string `json:"name" doc:"the worktree's parent repo name"`
  	Number int64  `json:"number" doc:"the worktree row id (PR-shaped number)"`
  	Branch string `json:"branch" doc:"the worktree's live current branch"`
  }

  type localResolveOutput struct {
  	Body localResolveResponse
  }
  ```

- [ ] **Add the handler.** Add to `internal/server/local_dispatch.go` (it already imports `context`, `errors`, `database/sql` is NOT imported there — add `"database/sql"` to the import block; `huma` and `worktrees` are present):

  ```go
  // resolveLocalWorktreeByPath powers GET /local/resolve: given an absolute
  // worktree path, return the PR-shaped review handle and the live current
  // branch. 404 when no active worktree matches. Loopback only, like the
  // rest of the API. The path is canonicalized (EvalSymlinks) so an aliased
  // path resolves the same row `git worktree list` reported.
  func (s *Server) resolveLocalWorktreeByPath(
  	ctx context.Context, input *localResolveInput,
  ) (*localResolveOutput, error) {
  	if input.Path == "" {
  		return nil, huma.Error400BadRequest("path is required")
  	}
  	canon := input.Path
  	if resolved, err := filepath.EvalSymlinks(input.Path); err == nil {
  		canon = resolved
  	}
  	wr, err := s.db.GetActiveWorktreeByPath(ctx, canon)
  	if errors.Is(err, sql.ErrNoRows) {
  		// Retry with the raw path in case the stored row isn't canonicalized.
  		wr, err = s.db.GetActiveWorktreeByPath(ctx, input.Path)
  	}
  	if errors.Is(err, sql.ErrNoRows) {
  		return nil, huma.Error404NotFound("no middleman review for this directory: " + input.Path)
  	}
  	if err != nil {
  		return nil, huma.Error500InternalServerError("resolve worktree by path: " + err.Error())
  	}
  	w := wr.Worktree
  	return &localResolveOutput{Body: localResolveResponse{
  		Owner:  localOwner,
  		Name:   wr.RepoName,
  		Number: w.ID,
  		Branch: s.currentWorktreeBranch(ctx, &w),
  	}}, nil
  }
  ```

  Add `"path/filepath"` to the `local_dispatch.go` import block (it's not currently imported there).

- [ ] **Register the route.** In `internal/server/huma_routes.go`, in `registerAPI`, add next to the other top-level worktree routes (after `huma.Get(api, "/worktrees/{id}/diff", s.getWorktreeDiff)` at ~L390):

  ```go
  	huma.Get(api, "/local/resolve", s.resolveLocalWorktreeByPath)
  ```

- [ ] **Regenerate the spec + clients** (two steps — `make api-generate` writes the spec JSONs + the TS schema; `go generate` rebuilds the Go client from the spec):

  ```
  make api-generate
  go generate ./internal/apiclient/generated
  ```

  Expected: `make api-generate` updates `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, and `packages/ui/src/api/generated/schema.ts`; `go generate` updates `internal/apiclient/generated/client.gen.go` with the new `GetLocalResolveWithResponse`/`GetLocalResolve` methods + `GetLocalResolveParams`. If either errors on GOCACHE/temp, prefix `GOCACHE=$HOME/.cache/go-build`. Then fix the placeholder constructor in the e2e test to the real generated type, e.g.:

  ```go
  	hit, err := client.HTTP.GetLocalResolveWithResponse(ctx, &generated.GetLocalResolveParams{Path: dir})
  ```

  (and import `"github.com/wesm/middleman/internal/apiclient/generated"` in the test).

- [ ] **Run the API e2e — expect PASS:**

  ```
  go test ./internal/server -short -run 'TestAPILocalResolveHitAnd404' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/server`.

- [ ] **Confirm the TS schema stays clean:**

  ```
  cd frontend && bun run check
  ```

  Expected: 0 errors (the new endpoint adds types but no frontend code references them yet).

- [ ] **Commit** (stage the generated artifacts explicitly — run `git status` first to confirm exactly which generated files changed, then stage those):

  ```
  git add internal/db/queries_worktrees.go internal/db/queries_worktrees_test.go \
          internal/server/local_dispatch.go internal/server/api_types.go internal/server/huma_routes.go \
          internal/server/local_resolve_e2e_test.go \
          internal/apiclient/generated/client.gen.go internal/apiclient/spec/openapi.json \
          frontend/openapi/openapi.json packages/ui/src/api/generated/schema.ts
  git commit -m "$(cat <<'EOF'
  feat(server): GET /local/resolve maps a worktree path to its review handle

  Returns {owner:"local", name, number, branch} for an active worktree;
  404 when none matches. Regenerates the Go + TS API clients and specs.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

  > NOTE: `make api-generate` writes BOTH `frontend/openapi/openapi.json` and `internal/apiclient/spec/openapi.json` (the checked-in specs); `go generate` writes `internal/apiclient/generated/client.gen.go`. Stage exactly what `git status` shows changed under those paths. NEVER `git add -A`.

---

## Task 7 — MCP `get_pull` tool + `middleman mcp` cwd-default mode + registration docs

Add a fourth MCP tool `get_pull` (no args) that GETs the pull detail, and a cwd-default mode for `middleman mcp`: when `--owner/--name/--number` are absent, self-locate via `git rev-parse --show-toplevel` + `GET /local/resolve`. Resolution failures surface as a clear MCP `isError` tool result. Document the registration one-liner.

- [ ] **Write the failing `get_pull` unit test.** Append to `internal/mcp/tools_test.go`:

  ```go
  func TestGetPullProxiesPullEndpoint(t *testing.T) {
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		require.Equal(t, "/api/v1/repos/local/demo/pulls/7", r.URL.Path)
  		_, _ = w.Write([]byte(`{"merge_request":{"number":7,"title":"Worktree: feat","head_branch":"feat"}}`))
  	}))
  	defer srv.Close()
  	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
  	out, err := s.tools["get_pull"].call(s, map[string]any{})
  	require.NoError(t, err)
  	require.Contains(t, out, "Worktree: feat")
  }

  func TestToolListIncludesGetPull(t *testing.T) {
  	s := New(Config{ServerName: "middleman", BaseURL: "http://127.0.0.1:0", ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
  	names := map[string]bool{}
  	for _, td := range s.toolList() {
  		names[td["name"].(string)] = true
  	}
  	require.True(t, names["list_threads"])
  	require.True(t, names["get_thread"])
  	require.True(t, names["reply_to_thread"])
  	require.True(t, names["get_pull"])
  }
  ```

- [ ] **Run it — expect FAIL** (no `get_pull` key in the tools map):

  ```
  go test ./internal/mcp -run 'TestGetPullProxiesPullEndpoint|TestToolListIncludesGetPull' -shuffle=on
  ```

  Expected: `FAIL` (`s.tools["get_pull"]` is the zero `toolDef`, whose `call` is nil → panic, or for the list test the assertion fails).

- [ ] **Add the `get_pull` tool** to `builtinTools()` in `internal/mcp/tools.go` (add the entry inside the returned map, after `reply_to_thread`):

  ```go
  		"get_pull": {
  			name:        "get_pull",
  			description: "Get the pull/review detail (title, head/base branch + SHAs) so you can diff the exact range under review yourself.",
  			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
  			call: func(s *Server, _ map[string]any) (string, error) {
  				return s.restJSON("GET", s.reviewPath(""), nil)
  			},
  		},
  ```

  (`s.reviewPath("")` yields `/api/v1/repos/{owner}/{name}/pulls/{number}` — the `getPull`/`getPullLocal` detail route.)

- [ ] **Run it — expect PASS:**

  ```
  go test ./internal/mcp -run 'TestGetPullProxiesPullEndpoint|TestToolListIncludesGetPull|TestReplyToThreadPostsAgentComment|TestListThreadsProxiesGet' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/internal/mcp`.

- [ ] **Write the failing cwd-default resolver test.** Add `cmd/middleman/mcp_resolve_test.go`:

  ```go
  package main

  import (
  	"net/http"
  	"net/http/httptest"
  	"os/exec"
  	"testing"

  	Assert "github.com/stretchr/testify/assert"
  	"github.com/stretchr/testify/require"
  )

  func TestResolveCwdHandleHit(t *testing.T) {
  	if _, err := exec.LookPath("git"); err != nil {
  		t.Skip("git not available on PATH")
  	}
  	require := require.New(t)
  	assert := Assert.New(t)

  	dir := t.TempDir()
  	runGitInit(t, dir)

  	var gotPath string
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		gotPath = r.URL.Path + "?" + r.URL.RawQuery
  		_, _ = w.Write([]byte(`{"owner":"local","name":"demo","number":7,"branch":"feat/a"}`))
  	}))
  	defer srv.Close()

  	owner, name, number, err := resolveCwdHandle(srv.URL, dir)
  	require.NoError(err)
  	assert.Equal("local", owner)
  	assert.Equal("demo", name)
  	assert.Equal(7, number)
  	assert.Contains(gotPath, "/api/v1/local/resolve?path=")
  }

  func TestResolveCwdHandleUnresolvable(t *testing.T) {
  	require := require.New(t)
  	// A non-git directory: git rev-parse --show-toplevel fails before any HTTP.
  	_, _, _, err := resolveCwdHandle("http://127.0.0.1:0", t.TempDir())
  	require.Error(err)
  }

  func runGitInit(t *testing.T, dir string) {
  	t.Helper()
  	for _, args := range [][]string{
  		{"-C", dir, "init", "--initial-branch=feat/a", dir},
  		{"-C", dir, "config", "user.email", "test@example.com"},
  		{"-C", dir, "config", "user.name", "Test"},
  		{"-C", dir, "commit", "--allow-empty", "-m", "c1"},
  	} {
  		out, err := exec.Command("git", args...).CombinedOutput()
  		require.NoErrorf(t, err, "git %v: %s", args, string(out))
  	}
  }
  ```

- [ ] **Run it — expect FAIL** (undefined: `resolveCwdHandle`):

  ```
  go test ./cmd/middleman -run 'TestResolveCwdHandle' -shuffle=on
  ```

  Expected: `FAIL` (build error: undefined `resolveCwdHandle`).

- [ ] **Add the unresolved-handle short-circuit to the MCP server** so a cwd-default failure yields a *clear* tool error (the spec requires it). In `internal/mcp/server.go`, add a field to `Config`:

  ```go
  	// Unresolved, when non-empty, means cwd-default resolution failed.
  	// Every tools/call returns it as a clear isError result (tools/list
  	// still works, so the client sees the tools and learns why calls fail).
  	Unresolved string
  ```

  In `internal/mcp/tools.go`, short-circuit at the very top of `handleToolCall` (right after the `_ = ctx` line, before the param unmarshal / tool lookup):

  ```go
  func (s *Server) handleToolCall(ctx context.Context, w io.Writer, req rpcRequest) error {
  	_ = ctx
  	if s.cfg.Unresolved != "" {
  		return s.writeResult(w, req.ID, map[string]any{
  			"content": []map[string]any{{"type": "text", "text": s.cfg.Unresolved}},
  			"isError": true,
  		})
  	}
  	var p struct {
  		Name      string         `json:"name"`
  		Arguments map[string]any `json:"arguments"`
  	}
  ```

  Add the test to `internal/mcp/tools_test.go` (ensure `bytes`, `context`, `encoding/json` are imported):

  ```go
  func TestUnresolvedHandleReturnsClearToolError(t *testing.T) {
  	s := New(Config{ServerName: "middleman", Unresolved: "no middleman review for this directory (/x): boom"})
  	var buf bytes.Buffer
  	req := rpcRequest{
  		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
  		Params: json.RawMessage(`{"name":"list_threads","arguments":{}}`),
  	}
  	require.NoError(t, s.handleToolCall(context.Background(), &buf, req))
  	require.Contains(t, buf.String(), "no middleman review for this directory")
  	require.Contains(t, buf.String(), `"isError":true`)
  }
  ```

  Run: `go test ./internal/mcp -run 'TestUnresolvedHandleReturnsClearToolError' -shuffle=on` → expect FAIL (undefined `Config.Unresolved`) before the field/short-circuit, PASS after.

- [ ] **Implement the cwd-default mode** in `cmd/middleman/main.go`. Add `"os/exec"` and `"encoding/json"` and `"bytes"` to the import block. Replace `runMCP` and add the `resolveCwdHandle` helper:

  ```go
  // runMCP parses flags and serves the stdio MCP server. The reader is
  // injected: os.Stdin from the CLI dispatch, an explicit reader in tests.
  //
  // When --owner/--name/--number are all omitted, the proxy self-locates:
  // it finds its git worktree (git rev-parse --show-toplevel in cwd) and
  // asks middleman's /local/resolve for the review handle. The lookup is
  // lazy/best-effort here; if it fails the tools surface a clear MCP error
  // rather than the process crashing.
  func runMCP(args []string, in io.Reader, out io.Writer) error {
  	fs := flag.NewFlagSet("middleman mcp", flag.ContinueOnError)
  	fs.SetOutput(io.Discard)
  	baseURL := fs.String("base-url", "http://127.0.0.1:8091", "middleman REST base URL")
  	owner := fs.String("owner", "", "review owner (local)")
  	name := fs.String("name", "", "review repo name")
  	number := fs.Int("number", 0, "review number (worktree id; 0 = unset, a real id is >= 1)")
  	if err := fs.Parse(args); err != nil {
  		return err
  	}

  	cfg := mcp.Config{
  		ServerName:   "middleman",
  		BaseURL:      *baseURL,
  		ReviewOwner:  *owner,
  		ReviewName:   *name,
  		ReviewNumber: *number,
  	}

  	// cwd-default mode: no explicit handle → resolve from the current
  	// directory. A resolution failure here is non-fatal; we leave the
  	// (empty) handle so tool calls return a clear isError result.
  	if *owner == "" && *name == "" && *number == 0 {
  		cwd, err := os.Getwd()
  		if err == nil {
  			if ro, rn, rnum, rerr := resolveCwdHandle(*baseURL, cwd); rerr == nil {
  				cfg.ReviewOwner, cfg.ReviewName, cfg.ReviewNumber = ro, rn, rnum
  			} else {
  				cfg.Unresolved = fmt.Sprintf("no middleman review for this directory (%s): %v", cwd, rerr)
  				slog.Warn("middleman mcp: could not resolve review for cwd", "err", rerr)
  			}
  		}
  	}

  	srv := mcp.New(cfg)
  	return srv.Serve(context.Background(), in, out)
  }

  // resolveCwdHandle finds the git worktree containing dir and asks
  // middleman's /local/resolve for its review handle. Returns an error when
  // dir is not a git worktree, the server is unreachable, or no review
  // matches (the path isn't an enrolled worktree).
  func resolveCwdHandle(baseURL, dir string) (owner, name string, number int, err error) {
  	top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
  	if err != nil {
  		return "", "", 0, fmt.Errorf("not a git worktree: %s: %w", dir, err)
  	}
  	worktreePath := strings.TrimSpace(string(top))

  	req, err := http.NewRequest("GET", baseURL+"/api/v1/local/resolve", nil)
  	if err != nil {
  		return "", "", 0, err
  	}
  	q := req.URL.Query()
  	q.Set("path", worktreePath)
  	req.URL.RawQuery = q.Encode()

  	resp, err := http.DefaultClient.Do(req)
  	if err != nil {
  		return "", "", 0, fmt.Errorf("resolve %s: %w", worktreePath, err)
  	}
  	defer resp.Body.Close()
  	body, _ := io.ReadAll(resp.Body)
  	if resp.StatusCode != http.StatusOK {
  		return "", "", 0, fmt.Errorf("no middleman review for %s: status %d: %s",
  			worktreePath, resp.StatusCode, bytes.TrimSpace(body))
  	}
  	var h struct {
  		Owner  string `json:"owner"`
  		Name   string `json:"name"`
  		Number int    `json:"number"`
  		Branch string `json:"branch"`
  	}
  	if err := json.Unmarshal(body, &h); err != nil {
  		return "", "", 0, fmt.Errorf("decode resolve response: %w", err)
  	}
  	if h.Owner == "" || h.Name == "" || h.Number == 0 {
  		return "", "", 0, fmt.Errorf("incomplete review handle for %s", worktreePath)
  	}
  	return h.Owner, h.Name, h.Number, nil
  }
  ```

- [ ] **Update the now-stale CLI test.** `cmd/middleman/main_test.go:TestMCPRequiresReviewFlags` asserts that omitting the flags errors. With cwd-default mode, `runMCP` no longer errors on missing flags (it tries to resolve and serves with an empty handle). Since `runCLI([]string{"mcp", "--base-url", "http://127.0.0.1:8091"}, ...)` reads from `os.Stdin` (empty in the test harness → immediate EOF → clean return), replace that test with one asserting the cwd-default *resolver* errors for a non-worktree cwd (the behavior we actually want), keeping `TestMCPParsesFlags` unchanged:

  ```go
  func TestMCPCwdDefaultResolverErrorsOutsideWorktree(t *testing.T) {
  	// Outside any git worktree the cwd-default resolver fails cleanly;
  	// the served tools then return isError, the server itself does not
  	// crash. This replaces the old "flags required" contract.
  	_, _, _, err := resolveCwdHandle("http://127.0.0.1:0", t.TempDir())
  	require.Error(t, err)
  }
  ```

  Delete the old `TestMCPRequiresReviewFlags` function.

- [ ] **Run it — expect PASS:**

  ```
  go test ./cmd/middleman -run 'TestResolveCwdHandle|TestMCP' -shuffle=on
  ```

  Expected: `ok  github.com/wesm/middleman/cmd/middleman`.

- [ ] **Document the registration one-liner.** Find existing `middleman mcp` mentions and add the `claude mcp add` one-liner near the local-worktree / MCP usage docs:

  ```
  grep -rn "middleman mcp\|claude mcp\|review-threads" README.md docs/ 2>/dev/null
  ```

  In the doc that describes the local-worktree review / agent usage (e.g. `README.md` under a "Local worktree reviews" / "MCP" heading — use whatever the grep surfaces as the canonical usage doc), add:

  ```markdown
  ### Use your own terminal Claude on a worktree's review

  Register middleman's MCP server once:

  ```
  claude mcp add middleman -- middleman mcp
  ```

  Then, from inside any enrolled worktree, run `claude`. It auto-discovers
  that worktree's review (no flags, no IDs) and gets four read-and-discuss
  tools: `list_threads`, `get_thread`, `get_pull`, and `reply_to_thread`.
  It reads the review and replies in the threads; resolving/hiding threads
  and applying edits stay in the middleman app. Requires `middleman` to be
  running (the proxy talks to its loopback API).
  ```

  If no such usage doc exists, add the section to `README.md`. (Do NOT create a brand-new standalone doc file unless the grep shows there is genuinely no usage section anywhere.)

- [ ] **Commit:**

  ```
  git add internal/mcp/server.go internal/mcp/tools.go internal/mcp/tools_test.go cmd/middleman/main.go cmd/middleman/main_test.go cmd/middleman/mcp_resolve_test.go README.md
  git commit -m "$(cat <<'EOF'
  feat(mcp): get_pull tool + cwd-default mode for middleman mcp

  get_pull exposes the pull detail (base/head SHAs) so a terminal Claude
  can diff the review range itself. Without --owner/--name/--number,
  middleman mcp self-locates via git toplevel + /local/resolve; failures
  surface as clear MCP tool errors. Documents `claude mcp add middleman`.

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

  > NOTE: if the registration text lands in a doc file other than `README.md`, stage that path instead. Adjust the staged doc path to match the grep result; never `git add -A`.

---

## Self-review

Mapping each spec requirement (from `docs/superpowers/specs/2026-05-31-phase3-external-mcp-design.md`) to the task that delivers it:

| Spec requirement | Task |
|---|---|
| Migration `000023`: `branch TEXT NOT NULL DEFAULT ''` on `middleman_review_threads` AND `middleman_worktree_sessions` | Task 1 |
| Backfill review-thread rows via `mr_id → merge_requests.platform_id → middleman_worktrees.branch` (correlated UPDATE) | Task 1 (up migration) |
| Backfill active session rows from their worktree's branch | Task 1 (up migration) |
| Down drops both columns | Task 1 (down migration) |
| Migration test: fresh DB, both columns exist + default `''`; schema-version test still green | Task 1 |
| `ReviewThread.Branch` field; `scanReviewThread` reads it; create writes it | Task 2 |
| List filters by current branch; legacy `''` rows remain visible | Task 2 (`ListReviewThreadsForMRBranch`) + Task 5 (`loadReviewThreadsResponse`) |
| DB test: two branches → disjoint thread-sets; `''` legacy stays visible | Task 2 |
| `WorktreeSession.Branch`; branch-keyed `GetActiveWorktreeSession`/`CreateWorktreeSession`; scan reads it | Task 3 |
| DB test: two branches → two distinct active sessions, each its own session row | Task 3 |
| `worktrees.CurrentBranch` (`git rev-parse --abbrev-ref HEAD`, detached → `""`) | Task 4 |
| Server-derived, authoritative current branch with fallback to scanned branch (`currentWorktreeBranch`) | Task 4 (helper) + Task 5 (wired into create/list/session) |
| Create stamps the worktree's current branch (in-app only) | Task 5 |
| List filters to current branch so a branch switch takes effect immediately | Task 5 |
| `ensureWorktreeSession`/session endpoints scope to `(worktree, branch)`; switching branches → fresh `--resume`, switch-back resumes | Task 3 (DB) + Task 5 (server) |
| `number` stays `worktree.id`; `resolveLocalWorktree`, handle, synthetic-MR keying, UI nav unchanged | Tasks 5 & 6 (no re-key; `resolveLocalWorktree` untouched) |
| API e2e: create on one branch, switch worktree branch, list returns the other set (git via the seed seam) | Task 5 (`TestAPIReviewThreadsBranchScoped`, `seedReviewWorktreeGit`) |
| `GET /api/v1/local/resolve?path=` → `{owner:"local", name, number, branch}`; 404 when no active worktree matches; loopback only | Task 6 |
| `name` = parent repo name; `branch` = live current branch (§1 helper) | Task 6 (`GetActiveWorktreeByPath` join + `currentWorktreeBranch`) |
| API e2e: resolve hit + 404 | Task 6 (`TestAPILocalResolveHitAnd404`) |
| `make api-generate` regenerates Go + TS clients; `bun run check` clean | Task 6 |
| `get_pull` MCP tool (no args) → `GET …/pulls/{number}` | Task 7 |
| Four-tool surface: `list_threads`, `get_thread`, `reply_to_thread`, `get_pull` | Task 7 (`TestToolListIncludesGetPull`) |
| `middleman mcp` cwd-default: resolve via `git rev-parse --show-toplevel` + `/local/resolve` when flags absent; flags-present path unchanged | Task 7 (`resolveCwdHandle`, `runMCP`) |
| Resolution failure → clear MCP `isError` tool result, not a crash | Task 7 (empty handle → existing `restJSON`/`handleToolCall` isError path; `slog.Warn` on resolve failure) |
| MCP tests: `tools/list` has the four tools; `get_pull` routes to pull endpoint; cwd-default maps a path→handle against a fake resolve server; unresolvable cwd → error | Task 7 (`TestToolListIncludesGetPull`, `TestGetPullProxiesPullEndpoint`, `TestResolveCwdHandleHit`, `TestResolveCwdHandleUnresolvable`) |
| Registration docs: `claude mcp add middleman -- middleman mcp` | Task 7 |
| Conventions: `-shuffle=on`, testify, server `-short`, generated-client e2e | All tasks (per the Conventions block) |

**Out-of-scope items confirmed NOT implemented:** `list_reviews`/`get_review` discovery tools; `get_diff`/`resolve_thread`/`hide_thread`/`apply_thread` external tools; per-`(worktree,branch)` review *identity* re-key (`number` stays `worktree.id`); token auth — none appear in any task.

### Implementer notes / spec-vs-map deltas the controller should know

1. **`ReviewThread` struct location.** The code map says the `ReviewThread` struct is in `internal/db/types.go`; it is actually defined at the top of `internal/db/queries_review_threads.go` (lines 14-26). `WorktreeSession`/`Worktree` *are* in `types.go`. Task 2 edits the struct where it actually lives; Task 3 edits `WorktreeSession` in `types.go`. Confirmed by reading both files.

2. **Unresolved cwd → clear `isError` (wired in).** `runMCP` sets `Config.Unresolved` to `"no middleman review for this directory (<cwd>): <reason>"` when cwd-default resolution fails; `handleToolCall` short-circuits every `tools/call` with that message as an `isError` result, while `tools/list` still lists the four tools (so the client sees them and learns why calls fail). This satisfies the spec's "clear MCP tool error" rather than leaking a malformed-path REST 404. `TestUnresolvedHandleReturnsClearToolError` covers it.

3. **Generated client symbol names for `/local/resolve` are produced by `make api-generate`** and are not knowable until regen. Task 6's e2e is written against the deterministic huma naming (`GetLocalResolveWithResponse` + `generated.GetLocalResolveParams{Path: ...}`); the implementer must reconcile the placeholder constructor with the actual emitted names after running `make api-generate` (the step explicitly calls this out). This is the one spot where exact code can't be pinned ahead of regen.

4. **Two-step regen.** Verified against the `Makefile`: `make api-generate` writes `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, and `packages/ui/src/api/generated/schema.ts`, but does NOT rebuild the Go `client.gen.go`. The Go client is regenerated separately via `go generate ./internal/apiclient/generated` (oapi-codegen reading `internal/apiclient/spec/openapi.json`, per `internal/apiclient/generated/generate.go`). Task 6 runs both, in that order, and stages all four generated paths. The map's "Client regen: `make api-generate`" line was incomplete on this point — flagged so the controller isn't surprised that a second `go generate` is required for the e2e client methods to exist.

5. **Existing non-git e2e tests stay green** because `currentWorktreeBranch` falls back to the scanned `w.Branch` when `git rev-parse` fails (the `seedReviewWorktree` tempdir isn't a git repo). Their threads are stamped with the scanned branch on create and listed for that same branch, so nothing disappears. Verified against `seedReviewWorktree` (uses `t.TempDir()`, `Branch: "feat/x"`).

6. **`applyAllReviewThreads` eligible set is branch-scoped** (`ListReviewThreadsForMRBranch`): apply-all applies only the current branch's open/discussed threads, never another branch's (whose `commit_sha`/line anchors don't exist on the current checkout). Wired in Task 5 alongside the branch-aware reload — coherent with the branch-scoped list.
