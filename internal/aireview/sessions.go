package aireview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wesm/middleman/internal/db"
)

// SessionRunner owns interactive Claude sessions bound to local
// worktrees. It's the agent loop the user drives from the Activity
// tab: review-feedback submissions and free-text follow-ups land as
// turns; each turn spawns one `claude -p` subprocess that resumes
// the session via --resume after the first turn.
//
// Compared to the existing Runner (ask threads, briefs, commit
// analyses), SessionRunner:
//
//   - operates against the user's existing worktree, not a
//     provisioned ephemeral one
//   - allows Edit / Write / Bash so Claude can make changes in
//     response to feedback
//   - persists turns to middleman_worktree_session_turns instead of
//     ai_questions / ai_briefs
//   - keys cancellation by turn id rather than question id
//
// Like the Runner, every Claude invocation is one-shot: the runner
// doesn't keep a long-lived subprocess. State continuity is the
// CLI's `--resume <session_id>` feature, fed by the session row.
type SessionRunner struct {
	db SessionDB

	mu      sync.Mutex
	running map[int64]context.CancelFunc // by turn id

	// turnTimeout caps a single claude turn so a hung subprocess can't
	// wedge the session forever (a hung turn stays "running" and the
	// one-turn-at-a-time busy gate then 409s every later turn).
	turnTimeout time.Duration
}

// SessionDB is the narrow DB surface the SessionRunner needs.
// Exposed as an interface for stubbing in tests.
type SessionDB interface {
	GetWorktreeSession(ctx context.Context, id int64) (db.WorktreeSession, error)
	SetWorktreeSessionClaudeID(ctx context.Context, id int64, claudeSessionID string) error
	AddWorktreeSessionTurn(ctx context.Context, in db.NewWorktreeSessionTurn) (db.WorktreeSessionTurn, error)
	UpdateWorktreeSessionTurnFields(ctx context.Context, id int64, u db.UpdateWorktreeSessionTurn) error
	ListRunningWorktreeSessionTurns(ctx context.Context) ([]db.WorktreeSessionTurn, error)
}

// NewSessionRunner builds a runner backed by the given DB.
func NewSessionRunner(database SessionDB) *SessionRunner {
	return &SessionRunner{
		db:          database,
		running:     make(map[int64]context.CancelFunc),
		turnTimeout: 10 * time.Minute,
	}
}

// ReconcileOnStartup marks any leftover queued/running session
// turns as failed. Their subprocesses didn't survive the middleman
// restart; without this the UI would show them as in-flight
// forever. The session row itself is left alive — the user can
// submit a new turn against the same Claude session via --resume.
func (r *SessionRunner) ReconcileOnStartup(ctx context.Context) error {
	orphan, err := r.db.ListRunningWorktreeSessionTurns(ctx)
	if err != nil {
		return fmt.Errorf("list running session turns: %w", err)
	}
	failed := "failed"
	msg := "interrupted by middleman restart"
	for _, t := range orphan {
		_ = r.db.UpdateWorktreeSessionTurnFields(ctx, t.ID, db.UpdateWorktreeSessionTurn{
			Status:   &failed,
			Error:    &msg,
			ClearPID: true,
		})
	}
	return nil
}

// ThreadContext is the minimal per-thread context the prompt needs.
type ThreadContext struct {
	ID          int64
	Path        string
	Line        int
	Side        string
	RootComment string
}

// MCPConfig tells the runner how to wire the middleman MCP server for a
// turn. Nil => no MCP (e.g. legacy review_feedback turns).
type MCPConfig struct {
	Binary  string // path to the middleman executable
	BaseURL string
	Owner   string
	Name    string
	Number  int
}

// SubmitTurnInput packages a user-driven turn submission.
type SubmitTurnInput struct {
	SessionID    int64
	WorktreePath string // cwd for the Claude subprocess
	Branch       string // for context priming
	BaseRef      string
	BaseSHA      string
	HeadSHA      string
	// UserTurnType is "review_feedback" or "user_message".
	UserTurnType    string
	UserTurnContent string
	// UserTurnMetadataJSON is opaque metadata stored alongside the
	// user turn (e.g. linked draft comment ids for review_feedback).
	UserTurnMetadataJSON string
	// IsFirstTurn signals that this is the first turn in the
	// session — the runner primes the prompt with worktree context
	// instead of relying on --resume.
	IsFirstTurn bool
	// Action is "discuss" | "apply" | "steer" | "" (legacy review_feedback/user_message free-text follow-ups).
	Action  string
	Threads []ThreadContext
	MCP     *MCPConfig
}

