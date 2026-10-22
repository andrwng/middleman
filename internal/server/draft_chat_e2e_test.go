package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
)

const chatOK = `echo '{"type":"system","subtype":"init","session_id":"sess-'$$'"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Because the error is dropped.","session_id":"sess-'$$'"}'
`

// setupChatServer starts a server whose fake claude plays the review
// run (session sess-root, after a short pause so the test can propose)
// and answers chat turns with chatBody. Chat calls are logged one per
// line to the returned file, with the working directory in .cwd.
func setupChatServer(t *testing.T, chatBody string) (*apiclient.Client, string) {
	t.Helper()
	body := `case "$2" in
  *"We are discussing"*)
    echo "$*" | tr '\n' ' ' >> "$0.calls"; echo >> "$0.calls"; pwd >> "$0.cwd"
` + chatBody + `    ;;
  *)
    sleep 1
    echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
    echo '{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"sess-root"}'
    ;;
esac
`
	client, _, argsFile := setupReviewRunServer(t, body)
	return client, filepath.Join(filepath.Dir(argsFile), "claude.sh")
}

func chatCalls(t *testing.T, fake string) []string {
	t.Helper()
	b, err := os.ReadFile(fake + ".calls")
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// seedSuggestion runs a review and proposes one suggestion while it runs.
func seedSuggestion(t *testing.T, client *apiclient.Client) string {
	t.Helper()
	ctx := context.Background()
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, start.StatusCode())
	path, line := firstAddedLine(t, client)
	prop, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, start.JSON200.Id,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "Handle the error.", Rationale: "err is dropped"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, prop.StatusCode())
	require.Eventually(t, func() bool {
		r := latestRun(t, client)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 50*time.Millisecond)
	return prop.JSON200.Id
}

func sendChat(t *testing.T, client *apiclient.Client, draftID, body string, fresh bool) []generated.DraftMessageResponse {
	t.Helper()
	req := generated.SendDraftMessageInputBody{Body: body}
	if fresh {
		req.Fresh = &fresh
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesWithResponse(context.Background(), "acme", "widgets", 1, draftID, req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode(), string(resp.Body))
	return *resp.JSON200.Messages
}

func waitLastClaude(t *testing.T, client *apiclient.Client, draftID, status string) generated.DraftMessageResponse {
	t.Helper()
	var last generated.DraftMessageResponse
	require.Eventually(t, func() bool {
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesWithResponse(context.Background(), "acme", "widgets", 1, draftID)
		if err != nil || resp.JSON200 == nil {
			return false
		}
		for _, m := range *resp.JSON200.Messages {
			if m.Author == "claude" && m.Kind == "message" {
				last = m
			}
		}
		return last.Status == status
	}, 15*time.Second, 50*time.Millisecond, "waiting for claude message status %s", status)
	return last
}

func TestAPIDraftChatForkThenResume(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, fake := setupChatServer(t, chatOK)
	draftID := seedSuggestion(t, client)

	msgs := sendChat(t, client, draftID, "why?", false)
	// rationale, user question, queued reply
	require.GreaterOrEqual(len(msgs), 3)
	assert.Equal("err is dropped", msgs[0].Body)
	reply := waitLastClaude(t, client, draftID, "done")
	assert.Equal("Because the error is dropped.", reply.Body)

	sendChat(t, client, draftID, "and?", false)
	require.Eventually(func() bool { return len(chatCalls(t, fake)) == 2 }, 15*time.Second, 50*time.Millisecond)
	waitLastClaude(t, client, draftID, "done")
	calls := chatCalls(t, fake)
	assert.Contains(calls[0], "--resume sess-root --fork-session")
	assert.Contains(calls[1], "--resume sess-")
	assert.NotContains(calls[1], "--fork-session")
	assert.NotContains(calls[1], "sess-root")
}

func TestAPIDraftChatRevisionRules(t *testing.T) {
	cases := []struct {
		name        string
		accept      bool
		editDuring  bool
		wantApplied bool
		wantBody    string
	}{
		{name: "suggestion is revised in place", wantApplied: true, wantBody: "revised"},
		{name: "reviewer edit during the turn wins", editDuring: true, wantApplied: false, wantBody: "reviewer text"},
		{name: "accepted draft gets an offer", accept: true, wantApplied: false, wantBody: "Handle the error."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := require.New(t)
			ctx := context.Background()
			client, _ := setupChatServer(t, "sleep 2\n"+chatOK)
			draftID := seedSuggestion(t, client)
			if tc.accept {
				_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, draftID)
				must.NoError(err)
			}
			sendChat(t, client, draftID, "make it shorter", false)
			waitLastClaude(t, client, draftID, "running")
			if tc.editDuring {
				_, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, draftID,
					generated.UpdateReviewDraftInputBody{Body: "reviewer text"})
				must.NoError(err)
			}
			rev, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdRevisionsWithResponse(ctx, "acme", "widgets", 1, draftID,
				generated.ReviseDraftInputBody{Body: "revised"})
			must.NoError(err)
			must.Equal(http.StatusOK, rev.StatusCode(), string(rev.Body))
			must.Equal(tc.wantApplied, rev.JSON200.Applied)
			d, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, draftID)
			must.NoError(err)
			must.Equal(tc.wantBody, d.JSON200.Body)

			if !tc.wantApplied {
				applied, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesByMessageIdApplyWithResponse(ctx, "acme", "widgets", 1, draftID, rev.JSON200.Id)
				must.NoError(err)
				must.Equal(http.StatusOK, applied.StatusCode())
				must.Equal("revised", applied.JSON200.Body)
				reverted, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesByMessageIdRevertWithResponse(ctx, "acme", "widgets", 1, draftID, rev.JSON200.Id)
				must.NoError(err)
				must.Equal(tc.wantBody, reverted.JSON200.Body)
			}
			waitLastClaude(t, client, draftID, "done")
		})
	}
}

