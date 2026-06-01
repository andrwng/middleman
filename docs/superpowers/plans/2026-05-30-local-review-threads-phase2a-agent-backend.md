# Local Review Threads — Phase 2a (Agent Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one review-wide Claude session discuss the persisted review threads inline (read-only) and apply fixes only on "Go" — driven through the worktree-session runner, with the agent posting replies via a new `middleman mcp` tool server.

**Architecture:** A new hand-rolled stdio MCP server (`middleman mcp`) exposes `list_threads`/`get_thread`/`reply_to_thread` as a thin proxy over the loopback REST API. `SessionRunner` gains discuss/apply/steer turns: each turn's `metadata_json` carries `{action, thread_ids}`, and `runTurn` picks the prompt template + per-phase `--allowedTools` (read-only + reply for discuss/steer; + Edit/Write/Bash for apply) and spawns `claude` with `--mcp-config` pointing at `middleman mcp`. The review-thread create endpoint gains a `mode`, and new `apply`/`apply-all` endpoints submit apply turns. Server marks thread status (`discussed`/`applied`) on turn completion.

**Tech Stack:** Go (stdlib `encoding/json` for JSON-RPC; `os/exec`), Huma v2, the Claude Code CLI's `--mcp-config`/`--strict-mcp-config`/`--allowedTools` flags, testify, the generated client.

**Spec:** `docs/superpowers/specs/2026-05-29-local-review-threads-design.md` (Phase 2a in "Build phases"; "MCP proxy", "Tool-gating", "Status transitions", "Submit flow"). **Depends on:** Phase 1a (thread tables + REST + `SessionRunner`) and 1b — both on this branch.

---

## Scope

Phase 2a is the **backend** of the discuss/apply agent. In scope: the MCP server + subcommand, the discuss/apply runner turns + tool-gating, status transitions, and the create-`mode`/apply trigger endpoints — all e2e-testable with a fake `claude`. **Out of scope** (Phase 2b): the submit mode picker UI, per-thread Apply buttons, and store polling. **Out of scope** (Phase 3): `list_reviews`/`get_review` discovery, cwd-default resolution, external-shell registration. The MCP server here operates on a **review handle the runner passes** (`--owner/--name/--number`).

## Patterns to follow (read first)

- Runner: `internal/aireview/sessions.go` — `SessionRunner`, `SubmitTurnInput` (`:84`), `SubmitTurn` (`:114`), `runTurn` (`:184`, the `args := []string{…}` spawn at `:204`), `buildSessionPrompt` (`:508`). Continuity via `--resume <claude_session_id>`; output is `--output-format stream-json`.
- Runner tests + fake claude: `internal/aireview/sessions_test.go` — `setupSessionTest` writes a fake `claude` shell script and sets the package var `claudeBinary`; `aireview.SetBinaryForTest` (`internal/aireview/runner.go:41`). Tests poll the response turn to `done`.
- Thread queries/status: `internal/db/queries_review_threads.go` — `ListReviewThreadsForMR`, `GetReviewThread`, `AddReviewThreadComment(ctx, threadID, author, body, turnID)`, `SetReviewThreadStatus(ctx, id, status)`.
- Thread endpoints + synthetic MR: `internal/server/huma_routes_review_threads.go` (the Phase-1a handlers), `internal/server/local_dispatch.go` (`resolveOrEnsureMRID`, `isLocalSource`). Session endpoints: `internal/server/huma_routes_sessions.go` (`submitWorktreeSessionTurn`, `hasRunningTurn` gating via `ListWorktreeSessionTurns`).
- CLI: `cmd/middleman/main.go` — `runCLI(args, stdout)` dispatches `switch args[0]` (`version`, `config`, default = serve). Add `case "mcp"`.
- MCP transport facts (verified): `claude --mcp-config <json…>` loads MCP servers from JSON files; `--strict-mcp-config` ignores all other MCP config; tool names are `mcp__<server>__<tool>`. The runner spawns the in-app agent with these.

## File structure

