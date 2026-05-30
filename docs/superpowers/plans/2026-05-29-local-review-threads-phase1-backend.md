# Local Review Threads — Phase 1 (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist local-worktree review comments as anchored, hideable threads with a comment conversation, exposed over a REST API — the foundation the discuss/apply agent (Phase 2) and the MCP proxy (Phase 3) build on.

**Architecture:** Two new SQLite tables (`middleman_review_threads`, `middleman_review_thread_comments`) hang off the per-worktree *synthetic* merge request (`ensureSyntheticMRForWorktree`). A new Huma route group at the existing PR-shaped path (`/repos/{owner}/{name}/pulls/{number}/review-threads`), gated to `owner == "local"`, does CRUD + hide/resolve. Verified end to end with the generated Go API client.

**Tech Stack:** Go (stdlib `database/sql`, modernc.org/sqlite), Huma v2, golang-migrate-style numbered SQL migrations, testify, the generated client in `internal/apiclient/generated`.

**Spec:** `docs/superpowers/specs/2026-05-29-local-review-threads-design.md`

---

## Scope & follow-on plans

This plan is **Phase 1, backend only** — it produces a working, e2e-tested thread API with no UI yet. It does **not** touch the Claude runner or the submit seam. Follow-on plans (written after this lands):

- **Phase 1b (frontend):** `ReviewThreadCard.svelte`, a `reviewThreads` store, mounting thread cards on the worktree diff, and changing `ReviewPanel.svelte`'s local submit to create threads.
- **Phase 2:** discuss/apply turns through the worktree session, per-phase tool-gating, Apply/Apply-all, the submit mode picker.
- **Phase 3:** the `middleman mcp` stdio proxy + discovery (`list_reviews`/`get_review`) + external-shell wiring.

## Patterns this plan follows (read first)

- DB query style: `internal/db/queries_ai.go` — `d.rw`/`d.ro` handles, `intPtrToNullable`/`strPtrToNullable`, the `scanner` interface + per-type scan funcs, a `tx` for multi-row inserts, `datetime('now')` for timestamps.
- Migration rules: `context/db-migrations.md`. Migrations in `internal/db/migrations/` are embedded and run by `db.Open()`. **Append-only** — the pre-commit hook `migrationhistorycheck` rejects edits to migrations already on `main`.
- Route style: `internal/server/huma_routes_sessions.go` — input/output structs with `path:`/`json:`/`doc:` tags, `huma.Get/Post`, `isLocalSource` gating, `resolveLocalWorktree`, `to<X>Response` converters, `emptyOutput` (defined in `huma_routes_ai.go:177`).
- Synthetic MR: `internal/server/local_dispatch.go` — `resolveOrEnsureMRID(ctx, owner, name, number)` returns the MR id for any PR-shaped route (upserts the synthetic MR for local sources).
- e2e style: `internal/server/worktrees_e2e_test.go` — `setupTestServer(t)`, `setupTestClient(t, srv)`, `client.HTTP.<Generated>WithResponse(...)`, `runGitWT`. The AI-thread test (`TestAPILocalDispatchAIThreadAcceptsRangeAnchor`) is the closest analog.

---

### Task 1: Migration — review thread tables

**Files:**
- Create: `internal/db/migrations/000021_add_review_threads.up.sql`
- Create: `internal/db/migrations/000021_add_review_threads.down.sql`
- Test: `internal/db/queries_review_threads_test.go`

> Confirm `000021` is the next free number: `ls internal/db/migrations/ | tail`. If a higher number exists on this branch, use the next one and keep the rest of the plan's filename in sync.

- [ ] **Step 1: Write the failing test**

Create `internal/db/queries_review_threads_test.go`:

```go
package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewThreadsMigrationApplied proves migration 000021 ran: the
// tables exist and are queryable through the read handle.
func TestReviewThreadsMigrationApplied(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var threads int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_threads`).Scan(&threads))
	require.Equal(t, 0, threads)

	var comments int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_thread_comments`).Scan(&comments))
	require.Equal(t, 0, comments)
}
```

