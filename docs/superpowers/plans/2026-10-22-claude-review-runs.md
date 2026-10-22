# Claude Review Runs Implementation Plan (PR 2 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the reviewer start a "Review with Claude" run on a GitHub PR with optional instructions; Claude stages findings as suggestions (draft rows with `origin='claude', state='suggestion'`) through an MCP tool; the reviewer sees them in the diff and can add them to drafts, edit, or discard them.

**Architecture:** Migration 000028 adds `middleman_ai_review_runs` and `middleman_review_draft_messages` (the latter holds Claude's rationale now and the per-suggestion chat in PR 3). `aireview.Runner` gains `StartReviewRun`, which provisions a detached worktree at a fixed path `<ai-review>/run-<id>`, runs `claude -p` with stream-json and the middleman MCP server in a new `--run <id>` mode, records the stream for the UI, and stores the Claude session id (the root for PR 3's forks). The MCP `propose_comment` tool posts to a server route that validates the anchor against the PR diff and inserts a suggestion. Frontend: the diff store keeps suggestions next to drafts, a new `aiReviewRun` store polls the latest run, and new UI shows a toolbar entry, a sidebar section, and suggestion cards.

**Tech Stack:** Go, Huma v2, modernc.org/sqlite, testify; Svelte 5 runes, TypeScript, Vitest (run from `frontend/`), Bun.

**Spec:** `docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md` (Delivery PR 2). Builds on PR 1 (`docs/superpowers/plans/2026-10-22-server-side-review-drafts.md`), already committed.

## Global Constraints

- Datetimes UTC in storage; API emits UTC RFC3339.
- No CGO; no new Go or JS dependencies. Bun only, never npm.
- Go tests: testify only, `require` for preconditions; no `t.Fatal`/`t.Error*`; >3 assertions -> `assert := Assert.New(t)` (`Assert` alias in `internal/server`, `assert.New` elsewhere); table-driven where there are cases; `-shuffle=on`; never `-count=1`; never `-v`.
- E2E tests (HTTP + real SQLite + generated client) for every API behavior.
- Migrations: new numbered files only, honest down migrations.
- `make api-generate` after route changes; commit regenerated files. Generated Go client uses pointer slices (`*[]T`).
- Claude never publishes. Allowed tools for review runs: `Read,Glob,Grep` + `mcp__middleman__propose_comment,mcp__middleman__list_drafts,mcp__middleman__get_pull`; disallowed `Edit,Write,NotebookEdit,Bash,Agent`.
- Review run timeout 15 minutes; process group killed on timeout and cancel.
- User-visible terms: "Suggestion", "Add to drafts", "Draft", "Review with Claude". No emojis.
- Conventional commits ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never amend, never `--no-verify`, never change branches (detached HEAD stays).

## Spec deviations (decided while planning)

- `middleman_review_draft_messages` is created in this PR's migration (000028) instead of PR 3, because the spec stores Claude's rationale as a suggestion's first chat message. PR 3 adds the chat routes and runner on top.
- The fake `claude` in tests is a shell script (the existing pattern in `internal/aireview/*_test.go`), not a Go re-exec binary that speaks MCP. MCP behavior is covered by `internal/mcp` tool tests against a stub HTTP doer, and the REST route the tool calls is covered by e2e tests. Together they cover the same path without a test binary that has to spawn `middleman mcp`.
- Suggestions render in the diff view (`DiffFile`) only; rendered-markdown doc panes (`CommentGutter`) keep showing drafts only. A suggestion on a `.md` file is still visible in the diff view and in the sidebar list.
- Auto-cancel of runs on PR close is deferred to PR 4's lifecycle work; restart reconciliation is in this PR.
- Discard undo is an inline "Undo" button in the Suggestions sidebar section (no toast system exists).

## Review Focus

1. Claude proposes a line outside the diff -> rejected with an error that lists the valid ranges so Claude can retry. (Task 4 `TestAPIReviewRunProposals_AnchorValidation`)
2. A proposal arrives after the run finished or was cancelled -> rejected, nothing stored. (Task 4 `TestAPIReviewRunProposals_RejectsInactiveRun`)
3. Second "Review with Claude" while one is active -> 409, existing run untouched. (Task 1 `TestCreateAIReviewRunRejectsSecondActive`, Task 4 `TestAPIReviewRunLifecycle`)
4. A run that crashes after proposing keeps its suggestions and reports the count. (Task 1 `TestReviewRunFailureKeepsSuggestions`)
5. Editing a suggestion's body never changes its state; accept/discard/restore reject invalid transitions. (Task 1 `TestReviewDraftTransitions`, Task 5 store tests)

---

### Task 1: Migration 000028 and DB layer for runs, suggestions, messages

**Files:**
- Create: `internal/db/migrations/000028_add_ai_review_runs.up.sql`, `.down.sql`
- Create: `internal/db/queries_ai_review_runs.go`, `internal/db/queries_ai_review_runs_test.go`
- Modify: `internal/db/queries_review_drafts.go` (RunID field, suggestion insert, transitions, rationales)
- Modify: `internal/db/queries_review_drafts_test.go`
- Modify: `internal/db/db_test.go` (schema table list)

**Interfaces (produces):**

```go
var ErrReviewRunActive = errors.New("a review run is already active for this pull request")
var ErrInvalidDraftTransition = errors.New("invalid draft state transition")

type AIReviewRun struct {
	ID              int64
	MergeRequestID  int64
	Branch          string
	HeadSHA         string
	ReadSHA         string
	Instructions    string
	ClaudeSessionID string
	WorktreePath    string
	Status          string // queued|running|done|failed|cancelled
	Summary         string
	RawJSON         string
	Error           string
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

type NewAIReviewRun struct {
	MergeRequestID int64
	Branch         string
	HeadSHA        string
	ReadSHA        string
	Instructions   string
}

func (d *DB) CreateAIReviewRun(ctx context.Context, in NewAIReviewRun) (AIReviewRun, error) // ErrReviewRunActive if queued/running exists for (mr, branch)
func (d *DB) GetAIReviewRun(ctx context.Context, id int64) (*AIReviewRun, error)          // nil, nil if missing
func (d *DB) GetLatestAIReviewRun(ctx context.Context, mrID int64, branch string) (*AIReviewRun, error)
func (d *DB) MarkAIReviewRunRunning(ctx context.Context, id int64, pid int, worktreePath string) error
func (d *DB) UpdateAIReviewRunStream(ctx context.Context, id int64, rawJSON string) error
func (d *DB) MarkAIReviewRunDone(ctx context.Context, id int64, sessionID, summary, rawJSON string) error
func (d *DB) MarkAIReviewRunFailed(ctx context.Context, id int64, msg, rawJSON string) error // only from queued/running
func (d *DB) MarkAIReviewRunCancelled(ctx context.Context, id int64) error                   // only from queued/running
func (d *DB) ListActiveAIReviewRuns(ctx context.Context) ([]AIReviewRun, error)
func (d *DB) CountRunSuggestions(ctx context.Context, runID int64) (int, error) // claude-origin drafts of the run, any state

// queries_review_drafts.go additions
// ReviewDraft gains: RunID *int64
func (d *DB) InsertClaudeSuggestion(ctx context.Context, mrID int64, branch string, runID int64, in NewReviewDraft) (ReviewDraft, error)
func (d *DB) AcceptReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error)  // suggestion -> draft
func (d *DB) DiscardReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) // suggestion|draft -> discarded
func (d *DB) RestoreReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) // discarded -> discarded_from
func (d *DB) InsertReviewDraftMessage(ctx context.Context, in NewReviewDraftMessage) (ReviewDraftMessage, error)
func (d *DB) ListDraftRationales(ctx context.Context, mrID int64, branch string) (map[string]string, error) // draft id -> first claude message body

type ReviewDraftMessage struct {
	ID          int64
	DraftID     string
	Author      string // user|claude
	Kind        string // message|revision
	Body        string
	PrevBody    string
	Applied     bool
	Status      string // queued|running|done|failed|cancelled
	RawJSON     string
	Error       string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}
type NewReviewDraftMessage struct {
	DraftID string
	Author  string
	Kind    string
	Body    string
	Status  string
}
```

The three transition functions return `nil, nil` when the draft does not exist on that PR/branch and `nil, ErrInvalidDraftTransition` when it exists in the wrong state. `GetReviewDraft` keeps returning any state; `ListReviewDrafts` keeps excluding `discarded`.

- [ ] **Step 1: Migration**

`000028_add_ai_review_runs.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS middleman_ai_review_runs (
    id                INTEGER  PRIMARY KEY AUTOINCREMENT,
    merge_request_id  INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch            TEXT     NOT NULL DEFAULT '',
    head_sha          TEXT     NOT NULL,
    read_sha          TEXT     NOT NULL,
    instructions      TEXT     NOT NULL DEFAULT '',
    claude_session_id TEXT     NOT NULL DEFAULT '',
    worktree_path     TEXT     NOT NULL DEFAULT '',
    status            TEXT     NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    summary           TEXT     NOT NULL DEFAULT '',
    raw_json          TEXT     NOT NULL DEFAULT '',
    error             TEXT     NOT NULL DEFAULT '',
    pid               INTEGER,
    created_at        DATETIME NOT NULL,
    started_at        DATETIME,
    completed_at      DATETIME
);

CREATE INDEX IF NOT EXISTS idx_ai_review_runs_mr_branch
    ON middleman_ai_review_runs (merge_request_id, branch, id);

CREATE TABLE IF NOT EXISTS middleman_review_draft_messages (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    draft_id     TEXT     NOT NULL REFERENCES middleman_review_drafts(id) ON DELETE CASCADE,
    author       TEXT     NOT NULL CHECK (author IN ('user', 'claude')),
    kind         TEXT     NOT NULL CHECK (kind IN ('message', 'revision')),
    body         TEXT     NOT NULL DEFAULT '',
    prev_body    TEXT     NOT NULL DEFAULT '',
    applied      BOOLEAN  NOT NULL DEFAULT 0,
    status       TEXT     NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    raw_json     TEXT     NOT NULL DEFAULT '',
    error        TEXT     NOT NULL DEFAULT '',
    pid          INTEGER,
    created_at   DATETIME NOT NULL,
    started_at   DATETIME,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_review_draft_messages_draft
    ON middleman_review_draft_messages (draft_id, id);
```

`.down.sql`:

```sql
DROP INDEX IF EXISTS idx_review_draft_messages_draft;
DROP TABLE IF EXISTS middleman_review_draft_messages;
DROP INDEX IF EXISTS idx_ai_review_runs_mr_branch;
DROP TABLE IF EXISTS middleman_ai_review_runs;
```

Add both tables to `TestOpenAndSchema`.

- [ ] **Step 2: Failing tests**

`internal/db/queries_ai_review_runs_test.go`:

```go
package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRun(t *testing.T, d *DB, mrID int64) AIReviewRun {
	t.Helper()
	run, err := d.CreateAIReviewRun(context.Background(), NewAIReviewRun{
		MergeRequestID: mrID, HeadSHA: "head1", ReadSHA: "head1", Instructions: "focus on errors",
	})
	require.NoError(t, err)
	return run
}

func TestAIReviewRunLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	run := newRun(t, d, mrID)
	assert.Equal("queued", run.Status)
	assert.Equal("focus on errors", run.Instructions)

	require.NoError(d.MarkAIReviewRunRunning(ctx, run.ID, 4242, "/tmp/run-1"))
	require.NoError(d.UpdateAIReviewRunStream(ctx, run.ID, `{"events":[]}`))
	require.NoError(d.MarkAIReviewRunDone(ctx, run.ID, "sess-1", "two findings", `{"events":[1]}`))

	got, err := d.GetLatestAIReviewRun(ctx, mrID, "")
	require.NoError(err)
	require.NotNil(got)
	assert.Equal("done", got.Status)
	assert.Equal("sess-1", got.ClaudeSessionID)
	assert.Equal("two findings", got.Summary)
	assert.Equal("/tmp/run-1", got.WorktreePath)
	require.NotNil(got.CompletedAt)

	// Terminal runs cannot be failed or cancelled afterwards.
	require.NoError(d.MarkAIReviewRunFailed(ctx, run.ID, "late", ""))
	require.NoError(d.MarkAIReviewRunCancelled(ctx, run.ID))
	got, err = d.GetAIReviewRun(ctx, run.ID)
	require.NoError(err)
	assert.Equal("done", got.Status)
}

func TestCreateAIReviewRunRejectsSecondActive(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	first := newRun(t, d, mrID)
	_, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.ErrorIs(err, ErrReviewRunActive)

	// A different branch of the same MR (local) is independent.
	_, err = d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, Branch: "feat/b", HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)

	require.NoError(d.MarkAIReviewRunCancelled(ctx, first.ID))
	_, err = d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)
}

func TestReviewRunFailureKeepsSuggestions(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run := newRun(t, d, mrID)

	_, err := d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: "s-1", Path: "a.go", Side: "RIGHT", Line: 3, CommitSHA: "head1", Body: "nil deref"})
	require.NoError(err)
	require.NoError(d.MarkAIReviewRunFailed(ctx, run.ID, "claude exited: 1", ""))

	n, err := d.CountRunSuggestions(ctx, run.ID)
	require.NoError(err)
	assert.Equal(1, n)
	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
	assert.Equal("suggestion", list[0].State)
	assert.Equal("claude", list[0].Origin)
	require.NotNil(list[0].RunID)
	assert.Equal(run.ID, *list[0].RunID)

	active, err := d.ListActiveAIReviewRuns(ctx)
	require.NoError(err)
	assert.Empty(active)
}
```

