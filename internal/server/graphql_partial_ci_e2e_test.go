package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/google/go-github/v84/github"
	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ghclient "github.com/wesm/middleman/internal/github"
)

// A fine-grained PAT can't hold the Checks permission, so GitHub's
// GraphQL response nulls every CheckRun node in statusCheckRollup and
// adds a FORBIDDEN error per node. The sync must keep the PR and its
// readable statuses from that response instead of failing over to REST.
func TestE2EGraphQLSyncKeepsPRWhenCheckRunsForbidden(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	nowRFC := now.Format(time.RFC3339)

	var prQueries int
	gqlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if !bytes.Contains(body, []byte("pullRequests")) {
			_, _ = w.Write([]byte(`{"data":{"repository":{"issues":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`))
			return
		}
		prQueries++
		_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[{
			"databaseId":7001,"number":71,"title":"Partial CI PR","state":"OPEN","isDraft":false,
			"body":"from graphql","url":"https://github.com/acme/widget/pull/71","author":{"login":"pat"},
			"createdAt":"` + nowRFC + `","updatedAt":"` + nowRFC + `","mergedAt":null,"closedAt":null,
			"additions":3,"deletions":1,"mergeable":"MERGEABLE","reviewDecision":"",
			"headRefName":"feat","baseRefName":"main","headRefOid":"abc123","baseRefOid":"def456",
			"headRepository":{"url":"https://github.com/acme/widget"},
			"labels":{"nodes":[]},
			"comments":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
			"reviews":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
			"allCommits":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
			"lastCommit":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
				{"__typename":"StatusContext","context":"ci/lint","state":"SUCCESS","targetUrl":"https://ci.example/lint"},
				null,
				{"__typename":"StatusContext","context":"ci/test","state":"FAILURE","targetUrl":"https://ci.example/test"},
				null
			],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}]}
		}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}},
		"errors":[
			{"type":"FORBIDDEN","path":["repository","pullRequests","nodes",0,"lastCommit","nodes",0,"commit","statusCheckRollup","contexts","nodes",1],"message":"Resource not accessible by personal access token"},
			{"type":"FORBIDDEN","path":["repository","pullRequests","nodes",0,"lastCommit","nodes",0,"commit","statusCheckRollup","contexts","nodes",3],"message":"Resource not accessible by personal access token"}
		]}`))
	}))
	defer gqlSrv.Close()

	// REST: the PR list carries the PR (so the GraphQL PR path runs);
	// REST CI endpoints must not be needed.
	prID, prNumber := int64(7001), 71
	title, state, url, login := "Partial CI PR (rest)", "open", "https://github.com/acme/widget/pull/71", "pat"
	headSHA, headRef := "abc123", "feat"
	ts := gh.Timestamp{Time: now}
	mock := &mockGH{
		listOpenPullRequestsFn: func(_ context.Context, _, _ string) ([]*gh.PullRequest, error) {
			return []*gh.PullRequest{{
				ID: &prID, Number: &prNumber, Title: &title, State: &state, HTMLURL: &url,
				User: &gh.User{Login: &login}, CreatedAt: &ts, UpdatedAt: &ts,
				Head: &gh.PullRequestBranch{SHA: &headSHA, Ref: &headRef},
			}}, nil
		},
	}
	srv, _ := setupTestServerWithMock(t, mock)
	srv.syncer.SetFetchers(map[string]*ghclient.GraphQLFetcher{
		"github.com": ghclient.NewGraphQLFetcherForEndpoint(gqlSrv.URL, gqlSrv.Client(), nil),
	})

	srv.syncer.RunOnce(ctx)
	srv.syncer.RunOnce(ctx) // a second cycle behaves the same

	client := setupTestClient(t, srv)
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(ctx, "acme", "widget", 71)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	mr := resp.JSON200.MergeRequest
	assert.Equal("Partial CI PR", mr.Title) // GraphQL's PR fields won
	assert.Equal("from graphql", mr.Body)
	assert.Equal("failure", mr.CIStatus)
	assert.Contains(mr.CIChecksJSON, "ci/lint")
	assert.Contains(mr.CIChecksJSON, "ci/test")
	assert.Equal(int32(0), mock.restCIFetches.Load(), "no REST fallback for CI")
	assert.GreaterOrEqual(prQueries, 2)
}
