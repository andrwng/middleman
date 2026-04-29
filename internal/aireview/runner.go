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
	"time"

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
	// PRMergeBaseSHA / PRHeadSHA bound the PR's commit range so the
	// runner can pre-cache `git show` for each commit into a per-
	// worktree directory Claude can Read on demand. Without these,
	// the cache is skipped (Claude still has the anchored hunk).
	PRMergeBaseSHA string
	PRHeadSHA      string
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

	cachedShas := r.cachePRCommits(ctx, in.Owner, in.Name, worktree, in.PRMergeBaseSHA, in.PRHeadSHA)

	r.spawnQuestion(thread, question, buildPrompt(in, in.Question, cachedShas))
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
// The final DB delete runs on a detached context so a slow worktree
// removal (or a client disconnect mid-request) can't leave the row
// orphaned — it's the orphaned row that makes the card "come back"
// on refresh.
func (r *Runner) DeleteThread(ctx context.Context, threadID int64) error {
	if err := r.CloseThread(ctx, threadID); err != nil {
		slog.Warn("close thread failed during delete", "thread_id", threadID, "err", err)
	}
	delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return r.db.DeleteAIThread(delCtx, threadID)
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
// r.rootDir/<thread-id>/. Returns the absolute worktree path. If a
// previous worktree exists at the same path (e.g. SQLite reused the
// primary key after a regenerate), we tear it down first so
// `git worktree add` doesn't refuse.
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

	r.cleanWorktreePath(ctx, cloneDir, worktreePath)

	out, err := exec.CommandContext(ctx, "git", "-C", cloneDir,
		"worktree", "add", "--detach", worktreePath, commitSHA).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return worktreePath, nil
}

// cleanWorktreePath ensures worktreePath doesn't exist on disk and
// isn't tracked in the clone's worktree list. Safe to call when
// nothing exists there. Best-effort: any individual step that fails
// is logged and we move on.
func (r *Runner) cleanWorktreePath(ctx context.Context, cloneDir, worktreePath string) {
	// If the path doesn't exist, nothing to do.
	if _, err := os.Stat(worktreePath); err != nil {
		// Still prune dangling metadata in case `.git/worktrees/...`
		// is orphaned after a previous interrupted removal.
		_ = exec.CommandContext(ctx, "git", "-C", cloneDir, "worktree", "prune").Run()
		return
	}
	// Preferred: let git remove it cleanly.
	if out, err := exec.CommandContext(ctx, "git", "-C", cloneDir,
		"worktree", "remove", "--force", worktreePath).CombinedOutput(); err != nil {
		slog.Warn("git worktree remove during cleanup failed; falling back to rm -rf",
			"path", worktreePath, "err", err, "output", strings.TrimSpace(string(out)))
	}
	// If git didn't do the job (permissions, half-cleaned state),
	// nuke the directory from disk.
	if err := os.RemoveAll(worktreePath); err != nil {
		slog.Warn("rm -rf worktree failed", "path", worktreePath, "err", err)
	}
	// Prune git's internal metadata. Idempotent and fast.
	_ = exec.CommandContext(ctx, "git", "-C", cloneDir, "worktree", "prune").Run()
}

// commitCacheDirName lives inside the worktree as a sibling to the
// checked-out files. Claude reads files from there with the Read
// tool; a leading dot keeps it tucked away from casual greps over
// the working tree. The directory is ephemeral — it goes away with
// the worktree at thread/brief teardown.
const commitCacheDirName = ".middleman-commits"

