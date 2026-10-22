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
	ID             int64
	MergeRequestID int64
	Branch         string
	HeadSHA        string
	ReadSHA        string
	// BaseSHA is the diff base the run was started against; anchors are
	// validated against BaseSHA..ReadSHA for the whole run.
	BaseSHA string
	// LocalWorktreePath is the reviewer's worktree for local runs ("" for
	// GitHub runs); the run's checkout and snapshot ref live in its repo.
	LocalWorktreePath string
	Instructions      string
	ClaudeSessionID   string
	WorktreePath      string
	Status            string
	Summary           string
	RawJSON           string
	Error             string
	CreatedAt         time.Time
	StartedAt         *time.Time
	CompletedAt       *time.Time
}

type NewAIReviewRun struct {
	MergeRequestID    int64
	Branch            string
	HeadSHA           string
	ReadSHA           string
	BaseSHA           string
	LocalWorktreePath string
	Instructions      string
}

const aiReviewRunColumns = `id, merge_request_id, branch, head_sha, read_sha, instructions,
	claude_session_id, worktree_path, status, summary, raw_json, error,
	created_at, started_at, completed_at, base_sha, local_worktree_path`

func scanAIReviewRun(sc interface{ Scan(...any) error }) (AIReviewRun, error) {
	var (
		r         AIReviewRun
		started   sql.NullTime
		completed sql.NullTime
	)
	err := sc.Scan(&r.ID, &r.MergeRequestID, &r.Branch, &r.HeadSHA, &r.ReadSHA, &r.Instructions,
		&r.ClaudeSessionID, &r.WorktreePath, &r.Status, &r.Summary, &r.RawJSON, &r.Error,
		&r.CreatedAt, &started, &completed, &r.BaseSHA, &r.LocalWorktreePath)
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
		     (merge_request_id, branch, head_sha, read_sha, base_sha, local_worktree_path,
		      instructions, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
		in.MergeRequestID, in.Branch, in.HeadSHA, in.ReadSHA, in.BaseSHA, in.LocalWorktreePath,
		in.Instructions, time.Now().UTC(),
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

// SetAIReviewRunSession records Claude's session id as soon as it is
// known, so a run that fails later still has a session to fork from.
func (d *DB) SetAIReviewRunSession(ctx context.Context, id int64, sessionID string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs SET claude_session_id = ? WHERE id = ?`, sessionID, id)
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

// SetAIReviewRunReadSHA records what Claude reads for a run (the local
// snapshot, created after the row exists so it can be named by run id).
func (d *DB) SetAIReviewRunReadSHA(ctx context.Context, id int64, readSHA string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs SET read_sha = ? WHERE id = ?`, readSHA, id)
	return err
}

func (d *DB) listRuns(ctx context.Context, query string, args ...any) ([]AIReviewRun, error) {
	rows, err := d.ro.QueryContext(ctx, query, args...)
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

// ListActiveAIReviewRunsOnClosedPRs returns queued/running runs whose
// pull request has been closed or merged.
func (d *DB) ListActiveAIReviewRunsOnClosedPRs(ctx context.Context) ([]AIReviewRun, error) {
	return d.listRuns(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs r
		  WHERE r.status IN ('queued', 'running')
		    AND r.merge_request_id IN (
		        SELECT id FROM middleman_merge_requests WHERE state IN ('closed', 'merged'))
		  ORDER BY r.id`)
}

// ListLocalRunsWithoutLiveDrafts returns finished local runs that no
// suggestion or draft points at any more; their snapshot refs can go.
func (d *DB) ListLocalRunsWithoutLiveDrafts(ctx context.Context) ([]AIReviewRun, error) {
	return d.listRuns(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs r
		  WHERE r.local_worktree_path != ''
		    AND r.snapshot_pruned = 0
		    AND r.status NOT IN ('queued', 'running')
		    AND NOT EXISTS (
		        SELECT 1 FROM middleman_review_drafts rd
		         WHERE rd.run_id = r.id AND rd.state IN ('suggestion', 'draft'))
		  ORDER BY r.id`)
}

// MarkAIReviewRunSnapshotPruned records that a run's snapshot ref was
// deleted, so later prune passes skip it.
func (d *DB) MarkAIReviewRunSnapshotPruned(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_review_runs SET snapshot_pruned = 1 WHERE id = ?`, id)
	return err
}

// ListFinishedLocalRuns returns local runs that are no longer active.
// Their checkouts in the reviewer's repo should not exist; the startup
// sweep removes any a crash left behind.
func (d *DB) ListFinishedLocalRuns(ctx context.Context) ([]AIReviewRun, error) {
	return d.listRuns(ctx,
		`SELECT `+aiReviewRunColumns+` FROM middleman_ai_review_runs
		  WHERE local_worktree_path != '' AND status NOT IN ('queued', 'running')
		  ORDER BY id`)
}
