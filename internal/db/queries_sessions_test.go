package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorktreeSessionLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r", Branch: "feat", HeadSHA: "aaaa",
	})
	require.NoError(err)

	// No active session yet.
	_, err = d.GetActiveWorktreeSession(ctx, w.ID, "feat")
	require.ErrorIs(err, sql.ErrNoRows)

	sess, err := d.CreateWorktreeSession(ctx, w.ID, "feat")
	require.NoError(err)
	assert.Equal("active", sess.Status)
	assert.NotZero(sess.ID)

	live, err := d.GetActiveWorktreeSession(ctx, w.ID, "feat")
	require.NoError(err)
	assert.Equal(sess.ID, live.ID)

	require.NoError(d.SetWorktreeSessionClaudeID(ctx, sess.ID, "sess-abc123"))
	live, err = d.GetActiveWorktreeSession(ctx, w.ID, "feat")
	require.NoError(err)
	assert.Equal("sess-abc123", live.ClaudeSessionID)

	// Killing the session removes it from the active query.
	require.NoError(d.MarkWorktreesSessionStatus(ctx, sess.ID, "killed"))
	_, err = d.GetActiveWorktreeSession(ctx, w.ID, "feat")
	assert.ErrorIs(err, sql.ErrNoRows)
}

func TestActiveWorktreeSessionIsBranchScoped(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r", Branch: "a", HeadSHA: "aaaa",
	})
	require.NoError(err)

	// No active session on either branch yet.
	_, err = d.GetActiveWorktreeSession(ctx, w.ID, "a")
	require.ErrorIs(err, sql.ErrNoRows)

	sessA, err := d.CreateWorktreeSession(ctx, w.ID, "a")
	require.NoError(err)
	sessB, err := d.CreateWorktreeSession(ctx, w.ID, "b")
	require.NoError(err)
	assert.NotEqual(sessA.ID, sessB.ID)
	assert.Equal("a", sessA.Branch)
	assert.Equal("b", sessB.Branch)

	gotA, err := d.GetActiveWorktreeSession(ctx, w.ID, "a")
	require.NoError(err)
	assert.Equal(sessA.ID, gotA.ID)

	gotB, err := d.GetActiveWorktreeSession(ctx, w.ID, "b")
	require.NoError(err)
	assert.Equal(sessB.ID, gotB.ID)
}

func TestWorktreeSessionTurns(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r", Branch: "feat",
	})
	require.NoError(err)
	sess, err := d.CreateWorktreeSession(ctx, w.ID, "feat")
	require.NoError(err)

	// User feedback turn (done immediately).
	feedback, err := d.AddWorktreeSessionTurn(ctx, NewWorktreeSessionTurn{
		SessionID: sess.ID,
		TurnType:  "review_feedback",
		Content:   "please add tests for foo()",
		Status:    "done",
	})
	require.NoError(err)
	assert.NotZero(feedback.ID)

	// Claude response turn (queued first, then updated as it runs).
	pid := 12345
	resp, err := d.AddWorktreeSessionTurn(ctx, NewWorktreeSessionTurn{
		SessionID: sess.ID,
		TurnType:  "claude_response",
		Status:    "queued",
		PID:       &pid,
	})
	require.NoError(err)
	require.NotNil(resp.PID)
	assert.Equal(12345, *resp.PID)

	running := "running"
	require.NoError(d.UpdateWorktreeSessionTurnFields(ctx, resp.ID, UpdateWorktreeSessionTurn{
		Status: &running,
	}))

	done := "done"
	content := "I'll add tests for foo()."
	rawJSON := `{"result":"I'll add tests..."}`
	require.NoError(d.UpdateWorktreeSessionTurnFields(ctx, resp.ID, UpdateWorktreeSessionTurn{
		Status:   &done,
		Content:  &content,
		RawJSON:  &rawJSON,
		ClearPID: true,
	}))

	turns, err := d.ListWorktreeSessionTurns(ctx, sess.ID)
	require.NoError(err)
	require.Len(turns, 2)
	assert.Equal("review_feedback", turns[0].TurnType)
	assert.Equal("claude_response", turns[1].TurnType)
	assert.Equal("done", turns[1].Status)
	assert.Equal(content, turns[1].Content)
	assert.Nil(turns[1].PID)
}
