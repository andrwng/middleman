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

// setupDiffVarietyWorktree builds a worktree with one commit exercising
// every status SummaryRevspec and DiffRevspecPaths need to agree with
// diffRevspec on: a modification, an addition, a deletion, a rename with
// edits, a pure copy, a binary file, a whitespace-only change, and a path
// containing a space and "[x]". Returns the worktree path and the commit's
// SHA (diffed via RevspecSingleCommit against its parent).
func setupDiffVarietyWorktree(t *testing.T) (worktreePath, sha string) {
	t.Helper()
	dir := t.TempDir()
	setupRepoWithRemote(t, dir, "main")

	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	write("mod.txt", "alpha\nbeta\n")
	write("del.txt", "deleteme\nlorem\n")
	write("rename_src.txt", "line one\nline two\n")
	write("copy_src.txt", "copyline1\ncopyline2\ncopyline3\ncopyline4\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03}, 0o644))
	write("ws.txt", "line one\n\tindented\nline three\n")
	write("weird [x] path.txt", "alpha\nbeta\n")
	runGitT(t, dir, "add", ".")
	runGitT(t, dir, "commit", "-m", "before")

	write("mod.txt", "alpha\nbeta\nchanged\n")
	write("new.txt", "export const greeting = \"hello world\";\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "del.txt")))
	runGitT(t, dir, "mv", "rename_src.txt", "rename_dst.txt")
	write("rename_dst.txt", "line one\nline two\nline three\n")
	srcData, err := os.ReadFile(filepath.Join(dir, "copy_src.txt"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copy_dst.txt"), srcData, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0o644))
	write("ws.txt", "line one\n        indented\nline three\n") // re-indent only
	write("weird [x] path.txt", "alpha\nbeta\ngamma\n")
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-m", "changes")

	return dir, revSHA(t, dir, "HEAD")
}

func TestSummaryRevspecMatchesDiffRevspecCounts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir, sha := setupDiffVarietyWorktree(t)
	r := RevspecSingleCommit(sha)

	full, err := diffRevspec(ctx, dir, r)
	require.NoError(err)
	require.Len(full, 8)

	summary, err := SummaryRevspec(ctx, dir, r)
	require.NoError(err)
	require.Len(summary, len(full))

	fullByPath := make(map[string]gitclone.DiffFile, len(full))
	for _, f := range full {
		fullByPath[f.Path] = f
	}

	for _, sf := range summary {
		ff, ok := fullByPath[sf.Path]
		require.True(ok, "summary file %q not present in full diff", sf.Path)
		assert.Equalf(ff.Status, sf.Status, "status for %q", sf.Path)
		assert.Equalf(ff.OldPath, sf.OldPath, "old_path for %q", sf.Path)
		assert.Equalf(ff.IsBinary, sf.IsBinary, "is_binary for %q", sf.Path)
		assert.Equalf(ff.Additions, sf.Additions, "additions for %q", sf.Path)
		assert.Equalf(ff.Deletions, sf.Deletions, "deletions for %q", sf.Path)
		assert.Emptyf(sf.Hunks, "summary should carry no hunks for %q", sf.Path)
	}
}

func TestDiffRevspecPathsMatchesDiffRevspecPerFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	ctx := context.Background()

	dir, sha := setupDiffVarietyWorktree(t)
	r := RevspecSingleCommit(sha)

	full, err := diffRevspec(ctx, dir, r)
	require.NoError(t, err)
	require.NotEmpty(t, full)

	for _, ff := range full {
		ff := ff
		t.Run(ff.Path, func(t *testing.T) {
			result, err := DiffRevspecPaths(ctx, dir, r, []string{ff.Path, ff.OldPath})
			require.NoError(t, err)
			require.Len(t, result, 1, "expected exactly the requested file")
			assert.Equal(t, ff, result[0])
		})
	}
}

// TestDiffRevspecPathsRenameWithoutOldPath pins current behavior when the
// caller omits the rename's old path: --literal-pathspecs only pairs a
// rename when both sides match the pathspec, so the new path comes back as
// an addition instead. The client always sends both paths for
// renames/copies; this documents the fallback rather than prescribing it.
func TestDiffRevspecPathsRenameWithoutOldPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir, sha := setupDiffVarietyWorktree(t)
	r := RevspecSingleCommit(sha)

	result, err := DiffRevspecPaths(ctx, dir, r, []string{"rename_dst.txt"})
	require.NoError(err)
	require.Len(result, 1)
	assert.Equal("rename_dst.txt", result[0].Path)
	assert.Equal("added", result[0].Status)
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
