package github

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mixedPRResponse is what GitHub returns to a fine-grained PAT, which
// can't hold the Checks permission: the PR and its readable status
// contexts come back, each CheckRun node it can't read is null, and the
// errors array carries one FORBIDDEN entry per null node.
func mixedPRResponse(now string) string {
	return `{"data":{"repository":{"pullRequests":{"nodes":[{
		"databaseId":7001,"number":71,"title":"Partial CI PR","state":"OPEN","isDraft":false,
		"body":"body text","url":"https://github.com/acme/widget/pull/71","author":{"login":"pat"},
		"createdAt":"` + now + `","updatedAt":"` + now + `","mergedAt":null,"closedAt":null,
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
			null,
			{"__typename":"CheckRun","name":"readable","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://example/run","checkSuite":{"app":{"name":"Actions"}}}
		],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}]}
	}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}},
	"errors":[
		{"type":"FORBIDDEN","path":["repository","pullRequests","nodes",0,"lastCommit","nodes",0,"commit","statusCheckRollup","contexts","nodes",1],"message":"Resource not accessible by personal access token"},
		{"type":"FORBIDDEN","path":["repository","pullRequests","nodes",0,"lastCommit","nodes",0,"commit","statusCheckRollup","contexts","nodes",3],"message":"Resource not accessible by personal access token"}
	]}`
}

func gqlTestServer(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestClassifyGraphQLErrors(t *testing.T) {
	ctxPath := []any{"repository", "pullRequests", "nodes", float64(0), "lastCommit", "nodes", float64(0), "commit", "statusCheckRollup", "contexts", "nodes", float64(2)}
	cases := []struct {
		name string
		errs []gqlResponseError
		want bool
	}{
		{"no errors", nil, false},
		{"forbidden check contexts", []gqlResponseError{{Type: "FORBIDDEN", Path: ctxPath}, {Type: "FORBIDDEN", Path: ctxPath}}, true},
		{"forbidden elsewhere", []gqlResponseError{{Type: "FORBIDDEN", Path: []any{"repository", "pullRequests", "nodes", float64(0), "author"}}}, false},
		{"other error type on contexts", []gqlResponseError{{Type: "NOT_FOUND", Path: ctxPath}}, false},
		{"mixed with an unrelated error", []gqlResponseError{{Type: "FORBIDDEN", Path: ctxPath}, {Type: "MAX_NODE_LIMIT_EXCEEDED"}}, false},
		{"contexts connection itself, not a node", []gqlResponseError{{Type: "FORBIDDEN", Path: ctxPath[:len(ctxPath)-2]}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			Assert.Equal(t, tc.want, onlyForbiddenCheckContexts(tc.errs))
		})
	}
}

func TestFetchRepoPRsAcceptsForbiddenCheckRuns(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	srv, calls := gqlTestServer(t, mixedPRResponse(now))
	f := NewGraphQLFetcherForEndpoint(srv.URL, srv.Client(), nil)

	res, err := f.FetchRepoPRs(context.Background(), "acme", "widget", time.Time{})
	require.NoError(err)
	assert.Equal(int32(1), calls.Load(), "no smaller-page retry for a partial response")
	require.Len(res.PullRequests, 1)
	pr := res.PullRequests[0]
	assert.Equal("Partial CI PR", pr.PR.GetTitle())
	assert.Equal("abc123", pr.PR.GetHead().GetSHA())
	assert.Equal(3, pr.PR.GetAdditions())
	require.Len(pr.Statuses, 2)
	assert.Equal("ci/lint", pr.Statuses[0].GetContext())
	assert.Equal("failure", pr.Statuses[1].GetState())
	require.Len(pr.CheckRuns, 1)
	assert.Equal("readable", pr.CheckRuns[0].GetName())
	assert.True(pr.CIComplete)
}

func TestFetchRepoPRsOtherErrorsStillFail(t *testing.T) {
	body := `{"data":null,"errors":[{"type":"MAX_NODE_LIMIT_EXCEEDED","message":"too many nodes"}]}`
	srv, calls := gqlTestServer(t, body)
	f := NewGraphQLFetcherForEndpoint(srv.URL, srv.Client(), nil)
	_, err := f.FetchRepoPRs(context.Background(), "acme", "widget", time.Time{})
	require.Error(t, err)
	Assert.Equal(t, int32(2), calls.Load(), "still retries with a smaller page")
}

type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestFetchRepoPRsForbiddenNoticeOncePerRepo(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	srv, _ := gqlTestServer(t, mixedPRResponse(now))
	f := NewGraphQLFetcherForEndpoint(srv.URL, srv.Client(), nil)
	for range 3 {
		_, err := f.FetchRepoPRs(context.Background(), "acme", "widget", time.Time{})
		require.NoError(err)
	}
	_, err := f.FetchRepoPRs(context.Background(), "acme", "other", time.Time{})
	require.NoError(err)

	notices := map[string]int{}
	for _, r := range h.records {
		assert.NotEqual(slog.LevelWarn, r.Level, "unexpected WARN: %s", r.Message)
		if strings.Contains(r.Message, "check runs") {
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "repo" {
					notices[a.Value.String()]++
				}
				return true
			})
		}
	}
	assert.Equal(map[string]int{"acme/widget": 1, "acme/other": 1}, notices)
}
