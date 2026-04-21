// Package aireview runs Claude Code as a subprocess to answer reviewer
// questions about PR diffs. Each thread owns a disposable git worktree
// and a persistent Claude session (via --resume) so follow-ups reuse
// Claude's conversation state. Thread lifecycle:
//
//   CreateThread → worktree add at commit SHA → spawn Claude for Q1
//                                             → capture session_id
//   AddFollowUp  → spawn Claude with --resume session_id for Qn
//   CloseThread  → cancel any running Qs → worktree remove
//
// Questions run in their own goroutine with a cancellable context;
// CancelQuestion cancels the ctx which terminates the subprocess
// (exec.CommandContext guarantees that on cancellation).
package aireview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
)

// claudeBinary is the executable name used to spawn Claude Code. Kept
// as a variable so tests can swap in a fake.
var claudeBinary = "claude"

// SetBinaryForTest overrides the claude executable path. Returns the
// previous value. Intended for test packages that need to stub out
// the real CLI.
func SetBinaryForTest(path string) string {
	old := claudeBinary
	claudeBinary = path
	return old
}

// Runner owns the lifecycle of all active AI Q&A threads for this
// middleman process. It is safe for concurrent use.
type Runner struct {
	db      *db.DB
	clones  *gitclone.Manager
	rootDir string

	// hostFor resolves (owner, name) to a platform host so we can
	// find the right clone directory. Matches the signature exposed
	// by the sync engine.
	hostFor func(owner, name string) string

	mu      sync.Mutex
	running map[int64]context.CancelFunc // questionID -> cancel
}

// RunnerConfig holds dependencies for a Runner.
type RunnerConfig struct {
	DB          *db.DB
	Clones      *gitclone.Manager
	WorktreeDir string
	HostFor     func(owner, name string) string
}

func New(cfg RunnerConfig) *Runner {
	return &Runner{
		db:      cfg.DB,
		clones:  cfg.Clones,
		rootDir: cfg.WorktreeDir,
		hostFor: cfg.HostFor,
		running: make(map[int64]context.CancelFunc),
	}
}

// ReconcileOnStartup marks any leftover queued/running questions as
// failed. Their subprocesses didn't survive a restart.
func (r *Runner) ReconcileOnStartup(ctx context.Context) error {
	orphans, err := r.db.GetRunningAIQuestions(ctx)
	if err != nil {
		return fmt.Errorf("list running questions: %w", err)
	}
	for _, q := range orphans {
		_ = r.db.MarkAIQuestionFailed(ctx, q.ID, "interrupted by middleman restart")
	}
	return nil
}

// CreateThreadInput describes a new thread anchor and its first
// question. Owner/Name identify the repo for worktree provisioning;
// MergeRequestID ties the thread to its PR.
type CreateThreadInput struct {
	MergeRequestID int64
	Owner          string
	Name           string
	Path           string
	AnchorSide     string
	AnchorLine     int
	HunkStartLine  *int
	HunkEndLine    *int
	HunkText       string // included verbatim in the prompt
	SelectionText  *string
	CommitSHA      string
	Question       string
	// PromptContext is free-form text appended to the prompt to give
	// Claude additional orientation (PR title, branch, etc.). Kept
	// separate so the caller controls what goes in.
	PromptContext string
}

func (r *Runner) CreateThread(ctx context.Context, in CreateThreadInput) (db.AIThread, db.AIQuestion, error) {
	if r.clones == nil {
		return db.AIThread{}, db.AIQuestion{}, errors.New("clone manager not configured")
	}
	if in.CommitSHA == "" {
		return db.AIThread{}, db.AIQuestion{}, errors.New("commit SHA required")
	}

	thread, question, err := r.db.CreateAIThread(ctx, db.NewAIThreadInput{
		MergeRequestID: in.MergeRequestID,
		Path:           in.Path,
		AnchorSide:     in.AnchorSide,
		AnchorLine:     in.AnchorLine,
		HunkStartLine:  in.HunkStartLine,
		HunkEndLine:    in.HunkEndLine,
		SelectionText:  in.SelectionText,
		CommitSHA:      in.CommitSHA,
		Question:       in.Question,
	})
	if err != nil {
		return db.AIThread{}, db.AIQuestion{}, err
	}

	worktree, err := r.provisionWorktree(ctx, in.Owner, in.Name, in.CommitSHA, thread.ID)
	if err != nil {
		_ = r.db.DeleteAIThread(ctx, thread.ID)
		return db.AIThread{}, db.AIQuestion{}, fmt.Errorf("provision worktree: %w", err)
	}
	if err := r.db.UpdateAIThreadSession(ctx, thread.ID, "", worktree); err != nil {
		r.removeWorktree(ctx, in.Owner, in.Name, worktree)
		_ = r.db.DeleteAIThread(ctx, thread.ID)
		return db.AIThread{}, db.AIQuestion{}, err
	}
	thread.WorktreePath = &worktree

	r.spawnQuestion(thread, question, buildPrompt(in, in.Question))
	return thread, question, nil
}

