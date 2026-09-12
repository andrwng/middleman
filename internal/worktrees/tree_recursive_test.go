package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeRecursive_ListsEveryFileFlattened(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	subdir := filepath.Join(dir, "internal")
	require.NoError(os.Mkdir(subdir, 0o755))
	require.NoError(os.WriteFile(filepath.Join(subdir, "handler.go"), []byte("package internal\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644))
	runGitT(t, dir, "add", ".")
	runGitT(t, dir, "commit", "-m", "add files")
	headSHA := gitHeadT(t, dir)

	for _, sha := range []string{WorkingTreeSentinel, headSHA} {
		entries, err := TreeRecursive(ctx, dir, sha)
		require.NoError(err)
		require.NotEmpty(entries)

		var foundNested bool
		for _, e := range entries {
			assert.Equal("file", e.Type, "TreeRecursive must never return a dir entry")
			if e.Path == "internal/handler.go" {
				foundNested = true
				assert.Equal("handler.go", e.Name)
			}
		}
		assert.True(foundNested, "should find internal/handler.go by its full path (sha=%s)", sha)
	}
}

func TestTreeRecursive_NonexistentSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	_, err := TreeRecursive(ctx, dir, "0000000000000000000000000000000000000000")
	require.Error(err)
	require.ErrorIs(err, ErrNotFound)
}
