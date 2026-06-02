package worktrees

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "c1")

	got, err := CurrentBranch(ctx, dir)
	require.NoError(err)
	assert.Equal("main", got)

	runGitT(t, dir, "checkout", "-b", "feat/x")
	got, err = CurrentBranch(ctx, dir)
	require.NoError(err)
	assert.Equal("feat/x", got)

	// Detached HEAD reports as "" (matches the synthetic-MR convention).
	sha := gitHeadT(t, dir)
	runGitT(t, dir, "checkout", sha)
	got, err = CurrentBranch(ctx, dir)
	require.NoError(err)
	assert.Empty(got)
}

func TestCurrentBranchErrorsOnNonRepo(t *testing.T) {
	require := require.New(t)
	_, err := CurrentBranch(context.Background(), t.TempDir())
	require.Error(err)
}
