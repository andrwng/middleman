package worktrees

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wesm/middleman/internal/gitclone"
)

// TreeRecursive lists every file (not directory) in the worktree at
// worktreePath, flattened to its full path from the root, either at sha or,
// when sha equals WorkingTreeSentinel, in the working tree on disk. Used by
// the code browser's fuzzy file finder, which needs the whole tree rather
// than one directory at a time.
func TreeRecursive(ctx context.Context, worktreePath, sha string) ([]gitclone.TreeEntry, error) {
	if sha == WorkingTreeSentinel {
		return treeRecursiveFromDisk(worktreePath)
	}
	return treeRecursiveFromRev(ctx, worktreePath, sha)
}

func treeRecursiveFromDisk(worktreePath string) ([]gitclone.TreeEntry, error) {
	entries := make([]gitclone.TreeEntry, 0)
	err := filepath.WalkDir(worktreePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(worktreePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		entries = append(entries, gitclone.TreeEntry{
			Name: d.Name(),
			Path: rel,
			Type: "file",
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func treeRecursiveFromRev(ctx context.Context, worktreePath, sha string) ([]gitclone.TreeEntry, error) {
	// Same rev addressing and stderr-based ErrNotFound mapping as
	// treeFromRev in tree.go, but with -r and no trailing :path -- see
	// gitclone.Manager.TreeRecursive for why ls-tree -r's <file> column
	// already carries the full path (fixed up into Name below) rather
	// than a bare basename.
	out, err := gitCmd(ctx, worktreePath, "ls-tree", "-r", sha)
	if err != nil {
		if isRevNotFoundError(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	entries, err := gitclone.ParseLsTree(out, "")
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if idx := strings.LastIndexByte(e.Path, '/'); idx >= 0 {
			entries[i].Name = e.Path[idx+1:]
		}
	}
	return entries, nil
}
