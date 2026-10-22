# Server-side Review Drafts Implementation Plan (PR 1 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move draft review comments from browser localStorage into SQLite, behind an HTTP API, with no visible behavior change, so later PRs can let Claude create and edit drafts.

**Architecture:** New `middleman_review_drafts` and `middleman_review_draft_meta` tables (migration 000027) with a query layer in `internal/db/queries_review_drafts.go`. New Huma routes in `internal/server/huma_routes_review_drafts.go` for CRUD, a cross-PR summary, and a publish endpoint that posts `state='draft'` rows to GitHub, reusing the inline-comment posting core extracted from `submitReview`. Frontend keeps the `diff.svelte.ts` draft API (synchronous, optimistic, in-memory `$state`) but persists through a new fetch module `reviewDraftsApi.ts` instead of localStorage, and imports legacy localStorage drafts once.

**Tech Stack:** Go, Huma v2, modernc.org/sqlite, testify; Svelte 5 runes, TypeScript, Vitest (run via `frontend/`), Bun.

**Spec:** `docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md` (this plan implements Delivery PR 1: "Server-side drafts").

## Global Constraints

- Datetimes UTC in storage; API emits UTC RFC3339.
- No CGO; no new Go or JS dependencies.
- Use Bun only (`bun install`, `bun run ...`, `bunx`); never npm.
- Go tests: testify only (`require` for preconditions, `assert` otherwise); no `t.Fatal`/`t.Error*`/`t.Fail*`; more than 3 assertions -> `assert := Assert.New(t)` (in `internal/server` tests the package is imported as `Assert`); table-driven where there are cases; `openTestDB(t)` for DB tests; `t.TempDir()` for temp dirs.
- Run Go tests with `-shuffle=on`; never `-count=1`; never `-v`.
- E2E tests (HTTP API + real SQLite, generated Go client in `internal/apiclient`) for every API behavior.
- Migrations: new numbered files only, honest `.down.sql` (`context/db-migrations.md`).
- Regenerate API artifacts with `make api-generate` after route changes; commit the regenerated files.
- Conventional commit messages, ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never amend, never `--no-verify`, never change branches.
- No emojis in code or output.
- Do the task requested; do not add Claude/AI features in this PR.

## Spec deviations (decided while planning; spec updated in Task 0)

- Draft ids are client-generated UUID strings (`TEXT PRIMARY KEY`), not autoincrement integers. Optimistic creates need a stable id before the server responds, and it makes the localStorage import idempotent (`INSERT OR IGNORE`). `middleman_review_draft_messages.draft_id` becomes `TEXT` in PR 3.
- GitHub publish is a new route `POST .../review-drafts/publish` that reads drafts from the DB. The existing `POST .../review` stays unchanged (shared posting core), so existing tests and callers keep working.
- Local publish stays frontend-driven in this PR: `ReviewPanel` creates review threads through the existing route, then deletes the drafts through the new API. Moving it server-side lands with PR 4.
- The spec's `reviewDrafts.svelte.ts` reactive store is deferred to PR 2; this PR adds only a fetch module, and the in-memory state stays in `diff.svelte.ts`.

## Review Focus

1. Deleting a draft before its create request lands -> server must not keep a zombie draft. (Task 5, test "remove waits for pending create")
2. localStorage import running twice (two tabs, or a crash mid-import) -> no duplicate drafts. (Task 1 test "insert is idempotent on id"; Task 5 test "import is idempotent and clears the key only on success")
3. Publish fails on the second of three drafts -> first is deleted (it is on GitHub), second keeps `submit_error`, third untouched; retry posts only the remaining two. (Task 3, `TestAPIPublishReviewDrafts_PartialFailure`)
4. Local worktree switches branches -> drafts from the old branch are hidden, not lost, and reappear on switch-back. (Task 2, `TestAPIReviewDraftsLocalBranchScoped`)
5. Switching PRs while a drafts load is in flight -> the late response must not overwrite the new PR's drafts. (Task 5 test "stale load is ignored")

---

### Task 0: Record spec deviations

**Files:**
- Modify: `docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md`

- [ ] **Step 1: Edit the spec**

In the `middleman_review_drafts` DDL, change `id INTEGER PRIMARY KEY AUTOINCREMENT,` to `id TEXT PRIMARY KEY,  -- client-generated UUID`. In `middleman_review_draft_messages`, change `draft_id INTEGER NOT NULL,` to `draft_id TEXT NOT NULL,`. In "Server-side drafts", replace the sentence beginning "`submitReview` (`internal/server/huma_routes.go:1208`) reads" with:

```markdown
A new `POST .../review-drafts/publish {event, body, commit_id?}` reads `state=draft` rows from the DB and posts them using the same inline-comment core as `submitReview` (`internal/server/huma_routes.go:1208`), which stays for compatibility. Each draft is deleted as soon as GitHub accepts it, so on partial failure only unposted drafts remain, the failing one with `submit_error` set. Local publish stays frontend-driven until PR 4.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md
git commit -m "docs: record review draft id and publish route decisions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 1: Migration and DB query layer

**Files:**
- Create: `internal/db/migrations/000027_add_review_drafts.up.sql`
- Create: `internal/db/migrations/000027_add_review_drafts.down.sql`
- Create: `internal/db/queries_review_drafts.go`
- Test: `internal/db/queries_review_drafts_test.go`
- Modify: `internal/db/db_test.go` (add the two tables to `TestOpenAndSchema`'s `tables` list)

**Interfaces:**
- Consumes: `DB.rw`, `DB.ro` (existing), `seedAIQuestionTestMR(t, d) int64` (existing test helper in `internal/db`).
- Produces (exact):

```go
type ReviewDraft struct {
	ID             string
	MergeRequestID int64
	Branch         string
	Path           string
	Side           string
	Line           int
	StartLine      *int
	CommitSHA      string
	InReplyTo      *int64
	Body           string
	Origin         string // "user" | "claude"
	State          string // "suggestion" | "draft" | "discarded"
	SubmitError    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	BodyEditedAt   *time.Time
}

type NewReviewDraft struct {
	ID        string
	Path      string
	Side      string
	Line      int
	StartLine *int
	CommitSHA string
	InReplyTo *int64
	Body      string
	CreatedAt time.Time // zero -> now
}

type ReviewDraftMeta struct {
	Body      string
	Event     string
	UpdatedAt time.Time
}

type ReviewDraftCount struct {
	Owner  string
	Name   string
	Number int
	Count  int
}

func (d *DB) InsertUserReviewDrafts(ctx context.Context, mrID int64, branch string, in []NewReviewDraft) ([]ReviewDraft, error)
func (d *DB) ListReviewDrafts(ctx context.Context, mrID int64, branch string) ([]ReviewDraft, error)
func (d *DB) GetReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) // nil, nil when missing
func (d *DB) UpdateReviewDraftBody(ctx context.Context, mrID int64, branch, id, body string) (*ReviewDraft, error) // nil, nil when missing
func (d *DB) DeleteReviewDrafts(ctx context.Context, mrID int64, branch string, ids []string) (int64, error)
func (d *DB) SetReviewDraftSubmitError(ctx context.Context, id, msg string) error
func (d *DB) GetReviewDraftMeta(ctx context.Context, mrID int64, branch string) (ReviewDraftMeta, error) // zero value when missing
func (d *DB) UpsertReviewDraftMeta(ctx context.Context, mrID int64, branch, body, event string) (ReviewDraftMeta, error)
func (d *DB) DeleteReviewDraftMeta(ctx context.Context, mrID int64, branch string) error
func (d *DB) ListReviewDraftCounts(ctx context.Context) ([]ReviewDraftCount, error) // non-local repos, state='draft' only
```

Columns `discarded_from`, `run_id`, `claude_session_id`, `chat_worktree_path` are created now (so PR 2 and 3 need no table rebuild) but have no Go fields yet. `ListReviewDrafts` excludes `state='discarded'`.

- [ ] **Step 1: Write the migration**

`internal/db/migrations/000027_add_review_drafts.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS middleman_review_drafts (
    id                 TEXT     PRIMARY KEY,
    merge_request_id   INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch             TEXT     NOT NULL DEFAULT '',
    path               TEXT     NOT NULL,
    side               TEXT     NOT NULL CHECK (side IN ('LEFT', 'RIGHT')),
    line               INTEGER  NOT NULL,
    start_line         INTEGER,
    commit_sha         TEXT     NOT NULL,
    in_reply_to        INTEGER,
    body               TEXT     NOT NULL,
    origin             TEXT     NOT NULL CHECK (origin IN ('user', 'claude')),
    state              TEXT     NOT NULL CHECK (state IN ('suggestion', 'draft', 'discarded')),
    discarded_from     TEXT     NOT NULL DEFAULT '',
    run_id             INTEGER,
    claude_session_id  TEXT     NOT NULL DEFAULT '',
    chat_worktree_path TEXT     NOT NULL DEFAULT '',
    submit_error       TEXT     NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    body_edited_at     DATETIME
);

CREATE INDEX IF NOT EXISTS idx_review_drafts_mr_branch_state
    ON middleman_review_drafts (merge_request_id, branch, state);