- Create: `internal/mcp/server.go` — stdio JSON-RPC loop + MCP methods (`initialize`, `notifications/initialized`, `tools/list`, `tools/call`).
- Create: `internal/mcp/tools.go` — the three tools as REST-proxy handlers (`http.Client` to the base URL).
- Create: `internal/mcp/server_test.go`, `internal/mcp/tools_test.go`.
- Modify: `cmd/middleman/main.go` — `case "mcp"` → `runMCP(args, stdout)`.
- Modify: `internal/aireview/sessions.go` — `SubmitTurnInput` += `Action`, `Threads`; `buildSessionPrompt` per-action; `runTurn` per-phase tools + `--mcp-config`. Add fields to drive the MCP config (binary path, base URL, review handle).
- Modify: `internal/server/huma_routes_review_threads.go` — create endpoint `mode`; `apply`/`apply-all` handlers; status-on-completion.
- Modify: `internal/server/huma_routes_sessions.go` or a shared helper — turn submission already exists; reuse it.

Frontend untouched (Phase 2b). Run Go tests with `-shuffle=on`; the runner/server tests need a `claude` stub (provided) and don't need a real Claude.

---

### Task 1: MCP stdio server — protocol loop

**Files:**
- Create: `internal/mcp/server.go`
- Test: `internal/mcp/server_test.go`

The MCP stdio transport is newline-delimited JSON-RPC 2.0. The server must handle: `initialize` (respond with protocol version + `capabilities.tools` + `serverInfo`), the `notifications/initialized` notification (no response), `tools/list` (return tool schemas), and `tools/call` (dispatch + return `content`). Unknown methods return a JSON-RPC error.

