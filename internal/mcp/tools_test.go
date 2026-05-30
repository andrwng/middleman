package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	require.Equal(t, "/repos/local/demo/pulls/7/review-threads/5/comments", gotPath)
	require.Contains(t, gotBody, `"author":"agent"`)
	require.Contains(t, gotBody, `"body":"ok"`)
	require.Contains(t, out, "agent") // text content echoes the updated thread
}

func TestListThreadsProxiesGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/local/demo/pulls/7/review-threads", r.URL.Path)
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
		require.Equal(t, "/repos/local/demo/pulls/7/review-threads", r.URL.Path)
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
