package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRun(t *testing.T, d *DB, mrID int64) AIReviewRun {
	t.Helper()
	run, err := d.CreateAIReviewRun(context.Background(), NewAIReviewRun{
		MergeRequestID: mrID, HeadSHA: "head1", ReadSHA: "head1", Instructions: "focus on errors",
	})
	require.NoError(t, err)
	return run
}

func TestAIReviewRunLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	run := newRun(t, d, mrID)
	assert.Equal("queued", run.Status)
	assert.Equal("focus on errors", run.Instructions)

	require.NoError(d.MarkAIReviewRunRunning(ctx, run.ID, 4242, "/tmp/run-1"))
	require.NoError(d.UpdateAIReviewRunStream(ctx, run.ID, `{"events":[]}`))
	require.NoError(d.MarkAIReviewRunDone(ctx, run.ID, "sess-1", "two findings", `{"events":[1]}`))

	got, err := d.GetLatestAIReviewRun(ctx, mrID, "")
	require.NoError(err)
	require.NotNil(got)
	assert.Equal("done", got.Status)
	assert.Equal("sess-1", got.ClaudeSessionID)
	assert.Equal("two findings", got.Summary)
	assert.Equal("/tmp/run-1", got.WorktreePath)
	require.NotNil(got.CompletedAt)

	// Terminal runs cannot be failed or cancelled afterwards.
	require.NoError(d.MarkAIReviewRunFailed(ctx, run.ID, "late", ""))
	require.NoError(d.MarkAIReviewRunCancelled(ctx, run.ID))
	got, err = d.GetAIReviewRun(ctx, run.ID)
	require.NoError(err)
	assert.Equal("done", got.Status)
}

func TestCreateAIReviewRunRejectsSecondActive(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	first := newRun(t, d, mrID)
	_, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.ErrorIs(err, ErrReviewRunActive)

	// A different branch of the same MR (local) is independent.
	_, err = d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, Branch: "feat/b", HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)

	require.NoError(d.MarkAIReviewRunCancelled(ctx, first.ID))
	_, err = d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(err)
}

func TestReviewRunFailureKeepsSuggestions(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run := newRun(t, d, mrID)

	_, err := d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: "s-1", Path: "a.go", Side: "RIGHT", Line: 3, CommitSHA: "head1", Body: "nil deref"})
	require.NoError(err)
	require.NoError(d.MarkAIReviewRunFailed(ctx, run.ID, "claude exited: 1", ""))

	n, err := d.CountRunSuggestions(ctx, run.ID)
	require.NoError(err)
	assert.Equal(1, n)
	list, err := d.ListReviewDrafts(ctx, mrID, "")
	require.NoError(err)
	require.Len(list, 1)
	assert.Equal("suggestion", list[0].State)
	assert.Equal("claude", list[0].Origin)
	require.NotNil(list[0].RunID)
	assert.Equal(run.ID, *list[0].RunID)

	active, err := d.ListActiveAIReviewRuns(ctx)
	require.NoError(err)
	assert.Empty(active)
}

func TestAIReviewRunLocalFieldsAndCleanupQueries(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	local, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, Branch: "feat/a", HeadSHA: "head", ReadSHA: "head", BaseSHA: "base", LocalWorktreePath: "/w"})
	require.NoError(err)
	assert.Equal("base", local.BaseSHA)
	assert.Equal("/w", local.LocalWorktreePath)
	require.NoError(d.SetAIReviewRunReadSHA(ctx, local.ID, "snap"))
	got, err := d.GetAIReviewRun(ctx, local.ID)
	require.NoError(err)
	assert.Equal("snap", got.ReadSHA)

	// Active local run: not prunable yet.
	prunable, err := d.ListLocalRunsWithoutLiveDrafts(ctx)
	require.NoError(err)
	assert.Empty(prunable)

	_, err = d.InsertClaudeSuggestion(ctx, mrID, "feat/a", local.ID, NewReviewDraft{ID: "l1", Path: "a", Side: "RIGHT", Line: 1, CommitSHA: "head", Body: "b"})
	require.NoError(err)
	require.NoError(d.MarkAIReviewRunDone(ctx, local.ID, "s", "", ""))
	prunable, err = d.ListLocalRunsWithoutLiveDrafts(ctx)
	require.NoError(err)
	assert.Empty(prunable) // a live suggestion still points at it

	_, err = d.DiscardReviewDraft(ctx, mrID, "feat/a", "l1")
	require.NoError(err)
	prunable, err = d.ListLocalRunsWithoutLiveDrafts(ctx)
	require.NoError(err)
	require.Len(prunable, 1)
	assert.Equal(local.ID, prunable[0].ID)

	// GitHub runs are never in the prune list; active runs on closed PRs are listed.
	gh, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h", BaseSHA: "b"})
	require.NoError(err)
	closed, err := d.ListActiveAIReviewRunsOnClosedPRs(ctx)
	require.NoError(err)
	assert.Empty(closed)
	_, err = d.rw.ExecContext(ctx, `UPDATE middleman_merge_requests SET state = 'merged' WHERE id = ?`, mrID)
	require.NoError(err)
	closed, err = d.ListActiveAIReviewRunsOnClosedPRs(ctx)
	require.NoError(err)
	require.Len(closed, 1)
	assert.Equal(gh.ID, closed[0].ID)
	prunable, err = d.ListLocalRunsWithoutLiveDrafts(ctx)
	require.NoError(err)
	require.Len(prunable, 1)
}
