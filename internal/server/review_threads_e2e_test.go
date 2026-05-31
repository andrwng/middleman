package server

import (
	"context"
	"net/http"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

// seedReviewWorktree registers a local repo + worktree row and returns its
// id (the "number" in PR-shaped local routes). No real git tree is needed:
// the review-thread routes only resolve the synthetic MR.
func seedReviewWorktree(t *testing.T, database *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: t.TempDir(), Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	return w.ID
}

func TestAPIReviewThreadsLifecycle(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	start := int64(8)
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc123", Body: "rename this"},
				{Path: "b.go", Side: "RIGHT", Line: 20, StartLine: &start, CommitSha: "abc123", Body: "extract a helper"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 2)
	assert.Equal("open", created[0].Status)
	require.NotNil(created[0].Comments)
	require.Len(*created[0].Comments, 1)
	assert.Equal("user", (*created[0].Comments)[0].Author)
	assert.Equal("rename this", (*created[0].Comments)[0].Body)
	// Second thread round-trips its multi-line anchor (start_line).
	assert.Equal("b.go", created[1].Path)
	require.NotNil(created[1].StartLine)
	assert.Equal(int64(8), *created[1].StartLine)
	threadID := created[0].Id

	// List returns both threads.
	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200)
	require.NotNil(listResp.JSON200.Threads)
	require.Len(*listResp.JSON200.Threads, 2)

	// Reply as the agent.
	agent := "agent"
	replyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AddReviewThreadCommentInputBody{Body: "agreed, will rename", Author: &agent},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, replyResp.StatusCode())
	require.NotNil(replyResp.JSON200)
	require.NotNil(replyResp.JSON200.Comments)
	require.Len(*replyResp.JSON200.Comments, 2)
	assert.Equal("agent", (*replyResp.JSON200.Comments)[1].Author)

	// Hide.
	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hideResp.StatusCode())
	require.NotNil(hideResp.JSON200)
	assert.True(hideResp.JSON200.Hidden)

	// Unhide.
	unhideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdUnhideWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, unhideResp.StatusCode())
	require.NotNil(unhideResp.JSON200)
	assert.False(unhideResp.JSON200.Hidden)

	// Resolve.
	resolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resolveResp.StatusCode())
	require.NotNil(resolveResp.JSON200)
	assert.Equal("resolved", resolveResp.JSON200.Status)
}

func TestAPIReviewThreadsRejectNonLocal(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	// GET on a non-local owner is rejected.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, getResp.StatusCode())

	// POST (create) on a non-local owner is rejected by the same guard.
	postResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "acme", "widget", 1,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 1, CommitSha: "abc", Body: "x"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, postResp.StatusCode())
}

// TestAPIReviewThreadActionUnknownThread covers the ownership guard: an
// action on a thread id that does not belong to this worktree is a 404.
func TestAPIReviewThreadActionUnknownThread(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	num := seedReviewWorktree(t, database)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse(
		context.Background(), "local", "demo", num, 99999,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())
}

func TestAPIReviewThreadDelete(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
				{Path: "b.go", Side: "RIGHT", Line: 20, CommitSha: "abc", Body: "extract"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	created := *createResp.JSON200.Threads
	require.Len(created, 2)
	threadID := created[0].Id

	delResp, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, delResp.StatusCode())
	require.NotNil(delResp.JSON200)
	require.NotNil(delResp.JSON200.Threads)
	require.Len(*delResp.JSON200.Threads, 1)
	require.Equal(created[1].Id, (*delResp.JSON200.Threads)[0].Id)

	delAgain, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, delAgain.StatusCode())
}
