package server

import (
	"context"
	"net/http"
	"os/exec"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

// seedReviewWorktreeGit registers a local repo + a worktree backed by a
// REAL git repo (so the server's currentWorktreeBranch can read a live
// branch). Returns the worktree id (PR-shaped "number") and its on-disk
// path so the test can switch branches.
func seedReviewWorktreeGit(t *testing.T, database *db.DB) (int64, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=feat/a", dir)
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: dir, Branch: "feat/a", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	return w.ID, dir
}

func TestAPIReviewThreadsBranchScoped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)

	// Create one thread while on feat/a.
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 1, CommitSha: "abc", Body: "on a"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())

	// Listing on feat/a sees the thread.
	listA, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, listA.StatusCode())
	require.NotNil(listA.JSON200)
	require.NotNil(listA.JSON200.Threads)
	assert.Len(*listA.JSON200.Threads, 1)

	// Switch the worktree to feat/b.
	runGit(t, dir, "checkout", "-b", "feat/b")

	// Now listing returns the feat/b set (empty) — the feat/a thread is
	// scoped out.
	listB, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, listB.StatusCode())
	require.NotNil(listB.JSON200)
	threadsB := []generated.ReviewThreadResponse{}
	if listB.JSON200.Threads != nil {
		threadsB = *listB.JSON200.Threads
	}
	assert.Empty(threadsB)

	// Creating on feat/b, then switching back to feat/a, shows the
	// original feat/a thread again (and not the feat/b one).
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "b.go", Side: "RIGHT", Line: 2, CommitSha: "abc", Body: "on b"},
			},
		},
	)
	require.NoError(err)
	runGit(t, dir, "checkout", "feat/a")
	listA2, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num)
	require.NoError(err)
	require.NotNil(listA2.JSON200)
	require.NotNil(listA2.JSON200.Threads)
	paths := make([]string, 0)
	for _, th := range *listA2.JSON200.Threads {
		paths = append(paths, th.Path)
	}
	assert.Equal([]string{"a.go"}, paths)
}
