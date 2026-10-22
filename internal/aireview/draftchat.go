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
	"path/filepath"
	"strings"
	"time"
)

// These tool names must stay in sync with internal/mcp draft-chat tools.
const draftChatToolNames = "mcp__middleman__get_draft,mcp__middleman__revise_comment,mcp__middleman__get_pull"

// DraftChatMode says how a chat turn connects to an earlier Claude session.
type DraftChatMode string

const (
	DraftChatFork   DraftChatMode = "fork"   // --resume <ParentSessionID> --fork-session
	DraftChatResume DraftChatMode = "resume" // --resume <ParentSessionID>
	DraftChatFresh  DraftChatMode = "fresh"  // a new session
)

// DraftTurnInput is one queued Claude reply in a draft's conversation.
type DraftTurnInput struct {
	DraftID         string
	MessageID       int64 // the queued claude message to fill
	Owner           string
	Name            string
	Number          int
	Mode            DraftChatMode
	ParentSessionID string
	WorktreePath    string // fixed cwd: Claude Code keys sessions by cwd
	CheckoutSHA     string // checked out at WorktreePath when missing
	Prompt          string
	MCP             MCPConfig
}

// DraftChatWorktreePath is where fresh draft conversations run.
func (r *Runner) DraftChatWorktreePath(draftID string) string {
	return filepath.Join(r.rootDir, "draft-"+draftID)
}

// ReviewRunPath is the fixed worktree path of a review run, which forks
// of its session must run in.
func (r *Runner) ReviewRunPath(runID int64) string {
	return ReviewRunWorktreePath(r.rootDir, runID)
}

// SubmitDraftTurn queues a turn. Turns for one draft run in order so
// each can resume the session the previous one left; at most
// cap(draftSem) turns run at once across drafts.
func (r *Runner) SubmitDraftTurn(in DraftTurnInput) {
	r.mu.Lock()
	r.draftQueues[in.DraftID] = append(r.draftQueues[in.DraftID], in)
	if r.draftBusy[in.DraftID] {
		r.mu.Unlock()
		return
	}
	r.draftBusy[in.DraftID] = true
	r.mu.Unlock()
	go r.drainDraftQueue(in.DraftID)
}

func (r *Runner) drainDraftQueue(draftID string) {
	for {
		r.mu.Lock()
		q := r.draftQueues[draftID]
		if len(q) == 0 {
			delete(r.draftQueues, draftID)
			delete(r.draftBusy, draftID)
			r.mu.Unlock()
			return
		}
		in := q[0]
		r.draftQueues[draftID] = q[1:]
		r.mu.Unlock()
		r.runDraftTurn(in)
	}
}

