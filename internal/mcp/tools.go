// tools.go defines the MCP tool registry for Task 1.
// This file is replaced wholesale in Task 2 with real REST-proxy implementations.
// The three tool stubs here exist only so Task 1's protocol tests compile and pass.
package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// HTTPDoer is the interface satisfied by *http.Client (and test doubles).
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func defaultHTTPDoer() HTTPDoer { return http.DefaultClient }

// toolDef describes a single MCP tool.
type toolDef struct {
	name        string
	description string
	inputSchema map[string]any
	// call runs the tool and returns the text content.
	call func(s *Server, args map[string]any) (string, error)
}

// builtinTools registers the three review-thread tools.
// All call functions return "not implemented" in Task 1; Task 2 replaces them.
func builtinTools() map[string]toolDef {
	placeholder := func(s *Server, args map[string]any) (string, error) {
		return "", fmt.Errorf("not implemented in task 1")
	}
	tools := []toolDef{
		{
			name:        "list_threads",
			description: "List all review threads for the current pull request.",
			inputSchema: map[string]any{"type": "object"},
			call:        placeholder,
		},
		{
			name:        "get_thread",
			description: "Get the full content of a specific review thread by ID.",
			inputSchema: map[string]any{"type": "object"},
			call:        placeholder,
		},
		{
			name:        "reply_to_thread",
			description: "Post a reply to a review thread.",
			inputSchema: map[string]any{"type": "object"},
			call:        placeholder,
		},
	}
	m := make(map[string]toolDef, len(tools))
	for _, td := range tools {
		m[td.name] = td
	}
	return m
}

// toolList returns the list of tool descriptors for the tools/list response.
func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, td := range s.tools {
		out = append(out, map[string]any{
			"name":        td.name,
			"description": td.description,
			"inputSchema": td.inputSchema,
		})
	}
	return out
}

// handleToolCall dispatches a tools/call request.
// In Task 1 this always returns a tool-level error; Task 2 replaces this body.
func (s *Server) handleToolCall(ctx context.Context, w io.Writer, req rpcRequest) error {
	_ = ctx
	return s.writeResult(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "not implemented in task 1"},
		},
		"isError": true,
	})
}
