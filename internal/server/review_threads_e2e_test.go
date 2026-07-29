package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/worktrees"
)

// seedReviewWorktree registers a local repo + worktree row and returns its
// id (the "number" in PR-shaped local routes). No real git tree is needed:
// the review-thread routes only resolve the synthetic MR.
func seedReviewWorktree(t *testing.T, database *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: t.TempDir(), Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	return w.ID
}

func TestAPIReviewThreadsLifecycle(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	start := int64(8)
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc123", Body: "rename this"},
				{Path: "b.go", Side: "RIGHT", Line: 20, StartLine: &start, CommitSha: "abc123", Body: "extract a helper"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 2)
	assert.Equal("open", created[0].Status)
	require.NotNil(created[0].Comments)
	require.Len(*created[0].Comments, 1)
	assert.Equal("user", (*created[0].Comments)[0].Author)
	assert.Equal("rename this", (*created[0].Comments)[0].Body)
	// Second thread round-trips its multi-line anchor (start_line).
	assert.Equal("b.go", created[1].Path)
	require.NotNil(created[1].StartLine)
	assert.Equal(int64(8), *created[1].StartLine)
	threadID := created[0].Id

	// List returns both threads.
	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200)
	require.NotNil(listResp.JSON200.Threads)
	require.Len(*listResp.JSON200.Threads, 2)

	// Reply as the agent.
	agent := "agent"
	replyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AddReviewThreadCommentInputBody{Body: "agreed, will rename", Author: &agent},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, replyResp.StatusCode())
	require.NotNil(replyResp.JSON200)
	require.NotNil(replyResp.JSON200.Comments)
	require.Len(*replyResp.JSON200.Comments, 2)
	assert.Equal("agent", (*replyResp.JSON200.Comments)[1].Author)

	// Hide.
	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, hideResp.StatusCode())
	require.NotNil(hideResp.JSON200)
	assert.True(hideResp.JSON200.Hidden)

	// Unhide.
	unhideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdUnhideWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, unhideResp.StatusCode())
	require.NotNil(unhideResp.JSON200)
	assert.False(unhideResp.JSON200.Hidden)

	// Resolve.
	resolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resolveResp.StatusCode())
	require.NotNil(resolveResp.JSON200)
	assert.Equal("resolved", resolveResp.JSON200.Status)
}

func TestAPIReviewThreadUnresolve(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "x"}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	threadID := (*createResp.JSON200.Threads)[0].Id

	resolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdResolveWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resolveResp.StatusCode())
	assert.Equal("resolved", resolveResp.JSON200.Status)

	unresolveResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdUnresolveWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, unresolveResp.StatusCode())
	assert.Equal("open", unresolveResp.JSON200.Status)
}

func TestAPIReviewThreadsCreateWithAppendedComments(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{
				Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "why unbounded?",
				Comments: &[]generated.ReviewThreadDraftComment{
					{Author: "agent", Body: "bounded by ctx deadline"},
					{Author: "user", Body: "cap attempts too?"},
					{Author: "agent", Body: "add maxAttempts"},
				},
			}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)

	// Root 'user' comment followed by the appended comments, in order.
	require.NotNil(created[0].Comments)
	cs := *created[0].Comments
	require.Len(cs, 4)
	assert.Equal("user", cs[0].Author)
	assert.Equal("why unbounded?", cs[0].Body)
	assert.Equal("agent", cs[1].Author)
	assert.Equal("bounded by ctx deadline", cs[1].Body)
	assert.Equal("user", cs[2].Author)
	assert.Equal("cap attempts too?", cs[2].Body)
	assert.Equal("agent", cs[3].Author)
	assert.Equal("add maxAttempts", cs[3].Body)

	// A promoted thread carries agent input, so it is created 'discussed'.
	assert.Equal("discussed", created[0].Status)
}

// TestCreateReviewThreads_WorkingTreeSentinelResolvesToHead verifies that a
// draft thread whose commit_sha carries the WORKING-TREE sentinel — how the
// doc pane anchors a comment made against the live worktree, not a specific
// commit — is resolved to the worktree's real HEAD sha rather than being
// persisted verbatim (which would anchor the thread to a non-commit).
func TestCreateReviewThreads_WorkingTreeSentinelResolvesToHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{
				Path: "README.md", Side: "RIGHT", Line: 1,
				CommitSha: worktrees.WorkingTreeSentinel,
				Body:      "doc note",
			}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.Threads)
	created := *resp.JSON200.Threads
	require.Len(created, 1)

	got := created[0].CommitSha
	assert.NotEqual(worktrees.WorkingTreeSentinel, got, "sentinel must be resolved, not stored verbatim")
	assert.Len(got, 40, "resolved commit_sha should be a full SHA")

	head, err := worktrees.ResolveCommitSHA(ctx, dir, "HEAD")
	require.NoError(err)
	assert.Equal(head, got)
}

func TestAPIReviewThreadsCreateRejectsBadCommentAuthor(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{
				Path: "a.go", Side: "RIGHT", Line: 1, CommitSha: "abc", Body: "root",
				Comments: &[]generated.ReviewThreadDraftComment{{Author: "bot", Body: "nope"}},
			}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())
}

func TestAPIReviewThreadsRejectNonLocal(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	// GET on a non-local owner is rejected.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, getResp.StatusCode())

	// POST (create) on a non-local owner is rejected by the same guard.
	postResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "acme", "widget", 1,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 1, CommitSha: "abc", Body: "x"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, postResp.StatusCode())
}