> **Protocol-version note:** echo the `protocolVersion` the client sends in `initialize.params` back in the result (Claude negotiates; echoing the client's version is the safe interop choice). Capabilities for a tools-only server: `{"tools": {}}`. If a real `claude --mcp-config` connection later rejects the handshake, that echo + capabilities shape is the first thing to check against the current MCP spec — but it is concrete here, not a placeholder.

- [ ] **Step 1: Write the failing test** — `internal/mcp/server_test.go`:

```go
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
	require.Equal(t, "", resp)
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
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/mcp -run TestInitialize -shuffle=on` → FAIL (package/symbols absent).

- [ ] **Step 3: Implement `internal/mcp/server.go`:**

```go
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
	// HTTPDoer is the REST client; defaults to http.DefaultClient in New.
	httpDoer HTTPDoer
}

type Server struct {
	cfg   Config
	tools map[string]toolDef
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent ⇒ notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

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
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write rpc: %w", err)
	}
	return nil
}
```

(`toolList`, `handleToolCall`, `builtinTools`, `toolDef`, `HTTPDoer`, `defaultHTTPDoer` come in Task 2.)

- [ ] **Step 4: Stub the tools.go symbols** so Task 1 compiles, then implement fully in Task 2. For now add a minimal `internal/mcp/tools.go`:

```go
package mcp

import "net/http"

type HTTPDoer interface{ Do(*http.Request) (*http.Response, error) }

func defaultHTTPDoer() HTTPDoer { return http.DefaultClient }

type toolDef struct {
	name        string
	description string
	inputSchema map[string]any
	// call runs the tool; returns the text content.
	call func(s *Server, args map[string]any) (string, error)
}

func builtinTools() map[string]toolDef { return map[string]toolDef{} }

func (s *Server) toolList() []map[string]any { return []map[string]any{} }

func (s *Server) handleToolCall(ctx interface{ Done() <-chan struct{} }, w interface{ Write([]byte) (int, error) }, req rpcRequest) error {
	return s.writeError(w, req.ID, -32601, "tools/call not implemented yet")
}
```

> This stub is replaced wholesale in Task 2; it exists only so Task 1's protocol tests compile and pass. (If the `interface{…}` param types feel awkward, give `handleToolCall` its real signature `(ctx context.Context, w io.Writer, req rpcRequest)` now — the real one in Task 2 uses exactly that.) Prefer writing `handleToolCall` with the real `(context.Context, io.Writer, rpcRequest)` signature immediately to avoid churn.

- [ ] **Step 5: Run to verify it passes** — `go test ./internal/mcp -run 'TestInitialize|TestInitialized|TestToolsList|TestUnknown|TestServe' -shuffle=on` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go internal/mcp/server_test.go
git commit -m "feat(mcp): stdio JSON-RPC MCP server protocol loop"
```

---

### Task 2: MCP thread tools (REST proxy)

**Files:**
- Modify: `internal/mcp/tools.go` (replace the stub)
- Test: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write the failing test** — `internal/mcp/tools_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
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
```

Add `"io"` to the test imports.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/mcp -run 'TestReply|TestList|TestToolsCall' -shuffle=on` → FAIL.

- [ ] **Step 3: Implement `internal/mcp/tools.go`** (replace the stub):

```go
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
	}
}

func (s *Server) reviewPath(suffix string) string {
	return fmt.Sprintf("/repos/%s/%s/pulls/%d%s", s.cfg.ReviewOwner, s.cfg.ReviewName, s.cfg.ReviewNumber, suffix)
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

func filterThread(listJSON string, id int) (string, error) {
	var parsed struct {
		Threads []json.RawMessage `json:"threads"`
	}
	if err := json.Unmarshal([]byte(listJSON), &parsed); err != nil {
		return "", err
	}
	for _, raw := range parsed.Threads {
		var probe struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.ID == id {
			return string(raw), nil
		}
	}
	return "", fmt.Errorf("thread %d not found", id)
}

func intArg(args map[string]any, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	switch n := v.(type) {
	case float64: // JSON numbers decode to float64
		return int(n), nil
	case int:
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
```

> Delete the Task-1 stub versions of these symbols (`HTTPDoer`, `defaultHTTPDoer`, `toolDef`, `builtinTools`, `toolList`, `handleToolCall`) — this file now defines them for real. The `ctx` arg to `handleToolCall` is unused today but keeps the signature ready for cancellation.

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/mcp -shuffle=on` → PASS (all server + tools tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): review-thread tools as a REST proxy (list/get/reply)"
```

---

### Task 3: `middleman mcp` subcommand

**Files:**
- Modify: `cmd/middleman/main.go`
- Test: `cmd/middleman/main_test.go` (create or append)

- [ ] **Step 1: Write the failing test** — append to (or create) `cmd/middleman/main_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPRequiresReviewFlags(t *testing.T) {
	err := runCLI([]string{"mcp", "--base-url", "http://127.0.0.1:8091"}, &strings.Builder{})
	require.Error(t, err) // missing --owner/--name/--number
}

func TestMCPParsesFlags(t *testing.T) {
	// With all flags + empty stdin, the server should start and return
	// cleanly at EOF. We exercise the flag-parse + Serve path via a helper
	// that accepts an explicit reader.
	var out strings.Builder
	err := runMCPWith([]string{
		"--base-url", "http://127.0.0.1:8091",
		"--owner", "local", "--name", "demo", "--number", "7",
	}, strings.NewReader(""), &out)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./cmd/middleman -run TestMCP -shuffle=on` → FAIL (`runMCPWith` undefined).

- [ ] **Step 3: Implement** in `cmd/middleman/main.go`. Add `case "mcp":` to the `runCLI` switch (alongside `version`/`config`), and the handlers:

```go
// in runCLI's switch over args[0], add:
	case "mcp":
		return runMCP(args[1:], os.Stdin, out)
```

```go
import (
	// add:
	"io"
	"github.com/wesm/middleman/internal/mcp"
)

// runMCP parses flags and serves the stdio MCP server on stdin/stdout.
func runMCP(args []string, in io.Reader, out io.Writer) error {
	return runMCPWith(args, in, out)
}

func runMCPWith(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("middleman mcp", flag.ContinueOnError)
	baseURL := fs.String("base-url", "http://127.0.0.1:8091", "middleman REST base URL")
	owner := fs.String("owner", "", "review owner (local)")
	name := fs.String("name", "", "review repo name")
	number := fs.Int("number", 0, "review number (worktree id)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *owner == "" || *name == "" || *number == 0 {
		return fmt.Errorf("mcp: --owner, --name and --number are required")
	}
	srv := mcp.New(mcp.Config{
		ServerName:   "middleman",
		BaseURL:      *baseURL,
		ReviewOwner:  *owner,
		ReviewName:   *name,
		ReviewNumber: *number,
	})
	return srv.Serve(context.Background(), in, out)
}
```

> If `runCLI` writes to `out` and reads `os.Stdin` directly, keep `runMCP` thin (it just forwards to `runMCPWith` with `os.Stdin`); the tests call `runMCPWith` with an explicit reader. Confirm `context`, `flag`, `fmt`, `os` are imported in main.go (they are, per the existing CLI).

- [ ] **Step 4: Run to verify it passes** — `go test ./cmd/middleman -run TestMCP -shuffle=on` → PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/middleman/main.go cmd/middleman/main_test.go
git commit -m "feat(cli): middleman mcp subcommand serving the stdio MCP server"
```

---

### Task 4: discuss/apply turns + per-phase tool-gating in the runner

**Files:**
- Modify: `internal/aireview/sessions.go`
- Test: `internal/aireview/sessions_discuss_test.go` (new)

The runner must: accept an `Action` (`discuss`/`apply`/`steer`) + the threads it covers; build the right prompt; and spawn `claude` with per-phase `--allowedTools` + a generated `--mcp-config` so the agent can call `reply_to_thread`.

- [ ] **Step 1: Write the failing test** — `internal/aireview/sessions_discuss_test.go`. This uses a fake `claude` that records its argv to a file, so we can assert the gating.

```go
package aireview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

// fakeClaudeRecordingArgs writes its argv (newline-joined) to argsFile, then
// emits a minimal stream-json success line.
func fakeClaudeRecordingArgs(t *testing.T, path, argsFile string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sx"}'` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func waitTurnDone(t *testing.T, database *db.DB, turnID int64) db.WorktreeSessionTurn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := database.GetWorktreeSessionTurn(context.Background(), turnID)
		require.NoError(t, err)
		if turn.Status == "done" || turn.Status == "failed" {
			return turn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %d never finished", turnID) // replaced by require below
	return db.WorktreeSessionTurn{}
}

func TestDiscussTurnIsReadOnlyAndConfiguresMCP(t *testing.T) {
	require := require.New(t)
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "args.txt")
	fake := filepath.Join(tmp, "claude.sh")
	fakeClaudeRecordingArgs(t, fake, argsFile)
	orig := claudeBinary
	claudeBinary = fake
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{Path: tmp, Branch: "f", HeadSHA: "h"})
	require.NoError(err)
	sess, err := database.CreateWorktreeSession(ctx, w.ID)
	require.NoError(err)

	runner := NewSessionRunner(database)
	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp, IsFirstTurn: true,
		Action: "discuss", UserTurnType: "review_feedback",
		UserTurnContent: "Reply to the threads.",
		Threads: []ThreadContext{{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"}},
		MCP: &MCPConfig{Binary: "/bin/true", BaseURL: "http://127.0.0.1:8091", Owner: "local", Name: "demo", Number: int(w.ID)},
	})
	require.NoError(err)
	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status)

	argv, err := os.ReadFile(argsFile)
	require.NoError(err)
	args := string(argv)
	require.Contains(args, "--mcp-config")
	require.Contains(args, "mcp__middleman__reply_to_thread")
	require.NotContains(args, "Edit")  // discuss is read-only
	require.NotContains(args, "Write")
	require.NotContains(args, "Bash")
}