Append to `internal/db/queries_review_drafts_test.go`:

```go
func TestReviewDraftTransitions(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(t, err)

	type step struct {
		op      string // accept|discard|restore|edit
		want    string // resulting state, "" when the op must fail
		wantErr error
	}
	cases := []struct {
		name  string
		start string // suggestion|draft
		steps []step
	}{
		{"accept suggestion", "suggestion", []step{{op: "accept", want: "draft"}}},
		{"accept twice fails", "suggestion", []step{{op: "accept", want: "draft"}, {op: "accept", wantErr: ErrInvalidDraftTransition}}},
		{"discard and restore suggestion", "suggestion", []step{{op: "discard", want: "discarded"}, {op: "restore", want: "suggestion"}}},
		{"discard and restore draft", "draft", []step{{op: "discard", want: "discarded"}, {op: "restore", want: "draft"}}},
		{"restore live draft fails", "draft", []step{{op: "restore", wantErr: ErrInvalidDraftTransition}}},
		{"accept discarded fails", "suggestion", []step{{op: "discard", want: "discarded"}, {op: "accept", wantErr: ErrInvalidDraftTransition}}},
		{"edit keeps suggestion state", "suggestion", []step{{op: "edit", want: "suggestion"}}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("t-%d", i)
			nd := NewReviewDraft{ID: id, Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "b"}
			if tc.start == "suggestion" {
				_, err := d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, nd)
				require.NoError(t, err)
			} else {
				_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{nd})
				require.NoError(t, err)
			}
			for _, s := range tc.steps {
				var got *ReviewDraft
				var err error
				switch s.op {
				case "accept":
					got, err = d.AcceptReviewDraft(ctx, mrID, "", id)
				case "discard":
					got, err = d.DiscardReviewDraft(ctx, mrID, "", id)
				case "restore":
					got, err = d.RestoreReviewDraft(ctx, mrID, "", id)
				case "edit":
					got, err = d.UpdateReviewDraftBody(ctx, mrID, "", id, "edited")
				}
				if s.wantErr != nil {
					require.ErrorIs(t, err, s.wantErr)
					continue
				}
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, s.want, got.State)
			}
		})
	}

	missing, err := d.AcceptReviewDraft(ctx, mrID, "", "nope")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestDraftRationales(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)
	_, err = d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: "r-1", Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "b"})
	require.NoError(err)
	_, err = d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "r-1", Author: "claude", Kind: "message", Body: "because X", Status: "done"})
	require.NoError(err)
	_, err = d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "r-1", Author: "claude", Kind: "message", Body: "later reply", Status: "done"})
	require.NoError(err)

	got, err := d.ListDraftRationales(ctx, mrID, "")
	require.NoError(err)
	require.Equal(map[string]string{"r-1": "because X"}, got)
}
```

Add `"fmt"` to that test file's imports.

Run: `go test ./internal/db -run 'TestAIReviewRun|TestCreateAIReviewRun|TestReviewRunFailure|TestReviewDraftTransitions|TestDraftRationales|TestOpenAndSchema' -shuffle=on`
Expected: build failure (undefined names).

- [ ] **Step 3: Implement `internal/db/queries_ai_review_runs.go`**

```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrReviewRunActive = errors.New("a review run is already active for this pull request")

// AIReviewRun is one "Review with Claude" invocation. ClaudeSessionID
// is the root session every per-suggestion chat forks from.
type AIReviewRun struct {
	ID              int64
	MergeRequestID  int64
	Branch          string
	HeadSHA         string
	ReadSHA         string
	Instructions    string
	ClaudeSessionID string
	WorktreePath    string
	Status          string
	Summary         string
	RawJSON         string
	Error           string
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

type NewAIReviewRun struct {
	MergeRequestID int64
	Branch         string
	HeadSHA        string
	ReadSHA        string
	Instructions   string
}

const aiReviewRunColumns = `id, merge_request_id, branch, head_sha, read_sha, instructions,
	claude_session_id, worktree_path, status, summary, raw_json, error,
	created_at, started_at, completed_at`

func scanAIReviewRun(sc interface{ Scan(...any) error }) (AIReviewRun, error) {
	var (
		r         AIReviewRun
		started   sql.NullTime
		completed sql.NullTime
	)
	err := sc.Scan(&r.ID, &r.MergeRequestID, &r.Branch, &r.HeadSHA, &r.ReadSHA, &r.Instructions,
		&r.ClaudeSessionID, &r.WorktreePath, &r.Status, &r.Summary, &r.RawJSON, &r.Error,
		&r.CreatedAt, &started, &completed)
	if err != nil {
		return r, err
	}
	r.CreatedAt = r.CreatedAt.UTC()
	if started.Valid {
		v := started.Time.UTC()
		r.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time.UTC()
		r.CompletedAt = &v
	}
	return r, nil
}

// CreateAIReviewRun inserts a queued run. At most one queued/running
// run may exist per (merge request, branch); the check and insert share
// a transaction so two concurrent requests cannot both start one.
func (d *DB) CreateAIReviewRun(ctx context.Context, in NewAIReviewRun) (AIReviewRun, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return AIReviewRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_ai_review_runs
		  WHERE merge_request_id = ? AND branch = ? AND status IN ('queued', 'running')`,
		in.MergeRequestID, in.Branch,
	).Scan(&active); err != nil {
		return AIReviewRun{}, err
	}
	if active > 0 {
		return AIReviewRun{}, ErrReviewRunActive
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_ai_review_runs
		     (merge_request_id, branch, head_sha, read_sha, instructions, status, created_at)
		 VALUES (?, ?, ?, ?, ?, 'queued', ?)`,
		in.MergeRequestID, in.Branch, in.HeadSHA, in.ReadSHA, in.Instructions, time.Now().UTC(),
	)
	if err != nil {
		return AIReviewRun{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AIReviewRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return AIReviewRun{}, err
	}
	r, err := d.GetAIReviewRun(ctx, id)
	if err != nil {
		return AIReviewRun{}, err
	}
	return *r, nil
}

func (d *DB) GetAIReviewRun(ctx context.Context, id int64) (*AIReviewRun, error) {
	r, err := scanAIReviewRun(d.rw.QueryRowContext(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (d *DB) GetLatestAIReviewRun(ctx context.Context, mrID int64, branch string) (*AIReviewRun, error) {
	r, err := scanAIReviewRun(d.rw.QueryRowContext(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs
		  WHERE merge_request_id = ? AND branch = ? ORDER BY id DESC LIMIT 1`, mrID, branch))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (d *DB) MarkAIReviewRunRunning(ctx context.Context, id int64, pid int, worktreePath string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs
		    SET status = 'running', pid = ?, worktree_path = ?, started_at = ?
		  WHERE id = ? AND status = 'queued'`,
		pid, worktreePath, time.Now().UTC(), id)
	return err
}

func (d *DB) UpdateAIReviewRunStream(ctx context.Context, id int64, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs SET raw_json = ? WHERE id = ?`, rawJSON, id)
	return err
}

func (d *DB) MarkAIReviewRunDone(ctx context.Context, id int64, sessionID, summary, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs
		    SET status = 'done', claude_session_id = ?, summary = ?, raw_json = ?,
		        pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`,
		sessionID, summary, rawJSON, time.Now().UTC(), id)
	return err
}

// MarkAIReviewRunFailed records a failure. An empty rawJSON keeps the
// stream already stored.
func (d *DB) MarkAIReviewRunFailed(ctx context.Context, id int64, msg, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs
		    SET status = 'failed', error = ?,
		        raw_json = CASE WHEN ? = '' THEN raw_json ELSE ? END,
		        pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`,
		msg, rawJSON, rawJSON, time.Now().UTC(), id)
	return err
}

func (d *DB) MarkAIReviewRunCancelled(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs
		    SET status = 'cancelled', pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`,
		time.Now().UTC(), id)
	return err
}

func (d *DB) ListActiveAIReviewRuns(ctx context.Context) ([]AIReviewRun, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs
		  WHERE status IN ('queued', 'running') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIReviewRun{}
	for rows.Next() {
		r, err := scanAIReviewRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) CountRunSuggestions(ctx context.Context, runID int64) (int, error) {
	var n int
	err := d.rw.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_drafts WHERE run_id = ? AND origin = 'claude'`,
		runID).Scan(&n)
	return n, err
}
```

- [ ] **Step 4: Extend `queries_review_drafts.go`**

- Add `RunID *int64` to `ReviewDraft` (after `State`), append `run_id` to `reviewDraftColumns` (after `body_edited_at`), scan it into a `sql.NullInt64` in `scanReviewDraft` and set `r.RunID` when valid.
- Add:

```go
var ErrInvalidDraftTransition = errors.New("invalid draft state transition")

// InsertClaudeSuggestion stores one finding from a review run as a
// suggestion. Suggestions are never published until the reviewer adds
// them to drafts.
func (d *DB) InsertClaudeSuggestion(
	ctx context.Context, mrID int64, branch string, runID int64, in NewReviewDraft,
) (ReviewDraft, error) {
	now := time.Now().UTC()
	if _, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_review_drafts
		     (id, merge_request_id, branch, path, side, line, start_line,
		      commit_sha, in_reply_to, body, origin, state, run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, 'claude', 'suggestion', ?, ?, ?)`,
		in.ID, mrID, branch, in.Path, in.Side, in.Line, in.StartLine,
		in.CommitSHA, in.Body, runID, now, now,
	); err != nil {
		return ReviewDraft{}, err
	}
	r, err := d.GetReviewDraft(ctx, mrID, branch, in.ID)
	if err != nil {
		return ReviewDraft{}, err
	}
	return *r, nil
}

// transitionReviewDraft moves a draft between states with a
// compare-and-set on the current state. Returns nil, nil when the
// draft does not exist on this PR/branch.
func (d *DB) transitionReviewDraft(
	ctx context.Context, mrID int64, branch, id, setSQL string, fromStates []string,
) (*ReviewDraft, error) {
	cur, err := d.GetReviewDraft(ctx, mrID, branch, id)
	if err != nil || cur == nil {
		return nil, err
	}
	allowed := false
	for _, s := range fromStates {
		if cur.State == s {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrInvalidDraftTransition
	}
	res, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts SET `+setSQL+`, updated_at = ?
		  WHERE merge_request_id = ? AND branch = ? AND id = ? AND state = ?`,
		time.Now().UTC(), mrID, branch, id, cur.State,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrInvalidDraftTransition // raced with another transition
	}
	return d.GetReviewDraft(ctx, mrID, branch, id)
}

func (d *DB) AcceptReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id, `state = 'draft'`, []string{"suggestion"})
}

func (d *DB) DiscardReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id,
		`discarded_from = state, state = 'discarded'`, []string{"suggestion", "draft"})
}

func (d *DB) RestoreReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id,
		`state = CASE discarded_from WHEN 'draft' THEN 'draft' ELSE 'suggestion' END, discarded_from = ''`,
		[]string{"discarded"})
}
```

The `SET` fragments are constants inside this file, never user input.

- Messages (same file or new `queries_review_draft_messages.go`; put in a new file to keep drafts focused):

```go
type ReviewDraftMessage struct { /* fields as in Interfaces */ }
type NewReviewDraftMessage struct { DraftID, Author, Kind, Body, Status string }

