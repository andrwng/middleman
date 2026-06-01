package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/mcp"
)

// The middleman MCP proxy must target middleman's real API mount (/api/v1),
// not bare /repos. This drives the real mcp server (New+Serve) against the
// real HTTP server and asserts reply_to_thread lands in the DB.
func TestMCPProxyReplyHitsRealAPIPath(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	num := seedReviewWorktree(t, database)
	ctx := context.Background()

	// Seed one thread to reply to.
	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	require.NotNil(createResp.JSON200)
	require.NotNil(createResp.JSON200.Threads)
	created := *createResp.JSON200.Threads
	require.Len(created, 1)
	threadID := created[0].Id

	// Drive the REAL mcp proxy at the server's ROOT url; reply_to_thread
	// must reach the /api/v1 route and persist an agent comment.
	m := mcp.New(mcp.Config{
		ServerName: "middleman", BaseURL: ts.URL,
		ReviewOwner: "local", ReviewName: "demo", ReviewNumber: int(num),
	})
	line := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reply_to_thread","arguments":{"thread_id":%d,"body":"agent reply via the real proxy path"}}}`,
		threadID)
	var out strings.Builder
	require.NoError(m.Serve(ctx, strings.NewReader(line+"\n"), &out))
	require.NotContains(out.String(), `"isError":true`, "tool call should not error (404 would set isError): %s", out.String())

	comments, err := database.ListReviewThreadComments(ctx, threadID)
	require.NoError(err)
	foundAgent := false
	for _, c := range comments {
		if c.Author == "agent" && strings.Contains(c.Body, "real proxy path") {
			foundAgent = true
		}
	}
	require.True(foundAgent, "agent reply must land via the /api/v1 proxy path; comments=%+v", comments)
}
