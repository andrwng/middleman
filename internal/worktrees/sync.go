package worktrees

import (
	"context"
	"fmt"

	"github.com/wesm/middleman/internal/db"
)

// Sync runs one reconciliation cycle for a single repo: scans its
// local_path with `git worktree list --porcelain`, upserts every
// observed worktree, and marks any previously-observed worktrees no
// longer present as removed.
//
// repoID is the row id of the repo in middleman_repos. repoPath is
// the configured local_path (already validated as absolute).
//
// Returns the live worktrees after sync for callers that want to
// fan out further work per worktree.
func Sync(
	ctx context.Context, store *db.DB, repoID int64, repoPath string,
) ([]db.Worktree, error) {
	scanned, err := Scan(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("scan worktrees: %w", err)
	}

	keepPaths := make([]string, 0, len(scanned))
	out := make([]db.Worktree, 0, len(scanned))
	for _, s := range scanned {
		w, err := store.UpsertWorktree(ctx, repoID, s)
		if err != nil {
			return nil, fmt.Errorf("upsert worktree %s: %w", s.Path, err)
		}
		keepPaths = append(keepPaths, s.Path)
		out = append(out, w)
	}

	if err := store.MarkWorktreesNotInSet(ctx, repoID, keepPaths); err != nil {
		return nil, fmt.Errorf("sweep stale worktrees: %w", err)
	}
	return out, nil
}
