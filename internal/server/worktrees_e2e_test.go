package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func TestAPIListWorktreesEmpty(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.GetWorktreesWithResponse(context.Background())
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.Worktrees)
	require.Empty(*resp.JSON200.Worktrees)
}

func TestAPIListWorktreesReturnsActiveAcrossRepos(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	repoA := insertTestRepoLocal(t, database, "alpha", "one")
	repoB := insertTestRepoLocal(t, database, "beta", "two")

	_, err := database.UpsertWorktree(ctx, repoA, db.ScannedWorktree{
		Path: "/code/alpha-a", Branch: "feat/a", HeadSHA: "aaaa1111",
	})
	require.NoError(err)
	_, err = database.UpsertWorktree(ctx, repoA, db.ScannedWorktree{
		Path: "/code/alpha-b", Branch: "feat/b", HeadSHA: "bbbb2222", IsLocked: true,
	})
	require.NoError(err)
	_, err = database.UpsertWorktree(ctx, repoB, db.ScannedWorktree{
		Path: "/code/beta-a", Branch: "main", HeadSHA: "cccc3333",
	})
	require.NoError(err)

	// Mark one of repoA's worktrees as removed — should not appear.
	require.NoError(database.MarkWorktreesNotInSet(ctx, repoA, []string{"/code/alpha-a"}))

	resp, err := client.HTTP.GetWorktreesWithResponse(ctx)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.Worktrees)
	got := *resp.JSON200.Worktrees
	require.Len(got, 2)

	assert.Equal("alpha", got[0].RepoOwner)
	assert.Equal("one", got[0].RepoName)
	assert.Equal("/code/alpha-a", got[0].Path)
	require.NotNil(got[0].Branch)
	assert.Equal("feat/a", *got[0].Branch)
	require.NotNil(got[0].HeadSha)
	assert.Equal("aaaa1111", *got[0].HeadSha)

	assert.Equal("beta", got[1].RepoOwner)
	assert.Equal("/code/beta-a", got[1].Path)

	for _, w := range got {
		assert.NotEmpty(w.DiscoveredAt)
		assert.NotEmpty(w.LastSeenAt)
	}
}

func TestAPIWorktreeChangedFilesAgainstBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	// Working repo on `main` plus a bare origin to give us
	// `origin/main` for the base resolver to match against.
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

	// Diverge: one committed change + one uncommitted modification.
	runGitWT(t, dir, "checkout", "-b", "feat/x")
	require.NoError(os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feat\n"), 0o644))
	runGitWT(t, dir, "add", "feature.txt")
	runGitWT(t, dir, "commit", "-m", "add feature")
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

	resp, err := client.HTTP.GetWorktreesByIdChangedFilesWithResponse(ctx, w.ID)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	assert.Equal("origin/main", resp.JSON200.Base.Ref)
	assert.False(resp.JSON200.Base.Fallback != nil && *resp.JSON200.Base.Fallback)

	require.NotNil(resp.JSON200.Files)
	got := *resp.JSON200.Files
	require.Len(got, 2)
	paths := map[string]string{}
	for _, f := range got {
		paths[f.Path] = f.Status
	}
	assert.Equal("modified", paths["base.txt"])
	assert.Equal("added", paths["feature.txt"])
}

func TestAPIWorktreeDiffReturnsHunks(t *testing.T) {
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
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("a\nb\n"), 0o644))
	runGitWT(t, dir, "add", "hello.txt")
	runGitWT(t, dir, "commit", "-m", "init")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")
	// Diverge: add one line to the existing file.
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("a\nb\nc\n"), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	resp, err := client.HTTP.GetWorktreesByIdDiffWithResponse(ctx, w.ID)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	assert.Equal("origin/main", resp.JSON200.Base.Ref)
	require.NotNil(resp.JSON200.Files)
	files := *resp.JSON200.Files
	require.Len(files, 1)
	assert.Equal("hello.txt", files[0].Path)
	require.NotNil(files[0].Hunks)
	require.NotEmpty(*files[0].Hunks)
}

func TestAPIWorktreeChangedFilesNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	resp, err := client.HTTP.GetWorktreesByIdChangedFilesWithResponse(
		context.Background(), 99999,
	)
	require.NoError(t, err)
	Assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

func TestAPILocalDispatchPRRoutes(t *testing.T) {
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
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("a\nb\n"), 0o644))
	runGitWT(t, dir, "add", "hello.txt")
	runGitWT(t, dir, "commit", "-m", "init")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")
	// Diverge so the diff has content.
	require.NoError(os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("a\nb\nc\n"), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	// /repos/local/demo/pulls/{id} (detail)
	pullResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, pullResp.StatusCode())
	require.NotNil(pullResp.JSON200)
	assert.Equal("local", pullResp.JSON200.RepoOwner)
	assert.Equal("demo", pullResp.JSON200.RepoName)

	// /repos/local/demo/pulls/{id}/diff (full diff)
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200)
	require.NotNil(diffResp.JSON200.Files)
	require.Len(*diffResp.JSON200.Files, 1)

	// /repos/local/demo/pulls/{id}/files (lightweight list)
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200)
	require.NotNil(filesResp.JSON200.Files)
	require.Len(*filesResp.JSON200.Files, 1)
	// Files endpoint strips hunks.
	if (*filesResp.JSON200.Files)[0].Hunks != nil {
		assert.Empty(*(*filesResp.JSON200.Files)[0].Hunks)
	}
}

func runGitWT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
}

func insertTestRepoLocal(t *testing.T, d *db.DB, owner, name string) int64 {
	t.Helper()
	res, err := d.WriteDB().ExecContext(
		context.Background(),
		`INSERT INTO middleman_repos (platform, platform_host, owner, name) VALUES ('github', 'github.com', ?, ?)`,
		owner, name,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
