package worktrees

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound signals that the requested blob does not exist at the
// given SHA/path, or the working-tree file is missing from disk.
// Callers can distinguish this from generic git failures to return
// 404 rather than 502.
var ErrNotFound = errors.New("worktree blob not found")

// Blob returns the file content at the given SHA and path within
// the worktree. The special WorkingTreeSentinel SHA reads the file
// straight off disk so callers can fetch uncommitted content
// alongside historical revisions through one entry point.
func Blob(
	ctx context.Context, worktreePath, sha, path string,
) ([]byte, error) {
	if sha == WorkingTreeSentinel {
		full := filepath.Join(worktreePath, path)
		if rel, err := filepath.Rel(worktreePath, full); err != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
			}
			return nil, fmt.Errorf("read working-tree file %s: %w", path, err)
		}
		return raw, nil
	}
	out, err := gitCmd(ctx, worktreePath, "cat-file", "-p", sha+":"+path)
	if err != nil {
		// `git cat-file` doesn't expose a stable not-found exit code,
		// so we string-match its stderr. Both forms are emitted in
		// practice; the lowercase variant is the message for a path
		// missing under a known SHA, the capitalized one for an
		// unparseable object reference.
		msg := err.Error()
		if strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "Not a valid object name") {
			return nil, fmt.Errorf("%w: %s:%s", ErrNotFound, sha, path)
		}
		return nil, fmt.Errorf("cat-file %s:%s: %w", sha, path, err)
	}
	return out, nil
}

// BlobRange returns the 1-based inclusive line range [start, end]
// of the file at sha/path within the worktree. Mirrors
// gitclone.Manager.BlobRange semantics: ranges past EOF clamp
// silently rather than padding or erroring.
func BlobRange(
	ctx context.Context,
	worktreePath, sha, path string,
	start, end int,
) ([]string, error) {
	if start < 1 {
		start = 1
	}
	if end < start {
		return nil, fmt.Errorf("blob range: end (%d) < start (%d)", end, start)
	}
	raw, err := Blob(ctx, worktreePath, sha, path)
	if err != nil {
		return nil, err
	}
	text := string(bytes.TrimRight(raw, "\n"))
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
