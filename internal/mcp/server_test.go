package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runLine feeds one JSON-RPC request line through the server and returns
// the single response line (or "" for notifications).
func runLine(t *testing.T, s *Server, line string) string {
	t.Helper()
	var out strings.Builder
	require.NoError(t, s.handleLine(context.Background(), []byte(line), &out))
	return strings.TrimSpace(out.String())
}

func TestInitializeEchoesProtocolVersionAndAdvertisesTools(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &got))
	result := got["result"].(map[string]any)
	require.Equal(t, "2025-06-18", result["protocolVersion"])
	caps := result["capabilities"].(map[string]any)
	_, hasTools := caps["tools"]
	require.True(t, hasTools)
}

func TestInitializedNotificationProducesNoResponse(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	require.Empty(t, resp)
}

func TestToolsListReturnsTheThreeTools(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var got struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp), &got))
	names := map[string]bool{}
	for _, tl := range got.Result.Tools {
		names[tl.Name] = true
	}
	require.True(t, names["list_threads"])
	require.True(t, names["get_thread"])
	require.True(t, names["reply_to_thread"])
}

func TestUnknownMethodReturnsError(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","id":3,"method":"bogus"}`)
	require.Contains(t, resp, `"error"`)
}

func TestParseErrorReturnsRPCError(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","id":1,"method":`)
	require.Contains(t, resp, `"error"`)
	require.Contains(t, resp, "-32700")
}

func TestUnknownNotificationProducesNoResponse(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	resp := runLine(t, s, `{"jsonrpc":"2.0","method":"not-a-real-notification"}`)
	require.Empty(t, resp)
}

// Serve reads newline-delimited requests from r and writes responses to w.
func TestServeProcessesMultipleLines(t *testing.T) {
	s := New(Config{ServerName: "middleman"})
	in := bufio.NewReader(strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"v","capabilities":{}}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	var out strings.Builder
	require.NoError(t, s.Serve(context.Background(), in, &out))
	require.Contains(t, out.String(), `"protocolVersion":"v"`)
}
