package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/worktrees"
)

// --- shared response shapes -------------------------------------------------

type aiThreadResponse struct {
	ID              int64      `json:"id"`
	MergeRequestID  int64      `json:"mr_id"`
	Path            string     `json:"path"`
	AnchorSide      string     `json:"anchor_side"`
	AnchorLine      int        `json:"anchor_line"`
	HunkStartLine   *int       `json:"hunk_start_line,omitempty"`
	HunkEndLine     *int       `json:"hunk_end_line,omitempty"`
	SelectionText   *string    `json:"selection_text,omitempty"`
	CommitSHA       string     `json:"commit_sha"`
	ClaudeSessionID *string    `json:"claude_session_id,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
}

type aiQuestionResponse struct {
	ID            int64      `json:"id"`
	ThreadID      int64      `json:"thread_id"`
	Question      string     `json:"question"`
	Answer        string     `json:"answer"`
	CitationsJSON string     `json:"citations_json"`
	Error         string     `json:"error,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type aiThreadDetailResponse struct {
	Thread    aiThreadResponse     `json:"thread"`
	Questions []aiQuestionResponse `json:"questions"`
}

func toThreadResponse(t db.AIThread) aiThreadResponse {
	r := aiThreadResponse{
		ID:             t.ID,
		MergeRequestID: t.MergeRequestID,
		Path:           t.Path,
		AnchorSide:     t.AnchorSide,
		AnchorLine:     t.AnchorLine,
		HunkStartLine:  t.HunkStartLine,
		HunkEndLine:    t.HunkEndLine,
		SelectionText:  t.SelectionText,
		CommitSHA:      t.CommitSHA,
		Status:         t.Status,
		CreatedAt:      t.CreatedAt.UTC(),
		ClosedAt:       utcPtr(t.ClosedAt),
	}
	if t.ClaudeSessionID != nil && *t.ClaudeSessionID != "" {
		r.ClaudeSessionID = t.ClaudeSessionID
	}
	return r
}

func toQuestionResponse(q db.AIQuestion) aiQuestionResponse {
	return aiQuestionResponse{
		ID:            q.ID,
		ThreadID:      q.ThreadID,
		Question:      q.Question,
		Answer:        q.Answer,
		CitationsJSON: q.CitationsJSON,
		Error:         q.Error,
		Status:        q.Status,
		CreatedAt:     q.CreatedAt.UTC(),
		StartedAt:     utcPtr(q.StartedAt),
		CompletedAt:   utcPtr(q.CompletedAt),
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

// --- inputs -----------------------------------------------------------------

type createAIThreadInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Path          string  `json:"path"              doc:"File path the question is about"`
		AnchorSide    string  `json:"anchor_side"       doc:"LEFT or RIGHT"`
		AnchorLine    int     `json:"anchor_line"       doc:"1-based line in the file at the anchor SHA"`
		HunkStartLine *int    `json:"hunk_start_line,omitempty" doc:"Optional start of the hunk the reviewer was looking at"`
		HunkEndLine   *int    `json:"hunk_end_line,omitempty"   doc:"Optional end of the hunk"`
		HunkText      string  `json:"hunk_text,omitempty"       doc:"Raw hunk contents, quoted into the prompt"`
		SelectionText *string `json:"selection_text,omitempty"  doc:"Text the reviewer selected"`
		CommitSHA     string  `json:"commit_sha"        doc:"Commit the question is anchored to"`
		Question      string  `json:"question"          doc:"Reviewer's question, free-form natural language"`
		PromptContext string  `json:"prompt_context,omitempty" doc:"Extra orientation text appended to the prompt (PR title, branch, etc.)"`
	}
}

type addAIQuestionInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Question string `json:"question"`
	}
}

type aiThreadPathInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
}

type aiQuestionPathInput struct {
	Owner      string `path:"owner"`
	Name       string `path:"name"`
	Number     int    `path:"number"`
	ThreadID   int64  `path:"thread_id"`
	QuestionID int64  `path:"question_id"`
}

type listAIThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	// SinceID lets the UI poll cheaply — return only questions newer
	// than this. Threads are always returned in full so the UI can
	// reconcile closed-state changes.
	SinceID int64 `query:"since_id"`
}

// --- outputs ----------------------------------------------------------------

type aiThreadCreatedOutput struct {
	Body struct {
		Thread   aiThreadResponse   `json:"thread"`
		Question aiQuestionResponse `json:"question"`
	}
}

type aiQuestionCreatedOutput struct {
	Body aiQuestionResponse
}

