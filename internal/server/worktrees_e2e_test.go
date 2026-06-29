package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/gitclone"
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

	// Set up: origin/main has one commit. The worktree branches off
	// and adds a committed change AND an uncommitted change.
	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base commit")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")
	// Diverge with one committed file plus one uncommitted edit.
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

	pullResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, pullResp.StatusCode())
	require.NotNil(pullResp.JSON200)
	assert.Equal("local", pullResp.JSON200.RepoOwner)
	assert.Equal("demo", pullResp.JSON200.RepoName)

	// Default scope: base vs working tree → committed work AND
	// uncommitted edit both appear; this is the full draft view.
	diffResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, diffResp.StatusCode())
	require.NotNil(diffResp.JSON200)
	require.NotNil(diffResp.JSON200.Files)
	defaultFiles := *diffResp.JSON200.Files
	require.Len(defaultFiles, 2)
	defaultPaths := []string{defaultFiles[0].Path, defaultFiles[1].Path}
	assert.ElementsMatch([]string{"base.txt", "feature.txt"}, defaultPaths)

	// ?commit=WORKING-TREE → only the uncommitted edit.
	wtCommit := "WORKING-TREE"
	wtResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberDiffParams{Commit: &wtCommit},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, wtResp.StatusCode())
	require.NotNil(wtResp.JSON200)
	require.NotNil(wtResp.JSON200.Files)
	wtFiles := *wtResp.JSON200.Files
	require.Len(wtFiles, 1)
	assert.Equal("base.txt", wtFiles[0].Path)

	// Files endpoint matches default diff scope — both files appear.
	filesResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberFilesWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, filesResp.StatusCode())
	require.NotNil(filesResp.JSON200)
	require.NotNil(filesResp.JSON200.Files)
	require.Len(*filesResp.JSON200.Files, 2)

	// Commits endpoint: the working-tree sentinel is prepended.
	commitsResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCommitsWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, commitsResp.StatusCode())
	require.NotNil(commitsResp.JSON200)
	require.NotNil(commitsResp.JSON200.Commits)
	commits := *commitsResp.JSON200.Commits
	require.NotEmpty(commits)
	assert.Equal("WORKING-TREE", commits[0].Sha)
	assert.Equal("Uncommitted changes", commits[0].Message)
}

