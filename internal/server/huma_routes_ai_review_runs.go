package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
)

const reviewInstructionsMaxBytes = 8192

type aiReviewRunResponse struct {
	ID              int64   `json:"id"`
	Status          string  `json:"status"`
	HeadSHA         string  `json:"head_sha"`
	Instructions    string  `json:"instructions"`
	Summary         string  `json:"summary"`
	Error           string  `json:"error,omitempty"`
	RawJSON         string  `json:"raw_json"`
	SuggestionCount int     `json:"suggestion_count"`
	CreatedAt       string  `json:"created_at"`
	StartedAt       *string `json:"started_at,omitempty"`
	CompletedAt     *string `json:"completed_at,omitempty"`
}
type aiReviewRunOutput struct{ Body aiReviewRunResponse }
type latestAIReviewRunOutput struct {
	Body struct {
		Run *aiReviewRunResponse `json:"run"`
	}
}

type startAIReviewRunInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Instructions string `json:"instructions,omitempty" doc:"Optional reviewer instructions for Claude"`
	}
}

type aiReviewRunPathInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	RunID  int64  `path:"run_id"`
}

type proposeCommentInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	RunID  int64  `path:"run_id"`
	Body   struct {
		Path      string `json:"path"`
		Side      string `json:"side"`
		Line      int    `json:"line"`
		StartLine *int   `json:"start_line,omitempty"`
		Body      string `json:"body"`
		Rationale string `json:"rationale"`
	}
}

type draftTransitionInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
}

func (s *Server) registerAIReviewRunRoutes(api huma.API) {
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs", s.startAIReviewRun)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/latest", s.latestAIReviewRun)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/cancel", s.cancelAIReviewRun)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-review-runs/{run_id}/proposals", s.proposeComment)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/accept", s.acceptReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/discard", s.discardReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/restore", s.restoreReviewDraft)
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339)
	return &v
}

func (s *Server) toAIReviewRunResponse(ctx context.Context, r db.AIReviewRun) aiReviewRunResponse {
	n, _ := s.db.CountRunSuggestions(ctx, r.ID)
	return aiReviewRunResponse{
		ID: r.ID, Status: r.Status, HeadSHA: r.HeadSHA, Instructions: r.Instructions,
		Summary: r.Summary, Error: r.Error, RawJSON: r.RawJSON, SuggestionCount: n,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		StartedAt: rfc3339Ptr(r.StartedAt), CompletedAt: rfc3339Ptr(r.CompletedAt),
	}
}

