package gitclone

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/gitenv"
)

// runGitAllowFail is like commitTestRun but doesn't fail the test
// on a non-zero exit. Used for commands like `git rebase` that
// intentionally exit non-zero on conflict so the test can stage
// the resolution.
func runGitAllowFail(_ *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitenv.StripAll(os.Environ()),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	_, _ = cmd.CombinedOutput()
}

// interdiffScenario builds a small synthetic repo for an interdiff
// test case. Each test gets its own bare clone path plus a
// gitclone.Manager pointed at the partition root; the old/new base
// and head SHAs are computed as locals within each test.
type interdiffScenario struct {
	mgr   *Manager
	host  string
	owner string
	name  string
	work  string // working directory used to build the commits
}

// initScenario stands up a bare repo with one initial commit on
// main and returns helpers for staging additional state.
func initScenario(t *testing.T) (*interdiffScenario, string) {
	t.Helper()
	root := t.TempDir()
	host, owner, name := "", "", "remote"
	bare := filepath.Join(root, name+".git")
	commitTestRun(t, root, "git", "init", "--bare", "--initial-branch=main", bare)
	work := filepath.Join(root, "work")
	commitTestRun(t, root, "git", "clone", bare, work)
	commitTestRun(t, work, "git", "config", "user.email", "alice@test.com")
	commitTestRun(t, work, "git", "config", "user.name", "Alice")
	require.NoError(t, os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o644))
	commitTestRun(t, work, "git", "add", ".")
	commitTestRun(t, work, "git", "commit", "-m", "main 1")
	commitTestRun(t, work, "git", "push", "origin", "main")
	mgr := New(root, nil)
	return &interdiffScenario{
		mgr: mgr, host: host, owner: owner, name: name, work: work,
	}, mainHead(t, work)
}

func mainHead(t *testing.T, work string) string {
	return gitSHA(t, work, "HEAD")
}

func writeAndCommit(t *testing.T, work, fname, body, msg string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(work, fname), []byte(body), 0o644))
	commitTestRun(t, work, "git", "add", ".")
	commitTestRun(t, work, "git", "commit", "-m", msg)
	return gitSHA(t, work, "HEAD")
}

func checkout(t *testing.T, work, ref string) {
	t.Helper()
	commitTestRun(t, work, "git", "checkout", ref)
}

// Case 1 — Plain push (no rebase): oldBase == newBase.
// Interdiff = full diff oldHead..newHead.
func TestInterdiff_PlainPush(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	// Create PR branch off main with one commit. That's PS1.
	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	oldHead := writeAndCommit(t, s.work, "f1.txt", "v1\n", "add f1")
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	// Append another commit. That's PS2 — same base, just
	// fast-forward with a new commit.
	newHead := writeAndCommit(t, s.work, "f2.txt", "v2\n", "add f2")
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, base, newHead, base,
	)
	require.NoError(err)
	assert.Equal(InterdiffClean, res.Kind)
	// Interdiff should contain the new file (and only that file).
	assert.Contains(string(res.Diff), "f2.txt")
	assert.NotContains(string(res.Diff), "f1.txt")
}

// Case 2 — Rebase, no author edits. Interdiff should be empty.
func TestInterdiff_PureRebaseNoEdits(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	// PR branch off main with two commits → PS1.
	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	writeAndCommit(t, s.work, "a.txt", "a1\n", "add a")
	writeAndCommit(t, s.work, "b.txt", "b1\n", "add b")
	oldHead := mainHead(t, s.work)
	oldBase := base
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	// Advance main with an unrelated commit (the upstream change
	// the author needs to rebase onto).
	checkout(t, s.work, "main")
	writeAndCommit(t, s.work, "main2.txt", "m2\n", "main 2")
	commitTestRun(t, s.work, "git", "push", "origin", "main")
	newBase := mainHead(t, s.work)

	// Rebase the PR branch onto the new main without editing
	// anything. PS2 has a different head SHA but the same logical
	// changes.
	checkout(t, s.work, "pr")
	commitTestRun(t, s.work, "git", "rebase", "main")
	newHead := mainHead(t, s.work)
	commitTestRun(t, s.work, "git", "push", "--force", "origin", "pr")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, oldBase, newHead, newBase,
	)
	require.NoError(err)
	assert.Equal(InterdiffClean, res.Kind)
	// A pure rebase with no edits should produce an empty diff.
	assert.Empty(strings.TrimSpace(string(res.Diff)),
		"expected empty interdiff for pure rebase, got: %s", string(res.Diff))
}

