package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
)

func TestAPICodeBrowserState_CRUD(t *testing.T) {
	client, _, _, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()
	as := assert.New(t)

	empty, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(ctx, "acme", "widget", 1)
	require.NoError(t, err)
	as.Equal(http.StatusOK, empty.StatusCode())
	require.NotNil(t, empty.JSON200)
	as.Equal("", empty.JSON200.Path)

	put, err := client.HTTP.PutReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberCodeBrowserStateJSONRequestBody{Path: "src/kafka/server.cc"})
	require.NoError(t, err)
	as.Equal(http.StatusOK, put.StatusCode())
	require.NotNil(t, put.JSON200)
	as.Equal("src/kafka/server.cc", put.JSON200.Path)

	got, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(ctx, "acme", "widget", 1)
	require.NoError(t, err)
	require.NotNil(t, got.JSON200)
	as.Equal("src/kafka/server.cc", got.JSON200.Path)

	// A second PUT overwrites the previously stored path rather than
	// erroring or appending.
	putAgain, err := client.HTTP.PutReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberCodeBrowserStateJSONRequestBody{Path: "src/kafka/other.cc"})
	require.NoError(t, err)
	as.Equal(http.StatusOK, putAgain.StatusCode())
	require.NotNil(t, putAgain.JSON200)
	as.Equal("src/kafka/other.cc", putAgain.JSON200.Path)

	gotAgain, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(ctx, "acme", "widget", 1)
	require.NoError(t, err)
	require.NotNil(t, gotAgain.JSON200)
	as.Equal("src/kafka/other.cc", gotAgain.JSON200.Path)
}
