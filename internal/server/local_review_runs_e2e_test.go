package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

// setupLocalReviewServer is the PR-review fixture plus a local worktree
// "demo" (branch feat/a) whose committed a.go has 20 lines and whose
// working tree adds an uncommitted line 21.
func setupLocalReviewServer(t *testing.T, claudeBody string) (*apiclient.Client, *db.DB, int64, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	client, database, argsFile := setupReviewRunServer(t, claudeBody)
	num, dir := seedReviewWorktreeGit(t, database)
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	committed := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(committed), 0o644))
	runGit(t, dir, "add", "a.go")
	runGit(t, dir, "commit", "-m", "add a.go")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(committed+"line 21 uncommitted\n"), 0o644))
	return client, database, num, dir, filepath.Join(filepath.Dir(argsFile), "claude.sh")
}

func localLatestRun(t *testing.T, client *apiclient.Client, num int64) *generated.AiReviewRunResponse {
	t.Helper()
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberAiReviewRunsLatestWithResponse(context.Background(), "local", "demo", num)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	r := resp.JSON200.Run
	if r.Id == 0 {
		return nil
	}
	return &r
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	return string(out)
}

func TestAPILocalReviewRun(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()
	client, _, num, dir, _ := setupLocalReviewServer(t, "sleep 2\n"+okStream)
	statusBefore := gitOut(t, dir, "status", "--porcelain")
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "local", "demo", num, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	require.Equal(http.StatusOK, start.StatusCode(), string(start.Body))
	assert.Equal(head, start.JSON200.HeadSha)
	runID := start.JSON200.Id
	assert.Equal(statusBefore, gitOut(t, dir, "status", "--porcelain"))
	assert.NotEmpty(strings.TrimSpace(gitOut(t, dir, "rev-parse", "refs/middleman/review-snapshots/"+itoa(runID))))

	ok, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "local", "demo", num, runID,
		generated.ProposeCommentInputBody{Path: "a.go", Side: "RIGHT", Line: 21, Body: "Uncommitted line needs a test.", Rationale: "new code"})
	require.NoError(err)
	require.Equal(http.StatusOK, ok.StatusCode(), string(ok.Body))
	assert.Equal(head, ok.JSON200.CommitSha) // anchored to HEAD, not the snapshot

	outside, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "local", "demo", num, runID,
		generated.ProposeCommentInputBody{Path: "a.go", Side: "RIGHT", Line: 1, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, outside.StatusCode())

	require.Eventually(func() bool {
		r := localLatestRun(t, client, num)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 50*time.Millisecond)
	assert.Equal(statusBefore, gitOut(t, dir, "status", "--porcelain"))
}

func TestAPILocalDraftChat(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()
	body := `case "$2" in
  *"We are discussing"*)
    echo "$*" | tr '\n' ' ' >> "$0.calls"; echo >> "$0.calls"; pwd >> "$0.cwd"
` + chatOK + `    ;;
  *)
    sleep 1
    echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
    echo '{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"sess-root"}'
    ;;
esac
`
	client, _, num, dir, fake := setupLocalReviewServer(t, body)
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "local", "demo", num, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	prop, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "local", "demo", num, start.JSON200.Id,
		generated.ProposeCommentInputBody{Path: "a.go", Side: "RIGHT", Line: 21, Body: "b", Rationale: "r"})
	require.NoError(err)
	require.Equal(http.StatusOK, prop.StatusCode(), string(prop.Body))
	require.Eventually(func() bool {
		r := localLatestRun(t, client, num)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 50*time.Millisecond)

	send := func(id, text string) {
		resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdMessagesWithResponse(ctx, "local", "demo", num, id,
			generated.SendDraftMessageInputBody{Body: text})
		require.NoError(err)
		require.Equal(http.StatusOK, resp.StatusCode(), string(resp.Body))
	}
	send(prop.JSON200.Id, "why?")
	require.Eventually(func() bool { return len(chatCalls(t, fake)) == 1 }, 15*time.Second, 50*time.Millisecond)

	userID := "local-user-1"
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num,
		generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{{Id: &userID, Path: "a.go", Side: "RIGHT", Line: 21, CommitSha: "", Body: "mine"}}})
	require.NoError(err)
	send(userID, "is this right?")
	require.Eventually(func() bool { return len(chatCalls(t, fake)) == 2 }, 15*time.Second, 50*time.Millisecond)

	calls := chatCalls(t, fake)
	assert.Contains(calls[0], "--resume sess-root --fork-session")
	assert.NotContains(calls[1], "--resume")
	cwds, err := os.ReadFile(fake + ".cwd")
	require.NoError(err)
	lines := strings.Split(strings.TrimSpace(string(cwds)), "\n")
	assert.Contains(lines[0], "run-"+itoa(start.JSON200.Id))
	realDir, _ := filepath.EvalSymlinks(dir)
	assert.Contains([]string{dir, realDir}, lines[1])
}