> `ReadDB()` is the exported read handle accessor (sibling of `WriteDB()` used in `worktrees_e2e_test.go:696`). Confirm both exist: `grep -n "func (d \*DB) ReadDB\|func (d \*DB) WriteDB" internal/db/db.go`. If `ReadDB()` doesn't exist, use `d.WriteDB()` here — the count query works on either handle.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run TestReviewThreadsMigrationApplied -shuffle=on`
Expected: FAIL — `no such table: middleman_review_threads`.

- [ ] **Step 3: Write the up migration**

Create `internal/db/migrations/000021_add_review_threads.up.sql`:

```sql
-- Local-worktree review threads. A "review" is the living set of these
-- threads on a worktree's synthetic merge request; each thread anchors to
-- a file/line in the worktree diff and owns a comment conversation
-- (reviewer +, later, agent). Distinct from middleman_ai_threads (Q&A,
-- one Claude session per thread) and from GitHub-synced review comments in
-- middleman_mr_events (remote-only).
CREATE TABLE IF NOT EXISTS middleman_review_threads (
    id          INTEGER PRIMARY KEY,
    mr_id       INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path        TEXT     NOT NULL,
    side        TEXT     NOT NULL,                 -- 'LEFT' | 'RIGHT'
    line        INTEGER  NOT NULL,
    start_line  INTEGER,                           -- nullable; multi-line selection start
    commit_sha  TEXT     NOT NULL,
    status      TEXT     NOT NULL DEFAULT 'open',  -- 'open' | 'discussed' | 'applied' | 'resolved'
    hidden_at   DATETIME,                          -- non-null => hidden from the UI
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_review_threads_mr_id
    ON middleman_review_threads(mr_id);

CREATE INDEX IF NOT EXISTS idx_review_threads_mr_status
    ON middleman_review_threads(mr_id, status);

-- Comments within a review thread: the reviewer's root comment, agent
-- replies (Phase 2+), and reviewer follow-ups. Oldest-first by id.
CREATE TABLE IF NOT EXISTS middleman_review_thread_comments (
    id         INTEGER PRIMARY KEY,
    thread_id  INTEGER  NOT NULL REFERENCES middleman_review_threads(id) ON DELETE CASCADE,
    author     TEXT     NOT NULL,                  -- 'user' | 'agent'
    body       TEXT     NOT NULL,
    turn_id    INTEGER,                            -- nullable; worktree_session_turns.id for agent replies (Phase 2)
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_review_thread_comments_thread_id
    ON middleman_review_thread_comments(thread_id);
```

- [ ] **Step 4: Write the down migration**

Create `internal/db/migrations/000021_add_review_threads.down.sql`:

```sql
DROP TABLE IF EXISTS middleman_review_thread_comments;
DROP TABLE IF EXISTS middleman_review_threads;
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/db -run TestReviewThreadsMigrationApplied -shuffle=on`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/000021_add_review_threads.up.sql \
        internal/db/migrations/000021_add_review_threads.down.sql \
        internal/db/queries_review_threads_test.go
git commit -m "feat(db): add review_threads + review_thread_comments tables"
```

---

### Task 2: Thread model + create/get/list queries

**Files:**
- Create: `internal/db/queries_review_threads.go`
- Test: `internal/db/queries_review_threads_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/db/queries_review_threads_test.go`. Add `Assert "github.com/stretchr/testify/assert"` and `time` to the imports.

```go
// insertTestMR creates a local repo + a minimal merge request to FK
// review threads onto. Mirrors the synthetic-MR field set from
// local_dispatch.go:ensureSyntheticMRForWorktree; if UpsertMergeRequest
// rejects a missing column, copy more fields from there.
func insertTestMR(t *testing.T, d *DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := d.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	now := time.Now().UTC()
	mrID, err := d.UpsertMergeRequest(ctx, &MergeRequest{
		RepoID:     repoID,
		PlatformID: 1,
		Number:     1,
		Title:      "Worktree: feat",
		Author:     "local",
		State:      "open",
		HeadBranch: "feat",
		BaseBranch: "main",
		CreatedAt:  now,
		UpdatedAt:  now,
		LastActivityAt: now,
	})
	require.NoError(t, err)
	return mrID
}

