package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CodeBrowserState is the last file path browsed in the code browser panel
// for one pull request, keyed by merge_request_id like PR notes.
type CodeBrowserState struct {
	MergeRequestID int64
	Path           string
	UpdatedAt      time.Time
}

func (d *DB) GetCodeBrowserState(ctx context.Context, mrID int64) (CodeBrowserState, error) {
	var s CodeBrowserState
	row := d.ro.QueryRowContext(ctx,
		`SELECT mr_id, path, updated_at FROM middleman_code_browser_state WHERE mr_id = ?`,
		mrID)
	err := row.Scan(&s.MergeRequestID, &s.Path, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CodeBrowserState{MergeRequestID: mrID}, nil
		}
		return CodeBrowserState{}, err
	}
	return s, nil
}

func (d *DB) SetCodeBrowserState(ctx context.Context, mrID int64, path string) (CodeBrowserState, error) {
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_code_browser_state (mr_id, path, updated_at)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(mr_id) DO UPDATE SET path = excluded.path, updated_at = excluded.updated_at`,
		mrID, path)
	if err != nil {
		return CodeBrowserState{}, err
	}
	return d.GetCodeBrowserState(ctx, mrID)
}
