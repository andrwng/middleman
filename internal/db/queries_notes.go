package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PRNotes is a reviewer-local scratchpad attached to a single PR. The
// content is opaque text (we don't render markdown server-side); the
// UI drives a debounced PUT every few seconds while the user types.
type PRNotes struct {
	MergeRequestID int64
	Content        string
	UpdatedAt      time.Time
}

// GetPRNotes returns the notes row for the given MR, or an empty
// PRNotes value (with zero UpdatedAt) when no row exists. The
// empty-default keeps callers from branching on sql.ErrNoRows for
// what is really a cold-start UX — "you haven't typed anything yet."
func (d *DB) GetPRNotes(ctx context.Context, mrID int64) (PRNotes, error) {
	var n PRNotes
	n.MergeRequestID = mrID
	err := d.ro.QueryRowContext(ctx,
		`SELECT mr_id, content, updated_at
		   FROM middleman_pr_notes WHERE mr_id = ?`, mrID,
	).Scan(&n.MergeRequestID, &n.Content, &n.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PRNotes{MergeRequestID: mrID}, nil
		}
		return PRNotes{}, err
	}
	return n, nil
}

// UpsertPRNotes writes content for the MR, bumping updated_at. Empty
// content is preserved — the UI may want to distinguish "never
// touched" (no row) from "cleared deliberately" (row with "").
func (d *DB) UpsertPRNotes(ctx context.Context, mrID int64, content string) (PRNotes, error) {
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_pr_notes (mr_id, content, updated_at)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(mr_id) DO UPDATE SET
		     content = excluded.content,
		     updated_at = excluded.updated_at`,
		mrID, content,
	)
	if err != nil {
		return PRNotes{}, err
	}
	return d.GetPRNotes(ctx, mrID)
}
