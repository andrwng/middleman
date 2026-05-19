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

func TestDiffAgainstBaseProducesHunks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	// Commit a baseline file, push so origin/main has it, then
	// diverge with an uncommitted line addition.
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("alpha\nbeta\n"), 0o644))
	runGitT(t, dir, "add", "hello.txt")
	runGitT(t, dir, "commit", "-m", "add hello")
	runGitT(t, dir, "push", "origin", "main")
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644))

	ds, err := DiffAgainstBase(ctx, dir, "")
	require.NoError(err)
	assert.Equal("origin/main", ds.Base.Ref)
	require.Len(ds.Files, 1)

	f := ds.Files[0]
	assert.Equal("hello.txt", f.Path)
	assert.Equal("modified", f.Status)
	assert.Equal(1, f.Additions)
	assert.Equal(0, f.Deletions)
	require.NotEmpty(f.Hunks, "expected at least one hunk")

	h := f.Hunks[0]
	require.NotEmpty(h.Lines)
	// The hunk should contain a context line and an added line.
	sawContext := false
	sawAdd := false
	for _, line := range h.Lines {
		if line.Type == "context" {
			sawContext = true
		}
		if line.Type == "add" && line.Content == "gamma" {
			sawAdd = true
		}
	}
	assert.True(sawContext, "expected at least one context line")
	assert.True(sawAdd, "expected an added line with content 'gamma'")
}

func TestDiffAgainstBaseEmptyWhenClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	ds, err := DiffAgainstBase(ctx, dir, "")
	require.NoError(err)
	require.Empty(ds.Files)
}