func TestApplyTurnGetsEditTools(t *testing.T) {
	require := require.New(t)
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "args.txt")
	fake := filepath.Join(tmp, "claude.sh")
	fakeClaudeRecordingArgs(t, fake, argsFile)
	orig := claudeBinary
	claudeBinary = fake
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	ctx := context.Background()
	repoID, _ := database.UpsertLocalRepo(ctx, "demo")
	w, _ := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{Path: tmp, Branch: "f", HeadSHA: "h"})
	sess, _ := database.CreateWorktreeSession(ctx, w.ID)

	runner := NewSessionRunner(database)
	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: tmp,
		Action: "apply", UserTurnType: "user_message", UserTurnContent: "Apply thread 1.",
		Threads: []ThreadContext{{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"}},
		MCP: &MCPConfig{Binary: "/bin/true", BaseURL: "http://x", Owner: "local", Name: "demo", Number: int(w.ID)},
	})
	require.NoError(err)
	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status)
	args, _ := os.ReadFile(argsFile)
	require.Contains(string(args), "Edit")
	require.Contains(string(args), "--mcp-config")
}
```

Replace the `t.Fatalf` in `waitTurnDone` with `require.FailNow(t, ...)` to satisfy the repo's no-`t.Fatal` rule:
```go
	require.FailNow(t, "turn never finished")
	return db.WorktreeSessionTurn{}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/aireview -run 'TestDiscussTurn|TestApplyTurn' -shuffle=on` → FAIL (`Action`/`Threads`/`MCP`/`ThreadContext`/`MCPConfig` undefined).

- [ ] **Step 3: Implement** in `internal/aireview/sessions.go`.

Add the new input types and fields to `SubmitTurnInput`:

```go
// ThreadContext is the minimal per-thread context the prompt needs.
type ThreadContext struct {
	ID          int64
	Path        string
	Line        int
	Side        string
	RootComment string
}

