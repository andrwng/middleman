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
	"os"
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

	briefPromptFile string

	mu            sync.Mutex
	running       map[int64]context.CancelFunc // questionID -> cancel
	briefsRunning map[int64]context.CancelFunc // briefID -> cancel
}

// RunnerConfig holds dependencies for a Runner.
type RunnerConfig struct {
	DB          *db.DB
	Clones      *gitclone.Manager
	WorktreeDir string
	HostFor     func(owner, name string) string
	// BriefPromptFile, when non-empty, is a filesystem path that
	// middleman reads on every brief generation to use as the prompt
	// template (replacing the built-in one). The file may contain
	// the literal token {{CONTEXT}} where the per-PR context block
	// (title, branches, commit log) should be interpolated; if the
	// token is absent, context is appended to the end of the file.
	// Read errors fall back to the built-in prompt with a log.
	BriefPromptFile string
}

func New(cfg RunnerConfig) *Runner {
	return &Runner{
		db:              cfg.DB,
		clones:          cfg.Clones,
		rootDir:         cfg.WorktreeDir,
		hostFor:         cfg.HostFor,
		briefPromptFile: cfg.BriefPromptFile,
		running:         make(map[int64]context.CancelFunc),
		briefsRunning:   make(map[int64]context.CancelFunc),
	}
}

// ReconcileOnStartup marks any leftover queued/running questions
// and briefs as failed. Their subprocesses didn't survive a restart.
func (r *Runner) ReconcileOnStartup(ctx context.Context) error {
	orphanQs, err := r.db.GetRunningAIQuestions(ctx)
	if err != nil {
		return fmt.Errorf("list running questions: %w", err)
	}
	for _, q := range orphanQs {
		_ = r.db.MarkAIQuestionFailed(ctx, q.ID, "interrupted by middleman restart")
	}
	orphanBriefs, err := r.db.GetRunningAIBriefs(ctx)
	if err != nil {
		return fmt.Errorf("list running briefs: %w", err)
	}
	for _, b := range orphanBriefs {
		_ = r.db.MarkAIBriefFailed(ctx, b.ID, "interrupted by middleman restart")
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
	if in.HunkStartLine != nil && in.HunkEndLine != nil && *in.HunkStartLine != *in.HunkEndLine {
		b.WriteString(fmt.Sprintf("Anchored lines: %d-%d (%s side)\n", *in.HunkStartLine, *in.HunkEndLine, in.AnchorSide))
	} else {
		b.WriteString(fmt.Sprintf("Anchored line: %d (%s side)\n", in.AnchorLine, in.AnchorSide))
	}
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

// --- PR-level "brief" ---

// BriefInput describes what the brief should be generated against.
// The caller is responsible for passing the current PR head SHA so
// the brief row is keyed correctly; regeneration after a push uses
// a fresh head_sha and therefore a new row.
type BriefInput struct {
	MergeRequestID int64
	Owner          string
	Name           string
	HeadSHA        string
	Depth          string // "quick" or "deep"
	// PromptContext gets appended to the prompt — the PR title/
	// branches/body so Claude has some framing beyond the diff.
	PromptContext string
}

// CreateBrief queues a new brief, spawns Claude asynchronously, and
// returns the inserted row. The row transitions to running → done
// (or failed) in the background. Callers poll GetAIBrief to see
// progress.
func (r *Runner) CreateBrief(ctx context.Context, in BriefInput) (db.AIBrief, error) {
	if r.clones == nil {
		return db.AIBrief{}, errors.New("clone manager not configured")
	}
	if in.HeadSHA == "" {
		return db.AIBrief{}, errors.New("head SHA required")
	}
	depth := in.Depth
	if depth != "quick" && depth != "deep" {
		depth = "quick"
	}

	brief, err := r.db.UpsertAIBriefQueued(ctx, in.MergeRequestID, in.HeadSHA, depth)
	if err != nil {
		return db.AIBrief{}, err
	}

	worktree, err := r.provisionWorktree(ctx, in.Owner, in.Name, in.HeadSHA, briefWorktreeKey(brief.ID))
	if err != nil {
		_ = r.db.MarkAIBriefFailed(ctx, brief.ID, "provision worktree: "+err.Error())
		return db.AIBrief{}, fmt.Errorf("provision worktree: %w", err)
	}

	r.spawnBrief(brief, worktree, in)
	brief.WorktreePath = &worktree
	return brief, nil
}

func (r *Runner) CancelBrief(ctx context.Context, id int64) error {
	r.mu.Lock()
	cancel, ok := r.briefsRunning[id]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return r.db.MarkAIBriefCancelled(ctx, id)
}

func (r *Runner) DeleteBrief(ctx context.Context, id int64) error {
	// Cancel any in-flight subprocess first.
	_ = r.CancelBrief(ctx, id)
	// Remove the worktree best-effort.
	brief, err := r.db.GetAIBrief(ctx, id)
	if err == nil && brief.WorktreePath != nil && *brief.WorktreePath != "" {
		r.removeWorktree(ctx, "", "", *brief.WorktreePath)
	}
	return r.db.DeleteAIBrief(ctx, id)
}

// briefWorktreeKey keeps brief worktrees in a distinct subdirectory
// so they don't collide with thread worktrees (which key on thread
// id only).
func briefWorktreeKey(briefID int64) int64 {
	// Offset by 1e9 to ensure no collision with thread IDs when they
	// share a common root. Simple scheme; DB IDs on a personal
	// middleman instance will never approach that range.
	return 1_000_000_000 + briefID
}

func (r *Runner) spawnBrief(brief db.AIBrief, worktreePath string, in BriefInput) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.briefsRunning[brief.ID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.briefsRunning, brief.ID)
			r.mu.Unlock()
			cancel()
		}()
		r.runBrief(ctx, brief, worktreePath, in)
	}()
}

