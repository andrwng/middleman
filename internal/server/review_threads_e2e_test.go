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
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(t, err)
	Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
}