func (d *DB) InsertReviewDraftMessage(ctx context.Context, in NewReviewDraftMessage) (ReviewDraftMessage, error) {
	now := time.Now().UTC()
	res, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_review_draft_messages (draft_id, author, kind, body, status, created_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, CASE WHEN ? = 'done' THEN ? ELSE NULL END)`,
		in.DraftID, in.Author, in.Kind, in.Body, in.Status, now, in.Status, now)
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	return ReviewDraftMessage{ID: id, DraftID: in.DraftID, Author: in.Author, Kind: in.Kind,
		Body: in.Body, Status: in.Status, CreatedAt: now}, nil
}

// ListDraftRationales returns, per draft on this PR/branch, the body of
// Claude's first message: the rationale it gave when proposing it.
func (d *DB) ListDraftRationales(ctx context.Context, mrID int64, branch string) (map[string]string, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT m.draft_id, m.body
		   FROM middleman_review_draft_messages m
		   JOIN middleman_review_drafts rd ON rd.id = m.draft_id
		  WHERE rd.merge_request_id = ? AND rd.branch = ?
		    AND m.author = 'claude' AND m.kind = 'message'
		    AND m.id = (SELECT MIN(m2.id) FROM middleman_review_draft_messages m2
		                 WHERE m2.draft_id = m.draft_id AND m2.author = 'claude' AND m2.kind = 'message')`,
		mrID, branch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		out[id] = body
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/db -shuffle=on`
Expected: PASS.

```bash
git add internal/db
git commit -m "feat(db): add AI review runs, suggestion transitions, and draft messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Review run runner

**Files:**
- Create: `internal/aireview/stream.go` (moved stream types + pure parser)
- Modify: `internal/aireview/sessions.go` (use `applyStreamMessage`; `MCPConfig.RunID`; `writeMCPConfig` adds `--run`)
- Modify: `internal/aireview/runner.go` (fields, `provisionWorktreeAt`, reconcile)
- Create: `internal/aireview/reviewruns.go`, `internal/aireview/reviewruns_test.go`

**Interfaces:**
- Consumes: Task 1 run functions.
- Produces:

```go
// stream.go
func applyStreamMessage(state *streamState, line []byte) (text string, isResult bool, dirty bool)

// sessions.go
type MCPConfig struct {
	Binary  string
	BaseURL string
	Owner   string
	Name    string
	Number  int
	RunID   int64 // >0 selects the review-run tool set (`middleman mcp --run`)
}

// reviewruns.go
const reviewRunToolNames = "mcp__middleman__propose_comment,mcp__middleman__list_drafts,mcp__middleman__get_pull"

type ReviewRunInput struct {
	RunID         int64
	Owner         string
	Name          string
	Number        int
	HeadSHA       string // checked out for Claude to read
	MergeBaseSHA  string
	Instructions  string
	PromptContext string
	MCP           MCPConfig
}

func ReviewRunWorktreePath(root string, runID int64) string // filepath.Join(root, fmt.Sprintf("run-%d", runID))
func (r *Runner) StartReviewRun(ctx context.Context, in ReviewRunInput) error
func (r *Runner) CancelReviewRun(ctx context.Context, runID int64) error
```

`RunnerConfig` gains `ReviewRunTimeout time.Duration` (zero -> 15 minutes). `Runner` gains `reviewRunsRunning map[int64]context.CancelFunc` and `reviewRunTimeout time.Duration`. `ReconcileOnStartup` also marks every `ListActiveAIReviewRuns` row failed with "interrupted by middleman restart".

- [ ] **Step 1: Refactor stream parsing (no behavior change)**

Move `streamEvent`, `streamState`, `streamMessage`, `streamInnerMessage`, `streamContentBlock`, and `flattenToolResultContent` from `sessions.go` to `stream.go` unchanged. Add:

```go
// applyStreamMessage folds one stream-json line into state. It returns
// the final summary text and isResult=true for the terminating result
// line, and dirty=true whenever state gained something worth flushing.
// Unparseable lines (CLI warnings) are ignored.
func applyStreamMessage(state *streamState, line []byte) (string, bool, bool) {
	var msg streamMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return "", false, false
	}
	dirty := false
	switch msg.Type {
	case "system":
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
			dirty = true
		}
	case "assistant":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					state.Events = append(state.Events, streamEvent{Type: "text", Text: b.Text})
					dirty = true
				}
			case "tool_use":
				state.Events = append(state.Events, streamEvent{Type: "tool_use", Tool: b.Name, Input: b.Input, ID: b.ID})
				dirty = true
			}
		}
	case "user":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			if b.Type != "tool_result" {
				continue
			}
			state.Events = append(state.Events, streamEvent{
				Type: "tool_result", ToolUseID: b.ToolUseID,
				Content: flattenToolResultContent(b.Content), IsError: b.IsError,
			})
			dirty = true
		}
	case "result":
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
		}
		return msg.Result, true, true
	}
	return "", false, dirty
}
```

Rewrite `SessionRunner.applyStreamLine` as:

```go
func (r *SessionRunner) applyStreamLine(
	ctx context.Context, turnID int64, state *streamState, line []byte,
) (string, bool) {
	text, isResult, dirty := applyStreamMessage(state, line)
	if dirty {
		r.flushStreamState(ctx, turnID, state)
	}
	return text, isResult
}
```

Run: `go test ./internal/aireview -shuffle=on`
Expected: PASS (pure refactor). Commit: `refactor(aireview): extract stream-json parsing for reuse`.

- [ ] **Step 2: `--run` in MCP config — failing test**

Append to `internal/aireview/sessions_discuss_test.go` (or a new `mcpconfig_test.go`):

```go
func TestWriteMCPConfigRunMode(t *testing.T) {
	require := require.New(t)
	path, cleanup, err := writeMCPConfig(MCPConfig{Binary: "/bin/middleman", BaseURL: "http://x", Owner: "acme", Name: "widgets", Number: 1, RunID: 7})
	require.NoError(err)
	t.Cleanup(cleanup)
	raw, err := os.ReadFile(path)
	require.NoError(err)
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(json.Unmarshal(raw, &cfg))
	args := cfg.MCPServers["middleman"].Args
	require.Contains(strings.Join(args, " "), "--run 7")
}
```

Run: `go test ./internal/aireview -run TestWriteMCPConfigRunMode -shuffle=on` -> FAIL (no RunID field). Add `RunID int64` to `MCPConfig`; in `writeMCPConfig` after building `args`, `if c.RunID > 0 { args = append(args, "--run", strconv.FormatInt(c.RunID, 10)) }` (restructure the literal so `args` is a local slice). Run -> PASS.

- [ ] **Step 3: Runner tests (failing)**

`internal/aireview/reviewruns_test.go`. Build a bare clone fixture the same way existing runner tests build one for briefs: read `grep -n "func setupBriefClone\|func newTestRunner\|gitclone.New" internal/aireview/runner_test.go` and reuse that helper (it creates a work repo with commits and a `gitclone.Manager` whose `ClonePath` points at a bare clone). Then:

```go
func writeFakeReviewClaude(t *testing.T, path, argsFile, body string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"for a in \"$@\"; do case \"$prev\" in --mcp-config) cp \"$a\" " + argsFile + ".mcp.json;; esac; prev=\"$a\"; done\n" +
		body
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

const fakeStreamOK = `echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__middleman__propose_comment","input":{"path":"a.txt"}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Found one issue.","session_id":"sess-root"}'
`

func TestReviewRunSuccessStoresSessionSummaryAndArgs(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	r, database, mrID, head, base := newReviewRunFixture(t) // runner + db + seeded MR + SHAs from the clone fixture
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, argsFile, fakeStreamOK)
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	run, err := database.CreateAIReviewRun(context.Background(), db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head, Instructions: "check errors"})
	require.NoError(err)
	require.NoError(r.StartReviewRun(context.Background(), ReviewRunInput{
		RunID: run.ID, Owner: "acme", Name: "widgets", Number: 1, HeadSHA: head, MergeBaseSHA: base,
		Instructions: "check errors", PromptContext: "PR #1: test",
		MCP: MCPConfig{Binary: "/bin/middleman", BaseURL: "http://127.0.0.1:1", Owner: "acme", Name: "widgets", Number: 1, RunID: run.ID},
	}))

	require.Eventually(func() bool {
		got, _ := database.GetAIReviewRun(context.Background(), run.ID)
		return got != nil && got.Status == "done"
	}, 10*time.Second, 50*time.Millisecond)
	got, err := database.GetAIReviewRun(context.Background(), run.ID)
	require.NoError(err)
	assert.Equal("sess-root", got.ClaudeSessionID)
	assert.Equal("Found one issue.", got.Summary)
	assert.Contains(got.RawJSON, "mcp__middleman__propose_comment")
	assert.Equal(ReviewRunWorktreePath(r.rootDir, run.ID), got.WorktreePath)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	argv := string(args)
	assert.Contains(argv, "stream-json")
	assert.Contains(argv, "--strict-mcp-config")
	assert.Contains(argv, reviewRunToolNames)
	assert.Contains(argv, "Edit,Write,NotebookEdit,Bash,Agent")
	assert.Contains(argv, "check errors") // instructions reach the prompt
	mcpCfg, err := os.ReadFile(argsFile + ".mcp.json")
	require.NoError(err)
	assert.Contains(string(mcpCfg), "--run")

	// The worktree is torn down once the run is over.
	_, statErr := os.Stat(ReviewRunWorktreePath(r.rootDir, run.ID))
	assert.True(os.IsNotExist(statErr))
}

func TestReviewRunFailureAndTimeout(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		timeout   time.Duration
		wantError string
	}{
		{"nonzero exit", "echo 'boom' >&2\nexit 3\n", 0, "claude exited"},
		{"timeout", "sleep 30\n", 300 * time.Millisecond, "timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, database, mrID, head, base := newReviewRunFixture(t)
			if tc.timeout > 0 {
				r.reviewRunTimeout = tc.timeout
			}
			dir := t.TempDir()
			fake := filepath.Join(dir, "claude.sh")
			writeFakeReviewClaude(t, fake, filepath.Join(dir, "args.txt"), tc.body)
			orig := SetBinaryForTest(fake)
			t.Cleanup(func() { SetBinaryForTest(orig) })
			run, err := database.CreateAIReviewRun(context.Background(), db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
			require.NoError(t, err)
			require.NoError(t, r.StartReviewRun(context.Background(), ReviewRunInput{RunID: run.ID, Owner: "acme", Name: "widgets", Number: 1, HeadSHA: head, MergeBaseSHA: base}))
			require.Eventually(t, func() bool {
				got, _ := database.GetAIReviewRun(context.Background(), run.ID)
				return got != nil && got.Status == "failed"
			}, 10*time.Second, 50*time.Millisecond)
			got, _ := database.GetAIReviewRun(context.Background(), run.ID)
			assert.Contains(t, got.Error, tc.wantError)
		})
	}
}

func TestReviewRunCancel(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, base := newReviewRunFixture(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, filepath.Join(dir, "args.txt"), "sleep 30\n")
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	run, err := database.CreateAIReviewRun(context.Background(), db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(err)
	require.NoError(r.StartReviewRun(context.Background(), ReviewRunInput{RunID: run.ID, Owner: "acme", Name: "widgets", Number: 1, HeadSHA: head, MergeBaseSHA: base}))
	require.Eventually(func() bool {
		got, _ := database.GetAIReviewRun(context.Background(), run.ID)
		return got != nil && got.Status == "running"
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(r.CancelReviewRun(context.Background(), run.ID))
	got, err := database.GetAIReviewRun(context.Background(), run.ID)
	require.NoError(err)
	require.Equal("cancelled", got.Status)
}

func TestReconcileMarksActiveReviewRunsFailed(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	run, err := database.CreateAIReviewRun(context.Background(), db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(err)
	require.NoError(r.ReconcileOnStartup(context.Background()))
	got, err := database.GetAIReviewRun(context.Background(), run.ID)
	require.NoError(err)
	require.Equal("failed", got.Status)
	require.Contains(got.Error, "restart")
}
```