func TestCreateAndListReviewThreads(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := insertTestMR(t, d)

	start := 10
	threads, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSHA: "abc123", Body: "first comment"},
		{Path: "b.go", Side: "RIGHT", Line: 5, StartLine: &start, CommitSHA: "abc123", Body: "ranged comment"},
	})
	require.NoError(err)
	require.Len(threads, 2)
	assert.Equal("open", threads[0].Status)
	assert.Equal("a.go", threads[0].Path)
	require.Nil(threads[0].StartLine)
	require.NotNil(threads[1].StartLine)
	assert.Equal(10, *threads[1].StartLine)

	got, err := d.GetReviewThread(ctx, threads[0].ID)
	require.NoError(err)
	assert.Equal(mrID, got.MergeRequestID)
	assert.Equal(12, got.Line)
	assert.Nil(got.HiddenAt)

	listed, err := d.ListReviewThreadsForMR(ctx, mrID)
	require.NoError(err)
	require.Len(listed, 2)
	assert.Equal("a.go", listed[0].Path)
	assert.Equal("b.go", listed[1].Path)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run TestCreateAndListReviewThreads -shuffle=on`
Expected: FAIL — `undefined: NewReviewThread` / `d.CreateReviewThreads`.

- [ ] **Step 3: Write the model types + create/get/list**

Create `internal/db/queries_review_threads.go`:

```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ReviewThread is one anchored review-comment thread on a (local)
// merge request. The "review" for a worktree is the living set of these.
type ReviewThread struct {
	ID             int64
	MergeRequestID int64
	Path           string
	Side           string // "LEFT" | "RIGHT"
	Line           int
	StartLine      *int // nullable; multi-line selection start
	CommitSHA      string
	Status         string // "open" | "discussed" | "applied" | "resolved"
	HiddenAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ReviewThreadComment is one comment within a ReviewThread.
type ReviewThreadComment struct {
	ID        int64
	ThreadID  int64
	Author    string // "user" | "agent"
	Body      string
	TurnID    *int64 // nullable; worktree_session_turns.id for agent replies
	CreatedAt time.Time
}

// NewReviewThread describes a thread anchor plus the reviewer's root
// comment. CreateReviewThreads inserts the thread and its first
// ('user') comment together.
type NewReviewThread struct {
	Path      string
	Side      string
	Line      int
	StartLine *int
	CommitSHA string
	Body      string // the reviewer's root comment
}

// CreateReviewThreads inserts a batch of threads (each with its root
// 'user' comment) for one MR in a single transaction, and returns the
// created thread rows in input order.
func (d *DB) CreateReviewThreads(ctx context.Context, mrID int64, in []NewReviewThread) ([]ReviewThread, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ids := make([]int64, 0, len(in))
	for _, t := range in {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO middleman_review_threads
				(mr_id, path, side, line, start_line, commit_sha)
			VALUES (?, ?, ?, ?, ?, ?)`,
			mrID, t.Path, t.Side, t.Line, intPtrToNullable(t.StartLine), t.CommitSHA,
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

func (d *DB) GetReviewThread(ctx context.Context, id int64) (ReviewThread, error) {
	return scanReviewThread(d.ro.QueryRowContext(ctx, `
		SELECT id, mr_id, path, side, line, start_line, commit_sha,
		       status, hidden_at, created_at, updated_at
		  FROM middleman_review_threads WHERE id = ?`, id))
}

// ListReviewThreadsForMR returns all threads for an MR, oldest-first.
// Hidden threads are included (the response carries a `hidden` flag);
// the UI filters them. Comments are loaded separately.
func (d *DB) ListReviewThreadsForMR(ctx context.Context, mrID int64) ([]ReviewThread, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT id, mr_id, path, side, line, start_line, commit_sha,
		       status, hidden_at, created_at, updated_at
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

func scanReviewThread(row scanner) (ReviewThread, error) {
	var t ReviewThread
	var startLine sql.NullInt64
	var hiddenAt sql.NullTime
	err := row.Scan(
		&t.ID, &t.MergeRequestID, &t.Path, &t.Side, &t.Line,
		&startLine, &t.CommitSHA, &t.Status, &hiddenAt,
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

> `scanner`, `intPtrToNullable`, and `strPtrToNullable` are already defined in `queries_ai.go` (same package) — do not redeclare them.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db -run TestCreateAndListReviewThreads -shuffle=on`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db/queries_review_threads.go internal/db/queries_review_threads_test.go
git commit -m "feat(db): review thread model + create/get/list queries"
```

---

### Task 3: Comment + status/hide queries

**Files:**
- Modify: `internal/db/queries_review_threads.go`
- Test: `internal/db/queries_review_threads_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/db/queries_review_threads_test.go`:

```go
func TestReviewThreadCommentsAndState(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := insertTestMR(t, d)

	threads, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "root"},
	})
	require.NoError(err)
	threadID := threads[0].ID

	// Add an agent reply.
	c, err := d.AddReviewThreadComment(ctx, threadID, "agent", "i'd refactor X", nil)
	require.NoError(err)
	assert.Equal("agent", c.Author)
	assert.Equal(threadID, c.ThreadID)

	comments, err := d.ListReviewThreadCommentsForMR(ctx, mrID)
	require.NoError(err)
	require.Len(comments, 2) // root + reply
	assert.Equal("user", comments[0].Author)
	assert.Equal("agent", comments[1].Author)

	// Status transition + hide.
	require.NoError(d.SetReviewThreadStatus(ctx, threadID, "discussed"))
	require.NoError(d.HideReviewThread(ctx, threadID))
	got, err := d.GetReviewThread(ctx, threadID)
	require.NoError(err)
	assert.Equal("discussed", got.Status)
	require.NotNil(got.HiddenAt)

	require.NoError(d.UnhideReviewThread(ctx, threadID))
	got, err = d.GetReviewThread(ctx, threadID)
	require.NoError(err)
	assert.Nil(got.HiddenAt)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db -run TestReviewThreadCommentsAndState -shuffle=on`
Expected: FAIL — `undefined: d.AddReviewThreadComment`.

- [ ] **Step 3: Implement the comment + state queries**

Append to `internal/db/queries_review_threads.go`:

```go
// AddReviewThreadComment appends a comment and bumps the thread's
// updated_at, in one transaction. turnID is nil for user comments.
func (d *DB) AddReviewThreadComment(ctx context.Context, threadID int64, author, body string, turnID *int64) (ReviewThreadComment, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return ReviewThreadComment{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO middleman_review_thread_comments (thread_id, author, body, turn_id)
		VALUES (?, ?, ?, ?)`,
		threadID, author, body, int64PtrToNullable(turnID),
	)
	if err != nil {
		return ReviewThreadComment{}, fmt.Errorf("insert comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReviewThreadComment{}, fmt.Errorf("last insert id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE middleman_review_threads SET updated_at = datetime('now') WHERE id = ?`,
		threadID,
	); err != nil {
		return ReviewThreadComment{}, fmt.Errorf("bump thread: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReviewThreadComment{}, fmt.Errorf("commit: %w", err)
	}
	return d.getReviewThreadComment(ctx, id)
}

func (d *DB) getReviewThreadComment(ctx context.Context, id int64) (ReviewThreadComment, error) {
	return scanReviewThreadComment(d.ro.QueryRowContext(ctx, `
		SELECT id, thread_id, author, body, turn_id, created_at
		  FROM middleman_review_thread_comments WHERE id = ?`, id))
}

// ListReviewThreadCommentsForMR returns every comment across the MR's
// threads, oldest-first by id. The handler groups them by thread_id.
func (d *DB) ListReviewThreadCommentsForMR(ctx context.Context, mrID int64) ([]ReviewThreadComment, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT c.id, c.thread_id, c.author, c.body, c.turn_id, c.created_at
		  FROM middleman_review_thread_comments c
		  JOIN middleman_review_threads t ON t.id = c.thread_id
		 WHERE t.mr_id = ?
		 ORDER BY c.id ASC`, mrID)
	if err != nil {
		return nil, fmt.Errorf("list comments for mr: %w", err)
	}
	defer rows.Close()
	var out []ReviewThreadComment
	for rows.Next() {
		c, err := scanReviewThreadComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetReviewThreadStatus sets status (open|discussed|applied|resolved)
// and bumps updated_at.
func (d *DB) SetReviewThreadStatus(ctx context.Context, id int64, status string) error {
	_, err := d.rw.ExecContext(ctx, `
		UPDATE middleman_review_threads
		   SET status = ?, updated_at = datetime('now')
		 WHERE id = ?`, status, id)
	return err
}

func (d *DB) HideReviewThread(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx, `
		UPDATE middleman_review_threads
		   SET hidden_at = datetime('now'), updated_at = datetime('now')
		 WHERE id = ?`, id)
	return err
}

func (d *DB) UnhideReviewThread(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx, `
		UPDATE middleman_review_threads
		   SET hidden_at = NULL, updated_at = datetime('now')
		 WHERE id = ?`, id)
	return err
}

func scanReviewThreadComment(row scanner) (ReviewThreadComment, error) {
	var c ReviewThreadComment
	var turnID sql.NullInt64
	err := row.Scan(&c.ID, &c.ThreadID, &c.Author, &c.Body, &turnID, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewThreadComment{}, err
		}
		return ReviewThreadComment{}, fmt.Errorf("scan comment: %w", err)
	}
	if turnID.Valid {
		c.TurnID = &turnID.Int64
	}
	return c, nil
}

func int64PtrToNullable(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db -run TestReviewThreadCommentsAndState -shuffle=on`
Expected: PASS

- [ ] **Step 5: Run the full db package to catch coupling**

Run: `go test ./internal/db -shuffle=on`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries_review_threads.go internal/db/queries_review_threads_test.go
git commit -m "feat(db): review thread comment + status/hide queries"
```

---

### Task 4: REST routes — list / create / comment / hide / unhide / resolve

**Files:**
- Create: `internal/server/huma_routes_review_threads.go`
- Modify: `internal/server/huma_routes.go:499` (register the new group)

This task adds handler code. It is verified behaviorally by the e2e tests in Task 6 (the generated client needs the routes to exist first, so the test cycle for this layer lands in Task 6). After writing, verify it compiles and vets.

- [ ] **Step 1: Create the route file**

Create `internal/server/huma_routes_review_threads.go`:

```go
package server

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/db"
)

// Local-worktree review threads. Live at the PR-shaped path so middleman
// keeps one addressing convention; owner=="local" gates the behavior.
// A "review" is the living set of these threads on a worktree's
// synthetic merge request.

type reviewThreadCommentResponse struct {
	ID        int64  `json:"id"`
	Author    string `json:"author" doc:"user | agent"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at" doc:"UTC RFC3339 timestamp"`
}

type reviewThreadResponse struct {
	ID        int64                         `json:"id"`
	Path      string                        `json:"path"`
	Side      string                        `json:"side" doc:"LEFT | RIGHT"`
	Line      int                           `json:"line"`
	StartLine *int                          `json:"start_line,omitempty"`
	CommitSHA string                        `json:"commit_sha"`
	Status    string                        `json:"status" doc:"open | discussed | applied | resolved"`
	Hidden    bool                          `json:"hidden"`
	CreatedAt string                        `json:"created_at" doc:"UTC RFC3339 timestamp"`
	UpdatedAt string                        `json:"updated_at" doc:"UTC RFC3339 timestamp"`
	Comments  []reviewThreadCommentResponse `json:"comments"`
}

type listReviewThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type listReviewThreadsOutput struct {
	Body struct {
		Threads []reviewThreadResponse `json:"threads"`
	}
}

type createReviewThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Threads []struct {
			Path      string `json:"path"`
			Side      string `json:"side" doc:"LEFT | RIGHT"`
			Line      int    `json:"line"`
			StartLine *int   `json:"start_line,omitempty"`
			CommitSHA string `json:"commit_sha"`
			Body      string `json:"body" doc:"the reviewer's root comment"`
		} `json:"threads"`
	}
}

