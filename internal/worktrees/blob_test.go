package worktrees

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMinimalRepo creates a git repo at dir with one committed file
// so that cat-file operations have a valid HEAD object to work against.
func setupMinimalRepo(t *testing.T, dir string) {
	t.Helper()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644))
	runGitT(t, dir, "add", "hello.txt")
	runGitT(t, dir, "commit", "-m", "init")
}

func TestBlobPathTraversalRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	root := t.TempDir()
	worktreeDir := filepath.Join(root, "repo")
	require.NoError(os.MkdirAll(worktreeDir, 0o755))
	setupMinimalRepo(t, worktreeDir)

	// Write a secret file OUTSIDE the worktree.
	secretFile := filepath.Join(root, "secret.txt")
	require.NoError(os.WriteFile(secretFile, []byte("secret-content\n"), 0o644))

	// dot-dot traversal is rejected.
	got, err := Blob(ctx, worktreeDir, WorkingTreeSentinel, "../secret.txt")
	require.Error(err, "expected error for path traversal")
	assert.True(errors.Is(err, ErrNotFound), "error should wrap ErrNotFound, got: %v", err)
	assert.Nil(got, "should return no content for traversal path")
	assert.NotContains(string(got), "secret-content")

	// absolute path is also rejected (filepath.Join strips it to relative,
	// but a caller passing an absolute path that falls outside must still fail).
	got, err = Blob(ctx, worktreeDir, WorkingTreeSentinel, secretFile)
	require.Error(err, "expected error for absolute path outside worktree")
	assert.True(errors.Is(err, ErrNotFound), "error should wrap ErrNotFound, got: %v", err)
	assert.Nil(got)

	// Normal in-tree read still works (regression guard).
	got, err = Blob(ctx, worktreeDir, WorkingTreeSentinel, "hello.txt")
	require.NoError(err)
	assert.Equal("hello\n", string(got))
}

func TestBlobRangePathTraversalRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	root := t.TempDir()
	worktreeDir := filepath.Join(root, "repo")
	require.NoError(os.MkdirAll(worktreeDir, 0o755))
	setupMinimalRepo(t, worktreeDir)

	secretFile := filepath.Join(root, "secret.txt")
	require.NoError(os.WriteFile(secretFile, []byte("line1\nline2\n"), 0o644))

	// BlobRange routes through Blob — traversal must also be rejected.
	lines, err := BlobRange(ctx, worktreeDir, WorkingTreeSentinel, "../secret.txt", 1, 2)
	require.Error(err)
	assert.True(errors.Is(err, ErrNotFound))
	assert.Nil(lines)

	// Normal in-tree range still works.
	lines, err = BlobRange(ctx, worktreeDir, WorkingTreeSentinel, "hello.txt", 1, 1)
	require.NoError(err)
	assert.Equal([]string{"hello"}, lines)
}
