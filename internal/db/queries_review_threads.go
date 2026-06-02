package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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

// ReviewThreadComment is one comment within a ReviewThread.
type ReviewThreadComment struct {
	ID          int64
	ThreadID    int64
	Author      string // "user" | "agent"
	Body        string
	TurnID      *int64 // nullable; worktree_session_turns.id for agent replies
	CreatedAt   time.Time
	SentToAgent bool // true if this comment was an "Ask Claude" reply sent to the agent
}

// NewReviewThread describes a thread anchor plus the reviewer's root
// comment. CreateReviewThreads inserts the thread and its first
// ('user') comment together, followed by any Comments in order.
type NewReviewThread struct {
	Path      string
	Side      string
	Line      int
	StartLine *int
	CommitSHA string
	Body      string // the reviewer's root comment
	// Comments are extra authored comments appended after the root,
	// in order. Used when promoting an Ask-Claude session, whose Q&A
	// turns become alternating user/agent comments.
	Comments []NewReviewThreadComment
}

// NewReviewThreadComment is one extra comment appended after a thread's
// root comment at creation time.
type NewReviewThreadComment struct {
	Author string // "user" | "agent"
	Body   string
}

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
		// Append any extra authored comments in order. A thread that
		// carries an agent comment (e.g. a promoted Ask-Claude session)
		// is created as 'discussed' since it already holds agent input.
		hasAgent := false
		for _, c := range t.Comments {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO middleman_review_thread_comments (thread_id, author, body)
				VALUES (?, ?, ?)`,
				threadID, c.Author, c.Body,
			); err != nil {
				return nil, fmt.Errorf("insert appended comment: %w", err)
			}
			if c.Author == "agent" {
				hasAgent = true
			}
		}
		if hasAgent {
			if _, err := tx.ExecContext(ctx, `
				UPDATE middleman_review_threads SET status = 'discussed' WHERE id = ?`,
				threadID,
			); err != nil {
				return nil, fmt.Errorf("set discussed status: %w", err)
			}
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

// GetReviewThread returns a single review thread by its ID.
func (d *DB) GetReviewThread(ctx context.Context, id int64) (ReviewThread, error) {
	return scanReviewThread(d.ro.QueryRowContext(ctx, `
		SELECT id, mr_id, path, side, line, start_line, commit_sha,
		       status, branch, hidden_at, created_at, updated_at
		  FROM middleman_review_threads WHERE id = ?`, id))
}

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
		SELECT id, thread_id, author, body, turn_id, created_at, sent_to_agent
		  FROM middleman_review_thread_comments WHERE id = ?`, id))
}

// ListReviewThreadCommentsForMR returns every comment across the MR's
// threads, oldest-first by id. The handler groups them by thread_id.
func (d *DB) ListReviewThreadCommentsForMR(ctx context.Context, mrID int64) ([]ReviewThreadComment, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT c.id, c.thread_id, c.author, c.body, c.turn_id, c.created_at, c.sent_to_agent
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

// ListReviewThreadComments returns the comments for a single thread,
// oldest-first by id.
func (d *DB) ListReviewThreadComments(ctx context.Context, threadID int64) ([]ReviewThreadComment, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT id, thread_id, author, body, turn_id, created_at, sent_to_agent
		  FROM middleman_review_thread_comments
		 WHERE thread_id = ?
		 ORDER BY id ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("list thread comments: %w", err)
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

// MarkReviewThreadCommentSentToAgent flags a comment as one that engaged
// the agent (an "Ask Claude" reply).
func (d *DB) MarkReviewThreadCommentSentToAgent(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_thread_comments SET sent_to_agent = 1 WHERE id = ?`, id)
	return err
}

// DeleteReviewThread permanently removes a thread and its comments in one
// transaction. Comments are deleted explicitly so the call is correct
// regardless of whether the schema declares an ON DELETE CASCADE.
func (d *DB) DeleteReviewThread(ctx context.Context, id int64) error {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_review_thread_comments WHERE thread_id = ?`, id); err != nil {
		return fmt.Errorf("delete comments: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_review_threads WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete thread: %w", err)
	}
	return tx.Commit()
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
	var sentToAgent int64
	err := row.Scan(&c.ID, &c.ThreadID, &c.Author, &c.Body, &turnID, &c.CreatedAt, &sentToAgent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewThreadComment{}, err
		}
		return ReviewThreadComment{}, fmt.Errorf("scan comment: %w", err)
	}
	if turnID.Valid {
		c.TurnID = &turnID.Int64
	}
	c.SentToAgent = sentToAgent != 0
	return c, nil
}
