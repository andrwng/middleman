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
	ID          int64  `json:"id"`
	Author      string `json:"author" doc:"user | agent"`
	Body        string `json:"body"`
	SentToAgent bool   `json:"sent_to_agent" doc:"true if this comment was sent to the agent (an Ask)"`
	CreatedAt   string `json:"created_at" doc:"UTC RFC3339 timestamp"`
}

type reviewThreadResponse struct {
	ID            int64                         `json:"id"`
	Path          string                        `json:"path"`
	Side          string                        `json:"side" doc:"LEFT | RIGHT"`
	Line          int                           `json:"line"`
	StartLine     *int                          `json:"start_line,omitempty"`
	CommitSHA     string                        `json:"commit_sha"`
	Status        string                        `json:"status" doc:"open | discussed | applied | resolved"`
	WritesAllowed bool                          `json:"writes_allowed" doc:"true when the agent may edit files in steer turns scoped to this thread (equivalent to status=='applied' today)"`
	Hidden        bool                          `json:"hidden"`
	CreatedAt     string                        `json:"created_at" doc:"UTC RFC3339 timestamp"`
	UpdatedAt     string                        `json:"updated_at" doc:"UTC RFC3339 timestamp"`
	Comments      []reviewThreadCommentResponse `json:"comments"`
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
// reviewThreadDraftComment is one extra authored comment appended after a
// draft's root body, in order. Used when promoting an Ask-Claude session
// whose Q&A turns become alternating user/agent comments.
type reviewThreadDraftComment struct {
	Author string `json:"author" doc:"user | agent"`
	Body   string `json:"body"`
}

type reviewThreadDraft struct {
	Path      string                     `json:"path"`
	Side      string                     `json:"side" doc:"LEFT | RIGHT"`
	Line      int                        `json:"line"`
	StartLine *int                       `json:"start_line,omitempty"`
	CommitSHA string                     `json:"commit_sha"`
	Body      string                     `json:"body" doc:"the reviewer's root comment"`
	Comments  []reviewThreadDraftComment `json:"comments,omitempty" doc:"additional comments appended after the root, in order"`
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

type askReviewThreadInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Body string `json:"body"`
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
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask", s.askReviewThread)
	huma.Delete(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}", s.deleteReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/hide", s.hideLocalReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unhide", s.unhideLocalReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve", s.resolveReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unresolve", s.unresolveReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply", s.applyReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/discuss", s.discussReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all", s.applyAllReviewThreads)
}

// loadReviewThreadsResponse lists an MR's threads (scoped to branch) with
// their comments grouped in. Shared by the list and create handlers.
func (s *Server) loadReviewThreadsResponse(ctx context.Context, mrID int64, branch string) ([]reviewThreadResponse, error) {
	threads, err := s.db.ListReviewThreadsForMRBranch(ctx, mrID, branch)
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
			ID:          c.ID,
			Author:      c.Author,
			Body:        c.Body,
			SentToAgent: c.SentToAgent,
			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
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
		ID:            t.ID,
		Path:          t.Path,
		Side:          t.Side,
		Line:          t.Line,
		StartLine:     t.StartLine,
		CommitSHA:     t.CommitSHA,
		Status:        t.Status,
		WritesAllowed: t.Status == "applied",
		Hidden:        t.HiddenAt != nil,
		CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt.UTC().Format(time.RFC3339),
		Comments:      comments,
	}
}

