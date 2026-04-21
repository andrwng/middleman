package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedAIQuestionTestMR(t *testing.T, d *DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := d.UpsertRepo(ctx, "github.com", "acme", "widget")
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	mrID, err := d.UpsertMergeRequest(ctx, &MergeRequest{
		RepoID:         repoID,
		PlatformID:     100,
		Number:         1,
		URL:            "https://github.com/acme/widget/pull/1",
		Title:          "pr",
		Author:         "me",
		State:          "open",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastActivityAt: now,
	})
	require.NoError(t, err)
	return mrID
}

func TestCreateAIThreadAndQuestion(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	sel := "return false"
	start := 40
	end := 42
	thread, question, err := d.CreateAIThread(ctx, NewAIThreadInput{
		MergeRequestID: mrID,
		Path:           "foo.go",
		AnchorSide:     "RIGHT",
		AnchorLine:     42,
		HunkStartLine:  &start,
		HunkEndLine:    &end,
		SelectionText:  &sel,
		CommitSHA:      "abc1234",
		Question:       "what does this do?",
	})
	require.NoError(err)
	assert.Equal("active", thread.Status)
	assert.Equal(mrID, thread.MergeRequestID)
	assert.Equal("RIGHT", thread.AnchorSide)
	require.NotNil(thread.HunkStartLine)
	assert.Equal(40, *thread.HunkStartLine)
	require.NotNil(thread.SelectionText)
	assert.Equal("return false", *thread.SelectionText)

	assert.Equal("queued", question.Status)
	assert.Equal(thread.ID, question.ThreadID)
	assert.Equal("what does this do?", question.Question)
	assert.Empty(question.Answer)
}

func TestAIQuestionLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	_, q, err := d.CreateAIThread(ctx, NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "?",
	})
	require.NoError(err)

	require.NoError(d.MarkAIQuestionRunning(ctx, q.ID, 4242))
	got, err := d.GetAIQuestion(ctx, q.ID)
	require.NoError(err)
	assert.Equal("running", got.Status)
	require.NotNil(got.PID)
	assert.Equal(4242, *got.PID)

	require.NoError(d.MarkAIQuestionDone(ctx, q.ID, "the answer", `[{"file":"x.go","line":5}]`))
	got, err = d.GetAIQuestion(ctx, q.ID)
	require.NoError(err)
	assert.Equal("done", got.Status)
	assert.Equal("the answer", got.Answer)
	assert.Equal(`[{"file":"x.go","line":5}]`, got.CitationsJSON)
	assert.Nil(got.PID)
}

func TestAIThreadSessionUpdate(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	thread, _, err := d.CreateAIThread(ctx, NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "?",
	})
	require.NoError(err)

	require.NoError(d.UpdateAIThreadSession(ctx, thread.ID, "sess-xyz", "/tmp/wt-xyz"))
	got, err := d.GetAIThread(ctx, thread.ID)
	require.NoError(err)
	require.NotNil(got.ClaudeSessionID)
	assert.Equal("sess-xyz", *got.ClaudeSessionID)
	require.NotNil(got.WorktreePath)
	assert.Equal("/tmp/wt-xyz", *got.WorktreePath)
}

func TestCancelAIQuestionKeepsHistory(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	_, q, err := d.CreateAIThread(ctx, NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "?",
	})
	require.NoError(err)

	require.NoError(d.MarkAIQuestionRunning(ctx, q.ID, 9999))
	require.NoError(d.MarkAIQuestionCancelled(ctx, q.ID))

	got, err := d.GetAIQuestion(ctx, q.ID)
	require.NoError(err)
	assert.Equal("cancelled", got.Status)
	assert.Nil(got.PID)
}

func TestDeleteAIThreadCascadesQuestions(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	thread, _, err := d.CreateAIThread(ctx, NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "first",
	})
	require.NoError(err)
	_, err = d.AddAIQuestion(ctx, thread.ID, "follow-up")
	require.NoError(err)

	require.NoError(d.DeleteAIThread(ctx, thread.ID))

	qs, err := d.ListAIQuestionsForThread(ctx, thread.ID)
	require.NoError(err)
	assert.Empty(qs)
}

func TestListAIThreadsForMR(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	for i := 0; i < 3; i++ {
		_, _, err := d.CreateAIThread(ctx, NewAIThreadInput{
			MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
			AnchorLine: i + 1, CommitSHA: "abc", Question: "q",
		})
		require.NoError(err)
	}

	threads, err := d.ListAIThreadsForMR(ctx, mrID)
	require.NoError(err)
	assert.Len(threads, 3)
}
