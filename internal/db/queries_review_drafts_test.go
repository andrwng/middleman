package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewDraftsInsertListUpdateDelete(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	start := 3
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{
		{ID: "d-1", Path: "a.go", Side: "RIGHT", Line: 5, StartLine: &start, CommitSHA: "abc", Body: "one", CreatedAt: created},
		{ID: "d-2", Path: "b.go", Side: "LEFT", Line: 1, CommitSHA: "abc", Body: "two"},
	})
	require.NoError(err)
	require.Len(got, 2)
	assert.Equal("user", got[0].Origin)
	assert.Equal("draft", got[0].State)
	assert.Equal(created, got[0].CreatedAt)
	require.NotNil(got[0].StartLine)
	assert.Equal(3, *got[0].StartLine)
	assert.Nil(got[1].StartLine)

	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 2)
	assert.Equal("d-1", list[0].ID) // ordered by created_at, then id

	upd, err := d.UpdateReviewDraftBody(ctx, mrID, "", "d-2", "two edited")
	require.NoError(err)
	require.NotNil(upd)
	assert.Equal("two edited", upd.Body)
	require.NotNil(upd.BodyEditedAt)

	missing, err := d.UpdateReviewDraftBody(ctx, mrID, "", "nope", "x")
	require.NoError(err)
	assert.Nil(missing)

	n, err := d.DeleteReviewDrafts(ctx, mrID, "", []string{"d-1", "nope"})
	require.NoError(err)
	assert.Equal(int64(1), n)
	list, err = d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
	assert.Equal("d-2", list[0].ID)
}

func TestReviewDraftsInsertIsIdempotentOnID(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	in := []NewReviewDraft{{ID: "dup", Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "abc", Body: "first"}}

	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", in)
	require.NoError(err)
	in[0].Body = "second"
	got, err := d.InsertUserReviewDrafts(ctx, mrID, "", in)
	require.NoError(err)
	require.Len(got, 1)
	require.Equal("first", got[0].Body) // existing row wins

	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
}

func TestReviewDraftsScopedByMRAndBranch(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	_, err := d.InsertUserReviewDrafts(ctx, mrID, "feat/a", []NewReviewDraft{{ID: "a", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "on a"}})
	require.NoError(err)
	_, err = d.InsertUserReviewDrafts(ctx, mrID, "feat/b", []NewReviewDraft{{ID: "b", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "on b"}})
	require.NoError(err)

	onA, err := d.ListReviewDrafts(ctx, mrID, "feat/a")
	require.NoError(err)
	require.Len(onA, 1)
	assert.Equal("a", onA[0].ID)

	// Cross-branch get/update/delete are no-ops.
	g, err := d.GetReviewDraft(ctx, mrID, "feat/a", "b")
	require.NoError(err)
	assert.Nil(g)
	n, err := d.DeleteReviewDrafts(ctx, mrID, "feat/a", []string{"b"})
	require.NoError(err)
	assert.Equal(int64(0), n)
	// Wrong MR is a no-op too.
	n, err = d.DeleteReviewDrafts(ctx, mrID+999, "feat/b", []string{"b"})
	require.NoError(err)
	assert.Equal(int64(0), n)
}

func TestReviewDraftMetaUpsert(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	m, err := d.GetReviewDraftMeta(ctx, mrID, "")
	require.NoError(err)
	assert.Empty(m.Body)
	assert.True(m.UpdatedAt.IsZero())

	_, err = d.UpsertReviewDraftMeta(ctx, mrID, "", "summary", "APPROVE")
	require.NoError(err)
	m, err = d.UpsertReviewDraftMeta(ctx, mrID, "", "summary 2", "COMMENT")
	require.NoError(err)
	assert.Equal("summary 2", m.Body)
	assert.Equal("COMMENT", m.Event)

	require.NoError(d.DeleteReviewDraftMeta(ctx, mrID, ""))
	m, err = d.GetReviewDraftMeta(ctx, mrID, "")
	require.NoError(err)
	assert.Empty(m.Event)
}

func TestReviewDraftsSubmitErrorAndCascade(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{{ID: "e", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "b"}})
	require.NoError(err)

	require.NoError(d.SetReviewDraftSubmitError(ctx, "e", "could not be resolved"))
	g, err := d.GetReviewDraft(ctx, mrID, "", "e")
	require.NoError(err)
	require.NotNil(g)
	require.Equal("could not be resolved", g.SubmitError)

	_, err = d.rw.ExecContext(ctx, `DELETE FROM middleman_merge_requests WHERE id = ?`, mrID)
	require.NoError(err)
	var n int
	require.NoError(d.ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM middleman_review_drafts`).Scan(&n))
	require.Equal(0, n)
}

func TestListReviewDraftCounts(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{
		{ID: "c1", Path: "x", Side: "RIGHT", Line: 1, CommitSHA: "s", Body: "b"},
		{ID: "c2", Path: "x", Side: "RIGHT", Line: 2, CommitSHA: "s", Body: "b"},
	})
	require.NoError(err)

	counts, err := d.ListReviewDraftCounts(ctx)
	require.NoError(err)
	require.Len(counts, 1)
	assert.Equal(2, counts[0].Count)
	assert.Equal("acme", counts[0].Owner)
	assert.NotZero(counts[0].Number)
}
