package aireview

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func TestHungTurnTimesOutAndFreesSession(t *testing.T) {
	require := require.New(t)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{Path: tmp, Branch: "f", HeadSHA: "h"})
	require.NoError(err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(err)

	runner := NewSessionRunner(database)
	runner.turnTimeout = 150 * time.Millisecond // tiny, just for the test

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, IsFirstTurn: true,
		UserTurnType: "user_message", UserTurnContent: "hang please",
	})
	require.NoError(err)

	// The timeout should fire, kill claude, and mark the turn failed,
	// which frees the session (no queued/running claude_response left).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.GetWorktreeSessionTurn(ctx, res.ResponseTurn.ID)
		require.NoError(err)
		if turn.Status == "failed" {
			require.Contains(turn.Error, "timed out")
			turns, err := database.ListWorktreeSessionTurns(ctx, sess.ID)
			require.NoError(err)
			for _, tt := range turns {
				if tt.TurnType == "claude_response" {
					require.NotEqual("running", tt.Status)
					require.NotEqual("queued", tt.Status)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow("turn never timed out / failed — session still wedged")
}
