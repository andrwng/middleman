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
		return nil, err
	}

	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// <mode> SP <type> SP <sha>\t<name>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			return nil, fmt.Errorf("gitclone: unexpected ls-tree line %q", line)
		}
		meta, entryName := line[:tabIdx], line[tabIdx+1:]
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