// TestAPILocalDispatchBlobServesWorktreeFiles pins the bug fix where
// the rendered-markdown view in the diff sidebar 502'd for local
// worktrees. The /blob endpoint used to go through the bare-clone
// manager unconditionally; for platform=local repos the clone path
// doesn't exist, so git cat-file would fail and middleman returned
// HTTP 502 "read blob: cat-file ...: ...".
//
// Two scopes matter for worktrees:
//   - real commit SHA → read via git cat-file in the worktree's .git
//   - WORKING-TREE sentinel → read straight from disk (uncommitted)
func TestAPILocalDispatchBlobServesWorktreeFiles(t *testing.T) {
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

	committedMD := "# Committed\n\nThis is the committed body.\n"
	require.NoError(os.WriteFile(filepath.Join(dir, "doc.md"), []byte(committedMD), 0o644))
	runGitWT(t, dir, "add", "doc.md")
	runGitWT(t, dir, "commit", "-m", "add doc")

	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := string(headOut[:len(headOut)-1]) // strip trailing newline

	// Overwrite on disk so the working tree differs from HEAD.
	// WORKING-TREE reads should see this content; HEAD-SHA reads
	// should still see the committed version.
	workingMD := "# Working\n\nUncommitted edit.\n"
	require.NoError(os.WriteFile(filepath.Join(dir, "doc.md"), []byte(workingMD), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	// 1. Committed content via HEAD SHA.
	docPath := "doc.md"
	headResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobParams{
			Path: &docPath,
			Sha:  &headSHA,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, headResp.StatusCode())
	require.NotNil(headResp.JSON200)
	assert.Equal(committedMD, headResp.JSON200.Content)
	assert.False(headResp.JSON200.Truncated)

	// 2. Working-tree content via WORKING-TREE sentinel.
	wtSentinel := "WORKING-TREE"
	wtResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobParams{
			Path: &docPath,
			Sha:  &wtSentinel,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, wtResp.StatusCode())
	require.NotNil(wtResp.JSON200)
	assert.Equal(workingMD, wtResp.JSON200.Content)
	assert.False(wtResp.JSON200.Truncated)

	// 3. Missing path: 404 (not 502 — distinguishes user error
	// from server-side breakage).
	missingPath := "does-not-exist.md"
	missResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobParams{
			Path: &missingPath,
			Sha:  &headSHA,
		},
	)
	require.NoError(err)
	assert.Equal(http.StatusNotFound, missResp.StatusCode())
}

// TestAPILocalDispatchBlobRangeServesWorktreeFiles pins the same
// fix shape as TestAPILocalDispatchBlobServesWorktreeFiles for the
// click-to-expand affordance: GET /blob-range against a local
// worktree must read from the worktree's git dir / on-disk state,
// not the (non-existent) bare clone partition.
func TestAPILocalDispatchBlobRangeServesWorktreeFiles(t *testing.T) {
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

	committed := "alpha\nbravo\ncharlie\ndelta\necho\n"
	require.NoError(os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(committed), 0o644))
	runGitWT(t, dir, "add", "lines.txt")
	runGitWT(t, dir, "commit", "-m", "add lines")

	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := string(headOut[:len(headOut)-1])

	working := "alpha\nbravo\ncharlie\nDELTA\nfoxtrot\n"
	require.NoError(os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(working), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	linesPath := "lines.txt"
	startTwo, endFour := int64(2), int64(4)

	// 1. Committed slice via HEAD SHA: lines 2..4.
	headResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobRangeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobRangeParams{
			Path:  &linesPath,
			Sha:   &headSHA,
			Start: &startTwo,
			End:   &endFour,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, headResp.StatusCode())
	require.NotNil(headResp.JSON200)
	require.NotNil(headResp.JSON200.Lines)
	assert.Equal([]string{"bravo", "charlie", "delta"}, *headResp.JSON200.Lines)

	// 2. Working-tree slice via WORKING-TREE sentinel: same range,
	// different content (line 4 is "DELTA", line 5 is "foxtrot").
	wtSentinel := "WORKING-TREE"
	wtResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobRangeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobRangeParams{
			Path:  &linesPath,
			Sha:   &wtSentinel,
			Start: &startTwo,
			End:   &endFour,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, wtResp.StatusCode())
	require.NotNil(wtResp.JSON200)
	require.NotNil(wtResp.JSON200.Lines)
	assert.Equal([]string{"bravo", "charlie", "DELTA"}, *wtResp.JSON200.Lines)

	// 3. Range past EOF clamps to available lines.
	startOne, endTen := int64(1), int64(10)
	pastResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberBlobRangeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberBlobRangeParams{
			Path:  &linesPath,
			Sha:   &headSHA,
			Start: &startOne,
			End:   &endTen,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, pastResp.StatusCode())
	require.NotNil(pastResp.JSON200)
	require.NotNil(pastResp.JSON200.Lines)
	assert.Len(*pastResp.JSON200.Lines, 5)
}

// TestAPILocalDispatchAIThreadAcceptsRangeAnchor pins the contract
// the rendered markdown view (and the diff view) rely on: the AI
// thread create endpoint accepts an anchor of {path, anchor_line,
// anchor_side, commit_sha, hunk_start_line, hunk_end_line} for a
// local worktree, persists it, and returns it on subsequent reads.
func TestAPILocalDispatchAIThreadAcceptsRangeAnchor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)

	// AI threads require the server to have a clone manager and worktree
	// dir configured (otherwise s.aiReview is nil and the endpoint returns
	// 503). For local worktrees, Runner.CreateThread uses
	// LocalWorktreePath and skips the clone manager, but the server must
	// still be initialised with a non-nil clones value. A stub fake-claude
	// binary keeps the question runner from hanging in CI.
	dir := t.TempDir()
	bareDir := filepath.Join(dir, "clones")
	require.NoError(os.MkdirAll(bareDir, 0o755))
	claudeBin := filepath.Join(dir, "claude")
	require.NoError(os.WriteFile(claudeBin, []byte(`#!/bin/sh
cat <<'EOF'
{"type":"result","subtype":"success","is_error":false,"result":"fake","session_id":"s1"}
EOF
`), 0o755))
	aireview.SetBinaryForTest(claudeBin)
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	database := openTestDB(t)
	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	syncer := ghclient.NewSyncer(
		map[string]ghclient.Client{"github.com": mock},
		database, nil,
		[]ghclient.RepoRef{{Owner: "acme", Name: "widget", PlatformHost: "github.com"}},
		time.Minute, nil, nil,
	)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{
		Clones:      clones,
		WorktreeDir: filepath.Join(dir, "worktrees"),
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	client := setupTestClient(t, srv)
	ctx := context.Background()

	repoDir := filepath.Join(dir, "repo")
	require.NoError(os.MkdirAll(repoDir, 0o755))
	runGitWT(t, "", "init", "--initial-branch=main", repoDir)
	runGitWT(t, repoDir, "config", "user.email", "test@example.com")
	runGitWT(t, repoDir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(repoDir, "doc.md"),
		[]byte("line one\nline two\nline three\n"), 0o644))
	runGitWT(t, repoDir, "add", "doc.md")
	runGitWT(t, repoDir, "commit", "-m", "init")
	headOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := string(headOut[:len(headOut)-1])

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(repoDir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	docPath := "doc.md"
	hunkStart, hunkEnd := int64(1), int64(2)
	body := generated.PostReposByOwnerByNamePullsByNumberAiThreadsJSONRequestBody{
		Path:          docPath,
		AnchorLine:    2,
		AnchorSide:    "RIGHT",
		CommitSha:     headSHA,
		HunkStartLine: &hunkStart,
		HunkEndLine:   &hunkEnd,
		Question:      "What does this section say?",
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiThreadsWithResponse(
		ctx, "local", "demo", w.ID, body,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)

	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberAiThreadsWithResponse(
		ctx, "local", "demo", w.ID, nil,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200)
	require.NotNil(listResp.JSON200.Threads)
	threads := *listResp.JSON200.Threads
	require.Len(threads, 1)
	assert.Equal(docPath, threads[0].Path)
	assert.EqualValues(2, threads[0].AnchorLine)
	assert.Equal("RIGHT", threads[0].AnchorSide)
}

func TestAPILocalCommitsIncludeBranchHeads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)

	// Base commit on main, published to a bare origin so ResolveBase
	// finds origin/main and the feature commits fall in range.
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

	// Worktree branch 'feature', two commits ahead of origin/main.
	runGitWT(t, dir, "checkout", "-b", "feature")
	require.NoError(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644))
	runGitWT(t, dir, "add", "a.txt")
	runGitWT(t, dir, "commit", "-m", "add a")
	midOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	midSHA := strings.TrimSpace(string(midOut))
	require.NoError(os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644))
	runGitWT(t, dir, "add", "b.txt")
	runGitWT(t, dir, "commit", "-m", "add b")
	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := strings.TrimSpace(string(headOut))

	// Two more local branches point at the middle commit. The current
	// branch ('feature', at HEAD) must not be attributed to its own tip.
	runGitWT(t, dir, "branch", "stack/part-1", midSHA)
	runGitWT(t, dir, "branch", "spike", midSHA)

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:    canonDir,
		Branch:  "feature",
		HeadSHA: headSHA,
	})
	require.NoError(err)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCommitsWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(200, resp.StatusCode())
	require.NotNil(resp.JSON200)
	commits := *resp.JSON200.Commits
	require.Len(commits, 2) // feature is two commits ahead of origin/main

	var midHeads, headHeads *[]string
	for i := range commits {
		switch commits[i].Sha {
		case midSHA:
			midHeads = commits[i].BranchHeads
		case headSHA:
			headHeads = commits[i].BranchHeads
		}
	}
	require.NotNil(midHeads)
	assert.Equal([]string{"spike", "stack/part-1"}, *midHeads)
	assert.Nil(headHeads) // 'feature' (current branch) is excluded
}

