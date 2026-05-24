package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncRepoTriggers202(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.SyncRepoWithResponse(
		context.Background(), "acme", "widget",
	)
	require.NoError(err)
	assert.Equal(http.StatusAccepted, resp.StatusCode())
}

func TestSyncRepoIs403ForUntrackedRepo(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.SyncRepoWithResponse(
		context.Background(), "other", "repo",
	)
	require.NoError(err)
	assert.Equal(http.StatusForbidden, resp.StatusCode())
	assert.Contains(string(resp.Body), "not tracked")
}