// SubmitResult bundles the two new turn rows created by SubmitTurn.
type SubmitResult struct {
	UserTurn     db.WorktreeSessionTurn
	ResponseTurn db.WorktreeSessionTurn
}

// SubmitTurn inserts the user turn + a queued claude_response turn,
// then spawns Claude in the background. Returns immediately with
// both rows hydrated. Callers poll/subscribe to the response turn
// for status transitions.
func (r *SessionRunner) SubmitTurn(
	ctx context.Context, in SubmitTurnInput,
) (SubmitResult, error) {
	if in.SessionID == 0 {
		return SubmitResult{}, errors.New("session id required")
	}
	if in.WorktreePath == "" {
		return SubmitResult{}, errors.New("worktree path required")
	}
	if in.UserTurnType != "review_feedback" && in.UserTurnType != "user_message" {
		return SubmitResult{}, fmt.Errorf("unsupported user turn type %q", in.UserTurnType)
	}

	userTurn, err := r.db.AddWorktreeSessionTurn(ctx, db.NewWorktreeSessionTurn{
		SessionID:    in.SessionID,
		TurnType:     in.UserTurnType,
		Content:      in.UserTurnContent,
		Status:       "done",
		MetadataJSON: in.UserTurnMetadataJSON,
	})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("insert user turn: %w", err)
	}

	respTurn, err := r.db.AddWorktreeSessionTurn(ctx, db.NewWorktreeSessionTurn{
		SessionID: in.SessionID,
		TurnType:  "claude_response",
		Status:    "queued",
	})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("insert response turn: %w", err)
	}

	r.spawnTurn(in, respTurn)
	return SubmitResult{UserTurn: userTurn, ResponseTurn: respTurn}, nil
}

// CancelTurn kills any in-flight subprocess for the response turn
// and marks the row cancelled.
func (r *SessionRunner) CancelTurn(ctx context.Context, turnID int64) error {
	r.mu.Lock()
	cancel, ok := r.running[turnID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	cancelled := "cancelled"
	return r.db.UpdateWorktreeSessionTurnFields(ctx, turnID, db.UpdateWorktreeSessionTurn{
		Status:   &cancelled,
		ClearPID: true,
	})
}

func (r *SessionRunner) spawnTurn(in SubmitTurnInput, respTurn db.WorktreeSessionTurn) {
	ctx, cancel := context.WithTimeout(context.Background(), r.turnTimeout)
	r.mu.Lock()
	r.running[respTurn.ID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, respTurn.ID)
			r.mu.Unlock()
			cancel()
		}()
		r.runTurn(ctx, in, respTurn)
	}()
}

