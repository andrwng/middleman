package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReviewThreadsMigrationApplied proves migration 000021 ran: the
// tables exist and are queryable through the read handle.
func TestReviewThreadsMigrationApplied(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var threads int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_threads`).Scan(&threads))
	require.Equal(t, 0, threads)

	var comments int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_thread_comments`).Scan(&comments))
	require.Equal(t, 0, comments)
}

// insertTestMRLocal creates a local repo + a minimal merge request to FK
// review threads onto. Mirrors the synthetic-MR field set from
// local_dispatch.go:ensureSyntheticMRForWorktree; if UpsertMergeRequest
// rejects a missing column, copy more fields from there.
func insertTestMRLocal(t *testing.T, d *DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := d.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	now := time.Now().UTC()
	mrID, err := d.UpsertMergeRequest(ctx, &MergeRequest{
		RepoID:         repoID,
		PlatformID:     1,
		Number:         1,
		Title:          "Worktree: feat",
		Author:         "local",
		State:          "open",
		HeadBranch:     "feat",
		BaseBranch:     "main",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastActivityAt: now,
	})
	require.NoError(t, err)
	return mrID
}

func TestCreateAndListReviewThreads(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := insertTestMRLocal(t, d)

	start := 10
	threads, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSHA: "abc123", Body: "first comment"},
		{Path: "b.go", Side: "RIGHT", Line: 5, StartLine: &start, CommitSHA: "abc123", Body: "ranged comment"},
	})
	require.NoError(err)
	require.Len(threads, 2)
	assert.Equal("open", threads[0].Status)
	assert.Equal("a.go", threads[0].Path)
	require.Nil(threads[0].StartLine)
	require.NotNil(threads[1].StartLine)
	assert.Equal(10, *threads[1].StartLine)

	got, err := d.GetReviewThread(ctx, threads[0].ID)
	require.NoError(err)
	assert.Equal(mrID, got.MergeRequestID)
	assert.Equal(12, got.Line)
	assert.Nil(got.HiddenAt)

	listed, err := d.ListReviewThreadsForMR(ctx, mrID)
	require.NoError(err)
	require.Len(listed, 2)
	assert.Equal("a.go", listed[0].Path)
	assert.Equal("b.go", listed[1].Path)
}

func TestReviewThreadCommentsAndState(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := insertTestMRLocal(t, d)

	threads, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "root"},
	})
	require.NoError(err)
	threadID := threads[0].ID

	// Add an agent reply.
	c, err := d.AddReviewThreadComment(ctx, threadID, "agent", "i'd refactor X", nil)
	require.NoError(err)
	assert.Equal("agent", c.Author)
	assert.Equal(threadID, c.ThreadID)

	comments, err := d.ListReviewThreadCommentsForMR(ctx, mrID)
	require.NoError(err)
	require.Len(comments, 2) // root + reply
	assert.Equal("user", comments[0].Author)
	assert.Equal("agent", comments[1].Author)

	// Per-thread comment listing returns just this thread's comments.
	threadComments, err := d.ListReviewThreadComments(ctx, threadID)
	require.NoError(err)
	require.Len(threadComments, 2)
	assert.Equal(threadID, threadComments[0].ThreadID)

	// A comment carrying a turn id round-trips the nullable turn_id.
	tid := int64(42)
	withTurn, err := d.AddReviewThreadComment(ctx, threadID, "agent", "applied in this turn", &tid)
	require.NoError(err)
	require.NotNil(withTurn.TurnID)
	assert.Equal(int64(42), *withTurn.TurnID)

	// Status transition + hide.
	require.NoError(d.SetReviewThreadStatus(ctx, threadID, "discussed"))
	require.NoError(d.HideReviewThread(ctx, threadID))
	got, err := d.GetReviewThread(ctx, threadID)
	require.NoError(err)
	assert.Equal("discussed", got.Status)
	require.NotNil(got.HiddenAt)

	require.NoError(d.UnhideReviewThread(ctx, threadID))
	got, err = d.GetReviewThread(ctx, threadID)
	require.NoError(err)
	assert.Nil(got.HiddenAt)
}

func TestDeleteReviewThreadRemovesThreadAndComments(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	mrID := insertTestMRLocal(t, d)

	created, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSHA: "abc", Body: "rename this"},
	})
	require.NoError(err)
	require.Len(created, 1)
	id := created[0].ID

	_, err = d.AddReviewThreadComment(ctx, id, "agent", "done", nil)
	require.NoError(err)

	require.NoError(d.DeleteReviewThread(ctx, id))

	_, err = d.GetReviewThread(ctx, id)
	require.ErrorIs(err, sql.ErrNoRows)
	comments, err := d.ListReviewThreadComments(ctx, id)
	require.NoError(err)
	require.Empty(comments)
}
