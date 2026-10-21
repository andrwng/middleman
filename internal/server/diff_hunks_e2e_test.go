package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	ghclient "github.com/wesm/middleman/internal/github"
)

// sortDiffFilesByPath returns a copy of files sorted by Path, for
// order-independent comparisons.
func sortDiffFilesByPath(files []generated.DiffFile) []generated.DiffFile {
	out := make([]generated.DiffFile, len(files))
	copy(out, files)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// --- PR-backed scopes ---

func TestAPIDiffHunks_PRPerFileMatchesDiff(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, mergeBase, headSHA, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "widget", 1, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)
	diffFiles := *diffResp.JSON200.Files
	byPath := make(map[string]generated.DiffFile, len(diffFiles))
	for _, f := range diffFiles {
		byPath[f.Path] = f
	}

	for _, f := range diffFiles {
		paths := []string{f.Path}
		if f.OldPath != "" {
			paths = append(paths, f.OldPath)
		}
		body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
			ExpectFrom: mergeBase,
			ExpectTo:   headSHA,
			Paths:      &paths,
		}
		hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
			ctx, "acme", "widget", 1, body,
		)
		require.NoError(err)
		require.Equalf(http.StatusOK, hunksResp.StatusCode(), "path %s", f.Path)
		require.NotNil(hunksResp.JSON200.Files)
		got := *hunksResp.JSON200.Files
		require.Lenf(got, 1, "path %s", f.Path)
		assert.Equal(byPath[f.Path], got[0], "mismatch for %s", f.Path)
	}
}

func TestAPIDiffHunks_PRAllPathsAtOnceIgnoringOrder(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, mergeBase, headSHA, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "widget", 1, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)
	diffFiles := *diffResp.JSON200.Files

	paths := make([]string, 0, len(diffFiles))
	for _, f := range diffFiles {
		paths = append(paths, f.Path)
		if f.OldPath != "" {
			paths = append(paths, f.OldPath)
		}
	}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: mergeBase,
		ExpectTo:   headSHA,
		Paths:      &paths,
	}
	hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "acme", "widget", 1, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hunksResp.StatusCode())
	require.NotNil(hunksResp.JSON200.Files)

	assert.Equal(sortDiffFilesByPath(diffFiles), sortDiffFilesByPath(*hunksResp.JSON200.Files))
}

func TestAPIDiffHunks_PRStaleExpectReturns409(t *testing.T) {
	require := require.New(t)
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	paths := []string{"file1.txt"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: mergeBase,
		ExpectTo:   "0000000000000000000000000000000000000000",
		Paths:      &paths,
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "acme", "widget", 1, body,
	)
	require.NoError(err)
	require.Equal(http.StatusConflict, resp.StatusCode())
	require.NotNil(resp.ApplicationproblemJSONDefault)
	require.NotNil(resp.ApplicationproblemJSONDefault.Detail)
	require.Contains(*resp.ApplicationproblemJSONDefault.Detail, "diff revisions changed")
}

func TestAPIDiffHunks_PRTooManyPathsReturns400(t *testing.T) {
	require := require.New(t)
	client, _, mergeBase, headSHA, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	paths := make([]string, 101)
	for i := range paths {
		paths[i] = fmt.Sprintf("nonexistent-%d.txt", i)
	}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: mergeBase,
		ExpectTo:   headSHA,
		Paths:      &paths,
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "acme", "widget", 1, body,
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())
}

// --- Local worktree scopes ---

func TestAPIDiffHunks_LocalWorkingTreeEdit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")

	runGitWT(t, dir, "checkout", "-b", "feat/x")
	require.NoError(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feat\n"), 0o644))
	runGitWT(t, dir, "add", "feature.txt")
	runGitWT(t, dir, "commit", "-m", "add feature")
	// Uncommitted edit on top.
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\nedit\n"), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "feat/x",
	})
	require.NoError(err)

	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	paths := []string{"base.txt", "feature.txt"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: filesResp.JSON200.DiffFrom,
		ExpectTo:   filesResp.JSON200.DiffTo,
		Paths:      &paths,
	}
	hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hunksResp.StatusCode())
	require.NotNil(hunksResp.JSON200.Files)
	got := *hunksResp.JSON200.Files
	require.Len(got, 2)

	var baseFile *generated.DiffFile
	for i := range got {
		if got[i].Path == "base.txt" {
			baseFile = &got[i]
		}
	}
	require.NotNil(baseFile)
	require.NotNil(baseFile.Hunks)
	hunks := *baseFile.Hunks
	require.Len(hunks, 1)
	require.NotNil(hunks[0].Lines)
	foundEdit := false
	for _, l := range *hunks[0].Lines {
		if l.Content == "edit" {
			foundEdit = true
		}
	}
	assert.True(foundEdit, "expected 'edit' line in base.txt hunk")
}

