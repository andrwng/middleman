package aireview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/db"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// localRepo is a reviewer's worktree with committed files tracked.txt,
// gone.txt and a .gitignore ignoring *.log.
func localRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--initial-branch=feat/a")
	gitIn(t, dir, "config", "user.email", "t@t")
	gitIn(t, dir, "config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644))
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "c1")
	return dir
}

func TestSnapshotWorkingTreeLeavesTreeUntouched(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		file    string // expected in the snapshot with content
		content string
		absent  string // expected missing from the snapshot
	}{
		{"staged change", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("staged\n"), 0o644))
			gitIn(t, dir, "add", "tracked.txt")
		}, "tracked.txt", "staged\n", ""},
		{"unstaged change", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("unstaged\n"), 0o644))
		}, "tracked.txt", "unstaged\n", ""},
		{"untracked file", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644))
		}, "new.txt", "new\n", ""},
		{"deleted tracked file", func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
		}, "tracked.txt", "one\n", "gone.txt"},
		{"ignored file", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("noise\n"), 0o644))
		}, "tracked.txt", "one\n", "debug.log"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := require.New(t)
			dir := localRepo(t)
			tc.prepare(t, dir)
			statusBefore := gitIn(t, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
			indexBefore := gitIn(t, dir, "ls-files", "-s")

			runID := int64(i + 1)
			sha, err := SnapshotWorkingTree(context.Background(), dir, runID)
			must.NoError(err)

			must.Equal(statusBefore, gitIn(t, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all"))
			must.Equal(indexBefore, gitIn(t, dir, "ls-files", "-s"))
			must.Equal(tc.content, gitIn(t, dir, "show", sha+":"+tc.file))
			if tc.absent != "" {
				must.NotContains(gitIn(t, dir, "ls-tree", "--name-only", sha), tc.absent)
			}
			must.Equal(sha, strings.TrimSpace(gitIn(t, dir, "rev-parse", SnapshotRef(runID))))
			must.Equal(strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD")), strings.TrimSpace(gitIn(t, dir, "rev-parse", sha+"^")))

			must.NoError(DeleteSnapshotRef(context.Background(), dir, runID))
			cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", SnapshotRef(runID))
			cmd.Dir = dir
			must.Error(cmd.Run())
		})
	}
}

func TestReviewRunFromLocalRepoDir(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	dir := localRepo(t)
	require.NoError(os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("uncommitted\n"), 0o644))
	database := openTestDB(t)
	mrID := seedMR(t, database)
	root := t.TempDir()
	r := New(RunnerConfig{DB: database, WorktreeDir: root})

	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, Branch: "feat/a", HeadSHA: "h", ReadSHA: "h", LocalWorktreePath: dir})
	require.NoError(err)
	snap, err := SnapshotWorkingTree(ctx, dir, run.ID)
	require.NoError(err)

	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "args.txt")
	fake := filepath.Join(tmp, "claude.sh")
	writeFakeReviewClaude(t, fake, argsFile, "cat tracked.txt > "+argsFile+".content\n"+fakeStreamOK)
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	require.NoError(r.StartReviewRun(ctx, ReviewRunInput{RunID: run.ID, Owner: "local", Name: "demo", Number: 1, HeadSHA: snap, RepoDir: dir}))
	waitRunStatus(t, database, run.ID, "done")
	content, err := os.ReadFile(argsFile + ".content")
	require.NoError(err)
	assert.Equal("uncommitted\n", string(content))
	require.Eventually(func() bool {
		return !strings.Contains(gitIn(t, dir, "worktree", "list"), r.ReviewRunPath(run.ID))
	}, 5*time.Second, 20*time.Millisecond)
	// The reviewer's own tree is untouched.
	assert.Equal("uncommitted\n", func() string { b, _ := os.ReadFile(filepath.Join(dir, "tracked.txt")); return string(b) }())
}

func TestDraftChatUseExistingDir(t *testing.T) {
	require := require.New(t)
	dir := localRepo(t)
	database := openTestDB(t)
	mrID := seedMR(t, database)
	r := New(RunnerConfig{DB: database, WorktreeDir: t.TempDir()})
	ctx := context.Background()
	_, err := database.InsertUserReviewDrafts(ctx, mrID, "feat/a", []db.NewReviewDraft{{ID: "u1", Path: "tracked.txt", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "b"}})
	require.NoError(err)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\npwd > "+fake+".cwd\n"+forkStream), 0o755))
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	msg := queueReply(t, database, "u1")
	r.SubmitDraftTurn(DraftTurnInput{DraftID: "u1", MessageID: msg.ID, Owner: "local", Name: "demo", Number: 1,
		Mode: DraftChatFresh, WorktreePath: dir, UseExistingDir: true, Prompt: "q"})
	waitMessage(t, database, msg.ID, "done")
	cwd, err := os.ReadFile(fake + ".cwd")
	require.NoError(err)
	require.Equal(dir, strings.TrimSpace(string(cwd)))
	_, err = os.Stat(filepath.Join(dir, "tracked.txt"))
	require.NoError(err)
}

// A local run must not touch the reviewer's other worktrees, even ones
// git would consider prunable (moved or on an unmounted volume), and must
// not run the reviewer's checkout hooks.
func TestLocalRunLeavesReviewersRepoAlone(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	dir := localRepo(t)
	away := filepath.Join(t.TempDir(), "elsewhere")
	gitIn(t, dir, "worktree", "add", "--detach", away)
	require.NoError(os.RemoveAll(away)) // now "prunable" in git's eyes
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	require.NoError(os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755))

	database := openTestDB(t)
	mrID := seedMR(t, database)
	r := New(RunnerConfig{DB: database, WorktreeDir: t.TempDir()})
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, Branch: "feat/a", HeadSHA: "h", ReadSHA: "h", LocalWorktreePath: dir})
	require.NoError(err)
	snap, err := SnapshotWorkingTree(ctx, dir, run.ID)
	require.NoError(err)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "claude.sh")
	writeFakeReviewClaude(t, fake, filepath.Join(tmp, "args.txt"), fakeStreamOK)
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	require.NoError(r.StartReviewRun(ctx, ReviewRunInput{RunID: run.ID, Owner: "local", Name: "demo", Number: 1, HeadSHA: snap, RepoDir: dir}))
	waitRunStatus(t, database, run.ID, "done")
	require.Eventually(func() bool {
		return !strings.Contains(gitIn(t, dir, "worktree", "list"), r.ReviewRunPath(run.ID))
	}, 5*time.Second, 20*time.Millisecond)

	assert.Contains(gitIn(t, dir, "worktree", "list"), away, "reviewer's other worktree metadata must survive")
	_, statErr := os.Stat(marker)
	assert.True(os.IsNotExist(statErr), "reviewer's post-checkout hook must not run")
}
