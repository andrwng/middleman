package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

// seedReviewComment is one comment to insert via seedReviewComments.
// ID is the GitHub platform comment id; InReplyTo of 0 marks a root.
type seedReviewComment struct {
	ID        int64
	InReplyTo int64
	CreatedAt time.Time
}

func seedReviewComments(t *testing.T, database *db.DB, mrID int64, items []seedReviewComment) {
	t.Helper()
	events := make([]db.MREvent, 0, len(items))
	for _, it := range items {
		id := it.ID
		meta := `{"path":"f.go","line":1,"side":"RIGHT"}`
		if it.InReplyTo != 0 {
			meta = `{"path":"f.go","line":1,"side":"RIGHT","in_reply_to":` +
				strconv.FormatInt(it.InReplyTo, 10) + `}`
		}
		events = append(events, db.MREvent{
			MergeRequestID: mrID,
			PlatformID:     &id,
			EventType:      "review_comment",
			Author:         "reviewer",
			Body:           "comment body",
			CreatedAt:      it.CreatedAt,
			MetadataJSON:   meta,
			DedupeKey:      "review-comment-" + strconv.FormatInt(it.ID, 10),
		})
	}
	require.NoError(t, database.UpsertMREvents(context.Background(), events))
}

func TestPullDetailIncludesEmptyHiddenSetByDefault(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.HiddenThreadRootIds, "field should be present and non-nil")
	assert.Empty(*resp.JSON200.HiddenThreadRootIds)
}

// TestLocalSourcePullDetailIncludesEmptyHiddenSet pins the wire
// contract for local-source PRs: getPullLocal constructs its own
// mergeRequestDetailResponse literal (does not go through the
// shared buildPullDetailResponse helper), so it must explicitly
// emit hidden_thread_root_ids as [] rather than null. Local
// worktrees have no review comments today, so the set is always
// empty — but the field must be present and non-null.
func TestLocalSourcePullDetailIncludesEmptyHiddenSet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
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

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.HiddenThreadRootIds, "field should be present and non-nil")
	assert.Empty(*resp.JSON200.HiddenThreadRootIds)
}

func TestHideThreadAddsToActiveHiddenSet(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 1001, InReplyTo: 0, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: 1002, InReplyTo: 1001, CreatedAt: now.Add(-time.Hour)},
	})

	client := setupTestClient(t, srv)

	hideResp, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 1001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode(), string(hideResp.Body))

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	assert.ElementsMatch([]int64{1001}, *getResp.JSON200.HiddenThreadRootIds)

	// Events themselves are still in the response — the client is the
	// one that filters based on the hidden set.
	require.NotNil(getResp.JSON200.Events)
	var rootPresent, replyPresent bool
	for _, e := range *getResp.JSON200.Events {
		if e.PlatformID == nil {
			continue
		}
		switch *e.PlatformID {
		case 1001:
			rootPresent = true
		case 1002:
			replyPresent = true
		}
	}
	assert.True(rootPresent && replyPresent, "events should still include all comments")
}

func TestHideThreadIs400IfRootIsNotAReviewCommentOnPR(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 99999},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())
}

func TestHideThreadIs404ForUnknownPR(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 999,
		generated.HideReviewThreadInputBody{RootCommentId: 1},
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())
}

func TestUnhideThreadRemovesFromActiveSet(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 5001, InReplyTo: 0, CreatedAt: now.Add(-time.Hour)},
	})

	client := setupTestClient(t, srv)

	hideResp, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 5001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode())

	unhideResp, err := client.HTTP.UnhideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1, 5001,
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, unhideResp.StatusCode())

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	assert.Empty(*getResp.JSON200.HiddenThreadRootIds)

	// Idempotent — deleting again is a no-op 204.
	unhideResp2, err := client.HTTP.UnhideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1, 5001,
	)
	require.NoError(err)
	assert.Equal(http.StatusNoContent, unhideResp2.StatusCode())
}

func TestHiddenThreadAutoUnhidesOnNewReply(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	// Seed only the root, hide it, then add a later reply.
	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 7001, InReplyTo: 0, CreatedAt: now.Add(-2 * time.Hour)},
	})

	client := setupTestClient(t, srv)
	hideResp, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 7001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode())

	// Confirm it's hidden.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	require.ElementsMatch([]int64{7001}, *getResp.JSON200.HiddenThreadRootIds)

	// Read back hidden_at and stamp the reply just after it so the
	// reply unambiguously supersedes the hide. Using wall-clock-future
	// timestamps would break the re-hide step below.
	hiddenRows, err := database.ListHiddenReviewThreads(context.Background(), mrID)
	require.NoError(err)
	require.Len(hiddenRows, 1)
	replyAt := hiddenRows[0].HiddenAt.Add(time.Millisecond)

	// New reply arrives after the hide.
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 7002, InReplyTo: 7001, CreatedAt: replyAt},
	})

	getResp2, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp2.JSON200.HiddenThreadRootIds)
	assert.Empty(*getResp2.JSON200.HiddenThreadRootIds,
		"reply newer than hidden_at should supersede the hide")

	// Sleep long enough that the re-hide's time.Now() is strictly
	// after the reply's CreatedAt; otherwise the predicate still
	// treats the reply as superseding the (refreshed) hide.
	time.Sleep(5 * time.Millisecond)

	// Re-hide refreshes the timestamp; thread is hidden again.
	hideResp2, err := client.HTTP.HideReviewThreadWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 7001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp2.StatusCode())

	getResp3, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp3.JSON200.HiddenThreadRootIds)
	assert.ElementsMatch([]int64{7001}, *getResp3.JSON200.HiddenThreadRootIds)
}