CREATE TABLE IF NOT EXISTS middleman_review_draft_meta (
    merge_request_id INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch           TEXT     NOT NULL DEFAULT '',
    body             TEXT     NOT NULL DEFAULT '',
    event            TEXT     NOT NULL DEFAULT '',
    updated_at       DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, branch)
);
```

`internal/db/migrations/000027_add_review_drafts.down.sql`:

```sql
DROP TABLE IF EXISTS middleman_review_draft_meta;
DROP INDEX IF EXISTS idx_review_drafts_mr_branch_state;
DROP TABLE IF EXISTS middleman_review_drafts;
```

Add `"middleman_review_drafts"` and `"middleman_review_draft_meta"` to the `tables` slice in `TestOpenAndSchema` (`internal/db/db_test.go`).

- [ ] **Step 2: Write the failing tests**

`internal/db/queries_review_drafts_test.go`:

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewDraftsInsertListUpdateDelete(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	start := 3
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{
		{ID: "d-1", Path: "a.go", Side: "RIGHT", Line: 5, StartLine: &start, CommitSHA: "abc", Body: "one", CreatedAt: created},
		{ID: "d-2", Path: "b.go", Side: "LEFT", Line: 1, CommitSHA: "abc", Body: "two"},
	})
	require.NoError(err)
	require.Len(got, 2)
	assert.Equal("user", got[0].Origin)
	assert.Equal("draft", got[0].State)
	assert.Equal(created, got[0].CreatedAt)
	require.NotNil(got[0].StartLine)
	assert.Equal(3, *got[0].StartLine)
	assert.Nil(got[1].StartLine)

	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 2)
	assert.Equal("d-1", list[0].ID) // ordered by created_at, then id

	upd, err := d.UpdateReviewDraftBody(ctx, mrID, "", "d-2", "two edited")
	require.NoError(err)
	require.NotNil(upd)
	assert.Equal("two edited", upd.Body)
	require.NotNil(upd.BodyEditedAt)

	missing, err := d.UpdateReviewDraftBody(ctx, mrID, "", "nope", "x")
	require.NoError(err)
	assert.Nil(missing)

	n, err := d.DeleteReviewDrafts(ctx, mrID, "", []string{"d-1", "nope"})
	require.NoError(err)
	assert.Equal(int64(1), n)
	list, err = d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
	assert.Equal("d-2", list[0].ID)
}

func TestReviewDraftsInsertIsIdempotentOnID(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	in := []NewReviewDraft{{ID: "dup", Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "first"}}

	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", in)
	require.NoError(err)
	in[0].Body = "second"
	got, err := d.InsertUserReviewDrafts(ctx, mrID, "", in)
	require.NoError(err)
	require.Len(got, 1)
	require.Equal("first", got[0].Body) // existing row wins

	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
}

func TestReviewDraftsScopedByMRAndBranch(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	_, err := d.InsertUserReviewDrafts(ctx, mrID, "feat/a", []NewReviewDraft{{ID: "a", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "on a"}})
	require.NoError(err)
	_, err = d.InsertUserReviewDrafts(ctx, mrID, "feat/b", []NewReviewDraft{{ID: "b", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "on b"}})
	require.NoError(err)

	onA, err := d.ListReviewDrafts(ctx, mrID, "feat/a")
	require.NoError(err)
	require.Len(onA, 1)
	assert.Equal("a", onA[0].ID)

	// Cross-branch get/update/delete are no-ops.
	g, err := d.GetReviewDraft(ctx, mrID, "feat/a", "b")
	require.NoError(err)
	assert.Nil(g)
	n, err := d.DeleteReviewDrafts(ctx, mrID, "feat/a", []string{"b"})
	require.NoError(err)
	assert.Equal(int64(0), n)
	// Wrong MR is a no-op too.
	n, err = d.DeleteReviewDrafts(ctx, mrID+999, "feat/b", []string{"b"})
	require.NoError(err)
	assert.Equal(int64(0), n)
}

func TestReviewDraftMetaUpsert(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	m, err := d.GetReviewDraftMeta(ctx, mrID, "")
	require.NoError(err)
	assert.Empty(m.Body)
	assert.True(m.UpdatedAt.IsZero())

	_, err = d.UpsertReviewDraftMeta(ctx, mrID, "", "summary", "APPROVE")
	require.NoError(err)
	m, err = d.UpsertReviewDraftMeta(ctx, mrID, "", "summary 2", "COMMENT")
	require.NoError(err)
	assert.Equal("summary 2", m.Body)
	assert.Equal("COMMENT", m.Event)

	require.NoError(d.DeleteReviewDraftMeta(ctx, mrID, ""))
	m, err = d.GetReviewDraftMeta(ctx, mrID, "")
	require.NoError(err)
	assert.Empty(m.Event)
}

func TestReviewDraftsSubmitErrorAndCascade(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{{ID: "e", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "b"}})
	require.NoError(err)

	require.NoError(d.SetReviewDraftSubmitError(ctx, "e", "could not be resolved"))
	g, err := d.GetReviewDraft(ctx, mrID, "", "e")
	require.NoError(err)
	require.NotNil(g)
	require.Equal("could not be resolved", g.SubmitError)

	_, err = d.rw.ExecContext(ctx, `DELETE FROM middleman_merge_requests WHERE id = ?`, mrID)
	require.NoError(err)
	var n int
	require.NoError(d.ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM middleman_review_drafts`).Scan(&n))
	require.Equal(0, n)
}

func TestListReviewDraftCounts(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{
		{ID: "c1", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "b"},
		{ID: "c2", Path: "x", Side: "RIGHT", Line: 2, CommitSHA: "s", Body: "b"},
	})
	require.NoError(err)

	counts, err := d.ListReviewDraftCounts(ctx)
	require.NoError(err)
	require.Len(counts, 1)
	assert.Equal(2, counts[0].Count)
	assert.NotEmpty(counts[0].Owner)
	assert.NotZero(counts[0].Number)
}
```

`seedAIQuestionTestMR` (`internal/db/queries_ai_test.go`) seeds GitHub repo `acme/widget`, so it counts toward `ListReviewDraftCounts`.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/db -run 'TestReviewDraft|TestListReviewDraftCounts|TestOpenAndSchema' -shuffle=on`
Expected: FAIL to compile (`undefined: NewReviewDraft`).

- [ ] **Step 4: Implement `internal/db/queries_review_drafts.go`**

```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ReviewDraft is one row in middleman_review_drafts: an inline review
// comment the reviewer (or, from PR 2 on, Claude) has staged but not
// published. Branch is "" for GitHub PRs and the worktree branch for
// local reviews, mirroring middleman_review_threads.
type ReviewDraft struct {
	ID             string
	MergeRequestID int64
	Branch         string
	Path           string
	Side           string
	Line           int
	StartLine      *int
	CommitSHA      string
	InReplyTo      *int64
	Body           string
	Origin         string
	State          string
	SubmitError    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	BodyEditedAt   *time.Time
}

// NewReviewDraft is a reviewer-authored draft. ID is client-generated
// so optimistic UI writes and the one-time localStorage import are
// idempotent.
type NewReviewDraft struct {
	ID        string
	Path      string
	Side      string
	Line      int
	StartLine *int
	CommitSHA string
	InReplyTo *int64
	Body      string
	CreatedAt time.Time
}

// ReviewDraftMeta is the per-PR review summary and verdict.
type ReviewDraftMeta struct {
	Body      string
	Event     string
	UpdatedAt time.Time
}

// ReviewDraftCount is the number of publishable drafts on one PR.
type ReviewDraftCount struct {
	Owner  string
	Name   string
	Number int
	Count  int
}

const reviewDraftColumns = `id, merge_request_id, branch, path, side, line, start_line,
	commit_sha, in_reply_to, body, origin, state, submit_error,
	created_at, updated_at, body_edited_at`

func scanReviewDraft(sc interface{ Scan(...any) error }) (ReviewDraft, error) {
	var (
		r         ReviewDraft
		startLine sql.NullInt64
		inReplyTo sql.NullInt64
		editedAt  sql.NullTime
	)
	err := sc.Scan(&r.ID, &r.MergeRequestID, &r.Branch, &r.Path, &r.Side, &r.Line,
		&startLine, &r.CommitSHA, &inReplyTo, &r.Body, &r.Origin, &r.State,
		&r.SubmitError, &r.CreatedAt, &r.UpdatedAt, &editedAt)
	if err != nil {
		return r, err
	}
	if startLine.Valid {
		v := int(startLine.Int64)
		r.StartLine = &v
	}
	if inReplyTo.Valid {
		v := inReplyTo.Int64
		r.InReplyTo = &v
	}
	if editedAt.Valid {
		v := editedAt.Time.UTC()
		r.BodyEditedAt = &v
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r, nil
}

// InsertUserReviewDrafts inserts reviewer drafts in state 'draft'.
// Rows whose id already exists are left untouched (INSERT OR IGNORE),
// which makes retries and the localStorage import safe. Returns the
// stored rows for every requested id, in input order.
func (d *DB) InsertUserReviewDrafts(
	ctx context.Context, mrID int64, branch string, in []NewReviewDraft,
) ([]ReviewDraft, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	for _, n := range in {
		created := n.CreatedAt.UTC()
		if n.CreatedAt.IsZero() {
			created = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO middleman_review_drafts
			     (id, merge_request_id, branch, path, side, line, start_line,
			      commit_sha, in_reply_to, body, origin, state, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'user', 'draft', ?, ?)`,
			n.ID, mrID, branch, n.Path, n.Side, n.Line, n.StartLine,
			n.CommitSHA, n.InReplyTo, n.Body, created, now,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]ReviewDraft, 0, len(in))
	for _, n := range in {
		r, err := d.GetReviewDraft(ctx, mrID, branch, n.ID)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

// ListReviewDrafts returns non-discarded drafts for one PR/branch,
// oldest first.
func (d *DB) ListReviewDrafts(
	ctx context.Context, mrID int64, branch string,
) ([]ReviewDraft, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT `+reviewDraftColumns+`
		   FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND state != 'discarded'
		  ORDER BY created_at, id`,
		mrID, branch,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDraft{}
	for rows.Next() {
		r, err := scanReviewDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReviewDraft returns nil, nil when the id does not exist on this
// PR/branch. Scoping by mr+branch keeps one PR's route from reaching
// another PR's drafts.
func (d *DB) GetReviewDraft(
	ctx context.Context, mrID int64, branch, id string,
) (*ReviewDraft, error) {
	row := d.rw.QueryRowContext(ctx,
		`SELECT `+reviewDraftColumns+`
		   FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`,
		mrID, branch, id,
	)
	r, err := scanReviewDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateReviewDraftBody records a reviewer edit. Returns nil, nil when
// the draft does not exist on this PR/branch.
func (d *DB) UpdateReviewDraftBody(
	ctx context.Context, mrID int64, branch, id, body string,
) (*ReviewDraft, error) {
	now := time.Now().UTC()
	res, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts
		    SET body = ?, updated_at = ?, body_edited_at = ?, submit_error = ''
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`,
		body, now, now, mrID, branch, id,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return d.GetReviewDraft(ctx, mrID, branch, id)
}

// DeleteReviewDrafts hard-deletes the given ids on this PR/branch and
// returns how many rows went away. Unknown ids are ignored.
func (d *DB) DeleteReviewDrafts(
	ctx context.Context, mrID int64, branch string, ids []string,
) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, mrID, branch)
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ?
		    AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`,
		args...,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetReviewDraftSubmitError stores the GitHub error that blocked
// publishing this draft. Cleared by the next body edit.
func (d *DB) SetReviewDraftSubmitError(ctx context.Context, id, msg string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts SET submit_error = ?, updated_at = ? WHERE id = ?`,
		msg, time.Now().UTC(), id,
	)
	return err
}

// GetReviewDraftMeta returns the zero value when nothing is stored.
func (d *DB) GetReviewDraftMeta(
	ctx context.Context, mrID int64, branch string,
) (ReviewDraftMeta, error) {
	var m ReviewDraftMeta
	err := d.ro.QueryRowContext(ctx,
		`SELECT body, event, updated_at FROM middleman_review_draft_meta
		  WHERE merge_request_id = ? AND branch = ?`,
		mrID, branch,
	).Scan(&m.Body, &m.Event, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDraftMeta{}, nil
	}
	m.UpdatedAt = m.UpdatedAt.UTC()
	return m, err
}

