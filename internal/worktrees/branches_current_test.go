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

func TestResolveCommitSHA(t *testing.T) {
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

	want := gitHeadT(t, dir)
	require.Len(want, 40)

	// Empty ref resolves to HEAD.
	got, err := ResolveCommitSHA(ctx, dir, "")
	require.NoError(err)
	assert.Equal(want, got, "empty ref → HEAD")

	// "HEAD" resolves to the same full SHA.
	got, err = ResolveCommitSHA(ctx, dir, "HEAD")
	require.NoError(err)
	assert.Equal(want, got)

	// A full SHA passes through unchanged.
	got, err = ResolveCommitSHA(ctx, dir, want)
	require.NoError(err)
	assert.Equal(want, got)

	// A short SHA canonicalizes to the full SHA — the case that was
	// rendering agent-created threads as spurious orphans.
	short := want[:7]
	got, err = ResolveCommitSHA(ctx, dir, short)
	require.NoError(err)
	assert.Equal(want, got, "short SHA → full SHA")

	// The branch name resolves to its tip commit.
	got, err = ResolveCommitSHA(ctx, dir, "main")
	require.NoError(err)
	assert.Equal(want, got)

	// An unknown ref errors (callers decide fatal vs. verbatim fallback).
	_, err = ResolveCommitSHA(ctx, dir, "0000000000000000000000000000000000000000")
	require.Error(err)
}

func TestResolveCommitSHAErrorsOnNonRepo(t *testing.T) {
	require := require.New(t)
	_, err := ResolveCommitSHA(context.Background(), t.TempDir(), "HEAD")
	require.Error(err)
}

func TestResolveCommitSHAErrorsOnEmptyPath(t *testing.T) {
	require := require.New(t)
	_, err := ResolveCommitSHA(context.Background(), "", "HEAD")
	require.Error(err)
	require.Contains(err.Error(), "worktreePath is required")
}
