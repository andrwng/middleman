package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHiddenReviewThreadsUpsertAndDelete(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	// No rows initially.
	rows, err := d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	assert.Empty(rows)

	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 42, t0))

	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Len(rows, 1)
	assert.Equal(int64(42), rows[0].RootPlatformCommentID)
	assert.True(rows[0].HiddenAt.Equal(t0))

	// Re-hide overwrites hidden_at.
	t1 := t0.Add(time.Hour)
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 42, t1))
	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Len(rows, 1)
	assert.True(rows[0].HiddenAt.Equal(t1))

	// Delete is idempotent.
	require.NoError(d.DeleteHiddenReviewThread(ctx, mrID, 42))
	require.NoError(d.DeleteHiddenReviewThread(ctx, mrID, 42))
	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	assert.Empty(rows)
}

func TestHiddenReviewThreadsCascadeDelete(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	require.NoError(d.UpsertHiddenReviewThread(
		ctx, mrID, 99, time.Now().UTC().Truncate(time.Second),
	))

	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_merge_requests WHERE id = ?`, mrID,
	)
	require.NoError(err)

	rows, err := d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Empty(rows, "rows should cascade with the MR")
}

func TestActiveHiddenReviewThreadRoots(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	beforeHide := t0.Add(-time.Hour)
	afterHide := t0.Add(time.Hour)

	id100 := int64(100)
	id101 := int64(101)
	id200 := int64(200)
	id201 := int64(201)
	id300 := int64(300)
	id400 := int64(400)
	id401 := int64(401)
	id402 := int64(402)

	require.NoError(d.UpsertMREvents(ctx, []MREvent{
		{
			MergeRequestID: mrID, PlatformID: &id100, EventType: "review_comment",
			Author: "u", Body: "root A", CreatedAt: beforeHide.Add(-2 * time.Hour),
			MetadataJSON: `{"path":"a.go","line":1,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-100",
		},
		{
			MergeRequestID: mrID, PlatformID: &id101, EventType: "review_comment",
			Author: "u", Body: "reply on A (before hide)", CreatedAt: beforeHide,
			MetadataJSON: `{"path":"a.go","line":1,"side":"RIGHT","in_reply_to":100}`,
			DedupeKey:    "review-comment-101",
		},
		{
			MergeRequestID: mrID, PlatformID: &id200, EventType: "review_comment",
			Author: "u", Body: "root B", CreatedAt: beforeHide.Add(-2 * time.Hour),
			MetadataJSON: `{"path":"b.go","line":2,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-200",
		},
		{
			MergeRequestID: mrID, PlatformID: &id201, EventType: "review_comment",
			Author: "u", Body: "reply on B (after hide)", CreatedAt: afterHide,
			MetadataJSON: `{"path":"b.go","line":2,"side":"RIGHT","in_reply_to":200}`,
			DedupeKey:    "review-comment-201",
		},
		{
			MergeRequestID: mrID, PlatformID: &id300, EventType: "review_comment",
			Author: "u", Body: "lone root C", CreatedAt: beforeHide.Add(-3 * time.Hour),
			MetadataJSON: `{"path":"c.go","line":3,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-300",
		},
		{
			MergeRequestID: mrID, PlatformID: &id400, EventType: "review_comment",
			Author: "u", Body: "root A (chain)", CreatedAt: beforeHide.Add(-3 * time.Hour),
			MetadataJSON: `{"path":"d.go","line":4,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-400",
		},
		{
			MergeRequestID: mrID, PlatformID: &id401, EventType: "review_comment",
			Author: "u", Body: "B reply on A (before hide)", CreatedAt: beforeHide,
			MetadataJSON: `{"path":"d.go","line":4,"side":"RIGHT","in_reply_to":400}`,
			DedupeKey:    "review-comment-401",
		},
		{
			MergeRequestID: mrID, PlatformID: &id402, EventType: "review_comment",
			Author: "u", Body: "C reply on B (after hide)", CreatedAt: afterHide,
			MetadataJSON: `{"path":"d.go","line":4,"side":"RIGHT","in_reply_to":401}`,
			DedupeKey:    "review-comment-402",
		},
	}))

	// Hide all four roots at t0.
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 100, t0))
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 200, t0))
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 300, t0))
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 400, t0))

	events, err := d.ListMREvents(ctx, mrID)
	require.NoError(err)

	active, err := d.ActiveHiddenReviewThreadRoots(ctx, mrID, events)
	require.NoError(err)
	assert.ElementsMatch([]int64{100, 300}, active,
		"threads 200 and 400 each have a reply newer than hidden_at — should not be active")
}

func TestHasReviewCommentOnMR(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	id := int64(777)
	require.NoError(d.UpsertMREvents(ctx, []MREvent{{
		MergeRequestID: mrID,
		PlatformID:     &id,
		EventType:      "review_comment",
		Author:         "u",
		Body:           "hi",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		MetadataJSON:   `{"path":"f.go","line":1,"side":"RIGHT"}`,
		DedupeKey:      "review-comment-777",
	}}))

	got, err := d.HasReviewCommentOnMR(ctx, mrID, 777)
	require.NoError(err)
	assert.True(got)

	got, err = d.HasReviewCommentOnMR(ctx, mrID, 12345)
	require.NoError(err)
	assert.False(got, "unknown platform id")

	// Different MR: same platform id should not match.
	otherID, err := d.UpsertRepo(ctx, "github.com", "other", "repo")
	require.NoError(err)
	otherMR, err := d.UpsertMergeRequest(ctx, &MergeRequest{
		RepoID:         otherID,
		PlatformID:     888,
		Number:         2,
		URL:            "https://github.com/other/repo/pull/2",
		Title:          "other",
		Author:         "u",
		State:          "open",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		UpdatedAt:      time.Now().UTC().Truncate(time.Second),
		LastActivityAt: time.Now().UTC().Truncate(time.Second),
	})
	require.NoError(err)
	got, err = d.HasReviewCommentOnMR(ctx, otherMR, 777)
	require.NoError(err)
	assert.False(got, "platform id matches a row on a different MR")
}
