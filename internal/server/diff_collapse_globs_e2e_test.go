package server

import (
	"context"
	"net/http"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

func TestAPIDiffCollapseGlobs_PRGetDefaultEmpty(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	seedPR(t, database, "acme", "widget", 1)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.Globs)
	require.Equal([]string{}, *resp.JSON200.Globs)
}

func TestAPIDiffCollapseGlobs_PRPutGetRoundTrip(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	seedPR(t, database, "acme", "widget", 1)

	globs := []string{"proto/gen/**", "*.generated.go"}
	putResp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &globs},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, putResp.StatusCode())
	require.NotNil(putResp.JSON200)
	require.NotNil(putResp.JSON200.Globs)
	require.Equal(globs, *putResp.JSON200.Globs)

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Globs)
	require.Equal(globs, *getResp.JSON200.Globs)
}

func TestAPIDiffCollapseGlobs_PRUnknownRepoReturns404(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "nope", "nothing", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())

	globs := []string{"a/**"}
	putResp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "nope", "nothing", 1,
		generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &globs},
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, putResp.StatusCode())
}

// TestAPIDiffCollapseGlobs_PRNumberIgnored confirms globs are stored per
// repo, not per PR: an arbitrary (and non-existent) PR number still
// resolves to the repo's globs as long as owner/name match a known repo.
func TestAPIDiffCollapseGlobs_PRNumberIgnored(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	seedPR(t, database, "acme", "widget", 1)

	globs := []string{"vendor/**"}
	putResp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &globs},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, putResp.StatusCode())

	// PR number 999 never existed, but the owner/name repo does.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 999,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Globs)
	require.Equal(globs, *getResp.JSON200.Globs)
}

func TestAPIDiffCollapseGlobs_LocalWorktreeRoundTrip(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   t.TempDir(),
		Branch: "main",
	})
	require.NoError(err)

	globs := []string{"build/**"}
	putResp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "local", "demo", w.ID,
		generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &globs},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, putResp.StatusCode())
	require.NotNil(putResp.JSON200)
	require.NotNil(putResp.JSON200.Globs)
	require.Equal(globs, *putResp.JSON200.Globs)

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Globs)
	require.Equal(globs, *getResp.JSON200.Globs)
}

// TestAPIDiffCollapseGlobs_WorktreeAndPRShareRepoGlobs confirms a worktree
// and a PR backed by the same repo id see the same saved globs: globs are
// per repo, and the local worktree route and PR route both resolve to the
// same repo row.
func TestAPIDiffCollapseGlobs_WorktreeAndPRShareRepoGlobs(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   t.TempDir(),
		Branch: "main",
	})
	require.NoError(err)

	globs := []string{"dist/**"}
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, globs))

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Globs)
	require.Equal(globs, *getResp.JSON200.Globs)
}

func TestAPIDiffCollapseGlobs_PutValidation(t *testing.T) {
	assert := Assert.New(t)
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	seedPR(t, database, "acme", "widget", 1)

	putGlobs := func(globs []string) int {
		resp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
			ctx, "acme", "widget", 1,
			generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &globs},
		)
		require.NoError(err)
		return resp.StatusCode()
	}

	assert.Equal(http.StatusBadRequest, putGlobs([]string{""}))
	assert.Equal(http.StatusBadRequest, putGlobs([]string{"   "}))

	tooLong := make([]byte, 513)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	assert.Equal(http.StatusBadRequest, putGlobs([]string{string(tooLong)}))

	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = "x" + string(rune('a'+i%26))
	}
	assert.Equal(http.StatusBadRequest, putGlobs(tooMany))

	// Exactly 512 chars and exactly 200 entries are allowed.
	okLong := make([]byte, 512)
	for i := range okLong {
		okLong[i] = 'a'
	}
	assert.Equal(http.StatusOK, putGlobs([]string{string(okLong)}))

	okMany := make([]string, 200)
	for i := range okMany {
		okMany[i] = "glob-" + string(rune('a'+i%26)) + string(rune('0'+i%10))
	}
	assert.Equal(http.StatusOK, putGlobs(okMany))
}

func TestAPIDiffCollapseGlobs_PutTrimsAndDedupes(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	seedPR(t, database, "acme", "widget", 1)

	inGlobs := []string{" proto/gen/** ", "proto/gen/**", "vendor/**", "  vendor/**  "}
	putResp, err := client.HTTP.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberDiffCollapseGlobsJSONRequestBody{Globs: &inGlobs},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, putResp.StatusCode())
	require.NotNil(putResp.JSON200)
	require.NotNil(putResp.JSON200.Globs)
	require.Equal([]string{"proto/gen/**", "vendor/**"}, *putResp.JSON200.Globs)

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberDiffCollapseGlobsWithResponse(
		ctx, "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.Globs)
	require.Equal([]string{"proto/gen/**", "vendor/**"}, *getResp.JSON200.Globs)
}