func (r *SessionRunner) runTurn(
	ctx context.Context, in SubmitTurnInput, respTurn db.WorktreeSessionTurn,
) {
	prompt := buildSessionPrompt(in)

	// Pull the current session row to get the most recent
	// claude_session_id — earlier turns in the same session may have
	// already populated it.
	sess, err := r.db.GetWorktreeSession(ctx, in.SessionID)
	if err != nil {
		r.markFailed(ctx, respTurn.ID, "load session: "+err.Error())
		return
	}

	// stream-json gives us the tool_use / tool_result blocks alongside
	// the text. We accumulate events as they arrive and flush them to
	// the turn row so the conversation pane can render Claude's tool
	// activity in near-real-time instead of waiting for the final
	// summary. --verbose is required by the CLI for stream-json with
	// -p.
	allowed := "Read,Glob,Grep"
	// These tool names must stay in sync with internal/mcp/tools.go. The
	// "mcp__<server>__<tool>" prefix uses the --mcp-config server key
	// "middleman" written by writeMCPConfig (not mcp.Config.ServerName).
	mcpToolNames := "mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread"
	if in.MCP != nil {
		allowed += "," + mcpToolNames
	}
	if in.Action == "apply" || in.Action == "" {
		// apply (and legacy review_feedback / user_message) may edit the worktree.
		allowed += ",Edit,Write,MultiEdit,Bash"
	}

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", allowed,
	}

	// Wire the middleman MCP server for this turn (discuss/apply).
	if in.MCP != nil {
		mcpConfigPath, cleanup, err := writeMCPConfig(*in.MCP)
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

	cmd := exec.CommandContext(ctx, claudeBinary, args...)
	cmd.Dir = in.WorktreePath
	setPgid(cmd)
	// CommandContext's default Cancel kills only the leader; with setPgid
	// a hung grandchild can keep the stdout pipe open so the stream loop
	// and cmd.Wait block past the deadline. Kill the whole group instead,
	// and bound the post-cancel wait so Wait can't hang on the pipe.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.markFailed(ctx, respTurn.ID, "stdout pipe: "+err.Error())
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		r.markFailed(ctx, respTurn.ID, "start claude: "+err.Error())
		return
	}
	pid := cmd.Process.Pid
	running := "running"
	if err := r.db.UpdateWorktreeSessionTurnFields(ctx, respTurn.ID, db.UpdateWorktreeSessionTurn{
		Status: &running,
		PID:    &pid,
	}); err != nil {
		slog.Warn("mark session turn running failed", "turn_id", respTurn.ID, "err", err)
	}

	state := &streamState{Events: []streamEvent{}}
	var finalText string
	var streamErr error

	// Each stream-json line can carry an assistant message with a
	// large tool_result body, so bump the scanner buffer well past
	// the default 64 KiB.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		text, ok := r.applyStreamLine(ctx, respTurn.ID, state, line)
		if ok && text != "" {
			finalText = text
		}
	}
	if err := scanner.Err(); err != nil {
		streamErr = err
	}

	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Hung turn: CommandContext killed claude on the deadline.
			// Mark it failed so the session frees (the busy gate no longer
			// 409s). Use a fresh context — the turn ctx is already Done, so
			// DB writes on it would fail.
			bg := context.Background()
			r.flushStreamState(bg, respTurn.ID, state)
			r.markFailed(bg, respTurn.ID, fmt.Sprintf("claude turn timed out after %s", r.turnTimeout))
		}
		// context.Canceled => CancelTurn already set status "cancelled";
		// don't overwrite it.
		return
	}
	if waitErr != nil {
		msg := fmt.Sprintf("claude exited: %v", waitErr)
		if stderrBuf.Len() > 0 {
			msg += "\n" + strings.TrimSpace(stderrBuf.String())
		}
		if streamErr != nil {
			msg += "\nstream: " + streamErr.Error()
		}
		// Persist whatever events arrived before the failure so the
		// reviewer can still see what Claude attempted.
		r.flushStreamState(ctx, respTurn.ID, state)
		r.markFailed(ctx, respTurn.ID, msg)
		return
	}
	if streamErr != nil {
		r.flushStreamState(ctx, respTurn.ID, state)
		r.markFailed(ctx, respTurn.ID, "read stream: "+streamErr.Error())
		return
	}

	// Persist the session id on first turn so follow-ups can --resume.
	if sess.ClaudeSessionID == "" && state.SessionID != "" {
		if err := r.db.SetWorktreeSessionClaudeID(ctx, in.SessionID, state.SessionID); err != nil {
			slog.Warn("save claude session id failed", "session_id", in.SessionID, "err", err)
		}
	}

	rawJSON, _ := json.Marshal(state)
	rawStr := string(rawJSON)
	done := "done"
	if err := r.db.UpdateWorktreeSessionTurnFields(ctx, respTurn.ID, db.UpdateWorktreeSessionTurn{
		Status:   &done,
		Content:  &finalText,
		RawJSON:  &rawStr,
		ClearPID: true,
	}); err != nil {
		slog.Warn("mark session turn done failed", "turn_id", respTurn.ID, "err", err)
	}
}

