package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

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

// SetWorktreeSessionClaudeID stamps the claude_session_id on the
// row once we've parsed it from claude's first response. Returns
// silently if the row has been deleted or moved past active.
func (d *DB) SetWorktreeSessionClaudeID(
	ctx context.Context, id int64, claudeSessionID string,
) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_worktree_sessions
		    SET claude_session_id = ?,
		        last_activity_at  = ?
		  WHERE id = ?`,
		claudeSessionID, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("set claude session id: %w", err)
	}
	return nil
}

// MarkWorktreeSessionStatus moves a session to a terminal state
// (killed | closed). Idempotent.
func (d *DB) MarkWorktreesSessionStatus(
	ctx context.Context, id int64, status string,
) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_worktree_sessions
		    SET status = ?,
		        last_activity_at = ?
		  WHERE id = ?`,
		status, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("mark session %d %s: %w", id, status, err)
	}
	return nil
}

// AddWorktreeSessionTurn inserts a new turn and returns the
// hydrated row. Used for both user turns (review_feedback,
// user_message) inserted in 'done' state and claude_response turns
// inserted in 'queued' state.
func (d *DB) AddWorktreeSessionTurn(
	ctx context.Context, in NewWorktreeSessionTurn,
) (WorktreeSessionTurn, error) {
	now := time.Now().UTC()
	res, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_worktree_session_turns
		    (session_id, turn_type, content, raw_json,
		     status, error, pid, metadata_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.SessionID, in.TurnType, in.Content, in.RawJSON,
		in.Status, in.Error, in.PID, in.MetadataJSON, now,
	)
	if err != nil {
		return WorktreeSessionTurn{}, fmt.Errorf("add session turn: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return WorktreeSessionTurn{}, err
	}
	_, _ = d.rw.ExecContext(ctx,
		`UPDATE middleman_worktree_sessions
		    SET last_activity_at = ?
		  WHERE id = ?`,
		now, in.SessionID,
	)
	return d.GetWorktreeSessionTurn(ctx, id)
}

// NewWorktreeSessionTurn collects the writeable fields. The PID is
// optional — Claude-response turns set it once the subprocess
// starts; user turns leave it nil.
type NewWorktreeSessionTurn struct {
	SessionID    int64
	TurnType     string
	Content      string
	RawJSON      string
	Status       string
	Error        string
	PID          *int
	MetadataJSON string
}

// UpdateWorktreeSessionTurn is used by the runner to flip a
// claude_response from queued → running → done|failed|cancelled
// and to fill in content / raw_json / error when the result lands.
type UpdateWorktreeSessionTurn struct {
	Status   *string
	Content  *string
	RawJSON  *string
	Error    *string
	PID      *int
	ClearPID bool
}

// UpdateWorktreeSessionTurnFields applies the non-nil fields of u
// to the turn row. Used by the runner to advance state as the
// subprocess progresses.
func (d *DB) UpdateWorktreeSessionTurnFields(
	ctx context.Context, id int64, u UpdateWorktreeSessionTurn,
) error {
	sets := []string{}
	args := []any{}
	if u.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *u.Status)
	}
	if u.Content != nil {
		sets = append(sets, "content = ?")
		args = append(args, *u.Content)
	}
	if u.RawJSON != nil {
		sets = append(sets, "raw_json = ?")
		args = append(args, *u.RawJSON)
	}
	if u.Error != nil {
		sets = append(sets, "error = ?")
		args = append(args, *u.Error)
	}
	if u.ClearPID {
		sets = append(sets, "pid = NULL")
	} else if u.PID != nil {
		sets = append(sets, "pid = ?")
		args = append(args, *u.PID)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	query := "UPDATE middleman_worktree_session_turns SET " +
		joinCommas(sets) + " WHERE id = ?"
	if _, err := d.rw.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update session turn %d: %w", id, err)
	}
	return nil
}

func joinCommas(parts []string) string {
	var out strings.Builder
	for i, p := range parts {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(p)
	}
	return out.String()
}

// GetWorktreeSessionTurn returns one turn by id.
func (d *DB) GetWorktreeSessionTurn(
	ctx context.Context, id int64,
) (WorktreeSessionTurn, error) {
	row := d.ro.QueryRowContext(ctx,
		`SELECT id, session_id, turn_type, content, raw_json,
		        status, error, pid, metadata_json, created_at
		   FROM middleman_worktree_session_turns
		  WHERE id = ?`,
		id,
	)
	return scanWorktreeSessionTurn(row)
}

// WorktreeIDsWithRunningTurns returns the distinct set of worktree
// ids that currently have an active session with a claude_response
// turn in queued or running status. Used by the worktrees list
// endpoint to drive a sidebar indicator without requiring a separate
// roundtrip per worktree.
func (d *DB) WorktreeIDsWithRunningTurns(
	ctx context.Context,
) (map[int64]bool, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT DISTINCT s.worktree_id
		   FROM middleman_worktree_session_turns t
		   JOIN middleman_worktree_sessions s ON s.id = t.session_id
		  WHERE t.turn_type = 'claude_response'
		    AND t.status IN ('queued', 'running')
		    AND s.status = 'active'`,
	)
	if err != nil {
		return nil, fmt.Errorf("list worktrees with running turns: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan worktree id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ListRunningWorktreeSessionTurns returns every claude_response
// turn currently marked queued or running across all sessions.
// Used by the startup reconciler to mark orphaned subprocesses
// as failed — they didn't survive the middleman restart.
func (d *DB) ListRunningWorktreeSessionTurns(
	ctx context.Context,
) ([]WorktreeSessionTurn, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, session_id, turn_type, content, raw_json,
		        status, error, pid, metadata_json, created_at
		   FROM middleman_worktree_session_turns
		  WHERE turn_type = 'claude_response'
		    AND status IN ('queued', 'running')
		  ORDER BY id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list running session turns: %w", err)
	}
	defer rows.Close()
	var out []WorktreeSessionTurn
	for rows.Next() {
		t, err := scanWorktreeSessionTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListWorktreeSessionTurns returns all turns for a session in
// chronological order.
func (d *DB) ListWorktreeSessionTurns(
	ctx context.Context, sessionID int64,
) ([]WorktreeSessionTurn, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, session_id, turn_type, content, raw_json,
		        status, error, pid, metadata_json, created_at
		   FROM middleman_worktree_session_turns
		  WHERE session_id = ?
		  ORDER BY id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session turns: %w", err)
	}
	defer rows.Close()
	var out []WorktreeSessionTurn
	for rows.Next() {
		t, err := scanWorktreeSessionTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

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

func scanWorktreeSessionTurn(row rowScanner) (WorktreeSessionTurn, error) {
	var t WorktreeSessionTurn
	var pid sql.NullInt64
	err := row.Scan(
		&t.ID, &t.SessionID, &t.TurnType, &t.Content, &t.RawJSON,
		&t.Status, &t.Error, &pid, &t.MetadataJSON, &t.CreatedAt,
	)
	if err != nil {
		return WorktreeSessionTurn{}, fmt.Errorf("scan session turn: %w", err)
	}
	if pid.Valid {
		v := int(pid.Int64)
		t.PID = &v
	}
	return t, nil
}