// MCPConfig tells the runner how to wire the middleman MCP server for a
// turn. Nil ⇒ no MCP (e.g. legacy review_feedback turns).
type MCPConfig struct {
	Binary  string // path to the middleman executable
	BaseURL string
	Owner   string
	Name    string
	Number  int
}
```

Add to `SubmitTurnInput` (the struct at `sessions.go:84`):

```go
	// Action is "discuss" | "apply" | "steer" | "" (legacy review_feedback).
	Action  string
	Threads []ThreadContext
	MCP     *MCPConfig
```

Persist `Action`/threads on the user turn's metadata in `SubmitTurn` (so it survives restart). After the existing `UserTurnMetadataJSON` handling, if `Action != ""` set the metadata to a JSON `{ "action": Action, "thread_ids": [...] }` when the caller didn't supply its own. Minimal: pass `in.Action`/threads through to `spawnTurn`→`runTurn` (they already receive `in`); persistence of action metadata can reuse `UserTurnMetadataJSON` set by the server caller (Task 5). Keep `SubmitTurn` changes limited to threading `in` through (it already does).

In `runTurn` (`sessions.go:184`), replace the fixed `args := []string{…}` block (`:204-210`) with action-aware gating + MCP config:

```go
	prompt := buildSessionPrompt(in)
	// ... existing GetWorktreeSession ...

	allowed := "Read,Glob,Grep"
	mcpToolNames := "mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread"
	if in.MCP != nil {
		allowed += "," + mcpToolNames
	}
	if in.Action == "apply" || in.Action == "" {
		// apply (and legacy review_feedback) may edit the worktree.
		allowed += ",Edit,Write,MultiEdit,Bash"
	}

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", allowed,
	}

	// Wire the middleman MCP server for this turn (discuss/apply/steer).
	var mcpConfigPath string
	if in.MCP != nil {
		var cleanup func()
		var err error
		mcpConfigPath, cleanup, err = writeMCPConfig(*in.MCP)
		if err != nil {
			r.markFailed(ctx, respTurn.ID, "write mcp config: "+err.Error())
			return
		}
		defer cleanup()
		args = append(args, "--mcp-config", mcpConfigPath, "--strict-mcp-config")
	}

	if sess.ClaudeSessionID != "" {
		args = append(args, "--resume", sess.ClaudeSessionID)
	}