// Case 3 — Rebase + new commit appended. Interdiff = the new
// commit's diff only.
func TestInterdiff_RebasePlusNewCommit(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	writeAndCommit(t, s.work, "a.txt", "a1\n", "add a")
	oldHead := mainHead(t, s.work)
	oldBase := base
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	checkout(t, s.work, "main")
	writeAndCommit(t, s.work, "main2.txt", "m2\n", "main 2")
	commitTestRun(t, s.work, "git", "push", "origin", "main")
	newBase := mainHead(t, s.work)

	checkout(t, s.work, "pr")
	commitTestRun(t, s.work, "git", "rebase", "main")
	// New commit added after rebase.
	writeAndCommit(t, s.work, "c.txt", "c1\n", "add c")
	newHead := mainHead(t, s.work)
	commitTestRun(t, s.work, "git", "push", "--force", "origin", "pr")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, oldBase, newHead, newBase,
	)
	require.NoError(err)
	assert.Equal(InterdiffClean, res.Kind)
	// Interdiff should mention only the new file.
	assert.Contains(string(res.Diff), "c.txt")
	assert.NotContains(string(res.Diff), "a.txt")
	assert.NotContains(string(res.Diff), "main2.txt")
}

// Case 4 — Rebase + amend of an existing commit. Interdiff = the
// amend's net change (i.e. only the modified file's delta).
func TestInterdiff_RebasePlusAmend(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	writeAndCommit(t, s.work, "a.txt", "a1\n", "add a")
	oldHead := mainHead(t, s.work)
	oldBase := base
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	checkout(t, s.work, "main")
	writeAndCommit(t, s.work, "main2.txt", "m2\n", "main 2")
	commitTestRun(t, s.work, "git", "push", "origin", "main")
	newBase := mainHead(t, s.work)

	checkout(t, s.work, "pr")
	commitTestRun(t, s.work, "git", "rebase", "main")
	// Amend the PR commit to change its body.
	require.NoError(os.WriteFile(filepath.Join(s.work, "a.txt"), []byte("a1 amended\n"), 0o644))
	commitTestRun(t, s.work, "git", "add", ".")
	commitTestRun(t, s.work, "git", "commit", "--amend", "--no-edit")
	newHead := mainHead(t, s.work)
	commitTestRun(t, s.work, "git", "push", "--force", "origin", "pr")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, oldBase, newHead, newBase,
	)
	require.NoError(err)
	assert.Equal(InterdiffClean, res.Kind)
	// Interdiff should show only the a.txt change (the amend),
	// not the unrelated main2.txt.
	body := string(res.Diff)
	assert.Contains(body, "a.txt")
	assert.Contains(body, "amended")
	assert.NotContains(body, "main2.txt")
}