// cachePRCommits writes one file per first-parent commit in
// mergeBase..head to <worktree>/.middleman-commits/<sha>.diff so
// Claude can pull commit context (message + diff) on demand without
// Bash. Best-effort: a failure to enumerate commits or write any
// individual file is logged and the cache silently skips that SHA;
// the worst case is "Claude doesn't have that commit's details" not
// a hard failure of thread creation.
//
// Returns the list of cached SHAs (oldest-first) so callers can
// surface them in the prompt.
func (r *Runner) cachePRCommits(
	ctx context.Context, owner, name, worktreePath, mergeBaseSHA, headSHA string,
) []string {
	if mergeBaseSHA == "" || headSHA == "" || mergeBaseSHA == headSHA {
		return nil
	}
	if r.hostFor == nil {
		return nil
	}
	host := r.hostFor(owner, name)
	if host == "" {
		host = "github.com"
	}
	cloneDir := r.clones.ClonePath(host, owner, name)
	out, err := exec.CommandContext(ctx, "git", "-C", cloneDir,
		"log", "--first-parent", "--reverse", "--format=%H",
		mergeBaseSHA+".."+headSHA,
	).Output()
	if err != nil {
		slog.Warn("commit cache: list commits failed",
			"owner", owner, "name", name, "merge_base", mergeBaseSHA, "head", headSHA, "err", err)
		return nil
	}
	shas := strings.Fields(strings.TrimSpace(string(out)))
	if len(shas) == 0 {
		return nil
	}
	cacheDir := filepath.Join(worktreePath, commitCacheDirName)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		slog.Warn("commit cache: mkdir failed", "path", cacheDir, "err", err)
		return nil
	}
	cached := make([]string, 0, len(shas))
	for _, sha := range shas {
		body, err := exec.CommandContext(ctx, "git", "-C", cloneDir,
			"show", "--format=fuller", "--no-color", sha,
		).Output()
		if err != nil {
			slog.Warn("commit cache: git show failed", "sha", sha, "err", err)
			continue
		}
		if err := os.WriteFile(filepath.Join(cacheDir, sha+".diff"), body, 0o644); err != nil {
			slog.Warn("commit cache: write failed", "sha", sha, "err", err)
			continue
		}
		cached = append(cached, sha)
	}
	return cached
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
func buildPrompt(in CreateThreadInput, question string, cachedCommitShas []string) string {
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
	b.WriteString(commitCacheNotice(cachedCommitShas))
	b.WriteString("\nQuestion:\n")
	b.WriteString(question)
	return b.String()
}

