package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplyToThreadPostsAgentComment(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":5,"status":"discussed","comments":[{"id":9,"author":"agent","body":"ok"}]}`))
	}))
	defer srv.Close()

	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["reply_to_thread"].call(s, map[string]any{"thread_id": float64(5), "body": "ok"})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/repos/local/demo/pulls/7/review-threads/5/comments", gotPath)
	require.Contains(t, gotBody, `"author":"agent"`)
	require.Contains(t, gotBody, `"body":"ok"`)
	require.Contains(t, out, "agent") // text content echoes the updated thread
}

func TestListThreadsProxiesGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/local/demo/pulls/7/review-threads", r.URL.Path)
		_, _ = w.Write([]byte(`{"threads":[{"id":1,"path":"a.go","line":12,"status":"open"}]}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["list_threads"].call(s, map[string]any{})
	require.NoError(t, err)
	require.Contains(t, out, "a.go")
}

// tools/call end-to-end through the JSON-RPC layer.
func TestToolsCallDispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":5,"status":"discussed","comments":[]}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	var out strings.Builder
	line := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"reply_to_thread","arguments":{"thread_id":5,"body":"ok"}}}`
	require.NoError(t, s.handleLine(context.Background(), []byte(line), &out))
	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.String()), &resp))
	require.False(t, resp.Result.IsError)
	require.NotEmpty(t, resp.Result.Content)
}

func TestGetThreadFiltersFromList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/local/demo/pulls/7/review-threads", r.URL.Path)
		_, _ = w.Write([]byte(`{"threads":[{"id":1,"path":"a.go","line":12},{"id":2,"path":"b.go","line":34}]}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["get_thread"].call(s, map[string]any{"thread_id": float64(2)})
	require.NoError(t, err)
	require.Contains(t, out, "b.go")
	require.NotContains(t, out, "a.go")
}

func TestGetThreadNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"threads":[{"id":1,"path":"a.go"}]}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	_, err := s.tools["get_thread"].call(s, map[string]any{"thread_id": float64(99)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestGetPullProxiesPullEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/local/demo/pulls/7", r.URL.Path)
		_, _ = w.Write([]byte(`{"merge_request":{"number":7,"title":"Worktree: feat","head_branch":"feat"}}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["get_pull"].call(s, map[string]any{})
	require.NoError(t, err)
	require.Contains(t, out, "Worktree: feat")
}

func TestToolListIncludesAllTools(t *testing.T) {
	s := New(Config{ServerName: "middleman", BaseURL: "http://127.0.0.1:0", ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	names := map[string]bool{}
	for _, td := range s.toolList() {
		names[td["name"].(string)] = true
	}
	require.True(t, names["list_threads"])
	require.True(t, names["get_thread"])
	require.True(t, names["reply_to_thread"])
	require.True(t, names["get_pull"])
	require.True(t, names["start_thread"])
}

func TestUnresolvedHandleReturnsClearToolError(t *testing.T) {
	s := New(Config{ServerName: "middleman", Unresolved: "no middleman review for this directory (/x): boom"})
	var buf bytes.Buffer
	req := rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"list_threads","arguments":{}}`),
	}
	require.NoError(t, s.handleToolCall(context.Background(), &buf, req))
	require.Contains(t, buf.String(), "no middleman review for this directory")
	require.Contains(t, buf.String(), `"isError":true`)
}

// A tool that fails (empty body fails before any HTTP call) must come back
// as an MCP isError result, not a JSON-RPC protocol error.
func TestToolsCallReturnsIsErrorOnToolFailure(t *testing.T) {
	s := New(Config{ServerName: "middleman", BaseURL: "http://127.0.0.1:0", ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	var out strings.Builder
	line := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"reply_to_thread","arguments":{"thread_id":5,"body":""}}}`
	require.NoError(t, s.handleLine(context.Background(), []byte(line), &out))
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.String()), &resp))
	require.True(t, resp.Result.IsError)
	require.NotEmpty(t, resp.Result.Content)
}

func TestStartThreadWithExplicitCommitSHA(t *testing.T) {
	var posts []recordedPost
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			posts = append(posts, recordedPost{path: r.URL.Path, body: string(b)})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"threads":[{"id":42,"path":"a.go","line":3,"status":"open","comments":[]}]}`))
			return
		}
		assert.Failf(t, "unexpected request", "method=%s path=%s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["start_thread"].call(s, map[string]any{
		"path": "a.go", "side": "RIGHT", "line": float64(3),
		"body": "consider extracting", "commit_sha": "deadbeef",
	})
	require := require.New(t)
	require.NoError(err)
	require.Len(posts, 1)
	require.Equal("/api/v1/repos/local/demo/pulls/7/review-threads", posts[0].path)
	require.Contains(posts[0].body, `"path":"a.go"`)
	require.Contains(posts[0].body, `"side":"RIGHT"`)
	require.Contains(posts[0].body, `"line":3`)
	require.Contains(posts[0].body, `"commit_sha":"deadbeef"`)
	require.Contains(posts[0].body, `"body":"consider extracting"`)
	require.Contains(out, `"id":42`)
}

func TestStartThreadResolvesHeadWhenCommitOmitted(t *testing.T) {
	// Server is now responsible for HEAD resolution. The MCP should forward
	// commit_sha as "" (empty) when the caller omits it, and NOT call get_pull.
	var sawPullGet bool
	var postBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET":
			sawPullGet = true
			_, _ = w.Write([]byte(`{}`))
		case r.Method == "POST" && r.URL.Path == "/api/v1/repos/local/demo/pulls/7/review-threads":
			b, _ := io.ReadAll(r.Body)
			postBody = string(b)
			_, _ = w.Write([]byte(`{"threads":[{"id":1,"path":"x.go","line":1,"status":"open","comments":[]}]}`))
		default:
			assert.Failf(t, "unexpected request", "method=%s path=%s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	_, err := s.tools["start_thread"].call(s, map[string]any{
		"path": "x.go", "side": "LEFT", "line": float64(1), "body": "nit",
	})
	require := require.New(t)
	require.NoError(err)
	assert := assert.New(t)
	assert.False(sawPullGet, "MCP must NOT call get_pull — server resolves HEAD now")
	assert.Contains(postBody, `"commit_sha":""`)
}

func TestStartThreadValidatesRequiredArgs(t *testing.T) {
	s := New(Config{ServerName: "middleman", BaseURL: "http://example", ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{"side": "RIGHT", "line": float64(1), "body": "x"}, "path"},
		{"missing side", map[string]any{"path": "a", "line": float64(1), "body": "x"}, "side"},
		{"missing line", map[string]any{"path": "a", "side": "RIGHT", "body": "x"}, "line"},
		{"missing body", map[string]any{"path": "a", "side": "RIGHT", "line": float64(1)}, "body"},
		{"bad side", map[string]any{"path": "a", "side": "TOP", "line": float64(1), "body": "x"}, "side"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.tools["start_thread"].call(s, c.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestStartThreadFallsBackWhenResponseHasNoThreads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"threads":[]}`))
	}))
	defer srv.Close()
	s := New(Config{ServerName: "middleman", BaseURL: srv.URL, ReviewOwner: "local", ReviewName: "demo", ReviewNumber: 7})
	out, err := s.tools["start_thread"].call(s, map[string]any{
		"path": "a.go", "side": "RIGHT", "line": float64(1),
		"body": "x", "commit_sha": "deadbeef",
	})
	require.NoError(t, err)
	require.Contains(t, out, `"threads":[]`) // raw envelope returned as fallback
}

type recordedPost struct{ path, body string }
