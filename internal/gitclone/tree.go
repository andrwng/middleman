package gitclone

import (
	"context"
	"fmt"
	"strings"
)

// TreeEntry is one file or directory listed by Tree.
type TreeEntry struct {
	Name string
	Path string
	Type string // "dir" or "file"
}

// Tree lists the immediate entries of path (empty string for repo root)
// as they existed at sha, without recursing into subdirectories.
func (m *Manager) Tree(ctx context.Context, host, owner, name, sha, path string) ([]TreeEntry, error) {
	dir := m.ClonePath(host, owner, name)
	rev := sha + ":" + path

	out, err := m.git(ctx, host, dir, "ls-tree", rev)
	if err != nil {
		return nil, fmt.Errorf("ls-tree %s:%s: %w", sha, path, err)
	}

	return ParseLsTree(out, path)
}

// ParseLsTree parses `git ls-tree` output into TreeEntry elements.
// The path parameter adjusts entry Path fields to include the prefix directory.
// Format: <mode> SP <type> SP <sha>\t<name>
func ParseLsTree(out []byte, path string) ([]TreeEntry, error) {
	entries := make([]TreeEntry, 0) // non-nil empty slice
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// <mode> SP <type> SP <sha>\t<name>
		meta, entryName, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("gitclone: unexpected ls-tree line %q", line)
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			return nil, fmt.Errorf("gitclone: unexpected ls-tree metadata %q", meta)
		}
		entryType := "file"
		if fields[1] == "tree" {
			entryType = "dir"
		}
		entryPath := entryName
		if path != "" {
			entryPath = path + "/" + entryName
		}
		entries = append(entries, TreeEntry{Name: entryName, Path: entryPath, Type: entryType})
	}
	return entries, nil
}