func (s *Server) listReviewThreads(ctx context.Context, input *listReviewThreadsInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
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
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.ensureSyntheticMRForWorktree(ctx, w)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	branch := s.currentWorktreeBranch(ctx, w)

	// Canonicalize every commit_sha to a full SHA. An empty value (an
	// agent calling start_thread without one) resolves to live HEAD; a
	// non-empty value (a short SHA from the agent's worktree prompt, or a
	// full SHA from the UI) is peeled to its canonical form so stored
	// threads compare equal to the full SHAs in the commit list and never
	// render as spurious orphans. An empty value that won't resolve means
	// the worktree's HEAD is broken (fatal); a non-empty value that won't
	// resolve is kept verbatim — a genuinely bogus SHA legitimately reads
	// as orphan rather than failing the whole create.
	var headSHA string
	for i := range input.Body.Threads {
		raw := input.Body.Threads[i].CommitSHA
		if raw == "" {
			if headSHA == "" {
				sha, err := worktrees.ResolveCommitSHA(ctx, w.Path, "HEAD")
				if err != nil {
					return nil, huma.Error500InternalServerError("resolve HEAD: " + err.Error())
				}
				headSHA = sha
			}
			input.Body.Threads[i].CommitSHA = headSHA
			continue
		}
		if sha, err := worktrees.ResolveCommitSHA(ctx, w.Path, raw); err == nil {
			input.Body.Threads[i].CommitSHA = sha
		}
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
			// Defense-in-depth: should never fire after the resolution block above.
			return nil, huma.Error500InternalServerError("commit_sha is required (server failed to resolve)")
		}
		if t.Body == "" {
			return nil, huma.Error400BadRequest("each thread needs a comment body")
		}
		var comments []db.NewReviewThreadComment
		if len(t.Comments) > 0 {
			comments = make([]db.NewReviewThreadComment, 0, len(t.Comments))
			for _, c := range t.Comments {
				if c.Author != "user" && c.Author != "agent" {
					return nil, huma.Error400BadRequest("comment author must be user or agent")
				}
				if c.Body == "" {
					return nil, huma.Error400BadRequest("each appended comment needs a body")
				}
				comments = append(comments, db.NewReviewThreadComment{Author: c.Author, Body: c.Body})
			}
		}
		in = append(in, db.NewReviewThread{
			Path: t.Path, Side: t.Side, Line: t.Line,
			StartLine: t.StartLine, CommitSHA: t.CommitSHA, Body: t.Body,
			Comments: comments,
		})
	}
	created, err := s.db.CreateReviewThreadsOnBranch(ctx, mrID, branch, in)
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
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, branch)
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

// askReviewThread persists the reviewer's comment, then enqueues a
// read-only steer turn via the per-session FIFO. The comment is always
// persisted and marked sent_to_agent once the turn is enqueued; the
// reviewer's message is never lost regardless of session queue depth.
func (s *Server) askReviewThread(ctx context.Context, input *askReviewThreadInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if input.Body.Body == "" {
		return nil, huma.Error400BadRequest("message is required")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	th, err := s.db.GetReviewThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("get thread: " + err.Error())
	}
	if _, err := s.db.AddReviewThreadComment(ctx, input.ThreadID, "user", input.Body.Body, nil); err != nil {
		return nil, huma.Error500InternalServerError("add comment: " + err.Error())
	}
	if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "steer", []db.ReviewThread{th}); err != nil {
		// Comment is persisted; surface any infrastructure error.
		return nil, err
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}

func (s *Server) deleteReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID)
	if err != nil {
		return nil, err
	}
	if err := s.db.DeleteReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("delete thread: " + err.Error())
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
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

