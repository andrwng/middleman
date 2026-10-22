package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ReviewDraftMessage is one entry in a draft's conversation with
// Claude: a message from either side, or a revision Claude made to the
// draft's body.
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

var ErrDraftNotFound = errors.New("draft not found")

const draftMessageColumns = `id, draft_id, author, kind, body, prev_body, applied, status,
	raw_json, error, created_at, started_at, completed_at`

func scanReviewDraftMessage(sc interface{ Scan(...any) error }) (ReviewDraftMessage, error) {
	var (
		m         ReviewDraftMessage
		started   sql.NullTime
		completed sql.NullTime
	)
	err := sc.Scan(&m.ID, &m.DraftID, &m.Author, &m.Kind, &m.Body, &m.PrevBody, &m.Applied,
		&m.Status, &m.RawJSON, &m.Error, &m.CreatedAt, &started, &completed)
	if err != nil {
		return m, err
	}
	m.CreatedAt = m.CreatedAt.UTC()
	if started.Valid {
		v := started.Time.UTC()
		m.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time.UTC()
		m.CompletedAt = &v
	}
	return m, nil
}

func (d *DB) queryDraftMessages(ctx context.Context, where string, args ...any) ([]ReviewDraftMessage, error) {
	rows, err := d.rw.QueryContext(ctx,
		`SELECT `+draftMessageColumns+` FROM middleman_review_draft_messages WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDraftMessage{}
	for rows.Next() {
		m, err := scanReviewDraftMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListReviewDraftMessages returns a draft's conversation, oldest first.
func (d *DB) ListReviewDraftMessages(ctx context.Context, draftID string) ([]ReviewDraftMessage, error) {
	return d.queryDraftMessages(ctx, `draft_id = ? ORDER BY id`, draftID)
}

func (d *DB) GetReviewDraftMessage(ctx context.Context, id int64) (*ReviewDraftMessage, error) {
	m, err := scanReviewDraftMessage(d.rw.QueryRowContext(ctx,
		`SELECT `+draftMessageColumns+` FROM middleman_review_draft_messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (d *DB) MarkDraftMessageRunning(ctx context.Context, id int64, pid int) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages SET status = 'running', pid = ?, started_at = ?
		  WHERE id = ? AND status = 'queued'`, pid, time.Now().UTC(), id)
	return err
}

func (d *DB) UpdateDraftMessageStream(ctx context.Context, id int64, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages SET raw_json = ? WHERE id = ?`, rawJSON, id)
	return err
}

func (d *DB) MarkDraftMessageDone(ctx context.Context, id int64, body, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages
		    SET status = 'done', body = ?, raw_json = ?, pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`, body, rawJSON, time.Now().UTC(), id)
	return err
}

// MarkDraftMessageFailed records a failed turn. An empty rawJSON keeps
// the stream already stored.
func (d *DB) MarkDraftMessageFailed(ctx context.Context, id int64, msg, rawJSON string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages
		    SET status = 'failed', error = ?,
		        raw_json = CASE WHEN ? = '' THEN raw_json ELSE ? END,
		        pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`,
		msg, rawJSON, rawJSON, time.Now().UTC(), id)
	return err
}

func (d *DB) MarkDraftMessageCancelled(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages SET status = 'cancelled', pid = NULL, completed_at = ?
		  WHERE id = ? AND status IN ('queued', 'running')`, time.Now().UTC(), id)
	return err
}

// RunningDraftMessage returns the Claude turn currently running for a
// draft, or nil.
func (d *DB) RunningDraftMessage(ctx context.Context, draftID string) (*ReviewDraftMessage, error) {
	ms, err := d.queryDraftMessages(ctx,
		`draft_id = ? AND author = 'claude' AND status = 'running' ORDER BY id DESC LIMIT 1`, draftID)
	if err != nil || len(ms) == 0 {
		return nil, err
	}
	return &ms[0], nil
}

func (d *DB) ListActiveDraftMessages(ctx context.Context) ([]ReviewDraftMessage, error) {
	return d.queryDraftMessages(ctx, `status IN ('queued', 'running') ORDER BY id`)
}

// LastCompletedClaudeMessageAt is when Claude last finished replying in
// a draft's conversation, or nil if it never has.
func (d *DB) LastCompletedClaudeMessageAt(ctx context.Context, draftID string) (*time.Time, error) {
	// ORDER BY ... LIMIT 1 rather than MAX(): SQLite aggregates drop the
	// column's DATETIME type, so the driver would hand back a string.
	var at sql.NullTime
	err := d.rw.QueryRowContext(ctx,
		`SELECT completed_at FROM middleman_review_draft_messages
		  WHERE draft_id = ? AND author = 'claude' AND kind = 'message' AND status = 'done'
		  ORDER BY completed_at DESC LIMIT 1`, draftID,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !at.Valid {
		return nil, err
	}
	v := at.Time.UTC()
	return &v, nil
}

// SetReviewDraftChatSession stores the Claude session a draft's chat
// continues, and the directory it has to resume in. Empty values clear it.
func (d *DB) SetReviewDraftChatSession(ctx context.Context, draftID, sessionID, worktreePath string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts SET claude_session_id = ?, chat_worktree_path = ? WHERE id = ?`,
		sessionID, worktreePath, draftID)
	return err
}

