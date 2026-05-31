package aireview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

// fakeClaudeRecordingArgs writes its argv (newline-joined) to argsFile, then
// emits a minimal stream-json success line so the turn completes "done".
func fakeClaudeRecordingArgs(t *testing.T, path, argsFile string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sx"}'` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

// setupRecordingSessionTest mirrors setupSessionTest but swaps in the
// arg-recording fake claude so tests can assert the spawned argv.
func setupRecordingSessionTest(t *testing.T) (*db.DB, *SessionRunner, string, db.WorktreeSession, string) {
	t.Helper()
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "args.txt")
	fakeClaude := filepath.Join(tmp, "claude.sh")
	fakeClaudeRecordingArgs(t, fakeClaude, argsFile)

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: tmp, Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID)
	require.NoError(t, err)

	runner := NewSessionRunner(database)
	return database, runner, tmp, sess, argsFile
}

// waitTurnDone polls the response turn to a terminal status and returns it.
func waitTurnDone(t *testing.T, database *db.DB, turnID int64) db.WorktreeSessionTurn {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.GetWorktreeSessionTurn(ctx, turnID)
		require.NoError(t, err)
		if turn.Status == "done" || turn.Status == "failed" {
			return turn
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow(t, "turn never finished")
	return db.WorktreeSessionTurn{}
}

// allowedToolsArg returns the value passed to --allowedTools in the
// recorded argv (the fake writes one argv element per line).
func allowedToolsArg(t *testing.T, argsFile string) string {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i, l := range lines {
		if l == "--allowedTools" && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	require.FailNow(t, "--allowedTools not found in recorded argv")
	return ""
}

func TestDiscussTurnIsReadOnlyAndConfiguresMCP(t *testing.T) {
	require := require.New(t)
	database, runner, tmp, sess, argsFile := setupRecordingSessionTest(t)
	ctx := context.Background()

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		IsFirstTurn:     true,
		Action:          "discuss",
		UserTurnType:    "review_feedback",
		UserTurnContent: "Reply to the threads.",
		Threads: []ThreadContext{
			{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"},
		},
		MCP: &MCPConfig{
			Binary:  "/bin/true",
			BaseURL: "http://127.0.0.1:8091",
			Owner:   "local",
			Name:    "demo",
			Number:  int(sess.ID),
		},
	})
	require.NoError(err)

	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status, "turn never moved to done; raw=%s err=%s", turn.RawJSON, turn.Error)

	argv, err := os.ReadFile(argsFile)
	require.NoError(err)
	args := string(argv)
	require.Contains(args, "--mcp-config")
	// discuss is read-only: exact gating, no Edit/Write/Bash.
	require.Equal(
		"Read,Glob,Grep,mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread",
		allowedToolsArg(t, argsFile),
	)
}

func TestApplyTurnGetsEditTools(t *testing.T) {
	require := require.New(t)
	database, runner, tmp, sess, argsFile := setupRecordingSessionTest(t)
	ctx := context.Background()

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		IsFirstTurn:     true,
		Action:          "apply",
		UserTurnType:    "user_message",
		UserTurnContent: "Apply the change.",
		Threads: []ThreadContext{
			{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"},
		},
		MCP: &MCPConfig{
			Binary:  "/bin/true",
			BaseURL: "http://127.0.0.1:8091",
			Owner:   "local",
			Name:    "demo",
			Number:  int(sess.ID),
		},
	})
	require.NoError(err)

	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status, "turn never moved to done; raw=%s err=%s", turn.RawJSON, turn.Error)

	argv, err := os.ReadFile(argsFile)
	require.NoError(err)
	args := string(argv)
	require.Contains(args, "--mcp-config")
	// apply gets the edit tools appended after the read-only + mcp set.
	require.Equal(
		"Read,Glob,Grep,mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread,Edit,Write,MultiEdit,Bash",
		allowedToolsArg(t, argsFile),
	)
}

func TestSteerTurnIsReadOnlyAndCarriesTheMessage(t *testing.T) {
	require := require.New(t)
	database, runner, tmp, sess, argsFile := setupRecordingSessionTest(t)
	ctx := context.Background()

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		IsFirstTurn:     false,
		Action:          "steer",
		UserTurnType:    "user_message",
		UserTurnContent: "Can you clarify why this needs a mutex?",
		Threads: []ThreadContext{
			{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"},
		},
		MCP: &MCPConfig{Binary: "/bin/true", BaseURL: "http://127.0.0.1:8091", Owner: "local", Name: "demo", Number: int(sess.ID)},
	})
	require.NoError(err)
	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status, "raw=%s err=%s", turn.RawJSON, turn.Error)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	a := string(args)
	require.Contains(a, "--mcp-config")
	// steer is read-only: exact gating, no Edit/Write/Bash.
	require.Equal(
		"Read,Glob,Grep,mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread",
		allowedToolsArg(t, argsFile),
	)
	// The steer prompt carries the reviewer's message AND instructs the reply tool.
	require.Contains(a, "Can you clarify why this needs a mutex?")
	require.Contains(a, "continue the discussion")
}