type aiThreadsListOutput struct {
	Body struct {
		Threads   []aiThreadResponse   `json:"threads"`
		Questions []aiQuestionResponse `json:"questions"`
	}
}

type aiThreadDetailOutput struct {
	Body aiThreadDetailResponse
}

type emptyOutput struct{}

// --- handlers ---------------------------------------------------------------

func (s *Server) createAIThread(ctx context.Context, input *createAIThreadInput) (*aiThreadCreatedOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available: clone manager or worktree dir not configured")
	}

	if err := validateAIThreadInput(input); err != nil {
		return nil, err
	}

	var (
		mrID              int64
		mergeBaseSHA      string
		headSHA           string
		localWorktreePath string
	)

	if isLocalSource(input.Owner) {
		// Local source: ensure a synthetic MR row exists so the AI
		// thread (and any future PR-anchored data) can FK to it.
		// Pass the worktree path directly so the runner uses it
		// instead of provisioning an ephemeral worktree from a bare
		// clone — we don't have a bare clone for local repos.
		w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
		if err != nil {
			return nil, huma.Error404NotFound("worktree not found")
		}
		id, err := s.ensureSyntheticMRForWorktree(ctx, w)
		if err != nil {
			return nil, huma.Error500InternalServerError("ensure synthetic MR: " + err.Error())
		}
		mrID = id
		localWorktreePath = w.Path
		baseRef := s.lookupBaseRefForWorktree(ctx, *w)
		if base, err := worktrees.ResolveBase(ctx, w.Path, baseRef); err == nil {
			mergeBaseSHA = base.SHA
			headSHA = w.HeadSHA
		}
	} else {
		var err error
		mrID, err = s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
		if err != nil {
			return nil, huma.Error404NotFound("pull request not found")
		}
		// Look up the PR's merge_base / head so the runner can pre-cache
		// per-commit `git show` output for Claude to Read on demand. A
		// missing/empty result just disables the cache; the thread still
		// works.
		if shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number); err == nil && shas != nil {
			mergeBaseSHA = shas.MergeBaseSHA
			headSHA = shas.DiffHeadSHA
		}
	}

	thread, question, err := s.aiReview.CreateThread(ctx, aireview.CreateThreadInput{
		MergeRequestID:    mrID,
		Owner:             input.Owner,
		Name:              input.Name,
		Path:              input.Body.Path,
		AnchorSide:        input.Body.AnchorSide,
		AnchorLine:        input.Body.AnchorLine,
		HunkStartLine:     input.Body.HunkStartLine,
		HunkEndLine:       input.Body.HunkEndLine,
		HunkText:          input.Body.HunkText,
		SelectionText:     input.Body.SelectionText,
		CommitSHA:         input.Body.CommitSHA,
		Question:          input.Body.Question,
		PromptContext:     input.Body.PromptContext,
		PRMergeBaseSHA:    mergeBaseSHA,
		PRHeadSHA:         headSHA,
		LocalWorktreePath: localWorktreePath,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("create thread: " + err.Error())
	}

	out := &aiThreadCreatedOutput{}
	out.Body.Thread = toThreadResponse(thread)
	out.Body.Question = toQuestionResponse(question)
	return out, nil
}

func (s *Server) addAIQuestion(ctx context.Context, input *addAIQuestionInput) (*aiQuestionCreatedOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	if strings.TrimSpace(input.Body.Question) == "" {
		return nil, huma.Error400BadRequest("question is required")
	}

	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}

	q, err := s.aiReview.AddFollowUp(ctx, input.ThreadID, input.Body.Question)
	if err != nil {
		return nil, huma.Error400BadRequest("add follow-up: " + err.Error())
	}
	return &aiQuestionCreatedOutput{Body: toQuestionResponse(q)}, nil
}

func (s *Server) listAIThreads(ctx context.Context, input *listAIThreadsInput) (*aiThreadsListOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	threads, err := s.db.ListAIThreadsForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list threads: " + err.Error())
	}
	questions, err := s.db.ListAIQuestionsForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list questions: " + err.Error())
	}

	out := &aiThreadsListOutput{}
	out.Body.Threads = make([]aiThreadResponse, 0, len(threads))
	for _, t := range threads {
		out.Body.Threads = append(out.Body.Threads, toThreadResponse(t))
	}
	out.Body.Questions = make([]aiQuestionResponse, 0, len(questions))
	for _, q := range questions {
		if q.ID <= input.SinceID {
			continue
		}
		out.Body.Questions = append(out.Body.Questions, toQuestionResponse(q))
	}
	return out, nil
}