func TestAPILocalPublishCreatesThreads(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()
	client, _, num, _, _ := setupLocalReviewServer(t, okStream)
	ids := []string{"pa", "pb"}
	paths := []string{"a.go", "b.md"}
	for i := range ids {
		_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num,
			generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{{Id: &ids[i], Path: paths[i], Side: "RIGHT", Line: 1, CommitSha: "", Body: "note " + ids[i]}}})
		require.NoError(err)
	}
	mode, path := "persist-only", "a.go"
	pub, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "local", "demo", num,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT", Mode: &mode, Path: &path})
	require.NoError(err)
	require.Equal(http.StatusOK, pub.StatusCode(), string(pub.Body))
	assert.Equal(int64(1), pub.JSON200.Published)

	threads, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(ctx, "local", "demo", num, nil)
	require.NoError(err)
	require.Len(*threads.JSON200.Threads, 1)
	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Len(*list.JSON200.Drafts, 1)
	assert.Equal("pb", (*list.JSON200.Drafts)[0].Id)

	pub, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsPublishWithResponse(ctx, "local", "demo", num,
		generated.PublishReviewDraftsInputBody{Event: "COMMENT", Mode: &mode})
	require.NoError(err)
	require.Equal(http.StatusOK, pub.StatusCode(), string(pub.Body))
	threads, err = client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(ctx, "local", "demo", num, nil)
	require.NoError(err)
	assert.Len(*threads.JSON200.Threads, 2)
}

func TestAPIReviewRunsCancelledWhenPRCloses(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	client, database, _, srv := setupReviewRunServerWithSrv(t, "sleep 20\n")
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	_, err = database.WriteDB().ExecContext(ctx, `UPDATE middleman_merge_requests SET state = 'closed'`)
	require.NoError(err)
	srv.CancelReviewRunsForClosedPRs()
	got, err := database.GetAIReviewRun(ctx, start.JSON200.Id)
	require.NoError(err)
	require.Equal("cancelled", got.Status)
}

func TestAPISnapshotRefsPrunedWhenUnused(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	client, _, num, dir, _ := setupLocalReviewServer(t, "sleep 1\n"+okStream)
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "local", "demo", num, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	runID := start.JSON200.Id
	prop, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "local", "demo", num, runID,
		generated.ProposeCommentInputBody{Path: "a.go", Side: "RIGHT", Line: 21, Body: "b", Rationale: "r"})
	require.NoError(err)
	require.Eventually(func() bool {
		r := localLatestRun(t, client, num)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 50*time.Millisecond)
	ref := "refs/middleman/review-snapshots/" + itoa(runID)
	refExists := func() bool {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
		cmd.Dir = dir
		return cmd.Run() == nil
	}
	require.True(refExists(), "live suggestion keeps the snapshot")
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdDiscardWithResponse(ctx, "local", "demo", num, prop.JSON200.Id)
	require.NoError(err)
	require.False(refExists(), "snapshot dropped once nothing references it")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
