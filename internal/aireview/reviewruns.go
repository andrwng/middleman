package aireview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// These tool names must stay in sync with internal/mcp review-run tools.
const reviewRunToolNames = "mcp__middleman__propose_comment,mcp__middleman__list_drafts,mcp__middleman__get_pull"

type ReviewRunInput struct {
	RunID         int64
	Owner         string
	Name          string
	Number        int
	HeadSHA       string
	MergeBaseSHA  string
	Instructions  string
	PromptContext string
	MCP           MCPConfig
	// RepoDir, when set, is the reviewer's local repo to add the run's
	// worktree from (HeadSHA is then the snapshot commit).
	RepoDir string
}

// ReviewRunWorktreePath is fixed per run: Claude Code keys sessions by
// cwd, so every later fork of this run's session (PR 3 chats) must run
// in exactly this directory.
func ReviewRunWorktreePath(root string, runID int64) string {
	return filepath.Join(root, fmt.Sprintf("run-%d", runID))
}

// StartReviewRun provisions the run's worktree and spawns Claude in the
// background. The run row moves running -> done|failed|cancelled.
func (r *Runner) StartReviewRun(ctx context.Context, in ReviewRunInput) error {
	repoDir, err := r.repoDirFor(in.RepoDir, in.Owner, in.Name)
	if err != nil {
		_ = r.db.MarkAIReviewRunFailed(context.Background(), in.RunID, err.Error(), "")
		return err
	}
	path := ReviewRunWorktreePath(r.rootDir, in.RunID)
	if err := r.acquireWorktree(ctx, repoDir, in.HeadSHA, path); err != nil {
		_ = r.db.MarkAIReviewRunFailed(ctx, in.RunID, "provision worktree: "+err.Error(), "")
		return fmt.Errorf("provision worktree: %w", err)
	}
	var cached []string
	if in.RepoDir == "" { // local runs skip the per-commit cache, like local briefs
		cached = r.cachePRCommits(ctx, in.Owner, in.Name, path, in.MergeBaseSHA, in.HeadSHA)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), r.reviewRunTimeout)
	r.mu.Lock()
	r.reviewRunsRunning[in.RunID] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.reviewRunsRunning, in.RunID)
			r.mu.Unlock()
			cancel()
			r.releaseWorktree(repoDir, path)
		}()
		r.runReviewRun(runCtx, in, path, cached)
	}()
	return nil
}

func (r *Runner) CancelReviewRun(ctx context.Context, runID int64) error {
	if err := r.db.MarkAIReviewRunCancelled(ctx, runID); err != nil {
		return err
	}
	r.mu.Lock()
	cancel, ok := r.reviewRunsRunning[runID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (r *Runner) runReviewRun(ctx context.Context, in ReviewRunInput, worktreePath string, cached []string) {
	args := []string{
		"-p", buildReviewRunPrompt(in, cached),
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,Glob,Grep," + reviewRunToolNames,
		"--disallowedTools", "Edit,Write,NotebookEdit,Bash,Agent",
	}
	if in.MCP.Binary != "" {
		cfgPath, cleanup, err := writeMCPConfig(in.MCP)
		if err != nil {
			_ = r.db.MarkAIReviewRunFailed(context.Background(), in.RunID, "write mcp config: "+err.Error(), "")
			return
		}
		defer cleanup()
		args = append(args, "--mcp-config", cfgPath, "--strict-mcp-config")
	}

	cmd := exec.CommandContext(ctx, claudeBin(), args...)
	cmd.Dir = worktreePath
	setPgid(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second

	bg := context.Background() // DB writes must outlive ctx's deadline/cancel
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, "stdout pipe: "+err.Error(), "")
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, "start claude: "+err.Error(), "")
		return
	}
	if err := r.db.MarkAIReviewRunRunning(bg, in.RunID, cmd.Process.Pid, worktreePath); err != nil {
		slog.Warn("mark review run running failed", "run_id", in.RunID, "err", err)
	}

	state := &streamState{Events: []streamEvent{}}
	var summary string
	savedSession := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		text, isResult, dirty := applyStreamMessage(state, line)
		if isResult {
			summary = text
		}
		if !savedSession && state.SessionID != "" {
			savedSession = true
			if err := r.db.SetAIReviewRunSession(bg, in.RunID, state.SessionID); err != nil {
				slog.Warn("save review run session failed", "run_id", in.RunID, "err", err)
			}
		}
		if dirty {
			if raw, err := json.Marshal(state); err == nil {
				_ = r.db.UpdateAIReviewRunStream(bg, in.RunID, string(raw))
			}
		}
	}
	streamErr := scanner.Err()
	waitErr := cmd.Wait()
	raw, _ := json.Marshal(state)

	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, fmt.Sprintf("review timed out after %s", r.reviewRunTimeout), string(raw))
		}
		// Canceled: CancelReviewRun already set the status.
		return
	}
	if waitErr != nil || streamErr != nil {
		msg := fmt.Sprintf("claude exited: %v", waitErr)
		if streamErr != nil {
			msg += "\nstream: " + streamErr.Error()
		}
		if stderrBuf.Len() > 0 {
			msg += "\n" + strings.TrimSpace(stderrBuf.String())
		}
		_ = r.db.MarkAIReviewRunFailed(bg, in.RunID, msg, string(raw))
		return
	}
	if err := r.db.MarkAIReviewRunDone(bg, in.RunID, state.SessionID, summary, string(raw)); err != nil {
		slog.Warn("mark review run done failed", "run_id", in.RunID, "err", err)
	}
}

func buildReviewRunPrompt(in ReviewRunInput, cached []string) string {
	var b strings.Builder
	b.WriteString(reviewRunPromptHeader)
	b.WriteString("\n\n")
	if in.PromptContext != "" {
		b.WriteString("Context:\n")
		b.WriteString(in.PromptContext)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Head SHA: %s\nMerge base SHA: %s\n", in.HeadSHA, in.MergeBaseSHA)
	if strings.TrimSpace(in.Instructions) != "" {
		b.WriteString("\nThe reviewer's instructions for this review:\n")
		b.WriteString(strings.TrimSpace(in.Instructions))
		b.WriteString("\n")
	}
	b.WriteString(commitCacheNotice(cached))
	return b.String()
}

const reviewRunPromptHeader = `You are reviewing a pull request on behalf of a maintainer. You do not post anything: every finding you report becomes a suggestion the maintainer reads, discusses with you, edits, and decides whether to publish.

How to work:
- The working copy is checked out at the PR head. Read files with Read/Glob/Grep. Bash is not available. Per-commit ` + "`git show`" + ` output is cached as described below.
- First call mcp__middleman__list_drafts to see comments the maintainer already has, so you do not duplicate them.
- Call mcp__middleman__propose_comment once per finding:
  - path: repo-relative file path changed in this PR.
  - side: RIGHT for a line in the new version (added or unchanged context), LEFT for a deleted line.
  - line: the line number on that side; start_line for a multi-line range on the same side.
  - The line must be inside the PR's diff. If the tool rejects the anchor, it lists the valid ranges; pick one and call again.
  - body: the exact comment text to post on the PR, addressed to the author, in Markdown. Be specific and concise.
  - rationale: your reasoning for the maintainer (not posted): why this matters and how confident you are.
- Prefer a few high-value findings over many nits, unless the instructions say otherwise.
- When you are done, reply with a 2-4 sentence summary of the review.`