func (d *DB) UpsertReviewDraftMeta(
	ctx context.Context, mrID int64, branch, body, event string,
) (ReviewDraftMeta, error) {
	now := time.Now().UTC()
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_review_draft_meta (merge_request_id, branch, body, event, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(merge_request_id, branch) DO UPDATE
		     SET body = excluded.body, event = excluded.event, updated_at = excluded.updated_at`,
		mrID, branch, body, event, now,
	)
	if err != nil {
		return ReviewDraftMeta{}, err
	}
	return ReviewDraftMeta{Body: body, Event: event, UpdatedAt: now}, nil
}

func (d *DB) DeleteReviewDraftMeta(ctx context.Context, mrID int64, branch string) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_review_draft_meta WHERE merge_request_id = ? AND branch = ?`,
		mrID, branch,
	)
	return err
}

// ListReviewDraftCounts returns publishable-draft counts per GitHub PR
// for the sidebar's "in review" chip. Local worktrees are excluded:
// the sidebar only lists GitHub PRs.
func (d *DB) ListReviewDraftCounts(ctx context.Context) ([]ReviewDraftCount, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT r.owner, r.name, mr.number, COUNT(*)
		   FROM middleman_review_drafts rd
		   JOIN middleman_merge_requests mr ON mr.id = rd.merge_request_id
		   JOIN middleman_repos r ON r.id = mr.repo_id
		  WHERE rd.state = 'draft' AND r.platform != 'local'
		  GROUP BY r.owner, r.name, mr.number`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDraftCount{}
	for rows.Next() {
		var c ReviewDraftCount
		if err := rows.Scan(&c.Owner, &c.Name, &c.Number, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

`GetReviewDraft` deliberately reads from `d.rw` so `InsertUserReviewDrafts` and `UpdateReviewDraftBody` see their own writes; keep it that way.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/db -run 'TestReviewDraft|TestListReviewDraftCounts|TestOpenAndSchema' -shuffle=on`
Expected: PASS. Then `go test ./internal/db -shuffle=on` (whole package, catches the migration-version test).

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/000027_add_review_drafts.up.sql internal/db/migrations/000027_add_review_drafts.down.sql internal/db/queries_review_drafts.go internal/db/queries_review_drafts_test.go internal/db/db_test.go
git commit -m "feat(db): add review drafts and draft meta tables

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Draft CRUD routes

**Files:**
- Create: `internal/server/huma_routes_review_drafts.go`
- Modify: `internal/server/huma_routes.go` (one call to `s.registerReviewDraftRoutes(api)` next to the notes routes near line 443)
- Regenerate: `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts` (via `make api-generate`)
- Test: `internal/server/review_drafts_e2e_test.go`

**Interfaces:**
- Consumes: Task 1 DB functions; `s.resolveOrEnsureMRID`, `s.resolveLocalWorktree`, `s.currentWorktreeBranch`, `isLocalSource` (`internal/server/local_dispatch.go`); test helpers `setupTestServer`, `setupTestServerWithMock`, `setupTestClient`, `seedPR` (`internal/server/api_test.go`), `seedReviewWorktreeGit`, `runGit` (`internal/server/review_threads_branch_e2e_test.go`).
- Produces:

```go
// resolveDraftScope maps a PR-shaped path to (mrID, branch). branch is
// "" for GitHub and the worktree's current branch for local.
func (s *Server) resolveDraftScope(ctx context.Context, owner, name string, number int) (int64, string, error)

type reviewDraftResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Side        string `json:"side"`
	Line        int    `json:"line"`
	StartLine   *int   `json:"start_line,omitempty"`
	CommitSHA   string `json:"commit_sha"`
	InReplyTo   *int64 `json:"in_reply_to,omitempty"`
	Body        string `json:"body"`
	Origin      string `json:"origin"`
	State       string `json:"state"`
	SubmitError string `json:"submit_error,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}
type reviewDraftMetaResponse struct {
	Body  string `json:"body"`
	Event string `json:"event"`
}
type reviewDraftsResponse struct {
	Drafts []reviewDraftResponse   `json:"drafts"`
	Meta   reviewDraftMetaResponse `json:"meta"`
}
```

Routes (all under `/api/v1`):

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/repos/{owner}/{name}/pulls/{number}/review-drafts` | - | `reviewDraftsResponse` |
| POST | `/repos/{owner}/{name}/pulls/{number}/review-drafts` | `{drafts: [reviewDraftInput]}` (1-500) | `reviewDraftsResponse` (full list after insert) |
| PATCH | `/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}` | `{body}` | `reviewDraftResponse`; 404 if missing |
| POST | `/repos/{owner}/{name}/pulls/{number}/review-drafts/delete` | `{ids: [string]}` (1-500) | `{deleted: int}` |
| PUT | `/repos/{owner}/{name}/pulls/{number}/review-draft-meta` | `{body, event}` | `reviewDraftMetaResponse` |
| GET | `/review-drafts/summary` | - | `{prs: [{owner, name, number, count}]}` |

Validation: `id` matches `^[A-Za-z0-9-]{1,64}$` (server generates a UUID via `crypto/rand` when empty), `side` is `LEFT|RIGHT`, `line >= 1`, `path` non-empty unless `in_reply_to` is set, trimmed `body` non-empty, body at most 65536 bytes, `event` in `""|APPROVE|COMMENT|REQUEST_CHANGES`, `created_at` empty or RFC3339. Validation failures are 400 with the field named. Delete is bulk-only (single delete is `ids: [id]`) to keep one route.

- [ ] **Step 1: Write the failing e2e tests**

`internal/server/review_drafts_e2e_test.go`. Generated method and type names come from the paths; after Step 3's `make api-generate`, check exact names with `grep -n "ReviewDrafts\|ReviewDraftMeta" internal/apiclient/generated/client.gen.go` and adjust. Expected names follow the existing pattern (`PostReposByOwnerByNamePullsByNumberReviewWithResponse`):

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
)

func draftIn(id, path string, line int64, body string) generated.ReviewDraftInput {
	return generated.ReviewDraftInput{Id: &id, Path: path, Side: "RIGHT", Line: line, CommitSha: "abc", Body: body}
}

func TestAPIReviewDraftsCRUD(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	created, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1,
		generated.CreateReviewDraftsInputBody{Drafts: []generated.ReviewDraftInput{
			draftIn("d-1", "a.go", 3, "first"),
			draftIn("d-2", "b.go", 9, "second"),
		}})
	require.NoError(err)
	require.Equal(http.StatusOK, created.StatusCode())
	require.Len(created.JSON200.Drafts, 2)
	assert.Equal("user", created.JSON200.Drafts[0].Origin)
	assert.Equal("draft", created.JSON200.Drafts[0].State)

	patched, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widget", 1, "d-2",
		generated.UpdateReviewDraftInputBody{Body: "second edited"})
	require.NoError(err)
	require.Equal(http.StatusOK, patched.StatusCode())
	assert.Equal("second edited", patched.JSON200.Body)

	meta, err := client.HTTP.PutReposByOwnerByNamePullsByNumberReviewDraftMetaWithResponse(ctx, "acme", "widget", 1,
		generated.PutReviewDraftMetaInputBody{Body: "LGTM", Event: "APPROVE"})
	require.NoError(err)
	require.Equal(http.StatusOK, meta.StatusCode())

	del, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsDeleteWithResponse(ctx, "acme", "widget", 1,
		generated.DeleteReviewDraftsInputBody{Ids: []string{"d-1"}})
	require.NoError(err)
	require.Equal(http.StatusOK, del.StatusCode())
	assert.Equal(int64(1), del.JSON200.Deleted)

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	require.Equal(http.StatusOK, list.StatusCode())
	require.Len(list.JSON200.Drafts, 1)
	assert.Equal("d-2", list.JSON200.Drafts[0].Id)
	assert.Equal("LGTM", list.JSON200.Meta.Body)
	assert.Equal("APPROVE", list.JSON200.Meta.Event)

	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	require.Equal(http.StatusOK, summary.StatusCode())
	require.Len(summary.JSON200.Prs, 1)
	assert.Equal(int64(1), summary.JSON200.Prs[0].Count)
}

func TestAPIReviewDraftsValidation(t *testing.T) {
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	bad := func(mut func(*generated.ReviewDraftInput)) generated.ReviewDraftInput {
		d := draftIn("ok-id", "a.go", 1, "body")
		mut(&d)
		return d
	}
	cases := []struct {
		name  string
		draft generated.ReviewDraftInput
	}{
		{"bad side", bad(func(d *generated.ReviewDraftInput) { d.Side = "MIDDLE" })},
		{"zero line", bad(func(d *generated.ReviewDraftInput) { d.Line = 0 })},
		{"blank body", bad(func(d *generated.ReviewDraftInput) { d.Body = "   " })},
		{"empty path", bad(func(d *generated.ReviewDraftInput) { d.Path = "" })},
		{"bad id", bad(func(d *generated.ReviewDraftInput) { id := "../x"; d.Id = &id })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1,
				generated.CreateReviewDraftsInputBody{Drafts: []generated.ReviewDraftInput{tc.draft}})
			require.NoError(t, err)
			Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}
}

func TestAPIReviewDraftsUnknownPRAndDraft(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 404)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())

	seedPR(t, database, "acme", "widget", 1)
	patched, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widget", 1, "missing",
		generated.UpdateReviewDraftInputBody{Body: "x"})
	require.NoError(err)
	require.Equal(http.StatusNotFound, patched.StatusCode())
}

func TestAPIReviewDraftsLocalBranchScoped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)

	_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num,
		generated.CreateReviewDraftsInputBody{Drafts: []generated.ReviewDraftInput{draftIn("on-a", "a.go", 1, "on a")}})
	require.NoError(err)

	runGit(t, dir, "checkout", "-b", "feat/b")
	onB, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, onB.StatusCode())
	assert.Empty(onB.JSON200.Drafts)

	runGit(t, dir, "checkout", "feat/a")
	onA, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Len(onA.JSON200.Drafts, 1)
	assert.Equal("on-a", onA.JSON200.Drafts[0].Id)

	// Local drafts never show up in the GitHub sidebar summary.
	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	assert.Empty(summary.JSON200.Prs)
}
```

Check how `TestAPIReviewThreadsBranchScoped` makes the worktree's branch change visible to `currentWorktreeBranch`; if it re-scans or updates the worktree row after `git checkout`, do the same here.

- [ ] **Step 2: Implement `internal/server/huma_routes_review_drafts.go`**

```go
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/db"
)

const reviewDraftBodyMaxBytes = 65536

var reviewDraftIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type reviewDraftInput struct {
	ID        string `json:"id,omitempty"          doc:"Client-generated id; the server generates one when empty"`
	Path      string `json:"path"                  doc:"File path (may be empty only for replies)"`
	Side      string `json:"side"                  doc:"LEFT or RIGHT"`
	Line      int    `json:"line"                  doc:"1-based line"`
	StartLine *int   `json:"start_line,omitempty"  doc:"First line of a multi-line range"`
	CommitSHA string `json:"commit_sha"            doc:"Commit the comment was written against"`
	InReplyTo *int64 `json:"in_reply_to,omitempty" doc:"Upstream comment id this reply threads under"`
	Body      string `json:"body"                  doc:"Comment body (markdown)"`
	CreatedAt string `json:"created_at,omitempty"  doc:"RFC3339; kept by the localStorage import"`
}

type reviewDraftResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Side        string `json:"side"`
	Line        int    `json:"line"`
	StartLine   *int   `json:"start_line,omitempty"`
	CommitSHA   string `json:"commit_sha"`
	InReplyTo   *int64 `json:"in_reply_to,omitempty"`
	Body        string `json:"body"`
	Origin      string `json:"origin"`
	State       string `json:"state"`
	SubmitError string `json:"submit_error,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type reviewDraftMetaResponse struct {
	Body  string `json:"body"`
	Event string `json:"event"`
}

type reviewDraftsResponse struct {
	Drafts []reviewDraftResponse   `json:"drafts"`
	Meta   reviewDraftMetaResponse `json:"meta"`
}

type reviewDraftsOutput struct{ Body reviewDraftsResponse }
type reviewDraftOutput struct{ Body reviewDraftResponse }
type reviewDraftMetaOutput struct{ Body reviewDraftMetaResponse }

type createReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Drafts []reviewDraftInput `json:"drafts" minItems:"1" maxItems:"500"`
	}
}

type updateReviewDraftInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
	Body    struct {
		Body string `json:"body"`
	}
}

type deleteReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		IDs []string `json:"ids" minItems:"1" maxItems:"500"`
	}
}

type deleteReviewDraftsOutput struct {
	Body struct {
		Deleted int64 `json:"deleted"`
	}
}

type putReviewDraftMetaInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Body  string `json:"body"`
		Event string `json:"event" doc:"APPROVE, COMMENT, REQUEST_CHANGES, or empty"`
	}
}

type reviewDraftSummaryItem struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Number int    `json:"number"`
	Count  int    `json:"count"`
}

type reviewDraftSummaryOutput struct {
	Body struct {
		PRs []reviewDraftSummaryItem `json:"prs"`
	}
}

func (s *Server) registerReviewDraftRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts", s.listReviewDrafts)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts", s.createReviewDrafts)
	huma.Patch(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}", s.updateReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/delete", s.deleteReviewDrafts)
	huma.Put(api, "/repos/{owner}/{name}/pulls/{number}/review-draft-meta", s.putReviewDraftMeta)
	huma.Get(api, "/review-drafts/summary", s.reviewDraftSummary)
}

func (s *Server) resolveDraftScope(
	ctx context.Context, owner, name string, number int,
) (int64, string, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, owner, name, number)
	if err != nil {
		return 0, "", err
	}
	if !isLocalSource(owner) {
		return mrID, "", nil
	}
	w, err := s.resolveLocalWorktree(ctx, name, number)
	if err != nil {
		return 0, "", err
	}
	return mrID, s.currentWorktreeBranch(ctx, w), nil
}

func toReviewDraftResponse(r db.ReviewDraft) reviewDraftResponse {
	return reviewDraftResponse{
		ID: r.ID, Path: r.Path, Side: r.Side, Line: r.Line, StartLine: r.StartLine,
		CommitSHA: r.CommitSHA, InReplyTo: r.InReplyTo, Body: r.Body,
		Origin: r.Origin, State: r.State, SubmitError: r.SubmitError,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) loadReviewDrafts(ctx context.Context, mrID int64, branch string) (*reviewDraftsOutput, error) {
	rows, err := s.db.ListReviewDrafts(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review drafts: " + err.Error())
	}
	meta, err := s.db.GetReviewDraftMeta(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("load review draft meta: " + err.Error())
	}
	out := &reviewDraftsOutput{}
	out.Body.Drafts = make([]reviewDraftResponse, 0, len(rows))
	for _, r := range rows {
		out.Body.Drafts = append(out.Body.Drafts, toReviewDraftResponse(r))
	}
	out.Body.Meta = reviewDraftMetaResponse{Body: meta.Body, Event: meta.Event}
	return out, nil
}

func (s *Server) listReviewDrafts(ctx context.Context, input *repoNumberInput) (*reviewDraftsOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	return s.loadReviewDrafts(ctx, mrID, branch)
}

func validateDraftBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("body is required")
	}
	if len(body) > reviewDraftBodyMaxBytes {
		return fmt.Errorf("body too large: %d bytes (max %d)", len(body), reviewDraftBodyMaxBytes)
	}
	return nil
}

func newReviewDraftID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func toNewReviewDraft(i int, in reviewDraftInput) (db.NewReviewDraft, error) {
	fail := func(msg string) (db.NewReviewDraft, error) {
		return db.NewReviewDraft{}, fmt.Errorf("drafts[%d]: %s", i, msg)
	}
	id := in.ID
	if id == "" {
		id = newReviewDraftID()
	}
	if !reviewDraftIDPattern.MatchString(id) {
		return fail("id must match " + reviewDraftIDPattern.String())
	}
	if in.Side != "LEFT" && in.Side != "RIGHT" {
		return fail("side must be LEFT or RIGHT")
	}
	if in.Line < 1 {
		return fail("line must be positive")
	}
	if in.Path == "" && (in.InReplyTo == nil || *in.InReplyTo <= 0) {
		return fail("path is required")
	}
	if err := validateDraftBody(in.Body); err != nil {
		return fail(err.Error())
	}
	var created time.Time
	if in.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, in.CreatedAt)
		if err != nil {
			return fail("created_at must be RFC3339")
		}
		created = t.UTC()
	}
	return db.NewReviewDraft{
		ID: id, Path: in.Path, Side: in.Side, Line: in.Line, StartLine: in.StartLine,
		CommitSHA: in.CommitSHA, InReplyTo: in.InReplyTo, Body: in.Body, CreatedAt: created,
	}, nil
}

func (s *Server) createReviewDrafts(ctx context.Context, input *createReviewDraftsInput) (*reviewDraftsOutput, error) {
	news := make([]db.NewReviewDraft, 0, len(input.Body.Drafts))
	for i, d := range input.Body.Drafts {
		n, err := toNewReviewDraft(i, d)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		news = append(news, n)
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if _, err := s.db.InsertUserReviewDrafts(ctx, mrID, branch, news); err != nil {
		return nil, huma.Error500InternalServerError("create review drafts: " + err.Error())
	}
	return s.loadReviewDrafts(ctx, mrID, branch)
}

func (s *Server) updateReviewDraft(ctx context.Context, input *updateReviewDraftInput) (*reviewDraftOutput, error) {
	if err := validateDraftBody(input.Body.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	r, err := s.db.UpdateReviewDraftBody(ctx, mrID, branch, input.DraftID, input.Body.Body)
	if err != nil {
		return nil, huma.Error500InternalServerError("update review draft: " + err.Error())
	}
	if r == nil {
		return nil, huma.Error404NotFound("draft not found")
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*r)}, nil
}

func (s *Server) deleteReviewDrafts(ctx context.Context, input *deleteReviewDraftsInput) (*deleteReviewDraftsOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	n, err := s.db.DeleteReviewDrafts(ctx, mrID, branch, input.Body.IDs)
	if err != nil {
		return nil, huma.Error500InternalServerError("delete review drafts: " + err.Error())
	}
	out := &deleteReviewDraftsOutput{}
	out.Body.Deleted = n
	return out, nil
}

func (s *Server) putReviewDraftMeta(ctx context.Context, input *putReviewDraftMetaInput) (*reviewDraftMetaOutput, error) {
	switch input.Body.Event {
	case "", "APPROVE", "COMMENT", "REQUEST_CHANGES":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, COMMENT, REQUEST_CHANGES, or empty")
	}
	if len(input.Body.Body) > reviewDraftBodyMaxBytes {
		return nil, huma.Error400BadRequest("body too large")
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	m, err := s.db.UpsertReviewDraftMeta(ctx, mrID, branch, input.Body.Body, input.Body.Event)
	if err != nil {
		return nil, huma.Error500InternalServerError("save review draft meta: " + err.Error())
	}
	return &reviewDraftMetaOutput{Body: reviewDraftMetaResponse{Body: m.Body, Event: m.Event}}, nil
}

func (s *Server) reviewDraftSummary(ctx context.Context, _ *struct{}) (*reviewDraftSummaryOutput, error) {
	counts, err := s.db.ListReviewDraftCounts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("review draft summary: " + err.Error())
	}
	out := &reviewDraftSummaryOutput{}
	out.Body.PRs = make([]reviewDraftSummaryItem, 0, len(counts))
	for _, c := range counts {
		out.Body.PRs = append(out.Body.PRs, reviewDraftSummaryItem{Owner: c.Owner, Name: c.Name, Number: c.Number, Count: c.Count})
	}
	return out, nil
}
```

Before writing, confirm that `repoNumberInput` has `Owner`, `Name`, `Number` (`grep -n "type repoNumberInput" -A5 internal/server/*.go`). Note `/review-drafts/delete` vs `/review-drafts/{draft_id}`: they differ by method (POST vs PATCH), so no routing conflict. Add `s.registerReviewDraftRoutes(api)` directly after the `huma.Put(api, ".../notes", s.putPRNotes)` line in `huma_routes.go`.

Huma derives request-body schema names from Go types; generated names like `CreateReviewDraftsInputBody` come from that. If generated names differ, fix the test, not the server.

- [ ] **Step 3: Regenerate API artifacts**

Run: `make api-generate`
Expected: changes in `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/server -run 'TestAPIReviewDrafts' -shuffle=on`
Expected: PASS. Then `go test ./internal/server -shuffle=on` to catch OpenAPI snapshot tests, if any.

- [ ] **Step 5: Commit**

