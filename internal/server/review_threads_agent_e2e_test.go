package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestAPIReviewThreadDiscussMarksDiscussed creates a persist-only thread
// (status open), then kicks a read-only discuss turn via the /discuss
// endpoint and asserts the thread flips to "discussed".
func TestAPIReviewThreadDiscussMarksDiscussed(t *testing.T) {
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
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	assert.Equal("open", created[0].Status)
	threadID := created[0].Id

	discussResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdDiscussWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, discussResp.StatusCode())
	require.NotNil(discussResp.JSON200)
	require.NotNil(discussResp.JSON200.Threads)
	var found *generated.ReviewThreadResponse
	for i := range *discussResp.JSON200.Threads {
		th := &(*discussResp.JSON200.Threads)[i]
		if th.Id == threadID {
			found = th
			break
		}
	}
	require.NotNil(found)
	assert.Equal("discussed", found.Status)
}

// TestAPIReviewThreadsBusyConflict starts a discuss turn with a blocking
// fake claude (so the response turn stays queued/running), then asserts
// apply-all is accepted and enqueues a second turn rather than 409ing.
func TestAPIReviewThreadsBusyConflict(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
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

	// The first discuss turn is running (blocking fake claude), so the
	// second engage (apply-all) should join the queue and return 2xx.
	applyAllResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsApplyAllWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, applyAllResp.StatusCode())

	// The session must have at least one queued claude_response turn
	// waiting behind the running discuss turn.
	sess, err := database.GetActiveWorktreeSession(ctx, num, "feat/x")
	require.NoError(err)
	turns, err := database.ListWorktreeSessionTurns(ctx, sess.ID)
	require.NoError(err)
	var queuedCount int
	for _, tn := range turns {
		if tn.TurnType == "claude_response" && tn.Status == "queued" {
			queuedCount++
		}
	}
	assert.GreaterOrEqual(queuedCount, 1, "second apply-all should have enqueued a response turn")

	// Kill the session so the suite doesn't linger on the blocking
	// fake claude's sleep; a late DB write after cleanup only warns.
	_, _ = client.HTTP.PostReposByOwnerByNamePullsByNumberSessionKillWithResponse(
		ctx, "local", "demo", num,
	)
}

// TestAPIReviewThreadAskWhileBusyQueuesTheTurn verifies the
// persist-before-kickoff invariant of /ask: a reviewer's message is never
// lost while the agent is busy. The first ask kicks a steer turn that
// blocks (blocking fake claude). A second ask while busy must return 2xx
// and the comment must persist and be marked sent_to_agent (a queued turn
// was kicked for it).
func TestAPIReviewThreadAskWhileBusyQueuesTheTurn(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	aireview.SetBinaryForTest(blockingFakeClaude(t))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create a thread to ask on (persist-only, no agent turn yet).
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
	threadID := (*createResp.JSON200.Threads)[0].Id

	// First ask: kicks a steer turn that blocks, so the session is busy.
	first, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "first ask"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, first.StatusCode())

	// Second ask WHILE BUSY: queue accepts it, returns 2xx.
	second, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "second ask while busy"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, second.StatusCode())

	// The reviewer's message was persisted and marked sent_to_agent
	// (a queued turn was kicked for it).
	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200)
	require.NotNil(listResp.JSON200.Threads)
	var found bool
	var marked bool
	for _, th := range *listResp.JSON200.Threads {
		if th.Comments == nil {
			continue
		}
		for _, c := range *th.Comments {
			if c.Body == "second ask while busy" {
				found = true
				marked = c.SentToAgent
			}
		}
	}
	assert.True(found, "the busy ask's comment must persist")
	assert.True(marked, "a queued ask must be marked sent_to_agent")

	// Kill the session so the suite doesn't linger on the blocking
	// fake claude's sleep; a late DB write after cleanup only warns.
	_, _ = client.HTTP.PostReposByOwnerByNamePullsByNumberSessionKillWithResponse(
		ctx, "local", "demo", num,
	)
}

