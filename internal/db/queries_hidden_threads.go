package db

import (
	"context"
	"encoding/json"
	"time"
)

// HiddenReviewThread is one row in middleman_hidden_review_threads —
// a reviewer's intent to hide a review thread on a specific PR. The
// row is "active" only if no reply in the thread has a created_at
// after HiddenAt; see ActiveHiddenReviewThreadRoots.
type HiddenReviewThread struct {
	MergeRequestID        int64
	RootPlatformCommentID int64
	HiddenAt              time.Time
}

// UpsertHiddenReviewThread records (or re-records) the user's intent
// to hide the given thread. Re-hiding overwrites HiddenAt so a new
// reply that arrived between the two hides doesn't keep the thread
// visible.
func (d *DB) UpsertHiddenReviewThread(
	ctx context.Context,
	mrID, rootPlatformCommentID int64,
	hiddenAt time.Time,
) error {
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_hidden_review_threads
		     (merge_request_id, root_platform_comment_id, hidden_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(merge_request_id, root_platform_comment_id) DO UPDATE
		     SET hidden_at = excluded.hidden_at`,
		mrID, rootPlatformCommentID, hiddenAt.UTC(),
	)
	return err
}

// DeleteHiddenReviewThread clears the user's hide for a given thread.
// No-op when the row doesn't exist (DELETE on a missing row returns
// without error).
func (d *DB) DeleteHiddenReviewThread(
	ctx context.Context, mrID, rootPlatformCommentID int64,
) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_hidden_review_threads
		  WHERE merge_request_id = ? AND root_platform_comment_id = ?`,
		mrID, rootPlatformCommentID,
	)
	return err
}

// ListHiddenReviewThreads returns every stored row for an MR,
// including rows that may be superseded by a newer reply. Callers
// that need only currently-active hides should pass the result through
// ActiveHiddenReviewThreadRoots.
func (d *DB) ListHiddenReviewThreads(
	ctx context.Context, mrID int64,
) ([]HiddenReviewThread, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT merge_request_id, root_platform_comment_id, hidden_at
		   FROM middleman_hidden_review_threads
		  WHERE merge_request_id = ?`,
		mrID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HiddenReviewThread
	for rows.Next() {
		var h HiddenReviewThread
		if err := rows.Scan(
			&h.MergeRequestID, &h.RootPlatformCommentID, &h.HiddenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ActiveHiddenReviewThreadRoots returns the subset of stored
// hidden_review_threads rows for mrID whose hide is still in effect:
// no review_comment in the thread has created_at > hidden_at.
//
// The caller passes the pre-loaded events slice so this method doesn't
// re-query mr_events. The walk to root mirrors ResolveReviewCommentRootID
// but stays in memory.
func (d *DB) ActiveHiddenReviewThreadRoots(
	ctx context.Context, mrID int64, events []MREvent,
) ([]int64, error) {
	hides, err := d.ListHiddenReviewThreads(ctx, mrID)
	if err != nil {
		return nil, err
	}
	if len(hides) == 0 {
		return nil, nil
	}

	// Build platform_id → in_reply_to map and platform_id → created_at.
	parentByID := make(map[int64]int64, len(events))
	createdByID := make(map[int64]time.Time, len(events))
	for _, e := range events {
		if e.EventType != "review_comment" || e.PlatformID == nil {
			continue
		}
		pid := *e.PlatformID
		createdByID[pid] = e.CreatedAt
		var meta struct {
			InReplyTo int64 `json:"in_reply_to"`
		}
		if e.MetadataJSON != "" {
			// Ignore unmarshal errors — treat as root.
			_ = json.Unmarshal([]byte(e.MetadataJSON), &meta)
		}
		if meta.InReplyTo != 0 && meta.InReplyTo != pid {
			parentByID[pid] = meta.InReplyTo
		}
	}

	// Resolve every review_comment to its root (bounded chain walk).
	rootOf := func(pid int64) int64 {
		current := pid
		for range 32 {
			parent, ok := parentByID[current]
			if !ok {
				return current
			}
			current = parent
		}
		return current
	}

	// Compute max(created_at) per root.
	maxCreatedByRoot := make(map[int64]time.Time, len(events))
	for pid, t := range createdByID {
		root := rootOf(pid)
		if cur, ok := maxCreatedByRoot[root]; !ok || t.After(cur) {
			maxCreatedByRoot[root] = t
		}
	}

	out := make([]int64, 0, len(hides))
	for _, h := range hides {
		latest, ok := maxCreatedByRoot[h.RootPlatformCommentID]
		if ok && latest.After(h.HiddenAt) {
			continue // superseded by a newer reply
		}
		out = append(out, h.RootPlatformCommentID)
	}
	return out, nil
}

// HasReviewCommentOnMR returns true when (mrID, platformID) matches an
// existing review_comment event on this MR. Used by write paths that
// take a GitHub platform comment id as input — the cheap pre-write
// sanity check that the id refers to a real comment on the right PR.
func (d *DB) HasReviewCommentOnMR(
	ctx context.Context, mrID, platformID int64,
) (bool, error) {
	var count int
	err := d.ro.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_mr_events
		  WHERE merge_request_id = ? AND event_type = 'review_comment'
		        AND platform_id = ?`,
		mrID, platformID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