func TestGetWorktreeMarkdownFiles(t *testing.T) {
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

	// Tracked markdown files: README.md at root and notes/x.md nested.
	require.NoError(os.MkdirAll(filepath.Join(dir, "notes"), 0o755))
	require.NoError(os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Root\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(dir, "notes", "x.md"), []byte("# X\n"), 0o644))
	// Non-markdown file — must not appear in results.
	require.NoError(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))
	runGitWT(t, dir, "add", "README.md", "notes/x.md", "main.go")
	runGitWT(t, dir, "commit", "-m", "initial")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	// Happy path: two tracked markdown files, sorted.
	resp, err := client.HTTP.GetWorktreesByIdMarkdownFilesWithResponse(ctx, w.ID)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.Files)
	assert.Equal([]string{"README.md", "notes/x.md"}, *resp.JSON200.Files)

	// 404 for a worktree that was never inserted.
	notFoundResp, err := client.HTTP.GetWorktreesByIdMarkdownFilesWithResponse(ctx, 9999999)
	require.NoError(err)
	assert.Equal(http.StatusNotFound, notFoundResp.StatusCode())

	// 404 for a worktree that exists in the DB but is marked removed.
	require.NoError(database.MarkWorktreesNotInSet(ctx, repoID, []string{}))
	removedResp, err := client.HTTP.GetWorktreesByIdMarkdownFilesWithResponse(ctx, w.ID)
	require.NoError(err)
	assert.Equal(http.StatusNotFound, removedResp.StatusCode())
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