func TestAPIDiffHunks_LocalCommitScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")

	runGitWT(t, dir, "checkout", "-b", "feat/x")
	require.NoError(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feat\n"), 0o644))
	runGitWT(t, dir, "add", "feature.txt")
	runGitWT(t, dir, "commit", "-m", "add feature")
	commitSHA := testGitSHA(t, dir, "HEAD")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "feat/x",
	})
	require.NoError(err)

	filesParams := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{Commit: &commitSHA}
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, filesParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)
	require.Len(*filesResp.JSON200.Files, 1)

	paths := []string{"feature.txt"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		Commit:     &commitSHA,
		ExpectFrom: filesResp.JSON200.DiffFrom,
		ExpectTo:   filesResp.JSON200.DiffTo,
		Paths:      &paths,
	}
	hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hunksResp.StatusCode())
	require.NotNil(hunksResp.JSON200.Files)
	got := *hunksResp.JSON200.Files
	require.Len(got, 1)
	assert.Equal("feature.txt", got[0].Path)
	assert.Equal("added", got[0].Status)
}

func TestAPIDiffHunks_LocalStaleExpectReturns409(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	paths := []string{"base.txt"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: "0000000000000000000000000000000000000000",
		ExpectTo:   "",
		Paths:      &paths,
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusConflict, resp.StatusCode())
}

// --- Paths with spaces and brackets ---

func TestAPIDiffHunks_PathsWithSpacesAndBrackets(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	bareDir := filepath.Join(dir, "clones")
	require.NoError(os.MkdirAll(bareDir, 0o755))
	bare := filepath.Join(bareDir, "github.com", "acme", "special.git")

	tmpWork := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, tmpWork)
	runGit(t, tmpWork, "config", "user.email", "test@test.com")
	runGit(t, tmpWork, "config", "user.name", "Test")

	require.NoError(os.WriteFile(filepath.Join(tmpWork, "base.txt"), []byte("base\n"), 0o644))
	runGit(t, tmpWork, "add", ".")
	runGit(t, tmpWork, "commit", "-m", "base commit")
	runGit(t, tmpWork, "push", "origin", "main")
	mergeBase := testGitSHA(t, tmpWork, "HEAD")

	runGit(t, tmpWork, "checkout", "-b", "pr")
	oddName := "weird [dir]/file name with spaces.txt"
	require.NoError(os.MkdirAll(filepath.Join(tmpWork, "weird [dir]"), 0o755))
	require.NoError(os.WriteFile(filepath.Join(tmpWork, oddName), []byte("content\n"), 0o644))
	runGit(t, tmpWork, "add", "-A")
	runGit(t, tmpWork, "commit", "-m", "add odd path")
	runGit(t, tmpWork, "push", "origin", "pr")
	headSHA := testGitSHA(t, tmpWork, "HEAD")

	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "special", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: clones})

	seedPR(t, database, "acme", "special", 1)
	ctx := context.Background()
	repoID, err := database.UpsertRepo(ctx, "github.com", "acme", "special")
	require.NoError(err)
	require.NoError(database.UpdateDiffSHAs(ctx, repoID, 1, headSHA, mergeBase, mergeBase))

	client := setupTestClient(t, srv)

	paths := []string{oddName}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: mergeBase,
		ExpectTo:   headSHA,
		Paths:      &paths,
	}
	hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "acme", "special", 1, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hunksResp.StatusCode())
	require.NotNil(hunksResp.JSON200.Files)
	got := *hunksResp.JSON200.Files
	require.Len(got, 1)
	assert.Equal(oddName, got[0].Path)
	assert.Equal("added", got[0].Status)
}

// --- Rename / copy (Review Focus item 1, jointly owned by Task 3) ---