// ApplyClaudeRevision records a revision Claude made during a chat turn.
// It is applied to the draft only while the draft is still a suggestion
// and the reviewer has not edited it since the turn started; otherwise
// it is stored for the reviewer to Apply. Text the reviewer accepted
// only changes by the reviewer's hand.
func (d *DB) ApplyClaudeRevision(
	ctx context.Context, mrID int64, branch, draftID, newBody string, turnStartedAt time.Time,
) (ReviewDraftMessage, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		state, body string
		editedAt    sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT state, body, body_edited_at FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`, mrID, branch, draftID,
	).Scan(&state, &body, &editedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDraftMessage{}, ErrDraftNotFound
	}
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	if state == "discarded" {
		return ReviewDraftMessage{}, ErrInvalidDraftTransition
	}
	applied := state == "suggestion" && (!editedAt.Valid || editedAt.Time.Before(turnStartedAt))
	now := time.Now().UTC()
	if applied {
		if _, err := tx.ExecContext(ctx,
			`UPDATE middleman_review_drafts SET body = ?, updated_at = ? WHERE id = ?`,
			newBody, now, draftID); err != nil {
			return ReviewDraftMessage{}, err
		}
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_review_draft_messages
		     (draft_id, author, kind, body, prev_body, applied, status, created_at, completed_at)
		 VALUES (?, 'claude', 'revision', ?, ?, ?, 'done', ?, ?)`,
		draftID, newBody, body, applied, now, now)
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReviewDraftMessage{}, err
	}
	return ReviewDraftMessage{ID: id, DraftID: draftID, Author: "claude", Kind: "revision",
		Body: newBody, PrevBody: body, Applied: applied, Status: "done", CreatedAt: now}, nil
}

// setRevisionApplied applies (apply=true) or reverts a revision message
// on behalf of the reviewer. Both count as reviewer edits.
func (d *DB) setRevisionApplied(
	ctx context.Context, mrID int64, branch string, msgID int64, apply bool,
) (*ReviewDraft, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		draftID, kind, body, prevBody string
		applied                       bool
	)
	err = tx.QueryRowContext(ctx,
		`SELECT draft_id, kind, body, prev_body, applied FROM middleman_review_draft_messages WHERE id = ?`, msgID,
	).Scan(&draftID, &kind, &body, &prevBody, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if kind != "revision" || applied == apply {
		return nil, ErrInvalidDraftTransition
	}
	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM middleman_review_drafts WHERE merge_request_id = ? AND branch = ? AND id = ?`,
		mrID, branch, draftID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if state == "discarded" {
		return nil, ErrInvalidDraftTransition
	}
	text := prevBody
	if apply {
		text = body
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE middleman_review_drafts
		    SET body = ?, body_edited_at = ?, updated_at = ?, submit_error = '' WHERE id = ?`,
		text, now, now, draftID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE middleman_review_draft_messages SET applied = ? WHERE id = ?`, apply, msgID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetReviewDraft(ctx, mrID, branch, draftID)
}

func (d *DB) ApplyRevisionMessage(ctx context.Context, mrID int64, branch string, msgID int64) (*ReviewDraft, error) {
	return d.setRevisionApplied(ctx, mrID, branch, msgID, true)
}

func (d *DB) RevertRevisionMessage(ctx context.Context, mrID int64, branch string, msgID int64) (*ReviewDraft, error) {
	return d.setRevisionApplied(ctx, mrID, branch, msgID, false)
}
