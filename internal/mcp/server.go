// Package mcp implements a minimal stdio MCP (Model Context Protocol)
// server exposing review-thread tools as a thin proxy over middleman's
// loopback REST API. Transport is newline-delimited JSON-RPC 2.0 on
// stdin/stdout, which is what `claude --mcp-config` speaks to a stdio
// server.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Config configures the server. ReviewOwner/Name/Number identify the
// review the tools operate on (the runner passes these); BaseURL is the
// running middleman REST server.
type Config struct {
	ServerName   string
	BaseURL      string
	ReviewOwner  string
	ReviewName   string
	ReviewNumber int
	// Unresolved, when non-empty, means cwd-default resolution failed.
	// Every tools/call returns it as a clear isError result (tools/list
	// still works, so the client sees the tools and learns why calls fail).
	Unresolved string
	// httpDoer is the REST client; defaults to http.DefaultClient in New.
	httpDoer HTTPDoer
}

// Server is the MCP stdio server.
type Server struct {
	cfg   Config
	tools map[string]toolDef
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// New creates a new Server with the given Config.
func New(cfg Config) *Server {
	if cfg.httpDoer == nil {
		cfg.httpDoer = defaultHTTPDoer()
	}
	s := &Server{cfg: cfg}
	s.tools = builtinTools() // defined in tools.go
	return s
}

// Serve reads newline-delimited JSON-RPC requests until EOF.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := s.handleLine(ctx, line, w); err != nil {
			return err
		}
	}
	return sc.Err()
}

// handleLine processes one request line; writes a response line unless the
// message is a notification (no id).
func (s *Server) handleLine(ctx context.Context, line []byte, w io.Writer) error {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return s.writeError(w, nil, -32700, "parse error")
	}
	switch req.Method {
	case "initialize":
		return s.writeResult(w, req.ID, s.initializeResult(req.Params))
	case "notifications/initialized", "notifications/cancelled":
		return nil // notifications: no response
	case "tools/list":
		return s.writeResult(w, req.ID, map[string]any{"tools": s.toolList()})
	case "tools/call":
		return s.handleToolCall(ctx, w, req)
	default:
		if len(req.ID) == 0 {
			return nil // unknown notification: ignore
		}
		return s.writeError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) initializeResult(params json.RawMessage) map[string]any {
	version := "2025-06-18"
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		version = p.ProtocolVersion // echo the client's negotiated version
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": s.cfg.ServerName, "version": "0.1.0"},
	}
}

func (s *Server) writeResult(w io.Writer, id json.RawMessage, result any) error {
	if len(id) == 0 {
		return nil
	}
	return s.writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) writeError(w io.Writer, id json.RawMessage, code int, msg string) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return s.writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: msg}})
}

func (s *Server) writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal rpc: %w", err)
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write rpc: %w", err)
	}
	return nil
}