// TestAPIDiffHunks_PRRenameAndCopy pins that /diff/hunks reports a renamed
// (with edits) and a copied file as rename/copy -- not as an add plus a
// delete -- when the client sends [new_path, old_path] exactly as it will
// in production, and that the per-file result deep-equals the same file's
// entry from the full /diff.
func TestAPIDiffHunks_PRRenameAndCopy(t *testing.T) {
	// Named req (not require) so the nested t.Run subtests below can
	// still bind their own require/Assert helpers to their own
	// *testing.T without the outer var shadowing those packages.
	req := require.New(t)

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	req.NoError(err)
	t.Cleanup(func() { database.Close() })

	bareDir := filepath.Join(dir, "clones")
	req.NoError(os.MkdirAll(bareDir, 0o755))
	bare := filepath.Join(bareDir, "github.com", "acme", "renamecopy.git")

	tmpWork := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, tmpWork)
	runGit(t, tmpWork, "config", "user.email", "test@test.com")
	runGit(t, tmpWork, "config", "user.name", "Test")

	req.NoError(os.WriteFile(filepath.Join(tmpWork, "rename_src.go"),
		[]byte("package main\n\nfunc Old() {}\n"), 0o644))
	req.NoError(os.WriteFile(filepath.Join(tmpWork, "copy_src.go"),
		[]byte("line1\nline2\nline3\nline4\nline5\n"), 0o644))
	runGit(t, tmpWork, "add", ".")
	runGit(t, tmpWork, "commit", "-m", "base commit")
	runGit(t, tmpWork, "push", "origin", "main")
	mergeBase := testGitSHA(t, tmpWork, "HEAD")

	runGit(t, tmpWork, "checkout", "-b", "pr")
	runGit(t, tmpWork, "mv", "rename_src.go", "rename_dst.go")
	req.NoError(os.WriteFile(filepath.Join(tmpWork, "rename_dst.go"),
		[]byte("package main\n\nfunc Old() {}\n\nfunc New() {}\n"), 0o644))
	// Pure copy: copy_src.go is untouched; copy_dst.go is byte-identical,
	// so -C --find-copies-harder reports a copy rather than two adds.
	srcData, err := os.ReadFile(filepath.Join(tmpWork, "copy_src.go"))
	req.NoError(err)
	req.NoError(os.WriteFile(filepath.Join(tmpWork, "copy_dst.go"), srcData, 0o644))
	runGit(t, tmpWork, "add", "-A")
	runGit(t, tmpWork, "commit", "-m", "rename and copy")
	runGit(t, tmpWork, "push", "origin", "pr")
	headSHA := testGitSHA(t, tmpWork, "HEAD")

	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "renamecopy", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: clones})

	seedPR(t, database, "acme", "renamecopy", 1)
	ctx := context.Background()
	repoID, err := database.UpsertRepo(ctx, "github.com", "acme", "renamecopy")
	req.NoError(err)
	req.NoError(database.UpdateDiffSHAs(ctx, repoID, 1, headSHA, mergeBase, mergeBase))

	client := setupTestClient(t, srv)

	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "renamecopy", 1, nil,
	)
	req.NoError(err)
	req.Equal(http.StatusOK, diffResp.StatusCode())
	req.NotNil(diffResp.JSON200.Files)
	diffByPath := make(map[string]generated.DiffFile, len(*diffResp.JSON200.Files))
	for _, f := range *diffResp.JSON200.Files {
		diffByPath[f.Path] = f
	}
	renameDiff, ok := diffByPath["rename_dst.go"]
	req.True(ok, "rename_dst.go missing from full diff")
	req.Equal("renamed", renameDiff.Status)
	copyDiff, ok := diffByPath["copy_dst.go"]
	req.True(ok, "copy_dst.go missing from full diff")
	req.Equal("copied", copyDiff.Status)

	cases := []struct {
		name       string
		newPath    string
		oldPath    string
		wantStatus string
		want       generated.DiffFile
	}{
		{"rename", "rename_dst.go", "rename_src.go", "renamed", renameDiff},
		{"copy", "copy_dst.go", "copy_src.go", "copied", copyDiff},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require := require.New(t)
			assert := Assert.New(t)
			paths := []string{c.newPath, c.oldPath}
			body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
				ExpectFrom: mergeBase,
				ExpectTo:   headSHA,
				Paths:      &paths,
			}
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
				ctx, "acme", "renamecopy", 1, body,
			)
			require.NoError(err)
			require.Equal(http.StatusOK, resp.StatusCode())
			require.NotNil(resp.JSON200.Files)
			got := *resp.JSON200.Files
			require.Len(got, 1, "expected exactly one result for %s", c.name)
			assert.Equal(c.wantStatus, got[0].Status)
			assert.Equal(c.oldPath, got[0].OldPath)
			assert.Equal(c.want, got[0])
		})
	}
}