```bash
git add internal/server/huma_routes_review_drafts.go internal/server/huma_routes.go internal/server/review_drafts_e2e_test.go frontend/openapi/openapi.json internal/apiclient packages/ui/src/api/generated/schema.ts
git commit -m "feat(server): add review draft CRUD routes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Publish drafts to GitHub from the DB

**Files:**
- Modify: `internal/server/huma_routes.go:1199-1384` (extract shared helpers out of `submitReview`; `translateCreateError` signature)
- Modify: `internal/server/huma_routes_review_drafts.go` (publish route + handler)
- Regenerate API artifacts
- Test: `internal/server/review_drafts_publish_e2e_test.go`

**Interfaces:**
- Consumes: Task 1 (`ListReviewDrafts`, `DeleteReviewDrafts`, `SetReviewDraftSubmitError`, `DeleteReviewDraftMeta`), Task 2 (`resolveDraftScope`, `loadReviewDrafts`), `mockGH` with `createInlineCommentFn` and `lastInlineComments` (`internal/server/api_test.go:53,202`).
- Produces:

```go
// prepareReviewComments validates and normalizes submitReview-shaped
// comments; error is already a huma 400.
func prepareReviewComments(comments []submitReviewComment, fallbackSHA string) ([]preparedComment, error)

// postPreparedComment posts one inline comment or reply, resolving
// reply targets to thread roots when mrID != 0.
func (s *Server) postPreparedComment(ctx context.Context, client ghclient.Client, owner, name string, number int, mrID int64, p preparedComment) error

// postReviewWrapper posts the verdict/summary when needed and records
// the review event; returns nil review when skipped.
func (s *Server) postReviewWrapper(ctx context.Context, client ghclient.Client, owner, name string, number int, event, body, commitID string) (*gh.PullRequestReview, error)

func translateCreateError(err error, owner, name string, number int, failing *preparedComment, postedSoFar int) error
```

Route: `POST /repos/{owner}/{name}/pulls/{number}/review-drafts/publish`, body `{event, body?, commit_id?}`, response `{review_id, state, published}` where `published` is the number of inline comments posted. 400 for owner `local` ("local drafts publish as review threads"). Order: posts drafts oldest first; deletes each draft row right after GitHub accepts it; on the first failure stores the translated message in that draft's `submit_error` and returns the error; posts the wrapper only after every draft succeeded; deletes the meta row after the wrapper (or when no wrapper is needed).

- [ ] **Step 1: Write failing tests**

`internal/server/review_drafts_publish_e2e_test.go`:

```go
package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gh "github.com/google/go-github/v84/github"
	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	ghclient "github.com/wesm/middleman/internal/github"
)

func seedDrafts(t *testing.T, client *apiclient.Client, ids ...string) {
	t.Helper()
	drafts := make([]generated.ReviewDraftInput, 0, len(ids))
	for i, id := range ids {
		drafts = append(drafts, draftIn(id, "a.go", int64(i+1), "body "+id))
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(context.Background(), "acme", "widget", 1,
		generated.CreateReviewDraftsInputBody{Drafts: drafts})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
}

func TestAPIPublishReviewDrafts_PostsAllAndClears(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	mock := &mockGH{}
	srv, database := setupTestServerWithMock(t, mock)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)
	seedDrafts(t, client, "p-1", "p-2")

	body := "Overall LGTM"
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT", Body: &body})
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	assert.Equal(int64(2), resp.JSON200.Published)
	require.Len(mock.lastInlineComments, 2)
	assert.Equal("body p-1", mock.lastInlineComments[0].Body)
	assert.Equal("abc", mock.lastInlineComments[0].CommitID)
	require.NotNil(mock.lastCreateReviewOpts)
	assert.Equal("Overall LGTM", mock.lastCreateReviewOpts.Body)

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	assert.Empty(list.JSON200.Drafts)
	assert.Empty(list.JSON200.Meta.Event)
}

func TestAPIPublishReviewDrafts_PartialFailure(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	calls := 0
	mock := &mockGH{}
	mock.createInlineCommentFn = func(_ context.Context, _, _ string, _ int, opts ghclient.InlineCommentOpts) (*gh.PullRequestComment, error) {
		calls++
		if opts.Body == "body p-2" && calls == 2 {
			return nil, errors.New("Line could not be resolved")
		}
		id := int64(calls)
		return &gh.PullRequestComment{ID: &id}, nil
	}
	srv, database := setupTestServerWithMock(t, mock)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)
	seedDrafts(t, client, "p-1", "p-2", "p-3")

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT"})
	require.NoError(err)
	assert.Equal(http.StatusConflict, resp.StatusCode())
	assert.Nil(mock.lastCreateReviewOpts) // no wrapper after a failure

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	require.Len(list.JSON200.Drafts, 2) // p-1 was posted and removed
	assert.Equal("p-2", list.JSON200.Drafts[0].Id)
	require.NotNil(list.JSON200.Drafts[0].SubmitError)
	assert.Contains(*list.JSON200.Drafts[0].SubmitError, "could not be resolved")
	assert.Equal("p-3", list.JSON200.Drafts[1].Id)

	// Retry posts only what is left.
	retry, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT"})
	require.NoError(err)
	require.Equal(http.StatusOK, retry.StatusCode())
	assert.Equal(int64(2), retry.JSON200.Published)
	assert.Equal(4, calls)
}

func TestAPIPublishReviewDrafts_RejectsLocalAndBadEvent(t *testing.T) {
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	cases := []struct {
		name, owner, event string
	}{
		{"local owner", "local", "COMMENT"},
		{"bad event", "acme", "MAYBE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, tc.owner, "widget", 1,
				generated.PublishReviewDraftsInputBody{Event: tc.event})
			require.NoError(t, err)
			Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}
}
```

If `submit_error` is generated as a non-pointer string, drop the `NotNil`/deref.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/server -run 'TestAPIPublishReviewDrafts' -shuffle=on`
Expected: compile failure (missing generated publish method).

- [ ] **Step 3: Extract helpers from `submitReview`**

In `huma_routes.go`, move the validation loop (lines ~1226-1259) into `prepareReviewComments`, the per-comment posting body of the phase-1 loop (lines ~1288-1306) into `postPreparedComment`, and phase 2 + event persistence (lines ~1314-1342) into `postReviewWrapper`. `submitReview` then reads:

```go
func (s *Server) submitReview(ctx context.Context, input *submitReviewInput) (*submitReviewOutput, error) {
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	switch input.Body.Event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, REQUEST_CHANGES, or COMMENT")
	}
	if input.Body.Event == "REQUEST_CHANGES" && strings.TrimSpace(input.Body.Body) == "" && len(input.Body.Comments) == 0 {
		return nil, huma.Error400BadRequest("REQUEST_CHANGES requires a review body or at least one inline comment")
	}
	preps, err := prepareReviewComments(input.Body.Comments, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	var mrID int64
	if id, lerr := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}); lerr == nil {
		mrID = id
	}
	for posted, p := range preps {
		if err := s.postPreparedComment(ctx, client, input.Owner, input.Name, input.Number, mrID, p); err != nil {
			return nil, translateCreateError(err, input.Owner, input.Name, input.Number, &p, posted)
		}
	}
	review, err := s.postReviewWrapper(ctx, client, input.Owner, input.Name, input.Number, input.Body.Event, input.Body.Body, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	out := submitReviewResponseBody{}
	if review != nil {
		out.ReviewID = review.GetID()
		out.State = review.GetState()
	}
	return &submitReviewOutput{Body: out}, nil
}
```

`postPreparedComment` keeps the existing reply-root resolution (`s.db.ResolveReviewCommentRootID`) when `mrID != 0 && p.in.InReplyTo > 0`. `postReviewWrapper` keeps the "one pending review per pull request" 409 and the `UpsertMREvents` call. Note the old code only looked up `mrID` when a reply existed; looking it up unconditionally is harmless (one indexed query). Change `translateCreateError` to take `owner, name string, number int` instead of `*submitReviewInput`.

Run: `go test ./internal/server -run 'TestAPISubmitReview' -shuffle=on`
Expected: PASS (pure refactor).

- [ ] **Step 4: Add the publish handler**

Append to `huma_routes_review_drafts.go` (add imports `ghclient` and `gh` as used in `huma_routes.go`):

```go
type publishReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Event    string `json:"event"               doc:"APPROVE, REQUEST_CHANGES, or COMMENT"`
		Body     string `json:"body,omitempty"      doc:"Optional top-level review body"`
		CommitID string `json:"commit_id,omitempty" doc:"PR head SHA; fallback anchor for drafts without one"`
	}
}

type publishReviewDraftsOutput struct {
	Body struct {
		ReviewID  int64  `json:"review_id"`
		State     string `json:"state"`
		Published int    `json:"published"`
	}
}

func draftToSubmitComment(d db.ReviewDraft) submitReviewComment {
	c := submitReviewComment{Path: d.Path, Line: d.Line, Side: d.Side, Body: d.Body, CommitID: d.CommitSHA}
	if d.StartLine != nil {
		c.StartLine = *d.StartLine
	}
	if d.InReplyTo != nil {
		c.InReplyTo = *d.InReplyTo
	}
	return c
}

func (s *Server) publishReviewDrafts(ctx context.Context, input *publishReviewDraftsInput) (*publishReviewDraftsOutput, error) {
	if isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("local drafts publish as review threads")
	}
	switch input.Body.Event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, REQUEST_CHANGES, or COMMENT")
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	rows, err := s.db.ListReviewDrafts(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review drafts: " + err.Error())
	}
	drafts := make([]db.ReviewDraft, 0, len(rows))
	comments := make([]submitReviewComment, 0, len(rows))
	for _, r := range rows {
		if r.State != "draft" {
			continue
		}
		drafts = append(drafts, r)
		comments = append(comments, draftToSubmitComment(r))
	}
	if input.Body.Event == "REQUEST_CHANGES" && strings.TrimSpace(input.Body.Body) == "" && len(comments) == 0 {
		return nil, huma.Error400BadRequest("REQUEST_CHANGES requires a review body or at least one inline comment")
	}
	preps, err := prepareReviewComments(comments, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	for i, p := range preps {
		if err := s.postPreparedComment(ctx, client, input.Owner, input.Name, input.Number, mrID, p); err != nil {
			herr := translateCreateError(err, input.Owner, input.Name, input.Number, &p, i)
			_ = s.db.SetReviewDraftSubmitError(ctx, drafts[i].ID, herr.Error())
			return nil, herr
		}
		if _, err := s.db.DeleteReviewDrafts(ctx, mrID, branch, []string{drafts[i].ID}); err != nil {
			return nil, huma.Error500InternalServerError("comment posted but draft not cleared: " + err.Error())
		}
	}
	review, err := s.postReviewWrapper(ctx, client, input.Owner, input.Name, input.Number, input.Body.Event, input.Body.Body, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	_ = s.db.DeleteReviewDraftMeta(ctx, mrID, branch)
	out := &publishReviewDraftsOutput{}
	out.Body.Published = len(preps)
	if review != nil {
		out.Body.ReviewID = review.GetID()
		out.Body.State = review.GetState()
	}
	return out, nil
}
```

