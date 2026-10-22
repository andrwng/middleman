package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/shurcooL/githubv4"
)

// The githubv4 client decodes a response's partial data and then returns
// its "errors" array as an error, dropping each error's type and path.
// GitHub returns exactly that shape to fine-grained PATs, which can't
// hold the Checks permission: the PRs come back with every CheckRun node
// in statusCheckRollup.contexts nulled out and one FORBIDDEN error per
// node. graphqlErrorTransport re-reads the errors so the fetcher can tell
// that case from a real failure.

// gqlResponseError is one entry of a GraphQL response's "errors" array.
type gqlResponseError struct {
	Type    string `json:"type"`
	Path    []any  `json:"path"`
	Message string `json:"message"`
}

type gqlErrorRecorder struct {
	mu   sync.Mutex
	errs []gqlResponseError
}

func (r *gqlErrorRecorder) add(errs []gqlResponseError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, errs...)
}

func (r *gqlErrorRecorder) errors() []gqlResponseError {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gqlResponseError(nil), r.errs...)
}

type gqlErrorRecorderKey struct{}

// withGQLErrorRecorder returns a context whose GraphQL requests record
// their response errors into the returned recorder.
func withGQLErrorRecorder(ctx context.Context) (context.Context, *gqlErrorRecorder) {
	rec := &gqlErrorRecorder{}
	return context.WithValue(ctx, gqlErrorRecorderKey{}, rec), rec
}

// graphqlErrorTransport copies the "errors" array of successful GraphQL
// responses into the request context's recorder, if any. The body is
// handed on unchanged.
type graphqlErrorTransport struct {
	base http.RoundTripper
}

func (t *graphqlErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	rec, _ := req.Context().Value(gqlErrorRecorderKey{}).(*gqlErrorRecorder)
	if rec == nil {
		return resp, nil
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		return resp, nil // let the client hit and report the read failure
	}
	var out struct {
		Errors []gqlResponseError `json:"errors"`
	}
	if json.Unmarshal(body, &out) == nil && len(out.Errors) > 0 {
		rec.add(out.Errors)
	}
	return resp, nil
}

// onlyForbiddenCheckContexts reports whether errs is non-empty and every
// error is a FORBIDDEN on a single node of a statusCheckRollup's
// contexts: the data is complete apart from those nulled nodes.
func onlyForbiddenCheckContexts(errs []gqlResponseError) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		if e.Type != "FORBIDDEN" || !isCheckContextNodePath(e.Path) {
			return false
		}
	}
	return true
}

// isCheckContextNodePath matches paths ending in
// statusCheckRollup.contexts.nodes[<index>].
func isCheckContextNodePath(path []any) bool {
	n := len(path)
	if n < 4 {
		return false
	}
	if _, ok := path[n-1].(float64); !ok {
		return false
	}
	return path[n-2] == "nodes" && path[n-3] == "contexts" && path[n-4] == "statusCheckRollup"
}

// noteForbiddenCheckRuns logs, once per repo, that this token can't read
// check runs so CI comes from commit statuses only.
func (g *GraphQLFetcher) noteForbiddenCheckRuns(owner, name string) {
	repo := owner + "/" + name
	if _, seen := g.forbiddenChecksNoticed.LoadOrStore(repo, struct{}{}); seen {
		return
	}
	slog.Info("GraphQL: check runs are not readable with this token "+
		"(fine-grained PATs can't hold the Checks permission); using commit statuses only",
		"repo", repo,
	)
}

// NewGraphQLFetcherForEndpoint builds a fetcher for a GraphQL endpoint
// over hc, with the same response-error handling as NewGraphQLFetcher.
// Used by tests that point the fetcher at a mock backend.
func NewGraphQLFetcherForEndpoint(
	endpoint string, hc *http.Client, rateTracker *RateTracker,
) *GraphQLFetcher {
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var rt http.RoundTripper = &graphqlErrorTransport{base: base}
	if rateTracker != nil {
		rt = &graphqlRateTransport{base: rt, rateTracker: rateTracker}
	}
	client := &http.Client{Transport: rt, Timeout: hc.Timeout}
	return &GraphQLFetcher{
		client:      githubv4.NewEnterpriseClient(endpoint, client),
		rateTracker: rateTracker,
	}
}
