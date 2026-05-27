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

// ActiveAIThreadWithContext is an active thread plus enough repo/PR
// metadata for the global sessions panel to render a row without
// a second lookup per row.
type ActiveAIThreadWithContext struct {
	AIThread
	RepoOwner             string
	RepoName              string
	PlatformHost          string
	MRNumber              int
	MRTitle               string
	LatestQuestionStatus  string // "queued" | "running" | "done" | "failed" | "cancelled" | ""
	OpenQuestionCount     int    // questions in queued/running state
	LatestQuestionStarted *time.Time
}

// InFlightAIBriefWithContext is a queued/running brief enriched with
// repo + PR metadata for the same panel.
type InFlightAIBriefWithContext struct {
	AIBrief
	RepoOwner    string
	RepoName     string
	PlatformHost string
	MRNumber     int
	MRTitle      string
}

// AIBrief is a structural review brief for a PR. One-per-(mr_id,
// head_sha): regenerating after a push creates a new row, so older
// briefs remain as historical artifacts.
type AIBrief struct {
	ID              int64
	MergeRequestID  int64
	HeadSHA         string
	ClaudeSessionID *string
	WorktreePath    *string
	Status          string // queued | running | done | failed | cancelled
	Depth           string // quick | deep
	Content         string // raw Markdown body
	Error           string
	PID             *int
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
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

// ListAIThreadsForMR returns only *active* threads for the given
// MR. Closed threads linger in the table (we may want them for
// audit/history) but the review UI treats "closed" as "gone" —
// otherwise a failed delete would make the card keep reappearing
// on refresh, which is a worse UX than an orphaned row.
func (d *DB) ListAIThreadsForMR(ctx context.Context, mrID int64) ([]AIThread, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, mr_id, path, anchor_side, anchor_line,
		        hunk_start_line, hunk_end_line, selection_text, commit_sha,
		        claude_session_id, worktree_path, status, created_at, closed_at
		   FROM middleman_ai_threads
		  WHERE mr_id = ? AND status = 'active'
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

// ListActiveAIThreads returns every open Q&A thread across all PRs,
// enriched with the repo + PR metadata needed to render a global
// "Claude sessions" panel. Threads are ordered newest-first so
// recently-created ones bubble up.
func (d *DB) ListActiveAIThreads(ctx context.Context) ([]ActiveAIThreadWithContext, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT t.id, t.mr_id, t.path, t.anchor_side, t.anchor_line,
		       t.hunk_start_line, t.hunk_end_line, t.selection_text, t.commit_sha,
		       t.claude_session_id, t.worktree_path, t.status, t.created_at, t.closed_at,
		       r.owner, r.name, COALESCE(r.platform_host, ''),
		       m.number, m.title,
		       (SELECT status FROM middleman_ai_questions q
		         WHERE q.thread_id = t.id
		         ORDER BY q.id DESC LIMIT 1) AS latest_question_status,
		       (SELECT COUNT(*) FROM middleman_ai_questions q
		         WHERE q.thread_id = t.id
		           AND q.status IN ('queued', 'running')) AS open_question_count,
		       (SELECT started_at FROM middleman_ai_questions q
		         WHERE q.thread_id = t.id
		         ORDER BY q.id DESC LIMIT 1) AS latest_question_started
		  FROM middleman_ai_threads t
		  JOIN middleman_merge_requests m ON m.id = t.mr_id
		  JOIN middleman_repos r ON r.id = m.repo_id
		 WHERE t.status = 'active'
		 ORDER BY t.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list active threads: %w", err)
	}
	defer rows.Close()

	var out []ActiveAIThreadWithContext
	for rows.Next() {
		var item ActiveAIThreadWithContext
		var sessionID, worktree, selection sql.NullString
		var hunkStart, hunkEnd sql.NullInt64
		var closedAt sql.NullTime
		var latestStatus sql.NullString
		var latestStarted sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.MergeRequestID, &item.Path, &item.AnchorSide, &item.AnchorLine,
			&hunkStart, &hunkEnd, &selection, &item.CommitSHA,
			&sessionID, &worktree, &item.Status, &item.CreatedAt, &closedAt,
			&item.RepoOwner, &item.RepoName, &item.PlatformHost,
			&item.MRNumber, &item.MRTitle,
			&latestStatus, &item.OpenQuestionCount, &latestStarted,
		); err != nil {
			return nil, fmt.Errorf("scan active thread: %w", err)
		}
		if hunkStart.Valid {
			v := int(hunkStart.Int64)
			item.HunkStartLine = &v
		}
		if hunkEnd.Valid {
			v := int(hunkEnd.Int64)
			item.HunkEndLine = &v
		}
		if selection.Valid {
			item.SelectionText = &selection.String
		}
		if sessionID.Valid {
			item.ClaudeSessionID = &sessionID.String
		}
		if worktree.Valid {
			item.WorktreePath = &worktree.String
		}
		if closedAt.Valid {
			item.ClosedAt = &closedAt.Time
		}
		if latestStatus.Valid {
			item.LatestQuestionStatus = latestStatus.String
		}
		if latestStarted.Valid {
			item.LatestQuestionStarted = &latestStarted.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListInFlightAIBriefs returns queued/running briefs across all PRs
// with repo + PR metadata. Briefs that already finished, failed, or
// were cancelled are excluded; there's nothing to close there.
func (d *DB) ListInFlightAIBriefs(ctx context.Context) ([]InFlightAIBriefWithContext, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT b.id, b.mr_id, b.head_sha, b.claude_session_id, b.worktree_path,
		       b.status, b.depth, b.content, b.error, b.pid,
		       b.created_at, b.started_at, b.completed_at,
		       r.owner, r.name, COALESCE(r.platform_host, ''),
		       m.number, m.title
		  FROM middleman_ai_briefs b
		  JOIN middleman_merge_requests m ON m.id = b.mr_id
		  JOIN middleman_repos r ON r.id = m.repo_id
		 WHERE b.status IN ('queued', 'running')
		 ORDER BY b.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list in-flight briefs: %w", err)
	}
	defer rows.Close()

	var out []InFlightAIBriefWithContext
	for rows.Next() {
		var item InFlightAIBriefWithContext
		var sessionID, worktree sql.NullString
		var pid sql.NullInt64
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.MergeRequestID, &item.HeadSHA, &sessionID, &worktree,
			&item.Status, &item.Depth, &item.Content, &item.Error, &pid,
			&item.CreatedAt, &startedAt, &completedAt,
			&item.RepoOwner, &item.RepoName, &item.PlatformHost,
			&item.MRNumber, &item.MRTitle,
		); err != nil {
			return nil, fmt.Errorf("scan in-flight brief: %w", err)
		}
		if sessionID.Valid {
			item.ClaudeSessionID = &sessionID.String
		}
		if worktree.Valid {
			item.WorktreePath = &worktree.String
		}
		if pid.Valid {
			v := int(pid.Int64)
			item.PID = &v
		}
		if startedAt.Valid {
			item.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.Time
		}
		out = append(out, item)
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

// AIThreadCloseTarget identifies an AI thread that should be
// auto-closed because its parent PR is no longer open. Returned by
// ListAIThreadsToAutoClose for the reconciler that runs after each
// sync.
type AIThreadCloseTarget struct {
	ID   int64
	MRID int64
}

// ListAIThreadsToAutoClose returns every active AI thread whose
// parent merge request is in a non-open state ('closed' or 'merged').
// Used by the post-sync reconciler to enumerate threads that should
// be closed because their PR is no longer open. Idempotent — already-
// closed threads are filtered out by the t.status='active' clause.
func (d *DB) ListAIThreadsToAutoClose(ctx context.Context) ([]AIThreadCloseTarget, error) {
	rows, err := d.ro.QueryContext(ctx, `
		SELECT t.id, t.mr_id
		  FROM middleman_ai_threads t
		  JOIN middleman_merge_requests m ON m.id = t.mr_id
		 WHERE t.status = 'active'
		   AND m.state IN ('closed', 'merged')
		 ORDER BY t.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list threads to auto-close: %w", err)
	}
	defer rows.Close()
	var out []AIThreadCloseTarget
	for rows.Next() {
		var t AIThreadCloseTarget
		if err := rows.Scan(&t.ID, &t.MRID); err != nil {
			return nil, fmt.Errorf("scan auto-close target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
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
		  WHERE t.mr_id = ? AND t.status = 'active'
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

// --- AIBrief ---

// UpsertAIBriefQueued creates a brief row in `queued` status for the
// given (mr_id, head_sha), replacing any existing row for that pair.
// Callers then spawn the Claude run and transition the row through
// running → done/failed.
func (d *DB) UpsertAIBriefQueued(ctx context.Context, mrID int64, headSHA, depth string) (AIBrief, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return AIBrief{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_ai_briefs WHERE mr_id = ? AND head_sha = ?`,
		mrID, headSHA,
	); err != nil {
		return AIBrief{}, fmt.Errorf("drop existing brief: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_ai_briefs (mr_id, head_sha, depth) VALUES (?, ?, ?)`,
		mrID, headSHA, depth,
	)
	if err != nil {
		return AIBrief{}, fmt.Errorf("insert brief: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AIBrief{}, err
	}
	if err := tx.Commit(); err != nil {
		return AIBrief{}, err
	}
	return d.GetAIBrief(ctx, id)
}

func (d *DB) GetAIBrief(ctx context.Context, id int64) (AIBrief, error) {
	return scanAIBrief(d.ro.QueryRowContext(ctx,
		`SELECT id, mr_id, head_sha, claude_session_id, worktree_path,
		        status, depth, content, error, pid,
		        created_at, started_at, completed_at
		   FROM middleman_ai_briefs WHERE id = ?`, id))
}

// GetLatestAIBriefForMR returns the most recent brief for the MR,
// regardless of head SHA. Callers typically compare head_sha against
// the PR's current head to decide whether the brief is fresh.
func (d *DB) GetLatestAIBriefForMR(ctx context.Context, mrID int64) (AIBrief, error) {
	return scanAIBrief(d.ro.QueryRowContext(ctx,
		`SELECT id, mr_id, head_sha, claude_session_id, worktree_path,
		        status, depth, content, error, pid,
		        created_at, started_at, completed_at
		   FROM middleman_ai_briefs
		  WHERE mr_id = ?
		  ORDER BY id DESC LIMIT 1`, mrID))
}

func (d *DB) MarkAIBriefRunning(ctx context.Context, id int64, pid int, sessionID, worktreePath string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_briefs
		    SET status = 'running', pid = ?,
		        claude_session_id = ?, worktree_path = ?,
		        started_at = datetime('now')
		  WHERE id = ?`,
		pid, sessionID, worktreePath, id,
	)
	return err
}

func (d *DB) MarkAIBriefDone(ctx context.Context, id int64, content string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_briefs
		    SET status = 'done', content = ?, pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ?`,
		content, id,
	)
	return err
}

func (d *DB) MarkAIBriefFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_briefs
		    SET status = 'failed', error = ?, pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ?`,
		errMsg, id,
	)
	return err
}

func (d *DB) MarkAIBriefCancelled(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_briefs
		    SET status = 'cancelled', pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ? AND status IN ('queued', 'running')`,
		id,
	)
	return err
}

func (d *DB) DeleteAIBrief(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_ai_briefs WHERE id = ?`, id,
	)
	return err
}

// GetRunningAIBriefs returns all briefs in a non-terminal state.
// Used on startup to mark stale rows as failed.
func (d *DB) GetRunningAIBriefs(ctx context.Context) ([]AIBrief, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, mr_id, head_sha, claude_session_id, worktree_path,
		        status, depth, content, error, pid,
		        created_at, started_at, completed_at
		   FROM middleman_ai_briefs
		  WHERE status IN ('queued', 'running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIBrief
	for rows.Next() {
		b, err := scanAIBrief(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanAIBrief(row scanner) (AIBrief, error) {
	var b AIBrief
	var sessionID, worktree sql.NullString
	var pid sql.NullInt64
	var startedAt, completedAt sql.NullTime
	err := row.Scan(
		&b.ID, &b.MergeRequestID, &b.HeadSHA, &sessionID, &worktree,
		&b.Status, &b.Depth, &b.Content, &b.Error, &pid,
		&b.CreatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AIBrief{}, err
		}
		return AIBrief{}, fmt.Errorf("scan brief: %w", err)
	}
	if sessionID.Valid {
		b.ClaudeSessionID = &sessionID.String
	}
	if worktree.Valid {
		b.WorktreePath = &worktree.String
	}
	if pid.Valid {
		v := int(pid.Int64)
		b.PID = &v
	}
	if startedAt.Valid {
		b.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		b.CompletedAt = &completedAt.Time
	}
	return b, nil
}

// --- AICommitAnalysis ---

// AICommitAnalysis is Claude's per-commit review guide. One row per
// (mr_id, commit_sha): regenerating replaces in place. Commits are
// immutable so the analysis is permanently cache-able; we still key
// on mr_id because the surrounding-commit context (Sequence section)
// depends on the PR's other commits.
type AICommitAnalysis struct {
	ID              int64
	MergeRequestID  int64
	CommitSHA       string
	ClaudeSessionID *string
	WorktreePath    *string
	Status          string // queued | running | done | failed | cancelled
	Content         string
	Error           string
	PID             *int
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

func (d *DB) UpsertAICommitAnalysisQueued(ctx context.Context, mrID int64, commitSHA string) (AICommitAnalysis, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return AICommitAnalysis{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_ai_commit_analyses WHERE mr_id = ? AND commit_sha = ?`,
		mrID, commitSHA,
	); err != nil {
		return AICommitAnalysis{}, fmt.Errorf("drop existing commit analysis: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_ai_commit_analyses (mr_id, commit_sha) VALUES (?, ?)`,
		mrID, commitSHA,
	)
	if err != nil {
		return AICommitAnalysis{}, fmt.Errorf("insert commit analysis: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AICommitAnalysis{}, err
	}
	if err := tx.Commit(); err != nil {
		return AICommitAnalysis{}, err
	}
	return d.GetAICommitAnalysis(ctx, id)
}

func (d *DB) GetAICommitAnalysis(ctx context.Context, id int64) (AICommitAnalysis, error) {
	return scanAICommitAnalysis(d.ro.QueryRowContext(ctx,
		`SELECT id, mr_id, commit_sha, claude_session_id, worktree_path,
		        status, content, error, pid,
		        created_at, started_at, completed_at
		   FROM middleman_ai_commit_analyses WHERE id = ?`, id))
}

// GetAICommitAnalysisForCommit returns the analysis for the given
// (mr_id, commit_sha). Used by the UI's per-commit "Analyze" surface
// to check whether a guide has already been generated.
func (d *DB) GetAICommitAnalysisForCommit(ctx context.Context, mrID int64, commitSHA string) (AICommitAnalysis, error) {
	return scanAICommitAnalysis(d.ro.QueryRowContext(ctx,
		`SELECT id, mr_id, commit_sha, claude_session_id, worktree_path,
		        status, content, error, pid,
		        created_at, started_at, completed_at
		   FROM middleman_ai_commit_analyses
		  WHERE mr_id = ? AND commit_sha = ?`, mrID, commitSHA))
}

func (d *DB) MarkAICommitAnalysisRunning(ctx context.Context, id int64, pid int, sessionID, worktreePath string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_commit_analyses
		    SET status = 'running', pid = ?,
		        claude_session_id = ?, worktree_path = ?,
		        started_at = datetime('now')
		  WHERE id = ?`,
		pid, sessionID, worktreePath, id,
	)
	return err
}

func (d *DB) MarkAICommitAnalysisDone(ctx context.Context, id int64, content string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_commit_analyses
		    SET status = 'done', content = ?, pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ?`,
		content, id,
	)
	return err
}

func (d *DB) MarkAICommitAnalysisFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_commit_analyses
		    SET status = 'failed', error = ?, pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ?`,
		errMsg, id,
	)
	return err
}

func (d *DB) MarkAICommitAnalysisCancelled(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_ai_commit_analyses
		    SET status = 'cancelled', pid = NULL,
		        completed_at = datetime('now')
		  WHERE id = ? AND status IN ('queued', 'running')`,
		id,
	)
	return err
}

func (d *DB) DeleteAICommitAnalysis(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_ai_commit_analyses WHERE id = ?`, id,
	)
	return err
}

func scanAICommitAnalysis(row scanner) (AICommitAnalysis, error) {
	var a AICommitAnalysis
	var sessionID, worktree sql.NullString
	var pid sql.NullInt64
	var startedAt, completedAt sql.NullTime
	err := row.Scan(
		&a.ID, &a.MergeRequestID, &a.CommitSHA, &sessionID, &worktree,
		&a.Status, &a.Content, &a.Error, &pid,
		&a.CreatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AICommitAnalysis{}, err
		}
		return AICommitAnalysis{}, fmt.Errorf("scan commit analysis: %w", err)
	}
	if sessionID.Valid {
		a.ClaudeSessionID = &sessionID.String
	}
	if worktree.Valid {
		a.WorktreePath = &worktree.String
	}
	if pid.Valid {
		v := int(pid.Int64)
		a.PID = &v
	}
	if startedAt.Valid {
		a.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		a.CompletedAt = &completedAt.Time
	}
	return a, nil
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