`newReviewRunFixture(t)` returns `(*Runner, *db.DB, mrID int64, headSHA, mergeBaseSHA string)`: open a test DB (`openTestDB` exists in this package; check with grep), seed repo `acme/widgets` + MR #1 like `seedAIQuestionTestMR` does in `internal/db` (copy the minimal upserts), build the clone fixture used by the brief tests with an owner/name of `acme/widgets`, and construct `New(RunnerConfig{DB: database, Clones: mgr, WorktreeDir: t.TempDir(), HostFor: func(_, _ string) string { return "github.com" }})`. Put it at the top of `reviewruns_test.go`.

Run: `go test ./internal/aireview -run 'TestReviewRun|TestReconcileMarksActive' -shuffle=on` -> build failure.

- [ ] **Step 4: Implement**

In `runner.go`: add fields, initialize in `New` (`reviewRunsRunning: make(...)`, `reviewRunTimeout: cfg.ReviewRunTimeout` defaulting to `15 * time.Minute`), add to `ReconcileOnStartup`:

```go
	runs, err := r.db.ListActiveAIReviewRuns(ctx)
	if err != nil {
		return fmt.Errorf("list active review runs: %w", err)
	}
	for _, run := range runs {
		_ = r.db.MarkAIReviewRunFailed(ctx, run.ID, "interrupted by middleman restart", "")
	}
```

Split `provisionWorktree` into a path-taking core:

```go
func (r *Runner) provisionWorktree(ctx context.Context, owner, name, commitSHA string, threadID int64) (string, error) {
	return r.provisionWorktreeAt(ctx, owner, name, commitSHA, filepath.Join(r.rootDir, strconv.FormatInt(threadID, 10)))
}

func (r *Runner) provisionWorktreeAt(ctx context.Context, owner, name, commitSHA, worktreePath string) (string, error) {
	// body of the old provisionWorktree from `if r.hostFor == nil` on,
	// using the passed worktreePath
}

func (r *Runner) cloneDirFor(owner, name string) string {
	host := ""
	if r.hostFor != nil {
		host = r.hostFor(owner, name)
	}
	if host == "" {
		host = "github.com"
	}
	return r.clones.ClonePath(host, owner, name)
}
```

`internal/aireview/reviewruns.go`:

```go
package aireview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// These tool names must stay in sync with internal/mcp review-run tools.
const reviewRunToolNames = "mcp__middleman__propose_comment,mcp__middleman__list_drafts,mcp__middleman__get_pull"

type ReviewRunInput struct {
	RunID         int64
	Owner         string
	Name          string
	Number        int
	HeadSHA       string
	MergeBaseSHA  string
	Instructions  string
	PromptContext string
	MCP           MCPConfig
}

// ReviewRunWorktreePath is fixed per run: Claude Code keys sessions by
// cwd, so every later fork of this run's session (PR 3 chats) must run
// in exactly this directory.
func ReviewRunWorktreePath(root string, runID int64) string {
	return filepath.Join(root, fmt.Sprintf("run-%d", runID))
}

// StartReviewRun provisions the run's worktree and spawns Claude in the
// background. The run row moves running -> done|failed|cancelled.
func (r *Runner) StartReviewRun(ctx context.Context, in ReviewRunInput) error {
	if r.clones == nil {
		return errors.New("clone manager not configured")
	}
	path, err := r.provisionWorktreeAt(ctx, in.Owner, in.Name, in.HeadSHA, ReviewRunWorktreePath(r.rootDir, in.RunID))
	if err != nil {
		_ = r.db.MarkAIReviewRunFailed(ctx, in.RunID, "provision worktree: "+err.Error(), "")
		return fmt.Errorf("provision worktree: %w", err)
	}
	cached := r.cachePRCommits(ctx, in.Owner, in.Name, path, in.MergeBaseSHA, in.HeadSHA)

	runCtx, cancel := context.WithTimeout(context.Background(), r.reviewRunTimeout)
	r.mu.Lock()
	r.reviewRunsRunning[in.RunID] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.reviewRunsRunning, in.RunID)
			r.mu.Unlock()
			cancel()
			r.cleanWorktreePath(context.Background(), r.cloneDirFor(in.Owner, in.Name), path)
		}()
		r.runReviewRun(runCtx, in, path, cached)
	}()
	return nil
}

func (r *Runner) CancelReviewRun(ctx context.Context, runID int64) error {
	if err := r.db.MarkAIReviewRunCancelled(ctx, runID); err != nil {
		return err
	}
	r.mu.Lock()
	cancel, ok := r.reviewRunsRunning[runID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (r *Runner) runReviewRun(ctx context.Context, in ReviewRunInput, worktreePath string, cached []string) {
	args := []string{
		"-p", buildReviewRunPrompt(in, cached),
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,Glob,Grep," + reviewRunToolNames,
		"--disallowedTools", "Edit,Write,NotebookEdit,Bash,Agent",
	}
	if in.MCP.Binary != "" {
		cfgPath, cleanup, err := writeMCPConfig(in.MCP)
		if err != nil {
			_ = r.db.MarkAIReviewRunFailed(context.Background(), in.RunID, "write mcp config: "+err.Error(), "")
			return
		}
		defer cleanup()
		args = append(args, "--mcp-config", cfgPath, "--strict-mcp-config")
	}

	cmd := exec.CommandContext(ctx, claudeBin(), args...)
	cmd.Dir = worktreePath
	setPgid(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second

	bg := context.Background() // DB writes must outlive ctx's deadline/cancel
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, "stdout pipe: "+err.Error(), "")
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, "start claude: "+err.Error(), "")
		return
	}
	if err := r.db.MarkAIReviewRunRunning(bg, in.RunID, cmd.Process.Pid, worktreePath); err != nil {
		slog.Warn("mark review run running failed", "run_id", in.RunID, "err", err)
	}

	state := &streamState{Events: []streamEvent{}}
	var summary string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		text, isResult, dirty := applyStreamMessage(state, line)
		if isResult {
			summary = text
		}
		if dirty {
			if raw, err := json.Marshal(state); err == nil {
				_ = r.db.UpdateAIReviewRunStream(bg, in.RunID, string(raw))
			}
		}
	}
	streamErr := scanner.Err()
	waitErr := cmd.Wait()
	raw, _ := json.Marshal(state)

	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, fmt.Sprintf("review timed out after %s", r.reviewRunTimeout), string(raw))
		}
		// Canceled: CancelReviewRun already set the status.
		return
	}
	if waitErr != nil || streamErr != nil {
		msg := fmt.Sprintf("claude exited: %v", waitErr)
		if streamErr != nil {
			msg += "\nstream: " + streamErr.Error()
		}
		if stderrBuf.Len() > 0 {
			msg += "\n" + strings.TrimSpace(stderrBuf.String())
		}
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, msg, string(raw))
		return
	}
	if err := r.db.MarkAIReviewRunDone(bg, in.RunID, state.SessionID, summary, string(raw)); err != nil {
		slog.Warn("mark review run done failed", "run_id", in.RunID, "err", err)
	}
}

func buildReviewRunPrompt(in ReviewRunInput, cached []string) string {
	var b strings.Builder
	b.WriteString(reviewRunPromptHeader)
	b.WriteString("\n\n")
	if in.PromptContext != "" {
		b.WriteString("Context:\n")
		b.WriteString(in.PromptContext)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Head SHA: %s\nMerge base SHA: %s\n", in.HeadSHA, in.MergeBaseSHA)
	if strings.TrimSpace(in.Instructions) != "" {
		b.WriteString("\nThe reviewer's instructions for this review:\n")
		b.WriteString(strings.TrimSpace(in.Instructions))
		b.WriteString("\n")
	}
	b.WriteString(commitCacheNotice(cached))
	return b.String()
}

const reviewRunPromptHeader = `You are reviewing a pull request on behalf of a maintainer. You do not post anything: every finding you report becomes a suggestion the maintainer reads, discusses with you, edits, and decides whether to publish.

How to work:
- The working copy is checked out at the PR head. Read files with Read/Glob/Grep. Bash is not available. Per-commit ` + "`git show`" + ` output is cached as described below.
- First call mcp__middleman__list_drafts to see comments the maintainer already has, so you do not duplicate them.
- Call mcp__middleman__propose_comment once per finding:
  - path: repo-relative file path changed in this PR.
  - side: RIGHT for a line in the new version (added or unchanged context), LEFT for a deleted line.
  - line: the line number on that side; start_line for a multi-line range on the same side.
  - The line must be inside the PR's diff. If the tool rejects the anchor, it lists the valid ranges; pick one and call again.
  - body: the exact comment text to post on the PR, addressed to the author, in Markdown. Be specific and concise.
  - rationale: your reasoning for the maintainer (not posted): why this matters and how confident you are.
- Prefer a few high-value findings over many nits, unless the instructions say otherwise.
- When you are done, reply with a 2-4 sentence summary of the review.`
```

Run: `go test ./internal/aireview -shuffle=on`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/aireview
git commit -m "feat(aireview): run Claude review runs with the MCP review tools

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: MCP review-run tool set

**Files:**
- Modify: `internal/mcp/server.go` (`Config.RunID`; tool selection in `New`)
- Create: `internal/mcp/review_tools.go`, `internal/mcp/review_tools_test.go`
- Modify: `cmd/middleman/main.go` (`--run` flag; skip cwd resolution when `--run` is set)

**Interfaces:**
- Consumes: routes from Task 4 (`POST /api/v1/repos/{o}/{n}/pulls/{num}/ai-review-runs/{run_id}/proposals`, `GET .../review-drafts`).
- Produces: `func reviewRunTools() map[string]toolDef` with `propose_comment`, `list_drafts`, `get_pull` (the same `get_pull` def as `builtinTools()["get_pull"]`).

- [ ] **Step 1: Failing tests**

Read `internal/mcp/tools_test.go` first for the stub doer type and how tests construct a `Server` with a fake `httpDoer` and call a tool (look for `httpDoer:` and `callTool`). Then `internal/mcp/review_tools_test.go`:

```go
package mcp

// Uses the same stub doer + call helpers as tools_test.go.

func TestReviewRunModeListsOnlyReviewTools(t *testing.T) {
	s := New(Config{ServerName: "middleman", BaseURL: "http://x", ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7})
	names := []string{}
	for _, tl := range s.toolList() {
		names = append(names, tl["name"].(string))
	}
	sort.Strings(names)
	require.Equal(t, []string{"get_pull", "list_drafts", "propose_comment"}, names)
}

func TestProposeCommentPostsToRunProposals(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	doer := &stubDoer{respond: `{"id":"abc","state":"suggestion"}`} // match the stub type in tools_test.go
	s := newTestServer(Config{BaseURL: "http://x", ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7}, doer)
	out, err := s.tools["propose_comment"].call(s, map[string]any{
		"path": "a.go", "side": "RIGHT", "line": float64(12), "start_line": float64(10),
		"body": "Handle the error.", "rationale": "err is dropped",
	})
	require.NoError(err)
	assert.Contains(out, "suggestion")
	req := doer.last
	assert.Equal("POST", req.Method)
	assert.Equal("/api/v1/repos/acme/widgets/pulls/1/ai-review-runs/7/proposals", req.URL.Path)
	var body map[string]any
	require.NoError(json.Unmarshal(doer.lastBody, &body))
	assert.Equal("a.go", body["path"])
	assert.EqualValues(12, body["line"])
	assert.EqualValues(10, body["start_line"])
	assert.Equal("err is dropped", body["rationale"])
}

func TestProposeCommentSurfacesAnchorRejection(t *testing.T) {
	doer := &stubDoer{status: 400, respond: `{"detail":"line 99 is not in the diff; valid RIGHT lines for a.go: 10-14"}`}
	s := newTestServer(Config{BaseURL: "http://x", ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7}, doer)
	_, err := s.tools["propose_comment"].call(s, map[string]any{"path": "a.go", "side": "RIGHT", "line": float64(99), "body": "x", "rationale": "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid RIGHT lines")
}

func TestProposeCommentRequiresFields(t *testing.T) {
	s := newTestServer(Config{BaseURL: "http://x", ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7}, &stubDoer{})
	cases := []map[string]any{
		{"side": "RIGHT", "line": float64(1), "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "UP", "line": float64(1), "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "RIGHT", "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "RIGHT", "line": float64(1), "rationale": "r"},
	}
	for i, args := range cases {
		_, err := s.tools["propose_comment"].call(s, args)
		require.Errorf(t, err, "case %d", i)
	}
}

func TestListDraftsGetsReviewDrafts(t *testing.T) {
	doer := &stubDoer{respond: `{"drafts":[],"meta":{"body":"","event":""}}`}
	s := newTestServer(Config{BaseURL: "http://x", ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7}, doer)
	_, err := s.tools["list_drafts"].call(s, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/repos/acme/widgets/pulls/1/review-drafts", doer.last.URL.Path)
}
```