// AddFollowUp enqueues another question in the thread, using Claude's
// --resume so the model keeps tool-result context from earlier turns.
func (r *Runner) AddFollowUp(ctx context.Context, threadID int64, question string) (db.AIQuestion, error) {
	thread, err := r.db.GetAIThread(ctx, threadID)
	if err != nil {
		return db.AIQuestion{}, err
	}
	if thread.Status != "active" {
		return db.AIQuestion{}, fmt.Errorf("thread is %s", thread.Status)
	}
	if thread.WorktreePath == nil {
		return db.AIQuestion{}, errors.New("thread has no worktree")
	}

	q, err := r.db.AddAIQuestion(ctx, threadID, question)
	if err != nil {
		return db.AIQuestion{}, err
	}

	// Follow-ups only send the question itself — the session carries
	// hunk/selection context from the first message.
	r.spawnQuestion(thread, q, question)
	return q, nil
}

// CancelQuestion stops an in-flight subprocess and marks the question
// cancelled. Idempotent: a no-op if the question already completed.
func (r *Runner) CancelQuestion(ctx context.Context, questionID int64) error {
	r.mu.Lock()
	cancel, ok := r.running[questionID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return r.db.MarkAIQuestionCancelled(ctx, questionID)
}

// CloseThread cancels any in-flight questions, removes the worktree,
// and marks the thread closed.
func (r *Runner) CloseThread(ctx context.Context, threadID int64) error {
	thread, err := r.db.GetAIThread(ctx, threadID)
	if err != nil {
		return err
	}

	questions, err := r.db.ListAIQuestionsForThread(ctx, threadID)
	if err != nil {
		return err
	}
	for _, q := range questions {
		if q.Status == "queued" || q.Status == "running" {
			_ = r.CancelQuestion(ctx, q.ID)
		}
	}

	if thread.WorktreePath != nil && *thread.WorktreePath != "" {
		r.removeWorktree(ctx, "", "", *thread.WorktreePath)
	}
	return r.db.CloseAIThread(ctx, threadID)
}

// DeleteThread closes and then removes the thread entirely from the DB.
func (r *Runner) DeleteThread(ctx context.Context, threadID int64) error {
	if err := r.CloseThread(ctx, threadID); err != nil {
		// proceed — we still want to remove the row if possible
		slog.Warn("close thread failed during delete", "thread_id", threadID, "err", err)
	}
	return r.db.DeleteAIThread(ctx, threadID)
}

// --- internals ---

func (r *Runner) spawnQuestion(thread db.AIThread, question db.AIQuestion, prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.running[question.ID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, question.ID)
			r.mu.Unlock()
			cancel()
		}()
		r.runQuestion(ctx, thread, question, prompt)
	}()
}

func (r *Runner) runQuestion(ctx context.Context, thread db.AIThread, question db.AIQuestion, prompt string) {
	if thread.WorktreePath == nil || *thread.WorktreePath == "" {
		_ = r.db.MarkAIQuestionFailed(ctx, question.ID, "thread has no worktree")
		return
	}

	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,Glob,Grep",
		"--disallowedTools", "Edit,Write,NotebookEdit,Bash,Agent",
	}
	if thread.ClaudeSessionID != nil && *thread.ClaudeSessionID != "" {
		args = append(args, "--resume", *thread.ClaudeSessionID)
	}

	cmd := exec.CommandContext(ctx, claudeBinary, args...)
	cmd.Dir = *thread.WorktreePath
	setPgid(cmd) // so we can kill the whole process group

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.db.MarkAIQuestionFailed(ctx, question.ID, "stdout pipe: "+err.Error())
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = r.db.MarkAIQuestionFailed(ctx, question.ID, "start claude: "+err.Error())
		return
	}
	if err := r.db.MarkAIQuestionRunning(ctx, question.ID, cmd.Process.Pid); err != nil {
		slog.Warn("mark running failed", "question_id", question.ID, "err", err)
	}

	// Parse JSON result. Claude --output-format json emits a single
	// JSON object at end-of-run. We read the whole stream, then parse.
	raw, readErr := readAll(stdout)
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		// Cancelled: the DB was (or will be) marked cancelled by
		// CancelQuestion. Don't overwrite.
		return
	}
	if waitErr != nil {
		msg := fmt.Sprintf("claude exited: %v", waitErr)
		if stderrBuf.Len() > 0 {
			msg += "\n" + strings.TrimSpace(stderrBuf.String())
		}
		if readErr != nil {
			msg += "\nread: " + readErr.Error()
		}
		_ = r.db.MarkAIQuestionFailed(ctx, question.ID, msg)
		return
	}

	result, err := parseClaudeResult(raw)
	if err != nil {
		_ = r.db.MarkAIQuestionFailed(ctx, question.ID, fmt.Sprintf("parse claude output: %v\noutput: %s", err, snippet(raw, 400)))
		return
	}

	// First question in a thread: capture session ID for future resume.
	if thread.ClaudeSessionID == nil || *thread.ClaudeSessionID == "" {
		if result.SessionID != "" {
			if err := r.db.UpdateAIThreadSession(ctx, thread.ID, result.SessionID, *thread.WorktreePath); err != nil {
				slog.Warn("save session id failed", "thread_id", thread.ID, "err", err)
			}
		}
	}

	citationsJSON, _ := json.Marshal(result.Citations)
	if err := r.db.MarkAIQuestionDone(ctx, question.ID, result.Text, string(citationsJSON)); err != nil {
		slog.Warn("mark done failed", "question_id", question.ID, "err", err)
	}
}