func (s *Server) getAIThread(ctx context.Context, input *aiThreadPathInput) (*aiThreadDetailOutput, error) {
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}
	questions, err := s.db.ListAIQuestionsForThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list questions: " + err.Error())
	}
	out := &aiThreadDetailOutput{}
	out.Body.Thread = toThreadResponse(thread)
	out.Body.Questions = make([]aiQuestionResponse, 0, len(questions))
	for _, q := range questions {
		out.Body.Questions = append(out.Body.Questions, toQuestionResponse(q))
	}
	return out, nil
}

func (s *Server) deleteAIThread(ctx context.Context, input *aiThreadPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}
	if err := s.aiReview.DeleteThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("delete thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}

func (s *Server) deleteAIQuestion(ctx context.Context, input *aiQuestionPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	q, err := s.db.GetAIQuestion(ctx, input.QuestionID)
	if err != nil {
		return nil, huma.Error404NotFound("question not found")
	}
	if q.ThreadID != input.ThreadID {
		return nil, huma.Error404NotFound("question not in thread")
	}
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}

	// If still in-flight, cancel before deleting.
	if q.Status == "running" || q.Status == "queued" {
		_ = s.aiReview.CancelQuestion(ctx, q.ID)
	}
	if err := s.db.DeleteAIQuestion(ctx, q.ID); err != nil {
		return nil, huma.Error500InternalServerError("delete question: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// checkThreadOwnership ensures the thread belongs to the PR in the URL.
// Prevents thread IDs from being used across PRs via URL tampering.
func checkThreadOwnership(ctx context.Context, s *Server, owner, name string, number int, thread db.AIThread) error {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: owner, name: name, number: number})
	if err != nil {
		return huma.Error404NotFound("pull request not found")
	}
	if thread.MergeRequestID != mrID {
		return huma.Error404NotFound("thread not in this pull request")
	}
	return nil
}

func validateAIThreadInput(input *createAIThreadInput) error {
	if input.Body.Path == "" {
		return huma.Error400BadRequest("path is required")
	}
	switch input.Body.AnchorSide {
	case "LEFT", "RIGHT":
	default:
		return huma.Error400BadRequest("anchor_side must be LEFT or RIGHT")
	}
	if input.Body.AnchorLine <= 0 {
		return huma.Error400BadRequest("anchor_line must be positive")
	}
	if strings.TrimSpace(input.Body.Question) == "" {
		return huma.Error400BadRequest("question is required")
	}
	if input.Body.CommitSHA == "" {
		return huma.Error400BadRequest("commit_sha is required")
	}
	return nil
}

// Keep parseInt available for callers that may want to interpret
// thread IDs out of bodies; unused right now but prevents a dead import.
var _ = json.Marshal
var _ = http.MethodPost
var _ = fmt.Sprintf

// --- PR-level review brief ---

type aiBriefResponse struct {
	ID              int64      `json:"id"`
	MergeRequestID  int64      `json:"mr_id"`
	HeadSHA         string     `json:"head_sha"`
	Status          string     `json:"status"`
	Depth           string     `json:"depth"`
	Content         string     `json:"content"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ClaudeSessionID *string    `json:"claude_session_id,omitempty"`
}

func toBriefResponse(b db.AIBrief) aiBriefResponse {
	r := aiBriefResponse{
		ID:             b.ID,
		MergeRequestID: b.MergeRequestID,
		HeadSHA:        b.HeadSHA,
		Status:         b.Status,
		Depth:          b.Depth,
		Content:        b.Content,
		Error:          b.Error,
		CreatedAt:      b.CreatedAt.UTC(),
		StartedAt:      utcPtr(b.StartedAt),
		CompletedAt:    utcPtr(b.CompletedAt),
	}
	if b.ClaudeSessionID != nil && *b.ClaudeSessionID != "" {
		r.ClaudeSessionID = b.ClaudeSessionID
	}
	return r
}

type createBriefInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Depth string `json:"depth,omitempty" doc:"quick (default) or deep"`
	}
}

type briefPathInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type aiBriefOutput struct{ Body aiBriefResponse }

func (s *Server) createAIBrief(ctx context.Context, input *createBriefInput) (*aiBriefOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI brief not available: clone manager or worktree dir not configured")
	}

	if isLocalSource(input.Owner) {
		return s.createAIBriefLocal(ctx, input)
	}

	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	// Resolve the PR's current head SHA + merge base from DB (kept
	// up to date by the sync engine). The brief is keyed on head
	// SHA so a push invalidates the cache naturally.
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet; try refreshing the PR first")
	}

	// Build prompt context: PR title/branches + git log between base
	// and head, pre-computed so Claude doesn't need Bash. Diff text
	// is left for Claude to read via Read tools — embedding the full
	// diff would blow up the token budget on large PRs.
	pr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("load pull request: " + err.Error())
	}
	if pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	cloneDir := s.clones.ClonePath(host, input.Owner, input.Name)
	gitLog, _ := runGitLogForBrief(ctx, cloneDir, shas.MergeBaseSHA, shas.DiffHeadSHA)

	var ctxBuf strings.Builder
	fmt.Fprintf(&ctxBuf, "Repo: %s/%s\n", input.Owner, input.Name)
	fmt.Fprintf(&ctxBuf, "PR #%d: %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&ctxBuf, "Base: %s  Head: %s  Branch: %s → %s\n",
		shas.MergeBaseSHA[:min(7, len(shas.MergeBaseSHA))],
		shas.DiffHeadSHA[:min(7, len(shas.DiffHeadSHA))],
		pr.HeadBranch, pr.BaseBranch)
	if pr.Body != "" {
		ctxBuf.WriteString("\nAuthor's description:\n")
		ctxBuf.WriteString(strings.TrimSpace(pr.Body))
		ctxBuf.WriteString("\n")
	}
	if gitLog != "" {
		ctxBuf.WriteString("\nCommit log (oldest first):\n")
		ctxBuf.WriteString(gitLog)
		ctxBuf.WriteString("\n")
	}

	depth := strings.ToLower(strings.TrimSpace(input.Body.Depth))
	if depth == "" {
		depth = "quick"
	}

	brief, err := s.aiReview.CreateBrief(ctx, aireview.BriefInput{
		MergeRequestID: mrID,
		Owner:          input.Owner,
		Name:           input.Name,
		HeadSHA:        shas.DiffHeadSHA,
		MergeBaseSHA:   shas.MergeBaseSHA,
		Depth:          depth,
		PromptContext:  ctxBuf.String(),
	})
	if err != nil {
		return nil, huma.Error502BadGateway("create brief: " + err.Error())
	}
	return &aiBriefOutput{Body: toBriefResponse(brief)}, nil
}

// createAIBriefLocal builds the brief for a local worktree. Mirrors
// the GitHub path but pulls SHAs / commit log from the worktree
// directory instead of the bare clone, and points the runner at the
// existing worktree via LocalWorktreePath.
func (s *Server) createAIBriefLocal(
	ctx context.Context, input *createBriefInput,
) (*aiBriefOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
	if err != nil {
		return nil, huma.Error500InternalServerError("ensure synthetic MR: " + err.Error())
	}

	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, err := worktrees.ResolveBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error500InternalServerError("resolve base: " + err.Error())
	}
	if base.Fallback {
		return nil, huma.Error409Conflict("worktree has no remote base ref to summarize against")
	}

	gitLog, _ := runGitLogForBrief(ctx, w.Path, base.SHA, w.HeadSHA)

	branch := w.Branch
	if branch == "" {
		branch = "(detached)"
	}
	var ctxBuf strings.Builder
	fmt.Fprintf(&ctxBuf, "Local worktree: %s\n", w.Path)
	fmt.Fprintf(&ctxBuf, "Branch: %s vs %s\n", branch, base.Ref)
	fmt.Fprintf(&ctxBuf, "Base: %s  Head: %s\n",
		base.SHA[:min(7, len(base.SHA))],
		w.HeadSHA[:min(7, len(w.HeadSHA))])
	if gitLog != "" {
		ctxBuf.WriteString("\nCommit log (oldest first):\n")
		ctxBuf.WriteString(gitLog)
		ctxBuf.WriteString("\n")
	}

	depth := strings.ToLower(strings.TrimSpace(input.Body.Depth))
	if depth == "" {
		depth = "quick"
	}

	brief, err := s.aiReview.CreateBrief(ctx, aireview.BriefInput{
		MergeRequestID:    mrID,
		Owner:             input.Owner,
		Name:              input.Name,
		HeadSHA:           w.HeadSHA,
		MergeBaseSHA:      base.SHA,
		Depth:             depth,
		PromptContext:     ctxBuf.String(),
		LocalWorktreePath: w.Path,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("create brief: " + err.Error())
	}
	return &aiBriefOutput{Body: toBriefResponse(brief)}, nil
}

func (s *Server) getAIBrief(ctx context.Context, input *briefPathInput) (*aiBriefOutput, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	brief, err := s.db.GetLatestAIBriefForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error404NotFound("no brief for this PR yet")
	}
	return &aiBriefOutput{Body: toBriefResponse(brief)}, nil
}

func (s *Server) deleteAIBrief(ctx context.Context, input *briefPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI brief not available")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	brief, err := s.db.GetLatestAIBriefForMR(ctx, mrID)
	if err != nil {
		return &emptyOutput{}, nil // nothing to delete, idempotent
	}
	if err := s.aiReview.DeleteBrief(ctx, brief.ID); err != nil {
		return nil, huma.Error500InternalServerError("delete brief: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// --- Per-commit review guide ---

type aiCommitAnalysisResponse struct {
	ID             int64      `json:"id"`
	MergeRequestID int64      `json:"mr_id"`
	CommitSHA      string     `json:"commit_sha"`
	Status         string     `json:"status"`
	Content        string     `json:"content"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

func toCommitAnalysisResponse(a db.AICommitAnalysis) aiCommitAnalysisResponse {
	return aiCommitAnalysisResponse{
		ID:             a.ID,
		MergeRequestID: a.MergeRequestID,
		CommitSHA:      a.CommitSHA,
		Status:         a.Status,
		Content:        a.Content,
		Error:          a.Error,
		CreatedAt:      a.CreatedAt.UTC(),
		StartedAt:      utcPtr(a.StartedAt),
		CompletedAt:    utcPtr(a.CompletedAt),
	}
}

type commitAnalysisPathInput struct {
	Owner     string `path:"owner"`
	Name      string `path:"name"`
	Number    int    `path:"number"`
	CommitSHA string `path:"sha"`
}

type aiCommitAnalysisOutput struct {
	Body aiCommitAnalysisResponse
}

func (s *Server) createAICommitAnalysis(ctx context.Context, input *commitAnalysisPathInput) (*aiCommitAnalysisOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI commit analysis not available: clone manager or worktree dir not configured")
	}
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if input.CommitSHA == "" {
		return nil, huma.Error400BadRequest("commit sha required")
	}

	// PR metadata + range bounds. The PR range powers the per-commit
	// cache (Sequence section needs neighbouring commits); the PR
	// title/branches go into the prompt context so Claude has
	// orientation beyond the bare commit.
	pr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil || pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil {
		return nil, huma.Error500InternalServerError("load pull request shas")
	}

	var ctxBuf strings.Builder
	fmt.Fprintf(&ctxBuf, "Repo: %s/%s\n", input.Owner, input.Name)
	fmt.Fprintf(&ctxBuf, "PR #%d: %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&ctxBuf, "Branch: %s → %s\n", pr.HeadBranch, pr.BaseBranch)

	row, err := s.aiReview.CreateCommitAnalysis(ctx, aireview.CommitAnalysisInput{
		MergeRequestID: mrID,
		Owner:          input.Owner,
		Name:           input.Name,
		CommitSHA:      input.CommitSHA,
		PRMergeBaseSHA: shas.MergeBaseSHA,
		PRHeadSHA:      shas.DiffHeadSHA,
		PromptContext:  ctxBuf.String(),
	})
	if err != nil {
		return nil, huma.Error502BadGateway("create commit analysis: " + err.Error())
	}
	return &aiCommitAnalysisOutput{Body: toCommitAnalysisResponse(row)}, nil
}

func (s *Server) getAICommitAnalysis(ctx context.Context, input *commitAnalysisPathInput) (*aiCommitAnalysisOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	row, err := s.db.GetAICommitAnalysisForCommit(ctx, mrID, input.CommitSHA)
	if err != nil {
		return nil, huma.Error404NotFound("no analysis for this commit yet")
	}
	return &aiCommitAnalysisOutput{Body: toCommitAnalysisResponse(row)}, nil
}

func (s *Server) deleteAICommitAnalysis(ctx context.Context, input *commitAnalysisPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI commit analysis not available")
	}
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	row, err := s.db.GetAICommitAnalysisForCommit(ctx, mrID, input.CommitSHA)
	if err != nil {
		return &emptyOutput{}, nil // nothing to delete; idempotent
	}
	if err := s.aiReview.DeleteCommitAnalysis(ctx, row.ID); err != nil {
		return nil, huma.Error500InternalServerError("delete commit analysis: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// runGitLogForBrief captures a compact oneline log between base and
// head that Claude can reference without needing Bash.
func runGitLogForBrief(ctx context.Context, cloneDir, base, head string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", cloneDir,
		"log", "--first-parent", "--reverse", "--format=%H %an%x00%s", base+".."+head)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Rewrite null-separated "sha name\0subject" into "sha (name) subject"
	// for the prompt.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var sb strings.Builder
	for _, l := range lines {
		parts := strings.SplitN(l, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		head, subj := parts[0], parts[1]
		sb.WriteString(head)
		sb.WriteString(" ")
		sb.WriteString(subj)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}
