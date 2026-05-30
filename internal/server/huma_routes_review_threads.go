package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/worktrees"
)

// Local-worktree review threads. Live at the PR-shaped path so middleman
// keeps one addressing convention; owner=="local" gates the behavior.
// A "review" is the living set of these threads on a worktree's
// synthetic merge request.

type reviewThreadCommentResponse struct {
	ID        int64  `json:"id"`
	Author    string `json:"author" doc:"user | agent"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at" doc:"UTC RFC3339 timestamp"`
}

type reviewThreadResponse struct {
	ID        int64                         `json:"id"`
	Path      string                        `json:"path"`
	Side      string                        `json:"side" doc:"LEFT | RIGHT"`
	Line      int                           `json:"line"`
	StartLine *int                          `json:"start_line,omitempty"`
	CommitSHA string                        `json:"commit_sha"`
	Status    string                        `json:"status" doc:"open | discussed | applied | resolved"`
	Hidden    bool                          `json:"hidden"`
	CreatedAt string                        `json:"created_at" doc:"UTC RFC3339 timestamp"`
	UpdatedAt string                        `json:"updated_at" doc:"UTC RFC3339 timestamp"`
	Comments  []reviewThreadCommentResponse `json:"comments"`
}

type listReviewThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type listReviewThreadsOutput struct {
	Body struct {
		Threads []reviewThreadResponse `json:"threads"`
	}
}

// reviewThreadDraft is one inline draft comment in a create request: an
// anchor (path/side/line[/start_line]/commit) plus the reviewer's root
// comment body. Named (not anonymous) so the generated client exposes a
// meaningful type rather than the auto-named "Item".
type reviewThreadDraft struct {
	Path      string `json:"path"`
	Side      string `json:"side" doc:"LEFT | RIGHT"`
	Line      int    `json:"line"`
	StartLine *int   `json:"start_line,omitempty"`
	CommitSHA string `json:"commit_sha"`
	Body      string `json:"body" doc:"the reviewer's root comment"`
}

type createReviewThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Mode    string              `json:"mode,omitempty" doc:"discuss-first | act-immediately | persist-only (default)"`
		Threads []reviewThreadDraft `json:"threads"`
	}
}

type createReviewThreadsOutput struct {
	Body struct {
		Threads []reviewThreadResponse `json:"threads" doc:"the MR's full review-thread list after creation"`
	}
}

type addReviewThreadCommentInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Body   string `json:"body"`
		Author string `json:"author,omitempty" doc:"user (default) | agent"`
	}
}

type reviewThreadOutput struct {
	Body reviewThreadResponse
}

type reviewThreadActionInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
}

func (s *Server) registerReviewThreadRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-threads", s.listReviewThreads)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads", s.createReviewThreads)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments", s.addReviewThreadComment)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/hide", s.hideLocalReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unhide", s.unhideLocalReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve", s.resolveReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply", s.applyReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all", s.applyAllReviewThreads)
}