`prepareReviewComments` preserves index order, so `preps[i]` corresponds to `drafts[i]`; keep it that way (it must not filter). `herr.Error()` on a huma error includes status text; if that reads badly in the UI, store `err.Error()` wrapped with the same guidance text instead, and keep the test's `Contains` check. Register in `registerReviewDraftRoutes`:

```go
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/publish", s.publishReviewDrafts)
```

- [ ] **Step 5: Regenerate and run tests**

Run: `make api-generate && go test ./internal/server -run 'TestAPIPublishReviewDrafts|TestAPISubmitReview|TestAPIReviewDrafts' -shuffle=on`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/huma_routes.go internal/server/huma_routes_review_drafts.go internal/server/review_drafts_publish_e2e_test.go frontend/openapi/openapi.json internal/apiclient packages/ui/src/api/generated/schema.ts
git commit -m "feat(server): publish review drafts from the database

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Frontend review drafts API module

**Files:**
- Create: `packages/ui/src/stores/reviewDraftsApi.ts`
- Test: `packages/ui/src/stores/reviewDraftsApi.test.ts`

**Interfaces:**
- Consumes: Task 2/3 routes.
- Produces:

```ts
export interface ServerDraft {
  id: string; path: string; side: "LEFT" | "RIGHT"; line: number;
  start_line?: number; commit_sha: string; in_reply_to?: number; body: string;
  origin: "user" | "claude"; state: "suggestion" | "draft" | "discarded";
  submit_error?: string; created_at: string; updated_at: string;
}
export interface ServerDraftsResponse { drafts: ServerDraft[]; meta: { body: string; event: string } }
export interface DraftWrite {
  id: string; path: string; side: "LEFT" | "RIGHT"; line: number;
  start_line?: number; commit_sha: string; in_reply_to?: number; body: string; created_at?: string;
}
export interface PRRef { owner: string; name: string; number: number }
export class DraftApiError extends Error { status: number }
export function createReviewDraftsApi(getBasePath: () => string): {
  list(pr: PRRef): Promise<ServerDraftsResponse>;
  create(pr: PRRef, drafts: DraftWrite[]): Promise<ServerDraftsResponse>;
  updateBody(pr: PRRef, id: string, body: string): Promise<void>;
  remove(pr: PRRef, ids: string[]): Promise<void>;
  putMeta(pr: PRRef, body: string, event: string): Promise<void>;
  publish(pr: PRRef, req: { event: string; body: string; commit_id?: string }): Promise<{ review_id: number; state: string; published: number }>;
  summary(): Promise<Array<{ owner: string; name: string; number: number; count: number }>>;
}
export type ReviewDraftsApi = ReturnType<typeof createReviewDraftsApi>;
```

Raw `fetch` matches `notesPrefix`/`flushNotes` in `diff.svelte.ts` (same base path construction, same error extraction from `detail`/`title`). Check whether those fetches attach a CSRF header (`grep -n "csrf" packages/ui/src/stores/diff.svelte.ts packages/ui/src/api/csrf.ts`); if mutating fetches need one, use the same helper.

- [ ] **Step 1: Write failing tests**

`packages/ui/src/stores/reviewDraftsApi.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { createReviewDraftsApi, DraftApiError } from "./reviewDraftsApi.js";

const pr = { owner: "acme", name: "wid get", number: 7 };

function mockFetch(status: number, body: unknown) {
  const fn = vi.fn(async () =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
  );
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

afterEach(() => vi.restoreAllMocks());

describe("reviewDraftsApi", () => {
  it("lists drafts at the encoded PR path", async () => {
    const fn = mockFetch(200, { drafts: [], meta: { body: "", event: "" } });
    const api = createReviewDraftsApi(() => "/");
    await api.list(pr);
    expect(fn).toHaveBeenCalledWith("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts", expect.anything());
  });

  it("sends deletes as a bulk POST", async () => {
    const fn = mockFetch(200, { deleted: 2 });
    const api = createReviewDraftsApi(() => "/");
    await api.remove(pr, ["a", "b"]);
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts/delete");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ ids: ["a", "b"] });
  });

  it("throws DraftApiError with server detail and status", async () => {
    mockFetch(409, { detail: "could not be resolved" });
    const api = createReviewDraftsApi(() => "/");
    const err = await api.publish(pr, { event: "COMMENT", body: "" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(DraftApiError);
    expect((err as DraftApiError).status).toBe(409);
    expect((err as DraftApiError).message).toBe("could not be resolved");
  });

  it("skips the network for empty deletes", async () => {
    const fn = mockFetch(200, {});
    const api = createReviewDraftsApi(() => "/");
    await api.remove(pr, []);
    expect(fn).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/reviewDraftsApi.test.ts`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement `packages/ui/src/stores/reviewDraftsApi.ts`**

```ts
// Thin fetch wrapper over the review-drafts routes. Raw fetch (not the
// generated client) to match how diff.svelte.ts talks to the notes
// routes, so the same test doubles work.

export interface ServerDraft {
  id: string;
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  in_reply_to?: number;
  body: string;
  origin: "user" | "claude";
  state: "suggestion" | "draft" | "discarded";
  submit_error?: string;
  created_at: string;
  updated_at: string;
}

export interface ServerDraftsResponse {
  drafts: ServerDraft[];
  meta: { body: string; event: string };
}

export interface DraftWrite {
  id: string;
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  start_line?: number;
  commit_sha: string;
  in_reply_to?: number;
  body: string;
  created_at?: string;
}

export interface PRRef {
  owner: string;
  name: string;
  number: number;
}

export class DraftApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export function createReviewDraftsApi(getBasePath: () => string) {
  function prPrefix(pr: PRRef): string {
    return (
      `${getBasePath()}api/v1/repos/` +
      `${encodeURIComponent(pr.owner)}/` +
      `${encodeURIComponent(pr.name)}/` +
      `pulls/${pr.number}`
    );
  }

  async function call<T>(url: string, method: string, body?: unknown): Promise<T> {
    const init: RequestInit = { method };
    if (body !== undefined) {
      init.headers = { "content-type": "application/json" };
      init.body = JSON.stringify(body);
    }
    const response = await fetch(url, init);
    if (!response.ok) {
      const data = (await response.json().catch(() => ({}))) as Record<string, string>;
      throw new DraftApiError(data.detail ?? data.title ?? `HTTP ${response.status}`, response.status);
    }
    return (await response.json().catch(() => ({}))) as T;
  }

  return {
    list(pr: PRRef): Promise<ServerDraftsResponse> {
      return call(`${prPrefix(pr)}/review-drafts`, "GET");
    },
    create(pr: PRRef, drafts: DraftWrite[]): Promise<ServerDraftsResponse> {
      return call(`${prPrefix(pr)}/review-drafts`, "POST", { drafts });
    },
    async updateBody(pr: PRRef, id: string, body: string): Promise<void> {
      await call(`${prPrefix(pr)}/review-drafts/${encodeURIComponent(id)}`, "PATCH", { body });
    },
    async remove(pr: PRRef, ids: string[]): Promise<void> {
      if (ids.length === 0) return;
      await call(`${prPrefix(pr)}/review-drafts/delete`, "POST", { ids });
    },
    async putMeta(pr: PRRef, body: string, event: string): Promise<void> {
      await call(`${prPrefix(pr)}/review-draft-meta`, "PUT", { body, event });
    },
    publish(
      pr: PRRef,
      req: { event: string; body: string; commit_id?: string },
    ): Promise<{ review_id: number; state: string; published: number }> {
      return call(`${prPrefix(pr)}/review-drafts/publish`, "POST", req);
    },
    async summary(): Promise<Array<{ owner: string; name: string; number: number; count: number }>> {
      const data = await call<{ prs: Array<{ owner: string; name: string; number: number; count: number }> }>(
        `${getBasePath()}api/v1/review-drafts/summary`,
        "GET",
      );
      return data.prs ?? [];
    },
  };
}

export type ReviewDraftsApi = ReturnType<typeof createReviewDraftsApi>;
```

The first test asserts `toHaveBeenCalledWith(url, expect.anything())`; `call` always passes an init object, so that holds for GET too.

- [ ] **Step 4: Run to verify pass**

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/reviewDraftsApi.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/stores/reviewDraftsApi.ts packages/ui/src/stores/reviewDraftsApi.test.ts
git commit -m "feat(ui): add review drafts API module

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Diff store persists drafts through the API

**Files:**
- Modify: `packages/ui/src/stores/diff.svelte.ts` (draft section: lines ~135-203 load/save helpers, ~316 state init, ~323 `setActivePR`, ~822 `loadDiff`, ~962 reset, ~1390-1527 draft functions, ~1786 exports)
- Test: `packages/ui/src/stores/diff.drafts.test.ts`

**Interfaces:**
- Consumes: Task 4 `createReviewDraftsApi`, `ServerDraft`, `PRRef`, `DraftApiError`.
- Produces (new exports on the diff store, in addition to the unchanged existing ones):

```ts
getDraftSyncError(): string | null
reloadDrafts(): Promise<void>           // re-fetch current PR drafts
publishDraft(req: { event: ReviewEvent; body: string; commitId?: string }): Promise<{ ok: true } | { ok: false; error: string }>
whenDraftsSettled(): Promise<void>      // resolves when no draft writes are in flight (tests, publish)
```

`DraftComment` gains `submitError?: string`. Existing function signatures stay identical and synchronous.

Behavior:
- `draftReviews` starts as `{}`; localStorage is no longer written.
- On `loadDiff` when the PR changes, and on `setActivePR` with a non-empty owner, call `loadDraftsFor(pr)`: first awaits the one-time legacy import, then `api.list(pr)`, guarded by a `draftsGen` counter so a late response for an old PR is dropped. Result replaces `draftReviews[key]`, keeping local comments whose id is still in `pendingCreates` (created locally, POST not answered yet).
- `addDraftComment`: as today (same UUID generation), then `pendingCreates.set(id, api.create(pr, [toWrite(c)]))`; on settle remove from `pendingCreates`; on error set `draftSyncError`. Local state is not rolled back.
- `removeDraftComment` / `clearDraft` / `clearDraftCommentsForPath`: update state, then for each affected id await `pendingCreates.get(id)` (ignoring its error) before `api.remove(pr, ids)`. This is Review Focus 1.
- `updateDraftCommentBody`: as today; empty body still routes to remove; otherwise await any pending create for that id, then `api.updateBody`.
- `setDraftBody` / `setDraftEvent`: update state, debounce `api.putMeta` by 400ms per PR (flush immediately on PR change and before `publishDraft`).
- `hasDraftForPR`: loaded PR -> its comments; otherwise a `draftSummary` map loaded lazily by the first call (one `api.summary()` per store, refreshed after `publishDraft` and `clearDraft`).
- Legacy import (`importLegacyDrafts`, once per store instance): read `diff-draft-reviews` with the existing `loadDraftReviews()` parser; for each `owner/name#number` key, `api.create` the comments (with `created_at`) and `api.putMeta` when body/event are non-default; delete the localStorage key only if every PR imported, otherwise keep it and set `draftSyncError` ("Some drafts from this browser could not be imported; they will retry on reload"). Server-side `INSERT OR IGNORE` makes the retry safe (Review Focus 2). Remove `saveDraftReviews`.
- `publishDraft`: flush meta, `await whenDraftsSettled()`, call `api.publish`; on success drop the key from `draftReviews` and refresh summary; on `DraftApiError` call `reloadDrafts()` (so posted drafts disappear and the failing one shows `submitError`) and return `{ ok: false, error: err.message }`.
- Map server -> local: `{ id, path, line, side, startLine: start_line, commitSha: commit_sha, body, createdAt: created_at, inReplyTo: in_reply_to, submitError: submit_error }`; ignore rows whose `state` is not `"draft"` (suggestions arrive in PR 2).

