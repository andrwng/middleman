package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gh "github.com/google/go-github/v84/github"
	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	ghclient "github.com/wesm/middleman/internal/github"
)

func seedDrafts(t *testing.T, client *apiclient.Client, ids ...string) {
	t.Helper()
	drafts := make([]generated.ReviewDraftInput, 0, len(ids))
	for i, id := range ids {
		drafts = append(drafts, draftIn(id, "a.go", int64(i+1), "body "+id))
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(context.Background(), "acme", "widget", 1,
		generated.CreateReviewDraftsInputBody{Drafts: &drafts})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
}

func TestAPIPublishReviewDrafts_PostsAllAndClears(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	mock := &mockGH{}
	srv, database := setupTestServerWithMock(t, mock)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)
	seedDrafts(t, client, "p-1", "p-2")

	body := "Overall LGTM"
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT", Body: &body})
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	assert.Equal(int64(2), resp.JSON200.Published)
	require.Len(mock.lastInlineComments, 2)
	assert.Equal("body p-1", mock.lastInlineComments[0].Body)
	assert.Equal("abc", mock.lastInlineComments[0].CommitID)
	require.NotNil(mock.lastCreateReviewOpts)
	assert.Equal("Overall LGTM", mock.lastCreateReviewOpts.Body)

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	assert.Empty(*list.JSON200.Drafts)
	assert.Empty(list.JSON200.Meta.Event)
}

func TestAPIPublishReviewDrafts_PartialFailure(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	calls := 0
	mock := &mockGH{}
	mock.createInlineCommentFn = func(_ context.Context, _, _ string, _ int, opts ghclient.InlineCommentOpts) (*gh.PullRequestComment, error) {
		calls++
		if opts.Body == "body p-2" && calls == 2 {
			return nil, errors.New("Line could not be resolved")
		}
		id := int64(calls)
		return &gh.PullRequestComment{ID: &id}, nil
	}
	srv, database := setupTestServerWithMock(t, mock)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)
	seedDrafts(t, client, "p-1", "p-2", "p-3")

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT"})
	require.NoError(err)
	assert.Equal(http.StatusConflict, resp.StatusCode())
	assert.Nil(mock.lastCreateReviewOpts) // no wrapper after a failure

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	drafts := *list.JSON200.Drafts
	require.Len(drafts, 2) // p-1 was posted and removed
	assert.Equal("p-2", drafts[0].Id)
	require.NotNil(drafts[0].SubmitError)
	assert.Contains(*drafts[0].SubmitError, "could not be resolved")
	assert.Equal("p-3", drafts[1].Id)

	// Retry posts only what is left.
	retry, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "acme", "widget", 1,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT"})
	require.NoError(err)
	require.Equal(http.StatusOK, retry.StatusCode())
	assert.Equal(int64(2), retry.JSON200.Published)
	assert.Equal(4, calls) // 2 in the first attempt (p-1 ok, p-2 fail), 2 on retry
}

func TestAPIPublishReviewDrafts_RejectsLocalAndBadEvent(t *testing.T) {
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	cases := []struct {
		name, owner, event string
	}{
		{"local owner", "local", "COMMENT"},
		{"bad event", "acme", "MAYBE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, tc.owner, "widget", 1,
				generated.PublishReviewDraftsInputBody{Event: tc.event})
			require.NoError(t, err)
			Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}
}