// reviewCommentsContext lists published review comments so Claude does
// not repeat what reviewers already said. Capped to keep the prompt small.
func (s *Server) reviewCommentsContext(ctx context.Context, mrID int64) string {
	events, err := s.db.ListMREvents(ctx, mrID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, e := range events {
		if e.EventType != "review_comment" || n >= 50 {
			continue
		}
		var meta struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		}
		_ = json.Unmarshal([]byte(e.MetadataJSON), &meta)
		body := strings.TrimSpace(e.Body)
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		fmt.Fprintf(&b, "- %s:%d (%s): %s\n", meta.Path, meta.Line, e.Author, strings.ReplaceAll(body, "\n", " "))
		n++
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nPublished review comments already on this PR:\n" + b.String()
}

func (s *Server) startAIReviewRun(ctx context.Context, input *startAIReviewRunInput) (*aiReviewRunOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("Review with Claude is not available: clone manager or worktree dir not configured")
	}
	if isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("Review with Claude is not available for local worktrees yet")
	}
	if len(input.Body.Instructions) > reviewInstructionsMaxBytes {
		return nil, huma.Error400BadRequest("instructions too long")
	}
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet; try refreshing the PR first")
	}
	pr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil || pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	gitLog, _ := runGitLogForBrief(ctx, s.clones.ClonePath(host, input.Owner, input.Name), shas.MergeBaseSHA, shas.DiffHeadSHA)
	var pc strings.Builder
	fmt.Fprintf(&pc, "Repo: %s/%s\nPR #%d: %s\nBranch: %s -> %s\n", input.Owner, input.Name, pr.Number, pr.Title, pr.HeadBranch, pr.BaseBranch)
	if strings.TrimSpace(pr.Body) != "" {
		pc.WriteString("\nAuthor's description:\n" + strings.TrimSpace(pr.Body) + "\n")
	}
	if gitLog != "" {
		pc.WriteString("\nCommit log (oldest first):\n" + gitLog + "\n")
	}
	if diff, err := s.clones.Diff(ctx, host, input.Owner, input.Name, shas.MergeBaseSHA, shas.DiffHeadSHA, false); err == nil {
		pc.WriteString(changedFilesContext(diff.Files))
	}
	pc.WriteString(s.reviewCommentsContext(ctx, mrID))

	run, err := s.db.CreateAIReviewRun(ctx, db.NewAIReviewRun{
		MergeRequestID: mrID, HeadSHA: shas.DiffHeadSHA, ReadSHA: shas.DiffHeadSHA,
		Instructions: strings.TrimSpace(input.Body.Instructions),
	})
	if errors.Is(err, db.ErrReviewRunActive) {
		return nil, huma.Error409Conflict("a Review with Claude run is already in progress for this pull request")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("create review run: " + err.Error())
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		_ = s.db.MarkAIReviewRunFailed(ctx, run.ID, "cannot resolve middleman executable for the MCP server", "")
		return nil, huma.Error500InternalServerError("cannot resolve middleman executable for the MCP server")
	}
	if err := s.aiReview.StartReviewRun(ctx, aireview.ReviewRunInput{
		RunID: run.ID, Owner: input.Owner, Name: input.Name, Number: input.Number,
		HeadSHA: shas.DiffHeadSHA, MergeBaseSHA: shas.MergeBaseSHA,
		Instructions: run.Instructions, PromptContext: pc.String(),
		MCP: aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: input.Owner, Name: input.Name, Number: input.Number, RunID: run.ID},
	}); err != nil {
		return nil, huma.Error502BadGateway("start review run: " + err.Error())
	}
	cur, err := s.db.GetAIReviewRun(ctx, run.ID)
	if err != nil || cur == nil {
		return nil, huma.Error500InternalServerError("reload review run")
	}
	return &aiReviewRunOutput{Body: s.toAIReviewRunResponse(ctx, *cur)}, nil
}

func (s *Server) latestAIReviewRun(ctx context.Context, input *repoNumberInput) (*latestAIReviewRunOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	run, err := s.db.GetLatestAIReviewRun(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("load review run: " + err.Error())
	}
	out := &latestAIReviewRunOutput{}
	if run != nil {
		resp := s.toAIReviewRunResponse(ctx, *run)
		out.Body.Run = &resp
	}
	return out, nil
}

// runForPR loads a run and checks it belongs to the PR in the path.
func (s *Server) runForPR(ctx context.Context, owner, name string, number int, runID int64) (*db.AIReviewRun, int64, string, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, owner, name, number)
	if err != nil {
		return nil, 0, "", huma.Error404NotFound("pull request not found")
	}
	run, err := s.db.GetAIReviewRun(ctx, runID)
	if err != nil {
		return nil, 0, "", huma.Error500InternalServerError("load review run: " + err.Error())
	}
	if run == nil || run.MergeRequestID != mrID || run.Branch != branch {
		return nil, 0, "", huma.Error404NotFound("review run not found")
	}
	return run, mrID, branch, nil
}

func (s *Server) cancelAIReviewRun(ctx context.Context, input *aiReviewRunPathInput) (*aiReviewRunOutput, error) {
	run, _, _, err := s.runForPR(ctx, input.Owner, input.Name, input.Number, input.RunID)
	if err != nil {
		return nil, err
	}
	if s.aiReview != nil {
		if err := s.aiReview.CancelReviewRun(ctx, run.ID); err != nil {
			return nil, huma.Error500InternalServerError("cancel review run: " + err.Error())
		}
	} else {
		_ = s.db.MarkAIReviewRunCancelled(ctx, run.ID)
	}
	cur, err := s.db.GetAIReviewRun(ctx, run.ID)
	if err != nil || cur == nil {
		return nil, huma.Error500InternalServerError("reload review run")
	}
	return &aiReviewRunOutput{Body: s.toAIReviewRunResponse(ctx, *cur)}, nil
}

