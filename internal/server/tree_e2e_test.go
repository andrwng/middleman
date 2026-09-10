package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/apiclient/generated"
)

func TestAPITree_RootAndSubdir(t *testing.T) {
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	root, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &mergeBase, Path: nil})
	require.NoError(t, err)
	assert := assert.New(t)
	assert.Equal(http.StatusOK, root.StatusCode())
	require.NotNil(t, root.JSON200)
	assert.NotEmpty(root.JSON200.Entries)
}

func TestAPITree_NonexistentPath(t *testing.T) {
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badPath := "does/not/exist"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &mergeBase, Path: &badPath})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

func TestAPITree_NonexistentSHA(t *testing.T) {
	client, _, _, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badSHA := "0000000000000000000000000000000000000000"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &badSHA, Path: nil})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}
