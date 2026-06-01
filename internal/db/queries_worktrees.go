package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ScannedWorktree is the per-row payload the scanner hands to
// UpsertWorktree — the live state observed by `git worktree list`
// at scan time. ID is not part of it; the DB layer owns identity.
type ScannedWorktree struct {
	Path       string
	Branch     string
	HeadSHA    string
	IsDetached bool
	IsLocked   bool
	IsPrunable bool
}

// UpsertWorktree inserts a worktree row for (repoID, path) if it
// isn't present, or refreshes branch/head/state and bumps
// last_seen_at if it is. A previously-removed row at the same path
// is revived (removed_at cleared) so any data hung off the row
// survives a temporary disappearance.
func (d *DB) UpsertWorktree(
	ctx context.Context, repoID int64, w ScannedWorktree,
) (Worktree, error) {
	if w.Path == "" {
		return Worktree{}, fmt.Errorf("worktree path is required")
	}
	now := time.Now().UTC()

	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_worktrees
		    (repo_id, path, branch, head_sha,
		     is_detached, is_locked, is_prunable,
		     discovered_at, last_seen_at, removed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT(repo_id, path) DO UPDATE SET
		     branch       = excluded.branch,
		     head_sha     = excluded.head_sha,
		     is_detached  = excluded.is_detached,
		     is_locked    = excluded.is_locked,
		     is_prunable  = excluded.is_prunable,
		     last_seen_at = excluded.last_seen_at,
		     removed_at   = NULL`,
		repoID, w.Path, w.Branch, w.HeadSHA,
		boolToInt(w.IsDetached), boolToInt(w.IsLocked), boolToInt(w.IsPrunable),
		now, now,
	)
	if err != nil {
		return Worktree{}, fmt.Errorf("upsert worktree %s: %w", w.Path, err)
	}

	got, err := d.getWorktreeByRepoPath(ctx, repoID, w.Path)
	if err != nil {
		return Worktree{}, err
	}
	return got, nil
}

// MarkWorktreesNotInSet sets removed_at = now on every active
// worktree for repoID whose path is NOT in keepPaths. Already-removed
// rows are left alone (removed_at not bumped).
func (d *DB) MarkWorktreesNotInSet(
	ctx context.Context, repoID int64, keepPaths []string,
) error {
	now := time.Now().UTC()
	if len(keepPaths) == 0 {
		_, err := d.rw.ExecContext(ctx,
			`UPDATE middleman_worktrees
			    SET removed_at = ?
			  WHERE repo_id = ? AND removed_at IS NULL`,
			now, repoID,
		)
		if err != nil {
			return fmt.Errorf("mark all worktrees removed for repo %d: %w", repoID, err)
		}
		return nil
	}

	placeholders := strings.Repeat("?,", len(keepPaths))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(keepPaths)+2)
	args = append(args, now, repoID)
	for _, p := range keepPaths {
		args = append(args, p)
	}

	query := `UPDATE middleman_worktrees
	             SET removed_at = ?
	           WHERE repo_id = ? AND removed_at IS NULL
	             AND path NOT IN (` + placeholders + `)`
	if _, err := d.rw.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("mark stale worktrees for repo %d: %w", repoID, err)
	}
	return nil
}

// ListWorktreesForRepo returns the active (not removed) worktrees
// for one repo, ordered by path for stable rendering.
func (d *DB) ListWorktreesForRepo(
	ctx context.Context, repoID int64,
) ([]Worktree, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT id, repo_id, path, branch, head_sha,
		        is_detached, is_locked, is_prunable,
		        discovered_at, last_seen_at, removed_at
		   FROM middleman_worktrees
		  WHERE repo_id = ? AND removed_at IS NULL
		  ORDER BY path`,
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("list worktrees for repo %d: %w", repoID, err)
	}
	defer rows.Close()

	var out []Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListAllActiveWorktrees joins worktrees with their repos for surfaces
// that span the whole instance (the Open sidebar, the list API).
func (d *DB) ListAllActiveWorktrees(
	ctx context.Context,
) ([]WorktreeWithRepo, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT w.id, w.repo_id, w.path, w.branch, w.head_sha,
		        w.is_detached, w.is_locked, w.is_prunable,
		        w.discovered_at, w.last_seen_at, w.removed_at,
		        r.owner, r.name
		   FROM middleman_worktrees w
		   JOIN middleman_repos r ON r.id = w.repo_id
		  WHERE w.removed_at IS NULL
		  ORDER BY r.owner, r.name, w.path`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all active worktrees: %w", err)
	}
	defer rows.Close()

	var out []WorktreeWithRepo
	for rows.Next() {
		var wr WorktreeWithRepo
		var removedAt sql.NullTime
		var isDetached, isLocked, isPrunable int
		if err := rows.Scan(
			&wr.ID, &wr.RepoID, &wr.Path, &wr.Branch, &wr.HeadSHA,
			&isDetached, &isLocked, &isPrunable,
			&wr.DiscoveredAt, &wr.LastSeenAt, &removedAt,
			&wr.RepoOwner, &wr.RepoName,
		); err != nil {
			return nil, fmt.Errorf("scan worktree with repo: %w", err)
		}
		wr.IsDetached = isDetached != 0
		wr.IsLocked = isLocked != 0
		wr.IsPrunable = isPrunable != 0
		if removedAt.Valid {
			t := removedAt.Time
			wr.RemovedAt = &t
		}
		out = append(out, wr)
	}
	return out, rows.Err()
}

// GetWorktreeByID returns a single worktree by row id, regardless
// of removed_at. Callers that want to reject removed worktrees can
// check the result.
func (d *DB) GetWorktreeByID(ctx context.Context, id int64) (Worktree, error) {
	row := d.ro.QueryRowContext(ctx,
		`SELECT id, repo_id, path, branch, head_sha,
		        is_detached, is_locked, is_prunable,
		        discovered_at, last_seen_at, removed_at
		   FROM middleman_worktrees
		  WHERE id = ?`,
		id,
	)
	w, err := scanWorktree(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Worktree{}, fmt.Errorf("worktree %d: %w", id, err)
	}
	return w, err
}

func (d *DB) getWorktreeByRepoPath(
	ctx context.Context, repoID int64, path string,
) (Worktree, error) {
	row := d.ro.QueryRowContext(ctx,
		`SELECT id, repo_id, path, branch, head_sha,
		        is_detached, is_locked, is_prunable,
		        discovered_at, last_seen_at, removed_at
		   FROM middleman_worktrees
		  WHERE repo_id = ? AND path = ?`,
		repoID, path,
	)
	w, err := scanWorktree(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Worktree{}, fmt.Errorf("worktree %s in repo %d: %w", path, repoID, err)
	}
	return w, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorktree(row rowScanner) (Worktree, error) {
	var w Worktree
	var removedAt sql.NullTime
	var isDetached, isLocked, isPrunable int
	if err := row.Scan(
		&w.ID, &w.RepoID, &w.Path, &w.Branch, &w.HeadSHA,
		&isDetached, &isLocked, &isPrunable,
		&w.DiscoveredAt, &w.LastSeenAt, &removedAt,
	); err != nil {
		return Worktree{}, err
	}
	w.IsDetached = isDetached != 0
	w.IsLocked = isLocked != 0
	w.IsPrunable = isPrunable != 0
	if removedAt.Valid {
		t := removedAt.Time
		w.RemovedAt = &t
	}
	return w, nil
}

// GetActiveWorktreeByPath finds an active (removed_at IS NULL) worktree
// by exact path, joined with its repo for the parent name. Returns
// sql.ErrNoRows when no active worktree matches.
func (d *DB) GetActiveWorktreeByPath(ctx context.Context, path string) (WorktreeWithRepo, error) {
	row := d.ro.QueryRowContext(ctx,
		`SELECT w.id, w.repo_id, w.path, w.branch, w.head_sha,
		        w.is_detached, w.is_locked, w.is_prunable,
		        w.discovered_at, w.last_seen_at, w.removed_at,
		        r.owner, r.name
		   FROM middleman_worktrees w
		   JOIN middleman_repos r ON r.id = w.repo_id
		  WHERE w.path = ? AND w.removed_at IS NULL
		  LIMIT 1`,
		path,
	)
	var wr WorktreeWithRepo
	var removedAt sql.NullTime
	var isDetached, isLocked, isPrunable int
	if err := row.Scan(
		&wr.ID, &wr.RepoID, &wr.Path, &wr.Branch, &wr.HeadSHA,
		&isDetached, &isLocked, &isPrunable,
		&wr.DiscoveredAt, &wr.LastSeenAt, &removedAt,
		&wr.RepoOwner, &wr.RepoName,
	); err != nil {
		return WorktreeWithRepo{}, err
	}
	wr.IsDetached = isDetached != 0
	wr.IsLocked = isLocked != 0
	wr.IsPrunable = isPrunable != 0
	if removedAt.Valid {
		t := removedAt.Time
		wr.RemovedAt = &t
	}
	return wr, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