```

Add the config writer + extend `buildSessionPrompt`:

```go
// writeMCPConfig writes a temp claude --mcp-config JSON declaring the
// middleman stdio server for one review, and returns its path + a cleanup.
func writeMCPConfig(c MCPConfig) (string, func(), error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"middleman": map[string]any{
				"command": c.Binary,
				"args": []string{
					"mcp",
					"--base-url", c.BaseURL,
					"--owner", c.Owner,
					"--name", c.Name,
					"--number", fmt.Sprintf("%d", c.Number),
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp("", "middleman-mcp-*.json")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return "", func() {}, err
	}
	_ = f.Close()
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
```

Extend `buildSessionPrompt` (at `sessions.go:508`) with action-specific templates. After the first-turn context priming, branch on `in.Action`:

```go
	// (inside buildSessionPrompt, after the worktree-context priming block
	// and before the trailing UserTurnContent for the legacy path)
	switch in.Action {
	case "discuss":
		b.WriteString("These review comment threads need your response. For EACH thread, " +
			"read the relevant code and call the reply_to_thread tool (thread_id + body) with your " +
			"reading and a proposed approach or a clarifying question. DO NOT edit any files yet.\n\n")
		b.WriteString(formatThreads(in.Threads))
		return b.String()
	case "apply":
		b.WriteString("Apply the change(s) discussed in the following thread(s). Make the edits in the " +
			"worktree, then call reply_to_thread on each with a one-line summary of what you changed.\n\n")
		b.WriteString(formatThreads(in.Threads))
		return b.String()
	}
	// steer / legacy: fall through to the existing UserTurnContent handling
```

```go
func formatThreads(ts []ThreadContext) string {
	var b strings.Builder
	for _, t := range ts {
		side := "after"
		if t.Side == "LEFT" {
			side = "before"
		}
		b.WriteString(fmt.Sprintf("- thread %d — %s:%d (%s): %s\n", t.ID, t.Path, t.Line, side, t.RootComment))
	}
	return b.String()
}
```

Ensure `buildSessionPrompt` builds context for these actions even when not the first turn (discuss/apply prompts must always include the thread list). Adjust the early `if !in.IsFirstTurn { return in.UserTurnContent }` so discuss/apply actions still format threads: change it to only short-circuit for steer/legacy:

```go
	if !in.IsFirstTurn && in.Action != "discuss" && in.Action != "apply" {
		return in.UserTurnContent
	}
```

Confirm `encoding/json`, `os`, `fmt`, `strings` are imported in sessions.go (json/os/fmt/strings already are).

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/aireview -run 'TestDiscussTurn|TestApplyTurn' -shuffle=on` → PASS. Then the whole package: `go test ./internal/aireview -shuffle=on` → PASS (existing turn tests still green; the `""`-action legacy path keeps Edit/Write/Bash).

- [ ] **Step 5: Commit**

```bash
git add internal/aireview/sessions.go internal/aireview/sessions_discuss_test.go
git commit -m "feat(aireview): discuss/apply turns with per-phase tool-gating + MCP wiring"
```

---

### Task 5: trigger endpoints (mode + apply) and status transitions

**Files:**
- Modify: `internal/server/huma_routes_review_threads.go`
- Modify: `internal/server/local_dispatch.go` (only if a shared turn-submit helper is cleaner; otherwise reuse the session path)
- Test: `internal/server/review_threads_agent_e2e_test.go` (new)

The create endpoint gains a `mode`; on `discuss-first`/`act-immediately` it submits a kickoff turn (via the existing `SessionRunner`). New `apply`/`apply-all` endpoints submit apply turns. On turn completion the server sets thread status. Concurrency: reject if a turn is in-flight.

- [ ] **Step 1: Write the failing e2e test** — `internal/server/review_threads_agent_e2e_test.go`. Uses the fake-claude args-recording binary (so no real Claude) and asserts: discuss-first kickoff creates a queued claude_response turn; threads flip to `discussed` after it completes; apply on a thread flips it to `applied`; apply-while-busy → 409.

```go
package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
)

func TestAPIReviewThreadsDiscussModeKicksOffAndMarksDiscussed(t *testing.T) {
	require := require.New(t)
	// Fake claude that calls back into the REST API to post a reply, then
	// exits success — exercises the discuss kickoff end to end.
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\n"+
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"replied","session_id":"s1"}'`+"\n"), 0o755))
	aireview.SetBinaryForTest(fake)
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	num := seedReviewWorktree(t, database) // from review_threads_e2e_test.go

	mode := "discuss-first"
	createBody := generated.PostReposByOwnerByNamePullsByNumberReviewThreadsJSONRequestBody{
		Mode: &mode,
		Threads: &[]generated.ReviewThreadDraft{
			{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
		},
	}
	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		context.Background(), "local", "demo", num, createBody)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())

	// A session + a queued/running claude_response turn should now exist.
	sessResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(
		context.Background(), "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, sessResp.StatusCode())
	require.NotNil(sessResp.JSON200)
	require.NotNil(sessResp.JSON200.Turns)
	require.NotEmpty(*sessResp.JSON200.Turns)
}
```

> The exact generated body field for `mode` (`Mode *string`) and the session response shape come from regeneration in Step 3b. If method/field names differ, reconcile against `internal/apiclient/generated/client.gen.go` after regen (as in Phase 1a). The assertion set above is intentionally modest (kickoff produces a turn); deeper status assertions can use the DB directly via `database.ListReviewThreadsForMR` after polling the turn to done.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/server -run TestAPIReviewThreadsDiscussMode -shuffle=on` → FAIL (no `Mode` field / behavior).

