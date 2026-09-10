package worktrees

import (
	"context"
	"fmt"
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
	return parseLsTree(out, path)
}

// isRevNotFoundError detects git rev/path not-found errors by string-matching
// stderr, mirroring the detection logic in worktrees.Blob's cat-file handling.
// Both forms are emitted in practice; the lowercase is the message for a path
// missing under a known SHA, the capitalized one for an unparseable object reference.
func isRevNotFoundError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "Not a valid object name")
}

// parseLsTree parses `git ls-tree` output, returning TreeEntry elements
// with Path fields adjusted to include the prefix directory when path != "".
// Format: <mode> SP <type> SP <sha>\t<name>
func parseLsTree(out []byte, path string) ([]gitclone.TreeEntry, error) {
	var entries []gitclone.TreeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// <mode> SP <type> SP <sha>\t<name>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			return nil, fmt.Errorf("worktrees: unexpected ls-tree line %q", line)
		}
		meta, entryName := line[:tabIdx], line[tabIdx+1:]
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			return nil, fmt.Errorf("worktrees: unexpected ls-tree metadata %q", meta)
		}
		entryType := "file"
		if fields[1] == "tree" {
			entryType = "dir"
		}
		entryPath := entryName
		if path != "" {
			entryPath = path + "/" + entryName
		}
		entries = append(entries, gitclone.TreeEntry{Name: entryName, Path: entryPath, Type: entryType})
	}
	return entries, nil
}