// TestAPIReviewThreadActionUnknownThread covers the ownership guard: an
// action on a thread id that does not belong to this worktree is a 404.
func TestAPIReviewThreadActionUnknownThread(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	num := seedReviewWorktree(t, database)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdHideWithResponse(
		context.Background(), "local", "demo", num, 99999,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())
}

func TestAPIReviewThreadDelete(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
				{Path: "b.go", Side: "RIGHT", Line: 20, CommitSha: "abc", Body: "extract"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	created := *createResp.JSON200.Threads
	require.Len(created, 2)
	threadID := created[0].Id

	delResp, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, delResp.StatusCode())
	require.NotNil(delResp.JSON200)
	require.NotNil(delResp.JSON200.Threads)
	require.Len(*delResp.JSON200.Threads, 1)
	require.Equal(created[1].Id, (*delResp.JSON200.Threads)[0].Id)

	delAgain, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, delAgain.StatusCode())
}

// TestAPIReviewThreadCommentEdit verifies the .../comments/{comment_id}/edit
// sub-action: a reviewer can rewrite their own comment's body (stamping
// edited_at), while an agent comment, an empty body, and an unknown comment
// id are all rejected.
func TestAPIReviewThreadCommentEdit(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "original"}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	threadID := created[0].Id
	require.NotNil(created[0].Comments)
	require.Len(*created[0].Comments, 1)
	userCommentID := (*created[0].Comments)[0].Id
	assert.Nil((*created[0].Comments)[0].EditedAt, "a freshly created comment has no edited_at")

	// Reply as the agent so we have a non-editable comment to test against.
	agent := "agent"
	replyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AddReviewThreadCommentInputBody{Body: "agent reply", Author: &agent},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, replyResp.StatusCode())
	require.NotNil(replyResp.JSON200.Comments)
	require.Len(*replyResp.JSON200.Comments, 2)
	agentCommentID := (*replyResp.JSON200.Comments)[1].Id

	// --- happy path: edit the user's own comment ---
	editResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsByCommentIdEditWithResponse(
		ctx, "local", "demo", num, threadID, userCommentID,
		generated.EditReviewThreadCommentInputBody{Body: "reworded"},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, editResp.StatusCode())
	require.NotNil(editResp.JSON200)
	require.NotNil(editResp.JSON200.Comments)
	var edited *generated.ReviewThreadCommentResponse
	for i, c := range *editResp.JSON200.Comments {
		if c.Id == userCommentID {
			edited = &(*editResp.JSON200.Comments)[i]
		}
	}
	require.NotNil(edited, "edited comment should still be present in the reloaded thread")
	assert.Equal("reworded", edited.Body)
	require.NotNil(edited.EditedAt)
	assert.NotEmpty(*edited.EditedAt)

	// --- rejections ---

	// Editing an agent comment is not allowed (read-only).
	agentEditResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsByCommentIdEditWithResponse(
		ctx, "local", "demo", num, threadID, agentCommentID,
		generated.EditReviewThreadCommentInputBody{Body: "x"},
	)
	require.NoError(err)
	assert.Equal(http.StatusNotFound, agentEditResp.StatusCode())

	// The agent comment's body must be unchanged.
	listResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, listResp.StatusCode())
	require.NotNil(listResp.JSON200.Threads)
	var agentBody string
	for _, th := range *listResp.JSON200.Threads {
		if th.Id != threadID || th.Comments == nil {
			continue
		}
		for _, c := range *th.Comments {
			if c.Id == agentCommentID {
				agentBody = c.Body
			}
		}
	}
	assert.Equal("agent reply", agentBody)

	// Empty/whitespace-only body is rejected.
	emptyResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsByCommentIdEditWithResponse(
		ctx, "local", "demo", num, threadID, userCommentID,
		generated.EditReviewThreadCommentInputBody{Body: "   "},
	)
	require.NoError(err)
	assert.Equal(http.StatusUnprocessableEntity, emptyResp.StatusCode())

	// Unknown comment id is rejected.
	unknownResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdCommentsByCommentIdEditWithResponse(
		ctx, "local", "demo", num, threadID, 999999,
		generated.EditReviewThreadCommentInputBody{Body: "x"},
	)
	require.NoError(err)
	assert.Equal(http.StatusNotFound, unknownResp.StatusCode())
}

// TestAPIReviewThreadAskEngagesAgentAndMarksComment verifies the /ask
// endpoint persists the reviewer's comment, kicks off a steer turn, and
// marks the persisted comment sent_to_agent.
func TestAPIReviewThreadAskEngagesAgentAndMarksComment(t *testing.T) {
	require := require.New(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\n"+
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}'`+"\n"), 0o755))
	aireview.SetBinaryForTest(fake)
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	srv, database := setupTestServer(t)
	srv.sessionRunner = aireview.NewSessionRunner(database)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"}},
		})
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	threadID := (*createResp.JSON200.Threads)[0].Id

	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "why a mutex here?"})
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())
	require.NotNil(askResp.JSON200)
	require.NotNil(askResp.JSON200.Comments)
	var asked bool
	for _, c := range *askResp.JSON200.Comments {
		if c.Author == "user" && c.Body == "why a mutex here?" && c.SentToAgent {
			asked = true
		}
	}
	require.True(asked, "the ask comment should be marked sent_to_agent; comments=%+v", *askResp.JSON200.Comments)

	sessResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, sessResp.StatusCode())
	require.NotNil(sessResp.JSON200.Turns)
	require.NotEmpty(*sessResp.JSON200.Turns)
}