// TestDiscussDoesNotDowngradeAppliedStatus verifies that firing /discuss on a
// thread already in "applied" state does NOT downgrade the status back to
// "discussed". "applied" is the writes-allowed signal and must stay sticky
// across later discuss turns.
func TestDiscussDoesNotDowngradeAppliedStatus(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	aireview.SetBinaryForTest(fastFakeClaude(t))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create a persist-only thread (status "open").
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "fix this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	threadID := created[0].Id

	// Apply the thread — status must become "applied".
	applyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdApplyWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, applyResp.StatusCode())
	require.NotNil(applyResp.JSON200)
	require.NotNil(applyResp.JSON200.Threads)
	var afterApply *generated.ReviewThreadResponse
	for i := range *applyResp.JSON200.Threads {
		th := &(*applyResp.JSON200.Threads)[i]
		if th.Id == threadID {
			afterApply = th
			break
		}
	}
	require.NotNil(afterApply)
	assert.Equal("applied", afterApply.Status)

	// Now fire /discuss on the same thread. The status must stay "applied",
	// not downgrade to "discussed".
	discussResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdDiscussWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, discussResp.StatusCode())
	require.NotNil(discussResp.JSON200)
	require.NotNil(discussResp.JSON200.Threads)
	var afterDiscuss *generated.ReviewThreadResponse
	for i := range *discussResp.JSON200.Threads {
		th := &(*discussResp.JSON200.Threads)[i]
		if th.Id == threadID {
			afterDiscuss = th
			break
		}
	}
	require.NotNil(afterDiscuss)
	assert.Equal("applied", afterDiscuss.Status)
}

// recordingFakeClaude writes the args passed to claude to outPath
// before emitting its success result. Used by tests that need to
// assert on the --allowedTools list.
func recordingFakeClaude(t *testing.T, outPath string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "claude.sh")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + outPath + "\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}'` + "\n"
	require.NoError(t, os.WriteFile(stub, []byte(script), 0o755))
	return stub
}

// waitForFile polls until the file at path is non-empty or timeout elapses.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fi, err := os.Stat(path)
		if err == nil && fi.Size() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow(t, "timed out waiting for "+path+" to be non-empty")
}

// TestSteerOnAppliedThreadGetsWriteTools verifies that an /ask turn
// on a thread that is already in "applied" state receives the edit
// tools (Edit/Write/MultiEdit/Bash) in its --allowedTools list.
func TestSteerOnAppliedThreadGetsWriteTools(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)

	argsPath := filepath.Join(t.TempDir(), "args.log")
	aireview.SetBinaryForTest(recordingFakeClaude(t, argsPath))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create with act-immediately so the thread goes straight to "applied".
	mode := "act-immediately"
	threads := []generated.ReviewThreadDraft{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "deadbeef", Body: "fix this"},
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode:    &mode,
			Threads: &threads,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	require.Len(*createResp.JSON200.Threads, 1)
	threadID := (*createResp.JSON200.Threads)[0].Id

	// Wait for the apply turn's args to land.
	waitForFile(t, argsPath, 3*time.Second)

	// Truncate the log so the next turn's args are isolated.
	require.NoError(os.Truncate(argsPath, 0))

	// Now /ask on the same thread; should get edit tools.
	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "now also rename Foo to Bar"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())

	waitForFile(t, argsPath, 3*time.Second)
	b, err := os.ReadFile(argsPath)
	require.NoError(err)
	require.Contains(string(b), "Edit,Write,MultiEdit,Bash")
}

