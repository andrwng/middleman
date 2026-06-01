package worktrees

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitHeadT(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestBranchHeads(t *testing.T) {
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
	c1 := gitHeadT(t, dir)
	runGitT(t, dir, "commit", "--allow-empty", "-m", "c2")
	c2 := gitHeadT(t, dir)

	// Two branches point at c1; one at c2. main (current) is at c2.
	runGitT(t, dir, "branch", "feat/a", c1)
	runGitT(t, dir, "branch", "feat/b", c1)
	runGitT(t, dir, "branch", "release", c2)

	heads, err := BranchHeads(ctx, dir, "main")
	require.NoError(err)
	assert.Equal([]string{"feat/a", "feat/b"}, heads[c1])
	// 'main' also points at c2 but is excluded; only 'release' remains.
	assert.Equal([]string{"release"}, heads[c2])
}

func TestBranchHeadsEmptyExcludeKeepsAll(t *testing.T) {
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
	c1 := gitHeadT(t, dir)

	// excludeBranch == "" (e.g. detached worktree) keeps every branch.
	heads, err := BranchHeads(ctx, dir, "")
	require.NoError(err)
	assert.Equal([]string{"main"}, heads[c1])
}