- [ ] **Step 1: Write failing tests**

`packages/ui/src/stores/diff.drafts.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import type { MiddlemanClient } from "../types.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

interface Call { url: string; method: string; body: unknown }

// In-memory fake of the review-drafts routes. `hold` lets a test keep
// a create request pending to exercise ordering.
function installDraftServer() {
  const calls: Call[] = [];
  const drafts = new Map<string, Record<string, unknown>[]>();
  let hold: Promise<void> | null = null;
  globalThis.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(init.body as string) : undefined;
    calls.push({ url, method, body });
    const m = url.match(/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/(review-drafts|review-draft-meta)(\/.*)?$/);
    const json = (b: unknown, status = 200) =>
      new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
    if (url.endsWith("/review-drafts/summary")) return json({ prs: [] });
    if (!m) return json({}, 404);
    const key = `${decodeURIComponent(m[1]!)}/${decodeURIComponent(m[2]!)}#${m[3]}`;
    const list = drafts.get(key) ?? [];
    if (m[4] === "review-drafts" && !m[5] && method === "GET") return json({ drafts: list, meta: { body: "", event: "" } });
    if (m[4] === "review-drafts" && !m[5] && method === "POST") {
      if (hold) await hold;
      const incoming = (body as { drafts: Record<string, unknown>[] }).drafts.map((d) => ({ ...d, origin: "user", state: "draft" }));
      const next = [...list];
      for (const d of incoming) if (!next.some((x) => x.id === d.id)) next.push(d);
      drafts.set(key, next);
      return json({ drafts: next, meta: { body: "", event: "" } });
    }
    if (m[5] === "/delete") {
      const ids = (body as { ids: string[] }).ids;
      drafts.set(key, list.filter((d) => !ids.includes(d.id as string)));
      return json({ deleted: ids.length });
    }
    return json({});
  }) as unknown as typeof fetch;
  return {
    calls,
    drafts,
    holdCreates(): () => void {
      let release!: () => void;
      hold = new Promise((r) => { release = r; });
      return () => { hold = null; release(); };
    },
  };
}

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

const draftInput = { path: "a.go", line: 1, side: "RIGHT" as const, commitSha: "abc", body: "hi" };

describe("diff store drafts persistence", () => {
  it("creates drafts on the server and reloads them", async () => {
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    const c = store.addDraftComment(draftInput);
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")?.map((d) => d.id)).toEqual([c.id]);

    const fresh = createDiffStore({ client: stubClient() });
    fresh.setActivePR("acme", "widget", 1);
    await fresh.reloadDrafts();
    expect(fresh.getDraft().comments.map((x) => x.id)).toEqual([c.id]);
  });

  it("remove waits for pending create", async () => {
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    const release = server.holdCreates();
    const c = store.addDraftComment(draftInput);
    store.removeDraftComment(c.id);
    expect(store.getDraft().comments).toHaveLength(0);
    release();
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1") ?? []).toHaveLength(0);
    const methods = server.calls.filter((x) => x.url.includes("review-drafts") && x.method === "POST").map((x) => x.url.endsWith("/delete"));
    expect(methods).toEqual([false, true]); // create, then delete
  });

  it("stale load is ignored", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [{ id: "old", path: "a.go", line: 1, side: "RIGHT", commit_sha: "s", body: "x", origin: "user", state: "draft", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    store.setActivePR("acme", "widget", 2);
    await store.whenDraftsSettled();
    await store.reloadDrafts();
    expect(store.getDraft().comments).toHaveLength(0);
  });

  it("import is idempotent and clears the key only on success", async () => {
    localStorage.setItem("diff-draft-reviews", JSON.stringify({
      "acme/widget#1": { body: "", event: "COMMENT", comments: [{ id: "legacy-1", path: "a.go", line: 2, side: "RIGHT", commitSha: "abc", body: "old", createdAt: "2026-01-01T00:00:00.000Z" }] },
    }));
    const server = installDraftServer();
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    await store.reloadDrafts();
    expect(store.getDraft().comments.map((c) => c.id)).toEqual(["legacy-1"]);
    expect(localStorage.getItem("diff-draft-reviews")).toBeNull();

    // A second import of the same payload (other tab) does not duplicate.
    localStorage.setItem("diff-draft-reviews", JSON.stringify({
      "acme/widget#1": { body: "", event: "COMMENT", comments: [{ id: "legacy-1", path: "a.go", line: 2, side: "RIGHT", commitSha: "abc", body: "old", createdAt: "2026-01-01T00:00:00.000Z" }] },
    }));
    const again = createDiffStore({ client: stubClient() });
    again.setActivePR("acme", "widget", 1);
    await again.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")).toHaveLength(1);
  });

  it("keeps the legacy key when the import fails", async () => {
    localStorage.setItem("diff-draft-reviews", JSON.stringify({
      "acme/widget#1": { body: "", event: "COMMENT", comments: [{ id: "legacy-1", path: "a.go", line: 2, side: "RIGHT", commitSha: "abc", body: "old", createdAt: "2026-01-01T00:00:00.000Z" }] },
    }));
    globalThis.fetch = vi.fn(async () => new Response("{}", { status: 500 })) as unknown as typeof fetch;
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(localStorage.getItem("diff-draft-reviews")).not.toBeNull();
    expect(store.getDraftSyncError()).not.toBeNull();
  });
});
```

`whenDraftsSettled` must also cover the in-flight import and load started by `setActivePR`; implement it as "await a set of tracked promises until the set is empty".

- [ ] **Step 2: Run to verify failure**

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/diff.drafts.test.ts`
Expected: FAIL (`whenDraftsSettled is not a function`).

- [ ] **Step 3: Implement in `diff.svelte.ts`**

Add near the top-level helpers:

```ts
import { createReviewDraftsApi, DraftApiError, type PRRef, type ServerDraft } from "./reviewDraftsApi.js";

function fromServerDraft(d: ServerDraft): DraftComment {
  return {
    id: d.id,
    path: d.path,
    line: d.line,
    side: d.side,
    ...(d.start_line != null ? { startLine: d.start_line } : {}),
    commitSha: d.commit_sha,
    body: d.body,
    createdAt: d.created_at,
    ...(d.in_reply_to != null ? { inReplyTo: d.in_reply_to } : {}),
    ...(d.submit_error ? { submitError: d.submit_error } : {}),
  };
}

function toDraftWrite(c: DraftComment) {
  return {
    id: c.id,
    path: c.path,
    side: c.side,
    line: c.line,
    ...(c.startLine != null ? { start_line: c.startLine } : {}),
    commit_sha: c.commitSha,
    ...(c.inReplyTo != null ? { in_reply_to: c.inReplyTo } : {}),
    body: c.body,
    created_at: c.createdAt,
  };
}

function parseDraftKey(key: string): PRRef | null {
  const m = key.match(/^(.+)\/(.+)#(\d+)$/);
  return m ? { owner: m[1]!, name: m[2]!, number: Number(m[3]) } : null;
}
```

Add `submitError?: string;` to `DraftComment`. Delete `saveDraftReviews`. Keep `loadDraftReviews` (now only the import reads it). Inside `createDiffStore`:

```ts
  const draftsApi = createReviewDraftsApi(getBasePath);
  let draftReviews = $state<Record<string, DraftReview>>({});
  let draftSyncError = $state<string | null>(null);
  let draftSummary = $state<Record<string, number> | null>(null);
  let draftsGen = 0;
  let importPromise: Promise<void> | null = null;
  const pendingCreates = new Map<string, Promise<unknown>>();
  const inflight = new Set<Promise<unknown>>();
  let metaTimer: ReturnType<typeof setTimeout> | null = null;
  let metaPendingFor: PRRef | null = null;

  function track<T>(p: Promise<T>): Promise<T> {
    inflight.add(p);
    void p.finally(() => inflight.delete(p)).catch(() => {});
    return p;
  }

  async function whenDraftsSettled(): Promise<void> {
    while (inflight.size > 0) {
      await Promise.allSettled([...inflight]);
    }
  }

  function currentPRRef(): PRRef | null {
    return currentOwner ? { owner: currentOwner, name: currentName, number: currentNumber } : null;
  }

  function syncFailed(err: unknown): void {
    draftSyncError = err instanceof Error ? err.message : String(err);
  }

  function importLegacyDrafts(): Promise<void> {
    if (importPromise) return importPromise;
    importPromise = track((async () => {
      const legacy = loadDraftReviews();
      const keys = Object.keys(legacy);
      if (keys.length === 0) return;
      let allOk = true;
      for (const key of keys) {
        const pr = parseDraftKey(key);
        const d = legacy[key]!;
        if (!pr) continue;
        try {
          if (d.comments.length > 0) await draftsApi.create(pr, d.comments.map(toDraftWrite));
          if (d.body || d.event !== "COMMENT") await draftsApi.putMeta(pr, d.body, d.event);
        } catch {
          allOk = false;
        }
      }
      if (allOk) {
        try { localStorage.removeItem("diff-draft-reviews"); } catch { /* storage unavailable */ }
      } else {
        draftSyncError = "Some drafts from this browser could not be imported; they will retry on reload";
      }
    })());
    return importPromise;
  }

  function loadDraftsFor(pr: PRRef): Promise<void> {
    const gen = ++draftsGen;
    const key = `${pr.owner}/${pr.name}#${pr.number}`;
    return track((async () => {
      await importLegacyDrafts();
      try {
        const res = await draftsApi.list(pr);
        if (gen !== draftsGen) return;
        const server = res.drafts.filter((d) => d.state === "draft").map(fromServerDraft);
        const local = draftReviews[key]?.comments ?? [];
        const pendingLocal = local.filter((c) => pendingCreates.has(c.id) && !server.some((s) => s.id === c.id));
        const event: ReviewEvent =
          res.meta.event === "APPROVE" || res.meta.event === "REQUEST_CHANGES" ? res.meta.event : "COMMENT";
        draftReviews = { ...draftReviews, [key]: { body: res.meta.body, event, comments: [...server, ...pendingLocal] } };
      } catch (err) {
        if (gen === draftsGen) syncFailed(err);
      }
    })());
  }

  function reloadDrafts(): Promise<void> {
    const pr = currentPRRef();
    return pr ? loadDraftsFor(pr) : Promise.resolve();
  }

  function flushMeta(): void {
    if (metaTimer) {
      clearTimeout(metaTimer);
      metaTimer = null;
    }
    const pr = metaPendingFor;
    metaPendingFor = null;
    if (!pr) return;
    const d = draftReviews[`${pr.owner}/${pr.name}#${pr.number}`] ?? emptyDraft();
    track(draftsApi.putMeta(pr, d.body, d.event).catch(syncFailed));
  }

  function scheduleMeta(): void {
    const pr = currentPRRef();
    if (!pr) return;
    if (metaPendingFor && draftKeyFor(metaPendingFor) !== draftKeyFor(pr)) flushMeta();
    metaPendingFor = pr;
    if (metaTimer) clearTimeout(metaTimer);
    metaTimer = setTimeout(flushMeta, 400);
  }

  function draftKeyFor(pr: PRRef): string {
    return `${pr.owner}/${pr.name}#${pr.number}`;
  }

  function removeOnServer(pr: PRRef, ids: string[]): void {
    if (ids.length === 0) return;
    track((async () => {
      await Promise.allSettled(ids.map((id) => pendingCreates.get(id) ?? Promise.resolve()));
      await draftsApi.remove(pr, ids);
    })().catch(syncFailed));
  }
