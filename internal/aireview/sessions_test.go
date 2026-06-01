package aireview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func setupSessionTest(t *testing.T) (*db.DB, *SessionRunner, string, int64) {
	t.Helper()
	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	writeFakeClaude(t, fakeClaude,
		`{"type":"result","subtype":"success","is_error":false,"result":"made the changes","session_id":"sess-abc"}`)

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)

	// Seed worktree.
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)

	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(t, err)

	runner := NewSessionRunner(database)
	return database, runner, tmp, sess.ID
}

func TestSessionRunnerFirstTurn(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	database, runner, worktreePath, sessionID := setupSessionTest(t)
	ctx := context.Background()

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sessionID,
		WorktreePath:    worktreePath,
		Branch:          "feat/x",
		BaseRef:         "origin/main",
		BaseSHA:         "aaaa1111",
		HeadSHA:         "bbbb2222",
		UserTurnType:    "review_feedback",
		UserTurnContent: "please add tests for foo()",
		IsFirstTurn:     true,
	})
	require.NoError(err)
	assert.Equal("review_feedback", res.UserTurn.TurnType)
	assert.Equal("done", res.UserTurn.Status)
	assert.Equal("claude_response", res.ResponseTurn.TurnType)
	// Response turn starts queued; the goroutine flips it to running
	// then done. Poll briefly for the terminal state.
	turnID := res.ResponseTurn.ID
	deadline := time.Now().Add(3 * time.Second)
	var finalTurn db.WorktreeSessionTurn
	for time.Now().Before(deadline) {
		turn, err := database.GetWorktreeSessionTurn(ctx, turnID)
		require.NoError(err)
		if turn.Status == "done" || turn.Status == "failed" {
			finalTurn = turn
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal("done", finalTurn.Status, "turn never moved to done; raw=%s err=%s", finalTurn.RawJSON, finalTurn.Error)
	assert.Equal("made the changes", finalTurn.Content)

	// Session row stores the claude_session_id after first turn.
	sess, err := database.GetWorktreeSession(ctx, sessionID)
	require.NoError(err)
	assert.Equal("sess-abc", sess.ClaudeSessionID)
}

func TestSessionRunnerSubprocessFails(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	script := "#!/bin/sh\necho 'nope' >&2\nexit 2\n"
	require.NoError(os.WriteFile(fakeClaude, []byte(script), 0o755))
	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "feat/x",
	})
	require.NoError(err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(err)

	runner := NewSessionRunner(database)
	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		UserTurnType:    "user_message",
		UserTurnContent: "ping",
	})
	require.NoError(err)

	deadline := time.Now().Add(3 * time.Second)
	var finalTurn db.WorktreeSessionTurn
	for time.Now().Before(deadline) {
		t2, err := database.GetWorktreeSessionTurn(ctx, res.ResponseTurn.ID)
		require.NoError(err)
		if t2.Status == "failed" || t2.Status == "done" {
			finalTurn = t2
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal("failed", finalTurn.Status)
	assert.Contains(finalTurn.Error, "nope")
}

func TestBuildSessionPromptFirstTurnIncludesContext(t *testing.T) {
	assert := assert.New(t)
	prompt := buildSessionPrompt(SubmitTurnInput{
		WorktreePath:    "/code/foo",
		Branch:          "feat/x",
		BaseRef:         "origin/main",
		BaseSHA:         "aaaaaaa1234",
		HeadSHA:         "bbbbbbb5678",
		UserTurnType:    "review_feedback",
		UserTurnContent: "fix the thing",
		IsFirstTurn:     true,
	})
	assert.Contains(prompt, "/code/foo")
	assert.Contains(prompt, "feat/x")
	assert.Contains(prompt, "origin/main")
	assert.Contains(prompt, "aaaaaaa") // shortSHA(BaseSHA)
	assert.Contains(prompt, "fix the thing")
	assert.Contains(prompt, "reviewer")
}

func TestBuildSessionPromptFollowUpIsBare(t *testing.T) {
	assert := assert.New(t)
	prompt := buildSessionPrompt(SubmitTurnInput{
		WorktreePath:    "/code/foo",
		Branch:          "feat/x",
		UserTurnType:    "user_message",
		UserTurnContent: "also rename Y to Z",
		IsFirstTurn:     false,
	})
	// Follow-up turns rely on --resume for context; the prompt is
	// just the user's message verbatim.
	assert.Equal("also rename Y to Z", prompt)
}

func TestSessionRunnerCancelTurn(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	// A subprocess that sleeps so cancellation has time to fire.
	script := "#!/bin/sh\nsleep 60\n"
	require.NoError(os.WriteFile(fakeClaude, []byte(script), 0o755))
	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "feat",
	})
	require.NoError(err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(err)
	runner := NewSessionRunner(database)

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		UserTurnType:    "user_message",
		UserTurnContent: "hello",
	})
	require.NoError(err)

	// Wait until the subprocess marks itself running, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.GetWorktreeSessionTurn(ctx, res.ResponseTurn.ID)
		require.NoError(err)
		if turn.Status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(runner.CancelTurn(ctx, res.ResponseTurn.ID))

	turn, err := database.GetWorktreeSessionTurn(ctx, res.ResponseTurn.ID)
	require.NoError(err)
	assert.Equal("cancelled", turn.Status)
	assert.Nil(turn.PID)
}

func TestSessionRunnerReconcileOnStartup(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	database := openTestDB(t)
	ctx := context.Background()

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: "/code/demo", Branch: "feat/x",
	})
	require.NoError(err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(err)

	// Seed one queued + one running + one done. Reconciler should
	// fail the first two and leave the third alone.
	queued, err := database.AddWorktreeSessionTurn(ctx, db.NewWorktreeSessionTurn{
		SessionID: sess.ID, TurnType: "claude_response", Status: "queued",
	})
	require.NoError(err)

	running, err := database.AddWorktreeSessionTurn(ctx, db.NewWorktreeSessionTurn{
		SessionID: sess.ID, TurnType: "claude_response", Status: "running",
	})
	require.NoError(err)

	done, err := database.AddWorktreeSessionTurn(ctx, db.NewWorktreeSessionTurn{
		SessionID: sess.ID, TurnType: "claude_response", Status: "done",
		Content: "all good",
	})
	require.NoError(err)

	runner := NewSessionRunner(database)
	require.NoError(runner.ReconcileOnStartup(ctx))

	q, err := database.GetWorktreeSessionTurn(ctx, queued.ID)
	require.NoError(err)
	assert.Equal("failed", q.Status)
	assert.Contains(q.Error, "interrupted")

	r, err := database.GetWorktreeSessionTurn(ctx, running.ID)
	require.NoError(err)
	assert.Equal("failed", r.Status)

	d, err := database.GetWorktreeSessionTurn(ctx, done.ID)
	require.NoError(err)
	assert.Equal("done", d.Status)
	assert.Equal("all good", d.Content)
}

// Suppress unused import vet failures when the test file is the only
// consumer of the symbol below.
var _ = fmt.Sprintf
