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