func TestAPIDraftChatRevisionRejected(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	client, _ := setupChatServer(t, chatOK)
	draftID := seedSuggestion(t, client)

	noTurn, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdRevisionsWithResponse(ctx, "acme", "widgets", 1, draftID,
		generated.ReviseDraftInputBody{Body: "revised"})
	require.NoError(err)
	require.Equal(http.StatusConflict, noTurn.StatusCode())

	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdDiscardWithResponse(ctx, "acme", "widgets", 1, draftID)
	require.NoError(err)
	discarded, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesWithResponse(ctx, "acme", "widgets", 1, draftID,
		generated.SendDraftMessageInputBody{Body: "why?"})
	require.NoError(err)
	require.Equal(http.StatusConflict, discarded.StatusCode())
	d, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, draftID)
	require.NoError(err)
	require.Equal("Handle the error.", d.JSON200.Body)
}

func TestAPIDraftChatFreshAfterFailure(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	failOnResume := `case " $* " in *" --resume "*) echo 'No conversation found with session ID' >&2; exit 1;; esac
` + chatOK
	client, fake := setupChatServer(t, failOnResume)
	draftID := seedSuggestion(t, client)

	sendChat(t, client, draftID, "why?", false)
	failed := waitLastClaude(t, client, draftID, "failed")
	require.NotNil(failed.Error)
	assert.Contains(*failed.Error, "No conversation found")

	sendChat(t, client, draftID, "why?", true)
	waitLastClaude(t, client, draftID, "done")
	calls := chatCalls(t, fake)
	require.Len(calls, 2)
	assert.NotContains(calls[1], "--resume")
	cwds, err := os.ReadFile(fake + ".cwd")
	require.NoError(err)
	assert.Contains(strings.Split(strings.TrimSpace(string(cwds)), "\n")[1], "draft-"+draftID)
}

func TestAPIDraftChatUserDraftStartsFresh(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	client, fake := setupChatServer(t, chatOK)
	path, line := firstAddedLine(t, client)
	id := "user-draft-1"
	_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widgets", 1,
		generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{{Id: &id, Path: path, Side: "RIGHT", Line: line, CommitSha: "", Body: "my note"}}})
	require.NoError(err)
	sendChat(t, client, id, "is this right?", false)
	waitLastClaude(t, client, id, "done")
	calls := chatCalls(t, fake)
	require.Len(calls, 1)
	require.NotContains(calls[0], "--resume")
	require.Contains(calls[0], "Pull request") // fresh sessions get the PR context block
}

func TestAPIDraftChatRejects(t *testing.T) {
	ctx := context.Background()
	client, _ := setupChatServer(t, chatOK)
	cases := []struct {
		name, owner, draft, body string
		want                     int
	}{
		{"unknown local worktree", "local", "x", "hi", http.StatusNotFound},
		{"empty body", "acme", "x", "  ", http.StatusBadRequest},
		{"unknown draft", "acme", "missing", "hi", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesWithResponse(ctx, tc.owner, "widgets", 1, tc.draft,
				generated.SendDraftMessageInputBody{Body: tc.body})
			if Assert.NoError(t, err) {
				Assert.Equal(t, tc.want, resp.StatusCode())
			}
		})
	}
}

// An edit made while a turn is still queued counts as "during the turn":
// Claude's revision must be offered, and its prompt must show the edit.
func TestAPIDraftChatEditWhileQueued(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()
	client, fake := setupChatServer(t, "sleep 2\n"+chatOK)
	draftID := seedSuggestion(t, client)

	sendChat(t, client, draftID, "first question", false)
	waitLastClaude(t, client, draftID, "running")
	sendChat(t, client, draftID, "make it shorter", false) // queued behind the first
	_, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, draftID,
		generated.UpdateReviewDraftInputBody{Body: "reviewer text"})
	require.NoError(err)

	require.Eventually(func() bool { return len(chatCalls(t, fake)) == 2 }, 15*time.Second, 50*time.Millisecond)
	waitLastClaude(t, client, draftID, "running")
	rev, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdRevisionsWithResponse(ctx, "acme", "widgets", 1, draftID,
		generated.ReviseDraftInputBody{Body: "revised"})
	require.NoError(err)
	require.Equal(http.StatusOK, rev.StatusCode(), string(rev.Body))
	assert.False(rev.JSON200.Applied)
	d, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, draftID)
	require.NoError(err)
	assert.Equal("reviewer text", d.JSON200.Body)
	assert.Contains(chatCalls(t, fake)[1], "reviewer text")
	waitLastClaude(t, client, draftID, "done")
}