```

Wire it in:

- `setActivePR(owner, name, number)`: after assigning, `if (owner) void loadDraftsFor({ owner, name, number });` and `flushMeta()` before assigning if the PR changes.
- `loadDiff`: inside `if (prChanged) { ... }`, add `flushMeta(); void loadDraftsFor({ owner, name, number });`.
- Reset block (~line 962): `flushMeta();` before clearing `currentOwner`.
- `setDraftBody` / `setDraftEvent`: replace `saveDraftReviews(draftReviews)` with `scheduleMeta()`.
- `addDraftComment`: replace `saveDraftReviews(draftReviews)` with:

```ts
    const pr = currentPRRef();
    if (pr) {
      const p = track(draftsApi.create(pr, [toDraftWrite(comment)]));
      pendingCreates.set(comment.id, p);
      void p.catch(syncFailed).finally(() => pendingCreates.delete(comment.id));
    }
```

- `removeDraftComment`: replace the save with `const pr = currentPRRef(); if (pr) removeOnServer(pr, [id]);`.
- `updateDraftCommentBody`: replace the save with:

```ts
    const pr = currentPRRef();
    if (pr) {
      track((async () => {
        await (pendingCreates.get(id) ?? Promise.resolve()).catch(() => {});
        await draftsApi.updateBody(pr, id, trimmed);
      })().catch(syncFailed));
    }
```

- `clearDraft`: capture `const ids = (draftReviews[key]?.comments ?? []).map((c) => c.id);` before deleting the key; after, `removeOnServer(pr, ids)`, then `metaPendingFor = null; if (pr) track(draftsApi.putMeta(pr, "", "").catch(syncFailed)); draftSummary = null;`.
- `clearDraftCommentsForPath`: capture removed ids, `removeOnServer(pr, removedIds)`.
- `hasDraftForPR`:

```ts
    const key = `${owner}/${name}#${number}`;
    const d = draftReviews[key];
    if (d) return d.comments.length > 0;
    if (draftSummary === null) {
      draftSummary = {};
      track(draftsApi.summary().then((prs) => {
        const next: Record<string, number> = {};
        for (const p of prs) next[`${p.owner}/${p.name}#${p.number}`] = p.count;
        draftSummary = next;
      }).catch(() => {}));
    }
    return (draftSummary[key] ?? 0) > 0;
```

- New `publishDraft`:

```ts
  async function publishDraft(req: { event: ReviewEvent; body: string; commitId?: string }): Promise<{ ok: true } | { ok: false; error: string }> {
    const pr = currentPRRef();
    if (!pr) return { ok: false, error: "No pull request selected" };
    flushMeta();
    await whenDraftsSettled();
    try {
      await draftsApi.publish(pr, { event: req.event, body: req.body, ...(req.commitId ? { commit_id: req.commitId } : {}) });
      const next = { ...draftReviews };
      delete next[draftKeyFor(pr)];
      draftReviews = next;
      draftSummary = null;
      return { ok: true };
    } catch (err) {
      await reloadDrafts();
      return { ok: false, error: err instanceof DraftApiError ? err.message : String(err) };
    }
  }
```

- Export `getDraftSyncError: () => draftSyncError`, `reloadDrafts`, `publishDraft`, `whenDraftsSettled` next to the existing draft exports (~line 1786).

Lint and typecheck may flag the unused `DraftApiError` import if `publishDraft` is written differently; keep the import only if used.

- [ ] **Step 4: Run the new and existing store tests**

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/`
Expected: PASS, including the existing `diff.test.ts` (`clearDraftCommentsForPath` test keeps working because state stays synchronous). Then run the component tests that touch drafts: `cd frontend && bun run test -- ../packages/ui/src/components/diff ../packages/ui/src/components/detail`. If a component test asserted on `localStorage` drafts, change it to assert on store state.

- [ ] **Step 5: Typecheck and lint**

Run: `make frontend-check`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add packages/ui/src/stores/diff.svelte.ts packages/ui/src/stores/diff.drafts.test.ts
git commit -m "feat(ui): persist review drafts on the server

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: ReviewPanel publishes through the drafts API

**Files:**
- Modify: `packages/ui/src/components/diff/ReviewPanel.svelte:76-182`
- Modify: `packages/ui/src/components/diff/PendingCommentCard.svelte` (show `submitError`)
- Test: `packages/ui/src/components/diff/ReviewPanel.test.ts`

**Interfaces:**
- Consumes: Task 5 `diffStore.publishDraft`, `DraftComment.submitError`.
- Produces: none for later tasks.

- [ ] **Step 1: Read the existing test file**

Run: `grep -n "it(\|POST\|review\"" packages/ui/src/components/diff/ReviewPanel.test.ts | head -40`. Note how it mounts the panel and stubs `client.POST` / the diff store.

- [ ] **Step 2: Update tests first**

Replace assertions that the GitHub path calls `client.POST("/repos/{owner}/{name}/pulls/{number}/review", ...)` with assertions that it calls `diffStore.publishDraft({ event, body, commitId })`. Add:

```ts
  it("shows the publish error and keeps the panel open", async () => {
    // Arrange the store stub so publishDraft resolves { ok: false, error: "GitHub couldn't anchor comment[0]" }
    // Act: click the submit button
    // Assert: the error text is rendered and onclose was not called
  });
```

Write it concretely in the file's existing style (same render helper, same store stub shape, `vi.fn().mockResolvedValue({ ok: false, error: "GitHub couldn't anchor comment[0]" })`, `await fireEvent.click(getByRole("button", { name: /submit|publish/i }))`, `expect(getByText(/couldn't anchor/)).toBeTruthy()`, `expect(onclose).not.toHaveBeenCalled()`). Keep the local-path tests unchanged: local still calls `reviewThreadsStore.createThreads` then `clearDraft` / `clearDraftCommentsForPath`, which now delete on the server via Task 5.

Run: `cd frontend && bun run test -- ../packages/ui/src/components/diff/ReviewPanel.test.ts`
Expected: FAIL (panel still posts to `/review`).

- [ ] **Step 3: Change the GitHub branch of `onSubmit`**

Replace lines ~126-181 (from `const commits = diffStore.getCommits();` through the end of the `try/catch/finally`) with:

```ts
    const commits = diffStore.getCommits();
    const headSha = commits && commits.length > 0 ? commits[0]!.sha : "";

    try {
      const result = await diffStore.publishDraft({
        event: draft.event,
        body: draft.body || "",
        ...(headSha ? { commitId: headSha } : {}),
      });
      if (!result.ok) {
        errorMsg = result.error;
        return;
      }
      // Publish writes the review event to the DB before responding,
      // so a refetch now flips the sidebar chip without waiting for
      // the next sync.
      void pullsStore.loadPulls();
      onclose();
    } catch (err) {
      errorMsg = err instanceof Error ? err.message : String(err);
    } finally {
      submitting = false;
    }
```

Remove the now-unused `client` import/const if nothing else in the file uses it (check with `grep -n "client\." ReviewPanel.svelte`). Update the comment above `onSubmit` to say comments are read from the server-side drafts.

- [ ] **Step 4: Show per-draft publish errors**

In `PendingCommentCard.svelte`, under the rendered body, add (reusing an existing error style class from the file or from `ReviewPanel.svelte`; check with `grep -n "error" PendingCommentCard.svelte ReviewPanel.svelte`):

```svelte
{#if comment.submitError}
  <p class="draft-submit-error" role="alert">{comment.submitError}</p>
{/if}
```

Add a matching style using existing design tokens from `frontend/src/app.css` (for example the token `ReviewPanel` uses for its error text).

- [ ] **Step 5: Run tests, typecheck, lint**

Run: `cd frontend && bun run test -- ../packages/ui/src/components/diff` then `make frontend-check`
Expected: PASS, no errors.

- [ ] **Step 6: Commit**

```bash
git add packages/ui/src/components/diff/ReviewPanel.svelte packages/ui/src/components/diff/ReviewPanel.test.ts packages/ui/src/components/diff/PendingCommentCard.svelte
git commit -m "feat(ui): publish reviews from server-side drafts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Full verification

**Files:** none new.

- [ ] **Step 1: Go**

Run: `make test-short` then `make vet` then `make lint`
Expected: all pass. `make lint` auto-fixes; if it changed files, review and commit them.

- [ ] **Step 2: Frontend**

Run: `cd frontend && bun run test` then `make frontend-check`
Expected: all pass.

- [ ] **Step 3: Build**

Run: `make build`
Expected: binary builds with embedded frontend.

- [ ] **Step 4: Manual smoke (optional, needs a configured instance)**

Run `make dev` and `make frontend-dev`; open a PR diff, add a draft, reload the page (draft persists), open in a second browser (draft visible), publish against a test PR. Not required for completion; say whether it was done.

- [ ] **Step 5: Commit lint fixes if any**

```bash
git add -u
git commit -m "style: lint fixes for server-side review drafts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

Only if Step 1 or 2 produced changes. `git add -u` stages tracked files only, so the pre-existing untracked `.impeccable/` and `frontend/PRODUCT.md` stay out.
