package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ReviewDraft is one row in middleman_review_drafts: an inline review
// comment the reviewer (or Claude) has staged but not published.
// Branch is "" for GitHub PRs and the worktree branch for local
// reviews, mirroring middleman_review_threads.
type ReviewDraft struct {
	ID             string
	MergeRequestID int64
	Branch         string
	Path           string
	Side           string
	Line           int
	StartLine      *int
	CommitSHA      string
	InReplyTo      *int64
	Body           string
	Origin         string
	State          string
	RunID          *int64
	// ClaudeSessionID and ChatWorktreePath identify this draft's chat
	// session with Claude and the directory it must resume in.
	ClaudeSessionID  string
	ChatWorktreePath string
	SubmitError      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	BodyEditedAt     *time.Time
}

// NewReviewDraft is a reviewer-authored draft. ID is client-generated
// so optimistic UI writes and the one-time localStorage import are
// idempotent.
type NewReviewDraft struct {
	ID        string
	Path      string
	Side      string
	Line      int
	StartLine *int
	CommitSHA string
	InReplyTo *int64
	Body      string
	CreatedAt time.Time
}

// ReviewDraftMeta is the per-PR review summary and verdict.
type ReviewDraftMeta struct {
	Body      string
	Event     string
	UpdatedAt time.Time
}

// ReviewDraftCount is the number of publishable drafts on one PR.
type ReviewDraftCount struct {
	Owner  string
	Name   string
	Number int
	Count  int
}

const reviewDraftColumns = `id, merge_request_id, branch, path, side, line, start_line,
	commit_sha, in_reply_to, body, origin, state, submit_error,
	created_at, updated_at, body_edited_at, run_id, claude_session_id, chat_worktree_path`