Rename `stubDoer`/`newTestServer`/`lastBody` to whatever `tools_test.go` already defines; if it has no body capture, extend the existing stub with a `lastBody []byte` field (read the request body in `Do`). If `toolList()` returns a typed slice, adapt the name extraction.

Run: `go test ./internal/mcp -run 'TestReviewRun|TestProposeComment|TestListDrafts' -shuffle=on` -> build failure.

- [ ] **Step 2: Implement**

`server.go`: add `RunID int64` to `Config` with a comment ("> 0 serves the review-run tool set used by `claude` during a Review with Claude run"); in `New`, `if cfg.RunID > 0 { s.tools = reviewRunTools() } else { s.tools = builtinTools() }`.

`review_tools.go`:

```go
package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
)

func intArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok || v != float64(int(v)) {
		return 0, false
	}
	return int(v), true
}

func prPath(s *Server) string {
	return fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d",
		url.PathEscape(s.cfg.ReviewOwner), url.PathEscape(s.cfg.ReviewName), s.cfg.ReviewNumber)
}

// reviewRunTools is the tool set Claude gets during a Review with Claude
// run. Nothing here publishes: proposals land as suggestions the
// reviewer must accept.
func reviewRunTools() map[string]toolDef {
	strSchema := map[string]any{"type": "string"}
	intSchema := map[string]any{"type": "integer"}
	return map[string]toolDef{
		"propose_comment": {
			name: "propose_comment",
			description: "Stage one review finding as a suggestion for the maintainer. " +
				"The anchor must be a line inside this PR's diff: side RIGHT for added or context lines of the new file, LEFT for deleted lines. " +
				"If the anchor is rejected, the error lists the valid ranges.",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "Repo-relative path of a file changed in the PR."},
					"side":       map[string]any{"type": "string", "enum": []string{"LEFT", "RIGHT"}},
					"line":       map[string]any{"type": "integer", "description": "Line number on that side (end line of a range)."},
					"start_line": intSchema,
					"body":       map[string]any{"type": "string", "description": "Exact comment text to post, Markdown, addressed to the PR author."},
					"rationale":  strSchema,
				},
				"required": []string{"path", "side", "line", "body", "rationale"},
			},
			call: func(s *Server, args map[string]any) (string, error) {
				path, _ := args["path"].(string)
				side, _ := args["side"].(string)
				body, _ := args["body"].(string)
				rationale, _ := args["rationale"].(string)
				line, okLine := intArg(args, "line")
				switch {
				case path == "":
					return "", fmt.Errorf("path is required")
				case side != "LEFT" && side != "RIGHT":
					return "", fmt.Errorf("side must be LEFT or RIGHT")
				case !okLine || line < 1:
					return "", fmt.Errorf("line must be a positive integer")
				case body == "":
					return "", fmt.Errorf("body is required")
				}
				req := map[string]any{"path": path, "side": side, "line": line, "body": body, "rationale": rationale}
				if start, ok := intArg(args, "start_line"); ok {
					req["start_line"] = start
				}
				b, err := json.Marshal(req)
				if err != nil {
					return "", err
				}
				return s.restJSON("POST", fmt.Sprintf("%s/ai-review-runs/%d/proposals", prPath(s), s.cfg.RunID), b)
			},
		},
		"list_drafts": {
			name:        "list_drafts",
			description: "List the maintainer's draft comments and the suggestions already staged on this PR (path, line, side, body, state).",
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			call: func(s *Server, _ map[string]any) (string, error) {
				return s.restJSON("GET", prPath(s)+"/review-drafts", nil)
			},
		},
		"get_pull": builtinTools()["get_pull"],
	}
}
```

Check `builtinTools()["get_pull"]` resolves `effectiveHandle` to the configured owner/name/number (it does when no `repo` arg is passed). `strSchema` is used for `rationale`; if lint flags the unused `intSchema` var, inline it.

`cmd/middleman/main.go` `runMCP`: add `run := fs.Int64("run", 0, "review run id: serve the Review with Claude tool set")`, set `cfg.RunID = *run`, and change the cwd-default condition to `if *run == 0 && *owner == "" && *name == "" && *number == 0`.

Run: `go test ./internal/mcp ./cmd/middleman -shuffle=on`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp cmd/middleman
git commit -m "feat(mcp): add the review-run tool set behind --run

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Server routes for runs, proposals, and suggestion transitions

**Files:**
- Create: `internal/server/huma_routes_ai_review_runs.go`
- Create: `internal/server/review_anchor.go`, `internal/server/review_anchor_test.go`
- Modify: `internal/server/huma_routes_review_drafts.go` (response fields; transition routes; register run routes)
- Regenerate API artifacts
- Test: `internal/server/ai_review_runs_e2e_test.go`

**Interfaces:**
- Consumes: Tasks 1-2; `testutil.SetupDiffRepo` (acme/widgets #1); `aireview.SetBinaryForTest`.
- Produces (routes, all `/api/v1`):

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/repos/{owner}/{name}/pulls/{number}/ai-review-runs` | `{instructions?}` | `aiReviewRunResponse`; 409 when active; 400 for `owner=local`; 503 when AI review is not configured |
| GET | `/repos/{owner}/{name}/pulls/{number}/ai-review-runs/latest` | - | `{run: aiReviewRunResponse \| null}` |
| POST | `/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/cancel` | - | `aiReviewRunResponse` |
| POST | `/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/proposals` | `{path, side, line, start_line?, body, rationale}` | `reviewDraftResponse`; 400 invalid anchor; 404 unknown run; 409 inactive run |
| POST | `/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/accept` | - | `reviewDraftResponse`; 404; 409 invalid transition |
| POST | `.../review-drafts/{draft_id}/discard` | - | same |
| POST | `.../review-drafts/{draft_id}/restore` | - | same |

```go
type aiReviewRunResponse struct {
	ID              int64   `json:"id"`
	Status          string  `json:"status"`
	HeadSHA         string  `json:"head_sha"`
	Instructions    string  `json:"instructions"`
	Summary         string  `json:"summary"`
	Error           string  `json:"error,omitempty"`
	RawJSON         string  `json:"raw_json"`
	SuggestionCount int     `json:"suggestion_count"`
	CreatedAt       string  `json:"created_at"`
	StartedAt       *string `json:"started_at,omitempty"`
	CompletedAt     *string `json:"completed_at,omitempty"`
}

// reviewDraftResponse gains:
//	RunID     *int64 `json:"run_id,omitempty"`
//	Rationale string `json:"rationale,omitempty"`

// review_anchor.go
func validateAnchor(f *gitclone.DiffFile, side string, line int, startLine *int) error
func describeRanges(f *gitclone.DiffFile, side string) string // "10-14, 30-41"
```

- [ ] **Step 1: Anchor validation unit tests (failing)**

`internal/server/review_anchor_test.go`:

```go
package server

import (
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/gitclone"
)

func anchorFile() *gitclone.DiffFile {
	return &gitclone.DiffFile{Path: "a.go", Hunks: []gitclone.Hunk{
		{Lines: []gitclone.Line{
			{Type: "context", OldNum: 9, NewNum: 10},
			{Type: "delete", OldNum: 10},
			{Type: "add", NewNum: 11},
			{Type: "add", NewNum: 12},
			{Type: "context", OldNum: 11, NewNum: 13},
		}},
		{Lines: []gitclone.Line{
			{Type: "add", NewNum: 40},
			{Type: "context", OldNum: 38, NewNum: 41},
		}},
	}}
}

func TestValidateAnchor(t *testing.T) {
	five, eleven, forty := 5, 11, 40
	cases := []struct {
		name      string
		side      string
		line      int
		start     *int
		wantErr   string
	}{
		{"right added line", "RIGHT", 12, nil, ""},
		{"right context line", "RIGHT", 13, nil, ""},
		{"left deleted line", "LEFT", 10, nil, ""},
		{"left context line", "LEFT", 9, nil, ""},
		{"right range in one hunk", "RIGHT", 13, &eleven, ""},
		{"right line outside hunks", "RIGHT", 20, nil, "valid RIGHT lines for a.go: 10-13, 40-41"},
		{"left on added-only line", "LEFT", 12, nil, "valid LEFT lines"},
		{"range across hunks", "RIGHT", 41, &eleven, "same hunk"},
		{"range start after end", "RIGHT", 11, &forty, "start_line"},
		{"range start outside diff", "RIGHT", 12, &five, "valid RIGHT lines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnchor(anchorFile(), tc.side, tc.line, tc.start)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			Assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
```

Run: `go test ./internal/server -run TestValidateAnchor -shuffle=on` -> build failure.

- [ ] **Step 2: Implement `review_anchor.go`**

```go
package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wesm/middleman/internal/gitclone"
)

// sideLine returns the line number a diff line has on side, or 0 when
// the line does not exist on that side (an added line has no LEFT
// number, a deleted line no RIGHT number).
func sideLine(l gitclone.Line, side string) int {
	if side == "LEFT" {
		if l.Type == "add" {
			return 0
		}
		return l.OldNum
	}
	if l.Type == "delete" {
		return 0
	}
	return l.NewNum
}

func hunkOf(f *gitclone.DiffFile, side string, line int) int {
	for i, h := range f.Hunks {
		for _, l := range h.Lines {
			if n := sideLine(l, side); n != 0 && n == line {
				return i
			}
		}
	}
	return -1
}

// describeRanges renders the lines commentable on side as compact
// ranges, e.g. "10-13, 40-41".
func describeRanges(f *gitclone.DiffFile, side string) string {
	var parts []string
	for _, h := range f.Hunks {
		start, prev := 0, 0
		flush := func() {
			if start == 0 {
				return
			}
			if start == prev {
				parts = append(parts, strconv.Itoa(start))
			} else {
				parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
			}
			start = 0
		}
		for _, l := range h.Lines {
			n := sideLine(l, side)
			if n == 0 {
				continue
			}
			if start != 0 && n == prev+1 {
				prev = n
				continue
			}
			flush()
			start, prev = n, n
		}
		flush()
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// validateAnchor checks that (side, line[, startLine]) is commentable in
// f, the way GitHub requires for review comments. Errors name the valid
// ranges so Claude can pick a correct anchor and retry.
func validateAnchor(f *gitclone.DiffFile, side string, line int, startLine *int) error {
	invalid := func(n int) error {
		return fmt.Errorf("line %d is not in the diff on side %s; valid %s lines for %s: %s",
			n, side, side, f.Path, describeRanges(f, side))
	}
	end := hunkOf(f, side, line)
	if end < 0 {
		return invalid(line)
	}
	if startLine == nil {
		return nil
	}
	if *startLine > line {
		return fmt.Errorf("start_line %d must not be after line %d", *startLine, line)
	}
	start := hunkOf(f, side, *startLine)
	if start < 0 {
		return invalid(*startLine)
	}
	if start != end {
		return fmt.Errorf("start_line %d and line %d must be in the same hunk", *startLine, line)
	}
	return nil
}
```

Run: `go test ./internal/server -run TestValidateAnchor -shuffle=on` -> PASS.

- [ ] **Step 3: E2E tests (failing)**

`internal/server/ai_review_runs_e2e_test.go`:

```go
package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/testutil"
)

// setupReviewRunServer wires acme/widgets #1 with a real bare clone
// and AI review enabled, and swaps in a fake claude running body.
func setupReviewRunServer(t *testing.T, claudeBody string) (*apiclient.Client, *db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	seedPR(t, database, "acme", "widgets", 1)
	diffRepo, err := testutil.SetupDiffRepo(context.Background(), dir, database)
	require.NoError(t, err)

	argsFile := filepath.Join(dir, "claude-args.txt")
	fake := filepath.Join(dir, "claude.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" + claudeBody
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))
	orig := aireview.SetBinaryForTest(fake)
	t.Cleanup(func() { aireview.SetBinaryForTest(orig) })

	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widgets", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, diffRepo.Manager, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: diffRepo.Manager, WorktreeDir: filepath.Join(dir, "wt")})
	return setupTestClient(t, srv), database, argsFile
}

const okStream = `echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"One finding.","session_id":"sess-root"}'
`

func latestRun(t *testing.T, client *apiclient.Client) *generated.AiReviewRunResponse {
	t.Helper()
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberAiReviewRunsLatestWithResponse(context.Background(), "acme", "widgets", 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	return resp.JSON200.Run
}

func firstAddedLine(t *testing.T, client *apiclient.Client) (string, int64) {
	t.Helper()
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(context.Background(), "acme", "widgets", 1, nil)
	require.NoError(t, err)
	for _, f := range *resp.JSON200.Files {
		for _, h := range *f.Hunks {
			for _, l := range *h.Lines {
				if l.Type == "add" && l.NewNum != nil {
					return f.Path, *l.NewNum
				}
			}
		}
	}
	require.FailNow(t, "no added line in fixture diff")
	return "", 0
}

func TestAPIReviewRunLifecycle(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, argsFile := setupReviewRunServer(t, okStream)
	ctx := context.Background()

	instr := "focus on error handling"
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1,
		generated.StartAiReviewRunInputBody{Instructions: &instr})
	require.NoError(err)
	require.Equal(http.StatusOK, start.StatusCode())

	require.Eventually(func() bool {
		r := latestRun(t, client)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 100*time.Millisecond)
	run := latestRun(t, client)
	assert.Equal("One finding.", run.Summary)
	assert.Equal(instr, run.Instructions)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	assert.Contains(string(args), "mcp__middleman__propose_comment")
	assert.Contains(string(args), instr)

	// Proposals after the run finished are rejected.
	path, line := firstAddedLine(t, client)
	late, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, run.Id,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusConflict, late.StatusCode())
}

func TestAPIReviewRunProposals_AnchorValidation(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()

	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAiReviewRunInputBody{})
	require.NoError(err)
	require.Equal(http.StatusOK, start.StatusCode())
	runID := start.JSON200.Id

	// A second run while this one is active is refused.
	again, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAiReviewRunInputBody{})
	require.NoError(err)
	assert.Equal(http.StatusConflict, again.StatusCode())

	path, line := firstAddedLine(t, client)
	ok, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "Handle this error.", Rationale: "err ignored"})
	require.NoError(err)
	require.Equal(http.StatusOK, ok.StatusCode())
	assert.Equal("suggestion", ok.JSON200.State)
	assert.Equal("claude", ok.JSON200.Origin)
	require.NotNil(ok.JSON200.Rationale)
	assert.Equal("err ignored", *ok.JSON200.Rationale)

	bad, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: 100000, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, bad.StatusCode())
	assert.Contains(string(bad.Body), "valid RIGHT lines")

	unchanged, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: "not/in/pr.go", Side: "RIGHT", Line: 1, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, unchanged.StatusCode())
	assert.Contains(string(unchanged.Body), "not changed in this pull request")

	// Suggestions appear in the drafts list but not in the publish count.
	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widgets", 1)
	require.NoError(err)
	require.Len(*list.JSON200.Drafts, 1)
	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	assert.Empty(*summary.JSON200.Prs)

	cancel, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdCancelWithResponse(ctx, "acme", "widgets", 1, runID)
	require.NoError(err)
	require.Equal(http.StatusOK, cancel.StatusCode())
	assert.Equal("cancelled", cancel.JSON200.Status)
	assert.Equal(int64(1), cancel.JSON200.SuggestionCount)
}

