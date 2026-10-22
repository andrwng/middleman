package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	"github.com/wesm/middleman/internal/worktrees"
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
	if len(input.Body.Instructions) > reviewInstructionsMaxBytes {
		return nil, huma.Error400BadRequest("instructions too long")
	}
	if isLocalSource(input.Owner) {
		return s.startAIReviewRunLocal(ctx, input)
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

	promptContext := s.prContextBlock(ctx, input.Owner, input.Name, mrID, shas, pr)

	run, err := s.db.CreateAIReviewRun(ctx, db.NewAIReviewRun{
		MergeRequestID: mrID, HeadSHA: shas.DiffHeadSHA, ReadSHA: shas.DiffHeadSHA,
		BaseSHA: shas.MergeBaseSHA, Instructions: strings.TrimSpace(input.Body.Instructions),
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
		Instructions: run.Instructions, PromptContext: promptContext,
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
	file, err := s.runAnchorFile(ctx, input.Owner, input.Name, input.Number, run, in.Path)
	if err != nil {
		return nil, err
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
	out, err := s.transitionDraft(ctx, input, s.db.DiscardReviewDraft)
	if err == nil && isLocalSource(input.Owner) {
		s.pruneSnapshotRefs(ctx)
	}
	return out, err
}

// CancelReviewRunsForClosedPRs stops review runs on pull requests that
// were closed or merged. Called after each sync and at startup.
func (s *Server) CancelReviewRunsForClosedPRs() {
	if s.aiReview == nil {
		return
	}
	runs, err := s.db.ListActiveAIReviewRunsOnClosedPRs(s.bgCtx)
	if err != nil {
		slog.Warn("list review runs on closed PRs failed", "err", err)
		return
	}
	for _, r := range runs {
		if err := s.aiReview.CancelReviewRun(s.bgCtx, r.ID); err != nil {
			slog.Warn("cancel review run on closed PR failed", "run_id", r.ID, "err", err)
		}
	}
}

// pruneSnapshotRefs deletes the snapshot refs of finished local runs that
// no suggestion or draft points at any more. Best-effort: a worktree that
// was removed simply has nothing left to clean.
func (s *Server) pruneSnapshotRefs(ctx context.Context) {
	runs, err := s.db.ListLocalRunsWithoutLiveDrafts(ctx)
	if err != nil {
		slog.Warn("list prunable review snapshots failed", "err", err)
		return
	}
	for _, r := range runs {
		_ = aireview.DeleteSnapshotRef(ctx, r.LocalWorktreePath, r.ID)
	}
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

// prContextBlock renders what Claude needs to orient itself in a PR: the
// title, branches, description, commit log, changed files with their
// commentable ranges, and published review comments.
func (s *Server) prContextBlock(
	ctx context.Context, owner, name string, mrID int64, shas *db.DiffSHAs, pr *db.MergeRequest,
) string {
	host := s.syncer.HostForRepo(owner, name)
	gitLog, _ := runGitLogForBrief(ctx, s.clones.ClonePath(host, owner, name), shas.MergeBaseSHA, shas.DiffHeadSHA)
	var pc strings.Builder
	fmt.Fprintf(&pc, "Repo: %s/%s\nPR #%d: %s\nBranch: %s -> %s\n", owner, name, pr.Number, pr.Title, pr.HeadBranch, pr.BaseBranch)
	if strings.TrimSpace(pr.Body) != "" {
		pc.WriteString("\nAuthor's description:\n" + strings.TrimSpace(pr.Body) + "\n")
	}
	if gitLog != "" {
		pc.WriteString("\nCommit log (oldest first):\n" + gitLog + "\n")
	}
	if diff, err := s.clones.Diff(ctx, host, owner, name, shas.MergeBaseSHA, shas.DiffHeadSHA, false); err == nil {
		pc.WriteString(changedFilesContext(diff.Files))
	}
	pc.WriteString(s.reviewCommentsContext(ctx, mrID))
	return pc.String()
}

// runAnchorFile loads path's diff for a run: base..read_sha, the diff
// Claude was shown. Local runs diff in the reviewer's repo, where the
// snapshot commit lives; the unfiltered diff keeps renames intact.
func (s *Server) runAnchorFile(
	ctx context.Context, owner, name string, number int, run *db.AIReviewRun, path string,
) (*gitclone.DiffFile, error) {
	if run.LocalWorktreePath != "" {
		files, err := worktrees.DiffRange(ctx, run.LocalWorktreePath, run.BaseSHA, run.ReadSHA)
		if err != nil {
			return nil, huma.Error500InternalServerError("load diff: " + err.Error())
		}
		for i := range files {
			if files[i].Path != path {
				continue
			}
			// The snapshot includes untracked files so Claude can read
			// them, but the local diff only shows files git tracks.
			if untracked, err := worktrees.IsUntracked(ctx, run.LocalWorktreePath, path); err == nil && untracked {
				return nil, huma.Error400BadRequest(fmt.Sprintf(
					"%s is untracked in the reviewer's worktree; comments can only anchor to files git tracks", path))
			}
			return &files[i], nil
		}
		return nil, nil
	}
	base := run.BaseSHA
	if base == "" { // runs started before base_sha was recorded
		shas, err := s.db.GetDiffSHAs(ctx, owner, name, number)
		if err != nil || shas == nil || shas.MergeBaseSHA == "" {
			return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet")
		}
		base = shas.MergeBaseSHA
	}
	host := s.syncer.HostForRepo(owner, name)
	file, err := anchorFileDiff(ctx, s.clones, host, owner, name, base, run.ReadSHA, path)
	if err != nil {
		return nil, huma.Error500InternalServerError("load diff: " + err.Error())
	}
	return file, nil
}

// startAIReviewRunLocal reviews a local worktree. Claude reads a snapshot
// commit of the live tree (uncommitted and untracked work included) in
// its own checkout; suggestions anchor to HEAD like working-tree
// comments, so they are not outdated the moment they appear.
func (s *Server) startAIReviewRunLocal(ctx context.Context, input *startAIReviewRunInput) (*aiReviewRunOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	branch := s.currentWorktreeBranch(ctx, w)
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, err := worktrees.ResolveBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error409Conflict("resolve base: " + err.Error())
	}
	head, err := worktrees.ResolveCommitSHA(ctx, w.Path, "HEAD")
	if err != nil {
		return nil, huma.Error500InternalServerError("resolve HEAD: " + err.Error())
	}
	run, err := s.db.CreateAIReviewRun(ctx, db.NewAIReviewRun{
		MergeRequestID: mrID, Branch: branch, HeadSHA: head, ReadSHA: head,
		BaseSHA: base.SHA, LocalWorktreePath: w.Path,
		Instructions: strings.TrimSpace(input.Body.Instructions),
	})
	if errors.Is(err, db.ErrReviewRunActive) {
		return nil, huma.Error409Conflict("a Review with Claude run is already in progress for this branch")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("create review run: " + err.Error())
	}
	fail := func(msg string) (*aiReviewRunOutput, error) {
		_ = s.db.MarkAIReviewRunFailed(context.WithoutCancel(ctx), run.ID, msg, "")
		return nil, huma.Error502BadGateway(msg)
	}
	snap, err := aireview.SnapshotWorkingTree(ctx, w.Path, run.ID)
	if err != nil {
		return fail("snapshot working tree: " + err.Error())
	}
	if err := s.db.SetAIReviewRunReadSHA(ctx, run.ID, snap); err != nil {
		return fail("record snapshot: " + err.Error())
	}
	files, err := worktrees.DiffRange(ctx, w.Path, base.SHA, snap)
	if err != nil {
		return fail("diff snapshot: " + err.Error())
	}
	gitLog, _ := runGitLogForBrief(ctx, w.Path, base.SHA, head)
	var pc strings.Builder
	fmt.Fprintf(&pc, "Local worktree: %s on branch %s\nBase: %s (%s)\n", input.Name, branch, base.Ref, shortRef(base.SHA))
	pc.WriteString("The working copy is a snapshot of the reviewer's worktree, uncommitted and untracked changes included.\n")
	if gitLog != "" {
		pc.WriteString("\nCommits since base (oldest first):\n" + gitLog + "\n")
	}
	pc.WriteString(changedFilesContext(files))
	if untracked, err := worktrees.ListUntracked(ctx, w.Path); err == nil && len(untracked) > 0 {
		if len(untracked) > maxPromptFiles {
			untracked = untracked[:maxPromptFiles]
		}
		pc.WriteString("\nUntracked files (included above so you can read them, but not commentable until the reviewer adds them):\n- " +
			strings.Join(untracked, "\n- ") + "\n")
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return fail("cannot resolve middleman executable for the MCP server")
	}
	if err := s.aiReview.StartReviewRun(ctx, aireview.ReviewRunInput{
		RunID: run.ID, Owner: input.Owner, Name: input.Name, Number: input.Number,
		HeadSHA: snap, MergeBaseSHA: base.SHA, RepoDir: w.Path,
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

func shortRef(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
