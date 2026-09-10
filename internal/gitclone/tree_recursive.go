package gitclone

import (
	"context"
	"fmt"
	"strings"
)

// TreeRecursive lists every file (not directory) in the repo at sha,
// flattened to its full path from the root. Used by the code browser's
// fuzzy file finder, which needs the whole tree rather than one directory
// at a time.
func (m *Manager) TreeRecursive(ctx context.Context, host, owner, name, sha string) ([]TreeEntry, error) {
	dir := m.ClonePath(host, owner, name)

	// `ls-tree -r` with no trailing `:path` already returns every blob's
	// path relative to the repo root, so ParseLsTree needs no path prefix.
	// Its Name field, though, comes straight from `ls-tree`'s own <file>
	// column, which under -r IS the full path (e.g. "internal/handler.go")
	// rather than a bare basename -- fix that up below.
	out, err := m.git(ctx, host, dir, "ls-tree", "-r", sha)
	if err != nil {
		return nil, fmt.Errorf("ls-tree -r %s: %w", sha, err)
	}
	entries, err := ParseLsTree(out, "")
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
