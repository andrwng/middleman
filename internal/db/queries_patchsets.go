package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PRPatchset is one observed state of a PR's head. Each distinct
// head SHA that the sync has seen becomes a row; the number is a
// 1-based sequence per MR for the UI picker (PS1, PS2, ...).
type PRPatchset struct {
	ID             int64
	MergeRequestID int64
	Number         int
	HeadSHA        string
	BaseSHA        string
	MergeBaseSHA   string
	ObservedAt     time.Time
}

// RecordPatchsetOpts controls how RecordPatchset behaves on a
// repeat head SHA. BaseSHA and MergeBaseSHA overwrite whatever
// was stored previously (they may become known only after the
// first observation, once the clone has been updated).
type RecordPatchsetOpts struct {
	HeadSHA      string
	BaseSHA      string
	MergeBaseSHA string
	ObservedAt   time.Time
}

// RecordPatchset inserts a new patchset row for the given MR if
// the head SHA hasn't been seen yet. Returns the row id plus a
// bool that's true when a new row was inserted (so callers can
// fire "new patchset detected" side effects).
//
// Numbering is strictly monotonic per MR: MAX(number)+1. The
// UNIQUE(mr_id, head_sha) guard lets repeat syncs of the same
// head silently no-op.
func (d *DB) RecordPatchset(
	ctx context.Context, mrID int64, opts RecordPatchsetOpts,
) (int64, bool, error) {
	if opts.HeadSHA == "" {
		return 0, false, fmt.Errorf("patchset head_sha is required")
	}
	observedAt := opts.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	// Shortcut: if the head was already recorded, just refresh the
	// base/merge-base SHAs (they may have become knowable after
	// the clone caught up) and return the existing row.
	var existingID int64
	err := d.ro.QueryRowContext(ctx,
		`SELECT id FROM middleman_pr_patchsets
		  WHERE mr_id = ? AND head_sha = ?`,
		mrID, opts.HeadSHA,
	).Scan(&existingID)
	if err == nil {
		if _, err := d.rw.ExecContext(ctx,
			`UPDATE middleman_pr_patchsets
			    SET base_sha = COALESCE(NULLIF(?, ''), base_sha),
			        merge_base_sha = COALESCE(NULLIF(?, ''), merge_base_sha)
			  WHERE id = ?`,
			opts.BaseSHA, opts.MergeBaseSHA, existingID,
		); err != nil {
			return 0, false, fmt.Errorf("refresh patchset %d: %w", existingID, err)
		}
		return existingID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("lookup patchset: %w", err)
	}

	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var nextNumber int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1
		   FROM middleman_pr_patchsets WHERE mr_id = ?`,
		mrID,
	).Scan(&nextNumber); err != nil {
		return 0, false, fmt.Errorf("compute next patchset number: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_pr_patchsets
		    (mr_id, number, head_sha, base_sha, merge_base_sha, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(mr_id, head_sha) DO NOTHING`,
		mrID, nextNumber, opts.HeadSHA, opts.BaseSHA, opts.MergeBaseSHA, observedAt,
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert patchset: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	if id == 0 {
		// Another writer beat us to the punch; fetch the
		// existing row.
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM middleman_pr_patchsets
			  WHERE mr_id = ? AND head_sha = ?`,
			mrID, opts.HeadSHA,
		).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("re-lookup patchset: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
		return id, false, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// ListPatchsets returns every patchset recorded for the given MR
// ordered oldest-first by number. The UI renders them as a
// left-to-right chip strip (PS1, PS2, …).
func (d *DB) ListPatchsets(ctx context.Context, mrID int64) ([]PRPatchset, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, mr_id, number, head_sha, base_sha, merge_base_sha, observed_at
		   FROM middleman_pr_patchsets
		  WHERE mr_id = ?
		  ORDER BY number ASC`, mrID,
	)
	if err != nil {
		return nil, fmt.Errorf("list patchsets: %w", err)
	}
	defer rows.Close()

	var out []PRPatchset
	for rows.Next() {
		var p PRPatchset
		if err := rows.Scan(
			&p.ID, &p.MergeRequestID, &p.Number,
			&p.HeadSHA, &p.BaseSHA, &p.MergeBaseSHA, &p.ObservedAt,
		); err != nil {
			return nil, fmt.Errorf("scan patchset: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
