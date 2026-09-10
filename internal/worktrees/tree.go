package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wesm/middleman/internal/gitclone"
)

// Tree lists the immediate entries of path (empty string for repo root)
// in the worktree at worktreePath, either at sha or, when sha equals
// WorkingTreeSentinel, in the working tree on disk.
func Tree(ctx context.Context, worktreePath, sha, path string) ([]gitclone.TreeEntry, error) {
	if sha == WorkingTreeSentinel {
		return treeFromDisk(worktreePath, path)
	}
	return treeFromRev(ctx, worktreePath, sha, path)
}

func treeFromDisk(worktreePath, path string) ([]gitclone.TreeEntry, error) {
	full := filepath.Join(worktreePath, path)
	rel, err := filepath.Rel(worktreePath, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, ErrNotFound
	}

	dirEntries, err := os.ReadDir(full)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	entries := make([]gitclone.TreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Name() == ".git" {
			continue
		}
		entryType := "file"
		if de.IsDir() {
			entryType = "dir"
		}
		entryPath := de.Name()
		if path != "" {
			entryPath = path + "/" + de.Name()
		}
		entries = append(entries, gitclone.TreeEntry{Name: de.Name(), Path: entryPath, Type: entryType})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func treeFromRev(ctx context.Context, worktreePath, sha, path string) ([]gitclone.TreeEntry, error) {
	// Mirror gitclone.Manager.Tree's ls-tree parsing against this worktree
	// via gitCmd instead of Manager.git — same rev:path addressing, same
	// stderr-based ErrNotFound mapping worktrees.Blob already uses for
	// "does not exist"/"Not a valid object name".
	out, err := gitCmd(ctx, worktreePath, "ls-tree", sha+":"+path)
	if err != nil {
		if isRevNotFoundError(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return gitclone.ParseLsTree(out, path)
}

// isRevNotFoundError detects git rev/path not-found errors by string-matching
// stderr, mirroring the detection logic in worktrees.Blob's cat-file handling.
// Both forms are emitted in practice; the lowercase is the message for a path
// missing under a known SHA, the capitalized one for an unparseable object reference.
func isRevNotFoundError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "Not a valid object name") ||
		strings.Contains(msg, "not a tree object")
}

