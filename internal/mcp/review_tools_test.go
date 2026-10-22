package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	method, path string
	body         []byte
}

// reviewToolServer serves canned responses and records each request.
func reviewToolServer(t *testing.T, status int, respond string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	reqs := &[]recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*reqs = append(*reqs, recordedRequest{method: r.Method, path: r.URL.Path, body: b})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respond))
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

func runModeServer(baseURL string) *Server {
	return New(Config{ServerName: "middleman", BaseURL: baseURL, ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, RunID: 7})
}

func TestReviewRunModeListsOnlyReviewTools(t *testing.T) {
	s := runModeServer("http://x")
	names := []string{}
	for _, td := range s.toolList() {
		names = append(names, td["name"].(string))
	}
	sort.Strings(names)
	require.Equal(t, []string{"get_pull", "list_drafts", "propose_comment"}, names)
}

func TestProposeCommentPostsToRunProposals(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, reqs := reviewToolServer(t, http.StatusOK, `{"id":"abc","state":"suggestion"}`)
	s := runModeServer(srv.URL)
	out, err := s.tools["propose_comment"].call(s, map[string]any{
		"path": "a.go", "side": "RIGHT", "line": float64(12), "start_line": float64(10),
		"body": "Handle the error.", "rationale": "err is dropped",
	})
	require.NoError(err)
	assert.Contains(out, "suggestion")
	require.Len(*reqs, 1)
	req := (*reqs)[0]
	assert.Equal("POST", req.method)
	assert.Equal("/api/v1/repos/acme/widgets/pulls/1/ai-review-runs/7/proposals", req.path)
	var body map[string]any
	require.NoError(json.Unmarshal(req.body, &body))
	assert.Equal("a.go", body["path"])
	assert.EqualValues(12, body["line"])
	assert.EqualValues(10, body["start_line"])
	assert.Equal("err is dropped", body["rationale"])
}

func TestProposeCommentSurfacesAnchorRejection(t *testing.T) {
	srv, _ := reviewToolServer(t, http.StatusBadRequest, `{"detail":"line 99 is not in the diff; valid RIGHT lines for a.go: 10-14"}`)
	s := runModeServer(srv.URL)
	_, err := s.tools["propose_comment"].call(s, map[string]any{"path": "a.go", "side": "RIGHT", "line": float64(99), "body": "x", "rationale": "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid RIGHT lines")
}

func TestProposeCommentRequiresFields(t *testing.T) {
	srv, reqs := reviewToolServer(t, http.StatusOK, `{}`)
	s := runModeServer(srv.URL)
	cases := []map[string]any{
		{"side": "RIGHT", "line": float64(1), "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "UP", "line": float64(1), "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "RIGHT", "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "RIGHT", "line": float64(1.5), "body": "b", "rationale": "r"},
		{"path": "a.go", "side": "RIGHT", "line": float64(1), "rationale": "r"},
	}
	for i, args := range cases {
		_, err := s.tools["propose_comment"].call(s, args)
		require.Errorf(t, err, "case %d", i)
	}
	require.Empty(t, *reqs)
}

func TestListDraftsGetsReviewDrafts(t *testing.T) {
	srv, reqs := reviewToolServer(t, http.StatusOK, `{"drafts":[],"meta":{"body":"","event":""}}`)
	s := runModeServer(srv.URL)
	_, err := s.tools["list_drafts"].call(s, map[string]any{})
	require.NoError(t, err)
	require.Len(t, *reqs, 1)
	assert.Equal(t, "/api/v1/repos/acme/widgets/pulls/1/review-drafts", (*reqs)[0].path)
}

func draftModeServer(baseURL string) *Server {
	return New(Config{ServerName: "middleman", BaseURL: baseURL, ReviewOwner: "acme", ReviewName: "widgets", ReviewNumber: 1, DraftID: "d 1"})
}

func TestDraftModeListsOnlyDraftTools(t *testing.T) {
	s := draftModeServer("http://x")
	names := []string{}
	for _, td := range s.toolList() {
		names = append(names, td["name"].(string))
	}
	sort.Strings(names)
	require.Equal(t, []string{"get_draft", "get_pull", "revise_comment"}, names)
}

func TestReviseCommentPostsRevision(t *testing.T) {
	require := require.New(t)
	srv, reqs := reviewToolServer(t, http.StatusOK, `{"id":3,"kind":"revision","applied":true}`)
	s := draftModeServer(srv.URL)
	out, err := s.tools["revise_comment"].call(s, map[string]any{"body": "Return the error."})
	require.NoError(err)
	require.Contains(out, "revision")
	require.Len(*reqs, 1)
	require.Equal("POST", (*reqs)[0].method)
	require.Equal("/api/v1/repos/acme/widgets/pulls/1/review-drafts/d 1/revisions", (*reqs)[0].path)
	var body map[string]any
	require.NoError(json.Unmarshal((*reqs)[0].body, &body))
	require.Equal("Return the error.", body["body"])
}

func TestReviseCommentRejectsEmptyAndSurfacesConflict(t *testing.T) {
	srv, reqs := reviewToolServer(t, http.StatusConflict, `{"detail":"draft is not in a state that allows this"}`)
	s := draftModeServer(srv.URL)
	_, err := s.tools["revise_comment"].call(s, map[string]any{"body": "  "})
	require.Error(t, err)
	require.Empty(t, *reqs)
	_, err = s.tools["revise_comment"].call(s, map[string]any{"body": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a state")
}

func TestGetDraftFetchesOneDraft(t *testing.T) {
	srv, reqs := reviewToolServer(t, http.StatusOK, `{"id":"d 1","state":"suggestion"}`)
	s := draftModeServer(srv.URL)
	_, err := s.tools["get_draft"].call(s, map[string]any{})
	require.NoError(t, err)
	require.Len(t, *reqs, 1)
	assert.Equal(t, "/api/v1/repos/acme/widgets/pulls/1/review-drafts/d 1", (*reqs)[0].path)
}
