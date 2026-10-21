//go:build integration

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

func TestDiff(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Create a test repo with two commits on different branches.
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	// Initial commit on main.
	require.NoError(os.WriteFile(filepath.Join(work, "hello.go"),
		[]byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	// Create a feature branch with changes.
	run(t, work, "git", "checkout", "-b", "feature")
	require.NoError(os.WriteFile(filepath.Join(work, "hello.go"),
		[]byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "new.go"),
		[]byte("package main\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "feature changes")
	run(t, work, "git", "push", "origin", "feature")

	// Get SHAs.
	mainSHA := getSHA(t, work, "origin/main")
	featureSHA := getSHA(t, work, "origin/feature")

	// Clone into manager.
	clonesDir := t.TempDir()
	mgr := New(clonesDir, nil)
	require.NoError(mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))

	// Compute merge base.
	mb, err := mgr.MergeBase(
		context.Background(), "github.com", "test", "repo",
		mainSHA, featureSHA)
	require.NoError(err)
	assert.Equal(mainSHA, mb) // merge base is the initial commit

	// Run diff.
	result, err := mgr.Diff(
		context.Background(), "github.com", "test", "repo",
		mb, featureSHA, false)
	require.NoError(err)
	require.Len(result.Files, 2)

	// hello.go should be modified.
	assert.Equal("hello.go", result.Files[0].Path)
	assert.Equal("modified", result.Files[0].Status)
	assert.Equal(1, result.Files[0].Additions)
	assert.Equal(1, result.Files[0].Deletions)

	// new.go should be added.
	assert.Equal("new.go", result.Files[1].Path)
	assert.Equal("added", result.Files[1].Status)
}

// TestDiffRangeIsInclusiveOfTheOldestCommit pins the guarantee the UI's span
// selection rests on. Selecting commits c1..c3 asks the server for
// from=c1&to=c3, which resolves to Diff(ParentOf(c1), c3) -- so c1's own
// changes have to appear. The scope pill used to render that span as
// "c1..c3", which in git syntax means the opposite, and a reader reasonably
// concluded the base commit was being left out.
func TestDiffRangeIsInclusiveOfTheOldestCommit(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	require.NoError(os.WriteFile(filepath.Join(work, "base.go"),
		[]byte("package main\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	// Three commits, each adding a file of its own, so the diff's file list
	// says exactly which commits are represented.
	run(t, work, "git", "checkout", "-b", "feature")
	for _, name := range []string{"first.go", "second.go", "third.go"} {
		require.NoError(os.WriteFile(filepath.Join(work, name),
			[]byte("package main\n"), 0o644))
		run(t, work, "git", "add", ".")
		run(t, work, "git", "commit", "-m", "add "+name)
	}
	run(t, work, "git", "push", "origin", "feature")

	oldest := getSHA(t, work, "origin/feature~2") // the commit adding first.go
	newest := getSHA(t, work, "origin/feature")   // the commit adding third.go

	clonesDir := t.TempDir()
	mgr := New(clonesDir, nil)
	require.NoError(mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))

	// The handler's from/to branch does exactly this pair of calls.
	parent, err := mgr.ParentOf(context.Background(), "github.com", "test", "repo", oldest)
	require.NoError(err)
	result, err := mgr.Diff(
		context.Background(), "github.com", "test", "repo", parent, newest, false)
	require.NoError(err)

	paths := make([]string, 0, len(result.Files))
	for _, f := range result.Files {
		paths = append(paths, f.Path)
	}
	// first.go is the one at stake: it comes from the oldest selected commit.
	assert.ElementsMatch([]string{"first.go", "second.go", "third.go"}, paths)
}

func TestDiffWhitespaceOnly(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	// Initial commit: ws.go is indented with a tab; real.go is normal.
	require.NoError(os.WriteFile(filepath.Join(work, "ws.go"),
		[]byte("package main\n\nfunc main() {\n\treturn\n}\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "real.go"),
		[]byte("package main\n\nvar x = 1\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	// Feature branch: ws.go only re-indents an existing line
	// (whitespace-only); real.go gets a genuine content change.
	run(t, work, "git", "checkout", "-b", "feature")
	require.NoError(os.WriteFile(filepath.Join(work, "ws.go"),
		[]byte("package main\n\nfunc main() {\n        return\n}\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "real.go"),
		[]byte("package main\n\nvar x = 2\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "feature")
	run(t, work, "git", "push", "origin", "feature")

	mainSHA := getSHA(t, work, "origin/main")
	featureSHA := getSHA(t, work, "origin/feature")

	clonesDir := t.TempDir()
	mgr := New(clonesDir, nil)
	require.NoError(mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))
	mb, err := mgr.MergeBase(
		context.Background(), "github.com", "test", "repo",
		mainSHA, featureSHA)
	require.NoError(err)

	// Default mode: ws.go is flagged whitespace-only and counted; the
	// real change is not. Regression guard: `git diff --raw -w` lists
	// whitespace-only files on git 2.43, so detection must use --numstat.
	result, err := mgr.Diff(
		context.Background(), "github.com", "test", "repo",
		mb, featureSHA, false)
	require.NoError(err)
	assert.Equal(1, result.WhitespaceOnlyCount)

	byPath := make(map[string]DiffFile, len(result.Files))
	for _, f := range result.Files {
		byPath[f.Path] = f
	}
	require.Contains(byPath, "ws.go")
	require.Contains(byPath, "real.go")
	assert.True(byPath["ws.go"].IsWhitespaceOnly)
	assert.False(byPath["real.go"].IsWhitespaceOnly)

	// Hide mode: the whitespace-only file is removed from the list; the
	// real change stays.
	hidden, err := mgr.Diff(
		context.Background(), "github.com", "test", "repo",
		mb, featureSHA, true)
	require.NoError(err)
	hiddenPaths := make(map[string]bool, len(hidden.Files))
	for _, f := range hidden.Files {
		hiddenPaths[f.Path] = true
	}
	assert.NotContains(hiddenPaths, "ws.go")
	assert.Contains(hiddenPaths, "real.go")
}

func TestDiffFiles(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Create a test repo with two commits on different branches.
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	// Initial commit on main.
	require.NoError(os.WriteFile(filepath.Join(work, "hello.go"),
		[]byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	// Create a feature branch with changes.
	run(t, work, "git", "checkout", "-b", "feature")
	require.NoError(os.WriteFile(filepath.Join(work, "hello.go"),
		[]byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(work, "new.go"),
		[]byte("package main\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "feature changes")
	run(t, work, "git", "push", "origin", "feature")

	mainSHA := getSHA(t, work, "origin/main")
	featureSHA := getSHA(t, work, "origin/feature")

	clonesDir := t.TempDir()
	mgr := New(clonesDir, nil)
	require.NoError(mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))

	mb, err := mgr.MergeBase(
		context.Background(), "github.com", "test", "repo",
		mainSHA, featureSHA)
	require.NoError(err)

	files, err := mgr.DiffFiles(
		context.Background(), "github.com", "test", "repo",
		mb, featureSHA)
	require.NoError(err)
	require.Len(files, 2)

	// File metadata present.
	assert.Equal("hello.go", files[0].Path)
	assert.Equal("modified", files[0].Status)
	assert.Equal("new.go", files[1].Path)
	assert.Equal("added", files[1].Status)

	// No patch content.
	assert.Empty(files[0].Hunks)
	assert.Empty(files[1].Hunks)
	assert.Zero(files[0].Additions)
	assert.Zero(files[0].Deletions)
}

func TestDiffFilesEmpty(t *testing.T) {
	assert := assert.New(t)

	// Diffing a commit against itself yields no files.
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(work, "hello.go"),
		[]byte("package main\n"), 0o644))
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	sha := getSHA(t, work, "origin/main")
	clonesDir := t.TempDir()
	mgr := New(clonesDir, nil)
	require.NoError(t, mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))

	// DiffFiles returns non-nil empty slice.
	files, err := mgr.DiffFiles(
		context.Background(), "github.com", "test", "repo", sha, sha)
	require.NoError(t, err)
	assert.NotNil(files)
	assert.Empty(files)

	// Diff returns non-nil empty slice.
	result, err := mgr.Diff(
		context.Background(), "github.com", "test", "repo", sha, sha, false)
	require.NoError(t, err)
	assert.NotNil(result.Files)
	assert.Empty(result.Files)
}

// setupDiffVarietyRepo builds a repo exercising every status DiffSummary and
// DiffPaths need to agree with Diff on: a modification, an addition, a
// deletion, a rename with edits, a pure copy, a binary file, a
// whitespace-only change, and a path containing a space and "[x]" (to probe
// pathspec literalness). Returns the manager, merge base, and feature head
// SHA.
func setupDiffVarietyRepo(t *testing.T) (mgr *Manager, mb, headSHA string) {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	run(t, dir, "git", "init", "--bare", "--initial-branch=main", remote)
	run(t, dir, "git", "clone", remote, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(content), 0o644))
	}

	// Base commit on main.
	write("mod.go", "package main\n\nvar x = 1\n")
	write("del.go", "package deleteme\n\nvar lorem = 12345\n")
	write("rename_src.go", "package main\n\nfunc Old() {}\n")
	write("copy_src.go", "line1\nline2\nline3\nline4\nline5\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(work, "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03}, 0o644))
	write("ws.go", "package main\n\nfunc main() {\n\treturn\n}\n")
	write("weird [x] path.txt", "alpha\nbeta\n")
	run(t, work, "git", "add", ".")
	run(t, work, "git", "commit", "-m", "initial")
	run(t, work, "git", "push", "origin", "main")

	// Feature branch: one change of each kind.
	run(t, work, "git", "checkout", "-b", "feature")
	write("mod.go", "package main\n\nvar x = 2\n")
	write("new.go", "export const greeting = \"hello world\";\n")
	require.NoError(t, os.Remove(filepath.Join(work, "del.go")))
	run(t, work, "git", "mv", "rename_src.go", "rename_dst.go")
	write("rename_dst.go", "package main\n\nfunc Old() {}\n\nfunc New() {}\n")
	// Pure copy: copy_src.go is untouched; copy_dst.go is identical, so
	// -C --find-copies-harder reports a copy rather than two adds.
	srcData, err := os.ReadFile(filepath.Join(work, "copy_src.go"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "copy_dst.go"), srcData, 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(work, "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0o644))
	write("ws.go", "package main\n\nfunc main() {\n        return\n}\n") // re-indent only
	write("weird [x] path.txt", "alpha\nbeta\ngamma\n")
	run(t, work, "git", "add", "-A")
	run(t, work, "git", "commit", "-m", "feature changes")
	run(t, work, "git", "push", "origin", "feature")

	mainSHA := getSHA(t, work, "origin/main")
	featureSHA := getSHA(t, work, "origin/feature")

	clonesDir := t.TempDir()
	mgr = New(clonesDir, nil)
	require.NoError(t, mgr.EnsureClone(
		context.Background(), "github.com", "test", "repo", remote))

	mb, err = mgr.MergeBase(
		context.Background(), "github.com", "test", "repo", mainSHA, featureSHA)
	require.NoError(t, err)

	return mgr, mb, featureSHA
}

func TestDiffSummaryMatchesDiffCounts(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	full, err := mgr.Diff(ctx, "github.com", "test", "repo", mb, headSHA, false)
	require.NoError(err)
	summary, err := mgr.DiffSummary(ctx, "github.com", "test", "repo", mb, headSHA, false)
	require.NoError(err)

	assert.Equal(full.WhitespaceOnlyCount, summary.WhitespaceOnlyCount)
	require.Len(summary.Files, len(full.Files))

	fullByPath := make(map[string]DiffFile, len(full.Files))
	for _, f := range full.Files {
		fullByPath[f.Path] = f
	}

	for _, sf := range summary.Files {
		ff, ok := fullByPath[sf.Path]
		require.True(ok, "summary file %q not present in full diff", sf.Path)
		assert.Equalf(ff.Status, sf.Status, "status for %q", sf.Path)
		assert.Equalf(ff.OldPath, sf.OldPath, "old_path for %q", sf.Path)
		assert.Equalf(ff.IsBinary, sf.IsBinary, "is_binary for %q", sf.Path)
		assert.Equalf(ff.Additions, sf.Additions, "additions for %q", sf.Path)
		assert.Equalf(ff.Deletions, sf.Deletions, "deletions for %q", sf.Path)
		assert.Equalf(ff.IsWhitespaceOnly, sf.IsWhitespaceOnly, "is_whitespace_only for %q", sf.Path)
		assert.Emptyf(sf.Hunks, "summary should carry no hunks for %q", sf.Path)
	}
}

func TestDiffSummaryHideWhitespace(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	summary, err := mgr.DiffSummary(ctx, "github.com", "test", "repo", mb, headSHA, true)
	require.NoError(err)
	assert.Equal(1, summary.WhitespaceOnlyCount)

	for _, f := range summary.Files {
		assert.NotEqualf("ws.go", f.Path, "whitespace-only file should be omitted in hide mode")
	}
}

func TestDiffPathsMatchesDiffPerFile(t *testing.T) {
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	full, err := mgr.Diff(ctx, "github.com", "test", "repo", mb, headSHA, false)
	require.NoError(t, err)
	require.NotEmpty(t, full.Files)

	for _, ff := range full.Files {
		ff := ff
		t.Run(ff.Path, func(t *testing.T) {
			result, err := mgr.DiffPaths(
				ctx, "github.com", "test", "repo", mb, headSHA, false,
				[]string{ff.Path, ff.OldPath})
			require.NoError(t, err)
			require.Len(t, result.Files, 1, "expected exactly the requested file")
			assert.Equal(t, ff, result.Files[0])
		})
	}
}

// TestDiffPathsRenameWithoutOldPath pins current behavior when the caller
// omits the rename's old path from the requested set: --literal-pathspecs
// only pairs a rename when both sides match the pathspec, so git (and thus
// DiffPaths) reports the new path as an addition instead of a rename. The
// client always sends both paths for renames/copies; this test documents
// the fallback rather than prescribing it.
func TestDiffPathsRenameWithoutOldPath(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	result, err := mgr.DiffPaths(
		ctx, "github.com", "test", "repo", mb, headSHA, false,
		[]string{"rename_dst.go"})
	require.NoError(err)
	require.Len(result.Files, 1)
	assert.Equal("rename_dst.go", result.Files[0].Path)
	assert.Equal("added", result.Files[0].Status)
}

// TestDiffPathsMultiFileBatch pins the shape the /diff/hunks endpoint
// actually uses: a batch of several files at once, including a rename (old
// and new path both requested). The batch's files must deep-equal the same
// entries from the full diff.
func TestDiffPathsMultiFileBatch(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	full, err := mgr.Diff(ctx, "github.com", "test", "repo", mb, headSHA, false)
	require.NoError(err)
	fullByPath := make(map[string]DiffFile, len(full.Files))
	for _, f := range full.Files {
		fullByPath[f.Path] = f
	}

	batch := []string{
		"mod.go", "mod.go",
		"rename_dst.go", "rename_src.go",
		"copy_dst.go", "copy_src.go",
		"new.go",
	}
	result, err := mgr.DiffPaths(
		ctx, "github.com", "test", "repo", mb, headSHA, false, batch)
	require.NoError(err)
	require.Len(result.Files, 4)

	for _, f := range result.Files {
		ff, ok := fullByPath[f.Path]
		require.True(ok, "batch file %q not present in full diff", f.Path)
		assert.Equal(ff, f)
	}
}

// TestDiffPathsEmptyAndDuplicatePaths covers two edge cases a caller can
// trigger: a paths list mixing "" and duplicates still works (git rejects a
// literal "" pathspec, so it must never reach the command line), and an
// all-empty/all-blank list short-circuits to no files without running git.
func TestDiffPathsEmptyAndDuplicatePaths(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	mgr, mb, headSHA := setupDiffVarietyRepo(t)

	result, err := mgr.DiffPaths(
		ctx, "github.com", "test", "repo", mb, headSHA, false,
		[]string{"", "mod.go", "mod.go", "", "mod.go"})
	require.NoError(err)
	require.Len(result.Files, 1)
	assert.Equal("mod.go", result.Files[0].Path)

	// A clone path that does not exist would make any real git invocation
	// fail, so a nil error here only holds if DiffPaths took the early-exit
	// path and never ran git.
	empty, err := mgr.DiffPaths(
		ctx, "github.com", "nonexistent-owner", "nonexistent-repo",
		mb, headSHA, false, []string{"", ""})
	require.NoError(err)
	assert.Empty(empty.Files)
}

func getSHA(t *testing.T, dir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", ref)
	cmd.Env = gitenv.StripAll(os.Environ())
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