// TestSteerOnUnappliedThreadStaysReadOnly verifies that an /ask turn
// on a thread that has NOT been applied does NOT receive edit tools.
func TestSteerOnUnappliedThreadStaysReadOnly(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)

	argsPath := filepath.Join(t.TempDir(), "args.log")
	aireview.SetBinaryForTest(recordingFakeClaude(t, argsPath))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create a thread WITHOUT applying (persist-only → status "open").
	mode := "persist-only"
	threads := []generated.ReviewThreadDraft{
		{Path: "b.go", Side: "RIGHT", Line: 5, CommitSha: "deadbeef", Body: "question?"},
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode:    &mode,
			Threads: &threads,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	require.Len(*createResp.JSON200.Threads, 1)
	threadID := (*createResp.JSON200.Threads)[0].Id

	// /ask — should NOT get edit tools.
	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "what did you mean"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())

	waitForFile(t, argsPath, 3*time.Second)
	b, err := os.ReadFile(argsPath)
	require.NoError(err)
	require.NotContains(string(b), "Edit,Write,MultiEdit,Bash")
}

// TestPureCommentsAreFlushedOnNextDiscuss verifies that Send-only (pure)
// user comments stacked between engage turns are included in the prompt of
// the next /discuss and then marked sent_to_agent.
func TestPureCommentsAreFlushedOnNextDiscuss(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)

	argsPath := filepath.Join(t.TempDir(), "args.log")
	aireview.SetBinaryForTest(recordingFakeClaude(t, argsPath))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create a thread (persist-only — no engage at creation).
	mode := "persist-only"
	threads := []generated.ReviewThreadDraft{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "deadbeef", Body: "root concern"},
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode:    &mode,
			Threads: &threads,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	thID := (*createResp.JSON200.Threads)[0].Id

	// Stack two pure "Send-only" comments.
	for _, body := range []string{"follow-up 1", "follow-up 2"} {
		_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
			ctx, "local", "demo", num, thID,
			generated.AddReviewThreadCommentInputBody{Body: body},
		)
		require.NoError(err)
	}

	// Kick a discuss; the prompt should carry the stacked block.
	discussResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdDiscussWithResponse(
		ctx, "local", "demo", num, thID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, discussResp.StatusCode())

	waitForFile(t, argsPath, 3*time.Second)
	args, err := os.ReadFile(argsPath)
	require.NoError(err)
	joined := string(args)
	assert := Assert.New(t)
	assert.Contains(joined, "Reviewer's notes since the last engage")
	assert.Contains(joined, "follow-up 1")
	assert.Contains(joined, "follow-up 2")

	// All three comments (root + two follow-ups) are now sent_to_agent=true.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Threads)
	thread := (*getResp.JSON200.Threads)[0]
	require.NotNil(thread.Comments)
	for _, c := range *thread.Comments {
		if c.Author == "user" {
			assert.True(c.SentToAgent, "user comment %d (%q) should be marked sent", c.Id, c.Body)
		}
	}
}

// TestSteerAfterResolveUnresolveLosesWriteTools verifies that a thread
// which was applied, then resolved, then unresolved (status back to "open")
// does NOT receive edit tools on a subsequent /ask. Resolving then
// unresolving must reset the writes-allowed gate — the thread is no longer
// in "applied" state.
func TestSteerAfterResolveUnresolveLosesWriteTools(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)

	argsPath := filepath.Join(t.TempDir(), "args.log")
	aireview.SetBinaryForTest(recordingFakeClaude(t, argsPath))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	// Create + apply a thread so status becomes "applied".
	mode := "act-immediately"
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode: &mode,
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "deadbeef", Body: "fix this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	thID := (*createResp.JSON200.Threads)[0].Id

	// Wait for the apply turn to complete, then clear the log.
	waitForFile(t, argsPath, 3*time.Second)
	require.NoError(os.Truncate(argsPath, 0))

	// Resolve, then unresolve — status goes applied → resolved → open.
	resolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse(
		ctx, "local", "demo", num, thID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resolveResp.StatusCode())

	unresolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdUnresolveWithResponse(
		ctx, "local", "demo", num, thID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, unresolveResp.StatusCode())

	// /ask now — status is "open", so no edit tools.
	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, thID,
		generated.AskReviewThreadInputBody{Body: "is this ok"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())

	waitForFile(t, argsPath, 3*time.Second)
	args, err := os.ReadFile(argsPath)
	require.NoError(err)
	require.NotContains(string(args), "Edit,Write,MultiEdit,Bash")
}

