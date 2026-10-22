package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/db"
)

const reviewDraftBodyMaxBytes = 65536

var reviewDraftIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type reviewDraftInput struct {
	ID        string `json:"id,omitempty"          doc:"Client-generated id; the server generates one when empty"`
	Path      string `json:"path"                  doc:"File path (may be empty only for replies)"`
	Side      string `json:"side"                  doc:"LEFT or RIGHT"`
	Line      int    `json:"line"                  doc:"1-based line"`
	StartLine *int   `json:"start_line,omitempty"  doc:"First line of a multi-line range"`
	CommitSHA string `json:"commit_sha"            doc:"Commit the comment was written against"`
	InReplyTo *int64 `json:"in_reply_to,omitempty" doc:"Upstream comment id this reply threads under"`
	Body      string `json:"body"                  doc:"Comment body (markdown)"`
	CreatedAt string `json:"created_at,omitempty"  doc:"RFC3339; kept by the localStorage import"`
}

type reviewDraftResponse struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Side        string `json:"side"`
	Line        int    `json:"line"`
	StartLine   *int   `json:"start_line,omitempty"`
	CommitSHA   string `json:"commit_sha"`
	InReplyTo   *int64 `json:"in_reply_to,omitempty"`
	Body        string `json:"body"`
	Origin      string `json:"origin"`
	State       string `json:"state"`
	RunID       *int64 `json:"run_id,omitempty"`
	Rationale   string `json:"rationale,omitempty"`
	SubmitError string `json:"submit_error,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type reviewDraftMetaResponse struct {
	Body  string `json:"body"`
	Event string `json:"event"`
}

type reviewDraftsResponse struct {
	Drafts []reviewDraftResponse   `json:"drafts"`
	Meta   reviewDraftMetaResponse `json:"meta"`
}

type reviewDraftsOutput struct{ Body reviewDraftsResponse }
type reviewDraftOutput struct{ Body reviewDraftResponse }
type reviewDraftMetaOutput struct{ Body reviewDraftMetaResponse }

type createReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Drafts []reviewDraftInput `json:"drafts" minItems:"1" maxItems:"500"`
	}
}

type updateReviewDraftInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
	Body    struct {
		Body string `json:"body"`
	}
}

type deleteReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		IDs []string `json:"ids" minItems:"1" maxItems:"500"`
	}
}

type deleteReviewDraftsOutput struct {
	Body struct {
		Deleted int64 `json:"deleted"`
	}
}

type putReviewDraftMetaInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Body  string `json:"body"`
		Event string `json:"event" doc:"APPROVE, COMMENT, REQUEST_CHANGES, or empty"`
	}
}

type reviewDraftSummaryItem struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Number int    `json:"number"`
	Count  int    `json:"count"`
}

type reviewDraftSummaryOutput struct {
	Body struct {
		PRs []reviewDraftSummaryItem `json:"prs"`
	}
}

func (s *Server) registerReviewDraftRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts", s.listReviewDrafts)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts", s.createReviewDrafts)
	huma.Patch(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}", s.updateReviewDraft)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/delete", s.deleteReviewDrafts)
	huma.Put(api, "/repos/{owner}/{name}/pulls/{number}/review-draft-meta", s.putReviewDraftMeta)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/publish", s.publishReviewDrafts)
	huma.Get(api, "/review-drafts/summary", s.reviewDraftSummary)
	s.registerAIReviewRunRoutes(api)
	s.registerDraftChatRoutes(api)
}

// resolveDraftScope maps a PR-shaped path to (mrID, branch). branch is
// "" for GitHub and the worktree's current branch for local, so local
// drafts follow the branch the way review threads do.
func (s *Server) resolveDraftScope(
	ctx context.Context, owner, name string, number int,
) (int64, string, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, owner, name, number)
	if err != nil {
		return 0, "", err
	}
	if !isLocalSource(owner) {
		return mrID, "", nil
	}
	w, err := s.resolveLocalWorktree(ctx, name, number)
	if err != nil {
		return 0, "", err
	}
	return mrID, s.currentWorktreeBranch(ctx, w), nil
}

func toReviewDraftResponse(r db.ReviewDraft) reviewDraftResponse {
	return reviewDraftResponse{
		ID: r.ID, Path: r.Path, Side: r.Side, Line: r.Line, StartLine: r.StartLine,
		CommitSHA: r.CommitSHA, InReplyTo: r.InReplyTo, Body: r.Body,
		Origin: r.Origin, State: r.State, RunID: r.RunID, SubmitError: r.SubmitError,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) loadReviewDrafts(ctx context.Context, mrID int64, branch string) (*reviewDraftsOutput, error) {
	rows, err := s.db.ListReviewDrafts(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review drafts: " + err.Error())
	}
	meta, err := s.db.GetReviewDraftMeta(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("load review draft meta: " + err.Error())
	}
	rationales, err := s.db.ListDraftRationales(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("load draft rationales: " + err.Error())
	}
	out := &reviewDraftsOutput{}
	out.Body.Drafts = make([]reviewDraftResponse, 0, len(rows))
	for _, r := range rows {
		resp := toReviewDraftResponse(r)
		resp.Rationale = rationales[r.ID]
		out.Body.Drafts = append(out.Body.Drafts, resp)
	}
	out.Body.Meta = reviewDraftMetaResponse{Body: meta.Body, Event: meta.Event}
	return out, nil
}

func (s *Server) listReviewDrafts(ctx context.Context, input *repoNumberInput) (*reviewDraftsOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	return s.loadReviewDrafts(ctx, mrID, branch)
}

func validateDraftBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("body is required")
	}
	if len(body) > reviewDraftBodyMaxBytes {
		return fmt.Errorf("body too large: %d bytes (max %d)", len(body), reviewDraftBodyMaxBytes)
	}
	return nil
}

func newReviewDraftID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func toNewReviewDraft(i int, in reviewDraftInput) (db.NewReviewDraft, error) {
	fail := func(msg string) (db.NewReviewDraft, error) {
		return db.NewReviewDraft{}, fmt.Errorf("drafts[%d]: %s", i, msg)
	}
	id := in.ID
	if id == "" {
		id = newReviewDraftID()
	}
	if !reviewDraftIDPattern.MatchString(id) {
		return fail("id must match " + reviewDraftIDPattern.String())
	}
	if in.Side != "LEFT" && in.Side != "RIGHT" {
		return fail("side must be LEFT or RIGHT")
	}
	if in.Line < 1 {
		return fail("line must be positive")
	}
	if in.Path == "" && (in.InReplyTo == nil || *in.InReplyTo <= 0) {
		return fail("path is required")
	}
	if err := validateDraftBody(in.Body); err != nil {
		return fail(err.Error())
	}
	var created time.Time
	if in.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, in.CreatedAt)
		if err != nil {
			return fail("created_at must be RFC3339")
		}
		created = t.UTC()
	}
	return db.NewReviewDraft{
		ID: id, Path: in.Path, Side: in.Side, Line: in.Line, StartLine: in.StartLine,
		CommitSHA: in.CommitSHA, InReplyTo: in.InReplyTo, Body: in.Body, CreatedAt: created,
	}, nil
}

func (s *Server) createReviewDrafts(ctx context.Context, input *createReviewDraftsInput) (*reviewDraftsOutput, error) {
	news := make([]db.NewReviewDraft, 0, len(input.Body.Drafts))
	for i, d := range input.Body.Drafts {
		n, err := toNewReviewDraft(i, d)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		news = append(news, n)
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if _, err := s.db.InsertUserReviewDrafts(ctx, mrID, branch, news); err != nil {
		return nil, huma.Error500InternalServerError("create review drafts: " + err.Error())
	}
	return s.loadReviewDrafts(ctx, mrID, branch)
}

func (s *Server) updateReviewDraft(ctx context.Context, input *updateReviewDraftInput) (*reviewDraftOutput, error) {
	if err := validateDraftBody(input.Body.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	r, err := s.db.UpdateReviewDraftBody(ctx, mrID, branch, input.DraftID, input.Body.Body)
	if err != nil {
		return nil, huma.Error500InternalServerError("update review draft: " + err.Error())
	}
	if r == nil {
		return nil, huma.Error404NotFound("draft not found")
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*r)}, nil
}

func (s *Server) deleteReviewDrafts(ctx context.Context, input *deleteReviewDraftsInput) (*deleteReviewDraftsOutput, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	n, err := s.db.DeleteReviewDrafts(ctx, mrID, branch, input.Body.IDs)
	if err != nil {
		return nil, huma.Error500InternalServerError("delete review drafts: " + err.Error())
	}
	if isLocalSource(input.Owner) {
		s.pruneSnapshotRefs(ctx)
	}
	out := &deleteReviewDraftsOutput{}
	out.Body.Deleted = n
	return out, nil
}

func (s *Server) putReviewDraftMeta(ctx context.Context, input *putReviewDraftMetaInput) (*reviewDraftMetaOutput, error) {
	switch input.Body.Event {
	case "", "APPROVE", "COMMENT", "REQUEST_CHANGES":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, COMMENT, REQUEST_CHANGES, or empty")
	}
	if len(input.Body.Body) > reviewDraftBodyMaxBytes {
		return nil, huma.Error400BadRequest("body too large")
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	m, err := s.db.UpsertReviewDraftMeta(ctx, mrID, branch, input.Body.Body, input.Body.Event)
	if err != nil {
		return nil, huma.Error500InternalServerError("save review draft meta: " + err.Error())
	}
	return &reviewDraftMetaOutput{Body: reviewDraftMetaResponse{Body: m.Body, Event: m.Event}}, nil
}

func (s *Server) reviewDraftSummary(ctx context.Context, _ *struct{}) (*reviewDraftSummaryOutput, error) {
	counts, err := s.db.ListReviewDraftCounts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("review draft summary: " + err.Error())
	}
	out := &reviewDraftSummaryOutput{}
	out.Body.PRs = make([]reviewDraftSummaryItem, 0, len(counts))
	for _, c := range counts {
		out.Body.PRs = append(out.Body.PRs, reviewDraftSummaryItem{Owner: c.Owner, Name: c.Name, Number: c.Number, Count: c.Count})
	}
	return out, nil
}

type publishReviewDraftsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Event    string `json:"event"               doc:"APPROVE, REQUEST_CHANGES, or COMMENT"`
		Body     string `json:"body,omitempty"      doc:"Optional top-level review body"`
		CommitID string `json:"commit_id,omitempty" doc:"PR head SHA; fallback anchor for drafts without one"`
		Mode     string `json:"mode,omitempty"      doc:"Local only: act-immediately or persist-only (default)"`
		Path     string `json:"path,omitempty"      doc:"Local only: publish only this file's drafts"`
	}
}

type publishReviewDraftsOutput struct {
	Body struct {
		ReviewID  int64  `json:"review_id"`
		State     string `json:"state"`
		Published int    `json:"published"`
	}
}

func draftToSubmitComment(d db.ReviewDraft) submitReviewComment {
	c := submitReviewComment{Path: d.Path, Line: d.Line, Side: d.Side, Body: d.Body, CommitID: d.CommitSHA}
	if d.StartLine != nil {
		c.StartLine = *d.StartLine
	}
	if d.InReplyTo != nil {
		c.InReplyTo = *d.InReplyTo
	}
	return c
}

// publishReviewDrafts posts the PR's state='draft' rows to GitHub,
// oldest first. Each draft is deleted as soon as GitHub accepts it, so
// a failure part-way leaves only the unposted drafts, the failing one
// carrying submit_error; a retry posts only what is left. The review
// wrapper goes out only after every draft succeeded.
func (s *Server) publishReviewDrafts(ctx context.Context, input *publishReviewDraftsInput) (*publishReviewDraftsOutput, error) {
	if isLocalSource(input.Owner) {
		return s.publishLocalReviewDrafts(ctx, input)
	}
	switch input.Body.Event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, REQUEST_CHANGES, or COMMENT")
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	rows, err := s.db.ListReviewDrafts(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review drafts: " + err.Error())
	}
	drafts := make([]db.ReviewDraft, 0, len(rows))
	comments := make([]submitReviewComment, 0, len(rows))
	for _, r := range rows {
		if r.State != "draft" {
			continue
		}
		drafts = append(drafts, r)
		comments = append(comments, draftToSubmitComment(r))
	}
	if input.Body.Event == "REQUEST_CHANGES" && strings.TrimSpace(input.Body.Body) == "" && len(comments) == 0 {
		return nil, huma.Error400BadRequest("REQUEST_CHANGES requires a review body or at least one inline comment")
	}
	preps, err := prepareReviewComments(comments, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	// Bookkeeping must survive the client going away: a comment GitHub
	// accepted but whose draft row stayed would be posted again on retry.
	dbCtx := context.WithoutCancel(ctx)
	for i, p := range preps {
		if err := s.postPreparedComment(ctx, client, input.Owner, input.Name, input.Number, mrID, p); err != nil {
			herr := translateCreateError(err, input.Owner, input.Name, input.Number, &p, i)
			_ = s.db.SetReviewDraftSubmitError(dbCtx, drafts[i].ID, herr.Error())
			return nil, herr
		}
		if _, err := s.db.DeleteReviewDrafts(dbCtx, mrID, branch, []string{drafts[i].ID}); err != nil {
			return nil, huma.Error500InternalServerError("comment posted but draft not cleared: " + err.Error())
		}
	}
	review, err := s.postReviewWrapper(ctx, client, input.Owner, input.Name, input.Number,
		input.Body.Event, input.Body.Body, input.Body.CommitID)
	if err != nil {
		return nil, err
	}
	_ = s.db.DeleteReviewDraftMeta(dbCtx, mrID, branch)
	out := &publishReviewDraftsOutput{}
	out.Body.Published = len(preps)
	if review != nil {
		out.Body.ReviewID = review.GetID()
		out.Body.State = review.GetState()
	}
	return out, nil
}

// publishLocalReviewDrafts turns a local worktree's drafts into review
// threads (the local equivalent of posting a review), then removes the
// drafts it turned into threads. Path scopes it to one file (doc pane).
func (s *Server) publishLocalReviewDrafts(ctx context.Context, input *publishReviewDraftsInput) (*publishReviewDraftsOutput, error) {
	mode := input.Body.Mode
	switch mode {
	case "", "persist-only":
		mode = ""
	case "act-immediately":
	default:
		return nil, huma.Error400BadRequest("mode must be act-immediately or persist-only")
	}
	mrID, branch, err := s.resolveDraftScope(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	rows, err := s.db.ListReviewDrafts(ctx, mrID, branch)
	if err != nil {
		return nil, huma.Error500InternalServerError("list review drafts: " + err.Error())
	}
	in := &createReviewThreadsInput{Owner: input.Owner, Name: input.Name, Number: input.Number}
	in.Body.Mode = mode
	var ids []string
	for _, r := range rows {
		if r.State != "draft" || r.InReplyTo != nil || (input.Body.Path != "" && r.Path != input.Body.Path) {
			continue
		}
		in.Body.Threads = append(in.Body.Threads, reviewThreadDraft{
			Path: r.Path, Side: r.Side, Line: r.Line, StartLine: r.StartLine, CommitSHA: r.CommitSHA, Body: r.Body,
		})
		ids = append(ids, r.ID)
	}
	out := &publishReviewDraftsOutput{}
	if len(ids) == 0 {
		return nil, huma.Error400BadRequest("add at least one inline comment to create review threads")
	}
	if _, err := s.createReviewThreads(ctx, in); err != nil {
		return nil, err
	}
	dbCtx := context.WithoutCancel(ctx)
	if _, err := s.db.DeleteReviewDrafts(dbCtx, mrID, branch, ids); err != nil {
		return nil, huma.Error500InternalServerError("threads created but drafts not cleared: " + err.Error())
	}
	if input.Body.Path == "" {
		_ = s.db.DeleteReviewDraftMeta(dbCtx, mrID, branch)
	}
	s.pruneSnapshotRefs(dbCtx)
	out.Body.Published = len(ids)
	return out, nil
}