type createReviewThreadsOutput struct {
	Body struct {
		Threads []reviewThreadResponse `json:"threads"`
	}
}

type addReviewThreadCommentInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Body   string `json:"body"`
		Author string `json:"author,omitempty" doc:"user (default) | agent"`
	}
}

type reviewThreadOutput struct {
	Body reviewThreadResponse
}

type reviewThreadActionInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
}

func (s *Server) registerReviewThreadRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-threads", s.listReviewThreads)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads", s.createReviewThreads)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments", s.addReviewThreadComment)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/hide", s.hideReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unhide", s.unhideReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve", s.resolveReviewThread)
}

// loadReviewThreadsResponse lists an MR's threads with their comments
// grouped in. Shared by the list and create handlers.
func (s *Server) loadReviewThreadsResponse(ctx context.Context, mrID int64) ([]reviewThreadResponse, error) {
	threads, err := s.db.ListReviewThreadsForMR(ctx, mrID)
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
			ID:        c.ID,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := make([]reviewThreadResponse, 0, len(threads))
	for _, t := range threads {
		out = append(out, toReviewThreadResponse(t, byThread[t.ID]))
	}
	return out, nil
}

func toReviewThreadResponse(t db.ReviewThread, comments []reviewThreadCommentResponse) reviewThreadResponse {
	if comments == nil {
		comments = []reviewThreadCommentResponse{}
	}
	return reviewThreadResponse{
		ID:        t.ID,
		Path:      t.Path,
		Side:      t.Side,
		Line:      t.Line,
		StartLine: t.StartLine,
		CommitSHA: t.CommitSHA,
		Status:    t.Status,
		Hidden:    t.HiddenAt != nil,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
		Comments:  comments,
	}
}

