package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HTTPDoer interface{ Do(*http.Request) (*http.Response, error) }

func defaultHTTPDoer() HTTPDoer { return &http.Client{Timeout: 30 * time.Second} }

type toolDef struct {
	name        string
	description string
	inputSchema map[string]any
	call        func(s *Server, args map[string]any) (string, error)
}

func builtinTools() map[string]toolDef {
	intSchema := map[string]any{"type": "integer"}
	strSchema := map[string]any{"type": "string"}
	return map[string]toolDef{
		"list_threads": {
			name:        "list_threads",
			description: "List the review threads for the current review (path, line, side, status, comments).",
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			call: func(s *Server, _ map[string]any) (string, error) {
				return s.restJSON("GET", s.reviewPath("/review-threads"), nil)
			},
		},
		"get_thread": {
			name:        "get_thread",
			description: "Get a single review thread (with its comments) by id.",
			inputSchema: map[string]any{
				"type": "object", "required": []string{"thread_id"},
				"properties": map[string]any{"thread_id": intSchema},
			},
			call: func(s *Server, args map[string]any) (string, error) {
				id, err := intArg(args, "thread_id")
				if err != nil {
					return "", err
				}
				// No single-thread GET endpoint; filter from the list.
				all, err := s.restJSON("GET", s.reviewPath("/review-threads"), nil)
				if err != nil {
					return "", err
				}
				return filterThread(all, id)
			},
		},
		"reply_to_thread": {
			name:        "reply_to_thread",
			description: "Post a reply comment (authored by the agent) to a review thread.",
			inputSchema: map[string]any{
				"type": "object", "required": []string{"thread_id", "body"},
				"properties": map[string]any{"thread_id": intSchema, "body": strSchema},
			},
			call: func(s *Server, args map[string]any) (string, error) {
				id, err := intArg(args, "thread_id")
				if err != nil {
					return "", err
				}
				body, _ := args["body"].(string)
				if body == "" {
					return "", fmt.Errorf("body is required")
				}
				payload, _ := json.Marshal(map[string]any{"body": body, "author": "agent"})
				return s.restJSON("POST", s.reviewPath(fmt.Sprintf("/review-threads/%d/comments", id)), payload)
			},
		},
		"get_pull": {
			name:        "get_pull",
			description: "Get the pull/review detail (title, head/base branch + SHAs) so you can diff the exact range under review yourself.",
			inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			call: func(s *Server, _ map[string]any) (string, error) {
				return s.restJSON("GET", s.reviewPath(""), nil)
			},
		},
	}
}

func (s *Server) reviewPath(suffix string) string {
	// middleman mounts its REST API under /api/v1; --base-url is the server root.
	return fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d%s", s.cfg.ReviewOwner, s.cfg.ReviewName, s.cfg.ReviewNumber, suffix)
}

func (s *Server) restJSON(method, path string, body []byte) (string, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.cfg.BaseURL+path, rdr)
	if err != nil {
		return "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.cfg.httpDoer.Do(req)
	if err != nil {
		return "", fmt.Errorf("rest %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("rest %s %s: status %d: %s", method, path, resp.StatusCode, string(b))
	}
	return string(b), nil
}

func filterThread(listJSON string, id int64) (string, error) {
	var parsed struct {
		Threads []json.RawMessage `json:"threads"`
	}
	if err := json.Unmarshal([]byte(listJSON), &parsed); err != nil {
		return "", err
	}
	for _, raw := range parsed.Threads {
		var probe struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.ID == id {
			return string(raw), nil
		}
	}
	return "", fmt.Errorf("thread %d not found", id)
}

func intArg(args map[string]any, key string) (int64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch n := v.(type) {
	case float64: // JSON numbers decode to float64
		return int64(n), nil
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, map[string]any{
			"name": t.name, "description": t.description, "inputSchema": t.inputSchema,
		})
	}
	return out
}

func (s *Server) handleToolCall(ctx context.Context, w io.Writer, req rpcRequest) error {
	_ = ctx
	if s.cfg.Unresolved != "" {
		return s.writeResult(w, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": s.cfg.Unresolved}},
			"isError": true,
		})
	}
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.writeError(w, req.ID, -32602, "invalid params")
	}
	t, ok := s.tools[p.Name]
	if !ok {
		return s.writeError(w, req.ID, -32602, "unknown tool: "+p.Name)
	}
	text, err := t.call(s, p.Arguments)
	if err != nil {
		// MCP convention: tool errors are a result with isError=true, not a
		// protocol error, so the model can read + react to them.
		return s.writeResult(w, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}
	return s.writeResult(w, req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}
