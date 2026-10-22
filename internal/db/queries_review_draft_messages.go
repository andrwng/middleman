package db

import (
	"context"
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