// TestAPIDiffHunks_LocalRename pins the same rename behavior for a local
// worktree scope: the client sends [new_path, old_path], and the server
// must report it as a rename (not add+delete) with hunks matching /diff.
func TestAPIDiffHunks_LocalRename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "rename_src.go"),
		[]byte("package main\n\nfunc Old() {}\n"), 0o644))
	runGitWT(t, dir, "add", "rename_src.go")
	runGitWT(t, dir, "commit", "-m", "base")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")

	runGitWT(t, dir, "checkout", "-b", "feat/rename")
	runGitWT(t, dir, "mv", "rename_src.go", "rename_dst.go")
	require.NoError(os.WriteFile(filepath.Join(dir, "rename_dst.go"),
		[]byte("package main\n\nfunc Old() {}\n\nfunc New() {}\n"), 0o644))
	runGitWT(t, dir, "add", "-A")
	runGitWT(t, dir, "commit", "-m", "rename and edit")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "feat/rename",
	})
	require.NoError(err)

	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)
	var renameDiff *generated.DiffFile
	for i, f := range *diffResp.JSON200.Files {
		if f.Path == "rename_dst.go" {
			renameDiff = &(*diffResp.JSON200.Files)[i]
		}
	}
	require.NotNil(renameDiff, "rename_dst.go missing from full diff")
	require.Equal("renamed", renameDiff.Status)

	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())

	paths := []string{"rename_dst.go", "rename_src.go"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		ExpectFrom: filesResp.JSON200.DiffFrom,
		ExpectTo:   filesResp.JSON200.DiffTo,
		Paths:      &paths,
	}
	hunksResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hunksResp.StatusCode())
	require.NotNil(hunksResp.JSON200.Files)
	got := *hunksResp.JSON200.Files
	require.Len(got, 1)
	assert.Equal("renamed", got[0].Status)
	assert.Equal("rename_src.go", got[0].OldPath)
	assert.Equal(*renameDiff, got[0])
}

// TestAPIDiffHunks_LocalRefScopeResolvesToSHA covers a local commit or
// span scope given a branch name instead of a SHA: diff_to must be the
// resolved SHA, so a hunk request pinned to it 409s once the branch moves
// (here an amend, which keeps the same parent and so the same diff_from).
func TestAPIDiffHunks_LocalRefScopeResolvesToSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base")
	baseSHA := testGitSHA(t, dir, "HEAD")
	runGitWT(t, dir, "checkout", "-b", "feat/x")
	require.NoError(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feat\n"), 0o644))
	runGitWT(t, dir, "add", "feature.txt")
	runGitWT(t, dir, "commit", "-m", "add feature")
	commitSHA := testGitSHA(t, dir, "HEAD")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "feat/x",
	})
	require.NoError(err)

	ref := "feat/x"
	commitScope := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{Commit: &ref}
	spanScope := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{From: &commitSHA, To: &ref}
	commitResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, commitScope,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, commitResp.StatusCode())
	assert.Equal(baseSHA, commitResp.JSON200.DiffFrom)
	assert.Equal(commitSHA, commitResp.JSON200.DiffTo)

	spanResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, spanScope,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, spanResp.StatusCode())
	assert.Equal(commitSHA, spanResp.JSON200.DiffTo)

	paths := []string{"feature.txt"}
	body := generated.PostReposByOwnerByNamePullsByNumberDiffHunksJSONRequestBody{
		Commit:     &ref,
		ExpectFrom: commitResp.JSON200.DiffFrom,
		ExpectTo:   commitResp.JSON200.DiffTo,
		Paths:      &paths,
	}
	okResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, okResp.StatusCode())

	// Amend: same parent, new commit. The branch name is unchanged but
	// now resolves to a different SHA.
	require.NoError(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feat v2\n"), 0o644))
	runGitWT(t, dir, "add", "feature.txt")
	runGitWT(t, dir, "commit", "--amend", "-m", "add feature v2")

	staleResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberDiffHunksWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	assert.Equal(http.StatusConflict, staleResp.StatusCode())
}
