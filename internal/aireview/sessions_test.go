package aireview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestBuildSessionPromptSteerWithAllowWritesSwapsReadOnlySentence(t *testing.T) {
	in := SubmitTurnInput{
		WorktreePath: "/tmp/wt", Branch: "main",
		Action: "steer", AllowWrites: true,
		UserTurnContent: "go ahead",
		Threads: []ThreadContext{{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "fix", WritesAllowed: true}},
	}
	prompt := buildSessionPrompt(in)
	assert := assert.New(t)
	assert.NotContains(prompt, "Do not change any files")
	assert.Contains(prompt, "You may edit files in the worktree")
}

func TestBuildSessionPromptSteerWithoutAllowWritesStaysReadOnly(t *testing.T) {
	in := SubmitTurnInput{
		WorktreePath: "/tmp/wt", Branch: "main",
		Action: "steer", AllowWrites: false,
		UserTurnContent: "what about",
		Threads: []ThreadContext{{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "fix"}},
	}
	prompt := buildSessionPrompt(in)
	assert := assert.New(t)
	assert.Contains(prompt, "Do not change any files")
	assert.NotContains(prompt, "You may edit files")
}

func TestBuildSessionPromptIncludesStackedComments(t *testing.T) {
	in := SubmitTurnInput{
		WorktreePath: "/tmp/wt", Branch: "main",
		Action: "apply",
		Threads: []ThreadContext{{
			ID: 1, Path: "a.go", Line: 12, Side: "RIGHT",
			RootComment:     "consider extracting",
			StackedComments: []string{"sounds good", "but also rename Foo"},
		}},
	}
	prompt := buildSessionPrompt(in)
	assert := assert.New(t)
	assert.Contains(prompt, "consider extracting")
	assert.Contains(prompt, "Reviewer's notes since the last engage")
	assert.Contains(prompt, "sounds good")
	assert.Contains(prompt, "but also rename Foo")
}

func TestBuildSessionPromptSkipsStackedBlockWhenEmpty(t *testing.T) {
	in := SubmitTurnInput{
		WorktreePath: "/tmp/wt", Branch: "main",
		Action: "discuss",
		Threads: []ThreadContext{{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "look at this"}},
	}
	prompt := buildSessionPrompt(in)
	assert.NotContains(t, prompt, "Reviewer's notes since the last engage")
}

// Suppress unused import vet failures when the test file is the only
// consumer of the symbol below.
var _ = fmt.Sprintf
var _ = strings.TrimSpace

// writeGateClaude installs a fake claude that:
//   - identifies itself as turn "A" or "B" by grepping for those tokens in
//     its args (the test sets UserTurnContent to "TURN-A"/"TURN-B")
//   - records start in $GATE_DIR/<name>-start
//   - polls for $GATE_DIR/<name>-release (created by the test to unblock)
//   - emits a valid result JSON, then records end in $GATE_DIR/<name>-end
//
// Returns the gate directory.
func writeGateClaude(t *testing.T, path string) string {
	t.Helper()
	gate := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
NAME="X"
case "$*" in
  *TURN-A*) NAME="A" ;;
  *TURN-B*) NAME="B" ;;
esac
date +%%s.%%N > "%s/$NAME-start"
while [ ! -f "%s/$NAME-release" ]; do sleep 0.02; done
cat <<EOF
{"type":"result","subtype":"success","is_error":false,"result":"ok-$NAME","session_id":"sess-$NAME"}
EOF
date +%%s.%%N > "%s/$NAME-end"
`, gate, gate, gate)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return gate
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, "timed out waiting for "+path)
}

func readUnixNano(t *testing.T, path string) float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var f float64
	_, err = fmt.Sscanf(strings.TrimSpace(string(b)), "%f", &f)
	require.NoError(t, err)
	return f
}

func TestSessionRunnerSerializesConcurrentSubmits(t *testing.T) {
	// Bypass setupSessionTest because we need the gate-aware claude.
	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	gate := writeGateClaude(t, fakeClaude)

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "main", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(t, err)
	runner := NewSessionRunner(database)

	_, err = runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, Branch: "main",
		UserTurnType: "user_message", UserTurnContent: "TURN-A",
	})
	require.NoError(t, err)
	_, err = runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, Branch: "main",
		UserTurnType: "user_message", UserTurnContent: "TURN-B",
	})
	require.NoError(t, err)

	// A starts; B must NOT have started while A is gated.
	waitForFile(t, filepath.Join(gate, "A-start"), 2*time.Second)
	// Give B a moment to (wrongly) start if the queue is broken.
	time.Sleep(100 * time.Millisecond)
	_, errB := os.Stat(filepath.Join(gate, "B-start"))
	require.True(t, os.IsNotExist(errB), "B started while A was still running — queue is not serializing")

	// Release A, then B.
	require.NoError(t, os.WriteFile(filepath.Join(gate, "A-release"), nil, 0o644))
	waitForFile(t, filepath.Join(gate, "A-end"), 2*time.Second)
	waitForFile(t, filepath.Join(gate, "B-start"), 2*time.Second)
	require.NoError(t, os.WriteFile(filepath.Join(gate, "B-release"), nil, 0o644))
	waitForFile(t, filepath.Join(gate, "B-end"), 2*time.Second)

	// Ordering: A ended before B started.
	aEnd := readUnixNano(t, filepath.Join(gate, "A-end"))
	bStart := readUnixNano(t, filepath.Join(gate, "B-start"))
	assert.Greater(t, bStart, aEnd, "B started before A ended — turns overlapped")
}

func TestSessionRunnerCancelQueuedTurnSkipsDispatch(t *testing.T) {
	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	gate := writeGateClaude(t, fakeClaude)

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "main", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID, "")
	require.NoError(t, err)
	runner := NewSessionRunner(database)

	_, err = runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, Branch: "main",
		UserTurnType: "user_message", UserTurnContent: "TURN-A",
	})
	require.NoError(t, err)
	resB, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, Branch: "main",
		UserTurnType: "user_message", UserTurnContent: "TURN-B",
	})
	require.NoError(t, err)

	// Wait for A to start, then cancel B while A is still gated.
	waitForFile(t, filepath.Join(gate, "A-start"), 2*time.Second)
	require.NoError(t, runner.CancelTurn(ctx, resB.ResponseTurn.ID))

	// Release A and wait for it to finish.
	require.NoError(t, os.WriteFile(filepath.Join(gate, "A-release"), nil, 0o644))
	waitForFile(t, filepath.Join(gate, "A-end"), 2*time.Second)

	// Give the dispatcher time to (wrongly) start B if cancel-queue is broken.
	time.Sleep(100 * time.Millisecond)
	_, errBStart := os.Stat(filepath.Join(gate, "B-start"))
	assert := assert.New(t)
	assert.True(os.IsNotExist(errBStart), "B should not have started after being cancelled while queued")

	// B's row is "cancelled".
	row, err := database.GetWorktreeSessionTurn(ctx, resB.ResponseTurn.ID)
	require.NoError(t, err)
	assert.Equal("cancelled", row.Status)
}