func TestAPIReviewRunProposals_RejectsInactiveRun(t *testing.T) {
	require := require.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAiReviewRunInputBody{})
	require.NoError(err)
	runID := start.JSON200.Id
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdCancelWithResponse(ctx, "acme", "widgets", 1, runID)
	require.NoError(err)

	path, line := firstAddedLine(t, client)
	cases := []struct {
		name  string
		runID int64
		want  int
	}{
		{"cancelled run", runID, http.StatusConflict},
		{"unknown run", runID + 1000, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, tc.runID,
				generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "x", Rationale: "y"})
			require.NoError(t, err)
			Assert.Equal(t, tc.want, resp.StatusCode())
		})
	}
	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widgets", 1)
	require.NoError(err)
	require.Empty(*list.JSON200.Drafts)
}

func TestAPISuggestionTransitions(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAiReviewRunInputBody{})
	require.NoError(err)
	path, line := firstAddedLine(t, client)
	prop, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, start.JSON200.Id,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "b", Rationale: "r"})
	require.NoError(err)
	id := prop.JSON200.Id

	edited, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, id,
		generated.UpdateReviewDraftInputBody{Body: "b edited"})
	require.NoError(err)
	assert.Equal("suggestion", edited.JSON200.State) // edits never change state

	disc, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdDiscardWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("discarded", disc.JSON200.State)
	acceptDiscarded, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal(http.StatusConflict, acceptDiscarded.StatusCode())
	rest, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdRestoreWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("suggestion", rest.JSON200.State)
	acc, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("draft", acc.JSON200.State)

	missing, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, "nope")
	require.NoError(err)
	assert.Equal(http.StatusNotFound, missing.StatusCode())
}

func TestAPIReviewRunRejectsLocal(t *testing.T) {
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(context.Background(), "local", "demo", 1, generated.StartAiReviewRunInputBody{})
	require.NoError(t, err)
	// setupTestServer has no AI review configured -> 503; with it configured, local -> 400.
	Assert.Contains(t, []int{http.StatusBadRequest, http.StatusServiceUnavailable}, resp.StatusCode())
}
```

Generated names are guesses from the path pattern; after `make api-generate`, correct them with `grep -n "AiReviewRun\|ProposeComment\|DraftIdAccept" internal/apiclient/generated/client.gen.go`. Also check `seedPR` + `SetupDiffRepo` together exactly as `TestAPIFilesSummary_PRWhitespaceHide` does (`seedPR` then `SetupDiffRepo`), and whether `New` with `WorktreeDir` starts a workspace manager that needs tmux; if construction fails for that reason, set the AI review worktree via whatever option the server uses (check `ServerOptions` for a separate AI worktree field) and record the choice in the ledger.

- [ ] **Step 4: Implement `huma_routes_ai_review_runs.go`**

```go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
)

const reviewInstructionsMaxBytes = 8192

type aiReviewRunResponse struct { /* as in Interfaces */ }
type aiReviewRunOutput struct{ Body aiReviewRunResponse }
type latestAIReviewRunOutput struct {
	Body struct {
		Run *aiReviewRunResponse `json:"run"`
	}
}

type startAIReviewRunInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Instructions string `json:"instructions,omitempty" doc:"Optional reviewer instructions for Claude"`
	}
}

type aiReviewRunPathInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	RunID  int64  `path:"run_id"`
}

type proposeCommentInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	RunID  int64  `path:"run_id"`
	Body   struct {
		Path      string `json:"path"`
		Side      string `json:"side"`
		Line      int    `json:"line"`
		StartLine *int   `json:"start_line,omitempty"`
		Body      string `json:"body"`
		Rationale string `json:"rationale"`
	}
}

type draftTransitionInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
}

func (s *Server) registerAIReviewRunRoutes(api huma.API) {
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs", s.startAIReviewRun)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/latest", s.latestAIReviewRun)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/cancel", s.cancelAIReviewRun)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/proposals", s.proposeComment)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/accept", s.acceptReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/discard", s.discardReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/restore", s.restoreReviewDraft)
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339)
	return &v
}

func (s *Server) toAIReviewRunResponse(ctx context.Context, r db.AIReviewRun) aiReviewRunResponse {
	n, _ := s.db.CountRunSuggestions(ctx, r.ID)
	return aiReviewRunResponse{
		ID: r.ID, Status: r.Status, HeadSHA: r.HeadSHA, Instructions: r.Instructions,
		Summary: r.Summary, Error: r.Error, RawJSON: r.RawJSON, SuggestionCount: n,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		StartedAt: rfc3339Ptr(r.StartedAt), CompletedAt: rfc3339Ptr(r.CompletedAt),
	}
}

// reviewCommentsContext lists published review comments so Claude does
// not repeat what reviewers already said. Capped to keep the prompt small.
func (s *Server) reviewCommentsContext(ctx context.Context, mrID int64) string {
	events, err := s.db.ListMREvents(ctx, mrID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, e := range events {
		if e.EventType != "review_comment" || n >= 50 {
			continue
		}
		var meta struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		}
		_ = json.Unmarshal([]byte(e.MetadataJSON), &meta)
		body := strings.TrimSpace(e.Body)
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		fmt.Fprintf(&b, "- %s:%d (%s): %s\n", meta.Path, meta.Line, e.Author, strings.ReplaceAll(body, "\n", " "))
		n++
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nPublished review comments already on this PR:\n" + b.String()
}

func (s *Server) startAIReviewRun(ctx context.Context, input *startAIReviewRunInput) (*aiReviewRunOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("Review with Claude is not available: clone manager or worktree dir not configured")
	}
	if isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("Review with Claude is not available for local worktrees yet")
	}
	if len(input.Body.Instructions) > reviewInstructionsMaxBytes {
		return nil, huma.Error400BadRequest("instructions too long")
	}
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet; try refreshing the PR first")
	}
	pr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil || pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	gitLog, _ := runGitLogForBrief(ctx, s.clones.ClonePath(host, input.Owner, input.Name), shas.MergeBaseSHA, shas.DiffHeadSHA)
	var pc strings.Builder
	fmt.Fprintf(&pc, "Repo: %s/%s\nPR #%d: %s\nBranch: %s -> %s\n", input.Owner, input.Name, pr.Number, pr.Title, pr.HeadBranch, pr.BaseBranch)
	if strings.TrimSpace(pr.Body) != "" {
		pc.WriteString("\nAuthor's description:\n" + strings.TrimSpace(pr.Body) + "\n")
	}
	if gitLog != "" {
		pc.WriteString("\nCommit log (oldest first):\n" + gitLog + "\n")
	}
	pc.WriteString(s.reviewCommentsContext(ctx, mrID))

	run, err := s.db.CreateAIReviewRun(ctx, db.NewAIReviewRun{
		MergeRequestID: mrID, HeadSHA: shas.DiffHeadSHA, ReadSHA: shas.DiffHeadSHA,
		Instructions: strings.TrimSpace(input.Body.Instructions),
	})
	if errors.Is(err, db.ErrReviewRunActive) {
		return nil, huma.Error409Conflict("a Review with Claude run is already in progress for this pull request")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("create review run: " + err.Error())
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		_ = s.db.MarkAIReviewRunFailed(ctx, run.ID, "cannot resolve middleman executable for the MCP server", "")
		return nil, huma.Error500InternalServerError("cannot resolve middleman executable for the MCP server")
	}
	if err := s.aiReview.StartReviewRun(ctx, aireview.ReviewRunInput{
		RunID: run.ID, Owner: input.Owner, Name: input.Name, Number: input.Number,
		HeadSHA: shas.DiffHeadSHA, MergeBaseSHA: shas.MergeBaseSHA,
		Instructions: run.Instructions, PromptContext: pc.String(),
		MCP: aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: input.Owner, Name: input.Name, Number: input.Number, RunID: run.ID},
	}); err != nil {
		return nil, huma.Error502BadGateway("start review run: " + err.Error())
	}
	cur, err := s.db.GetAIReviewRun(ctx, run.ID)
	if err != nil || cur == nil {
		return nil, huma.Error500InternalServerError("reload review run")
	}
	return &aiReviewRunOutput{Body: s.toAIReviewRunResponse(ctx, *cur)}, nil
}

func (s *Server) latestAIReviewRun(ctx context.Context, input *repoNumberInput) (*latestAIReviewRunOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	run, err := s.db.GetLatestAIReviewRun(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("load review run: " + err.Error())
	}
	out := &latestAIReviewRunOutput{}
	if run != nil {
		resp := s.toAIReviewRunResponse(ctx, *run)
		out.Body.Run = &resp
	}
	return out, nil
}

// runForPR loads a run and checks it belongs to the PR in the path.
func (s *Server) runForPR(ctx context.Context, owner, name string, number int, runID int64) (*db.AIReviewRun, int64, string, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, owner, name, number)
	if err != nil {
		return nil, 0, "", huma.Error404NotFound("pull request not found")
	}
	run, err := s.db.GetAIReviewRun(ctx, runID)
	if err != nil {
		return nil, 0, "", huma.Error500InternalServerError("load review run: " + err.Error())
	}
	if run == nil || run.MergeRequestID != mrID || run.Branch != branch {
		return nil, 0, "", huma.Error404NotFound("review run not found")
	}
	return run, mrID, branch, nil
}

