package db

import (
	"context"
	"fmt"
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

func TestReviewDraftTransitions(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(t, err)

	type step struct {
		op      string // accept|discard|restore|edit
		want    string // resulting state, "" when the op must fail
		wantErr error
	}
	cases := []struct {
		name  string
		start string // suggestion|draft
		steps []step
	}{
		{"accept suggestion", "suggestion", []step{{op: "accept", want: "draft"}}},
		{"accept twice fails", "suggestion", []step{{op: "accept", want: "draft"}, {op: "accept", wantErr: ErrInvalidDraftTransition}}},
		{"discard and restore suggestion", "suggestion", []step{{op: "discard", want: "discarded"}, {op: "restore", want: "suggestion"}}},
		{"discard and restore draft", "draft", []step{{op: "discard", want: "discarded"}, {op: "restore", want: "draft"}}},
		{"restore live draft fails", "draft", []step{{op: "restore", wantErr: ErrInvalidDraftTransition}}},
		{"accept discarded fails", "suggestion", []step{{op: "discard", want: "discarded"}, {op: "accept", wantErr: ErrInvalidDraftTransition}}},
		{"edit keeps suggestion state", "suggestion", []step{{op: "edit", want: "suggestion"}}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := require.New(t)
			id := fmt.Sprintf("t-%d", i)
			nd := NewReviewDraft{ID: id, Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "b"}
			if tc.start == "suggestion" {
				_, err := d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, nd)
				must.NoError(err)
			} else {
				_, err := d.InsertUserReviewDrafts(ctx, mrID, "", []NewReviewDraft{nd})
				must.NoError(err)
			}
			for _, s := range tc.steps {
				var got *ReviewDraft
				var err error
				switch s.op {
				case "accept":
					got, err = d.AcceptReviewDraft(ctx, mrID, "", id)
				case "discard":
					got, err = d.DiscardReviewDraft(ctx, mrID, "", id)
				case "restore":
					got, err = d.RestoreReviewDraft(ctx, mrID, "", id)
				case "edit":
					got, err = d.UpdateReviewDraftBody(ctx, mrID, "", id, "edited")
				}
				if s.wantErr != nil {
					must.ErrorIs(err, s.wantErr)
					continue
				}
				must.NoError(err)
				must.NotNil(got)
				must.Equal(s.want, got.State)
			}
		})
	}

	missing, err := d.AcceptReviewDraft(ctx, mrID, "", "nope")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestDraftRationales(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)
	_, err = d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: "r-1", Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "b"})
	require.NoError(err)
	_, err = d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "r-1", Author: "claude", Kind: "message", Body: "because X", Status: "done"})
	require.NoError(err)
	_, err = d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "r-1", Author: "claude", Kind: "message", Body: "later reply", Status: "done"})
	require.NoError(err)

	got, err := d.ListDraftRationales(ctx, mrID, "")
	require.NoError(err)
	require.Equal(map[string]string{"r-1": "because X"}, got)
}