func (s *Server) listReviewThreads(ctx context.Context, input *listReviewThreadsInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

func (s *Server) createReviewThreads(ctx context.Context, input *createReviewThreadsInput) (*createReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if len(input.Body.Threads) == 0 {
		return nil, huma.Error400BadRequest("at least one thread is required")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	in := make([]db.NewReviewThread, 0, len(input.Body.Threads))
	for _, t := range input.Body.Threads {
		if t.Side != "LEFT" && t.Side != "RIGHT" {
			return nil, huma.Error400BadRequest("side must be LEFT or RIGHT")
		}
		if t.Body == "" {
			return nil, huma.Error400BadRequest("each thread needs a comment body")
		}
		in = append(in, db.NewReviewThread{
			Path: t.Path, Side: t.Side, Line: t.Line,
			StartLine: t.StartLine, CommitSHA: t.CommitSHA, Body: t.Body,
		})
	}
	if _, err := s.db.CreateReviewThreads(ctx, mrID, in); err != nil {
		return nil, huma.Error500InternalServerError("create review threads: " + err.Error())
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &createReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

// resolveThreadForMR confirms the thread exists and belongs to the MR
// behind this PR-shaped route, guarding against cross-worktree ids.
func (s *Server) resolveThreadForMR(ctx context.Context, owner, name string, number int, threadID int64) (int64, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, owner, name, number)
	if err != nil {
		return 0, huma.Error404NotFound("worktree not found")
	}
	th, err := s.db.GetReviewThread(ctx, threadID)
	if err != nil || th.MergeRequestID != mrID {
		return 0, huma.Error404NotFound("review thread not found")
	}
	return mrID, nil
}

func (s *Server) addReviewThreadComment(ctx context.Context, input *addReviewThreadCommentInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if input.Body.Body == "" {
		return nil, huma.Error400BadRequest("comment body is required")
	}
	author := input.Body.Author
	if author == "" {
		author = "user"
	}
	if author != "user" && author != "agent" {
		return nil, huma.Error400BadRequest("author must be user or agent")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if _, err := s.db.AddReviewThreadComment(ctx, input.ThreadID, author, input.Body.Body, nil); err != nil {
		return nil, huma.Error500InternalServerError("add comment: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) hideReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.HideReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("hide thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) unhideReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.UnhideReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("unhide thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) resolveReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.SetReviewThreadStatus(ctx, input.ThreadID, "resolved"); err != nil {
		return nil, huma.Error500InternalServerError("resolve thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

// oneReviewThreadOutput re-reads a single thread (with comments) for the
// action responses.
func (s *Server) oneReviewThreadOutput(ctx context.Context, threadID int64) (*reviewThreadOutput, error) {
	th, err := s.db.GetReviewThread(ctx, threadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload thread: " + err.Error())
	}
	all, err := s.db.ListReviewThreadCommentsForMR(ctx, th.MergeRequestID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload comments: " + err.Error())
	}
	var comments []reviewThreadCommentResponse
	for _, c := range all {
		if c.ThreadID != threadID {
			continue
		}
		comments = append(comments, reviewThreadCommentResponse{
			ID:        c.ID,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := &reviewThreadOutput{Body: toReviewThreadResponse(th, comments)}
	return out, nil
}
```

- [ ] **Step 2: Register the route group**

In `internal/server/huma_routes.go`, find the `s.registerSessionRoutes(api)` call (~line 499) and add the new group right after it:

```go
	s.registerSessionRoutes(api)
	s.registerReviewThreadRoutes(api)
```

- [ ] **Step 3: Verify it compiles and vets**

Run: `go build ./... && go vet ./internal/server`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/server/huma_routes_review_threads.go internal/server/huma_routes.go
git commit -m "feat(server): review-thread REST routes (list/create/comment/hide/resolve)"
```

---

### Task 5: Regenerate OpenAPI spec + Go client

**Files:**
- Modify (generated): `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts`

- [ ] **Step 1: Regenerate the specs + TS schema**

Run: `make api-generate`
Expected: updates the two `openapi.json` files and `packages/ui/src/api/generated/schema.ts` with the new `review-threads` paths.

- [ ] **Step 2: Regenerate the Go client**

Run: `go generate ./internal/apiclient/generated`
Expected: `client.gen.go` gains `…ReviewThreads…WithResponse` methods.

- [ ] **Step 3: Confirm the generated method names**

Run: `grep -o "ReviewThreads[A-Za-z]*WithResponse" internal/apiclient/generated/client.gen.go | sort -u`
Expected (names may vary slightly with the generator; use whatever appears here in Task 6):
```
GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse
PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse
PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse
PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse
PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse
PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdUnhideWithResponse
```

- [ ] **Step 4: Verify everything still builds**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go packages/ui/src/api/generated/schema.ts
git commit -m "chore(api): regenerate client for review-threads endpoints"
```

---

### Task 6: e2e tests through the generated client

**Files:**
- Create: `internal/server/review_threads_e2e_test.go`

> Use the exact generated method/body names confirmed in Task 5, Step 3. The names below follow the AI-thread precedent (`worktrees_e2e_test.go:583`); adjust if the generator differs.

- [ ] **Step 1: Write the e2e test**

Create `internal/server/review_threads_e2e_test.go`:

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

// setupLocalWorktree registers a local repo + worktree row and returns
// its id (the "number" in PR-shaped local routes). No real git tree is
// needed: review-thread routes only resolve the synthetic MR.
func setupLocalWorktree(t *testing.T, database *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: t.TempDir(), Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	return w.ID
}

func TestAPIReviewThreadsLifecycle(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := setupLocalWorktree(t, database)

	start := int64(8)
	createBody := generated.PostReposByOwnerByNamePullsByNumberReviewThreadsJSONRequestBody{
		Threads: []struct {
			Body      string  `json:"body"`
			CommitSha string  `json:"commit_sha"`
			Line      int     `json:"line"`
			Path      string  `json:"path"`
			Side      string  `json:"side"`
			StartLine *int64  `json:"start_line,omitempty"`
		}{
			{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc123", Body: "rename this"},
			{Path: "b.go", Side: "RIGHT", Line: 20, StartLine: &start, CommitSha: "abc123", Body: "extract a helper"},
		},
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num, createBody,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.Len(createResp.JSON200.Threads, 2)
	assert.Equal("open", createResp.JSON200.Threads[0].Status)
	require.Len(createResp.JSON200.Threads[0].Comments, 1)
	assert.Equal("user", createResp.JSON200.Threads[0].Comments[0].Author)
	assert.Equal("rename this", createResp.JSON200.Threads[0].Comments[0].Body)
	threadID := createResp.JSON200.Threads[0].Id

	// List returns both threads with their root comments.
	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200)
	require.Len(listResp.JSON200.Threads, 2)

	// Reply as the agent.
	replyBody := generated.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsJSONRequestBody{
		Body: "agreed, will rename", Author: ptr("agent"),
	}
	replyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
		ctx, "local", "demo", num, threadID, replyBody,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, replyResp.StatusCode())
	require.NotNil(replyResp.JSON200)
	require.Len(replyResp.JSON200.Comments, 2)
	assert.Equal("agent", replyResp.JSON200.Comments[1].Author)

	// Hide, then resolve.
	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hideResp.StatusCode())
	require.NotNil(hideResp.JSON200)
	assert.True(hideResp.JSON200.Hidden)

	resolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resolveResp.StatusCode())
	require.NotNil(resolveResp.JSON200)
	assert.Equal("resolved", resolveResp.JSON200.Status)
}

func TestAPIReviewThreadsRejectNonLocal(t *testing.T) {
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(t, err)
	Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
}

func ptr[T any](v T) *T { return &v }
```

> The anonymous-struct literal for `Threads` must match the generator's field order/types exactly — copy it from the `…JSONRequestBody` definition the generator emitted (check `internal/apiclient/generated/client.gen.go`). If `ptr` already exists in the package's test files, drop the local definition here.

- [ ] **Step 2: Run the e2e tests**

Run: `go test ./internal/server -run TestAPIReviewThreads -shuffle=on`
Expected: PASS (both tests).

- [ ] **Step 3: Run the full server package**

Run: `go test ./internal/server -shuffle=on`
Expected: PASS — no regressions.

- [ ] **Step 4: Commit**

```bash
git add internal/server/review_threads_e2e_test.go
git commit -m "test(server): e2e coverage for review-thread endpoints"
```

---

## Self-review

**Spec coverage (Phase 1 backend slice):**
- Data model `review_threads` + `review_thread_comments`, FK to synthetic MR, anchoring columns from `ai_threads` → Task 1. ✓
- Status lifecycle (`open`/`discussed`/`applied`/`resolved`) storable + settable → Tasks 1, 3. ✓
- Hide via `hidden_at` (no remote-style staleness) → Tasks 1, 3. ✓
- Author `user`/`agent` on comments → Tasks 1, 3, 4. ✓
- REST: list, bulk-create-from-drafts, add comment, hide/unhide, resolve, gated to local, `mr_id` via `resolveOrEnsureMRID` → Task 4. ✓
- e2e via the generated client → Task 6. ✓
- **Deferred to later phases (intentionally not here):** submit mode picker + discuss/apply turns + tool-gating (Phase 2); the `mode` field on create and the kickoff turn (Phase 2); `/local/reviews` discovery endpoints + MCP (Phase 3); frontend rendering + submit seam (Phase 1b). The create endpoint here is persist-only, matching the spec's persist-only mode.

**Placeholder scan:** none — every step has runnable code/commands.

**Type consistency:** `NewReviewThread`, `ReviewThread`, `ReviewThreadComment` field names match across Tasks 2–4; query names (`CreateReviewThreads`, `GetReviewThread`, `ListReviewThreadsForMR`, `AddReviewThreadComment`, `ListReviewThreadCommentsForMR`, `SetReviewThreadStatus`, `HideReviewThread`, `UnhideReviewThread`) are used consistently in handlers (Task 4) and tests (Tasks 2, 3, 6). Generated client method names are confirmed empirically in Task 5 before use in Task 6.

**Known risk to verify during execution:**
- `insertTestMR` assumes `UpsertMergeRequest` accepts the minimal field set; if it errors on a NOT NULL column, copy the full field set from `local_dispatch.go:111-128`.
- The generated `…JSONRequestBody` anonymous struct shape in Task 6 must be copied verbatim from `client.gen.go` after Task 5.