// loadReviewThreadsResponse lists an MR's threads with their comments
// grouped in. Shared by the list and create handlers.
func (s *Server) loadReviewThreadsResponse(ctx context.Context, mrID int64) ([]reviewThreadResponse, error) {
	threads, err := s.db.ListReviewThreadsForMR(ctx, mrID)
	if err != nil {
		return nil, err
	}
	comments, err := s.db.ListReviewThreadCommentsForMR(ctx, mrID)
	if err != nil {
		return nil, err
	}
	byThread := map[int64][]reviewThreadCommentResponse{}
	for _, c := range comments {
		byThread[c.ThreadID] = append(byThread[c.ThreadID], reviewThreadCommentResponse{
			ID:        c.ID,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := make([]reviewThreadResponse, 0, len(threads))
	for _, t := range threads {
		out = append(out, toReviewThreadResponse(t, byThread[t.ID]))
	}
	return out, nil
}

func toReviewThreadResponse(t db.ReviewThread, comments []reviewThreadCommentResponse) reviewThreadResponse {
	if comments == nil {
		comments = []reviewThreadCommentResponse{}
	}
	return reviewThreadResponse{
		ID:        t.ID,
		Path:      t.Path,
		Side:      t.Side,
		Line:      t.Line,
		StartLine: t.StartLine,
		CommitSHA: t.CommitSHA,
		Status:    t.Status,
		Hidden:    t.HiddenAt != nil,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
		Comments:  comments,
	}
}

func (s *Server) listReviewThreads(ctx context.Context, input *listReviewThreadsInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

func (s *Server) createReviewThreads(ctx context.Context, input *createReviewThreadsInput) (*createReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if len(input.Body.Threads) == 0 {
		return nil, huma.Error400BadRequest("at least one thread is required")
	}
	// Validate mode before persisting so a bad request never writes rows.
	switch input.Body.Mode {
	case "", "persist-only", "discuss-first", "act-immediately":
	default:
		return nil, huma.Error400BadRequest("invalid mode: " + input.Body.Mode)
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	in := make([]db.NewReviewThread, 0, len(input.Body.Threads))
	for _, t := range input.Body.Threads {
		if t.Side != "LEFT" && t.Side != "RIGHT" {
			return nil, huma.Error400BadRequest("side must be LEFT or RIGHT")
		}
		if t.Path == "" {
			return nil, huma.Error400BadRequest("path is required")
		}
		if t.Line < 1 {
			return nil, huma.Error400BadRequest("line must be >= 1")
		}
		if t.CommitSHA == "" {
			return nil, huma.Error400BadRequest("commit_sha is required")
		}
		if t.Body == "" {
			return nil, huma.Error400BadRequest("each thread needs a comment body")
		}
		in = append(in, db.NewReviewThread{
			Path: t.Path, Side: t.Side, Line: t.Line,
			StartLine: t.StartLine, CommitSHA: t.CommitSHA, Body: t.Body,
		})
	}
	created, err := s.db.CreateReviewThreads(ctx, mrID, in)
	if err != nil {
		return nil, huma.Error500InternalServerError("create review threads: " + err.Error())
	}
	switch input.Body.Mode {
	case "discuss-first":
		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "discuss", created); err != nil {
			return nil, err
		}
	case "act-immediately":
		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "apply", created); err != nil {
			return nil, err
		}
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &createReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

// resolveThreadForMR confirms the thread exists and belongs to the MR
// behind this PR-shaped route, guarding against cross-worktree ids.
// Callers gate isLocalSource themselves; resolveOrEnsureMRID does not
// reject non-local owners.
func (s *Server) resolveThreadForMR(ctx context.Context, owner, name string, number int, threadID int64) (int64, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, owner, name, number)
	if err != nil {
		return 0, huma.Error404NotFound("worktree not found")
	}
	th, err := s.db.GetReviewThread(ctx, threadID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && th.MergeRequestID != mrID) {
		return 0, huma.Error404NotFound("review thread not found")
	}
	if err != nil {
		return 0, huma.Error500InternalServerError("get review thread: " + err.Error())
	}
	return mrID, nil
}

func (s *Server) addReviewThreadComment(ctx context.Context, input *addReviewThreadCommentInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if input.Body.Body == "" {
		return nil, huma.Error400BadRequest("comment body is required")
	}
	author := input.Body.Author
	if author == "" {
		author = "user"
	}
	if author != "user" && author != "agent" {
		return nil, huma.Error400BadRequest("author must be user or agent")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if _, err := s.db.AddReviewThreadComment(ctx, input.ThreadID, author, input.Body.Body, nil); err != nil {
		return nil, huma.Error500InternalServerError("add comment: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) hideLocalReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.HideReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("hide thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) unhideLocalReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.UnhideReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("unhide thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) resolveReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.SetReviewThreadStatus(ctx, input.ThreadID, "resolved"); err != nil {
		return nil, huma.Error500InternalServerError("resolve thread: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

// oneReviewThreadOutput re-reads a single thread (with comments) for the
// action responses.
func (s *Server) oneReviewThreadOutput(ctx context.Context, threadID int64) (*reviewThreadOutput, error) {
	th, err := s.db.GetReviewThread(ctx, threadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload thread: " + err.Error())
	}
	dbComments, err := s.db.ListReviewThreadComments(ctx, threadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload comments: " + err.Error())
	}
	comments := make([]reviewThreadCommentResponse, 0, len(dbComments))
	for _, c := range dbComments {
		comments = append(comments, reviewThreadCommentResponse{
			ID:        c.ID,
			Author:    c.Author,
			Body:      c.Body,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := &reviewThreadOutput{Body: toReviewThreadResponse(th, comments)}
	return out, nil
}

// kickoffReviewTurn drives the review agent for a set of threads. It
// mirrors submitWorktreeSessionTurn (ensure session, resolve base,
// SubmitTurn) but adds the discuss/apply Action, per-thread context,
// the middleman MCP wiring, a busy gate, and an optimistic status set.
//
// Status is set optimistically at kickoff (discussed for discuss,
// applied for apply): simple and acceptable for a local single-user
// tool — a failed turn surfaces in the session activity log.
func (s *Server) kickoffReviewTurn(
	ctx context.Context, owner, name string, number int,
	action string, threads []db.ReviewThread,
) error {
	if s.sessionRunner == nil {
		return huma.Error503ServiceUnavailable("sessions not available")
	}
	w, err := s.resolveLocalWorktree(ctx, name, number)
	if err != nil {
		return huma.Error404NotFound("worktree not found")
	}
	sess, isFirst, err := s.ensureWorktreeSession(ctx, w.ID)
	if err != nil {
		return huma.Error500InternalServerError("ensure session: " + err.Error())
	}
	if s.sessionHasRunningTurn(ctx, sess.ID) {
		return huma.Error409Conflict("the review agent is busy; wait for the current turn to finish")
	}
	tcs := make([]aireview.ThreadContext, 0, len(threads))
	for _, t := range threads {
		tcs = append(tcs, aireview.ThreadContext{
			ID: t.ID, Path: t.Path, Line: t.Line, Side: t.Side,
			RootComment: s.firstThreadCommentBody(ctx, t.ID),
		})
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, _ := worktrees.ResolveBase(ctx, w.Path, baseRef)
	exe, _ := os.Executable()
	// discuss runs as a read-only review_feedback turn; apply runs as a
	// user_message turn whose tools may edit the worktree.
	verb := "review_feedback"
	if action == "apply" {
		verb = "user_message"
	}
	if _, err := s.sessionRunner.SubmitTurn(ctx, aireview.SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: w.Path, Branch: w.Branch,
		BaseRef: base.Ref, BaseSHA: base.SHA, HeadSHA: w.HeadSHA,
		UserTurnType: verb, UserTurnContent: actionMessage(action, tcs), IsFirstTurn: isFirst,
		Action: action, Threads: tcs,
		MCP: &aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: owner, Name: name, Number: number},
	}); err != nil {
		return huma.Error500InternalServerError("submit turn: " + err.Error())
	}
	target := "discussed"
	if action == "apply" {
		target = "applied"
	}
	for _, t := range threads {
		_ = s.db.SetReviewThreadStatus(ctx, t.ID, target)
	}
	return nil
}

func actionMessage(action string, tcs []aireview.ThreadContext) string {
	if action == "apply" {
		return fmt.Sprintf("Apply %d review thread(s).", len(tcs))
	}
	return fmt.Sprintf("Discuss %d review thread(s).", len(tcs))
}

// firstThreadCommentBody returns the root comment body for a thread, or
// "" if it has none (comments are ordered id ASC, so [0] is the root).
func (s *Server) firstThreadCommentBody(ctx context.Context, threadID int64) string {
	comments, err := s.db.ListReviewThreadComments(ctx, threadID)
	if err != nil || len(comments) == 0 {
		return ""
	}
	return comments[0].Body
}

// applyReviewThread kicks off an apply turn for a single thread.
func (s *Server) applyReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	mrID, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID)
	if err != nil {
		return nil, err
	}
	th, err := s.db.GetReviewThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("get thread: " + err.Error())
	}
	if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "apply", []db.ReviewThread{th}); err != nil {
		return nil, err
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

// applyAllReviewThreads kicks off a single apply turn covering every
// eligible (visible, open|discussed) thread on the MR.
func (s *Server) applyAllReviewThreads(ctx context.Context, input *listReviewThreadsInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	all, err := s.db.ListReviewThreadsForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review threads: " + err.Error())
	}
	eligible := make([]db.ReviewThread, 0, len(all))
	for _, t := range all {
		if t.HiddenAt == nil && (t.Status == "open" || t.Status == "discussed") {
			eligible = append(eligible, t)
		}
	}
	if len(eligible) > 0 {
		if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "apply", eligible); err != nil {
			return nil, err
		}
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}