func (s *Server) cancelAIReviewRun(ctx context.Context, input *aiReviewRunPathInput) (*aiReviewRunOutput, error) {
	run, _, _, err := s.runForPR(ctx, input.Owner, input.Name, input.Number, input.RunID)
	if err != nil {
		return nil, err
	}
	if s.aiReview != nil {
		if err := s.aiReview.CancelReviewRun(ctx, run.ID); err != nil {
			return nil, huma.Error500InternalServerError("cancel review run: " + err.Error())
		}
	} else {
		_ = s.db.MarkAIReviewRunCancelled(ctx, run.ID)
	}
	cur, err := s.db.GetAIReviewRun(ctx, run.ID)
	if err != nil || cur == nil {
		return nil, huma.Error500InternalServerError("reload review run")
	}
	return &aiReviewRunOutput{Body: s.toAIReviewRunResponse(ctx, *cur)}, nil
}

func (s *Server) proposeComment(ctx context.Context, input *proposeCommentInput) (*reviewDraftOutput, error) {
	run, mrID, branch, err := s.runForPR(ctx, input.Owner, input.Name, input.Number, input.RunID)
	if err != nil {
		return nil, err
	}
	if run.Status != "queued" && run.Status != "running" {
		return nil, huma.Error409Conflict("this review run is no longer active; nothing was stored")
	}
	in := input.Body
	if in.Side != "LEFT" && in.Side != "RIGHT" {
		return nil, huma.Error400BadRequest("side must be LEFT or RIGHT")
	}
	if in.Line < 1 || in.Path == "" {
		return nil, huma.Error400BadRequest("path and a positive line are required")
	}
	if err := validateDraftBody(in.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if len(in.Rationale) > reviewDraftBodyMaxBytes {
		return nil, huma.Error400BadRequest("rationale too large")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.MergeBaseSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet")
	}
	host := s.syncer.HostForRepo(input.Owner, input.Name)
	diff, err := s.clones.DiffPaths(ctx, host, input.Owner, input.Name, shas.MergeBaseSHA, run.ReadSHA, false, []string{in.Path})
	if err != nil {
		return nil, huma.Error500InternalServerError("load diff: " + err.Error())
	}
	var file *gitclone.DiffFile
	for i := range diff.Files {
		if diff.Files[i].Path == in.Path {
			file = &diff.Files[i]
		}
	}
	if file == nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("%s is not changed in this pull request", in.Path))
	}
	if err := validateAnchor(file, in.Side, in.Line, in.StartLine); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	draft, err := s.db.InsertClaudeSuggestion(ctx, mrID, branch, run.ID, db.NewReviewDraft{
		ID: newReviewDraftID(), Path: in.Path, Side: in.Side, Line: in.Line, StartLine: in.StartLine,
		CommitSHA: run.HeadSHA, Body: in.Body,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("store suggestion: " + err.Error())
	}
	resp := toReviewDraftResponse(draft)
	if r := strings.TrimSpace(in.Rationale); r != "" {
		if _, err := s.db.InsertReviewDraftMessage(ctx, db.NewReviewDraftMessage{
			DraftID: draft.ID, Author: "claude", Kind: "message", Body: r, Status: "done",
		}); err != nil {
			return nil, huma.Error500InternalServerError("store rationale: " + err.Error())
		}
		resp.Rationale = r
	}
	return &reviewDraftOutput{Body: resp}, nil
}

func (s *Server) transitionDraft(
	ctx context.Context, input *draftTransitionInput,
	fn func(context.Context, int64, string, string) (*db.ReviewDraft, error),
) (*reviewDraftOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	r, err := fn(ctx, mrID, branch, input.DraftID)
	if errors.Is(err, db.ErrInvalidDraftTransition) {
		return nil, huma.Error409Conflict("draft is not in a state that allows this")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("update draft: " + err.Error())
	}
	if r == nil {
		return nil, huma.Error404NotFound("draft not found")
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*r)}, nil
}

func (s *Server) acceptReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.AcceptReviewDraft)
}
func (s *Server) discardReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.DiscardReviewDraft)
}
func (s *Server) restoreReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.RestoreReviewDraft)
}
```

Add `"github.com/wesm/middleman/internal/gitclone"` to imports. Check `s.clones` is the `*gitclone.Manager` field name on `Server` (`grep -n "clones " internal/server/server.go`) and that the PR model fields are `HeadBranch`, `BaseBranch`, `Body`, `Title`, `Number` (used by `createAIBrief`).

In `huma_routes_review_drafts.go`: add `RunID *int64` and `Rationale string` (`json:"rationale,omitempty"`) to `reviewDraftResponse`; set `RunID: r.RunID` in `toReviewDraftResponse`; in `loadReviewDrafts`, call `s.db.ListDraftRationales(ctx, mrID, branch)` and set `Rationale` per draft; call `s.registerAIReviewRunRoutes(api)` at the end of `registerReviewDraftRoutes`.

- [ ] **Step 5: Regenerate, run, commit**

Run: `make api-generate && go test ./internal/server -run 'TestValidateAnchor|TestAPIReviewRun|TestAPISuggestion|TestAPIReviewDrafts|TestAPIPublishReviewDrafts' -shuffle=on`, then `go test ./internal/server -shuffle=on`.
Expected: PASS.

```bash
git add internal/server frontend/openapi/openapi.json internal/apiclient packages/ui/src/api/generated
git commit -m "feat(server): add Review with Claude runs, proposals, and suggestion transitions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Frontend API and diff store suggestions

**Files:**
- Modify: `packages/ui/src/stores/reviewDraftsApi.ts`, `reviewDraftsApi.test.ts`
- Modify: `packages/ui/src/stores/diff.svelte.ts`
- Create: `packages/ui/src/stores/diff.suggestions.test.ts`

**Interfaces (produces):**

```ts
// reviewDraftsApi.ts
export interface ServerDraft { /* existing */ run_id?: number; rationale?: string }
export interface ServerReviewRun {
  id: number; status: "queued" | "running" | "done" | "failed" | "cancelled";
  head_sha: string; instructions: string; summary: string; error?: string;
  raw_json: string; suggestion_count: number; created_at: string;
  started_at?: string; completed_at?: string;
}
// added methods
accept(pr: PRRef, id: string): Promise<ServerDraft>;
discard(pr: PRRef, id: string): Promise<ServerDraft>;
restore(pr: PRRef, id: string): Promise<ServerDraft>;
startRun(pr: PRRef, instructions: string): Promise<ServerReviewRun>;
latestRun(pr: PRRef): Promise<ServerReviewRun | null>;
cancelRun(pr: PRRef, runId: number): Promise<ServerReviewRun>;

// diff.svelte.ts
export interface DraftComment { /* existing */ origin?: "user" | "claude"; rationale?: string; runId?: number }
export interface DraftReview { body: string; event: ReviewEvent; comments: DraftComment[]; suggestions: DraftComment[] }
// store methods
getSuggestions(): DraftComment[]
getSuggestionsForPath(path: string): DraftComment[]
acceptSuggestion(id: string): void
acceptAllSuggestions(): void
discardSuggestion(id: string): void           // sets lastDiscarded
discardAllSuggestions(): void                 // sets lastDiscarded to the batch
undoDiscard(): void                           // restores lastDiscarded
getLastDiscarded(): DraftComment[]
updateSuggestionBody(id: string, body: string): void
```

Behavior:
- `fromServerDraft` maps `origin`, `rationale`, `run_id`. `loadDraftsFor` splits server rows: `state === "draft"` -> `comments`, `state === "suggestion"` -> `suggestions`. Every `DraftReview` literal (`emptyDraft`, legacy parse, `loadDraftsFor`) includes `suggestions: []` or the split list. The stale-load guard (mutation seq) covers suggestion mutations too: every suggestion mutation calls `bumpDrafts(key)` and goes through `trackWrite`.
- `acceptSuggestion`: move the suggestion to the end of `comments` (origin claude), call `api.accept`; on failure put it back in `suggestions` and set `draftSyncError`.
- `discardSuggestion`: remove from `suggestions`, set `lastDiscarded = [it]`, call `api.discard`; on failure restore it.
- `undoDiscard`: re-insert `lastDiscarded` into `suggestions`, clear it, call `api.restore` per id.
- `updateSuggestionBody`: same rules as `updateDraftCommentBody` (empty -> discard), PATCH via `api.updateBody`; state never changes.
- `acceptAllSuggestions` / `discardAllSuggestions`: loop the single-item functions.
- `hasDraftForPR` keeps counting only `comments`.

- [ ] **Step 1: Failing tests**

Extend `reviewDraftsApi.test.ts`:

```ts
  it("posts transitions to the draft action routes", async () => {
    const fn = mockFetch(200, { id: "s1", state: "draft" });
    const api = createReviewDraftsApi(() => "/");
    await api.accept(pr, "s 1");
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts/s%201/accept");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["content-type"]).toBe("application/json");
  });

  it("returns null when there is no review run yet", async () => {
    mockFetch(200, { run: null });
    const api = createReviewDraftsApi(() => "/");
    expect(await api.latestRun(pr)).toBeNull();
  });
```

Transitions send an empty JSON body `{}` so the CSRF content-type rule holds.

`diff.suggestions.test.ts` — copy the `installDraftServer` fake from `diff.drafts.test.ts` into a shared helper module `packages/ui/src/stores/testDraftServer.ts` (export `installDraftServer`, `stubClient`), make both test files import it, and extend the fake with: `/review-drafts/{id}/accept|discard|restore` (flip `state` on the stored row; 409 when invalid) and failure injection `failAction(name: "accept" | "discard" | "restore")`. Tests:

```ts
const sug = (id: string, path = "a.go", line = 1) => ({
  id, path, line, side: "RIGHT", commit_sha: "s", body: `sug ${id}`, origin: "claude", state: "suggestion",
  rationale: `why ${id}`, run_id: 1, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
});

describe("suggestions in the diff store", () => {
  it("loads suggestions apart from drafts and keeps them out of hasDraftForPR", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1"), sug("s2", "b.go")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    expect(store.getDraft().comments).toHaveLength(0);
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["s1", "s2"]);
    expect(store.getSuggestionsForPath("b.go").map((s) => s.rationale)).toEqual(["why s2"]);
    expect(store.hasDraftForPR("acme", "widget", 1)).toBe(false);
  });

  it("accepting moves a suggestion into drafts on the server", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.acceptSuggestion("s1");
    expect(store.getSuggestions()).toHaveLength(0);
    expect(store.getDraft().comments.map((c) => [c.id, c.origin])).toEqual([["s1", "claude"]]);
    await store.whenDraftsSettled();
    expect(server.drafts.get("acme/widget#1")![0]!.state).toBe("draft");
  });

  it("a failed accept puts the suggestion back", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    server.failAction("accept");
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.acceptSuggestion("s1");
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id)).toEqual(["s1"]);
    expect(store.getDraft().comments).toHaveLength(0);
    expect(store.getDraftSyncError()).not.toBeNull();
  });

  it("discard can be undone", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1"), sug("s2")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.discardAllSuggestions();
    expect(store.getSuggestions()).toHaveLength(0);
    expect(store.getLastDiscarded().map((s) => s.id)).toEqual(["s1", "s2"]);
    await store.whenDraftsSettled();
    store.undoDiscard();
    await store.whenDraftsSettled();
    expect(store.getSuggestions().map((s) => s.id).sort()).toEqual(["s1", "s2"]);
    expect(server.drafts.get("acme/widget#1")!.every((d) => d.state === "suggestion")).toBe(true);
  });

  it("editing a suggestion keeps it a suggestion", async () => {
    const server = installDraftServer();
    server.drafts.set("acme/widget#1", [sug("s1")]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.whenDraftsSettled();
    store.updateSuggestionBody("s1", "better wording");
    await store.whenDraftsSettled();
    expect(store.getSuggestions()[0]!.body).toBe("better wording");
    expect(server.drafts.get("acme/widget#1")![0]!.state).toBe("suggestion");
    expect(server.drafts.get("acme/widget#1")![0]!.body).toBe("better wording");
  });
});
```

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/` -> new tests FAIL.

- [ ] **Step 2: Implement** the API methods and store behavior above. Run the store tests -> PASS, then `cd frontend && bun run test` and `make frontend-check`.

- [ ] **Step 3: Commit**

```bash
git add packages/ui/src/stores
git commit -m "feat(ui): load and act on Claude suggestions in the diff store

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Review run store

