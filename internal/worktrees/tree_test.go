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

func TestTree_Root(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	// Add some files to the repo
	require.NoError(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644))
	runGitT(t, dir, "add", ".")
	runGitT(t, dir, "commit", "-m", "add files")

	headSHA := gitHeadT(t, dir)

	// Test with WorkingTreeSentinel (uncommitted state)
	entries, err := Tree(ctx, dir, WorkingTreeSentinel, "")
	require.NoError(err)
	assert.NotEmpty(entries)
	for _, e := range entries {
		assert.NotEmpty(e.Name)
		assert.Contains([]string{"dir", "file"}, e.Type)
	}

	// Test with a real SHA
	entries2, err := Tree(ctx, dir, headSHA, "")
	require.NoError(err)
	assert.NotEmpty(entries2)
	for _, e := range entries2 {
		assert.NotEmpty(e.Name)
		assert.Contains([]string{"dir", "file"}, e.Type)
	}
}

func TestTree_Subdirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")
	headSHA := gitHeadT(t, dir)

	// Create a subdirectory structure
	subdir := filepath.Join(dir, "subdir")
	require.NoError(os.Mkdir(subdir, 0o755))
	require.NoError(os.WriteFile(filepath.Join(subdir, "file1.txt"), []byte("content1\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(subdir, "file2.txt"), []byte("content2\n"), 0o644))
	runGitT(t, dir, "add", "subdir/")
	runGitT(t, dir, "commit", "-m", "add subdir")
	headSHA = gitHeadT(t, dir)

	// Test with WorkingTreeSentinel
	entries, err := Tree(ctx, dir, WorkingTreeSentinel, "subdir")
	require.NoError(err)
	require.NotEmpty(entries)

	// Verify entries have correct Path prefix
	for _, e := range entries {
		assert.NotEmpty(e.Name)
		assert.Contains([]string{"dir", "file"}, e.Type)
		assert.True(filepath.HasPrefix(e.Path, "subdir/"), "path should be prefixed with directory name")
	}

	// Test with a real SHA
	entries2, err := Tree(ctx, dir, headSHA, "subdir")
	require.NoError(err)
	require.NotEmpty(entries2)
}

func TestTree_NonexistentPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	// Test with WorkingTreeSentinel
	_, err := Tree(ctx, dir, WorkingTreeSentinel, "does/not/exist")
	require.Error(err)
	require.True(errors.Is(err, ErrNotFound))

	// Test with a real SHA
	headSHA := gitHeadT(t, dir)
	_, err = Tree(ctx, dir, headSHA, "does/not/exist")
	require.Error(err)
	require.True(errors.Is(err, ErrNotFound))
}
