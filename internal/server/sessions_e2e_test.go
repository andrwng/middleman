package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

// fakeClaudeScript writes a shell stub at path that emits the given
// JSON on stdout. Same shape as the runner's writeFakeClaude helper
// but reachable from the server package.
func fakeClaudeScript(t *testing.T, path, json string) {
	t.Helper()
	script := "#!/bin/sh\ncat <<'EOF'\n" + json + "\nEOF\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func TestAPISessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)

	// Wire the session runner — setupTestServer doesn't because it
	// passes nil clones, which gates the regular runner. The session
	// runner doesn't depend on clones; we attach it directly.
	srv.sessionRunner = aireview.NewSessionRunner(database)

	// Make `claude` resolve to our stub for this test.
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "claude.sh")
	fakeClaudeScript(t, stub,
		`{"type":"result","subtype":"success","is_error":false,"result":"applied the requested edits","session_id":"sess-zzz"}`)
	oldClaude := aireview.SetBinaryForTest(stub)
	t.Cleanup(func() { aireview.SetBinaryForTest(oldClaude) })

	client := setupTestClient(t, srv)
	ctx := context.Background()

	// Build a worktree on disk.
	dir := filepath.Join(tmp, "wt")
	require.NoError(os.MkdirAll(dir, 0o755))
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	runGitWT(t, dir, "commit", "--allow-empty", "-m", "init")

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	// Initial GET should be empty.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	// No active session → zero-value session (the OpenAPI generator
	// flattens nullable values) and empty turns.
	assert.Zero(getResp.JSON200.Session.Id)
	if getResp.JSON200.Turns != nil {
		assert.Empty(*getResp.JSON200.Turns)
	}

	// Submit a review_feedback turn.
	submitResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberSessionTurnsWithResponse(
		ctx, "local", "demo", w.ID,
		generated.SubmitTurnInputBody{
			Type:    "review_feedback",
			Content: "please rename foo() to bar()",
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, submitResp.StatusCode())
	require.NotNil(submitResp.JSON200)
	assert.Equal("review_feedback", submitResp.JSON200.UserTurn.TurnType)
	assert.Equal("done", submitResp.JSON200.UserTurn.Status)
	assert.Equal("claude_response", submitResp.JSON200.ResponseTurn.TurnType)

	respTurnID := submitResp.JSON200.ResponseTurn.Id

	// Wait for the response turn to land.
	deadline := time.Now().Add(3 * time.Second)
	var finalContent string
	var finalStatus string
	for time.Now().Before(deadline) {
		listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(
			ctx, "local", "demo", w.ID,
		)
		require.NoError(err)
		require.NotNil(listResp.JSON200)
		require.NotNil(listResp.JSON200.Session)
		require.NotNil(listResp.JSON200.Turns)
		var found bool
		for _, turn := range *listResp.JSON200.Turns {
			if turn.Id == respTurnID && (turn.Status == "done" || turn.Status == "failed") {
				finalContent = turn.Content
				finalStatus = turn.Status
				found = true
				break
			}
		}
		_ = listResp.JSON200.Session // avoid unused-field gripes
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal("done", finalStatus, "turn never reached terminal status")
	assert.Contains(finalContent, "applied the requested edits")
}

func TestAPISessionRejectsNonLocal(t *testing.T) {
	srv, _ := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(nil)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(t, err)
	Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
}