// Case 5 — Cherry-pick conflicts during the synthetic replay.
// The author resolved a real merge conflict during their rebase,
// so cherry-picking the *unresolved* old commits onto new base
// fails. We expect kind=conflicted with a fallback raw diff.
func TestInterdiff_ConflictFallsBack(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	// PR commit edits base.txt one way.
	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	require.NoError(os.WriteFile(filepath.Join(s.work, "base.txt"), []byte("pr edit\n"), 0o644))
	commitTestRun(t, s.work, "git", "add", ".")
	commitTestRun(t, s.work, "git", "commit", "-m", "pr edits base")
	oldHead := mainHead(t, s.work)
	oldBase := base
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	// Main edits the same file a different way *and* adds an
	// unrelated file. The unrelated file makes oldHead..newHead a
	// non-empty diff after the rebase resolution, so we can
	// sanity-check that the fallback returns useful bytes.
	checkout(t, s.work, "main")
	require.NoError(os.WriteFile(filepath.Join(s.work, "base.txt"), []byte("main edit\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(s.work, "extra.txt"), []byte("extra\n"), 0o644))
	commitTestRun(t, s.work, "git", "add", ".")
	commitTestRun(t, s.work, "git", "commit", "-m", "main edits base + extra")
	commitTestRun(t, s.work, "git", "push", "origin", "main")
	newBase := mainHead(t, s.work)

	// Author rebases pr onto main and resolves the conflict by
	// keeping their version. New head's tree differs from a clean
	// cherry-pick.
	checkout(t, s.work, "pr")
	// Rebase will conflict on base.txt; that's expected.
	runGitAllowFail(t, s.work, "rebase", "main")
	require.NoError(os.WriteFile(filepath.Join(s.work, "base.txt"), []byte("pr edit\n"), 0o644))
	commitTestRun(t, s.work, "git", "add", ".")
	// GIT_EDITOR=true keeps git from launching an editor for the
	// rebase-continue commit message confirmation.
	cmd := exec.Command("git", "rebase", "--continue")
	cmd.Dir = s.work
	cmd.Env = append(gitenv.StripAll(os.Environ()),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_EDITOR=true",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(err, "rebase --continue failed: %s", out)
	newHead := mainHead(t, s.work)
	commitTestRun(t, s.work, "git", "push", "--force", "origin", "pr")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, oldBase, newHead, newBase,
	)
	require.NoError(err)
	assert.Equal(InterdiffConflicted, res.Kind, "reason: %s", res.Reason)
	// The fallback raw diff is whatever oldHead..newHead looks
	// like; sanity-check that we surfaced something useful.
	assert.NotEmpty(res.Diff)
}

// Case 6 — Force-push to unrelated history. oldHead and newHead
// share no relevant ancestry; cherry-pick will fail and we fall
// back to raw diff with conflicted (or unrelated) kind.
func TestInterdiff_UnrelatedHistoryFallsBack(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	s, base := initScenario(t)

	// PR with one file edit → PS1.
	commitTestRun(t, s.work, "git", "checkout", "-b", "pr", base)
	writeAndCommit(t, s.work, "x.txt", "x1\n", "pr v1")
	oldHead := mainHead(t, s.work)
	oldBase := base
	commitTestRun(t, s.work, "git", "push", "origin", "pr")

	// Author force-pushes a completely different branch (an
	// orphan) — simulated here by checking out a new branch from
	// the empty tree and committing different content.
	commitTestRun(t, s.work, "git", "checkout", "--orphan", "fresh")
	commitTestRun(t, s.work, "git", "rm", "-rf", ".")
	require.NoError(os.WriteFile(filepath.Join(s.work, "y.txt"), []byte("y1\n"), 0o644))
	commitTestRun(t, s.work, "git", "add", ".")
	commitTestRun(t, s.work, "git", "commit", "-m", "fresh start")
	newHead := mainHead(t, s.work)
	newBase := newHead // orphan; treat the new commit itself as its own base
	commitTestRun(t, s.work, "git", "push", "origin", "fresh")

	res, err := s.mgr.InterdiffPatchsets(
		context.Background(), s.host, s.owner, s.name,
		oldHead, oldBase, newHead, newBase,
	)
	require.NoError(err)
	// Either Conflicted or Unrelated is acceptable — the
	// invariant is that the caller is told it's not a clean
	// interdiff and the diff bytes are non-nil so something
	// useful renders.
	assert.NotEqual(InterdiffClean, res.Kind, "expected non-clean kind, reason: %s", res.Reason)
	assert.NotEmpty(res.Diff)
}
