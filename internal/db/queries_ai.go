package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AIThread is one reviewer-local Q&A thread on a PR. A thread owns a
// Claude session (via claude_session_id) and a disposable git worktree
// that stays alive until the reviewer explicitly closes the thread.
type AIThread struct {
	ID              int64
	MergeRequestID  int64
	Path            string
	AnchorSide      string // "LEFT" or "RIGHT"
	AnchorLine      int
	HunkStartLine   *int
	HunkEndLine     *int
	SelectionText   *string
	CommitSHA       string
	ClaudeSessionID *string
	WorktreePath    *string
	Status          string // "active" | "closed"
	CreatedAt       time.Time
	ClosedAt        *time.Time
}

// AIQuestion is one Q&A pair within an AIThread.
type AIQuestion struct {
	ID            int64
	ThreadID      int64
	Question      string
	Answer        string
	CitationsJSON string
	Error         string
	Status        string // "queued" | "running" | "done" | "cancelled" | "failed"
	PID           *int
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
}

// NewAIThreadInput describes a thread anchor and first question. It is
// used by CreateAIThread to insert the thread plus its initial
// question in one transaction — a thread always has at least one
// question (otherwise it's uninteresting).
type NewAIThreadInput struct {
	MergeRequestID int64
	Path           string
	AnchorSide     string
	AnchorLine     int
	HunkStartLine  *int
	HunkEndLine    *int
	SelectionText  *string
	CommitSHA      string
	Question       string
}

func (d *DB) CreateAIThread(ctx context.Context, in NewAIThreadInput) (AIThread, AIQuestion, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO middleman_ai_threads
			(mr_id, path, anchor_side, anchor_line, hunk_start_line, hunk_end_line, selection_text, commit_sha)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MergeRequestID, in.Path, in.AnchorSide, in.AnchorLine,
		intPtrToNullable(in.HunkStartLine), intPtrToNullable(in.HunkEndLine),
		strPtrToNullable(in.SelectionText), in.CommitSHA,
	)
	if err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("insert thread: %w", err)
	}
	threadID, err := res.LastInsertId()
	if err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("last insert id: %w", err)
	}

	qRes, err := tx.ExecContext(ctx, `
		INSERT INTO middleman_ai_questions (thread_id, question) VALUES (?, ?)`,
		threadID, in.Question,
	)
	if err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("insert question: %w", err)
	}
	questionID, err := qRes.LastInsertId()
	if err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("last insert id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AIThread{}, AIQuestion{}, fmt.Errorf("commit: %w", err)
	}

	thread, err := d.GetAIThread(ctx, threadID)
	if err != nil {
		return AIThread{}, AIQuestion{}, err
	}
	question, err := d.GetAIQuestion(ctx, questionID)
	if err != nil {
		return AIThread{}, AIQuestion{}, err
	}
	return thread, question, nil
}

func (d *DB) AddAIQuestion(ctx context.Context, threadID int64, question string) (AIQuestion, error) {
	res, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_ai_questions (thread_id, question) VALUES (?, ?)`,
		threadID, question,
	)
	if err != nil {
		return AIQuestion{}, fmt.Errorf("insert question: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AIQuestion{}, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetAIQuestion(ctx, id)
}

func (d *DB) GetAIThread(ctx context.Context, id int64) (AIThread, error) {
	return scanAIThread(d.ro.QueryRowContext(ctx,
		`SELECT id, mr_id, path, anchor_side, anchor_line,
		        hunk_start_line, hunk_end_line, selection_text, commit_sha,
		        claude_session_id, worktree_path, status, created_at, closed_at
		   FROM middleman_ai_threads WHERE id = ?`, id))
}

func (d *DB) ListAIThreadsForMR(ctx context.Context, mrID int64) ([]AIThread, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, mr_id, path, anchor_side, anchor_line,
		        hunk_start_line, hunk_end_line, selection_text, commit_sha,
		        claude_session_id, worktree_path, status, created_at, closed_at
		   FROM middleman_ai_threads
		  WHERE mr_id = ?
		  ORDER BY id ASC`, mrID)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()

	var out []AIThread
	for rows.Next() {
		t, err := scanAIThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) UpdateAIThreadSession(ctx context.Context, id int64, sessionID, worktreePath string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_threads
		    SET claude_session_id = ?, worktree_path = ?
		  WHERE id = ?`,
		sessionID, worktreePath, id,
	)
	return err
}

func (d *DB) CloseAIThread(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_threads
		    SET status = 'closed', closed_at = datetime('now')
		  WHERE id = ? AND status <> 'closed'`,
		id,
	)
	return err
}