// streamEvent is one normalized step from claude's stream-json output.
// The frontend renders these as cards (tool calls + results) plus
// text bubbles, in addition to the final summary stored on
// turn.content.
type streamEvent struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ID        string          `json:"id,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// streamState is what we serialize into the turn's raw_json column.
// Kept compact so the polling endpoint stays cheap.
type streamState struct {
	SessionID string        `json:"session_id,omitempty"`
	Events    []streamEvent `json:"events"`
}

// streamMessage is the wire shape claude emits as JSON-lines. We
// pick out only the fields we care about; unknown fields are
// ignored.
type streamMessage struct {
	Type      string              `json:"type"`
	Subtype   string              `json:"subtype,omitempty"`
	SessionID string              `json:"session_id,omitempty"`
	Message   *streamInnerMessage `json:"message,omitempty"`
	Result    string              `json:"result,omitempty"`
	IsError   bool                `json:"is_error,omitempty"`
}

type streamInnerMessage struct {
	Role    string               `json:"role"`
	Content []streamContentBlock `json:"content"`
}

type streamContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// applyStreamLine parses one JSON-lines record from claude's stream
// output, mutates state to reflect it, and flushes the partial
// state to the turn row so the conversation pane reflects progress
// within a poll cycle. Returns the final summary text if this line
// was the result message that terminates a turn.
func (r *SessionRunner) applyStreamLine(
	ctx context.Context, turnID int64, state *streamState, line []byte,
) (string, bool) {
	var msg streamMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		// Skip unparseable lines — claude occasionally writes
		// non-JSON noise (warnings) ahead of the stream.
		return "", false
	}
	dirty := false
	switch msg.Type {
	case "system":
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
			dirty = true
		}
	case "assistant":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			switch b.Type {
			case "text":
				if b.Text != "" {
					state.Events = append(state.Events, streamEvent{
						Type: "text",
						Text: b.Text,
					})
					dirty = true
				}
			case "tool_use":
				state.Events = append(state.Events, streamEvent{
					Type:  "tool_use",
					Tool:  b.Name,
					Input: b.Input,
					ID:    b.ID,
				})
				dirty = true
			}
		}
	case "user":
		if msg.Message == nil {
			break
		}
		for _, b := range msg.Message.Content {
			if b.Type != "tool_result" {
				continue
			}
			state.Events = append(state.Events, streamEvent{
				Type:      "tool_result",
				ToolUseID: b.ToolUseID,
				Content:   flattenToolResultContent(b.Content),
				IsError:   b.IsError,
			})
			dirty = true
		}
	case "result":
		// Final summary. The result line also (re)states the
		// session id; capture it if we didn't see system init.
		if msg.SessionID != "" && state.SessionID == "" {
			state.SessionID = msg.SessionID
		}
		// Flush before returning so the in-flight raw_json reflects
		// everything we have.
		r.flushStreamState(ctx, turnID, state)
		if msg.IsError {
			return msg.Result, true
		}
		return msg.Result, true
	}
	if dirty {
		r.flushStreamState(ctx, turnID, state)
	}
	return "", false
}

// flushStreamState marshals state into raw_json and updates the
// turn row. Errors are swallowed (logged) — a flush failure
// shouldn't abort the stream.
func (r *SessionRunner) flushStreamState(
	ctx context.Context, turnID int64, state *streamState,
) {
	rawJSON, err := json.Marshal(state)
	if err != nil {
		slog.Warn("marshal stream state failed", "turn_id", turnID, "err", err)
		return
	}
	raw := string(rawJSON)
	if err := r.db.UpdateWorktreeSessionTurnFields(ctx, turnID, db.UpdateWorktreeSessionTurn{
		RawJSON: &raw,
	}); err != nil {
		slog.Warn("flush stream state failed", "turn_id", turnID, "err", err)
	}
}

// flattenToolResultContent normalizes the wire shape of a
// tool_result.content field. Claude emits it either as a bare string
// or as an array of {type:"text", text:"..."} blocks; we present a
// single string to the frontend.
func flattenToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string form first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fall back to content array form.
	var blocks []streamContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for i, blk := range blocks {
			if blk.Type != "text" {
				continue
			}
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	// Unknown shape — keep raw bytes so reviewers can still see
	// something.
	return string(raw)
}

func (r *SessionRunner) markFailed(ctx context.Context, turnID int64, msg string) {
	failed := "failed"
	if err := r.db.UpdateWorktreeSessionTurnFields(ctx, turnID, db.UpdateWorktreeSessionTurn{
		Status:   &failed,
		Error:    &msg,
		ClearPID: true,
	}); err != nil {
		slog.Warn("mark session turn failed failed", "turn_id", turnID, "err", err)
	}
}

// writeMCPConfig writes a temp `claude --mcp-config` JSON declaring the
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
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	_ = f.Close()
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

func formatThreads(ts []ThreadContext) string {
	var b strings.Builder
	for _, t := range ts {
		side := "after"
		if t.Side == "LEFT" {
			side = "before"
		}
		b.WriteString(fmt.Sprintf("- thread %d - %s:%d (%s): %s\n", t.ID, t.Path, t.Line, side, t.RootComment))
	}
	return b.String()
}

// buildSessionPrompt assembles the prompt sent on each turn. The
// first turn primes Claude with worktree context (where it is,
// what branch, what base, what's middleman expecting). Subsequent
// turns rely on --resume so the model keeps that context already
// — we only send the new user message.
func buildSessionPrompt(in SubmitTurnInput) string {
	// discuss/apply produce their own action-specific prompts (first turn
	// AND resumed) and must NOT inherit the generic "full Read / Edit /
	// Write / Bash access ... act directly" priming below: discuss is
	// read-only (reply only), and the thread list must always be included.
	switch in.Action {
	case "discuss":
		var b strings.Builder
		writeWorktreeContext(&b, in)
		b.WriteString("\n")
		b.WriteString("These review comment threads need your response. For EACH thread, " +
			"read the relevant code and call the reply_to_thread tool (thread_id + body) with your " +
			"reading and a proposed approach or a clarifying question. Do not change any files yet.\n\n")
		b.WriteString(formatThreads(in.Threads))
		return b.String()
	case "apply":
		var b strings.Builder
		writeWorktreeContext(&b, in)
		b.WriteString("\n")
		b.WriteString("Apply the change(s) discussed in the following thread(s). Make the changes in the " +
			"worktree, then call reply_to_thread on each with a one-line summary of what you changed. " +
			"Don't push, don't open PRs, don't touch remotes.\n\n")
		b.WriteString(formatThreads(in.Threads))
		return b.String()
	case "steer":
		var b strings.Builder
		writeWorktreeContext(&b, in)
		b.WriteString("\n")
		b.WriteString("The reviewer replied in a review thread. Read the relevant code, " +
			"respond to continue the discussion, and call the reply_to_thread tool (thread_id + body) " +
			"with your reply. Do not change any files — this is discussion only.\n\n")
		b.WriteString(formatThreads(in.Threads))
		b.WriteString("\nThe reviewer's message:\n")
		b.WriteString(in.UserTurnContent)
		return b.String()
	}

	// legacy path (review_feedback + free-text follow-ups)
	if !in.IsFirstTurn {
		return in.UserTurnContent
	}
	var b strings.Builder
	b.WriteString(
		"You are operating inside a git worktree that middleman is " +
			"presenting to a reviewer as a draft PR. ",
	)
	b.WriteString(
		"You have full Read / Edit / Write / Bash access to this worktree; " +
			"act directly to address the reviewer's feedback rather than describing what you'd do. ",
	)
	b.WriteString(
		"When you finish, summarize what changed and where (file paths + a one-line description per change). ",
	)
	b.WriteString(
		"You can use `git log`, `git diff`, etc. to ground yourself in the commit history. " +
			"Don't push, don't open PRs, don't touch remotes.\n\n",
	)
	writeWorktreeContext(&b, in)
	b.WriteString("\n")
	if in.UserTurnType == "review_feedback" {
		b.WriteString("The reviewer has submitted the following feedback. " +
			"Read the comments, look at the relevant files, and make the requested changes.\n\n")
	}
	b.WriteString(in.UserTurnContent)
	return b.String()
}

// writeWorktreeContext writes the worktree location block (path/branch/
// base/head) shared by the legacy priming and the discuss/apply prompts.
func writeWorktreeContext(b *strings.Builder, in SubmitTurnInput) {
	b.WriteString("Worktree path: ")
	b.WriteString(in.WorktreePath)
	b.WriteString("\n")
	if in.Branch != "" {
		b.WriteString("Branch: ")
		b.WriteString(in.Branch)
		b.WriteString("\n")
	}
	if in.BaseRef != "" {
		b.WriteString("Base: ")
		b.WriteString(in.BaseRef)
		if in.BaseSHA != "" {
			b.WriteString(" (")
			b.WriteString(shortSHA(in.BaseSHA))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if in.HeadSHA != "" {
		b.WriteString("HEAD: ")
		b.WriteString(shortSHA(in.HeadSHA))
		b.WriteString("\n")
	}
}

func shortSHA(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