// TestAskFlushesStackedPureCommentsTogether verifies that when /ask is called,
// it flushes all prior unsent pure comments plus the ask body together in the
// steer prompt.
func TestAskFlushesStackedPureCommentsTogether(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)

	argsPath := filepath.Join(t.TempDir(), "args.log")
	aireview.SetBinaryForTest(recordingFakeClaude(t, argsPath))
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	mode := "persist-only"
	threads := []generated.ReviewThreadDraft{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "deadbeef", Body: "root concern"},
	}
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Mode:    &mode,
			Threads: &threads,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	thID := (*createResp.JSON200.Threads)[0].Id

	// Two pure comments + one Ask.
	for _, body := range []string{"pure-1", "pure-2"} {
		_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
			ctx, "local", "demo", num, thID,
			generated.AddReviewThreadCommentInputBody{Body: body},
		)
		require.NoError(err)
	}

	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, thID,
		generated.AskReviewThreadInputBody{Body: "and one more thing"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())

	waitForFile(t, argsPath, 3*time.Second)
	args, err := os.ReadFile(argsPath)
	require.NoError(err)
	joined := string(args)
	assert := Assert.New(t)
	assert.Contains(joined, "pure-1")
	assert.Contains(joined, "pure-2")
	assert.Contains(joined, "and one more thing")
}

// TestAPICreateReviewThreadEmptyCommitSHAResolvesToLiveHead verifies that
// POSTing a thread with commit_sha="" causes the server to fill in the
// worktree's live HEAD SHA via git rev-parse (not the stale scanned value).
//
// Uses seedReviewWorktreeGit (a real git repo) so ResolveCommitSHA succeeds.
// The worktree's DB HeadSHA is set to the stale value "deadbeef"; the test
// asserts the response commit_sha is a full 40-char SHA (the real git HEAD),
// not "deadbeef".
func TestAPICreateReviewThreadEmptyCommitSHAResolvesToLiveHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	num, _ := seedReviewWorktreeGit(t, database)

	drafts := []generated.ReviewThreadDraft{
		{Path: "main.go", Side: "RIGHT", Line: 1, Body: "check this", CommitSha: ""},
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &drafts,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	threads := *resp.JSON200.Threads
	require.Len(threads, 1)

	sha := threads[0].CommitSha
	assert.NotEmpty(sha, "commit_sha should be filled in by server")
	assert.NotEqual("deadbeef", sha, "should not be stale DB value")
	assert.Len(sha, 40, "should be a full 40-char git SHA")
}

// TestAPICreateReviewThreadShortCommitSHACanonicalizes verifies that a
// caller-supplied short SHA (as an agent calling start_thread would send,
// having seen an abbreviated HEAD in its worktree prompt) is canonicalized
// to the full 40-char SHA server-side. Without this, the thread would
// compare unequal to the full SHAs in the commit list and render as a
// spurious "orphan" in the picker.
func TestAPICreateReviewThreadShortCommitSHACanonicalizes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	num, dir := seedReviewWorktreeGit(t, database)

	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	fullSHA := strings.TrimSpace(string(headOut))
	require.Len(fullSHA, 40)
	shortSHA := fullSHA[:7]

	drafts := []generated.ReviewThreadDraft{
		{Path: "main.go", Side: "RIGHT", Line: 1, Body: "check this", CommitSha: shortSHA},
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &drafts,
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	threads := *resp.JSON200.Threads
	require.Len(threads, 1)

	assert.Equal(fullSHA, threads[0].CommitSha, "short SHA should be canonicalized to the full SHA")
}
