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

func TestAPILocalResolveHitAnd404(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	dir := t.TempDir()
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: dir, Branch: "feat/x", HeadSHA: "deadbeef",
	})
	require.NoError(err)

	missPath := "/code/nope"
	hit, err := client.HTTP.GetLocalResolveWithResponse(ctx, &generated.GetLocalResolveParams{Path: &dir})
	require.NoError(err)
	require.Equal(http.StatusOK, hit.StatusCode())
	require.NotNil(hit.JSON200)
	assert.Equal("local", hit.JSON200.Owner)
	assert.Equal("demo", hit.JSON200.Name)
	assert.Equal(w.ID, hit.JSON200.Number)
	// Non-git tempdir -> CurrentBranch errors -> falls back to scanned branch.
	assert.Equal("feat/x", hit.JSON200.Branch)

	miss, err := client.HTTP.GetLocalResolveWithResponse(ctx, &generated.GetLocalResolveParams{Path: &missPath})
	require.NoError(err)
	assert.Equal(http.StatusNotFound, miss.StatusCode())
}
