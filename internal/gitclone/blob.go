package gitclone

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// BlobRange returns the 1-based line range [start, end] inclusive
// from the file at path as it existed at sha. Used by the diff
// context expander to show surrounding code outside the hunks.
//
// Callers should clamp start/end to the file length themselves
// when they know it; BlobRange silently returns fewer lines when
// the range extends past EOF (no padding, no error).
func (m *Manager) BlobRange(
	ctx context.Context,
	host, owner, name, sha, path string,
	start, end int,
) ([]string, error) {
	if start < 1 {
		start = 1
	}
	if end < start {
		return nil, fmt.Errorf("blob range: end (%d) < start (%d)", end, start)
	}

	dir := m.ClonePath(host, owner, name)
	// `git cat-file -p <sha>:<path>` prints the raw blob. Cheap for
	// our scale — files are small and we don't need the blob more
	// than a couple of times per review session, so no caching yet.
	out, err := m.git(ctx, host, dir, "cat-file", "-p", sha+":"+path)
	if err != nil {
		return nil, fmt.Errorf("cat-file %s:%s: %w", sha, path, err)
	}

	// Splitting on "\n" preserves empty trailing lines that would
	// be lost by bufio.Scanner, which matters when callers want
	// to render blob content that ends without a final newline.
	text := string(bytes.TrimRight(out, "\n"))
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")

	if start > len(lines) {
		return []string{}, nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start-1 : end], nil
}
