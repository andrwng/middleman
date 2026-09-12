package worktrees

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/gitclone"
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

// revSHA resolves a revision in the worktree to a full SHA.
func revSHA(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	return string(bytes.TrimSpace(out))
}

// spanRepo builds a worktree with three commits on top of the initial one,
// each adding a file of its own, so a diff's file list names exactly which
// commits it represents. Returns the worktree path and the three SHAs
// oldest-first.
func spanRepo(t *testing.T) (string, [3]string) {
	t.Helper()
	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")
	var shas [3]string
	for i, name := range []string{"first.txt", "second.txt", "third.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("hi\n"), 0o644))
		runGitT(t, dir, "add", name)
		runGitT(t, dir, "commit", "-m", "add "+name)
		shas[i] = revSHA(t, dir, "HEAD")
	}
	return dir, shas
}

func paths(files []gitclone.DiffFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// The commits panel's span scope has to include the oldest commit the reader
// selected. Shift-clicking two consecutive commits showed only the newer
// one's changes, because the span was diffed as `from..to` -- git's
// two-endpoint form, which leaves `from`'s own changes out.
func TestDiffCommitSpanIncludesTheOldestCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	ctx := context.Background()
	dir, shas := spanRepo(t)
	oldest, middle, newest := shas[0], shas[1], shas[2]

	// Two consecutive commits: the exact case that was reported.
	files, err := DiffCommitSpan(ctx, dir, middle, newest)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"second.txt", "third.txt"}, paths(files))

	// And a longer span.
	files, err = DiffCommitSpan(ctx, dir, oldest, newest)
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{"first.txt", "second.txt", "third.txt"}, paths(files))

	// A span of one commit is that commit's own diff.
	files, err = DiffCommitSpan(ctx, dir, middle, middle)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"second.txt"}, paths(files))
}

// DiffRange keeps its two-endpoint meaning, which is what DiffBaseToHEAD
// needs: the merge base's own changes belong to the base branch, not to the
// branch under review.
func TestDiffRangeStaysExclusiveOfItsStartPoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	ctx := context.Background()
	dir, shas := spanRepo(t)

	files, err := DiffRange(ctx, dir, shas[0], shas[2])
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"second.txt", "third.txt"}, paths(files))
}