func scanReviewDraft(sc interface{ Scan(...any) error }) (ReviewDraft, error) {
	var (
		r         ReviewDraft
		startLine sql.NullInt64
		inReplyTo sql.NullInt64
		editedAt  sql.NullTime
		runID     sql.NullInt64
	)
	err := sc.Scan(&r.ID, &r.MergeRequestID, &r.Branch, &r.Path, &r.Side, &r.Line,
		&startLine, &r.CommitSHA, &inReplyTo, &r.Body, &r.Origin, &r.State,
		&r.SubmitError, &r.CreatedAt, &r.UpdatedAt, &editedAt, &runID,
		&r.ClaudeSessionID, &r.ChatWorktreePath)
	if err != nil {
		return r, err
	}
	if startLine.Valid {
		v := int(startLine.Int64)
		r.StartLine = &v
	}
	if inReplyTo.Valid {
		v := inReplyTo.Int64
		r.InReplyTo = &v
	}
	if editedAt.Valid {
		v := editedAt.Time.UTC()
		r.BodyEditedAt = &v
	}
	if runID.Valid {
		v := runID.Int64
		r.RunID = &v
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r, nil
}

// InsertUserReviewDrafts inserts reviewer drafts in state 'draft'.
// Rows whose id already exists are left untouched (INSERT OR IGNORE),
// which makes retries and the localStorage import safe. Returns the
// stored rows for every requested id, in input order.
func (d *DB) InsertUserReviewDrafts(
	ctx context.Context, mrID int64, branch string, in []NewReviewDraft,
) ([]ReviewDraft, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	for _, n := range in {
		created := n.CreatedAt.UTC()
		if n.CreatedAt.IsZero() {
			created = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO middleman_review_drafts
			     (id, merge_request_id, branch, path, side, line, start_line,
			      commit_sha, in_reply_to, body, origin, state, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'user', 'draft', ?, ?)`,
			n.ID, mrID, branch, n.Path, n.Side, n.Line, n.StartLine,
			n.CommitSHA, n.InReplyTo, n.Body, created, now,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]ReviewDraft, 0, len(in))
	for _, n := range in {
		r, err := d.GetReviewDraft(ctx, mrID, branch, n.ID)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

// ListReviewDrafts returns non-discarded drafts for one PR/branch,
// oldest first.
func (d *DB) ListReviewDrafts(
	ctx context.Context, mrID int64, branch string,
) ([]ReviewDraft, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT `+reviewDraftColumns+`
		   FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND state != 'discarded'
		  ORDER BY created_at, id`,
		mrID, branch,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDraft{}
	for rows.Next() {
		r, err := scanReviewDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReviewDraft returns nil, nil when the id does not exist on this
// PR/branch. Scoping by mr+branch keeps one PR's route from reaching
// another PR's drafts. Reads from the write handle so callers see
// their own just-committed writes.
func (d *DB) GetReviewDraft(
	ctx context.Context, mrID int64, branch, id string,
) (*ReviewDraft, error) {
	row := d.rw.QueryRowContext(ctx,
		`SELECT `+reviewDraftColumns+`
		   FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`,
		mrID, branch, id,
	)
	r, err := scanReviewDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateReviewDraftBody records a reviewer edit and clears any stale
// submit error. Returns nil, nil when the draft does not exist on this
// PR/branch.
func (d *DB) UpdateReviewDraftBody(
	ctx context.Context, mrID int64, branch, id, body string,
) (*ReviewDraft, error) {
	now := time.Now().UTC()
	res, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts
		    SET body = ?, updated_at = ?, body_edited_at = ?, submit_error = ''
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`,
		body, now, now, mrID, branch, id,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return d.GetReviewDraft(ctx, mrID, branch, id)
}

// DeleteReviewDrafts hard-deletes the given ids on this PR/branch and
// returns how many rows went away. Unknown ids are ignored.
func (d *DB) DeleteReviewDrafts(
	ctx context.Context, mrID int64, branch string, ids []string,
) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, mrID, branch)
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ?
		    AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`,
		args...,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetReviewDraftSubmitError stores the GitHub error that blocked
// publishing this draft. Cleared by the next body edit.
func (d *DB) SetReviewDraftSubmitError(ctx context.Context, id, msg string) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts SET submit_error = ?, updated_at = ? WHERE id = ?`,
		msg, time.Now().UTC(), id,
	)
	return err
}

// GetReviewDraftMeta returns the zero value when nothing is stored.
func (d *DB) GetReviewDraftMeta(
	ctx context.Context, mrID int64, branch string,
) (ReviewDraftMeta, error) {
	var m ReviewDraftMeta
	err := d.ro.QueryRowContext(ctx,
		`SELECT body, event, updated_at FROM middleman_review_draft_meta
		  WHERE merge_request_id = ? AND branch = ?`,
		mrID, branch,
	).Scan(&m.Body, &m.Event, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDraftMeta{}, nil
	}
	m.UpdatedAt = m.UpdatedAt.UTC()
	return m, err
}

// UpsertReviewDraftMeta replaces the review summary and verdict.
func (d *DB) UpsertReviewDraftMeta(
	ctx context.Context, mrID int64, branch, body, event string,
) (ReviewDraftMeta, error) {
	now := time.Now().UTC()
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_review_draft_meta (merge_request_id, branch, body, event, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(merge_request_id, branch) DO UPDATE
		     SET body = excluded.body, event = excluded.event, updated_at = excluded.updated_at`,
		mrID, branch, body, event, now,
	)
	if err != nil {
		return ReviewDraftMeta{}, err
	}
	return ReviewDraftMeta{Body: body, Event: event, UpdatedAt: now}, nil
}

// DeleteReviewDraftMeta clears the summary and verdict after publish.
func (d *DB) DeleteReviewDraftMeta(ctx context.Context, mrID int64, branch string) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_review_draft_meta WHERE merge_request_id = ? AND branch = ?`,
		mrID, branch,
	)
	return err
}

// ListReviewDraftCounts returns publishable-draft counts per GitHub PR
// for the sidebar's "in review" chip. Local worktrees are excluded:
// the sidebar only lists GitHub PRs.
func (d *DB) ListReviewDraftCounts(ctx context.Context) ([]ReviewDraftCount, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT r.owner, r.name, mr.number, COUNT(*)
		   FROM middleman_review_drafts rd
		   JOIN middleman_merge_requests mr ON mr.id = rd.merge_request_id
		   JOIN middleman_repos r ON r.id = mr.repo_id
		  WHERE rd.state = 'draft' AND r.platform != 'local'
		  GROUP BY r.owner, r.name, mr.number`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDraftCount{}
	for rows.Next() {
		var c ReviewDraftCount
		if err := rows.Scan(&c.Owner, &c.Name, &c.Number, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var ErrInvalidDraftTransition = errors.New("invalid draft state transition")

// InsertClaudeSuggestion stores one finding from a review run as a
// suggestion. Suggestions are never published until the reviewer adds
// them to drafts.
func (d *DB) InsertClaudeSuggestion(
	ctx context.Context, mrID int64, branch string, runID int64, in NewReviewDraft,
) (ReviewDraft, error) {
	now := time.Now().UTC()
	if _, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_review_drafts
		     (id, merge_request_id, branch, path, side, line, start_line,
		      commit_sha, in_reply_to, body, origin, state, run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, 'claude', 'suggestion', ?, ?, ?)`,
		in.ID, mrID, branch, in.Path, in.Side, in.Line, in.StartLine,
		in.CommitSHA, in.Body, runID, now, now,
	); err != nil {
		return ReviewDraft{}, err
	}
	r, err := d.GetReviewDraft(ctx, mrID, branch, in.ID)
	if err != nil {
		return ReviewDraft{}, err
	}
	return *r, nil
}

// transitionReviewDraft moves a draft between states with a
// compare-and-set on the current state. Returns nil, nil when the
// draft does not exist on this PR/branch.
func (d *DB) transitionReviewDraft(
	ctx context.Context, mrID int64, branch, id, setSQL string, fromStates []string,
) (*ReviewDraft, error) {
	cur, err := d.GetReviewDraft(ctx, mrID, branch, id)
	if err != nil || cur == nil {
		return nil, err
	}
	allowed := false
	for _, s := range fromStates {
		if cur.State == s {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrInvalidDraftTransition
	}
	res, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_drafts SET `+setSQL+`, updated_at = ?
		  WHERE merge_request_id = ? AND branch = ? AND id = ? AND state = ?`,
		time.Now().UTC(), mrID, branch, id, cur.State,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrInvalidDraftTransition // raced with another transition
	}
	return d.GetReviewDraft(ctx, mrID, branch, id)
}

func (d *DB) AcceptReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id, `state = 'draft'`, []string{"suggestion"})
}

func (d *DB) DiscardReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id,
		`discarded_from = state, state = 'discarded'`, []string{"suggestion", "draft"})
}

func (d *DB) RestoreReviewDraft(ctx context.Context, mrID int64, branch, id string) (*ReviewDraft, error) {
	return d.transitionReviewDraft(ctx, mrID, branch, id,
		`state = CASE discarded_from WHEN 'draft' THEN 'draft' ELSE 'suggestion' END, discarded_from = ''`,
		[]string{"discarded"})
}
