package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedSuggestion(t *testing.T, d *DB, id string) int64 {
	t.Helper()
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(t, err)
	_, err = d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: id, Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "original"})
	require.NoError(t, err)
	return mrID
}

func TestDraftMessageLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedSuggestion(t, d, "s1")

	user, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "user", Kind: "message", Body: "why?", Status: "done"})
	require.NoError(err)
	reply, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "claude", Kind: "message", Status: "queued"})
	require.NoError(err)

	active, err := d.ListActiveDraftMessages(ctx)
	require.NoError(err)
	require.Len(active, 1)

	require.NoError(d.MarkDraftMessageRunning(ctx, reply.ID, 99))
	running, err := d.RunningDraftMessage(ctx, "s1")
	require.NoError(err)
	require.NotNil(running)
	require.NotNil(running.StartedAt)
	require.NoError(d.UpdateDraftMessageStream(ctx, reply.ID, `{"events":[]}`))
	require.NoError(d.MarkDraftMessageDone(ctx, reply.ID, "because", `{"events":[1]}`))
	require.NoError(d.MarkDraftMessageFailed(ctx, reply.ID, "late", "")) // terminal: ignored

	msgs, err := d.ListReviewDraftMessages(ctx, "s1")
	require.NoError(err)
	require.Len(msgs, 2)
	assert.Equal(user.ID, msgs[0].ID)
	assert.Equal("done", msgs[1].Status)
	assert.Equal("because", msgs[1].Body)
	at, err := d.LastCompletedClaudeMessageAt(ctx, "s1")
	require.NoError(err)
	assert.NotNil(at)

	require.NoError(d.SetReviewDraftChatSession(ctx, "s1", "sess-fork", "/tmp/run-1"))
	got, err := d.GetReviewDraft(ctx, mrID, "", "s1")
	require.NoError(err)
	assert.Equal("sess-fork", got.ClaudeSessionID)
	assert.Equal("/tmp/run-1", got.ChatWorktreePath)
}

func TestApplyClaudeRevisionRules(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		accept      bool // move to draft first
		editAfter   bool // reviewer edits after the turn started
		discard     bool
		wantApplied bool
		wantErr     error
		wantBody    string
	}{
		{name: "suggestion, no edit -> applied", wantApplied: true, wantBody: "revised"},
		{name: "suggestion edited during turn -> pending", editAfter: true, wantApplied: false, wantBody: "reviewer text"},
		{name: "draft -> pending", accept: true, wantApplied: false, wantBody: "original"},
		{name: "discarded -> rejected", discard: true, wantErr: ErrInvalidDraftTransition, wantBody: "original"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := require.New(t)
			d := openTestDB(t)
			mrID := seedSuggestion(t, d, "s1")
			turnStarted := time.Now().UTC().Add(-time.Second)
			if tc.accept {
				_, err := d.AcceptReviewDraft(ctx, mrID, "", "s1")
				must.NoError(err)
			}
			if tc.editAfter {
				_, err := d.UpdateReviewDraftBody(ctx, mrID, "", "s1", "reviewer text")
				must.NoError(err)
			}
			if tc.discard {
				_, err := d.DiscardReviewDraft(ctx, mrID, "", "s1")
				must.NoError(err)
			}
			msg, err := d.ApplyClaudeRevision(ctx, mrID, "", "s1", "revised", turnStarted)
			if tc.wantErr != nil {
				must.ErrorIs(err, tc.wantErr)
			} else {
				must.NoError(err)
				must.Equal("revision", msg.Kind)
				must.Equal(tc.wantApplied, msg.Applied)
				must.Equal("revised", msg.Body)
			}
			got, err := d.GetReviewDraft(ctx, mrID, "", "s1")
			must.NoError(err)
			must.Equal(tc.wantBody, got.Body)
		})
	}
}

func TestApplyAndRevertRevisionMessage(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedSuggestion(t, d, "s1")
	_, err := d.AcceptReviewDraft(ctx, mrID, "", "s1")
	require.NoError(err)

	pending, err := d.ApplyClaudeRevision(ctx, mrID, "", "s1", "revised", time.Now().UTC())
	require.NoError(err)
	require.False(pending.Applied)

	got, err := d.ApplyRevisionMessage(ctx, mrID, "", pending.ID)
	require.NoError(err)
	assert.Equal("revised", got.Body)
	assert.Equal("draft", got.State) // applying never changes state
	_, err = d.ApplyRevisionMessage(ctx, mrID, "", pending.ID)
	require.ErrorIs(err, ErrInvalidDraftTransition)

	got, err = d.RevertRevisionMessage(ctx, mrID, "", pending.ID)
	require.NoError(err)
	assert.Equal("original", got.Body)

	user, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "user", Kind: "message", Body: "x", Status: "done"})
	require.NoError(err)
	_, err = d.ApplyRevisionMessage(ctx, mrID, "", user.ID)
	require.ErrorIs(err, ErrInvalidDraftTransition)
}