// unresolveReviewThread reopens a resolved thread back to 'open' so it can
// be discussed/applied again. We don't track the pre-resolve status, so
// reopening is always to 'open'.
func (s *Server) unresolveReviewThread(ctx context.Context, input *reviewThreadActionInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	if err := s.db.SetReviewThreadStatus(ctx, input.ThreadID, "open"); err != nil {
		return nil, huma.Error500InternalServerError("unresolve thread: " + err.Error())
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
			ID:          c.ID,
			Author:      c.Author,
			Body:        c.Body,
			SentToAgent: c.SentToAgent,
			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := &reviewThreadOutput{Body: toReviewThreadResponse(th, comments)}
	return out, nil
}

// kickoffReviewTurn drives the review agent for a set of threads. It
// mirrors submitWorktreeSessionTurn (ensure session, resolve base,
// SubmitTurn) but adds the discuss/apply Action, per-thread context,
// the middleman MCP wiring, the per-session FIFO enqueue, and an
// optimistic status set.
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
	sess, isFirst, err := s.ensureWorktreeSession(ctx, w.ID, s.currentWorktreeBranch(ctx, w))
	if err != nil {
		return huma.Error500InternalServerError("ensure session: " + err.Error())
	}
	tcs := make([]aireview.ThreadContext, 0, len(threads))
	allApplied := len(threads) > 0
	type flushEntry struct {
		threadID   int64
		commentIDs []int64
	}
	var toMark []flushEntry
	// steerLastBody holds the reviewer's typed message for single-thread steer
	// turns; it becomes the UserTurnContent so the conversation pane shows the
	// actual typed text instead of the generic "Discuss N thread(s)." summary.
	var steerLastBody string
	for _, t := range threads {
		writesAllowed := t.Status == "applied"
		if !writesAllowed {
			allApplied = false
		}

		comments, err := s.db.ListReviewThreadComments(ctx, t.ID)
		if err != nil {
			return huma.Error500InternalServerError("list thread comments: " + err.Error())
		}
		var rootID int64
		var rootBody string
		if len(comments) > 0 {
			rootID = comments[0].ID
			rootBody = comments[0].Body
		}

		unsent, err := s.db.ListUnsentUserComments(ctx, t.ID)
		if err != nil {
			return huma.Error500InternalServerError("list unsent comments: " + err.Error())
		}
		stacked := make([]string, 0, len(unsent))
		ids := make([]int64, 0, len(unsent))
		for _, c := range unsent {
			ids = append(ids, c.ID)
			if c.ID == rootID {
				continue // root is the thread headline; don't duplicate it in the stacked block
			}
			stacked = append(stacked, c.Body)
		}
		if action == "steer" && len(threads) == 1 && len(unsent) > 0 {
			steerLastBody = unsent[len(unsent)-1].Body
		}
		if len(ids) > 0 {
			toMark = append(toMark, flushEntry{threadID: t.ID, commentIDs: ids})
		}

		tcs = append(tcs, aireview.ThreadContext{
			ID: t.ID, Path: t.Path, Line: t.Line, Side: t.Side,
			RootComment:     rootBody,
			WritesAllowed:   writesAllowed,
			StackedComments: stacked,
		})
	}
	allowWrites := action == "steer" && allApplied
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, _ := worktrees.ResolveBase(ctx, w.Path, baseRef)
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return huma.Error500InternalServerError("cannot resolve middleman executable for the MCP server")
	}
	// discuss = read-only review_feedback; apply = user_message (may edit);
	// steer = read-only user_message continuation. For single-thread steer,
	// UserTurnContent is the reviewer's typed body so the conversation pane
	// shows the actual message; the agent also gets it via StackedComments.
	verb := "review_feedback"
	if action == "apply" || action == "steer" {
		verb = "user_message"
	}
	content := actionMessage(action, tcs)
	if action == "steer" && steerLastBody != "" {
		content = steerLastBody
	}
	if _, err := s.sessionRunner.SubmitTurn(ctx, aireview.SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: w.Path, Branch: w.Branch,
		BaseRef: base.Ref, BaseSHA: base.SHA, HeadSHA: w.HeadSHA,
		UserTurnType: verb, UserTurnContent: content, IsFirstTurn: isFirst,
		Action: action, Threads: tcs, AllowWrites: allowWrites,
		MCP: &aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: owner, Name: name, Number: number},
	}); err != nil {
		return huma.Error500InternalServerError("submit turn: " + err.Error())
	}
	// Item 5: mark every flushed comment sent now that the turn is enqueued.
	// Mark is at engage time, not dispatch time — "I sent it" semantics.
	for _, fe := range toMark {
		for _, cid := range fe.commentIDs {
			_ = s.db.MarkReviewThreadCommentSentToAgent(ctx, cid)
		}
	}
	// steer continues an existing discussion — leave thread status unchanged.
	if action != "steer" {
		target := "discussed"
		if action == "apply" {
			target = "applied"
		}
		for _, t := range threads {
			// Item 4: don't downgrade an already-applied thread back to "discussed"
			// when a subsequent Discuss fires — "applied" is the writes-allowed
			// signal and must stay sticky across later Discuss turns. Apply itself
			// is still hidden by the UI on applied threads (canApply gates on
			// open|discussed), so this only matters when /discuss is hit directly.
			if action == "discuss" && t.Status == "applied" {
				continue
			}
			_ = s.db.SetReviewThreadStatus(ctx, t.ID, target)
		}
	}
	return nil
}

func actionMessage(action string, tcs []aireview.ThreadContext) string {
	if action == "apply" {
		return fmt.Sprintf("Apply %d review thread(s).", len(tcs))
	}
	return fmt.Sprintf("Discuss %d review thread(s).", len(tcs))
}

// applyReviewThread kicks off an apply turn for a single thread.
func (s *Server) applyReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
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
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}

// discussReviewThread kicks off a read-only discuss turn for a single
// thread: the agent responds in-thread without editing. Used by the
// thread card's "Discuss" action (the empty-composer state) so a thread
// can be sent to the agent for discussion without typing a message.
func (s *Server) discussReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID)
	if err != nil {
		return nil, err
	}
	th, err := s.db.GetReviewThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("get thread: " + err.Error())
	}
	if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "discuss", []db.ReviewThread{th}); err != nil {
		return nil, err
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, s.currentWorktreeBranch(ctx, w))
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
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	branch := s.currentWorktreeBranch(ctx, w)
	// Scope the eligible set to the current branch: apply-all must apply
	// only this branch's open/discussed threads, never another branch's —
	// those threads' commit_sha/line anchors don't exist on the current
	// checkout, so applying them would be wrong.
	all, err := s.db.ListReviewThreadsForMRBranch(ctx, mrID, branch)
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
	threads, err := s.loadReviewThreadsResponse(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}
