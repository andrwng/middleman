package gitclone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTree_Root(t *testing.T) {
	mgr, host, owner, name, sha := setupDiffRepoForTree(t)

	entries, err := mgr.Tree(context.Background(), host, owner, name, sha, "")
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
	for _, e := range entries {
		assert.NotEmpty(t, e.Name)
		assert.Contains(t, []string{"dir", "file"}, e.Type)
	}
}

func TestTree_NonexistentPath(t *testing.T) {
	mgr, host, owner, name, sha := setupDiffRepoForTree(t)

	_, err := mgr.Tree(context.Background(), host, owner, name, sha, "does/not/exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestTree_NonexistentSHA(t *testing.T) {
	mgr, host, owner, name, _ := setupDiffRepoForTree(t)

	_, err := mgr.Tree(context.Background(), host, owner, name, "0000000000000000000000000000000000000000", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func setupDiffRepoForTree(t *testing.T) (*Manager, string, string, string, string) {
	require := require.New(t)
	assert := assert.New(t)

	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	commitTestRun(t, dir, "git", "init", "--bare", "--initial-branch=main", bare)

	work := filepath.Join(dir, "work")
	commitTestRun(t, dir, "git", "clone", bare, work)
	commitTestRun(t, work, "git", "config", "user.email", "test@test.com")
	commitTestRun(t, work, "git", "config", "user.name", "Test")

	// Create a directory structure with files
	require.NoError(os.Mkdir(filepath.Join(work, "internal"), 0o755))
	require.NoError(os.WriteFile(filepath.Join(work, "main.go"), []byte("package main\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "internal", "handler.go"), []byte("package internal\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "README.md"), []byte("# Test\n"), 0o644))

	commitTestRun(t, work, "git", "add", ".")
	commitTestRun(t, work, "git", "commit", "-m", "initial commit")
	commitTestRun(t, work, "git", "push", "origin", "main")
	headSHA := gitSHA(t, work, "HEAD")

	assert.NotEmpty(headSHA)

	mgr := New(filepath.Dir(bare), nil)
	return mgr, "", "", "remote", headSHA
}
