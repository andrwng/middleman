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
