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

func TestListChangedFilesEmptyWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "init")

	got, err := ListChangedFiles(ctx, dir)
	require.NoError(err)
	require.Empty(got)
}

func TestListChangedFilesPicksUpAddedAndModified(t *testing.T) {
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
	require.NoError(os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("alpha\nbeta\n"), 0o644))
	runGitT(t, dir, "add", "kept.txt")
	runGitT(t, dir, "commit", "-m", "init")

	// Modify the committed file (uncommitted change).
	require.NoError(os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644))
	// Add a new file (uncommitted).
	require.NoError(os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello\n"), 0o644))
	runGitT(t, dir, "add", "new.txt")

	got, err := ListChangedFiles(ctx, dir)
	require.NoError(err)
	require.Len(got, 2)

	byPath := map[string]ChangedFile{got[0].Path: got[0], got[1].Path: got[1]}

	mod := byPath["kept.txt"]
	assert.Equal("modified", mod.Status)
	assert.Equal(1, mod.Additions)
	assert.Equal(0, mod.Deletions)
	assert.False(mod.IsBinary)

	added := byPath["new.txt"]
	assert.Equal("added", added.Status)
	assert.Equal(1, added.Additions)
	assert.Equal(0, added.Deletions)
}

func TestListChangedFilesDetectsDelete(t *testing.T) {
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
	require.NoError(os.WriteFile(filepath.Join(dir, "doomed.txt"), []byte("alpha\nbeta\n"), 0o644))
	runGitT(t, dir, "add", "doomed.txt")
	runGitT(t, dir, "commit", "-m", "init")
	require.NoError(os.Remove(filepath.Join(dir, "doomed.txt")))
	runGitT(t, dir, "add", "-A", "doomed.txt")

	got, err := ListChangedFiles(ctx, dir)
	require.NoError(err)
	require.Len(got, 1)
	assert.Equal("doomed.txt", got[0].Path)
	assert.Equal("deleted", got[0].Status)
	assert.Equal(0, got[0].Additions)
	assert.Equal(2, got[0].Deletions)
}

func TestResolveBasePicksOriginMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	base, err := ResolveBase(ctx, dir, "")
	require.NoError(err)
	assert.Equal("origin/main", base.Ref)
	assert.False(base.Fallback)
	assert.NotEmpty(base.SHA)
}

func TestResolveBaseFallsThroughCandidates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "dev")

	base, err := ResolveBase(ctx, dir, "")
	require.NoError(err)
	assert.Equal("origin/dev", base.Ref)
	assert.False(base.Fallback)
}

func TestResolveBaseFallsBackToHEAD(t *testing.T) {
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
	runGitT(t, dir, "commit", "--allow-empty", "-m", "init")

	base, err := ResolveBase(ctx, dir, "")
	require.NoError(err)
	assert.Empty(base.Ref)
	assert.True(base.Fallback)
	assert.NotEmpty(base.SHA)
}

func TestChangedFilesAgainstBaseIncludesCommittedAndUncommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	// Diverge from origin/main: one committed change, one uncommitted.
	require.NoError(os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("commit\n"), 0o644))
	runGitT(t, dir, "checkout", "-b", "feat/x")
	runGitT(t, dir, "add", "committed.txt")
	runGitT(t, dir, "commit", "-m", "add committed file")
	require.NoError(os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("wip\n"), 0o644))

	cs, err := ChangedFilesAgainstBase(ctx, dir, "")
	require.NoError(err)
	assert.Equal("origin/main", cs.Base.Ref)
	assert.False(cs.Base.Fallback)

	paths := make(map[string]ChangedFile, len(cs.Files))
	for _, f := range cs.Files {
		paths[f.Path] = f
	}
	require.Contains(paths, "committed.txt")
	require.Contains(paths, "uncommitted.txt")
	assert.Equal("added", paths["committed.txt"].Status)
	assert.Equal("added", paths["uncommitted.txt"].Status)
}

func TestChangedFilesAgainstBaseFiltersNestedWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	parent := t.TempDir()
	setupRepoWithRemote(t, parent, "main")

	// Add a nested worktree under `.worktrees/test`. Its directory
	// is untracked from the parent's POV, so without filtering
	// `git ls-files --others` would report it as an "added" entry.
	nestedPath := filepath.Join(parent, ".worktrees", "test")
	require.NoError(os.MkdirAll(filepath.Dir(nestedPath), 0o755))
	runGitT(t, parent, "worktree", "add", "-b", "feat/x", nestedPath)

	cs, err := ChangedFilesAgainstBase(ctx, parent, "")
	require.NoError(err)

	for _, f := range cs.Files {
		assert.NotContains(f.Path, ".worktrees/test",
			"nested worktree dir should not appear as an untracked entry",
		)
	}
}

func TestResolveBaseHonorsOverride(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	// Repo has both origin/main and origin/dev. Without an override
	// the resolver picks origin/main; the override flips that.
	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")
	runGitT(t, dir, "checkout", "-b", "dev")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "dev tip")
	runGitT(t, dir, "push", "origin", "dev")
	runGitT(t, dir, "fetch", "origin")
	runGitT(t, dir, "checkout", "main")

	noOverride, err := ResolveBase(ctx, dir, "")
	require.NoError(err)
	assert.Equal("origin/main", noOverride.Ref)

	withOverride, err := ResolveBase(ctx, dir, "origin/dev")
	require.NoError(err)
	assert.Equal("origin/dev", withOverride.Ref)
}

func TestResolveBaseOverrideFallsBackWhenMissing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	// origin/main exists; the override ref doesn't. Resolver falls
	// through the override and lands on origin/main.
	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	base, err := ResolveBase(ctx, dir, "origin/bogus-does-not-exist")
	require.NoError(err)
	assert.Equal("origin/main", base.Ref)
	assert.False(base.Fallback)
}

// setupRepoWithRemote initialises `dir` as a working repo on
// branch `mainBranch`, then bootstraps a sibling bare clone as
// `origin`. The result is a repo where origin/<mainBranch> exists
// and tracks the working repo's history.
func setupRepoWithRemote(t *testing.T, dir, mainBranch string) {
	t.Helper()
	runGitT(t, "", "init", "--initial-branch="+mainBranch, dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "init")

	// Sibling bare repo to serve as origin.
	originDir := dir + "-origin.git"
	runGitT(t, "", "init", "--bare", originDir)
	runGitT(t, dir, "remote", "add", "origin", originDir)
	runGitT(t, dir, "push", "origin", mainBranch)
	runGitT(t, dir, "fetch", "origin")
}

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
}
