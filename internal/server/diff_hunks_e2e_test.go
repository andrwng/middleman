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
