package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/testutil"
)

// setupReviewRunServer wires acme/widgets #1 with a real bare clone
// and AI review enabled, and swaps in a fake claude running body.
func setupReviewRunServer(t *testing.T, claudeBody string) (*apiclient.Client, *db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	seedPR(t, database, "acme", "widgets", 1)
	diffRepo, err := testutil.SetupDiffRepo(context.Background(), dir, database)
	require.NoError(t, err)

	argsFile := filepath.Join(dir, "claude-args.txt")
	fake := filepath.Join(dir, "claude.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" + claudeBody
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))
	orig := aireview.SetBinaryForTest(fake)
	t.Cleanup(func() { aireview.SetBinaryForTest(orig) })

	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widgets", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, diffRepo.Manager, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: diffRepo.Manager, WorktreeDir: filepath.Join(dir, "wt")})
	return setupTestClient(t, srv), database, argsFile
}

const okStream = `echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"One finding.","session_id":"sess-root"}'
`

func latestRun(t *testing.T, client *apiclient.Client) *generated.AiReviewRunResponse {
	t.Helper()
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberAiReviewRunsLatestWithResponse(context.Background(), "acme", "widgets", 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	r := resp.JSON200.Run
	if r.Id == 0 { // "run": null decodes to the zero value
		return nil
	}
	return &r
}

func firstAddedLine(t *testing.T, client *apiclient.Client) (string, int64) {
	t.Helper()
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffWithResponse(context.Background(), "acme", "widgets", 1, nil)
	require.NoError(t, err)
	for _, f := range *resp.JSON200.Files {
		for _, h := range *f.Hunks {
			for _, l := range *h.Lines {
				if l.Type == "add" && l.NewNum != nil {
					return f.Path, *l.NewNum
				}
			}
		}
	}
	require.FailNow(t, "no added line in fixture diff")
	return "", 0
}

func TestAPIReviewRunLifecycle(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, argsFile := setupReviewRunServer(t, okStream)
	ctx := context.Background()

	instr := "focus on error handling"
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1,
		generated.StartAIReviewRunInputBody{Instructions: &instr})
	require.NoError(err)
	require.Equal(http.StatusOK, start.StatusCode())

	require.Eventually(func() bool {
		r := latestRun(t, client)
		return r != nil && r.Status == "done"
	}, 15*time.Second, 100*time.Millisecond)
	run := latestRun(t, client)
	assert.Equal("One finding.", run.Summary)
	assert.Equal(instr, run.Instructions)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	assert.Contains(string(args), "mcp__middleman__propose_comment")
	assert.Contains(string(args), instr)

	// Proposals after the run finished are rejected.
	path, line := firstAddedLine(t, client)
	late, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, run.Id,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusConflict, late.StatusCode())
}

func TestAPIReviewRunProposals_AnchorValidation(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()

	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	require.Equal(http.StatusOK, start.StatusCode())
	runID := start.JSON200.Id

	// A second run while this one is active is refused.
	again, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	assert.Equal(http.StatusConflict, again.StatusCode())

	path, line := firstAddedLine(t, client)
	ok, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "Handle this error.", Rationale: "err ignored"})
	require.NoError(err)
	require.Equal(http.StatusOK, ok.StatusCode())
	assert.Equal("suggestion", ok.JSON200.State)
	assert.Equal("claude", ok.JSON200.Origin)
	require.NotNil(ok.JSON200.Rationale)
	assert.Equal("err ignored", *ok.JSON200.Rationale)

	bad, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: 100000, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, bad.StatusCode())
	assert.Contains(string(bad.Body), "valid RIGHT lines")

	unchanged, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, runID,
		generated.ProposeCommentInputBody{Path: "not/in/pr.go", Side: "RIGHT", Line: 1, Body: "x", Rationale: "y"})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, unchanged.StatusCode())
	assert.Contains(string(unchanged.Body), "not changed in this pull request")

	// Suggestions appear in the drafts list but not in the publish count.
	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widgets", 1)
	require.NoError(err)
	require.Len(*list.JSON200.Drafts, 1)
	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	assert.Empty(*summary.JSON200.Prs)

	cancel, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdCancelWithResponse(ctx, "acme", "widgets", 1, runID)
	require.NoError(err)
	require.Equal(http.StatusOK, cancel.StatusCode())
	assert.Equal("cancelled", cancel.JSON200.Status)
	assert.Equal(int64(1), cancel.JSON200.SuggestionCount)
}

func TestAPIReviewRunProposals_RejectsInactiveRun(t *testing.T) {
	require := require.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	runID := start.JSON200.Id
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdCancelWithResponse(ctx, "acme", "widgets", 1, runID)
	require.NoError(err)

	path, line := firstAddedLine(t, client)
	cases := []struct {
		name  string
		runID int64
		want  int
	}{
		{"cancelled run", runID, http.StatusConflict},
		{"unknown run", runID + 1000, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, tc.runID,
				generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "x", Rationale: "y"})
			if Assert.NoError(t, err) {
				Assert.Equal(t, tc.want, resp.StatusCode())
			}
		})
	}
	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widgets", 1)
	require.NoError(err)
	require.Empty(*list.JSON200.Drafts)
}

func TestAPISuggestionTransitions(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	client, _, _ := setupReviewRunServer(t, "sleep 20\n")
	ctx := context.Background()
	start, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(ctx, "acme", "widgets", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(err)
	path, line := firstAddedLine(t, client)
	prop, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdProposalsWithResponse(ctx, "acme", "widgets", 1, start.JSON200.Id,
		generated.ProposeCommentInputBody{Path: path, Side: "RIGHT", Line: line, Body: "b", Rationale: "r"})
	require.NoError(err)
	id := prop.JSON200.Id

	edited, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widgets", 1, id,
		generated.UpdateReviewDraftInputBody{Body: "b edited"})
	require.NoError(err)
	assert.Equal("suggestion", edited.JSON200.State) // edits never change state

	disc, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdDiscardWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("discarded", disc.JSON200.State)
	acceptDiscarded, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal(http.StatusConflict, acceptDiscarded.StatusCode())
	rest, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdRestoreWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("suggestion", rest.JSON200.State)
	acc, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, id)
	require.NoError(err)
	assert.Equal("draft", acc.JSON200.State)

	missing, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdAcceptWithResponse(ctx, "acme", "widgets", 1, "nope")
	require.NoError(err)
	assert.Equal(http.StatusNotFound, missing.StatusCode())
	_, err = client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsByRunIdCancelWithResponse(ctx, "acme", "widgets", 1, start.JSON200.Id)
	require.NoError(err)
}

func TestAPIReviewRunRejectsLocal(t *testing.T) {
	client, _, _ := setupReviewRunServer(t, okStream)
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(context.Background(), "local", "demo", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(t, err)
	Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
}

func TestAPIReviewRunUnavailableWithoutAIReview(t *testing.T) {
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberAiReviewRunsWithResponse(context.Background(), "acme", "widget", 1, generated.StartAIReviewRunInputBody{})
	require.NoError(t, err)
	Assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode())
}
