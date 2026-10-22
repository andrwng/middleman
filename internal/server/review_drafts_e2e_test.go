package server

import (
	"context"
	"net/http"
	"os/exec"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient/generated"
)

func draftIn(id, path string, line int64, body string) generated.ReviewDraftInput {
	return generated.ReviewDraftInput{Id: &id, Path: path, Side: "RIGHT", Line: line, CommitSha: "abc", Body: body}
}

func TestAPIReviewDraftsCRUD(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	created, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1,
		generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{
			draftIn("d-1", "a.go", 3, "first"),
			draftIn("d-2", "b.go", 9, "second"),
		}})
	require.NoError(err)
	require.Equal(http.StatusOK, created.StatusCode())
	require.Len((*created.JSON200.Drafts), 2)
	assert.Equal("user", (*created.JSON200.Drafts)[0].Origin)
	assert.Equal("draft", (*created.JSON200.Drafts)[0].State)

	patched, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widget", 1, "d-2",
		generated.UpdateReviewDraftInputBody{Body: "second edited"})
	require.NoError(err)
	require.Equal(http.StatusOK, patched.StatusCode())
	assert.Equal("second edited", patched.JSON200.Body)

	meta, err := client.HTTP.PutReposByOwnerByNamePullsByNumberReviewDraftMetaWithResponse(ctx, "acme", "widget", 1,
		generated.PutReviewDraftMetaInputBody{Body: "LGTM", Event: "APPROVE"})
	require.NoError(err)
	require.Equal(http.StatusOK, meta.StatusCode())

	del, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsDeleteWithResponse(ctx, "acme", "widget", 1,
		generated.DeleteReviewDraftsInputBody{Ids: &[]string{"d-1"}})
	require.NoError(err)
	require.Equal(http.StatusOK, del.StatusCode())
	assert.Equal(int64(1), del.JSON200.Deleted)

	list, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1)
	require.NoError(err)
	require.Equal(http.StatusOK, list.StatusCode())
	require.Len((*list.JSON200.Drafts), 1)
	assert.Equal("d-2", (*list.JSON200.Drafts)[0].Id)
	assert.Equal("LGTM", list.JSON200.Meta.Body)
	assert.Equal("APPROVE", list.JSON200.Meta.Event)

	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	require.Equal(http.StatusOK, summary.StatusCode())
	require.Len((*summary.JSON200.Prs), 1)
	assert.Equal(int64(1), (*summary.JSON200.Prs)[0].Count)
}

func TestAPIReviewDraftsValidation(t *testing.T) {
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	seedPR(t, database, "acme", "widget", 1)

	bad := func(mut func(*generated.ReviewDraftInput)) generated.ReviewDraftInput {
		d := draftIn("ok-id", "a.go", 1, "body")
		mut(&d)
		return d
	}
	cases := []struct {
		name  string
		draft generated.ReviewDraftInput
	}{
		{"bad side", bad(func(d *generated.ReviewDraftInput) { d.Side = "MIDDLE" })},
		{"zero line", bad(func(d *generated.ReviewDraftInput) { d.Line = 0 })},
		{"blank body", bad(func(d *generated.ReviewDraftInput) { d.Body = "   " })},
		{"empty path", bad(func(d *generated.ReviewDraftInput) { d.Path = "" })},
		{"bad id", bad(func(d *generated.ReviewDraftInput) { id := "../x"; d.Id = &id })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 1,
				generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{tc.draft}})
			require.NoError(t, err)
			Assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}
}

func TestAPIReviewDraftsUnknownPRAndDraft(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "acme", "widget", 404)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())

	seedPR(t, database, "acme", "widget", 1)
	patched, err := client.HTTP.PatchReposByOwnerByNamePullsByNumberReviewDraftsByDraftIdWithResponse(ctx, "acme", "widget", 1, "missing",
		generated.UpdateReviewDraftInputBody{Body: "x"})
	require.NoError(err)
	require.Equal(http.StatusNotFound, patched.StatusCode())
}

func TestAPIReviewDraftsLocalBranchScoped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)

	_, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num,
		generated.CreateReviewDraftsInputBody{Drafts: &[]generated.ReviewDraftInput{draftIn("on-a", "a.go", 1, "on a")}})
	require.NoError(err)

	runGit(t, dir, "checkout", "-b", "feat/b")
	onB, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, onB.StatusCode())
	assert.Empty((*onB.JSON200.Drafts))

	runGit(t, dir, "checkout", "feat/a")
	onA, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewDraftsWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Len((*onA.JSON200.Drafts), 1)
	assert.Equal("on-a", (*onA.JSON200.Drafts)[0].Id)

	// Local drafts never show up in the GitHub sidebar summary.
	summary, err := client.HTTP.GetReviewDraftsSummaryWithResponse(ctx)
	require.NoError(err)
	assert.Empty((*summary.JSON200.Prs))
}