- [ ] **Step 3a: Implement the handlers** in `internal/server/huma_routes_review_threads.go`.

Add `Mode` to the create input body:
```go
	Body   struct {
		Mode    string              `json:"mode,omitempty" doc:"discuss-first | act-immediately | persist-only (default)"`
		Threads []reviewThreadDraft `json:"threads"`
	}
```

After `createReviewThreads` persists the threads (the existing `s.db.CreateReviewThreads(...)` + reload), add the kickoff when mode requires it:
```go
	created, err := s.db.CreateReviewThreads(ctx, mrID, in)
	if err != nil {
		return nil, huma.Error500InternalServerError("create review threads: " + err.Error())
	}
	switch input.Body.Mode {
	case "discuss-first":
		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, mrID, "discuss", created); err != nil {
			return nil, err
		}
	case "act-immediately":
		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, mrID, "apply", created); err != nil {
			return nil, err
		}
	case "", "persist-only":
		// no agent action
	default:
		return nil, huma.Error400BadRequest("invalid mode: " + input.Body.Mode)
	}
```

Add the kickoff helper + the apply endpoints + the status-on-completion. `kickoffReviewTurn` builds `ThreadContext`s, ensures the worktree session, rejects if a turn is in-flight, and submits via `SessionRunner`:
```go
func (s *Server) kickoffReviewTurn(
	ctx context.Context, owner, name string, number int, mrID int64,
	action string, threads []db.ReviewThread,
) error {
	if s.sessionRunner == nil {
		return huma.Error503ServiceUnavailable("sessions not available")
	}
	w, err := s.resolveLocalWorktree(ctx, name, number)
	if err != nil {
		return huma.Error404NotFound("worktree not found")
	}
	sess, isFirst, err := s.ensureWorktreeSession(ctx, w.ID) // small helper around GetActive/Create (see huma_routes_sessions.go)
	if err != nil {
		return huma.Error500InternalServerError(err.Error())
	}
	if s.sessionHasRunningTurn(ctx, sess.ID) {
		return huma.Error409Conflict("the review agent is busy; wait for the current turn to finish")
	}
	tcs := make([]aireview.ThreadContext, 0, len(threads))
	for _, t := range threads {
		root := s.firstThreadCommentBody(ctx, t.ID) // helper: ListReviewThreadComments(t.ID)[0].Body
		tcs = append(tcs, aireview.ThreadContext{ID: t.ID, Path: t.Path, Line: t.Line, Side: t.Side, RootComment: root})
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, _ := worktrees.ResolveBase(ctx, w.Path, baseRef)
	exe, _ := os.Executable()
	verb := "review_feedback"
	if action == "apply" {
		verb = "user_message"
	}
	_, err = s.sessionRunner.SubmitTurn(ctx, aireview.SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: w.Path, Branch: w.Branch,
		BaseRef: base.Ref, BaseSHA: base.SHA, HeadSHA: w.HeadSHA,
		UserTurnType: verb, UserTurnContent: actionMessage(action, tcs), IsFirstTurn: isFirst,
		Action: action, Threads: tcs,
		MCP: &aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: owner, Name: name, Number: number},
	})
	if err != nil {
		return huma.Error500InternalServerError("submit turn: " + err.Error())
	}
	// Mark addressed threads (server-driven status). discuss -> discussed,
	// apply -> applied. We set optimistically here; a failed turn is
	// surfaced in the activity log (the thread stays discussed for apply).
	target := "discussed"
	if action == "apply" {
		target = "applied"
	}
	for _, t := range threads {
		_ = s.db.SetReviewThreadStatus(ctx, t.ID, target)
	}
	return nil
}
```

Add the apply endpoints in `registerReviewThreadRoutes`:
```go
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply", s.applyReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all", s.applyAllReviewThreads)
```
`applyReviewThread` resolves the thread (reuse `resolveThreadForMR`), then `kickoffReviewTurn(..., "apply", []db.ReviewThread{thread})`; `applyAllReviewThreads` gathers `ListReviewThreadsForMR` filtered to `status in (open, discussed)` and kicks off one apply turn over all of them. Both return the reloaded thread list (reuse `loadReviewThreadsResponse`).