// provisionWorktree creates a detached worktree at commitSHA under
// r.rootDir/<thread-id>/. Returns the absolute worktree path.
func (r *Runner) provisionWorktree(ctx context.Context, owner, name, commitSHA string, threadID int64) (string, error) {
	if r.hostFor == nil {
		return "", errors.New("host resolver not configured")
	}
	host := r.hostFor(owner, name)
	if host == "" {
		host = "github.com"
	}
	cloneDir := r.clones.ClonePath(host, owner, name)
	worktreePath := filepath.Join(r.rootDir, strconv.FormatInt(threadID, 10))

	out, err := exec.CommandContext(ctx, "git", "-C", cloneDir,
		"worktree", "add", "--detach", worktreePath, commitSHA).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return worktreePath, nil
}

// removeWorktree does a best-effort cleanup. Errors are logged but not
// surfaced — a dangling worktree is preferable to leaving a thread
// stuck open.
func (r *Runner) removeWorktree(ctx context.Context, owner, name, worktreePath string) {
	// We can call `git worktree remove` from within the worktree
	// itself; the clone dir is parent-linked.
	out, err := exec.CommandContext(ctx, "git", "-C", worktreePath,
		"worktree", "remove", "--force", worktreePath).CombinedOutput()
	if err != nil {
		slog.Warn("git worktree remove failed",
			"path", worktreePath, "err", err, "output", strings.TrimSpace(string(out)))
	}
}

// --- prompt building ---

// buildPrompt assembles the first-question prompt. Follow-ups reuse
// the Claude session and only need the new question text, but the
// first message primes the model with PR location + selected code.
func buildPrompt(in CreateThreadInput, question string) string {
	var b strings.Builder
	b.WriteString(
		"You are a code review assistant. A reviewer has asked a question about a pull request. " +
			"Answer using the repository available in the current working directory. " +
			"Cite file paths and line numbers when referencing specific code. " +
			"Do not produce review feedback of your own — answer exactly what was asked, " +
			"scoped to where it was asked. " +
			"Respond in concise Markdown.\n\n",
	)
	if in.PromptContext != "" {
		b.WriteString(in.PromptContext)
		b.WriteString("\n\n")
	}
	b.WriteString(fmt.Sprintf("File: %s\n", in.Path))
	b.WriteString(fmt.Sprintf("Anchored line: %d (%s side)\n", in.AnchorLine, in.AnchorSide))
	b.WriteString(fmt.Sprintf("Commit SHA: %s\n", in.CommitSHA))
	if in.HunkText != "" {
		b.WriteString("\nHunk:\n```diff\n")
		b.WriteString(in.HunkText)
		b.WriteString("\n```\n")
	}
	if in.SelectionText != nil && *in.SelectionText != "" {
		b.WriteString("\nSelected code:\n```\n")
		b.WriteString(*in.SelectionText)
		b.WriteString("\n```\n")
	}
	b.WriteString("\nQuestion:\n")
	b.WriteString(question)
	return b.String()
}

// --- claude json parsing ---

// claudeResult is the subset of claude -p --output-format json we care
// about. The CLI emits other fields (cost, durations, etc.) that we
// currently ignore.
type claudeResult struct {
	SessionID string
	Text      string
	// Citations is always empty from the CLI — we keep the field so
	// the DB column has consistent shape for future structured
	// extraction.
	Citations []Citation
}

// Citation is a structured reference the UI can turn into a link.
// Currently unused from the model output; reserved for future
// extraction (e.g. parsing `path:line` patterns out of the answer).
type Citation struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

func parseClaudeResult(raw []byte) (claudeResult, error) {
	// Claude CLI emits a single JSON object with top-level fields
	// including "result" (text), "session_id", and "is_error".
	var v struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return claudeResult{}, err
	}
	if v.IsError {
		return claudeResult{}, fmt.Errorf("claude reported error (type=%s subtype=%s): %s", v.Type, v.Subtype, v.Result)
	}
	return claudeResult{
		SessionID: v.SessionID,
		Text:      v.Result,
	}, nil
}
