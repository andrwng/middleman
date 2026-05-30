package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient/generated"
)

// fastFakeClaude writes a stub claude that emits a single success
// result line and exits immediately. Used by the deterministic
// kickoff/apply tests where we only need the turn machinery to run,
// not a real conversation.
func fastFakeClaude(t *testing.T) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "claude.sh")
	script := "#!/bin/sh\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}'` + "\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o755))
	return stub
}

// blockingFakeClaude writes a stub claude that sleeps before emitting
// its success line, keeping the response turn in flight long enough to
// observe the busy gate.
func blockingFakeClaude(t *testing.T) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "claude.sh")
	script := "#!/bin/sh\nsleep 2\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}'` + "\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o755))
	return stub
}

// TestAPIReviewThreadsDiscussKickoff verifies that creating threads
// with mode=discuss-first kicks off a discuss turn and marks the
// created threads "discussed" synchronously (the create handler reloads
// after kickoff, so the status is deterministic in the response).
func TestAPIReviewThreadsDiscussKickoff(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	aireview.SetBinaryForTest(fastFakeClaude(t))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	mode := "discuss-first"
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode: &mode,
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	assert.Equal("discussed", created[0].Status)

	// The session now has at least the user turn + queued response turn.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Turns)
	assert.NotEmpty(*getResp.JSON200.Turns)
}

// TestAPIReviewThreadsApplyMarksApplied creates persist-only threads
// (status open), then applies one and asserts it flips to "applied".
func TestAPIReviewThreadsApplyMarksApplied(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	aireview.SetBinaryForTest(fastFakeClaude(t))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	assert.Equal("open", created[0].Status)
	threadID := created[0].Id

	applyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdApplyWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, applyResp.StatusCode())
	require.NotNil(applyResp.JSON200)
	require.NotNil(applyResp.JSON200.Threads)
	var found *generated.ReviewThreadResponse
	for i := range *applyResp.JSON200.Threads {
		th := &(*applyResp.JSON200.Threads)[i]
		if th.Id == threadID {
			found = th
			break
		}
	}
	require.NotNil(found)
	assert.Equal("applied", found.Status)
}

// TestAPIReviewThreadsBusyConflict starts a discuss turn with a blocking
// fake claude (so the response turn stays queued/running), then asserts
// apply-all is rejected 409 while the agent is busy.
func TestAPIReviewThreadsBusyConflict(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	aireview.SetBinaryForTest(blockingFakeClaude(t))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	mode := "discuss-first"
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode: &mode,
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())

	// The queued response turn was inserted synchronously, so the
	// session is busy; apply-all must 409.
	applyAllResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsApplyAllWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusConflict, applyAllResp.StatusCode())

	// Kill the session so the suite doesn't linger on the blocking
	// fake claude's sleep; a late DB write after cleanup only warns.
	_, _ = client.HTTP.PostReposByOwnerByNamePullsByNumberSessionKillWithResponse(
		ctx, "local", "demo", num,
	)
}