// commitCacheNotice tells Claude where the per-commit cache lives
// and what's in it. Returns "" when nothing was cached so the prompt
// doesn't lie about resources that aren't there.
func commitCacheNotice(cachedShas []string) string {
	if len(cachedShas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nPer-commit context:\n")
	b.WriteString("Each first-parent commit in this PR has a cached `git show --format=fuller` ")
	b.WriteString("output at `")
	b.WriteString(commitCacheDirName)
	b.WriteString("/<full-commit-sha>.diff` in the working directory. ")
	b.WriteString("Read those files (with the Read tool) when you need a specific commit's ")
	b.WriteString("message or its line-level changes — don't guess at history when you can ")
	b.WriteString("read the actual commit. Cached SHAs (oldest first):\n")
	for _, sha := range cachedShas {
		b.WriteString("  - ")
		b.WriteString(sha)
		b.WriteString("\n")
	}
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
	// MergeBaseSHA bounds the PR's commit range for the per-commit
	// cache. When empty the cache is skipped (the brief still has the
	// commit log + author description in PromptContext).
	MergeBaseSHA string
	Depth        string // "quick" or "deep"
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

	cachedShas := r.cachePRCommits(ctx, in.Owner, in.Name, worktree, in.MergeBaseSHA, in.HeadSHA)

	r.spawnBrief(brief, worktree, in, cachedShas)
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

func (r *Runner) spawnBrief(brief db.AIBrief, worktreePath string, in BriefInput, cachedShas []string) {
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
		r.runBrief(ctx, brief, worktreePath, in, cachedShas)
	}()
}

func (r *Runner) runBrief(ctx context.Context, brief db.AIBrief, worktreePath string, in BriefInput, cachedShas []string) {
	prompt := r.briefPrompt(in, cachedShas)

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
func (r *Runner) briefPrompt(in BriefInput, cachedShas []string) string {
	context := briefContextBlock(in) + commitCacheNotice(cachedShas)
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
	return buildBriefPrompt(in, cachedShas)
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
func buildBriefPrompt(in BriefInput, cachedShas []string) string {
	var b strings.Builder
	b.WriteString(briefPromptHeader)
	b.WriteString("\n\n")
	b.WriteString(briefContextBlock(in))
	b.WriteString(commitCacheNotice(cachedShas))
	b.WriteString(
		"\nBash is not available. Read files directly from the working copy — it is checked out at the PR head. " +
			"The PR-wide commit log is in the Context block above; per-commit `git show` output is in the cache " +
			"directory described above when you need it.\n",
	)
	return b.String()
}

// briefPromptHeader is the static rules portion of the prompt. Split
// out so a user's override file can copy/paste this verbatim as a
// starting point if they want to tweak only the rules.
const briefPromptHeader = `You are generating a structural review brief for a staff engineer who will dive into the PR's diff immediately after reading this. They have breadth across the codebase and decent depth in many areas, but for hairier subsystems they need their cognitive cache primed first. They learn visually and from high-level pseudocode.

The brief has two jobs:
- **Map** — a compressed view of what's coming so the reviewer can navigate (Intent, Before, After, Commits).
- **Compass** — the orientation the diff itself can't easily give (Subsystem, Mechanics when non-trivial, Risk surface, Open questions).

Both matter. Be terse but complete. Read repository files with Read/Glob/Grep as needed; per-commit ` + "`git show`" + ` output is cached in ` + "`.middleman-commits/<sha>.diff`" + ` — use it.

Output the following Markdown sections, in this order, with headings verbatim. **Skip a section entirely (no heading, no placeholder) when its content would be empty or trivial for this PR.**

## Subsystem
≤ 4 lines. For each affected subsystem: a phrase saying what it does, where it sits in the architecture, and the 1–2 invariants/protocols a reviewer needs to hold in mind. Cite the entrypoint file:line. Skip when the touched code is a leaf utility with no useful subsystem context.

## Intent
1–2 sentences. What this PR does, based on the code — not the title.

## Before
≤ 6 lines. The shape of the relevant code before this PR — control flow, data flow, types, or contract. Bullets > prose.

## After
≤ 6 lines. The same shape, after the PR. If the change is structural, include **at most one** small text-based diagram total across Before/After/Mechanics — a few boxes-and-arrows in a fenced code block, or a tiny call-tree with indentation. Skip diagrams entirely for refactors, renames, or mechanical changes; an unhelpful diagram is worse than no diagram. (Mermaid won't render in this UI yet — use plain ASCII.)

## Mechanics
≤ 15 lines of plain-language pseudocode showing the new behavior end-to-end. Skip this section entirely when the PR is a refactor, rename, or otherwise mechanical.

## Commits
One bullet per commit, oldest first:
- ` + "`<sha>`" + ` **<one-line title>** — ` + "`read carefully`" + ` | ` + "`skim`" + ` | ` + "`skip if trusted`" + `
  - ≤ 1 line: what it does and why.
  - When a commit edits tests, name the cause: a behavior change (point at it with file:line), an invariant shift, or scaffolding/flake-fix. Don't accept "updates tests for new behavior" as an answer to yourself.

## Risk surface
≤ 5 bullets, ≤ 12 words each. Concrete failure modes the reviewer should watch for: concurrency races, partial-state on errors, perf cliffs, missed edge cases. Each bullet cites file:line. Skip the section when there is no plausible failure surface.

## Open questions
≤ 3 bullets. Things only the author knows, phrased as questions. Skip when there are none.

Rules:
- Cite file:line for every non-trivial claim (e.g. ` + "`foo.go:42`" + `).
- One fact or one question per bullet. If a bullet contains "and" or a semicolon, split it or cut the weaker half.
- No throat-clearing. No "this PR appears to introduce". State directly.
- Hedge at most once per claim and only when genuinely uncertain ("uncertain whether X"); don't decorate every sentence with "appears to" / "seems to".
- Never produce review feedback. No "should", no approvals, no verdicts.
- Don't restate what an earlier section already covered.
- A short brief is correct, not lazy. Padding is worse than omitting.`


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