func (s *Server) proposeComment(ctx context.Context, input *proposeCommentInput) (*reviewDraftOutput, error) {
	run, mrID, branch, err := s.runForPR(ctx, input.Owner, input.Name, input.Number, input.RunID)
	if err != nil {
		return nil, err
	}
	if run.Status != "queued" && run.Status != "running" {
		return nil, huma.Error409Conflict("this review run is no longer active; nothing was stored")
	}
	in := input.Body
	if in.Side != "LEFT" && in.Side != "RIGHT" {
		return nil, huma.Error400BadRequest("side must be LEFT or RIGHT")
	}
	if in.Line < 1 || in.Path == "" {
		return nil, huma.Error400BadRequest("path and a positive line are required")
	}
	if err := validateDraftBody(in.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if len(in.Rationale) > reviewDraftBodyMaxBytes {
		return nil, huma.Error400BadRequest("rationale too large")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.MergeBaseSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet")
	}
	host := s.syncer.HostForRepo(input.Owner, input.Name)
	file, err := anchorFileDiff(ctx, s.clones, host, input.Owner, input.Name, shas.MergeBaseSHA, run.ReadSHA, in.Path)
	if err != nil {
		return nil, huma.Error500InternalServerError("load diff: " + err.Error())
	}
	if file == nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("%s is not changed in this pull request", in.Path))
	}
	if err := validateAnchor(file, in.Side, in.Line, in.StartLine); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	draft, err := s.db.InsertClaudeSuggestion(ctx, mrID, branch, run.ID, db.NewReviewDraft{
		ID: newReviewDraftID(), Path: in.Path, Side: in.Side, Line: in.Line, StartLine: in.StartLine,
		CommitSHA: run.HeadSHA, Body: in.Body,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("store suggestion: " + err.Error())
	}
	resp := toReviewDraftResponse(draft)
	if r := strings.TrimSpace(in.Rationale); r != "" {
		if _, err := s.db.InsertReviewDraftMessage(ctx, db.NewReviewDraftMessage{
			DraftID: draft.ID, Author: "claude", Kind: "message", Body: r, Status: "done",
		}); err != nil {
			return nil, huma.Error500InternalServerError("store rationale: " + err.Error())
		}
		resp.Rationale = r
	}
	return &reviewDraftOutput{Body: resp}, nil
}

func (s *Server) transitionDraft(
	ctx context.Context, input *draftTransitionInput,
	fn func(context.Context, int64, string, string) (*db.ReviewDraft, error),
) (*reviewDraftOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	r, err := fn(ctx, mrID, branch, input.DraftID)
	if errors.Is(err, db.ErrInvalidDraftTransition) {
		return nil, huma.Error409Conflict("draft is not in a state that allows this")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("update draft: " + err.Error())
	}
	if r == nil {
		return nil, huma.Error404NotFound("draft not found")
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*r)}, nil
}

func (s *Server) acceptReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.AcceptReviewDraft)
}
func (s *Server) discardReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.DiscardReviewDraft)
}
func (s *Server) restoreReviewDraft(ctx context.Context, input *draftTransitionInput) (*reviewDraftOutput, error) {
	return s.transitionDraft(ctx, input, s.db.RestoreReviewDraft)
}

// maxPromptFiles caps the changed-file listing so a huge PR does not
// blow up the prompt; Claude can still find the rest by reading.
const maxPromptFiles = 200

// changedFilesContext lists the PR's files with the line ranges a review
// comment can anchor to. Bash is unavailable to Claude, so without this
// it would have to guess anchors from per-commit diffs.
func changedFilesContext(files []gitclone.DiffFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nChanged files (status, path, commentable new-side lines / old-side lines):\n")
	for i := range files {
		if i == maxPromptFiles {
			fmt.Fprintf(&b, "- ... %d more files not listed\n", len(files)-maxPromptFiles)
			break
		}
		f := &files[i]
		fmt.Fprintf(&b, "- %s %s", f.Status, f.Path)
		if f.OldPath != "" && f.OldPath != f.Path {
			fmt.Fprintf(&b, " (from %s)", f.OldPath)
		}
		if f.IsBinary {
			b.WriteString("; binary, not commentable\n")
			continue
		}
		fmt.Fprintf(&b, "; commentable new-side lines: %s; old-side lines: %s\n",
			describeRanges(f, "RIGHT"), describeRanges(f, "LEFT"))
	}
	return b.String()
}