func (d *DB) DeleteAIThread(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_ai_threads WHERE id = ?`, id,
	)
	return err
}

func (d *DB) GetAIQuestion(ctx context.Context, id int64) (AIQuestion, error) {
	return scanAIQuestion(d.ro.QueryRowContext(ctx,
		`SELECT id, thread_id, question, answer, citations_json, error,
		        status, pid, created_at, started_at, completed_at
		   FROM middleman_ai_questions WHERE id = ?`, id))
}

func (d *DB) ListAIQuestionsForThread(ctx context.Context, threadID int64) ([]AIQuestion, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, thread_id, question, answer, citations_json, error,
		        status, pid, created_at, started_at, completed_at
		   FROM middleman_ai_questions
		  WHERE thread_id = ?
		  ORDER BY id ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	defer rows.Close()
	var out []AIQuestion
	for rows.Next() {
		q, err := scanAIQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (d *DB) ListAIQuestionsForMR(ctx context.Context, mrID int64) ([]AIQuestion, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT q.id, q.thread_id, q.question, q.answer, q.citations_json, q.error,
		        q.status, q.pid, q.created_at, q.started_at, q.completed_at
		   FROM middleman_ai_questions q
		   JOIN middleman_ai_threads t ON t.id = q.thread_id
		  WHERE t.mr_id = ?
		  ORDER BY q.id ASC`, mrID)
	if err != nil {
		return nil, fmt.Errorf("list questions for mr: %w", err)
	}
	defer rows.Close()
	var out []AIQuestion
	for rows.Next() {
		q, err := scanAIQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (d *DB) MarkAIQuestionRunning(ctx context.Context, id int64, pid int) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_questions
		    SET status = 'running', pid = ?, started_at = datetime('now')
		  WHERE id = ?`,
		pid, id,
	)
	return err
}

func (d *DB) MarkAIQuestionDone(ctx context.Context, id int64, answer, citationsJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_questions
		    SET status = 'done', answer = ?, citations_json = ?,
		        pid = NULL, completed_at = datetime('now')
		  WHERE id = ?`,
		answer, citationsJSON, id,
	)
	return err
}

func (d *DB) MarkAIQuestionFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_questions
		    SET status = 'failed', error = ?, pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ?`,
		errMsg, id,
	)
	return err
}

func (d *DB) MarkAIQuestionCancelled(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_questions
		    SET status = 'cancelled', pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ? AND status IN ('queued', 'running')`,
		id,
	)
	return err
}

func (d *DB) DeleteAIQuestion(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_ai_questions WHERE id = ?`, id,
	)
	return err
}

// GetRunningAIQuestions returns all questions currently marked as
// running or queued. Used on startup to reconcile state after a
// crash — any entries here had no in-flight process after restart.
func (d *DB) GetRunningAIQuestions(ctx context.Context) ([]AIQuestion, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, thread_id, question, answer, citations_json, error,
		        status, pid, created_at, started_at, completed_at
		   FROM middleman_ai_questions
		  WHERE status IN ('queued', 'running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIQuestion
	for rows.Next() {
		q, err := scanAIQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAIThread(row scanner) (AIThread, error) {
	var t AIThread
	var hunkStart, hunkEnd sql.NullInt64
	var selection, sessionID, worktree sql.NullString
	var closedAt sql.NullTime

	err := row.Scan(
		&t.ID, &t.MergeRequestID, &t.Path, &t.AnchorSide, &t.AnchorLine,
		&hunkStart, &hunkEnd, &selection, &t.CommitSHA,
		&sessionID, &worktree, &t.Status, &t.CreatedAt, &closedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AIThread{}, err
		}
		return AIThread{}, fmt.Errorf("scan thread: %w", err)
	}
	if hunkStart.Valid {
		v := int(hunkStart.Int64)
		t.HunkStartLine = &v
	}
	if hunkEnd.Valid {
		v := int(hunkEnd.Int64)
		t.HunkEndLine = &v
	}
	if selection.Valid {
		t.SelectionText = &selection.String
	}
	if sessionID.Valid {
		t.ClaudeSessionID = &sessionID.String
	}
	if worktree.Valid {
		t.WorktreePath = &worktree.String
	}
	if closedAt.Valid {
		t.ClosedAt = &closedAt.Time
	}
	return t, nil
}

func scanAIQuestion(row scanner) (AIQuestion, error) {
	var q AIQuestion
	var pid sql.NullInt64
	var startedAt, completedAt sql.NullTime

	err := row.Scan(
		&q.ID, &q.ThreadID, &q.Question, &q.Answer, &q.CitationsJSON, &q.Error,
		&q.Status, &pid, &q.CreatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AIQuestion{}, err
		}
		return AIQuestion{}, fmt.Errorf("scan question: %w", err)
	}
	if pid.Valid {
		v := int(pid.Int64)
		q.PID = &v
	}
	if startedAt.Valid {
		q.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		q.CompletedAt = &completedAt.Time
	}
	return q, nil
}

func intPtrToNullable(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtrToNullable(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