func (r *Runner) runBrief(ctx context.Context, brief db.AIBrief, worktreePath string, in BriefInput) {
	prompt := r.briefPrompt(in)

	// Brief uses the same restricted tool set as Q&A. Deep mode gets
	// the same tools — the difference is time budget / prompt wording,
	// not tool access.
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "Read,Glob,Grep",
		"--disallowedTools", "Edit,Write,NotebookEdit,Bash,Agent",
	}

	cmd := exec.CommandContext(ctx, claudeBinary, args...)
	cmd.Dir = worktreePath
	setPgid(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = r.db.MarkAIBriefFailed(ctx, brief.ID, "stdout pipe: "+err.Error())
		return
	}
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = r.db.MarkAIBriefFailed(ctx, brief.ID, "start claude: "+err.Error())
		return
	}
	if err := r.db.MarkAIBriefRunning(ctx, brief.ID, cmd.Process.Pid, "", worktreePath); err != nil {
		slog.Warn("mark brief running failed", "brief_id", brief.ID, "err", err)
	}

	raw, readErr := readAll(stdout)
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
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
		_ = r.db.MarkAIBriefFailed(ctx, brief.ID, msg)
		return
	}

	result, err := parseClaudeResult(raw)
	if err != nil {
		_ = r.db.MarkAIBriefFailed(ctx, brief.ID, fmt.Sprintf("parse claude output: %v\noutput: %s", err, snippet(raw, 400)))
		return
	}
	if err := r.db.MarkAIBriefDone(ctx, brief.ID, result.Text); err != nil {
		slog.Warn("mark brief done failed", "brief_id", brief.ID, "err", err)
	}
}

// briefPrompt returns the prompt to send Claude for this brief. If a
// BriefPromptFile is configured and readable, its contents are used
// as the prompt template: {{CONTEXT}} (if present) gets replaced
// with the per-PR context block, otherwise context is appended. On
// any read error we log and fall back to the built-in prompt so the
// brief still runs.
func (r *Runner) briefPrompt(in BriefInput) string {
	context := briefContextBlock(in)
	if r.briefPromptFile != "" {
		data, err := os.ReadFile(r.briefPromptFile)
		if err == nil {
			template := string(data)
			if strings.Contains(template, "{{CONTEXT}}") {
				return strings.ReplaceAll(template, "{{CONTEXT}}", context)
			}
			return strings.TrimRight(template, "\n") + "\n\n" + context
		}
		slog.Warn("read brief prompt override failed; using built-in",
			"path", r.briefPromptFile, "err", err)
	}
	return buildBriefPrompt(in)
}

// briefContextBlock renders just the dynamic per-PR context that
// buildBriefPrompt appends to the static rules section. Kept separate
// so user-supplied templates can interpolate it via {{CONTEXT}}.
func briefContextBlock(in BriefInput) string {
	var b strings.Builder
	if in.PromptContext != "" {
		b.WriteString("Context:\n")
		b.WriteString(in.PromptContext)
		b.WriteString("\n\n")
	}
	b.WriteString(fmt.Sprintf("Head SHA: %s\n", in.HeadSHA))
	if in.Depth == "deep" {
		b.WriteString("Depth: deep. Explore the repo to understand the before-state and cross-references. Use Read/Glob/Grep liberally.\n")
	} else {
		b.WriteString("Depth: quick. Do not explore the repo deeply. Use the diff and commit log; one or two Grep/Read calls to disambiguate are fine.\n")
	}
	return b.String()
}

// buildBriefPrompt assembles the structured-output prompt sent to
// Claude. The strict Markdown structure lets the UI parse sections
// deterministically.
func buildBriefPrompt(in BriefInput) string {
	var b strings.Builder
	b.WriteString(briefPromptHeader)
	b.WriteString("\n\n")
	b.WriteString(briefContextBlock(in))
	b.WriteString(
		"\nBash is not available. Read files directly from the working copy — it is checked out at the PR head. " +
			"The diff and commit log are pre-computed and included in the Context block above.\n",
	)
	return b.String()
}

// briefPromptHeader is the static rules portion of the prompt. Split
// out so a user's override file can copy/paste this verbatim as a
// starting point if they want to tweak only the rules.
const briefPromptHeader = `You are generating a structural review brief for a pull request. Read the repository files with your Read/Glob/Grep tools as needed for context. You MUST produce output in the following Markdown structure exactly, with those section headings verbatim:

## Intent
1–2 sentences naming what this PR actually does, based on the code (not the PR title).

## Before
How the code was organised or how it flowed before this PR. Prose or bullets. For a pure addition write: "Skipped: pure addition."

## After
How the code is organised or how it flows after this PR.

## Commits
For each commit in the PR, in order (oldest first), one bullet:
- ` + "`<sha>`" + ` **<one-line title>**
  - 1–3 bullets describing what this commit does, with file:line citations.
  - Suggested read depth: ` + "`read carefully`, `skim`, or `skip if trusted`" + `.

## Observations
Neutral observations with file:line citations. Phrase concerns as questions, never as verdicts or recommendations.

Rules:
- Every non-trivial claim MUST cite a file:line (e.g. ` + "`foo.go:42`" + `).
- Never produce review feedback. No 'should', no approvals, no verdicts.
- Hedge when uncertain ("appears to", "seems to").
- Keep prose tight. Bullets over paragraphs where it fits.`


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