**Files:**
- Create: `packages/ui/src/stores/aiReviewRun.svelte.ts`, `aiReviewRun.svelte.test.ts`
- Modify: `packages/ui/src/Provider.svelte`, `packages/ui/src/types.ts` (register `aiReviewRun`)
- Modify: `packages/ui/src/components/diff/DiffView.svelte` (start/stop)

**Interfaces (produces):**

```ts
export interface AIReviewRunStoreOptions {
  getBasePath?: () => string;
  onActivity?: () => void; // called on each poll while active and once when a run ends
}
export function createAIReviewRunStore(opts?: AIReviewRunStoreOptions): {
  start(owner: string, name: string, number: number): void;
  stop(): void;
  getRun(): ServerReviewRun | null;
  isActive(): boolean;             // queued|running
  isStarting(): boolean;
  getError(): string | null;
  startRun(instructions: string): Promise<boolean>;
  cancel(): Promise<void>;
  refresh(): Promise<void>;
};
export type AIReviewRunStore = ReturnType<typeof createAIReviewRunStore>;
```

Polls `latestRun` every 1500 ms while `isActive()`; when a poll sees the run leave the active state, calls `onActivity` one last time. In `Provider.svelte`, create it with `onActivity: () => void diffStore.reloadDrafts()`. `DiffView` calls `aiReviewRunStore.start(owner, name, number)` next to `briefStore.start` and `stop()` in the cleanup.

- [ ] **Step 1: Failing tests** (`aiReviewRun.svelte.test.ts`, fake timers + stubbed fetch):

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { createAIReviewRunStore } from "./aiReviewRun.svelte.js";

function run(status: string, extra: Record<string, unknown> = {}) {
  return { id: 1, status, head_sha: "h", instructions: "", summary: "", raw_json: "", suggestion_count: 0, created_at: "2026-01-01T00:00:00Z", ...extra };
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("aiReviewRun store", () => {
  it("polls while active and notifies once more when the run ends", async () => {
    vi.useFakeTimers();
    const replies = [run("running"), run("running"), run("done", { summary: "ok" })];
    globalThis.fetch = vi.fn(async () =>
      new Response(JSON.stringify({ run: replies.shift() ?? run("done") }), { status: 200 }),
    ) as unknown as typeof fetch;
    const onActivity = vi.fn();
    const store = createAIReviewRunStore({ onActivity });
    store.start("acme", "widget", 1);
    await vi.advanceTimersByTimeAsync(0);
    expect(store.isActive()).toBe(true);
    await vi.advanceTimersByTimeAsync(1500);
    await vi.advanceTimersByTimeAsync(1500);
    expect(store.getRun()?.status).toBe("done");
    const calls = onActivity.mock.calls.length;
    await vi.advanceTimersByTimeAsync(4500);
    expect(onActivity.mock.calls.length).toBe(calls); // stopped polling
    store.stop();
  });

  it("reports a conflict when a run is already active", async () => {
    globalThis.fetch = vi.fn(async (_u: unknown, init?: RequestInit) =>
      init?.method === "POST"
        ? new Response(JSON.stringify({ detail: "already in progress" }), { status: 409 })
        : new Response(JSON.stringify({ run: null }), { status: 200 }),
    ) as unknown as typeof fetch;
    const store = createAIReviewRunStore();
    store.start("acme", "widget", 1);
    expect(await store.startRun("focus")).toBe(false);
    expect(store.getError()).toContain("already in progress");
    store.stop();
  });
});
```

Run: `cd frontend && bun run test -- ../packages/ui/src/stores/aiReviewRun.svelte.test.ts` -> FAIL.

- [ ] **Step 2: Implement** with `createReviewDraftsApi(getBasePath)` for the HTTP calls (`startRun` -> `api.startRun` then begin polling; `cancel` -> `api.cancelRun` then `refresh`). Track a `polling` interval handle; `start()` resets state when the PR changes, fetches latest, and (re)arms the interval, which only fetches while `isActive()`. Register in `Provider.svelte` (`aiReviewRun: aiReviewRunStore`) and `types.ts` (`aiReviewRun: AIReviewRunStore;` in `StoreInstances`, with the import). Any test helper that builds a fake `StoreInstances` (`grep -rn "commitAnalysis:" packages/ui/src --include=*.test.ts`) needs the new key if typecheck requires it.

Run: store test PASS, `cd frontend && bun run test`, `make frontend-check`.

- [ ] **Step 3: Commit** `feat(ui): add the Review with Claude run store`.

---

### Task 7: Suggestion cards in the diff

**Files:**
- Create: `packages/ui/src/components/diff/SuggestionCard.svelte`, `SuggestionCard.test.ts`
- Modify: `packages/ui/src/components/diff/DiffFile.svelte` (three render sites after the pending cards, ~lines 1196, 1223, 1323)
- Modify: `packages/ui/src/components/diff/PendingCommentCard.svelte` (Claude badge for `origin === "claude"`), `PendingCommentCard.test.ts`

**Interfaces:** consumes Task 5 store methods.

SuggestionCard props: `{ suggestion: DraftComment; currentHeadSha: string }`. Renders:
- Header: badge "Suggestion" with a "Claude" marker, anchor label (`+12` / `−10`, range when `startLine`), outdated badge when `currentHeadSha !== "" && suggestion.commitSha !== currentHeadSha` (title: "Written against <sha7>; the PR has new commits").
- Body (pre-wrap text, like `PendingCommentCard`), or a textarea while editing (Ctrl/Cmd-Enter save, Esc cancel) that calls `diffStore.updateSuggestionBody`.
- Rationale block when present: label "Claude" and the text, muted.
- Actions: "Add to drafts" (`acceptSuggestion`), "Edit", "Discard" (`discardSuggestion`).
- Styling: dashed border using existing tokens (`--border-default` or what `PendingCommentCard` uses), accent from the same token family as the AI cards (check `AIThreadCard.svelte` styles and reuse its accent variable).

- [ ] **Step 1: Failing tests** (`SuggestionCard.test.ts`, mock `getStores` like `PendingCommentCard.test.ts`):

```ts
const acceptSuggestion = vi.fn();
const discardSuggestion = vi.fn();
const updateSuggestionBody = vi.fn();
vi.mock("../../context.js", () => ({
  getStores: () => ({ diff: { acceptSuggestion, discardSuggestion, updateSuggestionBody } }),
}));
import SuggestionCard from "./SuggestionCard.svelte";

const s = { id: "s1", path: "a.go", line: 12, side: "RIGHT" as const, commitSha: "abc", body: "Handle the error.", createdAt: "2026-01-01T00:00:00Z", origin: "claude" as const, rationale: "err is dropped" };

it("shows body, rationale, and acts on the suggestion", async () => {
  const { getByText } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
  expect(getByText("Handle the error.")).toBeTruthy();
  expect(getByText("err is dropped")).toBeTruthy();
  await fireEvent.click(getByText("Add to drafts"));
  expect(acceptSuggestion).toHaveBeenCalledWith("s1");
  await fireEvent.click(getByText("Discard"));
  expect(discardSuggestion).toHaveBeenCalledWith("s1");
});

it("flags a suggestion written against an older commit", () => {
  const { getByText } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "def" } });
  expect(getByText(/outdated/i)).toBeTruthy();
});

it("edits the body in place", async () => {
  const { getByText, getByRole } = render(SuggestionCard, { props: { suggestion: s, currentHeadSha: "abc" } });
  await fireEvent.click(getByText("Edit"));
  const box = getByRole("textbox") as HTMLTextAreaElement;
  await fireEvent.input(box, { target: { value: "Return the error." } });
  await fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
  expect(updateSuggestionBody).toHaveBeenCalledWith("s1", "Return the error.");
});
```

Add to `PendingCommentCard.test.ts`: a draft with `origin: "claude"` shows a "Claude" badge; a user draft does not.

- [ ] **Step 2: Implement** the card; in `DiffFile.svelte` add `const suggestionsByAnchor = $derived.by(...)` built from `diffStore.getSuggestionsForPath(file.path)` exactly like `pendingByAnchor`, and after each `{#each pending as p (p.id)}...{/each}` block add:

```svelte
                  {#each suggestionsByAnchor.get(leftKey) ?? [] as sg (sg.id)}
                    <SuggestionCard suggestion={sg} currentHeadSha={currentCommitSha()} />
                  {/each}
```

(with `rightKey` / the unified key at the other two sites — use the same key variable the neighbouring `pendingByAnchor.get(...)` uses). Check whether `DiffFile` decides a line "has cards" from `pendingByAnchor` (e.g. to render the card row at all); if so, include `suggestionsByAnchor` in that check. Run `DiffFile.test.ts` and the new tests.

- [ ] **Step 3: Commit** `feat(ui): show Claude suggestions inline in the diff`.

---

### Task 8: Toolbar entry, sidebar section, suggestions list, publish notice

**Files:**
- Modify: `packages/ui/src/components/diff/DiffToolbar.svelte` (+ test)
- Create: `packages/ui/src/components/diff/ClaudeReviewSection.svelte` (+ test); mount in `DiffSidebar.svelte` above `PendingCommentsSection`
- Modify: `packages/ui/src/components/diff/PendingCommentsSection.svelte` (Suggestions group, bulk actions, undo) (+ test)
- Modify: `packages/ui/src/components/diff/ReviewPanel.svelte` (+ test): notice "N suggestions not added"

Behavior:
- Toolbar: when `diff.getCurrentPR()?.owner` is not `"local"`, a "Review with Claude" button. Click opens a small popover (absolutely positioned panel under the button) with a textarea ("Optional instructions, e.g. focus on error handling") and Start / Cancel buttons; Start calls `aiReviewRun.startRun(text)`, closes on success, shows `getError()` on failure. While `aiReviewRun.isActive()`, the button is replaced by a pill: "Claude is reviewing · N suggestions" plus a "Stop" button calling `aiReviewRun.cancel()`. N = `diff.getSuggestions().length`.
- ClaudeReviewSection: hidden when `aiReviewRun.getRun()` is null. Shows status line (Reviewing… / Done / Failed / Stopped), instructions (if any), `SessionToolTimeline rawJSON={run.raw_json}` in a collapsible area, summary when done, error when failed (with "partial: N suggestions" when `suggestion_count > 0`), and "Re-run" (when not active) which opens the same popover prefilled with the last instructions. Reuse the collapsible-section pattern from `PendingCommentsSection` (header button + expanded state).
- PendingCommentsSection: above the drafts list, a "Suggestions (n)" group listing `diff.getSuggestions()` with jump-to (`jumpToDraft`), "Add all to drafts" and "Discard all" buttons; when `getLastDiscarded().length > 0`, a line "Discarded N suggestion(s) · Undo". The existing header count stays the drafts count.
- ReviewPanel: when `diffStore.getSuggestions().length > 0`, a non-blocking notice above the actions: "N Claude suggestion(s) not added to drafts. They won't be published." Not shown for local.

- [ ] **Step 1: Failing tests** — one per behavior, in the existing component-test style (mock `getStores` with the store methods each component reads):
  - `DiffToolbar.test.ts`: "Review with Claude" starts a run with the typed instructions; hidden for owner `local`; pill with Stop while active.
  - `ClaudeReviewSection.test.ts`: renders summary for a done run; renders error and partial count for a failed run; Re-run prefills instructions.
  - `PendingCommentsSection.test.ts` (create if missing): lists suggestions; "Add all to drafts" calls `acceptAllSuggestions`; undo line calls `undoDiscard`.
  - `ReviewPanel.test.ts`: notice appears with suggestions present on a GitHub PR, absent when none.

  Run them -> FAIL.

- [ ] **Step 2: Implement**; run `cd frontend && bun run test` and `make frontend-check`.

- [ ] **Step 3: Commit** `feat(ui): add Review with Claude entry, run panel, and suggestions list`.

---

### Task 9: Full verification

- [ ] `make test-short`, `make vet`, `make testify-helper-check` (only pre-existing failures allowed; list them), `gofmt -l` on changed Go files (must be empty).
- [ ] `cd frontend && bun run test`, `make frontend-check`, `make build`.
- [ ] Manual smoke (optional): run against a real PR with the real `claude` CLI; say whether done.
- [ ] Commit any formatting fixes with `git add -u`.