// CancelDraftTurn cancels a queued or running turn.
func (r *Runner) CancelDraftTurn(ctx context.Context, messageID int64) error {
	if err := r.db.MarkDraftMessageCancelled(ctx, messageID); err != nil {
		return err
	}
	r.mu.Lock()
	cancel, ok := r.draftTurnsRunning[messageID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (r *Runner) runDraftTurn(in DraftTurnInput) {
	bg := context.Background()
	stillQueued := func() bool {
		m, err := r.db.GetReviewDraftMessage(bg, in.MessageID)
		return err == nil && m != nil && m.Status == "queued"
	}
	if !stillQueued() {
		return // cancelled while waiting in the draft queue
	}
	r.draftSem <- struct{}{}
	defer func() { <-r.draftSem }()
	if !stillQueued() {
		return // cancelled while waiting for a slot
	}

	// A fork or fresh turn queued behind an earlier turn of the same
	// draft must continue the session that turn created, not start
	// another one.
	if in.Mode != DraftChatResume {
		if d, err := r.db.GetReviewDraftByID(bg, in.DraftID); err == nil && d != nil &&
			d.ClaudeSessionID != "" && d.ChatWorktreePath != "" {
			in.Mode = DraftChatResume
			in.ParentSessionID = d.ClaudeSessionID
			in.WorktreePath = d.ChatWorktreePath
		}
	}

	ctx, cancel := context.WithTimeout(bg, r.draftTurnTimeout)
	defer cancel()
	r.mu.Lock()
	r.draftTurnsRunning[in.MessageID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.draftTurnsRunning, in.MessageID)
		r.mu.Unlock()
	}()

	if _, err := os.Stat(in.WorktreePath); err != nil {
		if _, err := r.provisionWorktreeAt(bg, in.Owner, in.Name, in.CheckoutSHA, in.WorktreePath); err != nil {
			_ = r.db.MarkDraftMessageFailed(bg, in.MessageID,
				fmt.Sprintf("cannot check out %s for this conversation: %v", shortSHA(in.CheckoutSHA), err), "")
			return
		}
	}
	// The session lives outside the worktree, keyed by its path; the
	// checkout is recreated at the same path for the next turn.
	defer r.cleanWorktreePath(bg, r.cloneDirFor(in.Owner, in.Name), in.WorktreePath)

	args := []string{
		"-p", in.Prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,Glob,Grep," + draftChatToolNames,
		"--disallowedTools", "Edit,Write,NotebookEdit,Bash,Agent",
	}
	if in.MCP.Binary != "" {
		cfgPath, cleanup, err := writeMCPConfig(in.MCP)
		if err != nil {
			_ = r.db.MarkDraftMessageFailed(bg, in.MessageID, "write mcp config: "+err.Error(), "")
			return
		}
		defer cleanup()
		args = append(args, "--mcp-config", cfgPath, "--strict-mcp-config")
	}
	switch in.Mode {
	case DraftChatFork:
		args = append(args, "--resume", in.ParentSessionID, "--fork-session")
	case DraftChatResume:
		args = append(args, "--resume", in.ParentSessionID)
	}

	cmd := exec.CommandContext(ctx, claudeBin(), args...)
	cmd.Dir = in.WorktreePath
	setPgid(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.db.MarkDraftMessageFailed(bg, in.MessageID, "stdout pipe: "+err.Error(), "")
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		_ = r.db.MarkDraftMessageFailed(bg, in.MessageID, "start claude: "+err.Error(), "")
		return
	}
	if err := r.db.MarkDraftMessageRunning(bg, in.MessageID, cmd.Process.Pid); err != nil {
		slog.Warn("mark draft turn running failed", "message_id", in.MessageID, "err", err)
	}

	state := &streamState{Events: []streamEvent{}}
	var reply string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		text, isResult, dirty := applyStreamMessage(state, line)
		if isResult {
			reply = text
		}
		if dirty {
			if raw, err := json.Marshal(state); err == nil {
				_ = r.db.UpdateDraftMessageStream(bg, in.MessageID, string(raw))
			}
		}
	}
	streamErr := scanner.Err()
	waitErr := cmd.Wait()
	raw, _ := json.Marshal(state)

	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = r.db.MarkDraftMessageFailed(bg, in.MessageID, fmt.Sprintf("turn timed out after %s", r.draftTurnTimeout), string(raw))
		}
		return // cancelled: CancelDraftTurn already set the status
	}
	if waitErr != nil || streamErr != nil {
		msg := fmt.Sprintf("claude exited: %v", waitErr)
		if streamErr != nil {
			msg += "\nstream: " + streamErr.Error()
		}
		if stderrBuf.Len() > 0 {
			msg += "\n" + strings.TrimSpace(stderrBuf.String())
		}
		_ = r.db.MarkDraftMessageFailed(bg, in.MessageID, msg, string(raw))
		return
	}
	if in.Mode != DraftChatResume && state.SessionID != "" {
		if err := r.db.SetReviewDraftChatSession(bg, in.DraftID, state.SessionID, in.WorktreePath); err != nil {
			slog.Warn("save draft chat session failed", "draft_id", in.DraftID, "err", err)
		}
	}
	if err := r.db.MarkDraftMessageDone(bg, in.MessageID, reply, string(raw)); err != nil {
		slog.Warn("mark draft turn done failed", "message_id", in.MessageID, "err", err)
	}
}
