package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/testutil"
)

// assertFilesSummaryMatchesDiff checks that the lightweight /files summary
// agrees with the full /diff response on file set, statuses, and line
// counts for the same scope. Hunks are deliberately not compared: /files
// never populates them.
func assertFilesSummaryMatchesDiff(t *testing.T, diffFiles, summaryFiles []generated.DiffFile) {
	t.Helper()
	assert := Assert.New(t)
	require.Len(t, summaryFiles, len(diffFiles))

	byPath := make(map[string]generated.DiffFile, len(diffFiles))
	for _, f := range diffFiles {
		byPath[f.Path] = f
	}
	for _, sf := range summaryFiles {
		df, ok := byPath[sf.Path]
		if !assert.Truef(ok, "summary file %q not present in full diff", sf.Path) {
			continue
		}
		assert.Equal(df.Status, sf.Status, "status mismatch for %s", sf.Path)
		assert.Equal(df.OldPath, sf.OldPath, "old_path mismatch for %s", sf.Path)
		assert.Equal(df.Additions, sf.Additions, "additions mismatch for %s", sf.Path)
		assert.Equal(df.Deletions, sf.Deletions, "deletions mismatch for %s", sf.Path)
		assert.Equal(df.IsBinary, sf.IsBinary, "is_binary mismatch for %s", sf.Path)
		assert.Equal(df.IsWhitespaceOnly, sf.IsWhitespaceOnly, "is_whitespace_only mismatch for %s", sf.Path)
	}
}

// --- PR-backed scopes ---

func TestAPIFilesSummary_PRHeadScope(t *testing.T) {
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

	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "acme", "widget", 1, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	assert.Equal(mergeBase, filesResp.JSON200.DiffFrom)
	assert.Equal(headSHA, filesResp.JSON200.DiffTo)
	assert.Equal(diffResp.JSON200.Stale, filesResp.JSON200.Stale)
}

func TestAPIFilesSummary_PRCommitScope(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _, _, commitSHAs := setupTestServerWithClones(t)
	ctx := context.Background()

	commit := commitSHAs[2] // some commit in the middle of the stack

	diffParams := &generated.GetReposByOwnerByNamePullsByNumberDiffParams{Commit: &commit}
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "widget", 1, diffParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)

	filesParams := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{Commit: &commit}
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "acme", "widget", 1, filesParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	assert.Equal(commit, filesResp.JSON200.DiffTo)
	assert.NotEmpty(filesResp.JSON200.DiffFrom)
	assert.NotEqual(commit, filesResp.JSON200.DiffFrom)
}

func TestAPIFilesSummary_PRFromToScope(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _, _, commitSHAs := setupTestServerWithClones(t)
	ctx := context.Background()

	// commitSHAs is newest-first; from must be older (higher index) than to.
	from := commitSHAs[3]
	to := commitSHAs[1]

	diffParams := &generated.GetReposByOwnerByNamePullsByNumberDiffParams{From: &from, To: &to}
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "widget", 1, diffParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)

	filesParams := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{From: &from, To: &to}
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "acme", "widget", 1, filesParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	assert.Equal(to, filesResp.JSON200.DiffTo)
	assert.NotEmpty(filesResp.JSON200.DiffFrom)
}

func TestAPIFilesSummary_PRWhitespaceHide(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	seedPR(t, database, "acme", "widgets", 1)
	diffRepo, err := testutil.SetupDiffRepo(context.Background(), dir, database)
	require.NoError(err)

	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widgets", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, diffRepo.Manager, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: diffRepo.Manager})
	client := setupTestClient(t, srv)
	ctx := context.Background()

	hide := "hide"
	diffParams := &generated.GetReposByOwnerByNamePullsByNumberDiffParams{Whitespace: &hide}
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "acme", "widgets", 1, diffParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)

	filesParams := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{Whitespace: &hide}
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "acme", "widgets", 1, filesParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	// Task 2b fixed gitclone.Diff's ParsePatch to match raw entries to
	// patch sections by path instead of positional index, so /diff's
	// per-file counts are no longer misaligned when --raw -w still lists a
	// whitespace-only file (a documented git 2.43 quirk) while the patch
	// pass omits its now-empty hunk. The two endpoints' per-file counts are
	// directly comparable again in hide mode.
	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	// README.md is whitespace-only and must be hidden from both.
	for _, f := range *filesResp.JSON200.Files {
		assert.NotEqual("README.md", f.Path)
	}
	assert.Equal(diffResp.JSON200.WhitespaceOnlyCount, filesResp.JSON200.WhitespaceOnlyCount)
	assert.Equal(int64(1), filesResp.JSON200.WhitespaceOnlyCount)
}

func TestAPIFilesSummary_PRInvalidScopeReturns400(t *testing.T) {
	require := require.New(t)
	client, _, _, headSHA, _ := setupTestServerWithClones(t)

	// commit combined with from/to is an invalid scope, exactly as /diff
	// rejects it.
	params := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{
		Commit: &headSHA,
		From:   &headSHA,
		To:     &headSHA,
	}
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		context.Background(), "acme", "widget", 1, params,
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())
}

// --- Local worktree scopes ---

func TestAPIFilesSummary_LocalAgainstBaseWithUncommittedEdit(t *testing.T) {
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

	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)

	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	require.Len(*filesResp.JSON200.Files, 2)

	assert.Equal(baseSHA, filesResp.JSON200.DiffFrom)
	assert.Equal("", filesResp.JSON200.DiffTo)
	assert.Equal(int64(0), filesResp.JSON200.WhitespaceOnlyCount)
}

func TestAPIFilesSummary_LocalCommitScope(t *testing.T) {
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
	parentSHA := testGitSHA(t, dir, "HEAD^")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "feat/x",
	})
	require.NoError(err)

	diffParams := &generated.GetReposByOwnerByNamePullsByNumberDiffParams{Commit: &commitSHA}
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID, diffParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200.Files)

	filesParams := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{Commit: &commitSHA}
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, filesParams,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200.Files)

	assertFilesSummaryMatchesDiff(t, *diffResp.JSON200.Files, *filesResp.JSON200.Files)
	require.Len(*filesResp.JSON200.Files, 1)
	assert.Equal("feature.txt", (*filesResp.JSON200.Files)[0].Path)
	assert.Equal(parentSHA, filesResp.JSON200.DiffFrom)
	assert.Equal(commitSHA, filesResp.JSON200.DiffTo)
}

func TestAPIFilesSummary_LocalInvalidScopeReturns400(t *testing.T) {
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
	commitSHA := testGitSHA(t, dir, "HEAD")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	params := &generated.GetReposByOwnerByNamePullsByNumberFilesParams{
		Commit: &commitSHA,
		From:   &commitSHA,
		To:     &commitSHA,
	}
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID, params,
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())

	// Same invalid combo on /diff for parity.
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberDiffParams{
			Commit: &commitSHA,
			From:   &commitSHA,
			To:     &commitSHA,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, diffResp.StatusCode())
}