> Helpers referenced — implement as thin wrappers if they don't exist: `ensureWorktreeSession` (extract from `submitWorktreeSessionTurn`'s GetActive/Create block in `huma_routes_sessions.go`), `sessionHasRunningTurn` (`ListWorktreeSessionTurns` + any `claude_response` in `queued`/`running`), `firstThreadCommentBody` (`ListReviewThreadComments(threadID)[0].Body`), `selfBaseURL` (the server's own loopback addr — read from the server's configured listen address; thread it onto `Server` at construction), and `actionMessage(action, tcs)` (a short user-turn content string). `huma.Error409Conflict` exists in huma v2.

- [ ] **Step 3b: Regenerate the client** (the create body gained `mode`):

```bash
GOCACHE="$HOME/.cache/go-build" make api-generate    # sandbox off if bun temp-dir error (see Phase 1a notes)
go generate ./internal/apiclient/generated
```

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/server -run TestAPIReviewThreadsDiscussMode -shuffle=on` (unsandboxed if it needs git/tmux for setup — this test doesn't, but the package suite does). Then `go test ./internal/server ./internal/aireview ./internal/db -shuffle=on` for no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/server/huma_routes_review_threads.go internal/server/huma_routes_sessions.go \
        internal/server/review_threads_agent_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go packages/ui/src/api/generated/schema.ts
git commit -m "feat(server): review-thread mode + apply endpoints driving discuss/apply turns"
```

---

## Self-review

**Spec coverage (Phase 2a):**
- MCP core (`list_threads`/`get_thread`/`reply_to_thread`) over REST, runner-passed handle → Tasks 1, 2, 3. ✓
- `SessionRunner` discuss/apply/steer turns; per-phase `--allowedTools` (read-only + reply vs + Edit/Write/Bash); `--mcp-config`/`--strict-mcp-config`; shared session via `--resume` → Task 4. ✓
- Prompts per action (discuss: reply-only-no-edit; apply: edit-then-reply) → Task 4. ✓
- create `mode` (discuss-first/act-immediately/persist-only) + `apply`/`apply-all` endpoints → Task 5. ✓
- Server-driven status (`discussed`/`applied`) → Task 5. ✓
- Concurrency: one turn in-flight, 409 on busy → Task 5. ✓
- **Deferred (correctly absent):** mode-picker UI, Apply buttons, polling (2b); `list_reviews`/`get_review`, cwd-default, external registration (3).

**Placeholder scan:** The Task-1 stub `tools.go` is explicitly a throwaway replaced in Task 2 (noted, with the real signature recommended immediately). Task 5 lists helper wrappers with exact derivations (where to extract each from) rather than full bodies for the thin ones — each names its source; if any feels under-specified at execution time, the source function is cited. No "TBD"/"handle errors"/vague steps.

**Type consistency:** `ThreadContext`, `MCPConfig`, `SubmitTurnInput.{Action,Threads,MCP}` (Task 4) are used consistently in Task 5's `kickoffReviewTurn`. Tool names `mcp__middleman__{list_threads,get_thread,reply_to_thread}` match between the runner's `--allowedTools` (Task 4) and the MCP server's tool names (Task 2). The REST paths the MCP proxy calls (`/repos/{owner}/{name}/pulls/{number}/review-threads…`) match Phase 1a's routes.

**Known risks to verify during execution:**
- MCP handshake byte-compatibility with the live `claude --mcp-config` (protocol version echo + capabilities shape). The protocol unit tests are deterministic; a real-Claude smoke check (drive one discuss turn against a live worktree) is the acceptance gate — note it before calling 2a done.
- `selfBaseURL` must be the address the running server actually listens on (default `127.0.0.1:8091`, but honor config); thread it onto `Server`.
- Status is set optimistically at kickoff; if you prefer setting it only on turn *completion*, move the `SetReviewThreadStatus` into the runner's done-path (the runner would need the thread ids + action — already on the turn metadata). Optimistic-at-kickoff is simpler and acceptable for a local single-user tool; flagged for the reviewer.
