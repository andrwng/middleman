package worktrees

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

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
		if isIgnoredWorktree(s.Path) {
			continue
		}
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

var (
	ignoredMu    sync.RWMutex
	ignoredRoots []string
)

// IgnoreWorktreesUnder keeps Sync from treating worktrees under root as
// the reviewer's own. middleman adds detached checkouts there (Claude
// review runs and chats) to the reviewer's repo. Returns a func that
// undoes the registration (for tests).
func IgnoreWorktreesUnder(root string) func() {
	roots := []string{filepath.Clean(root)}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != roots[0] {
		roots = append(roots, real)
	}
	ignoredMu.Lock()
	ignoredRoots = append(ignoredRoots, roots...)
	ignoredMu.Unlock()
	return func() {
		ignoredMu.Lock()
		defer ignoredMu.Unlock()
		kept := ignoredRoots[:0]
		for _, r := range ignoredRoots {
			if r != roots[0] && (len(roots) < 2 || r != roots[1]) {
				kept = append(kept, r)
			}
		}
		ignoredRoots = kept
	}
}

func isIgnoredWorktree(path string) bool {
	p := filepath.Clean(path)
	ignoredMu.RLock()
	defer ignoredMu.RUnlock()
	for _, root := range ignoredRoots {
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
