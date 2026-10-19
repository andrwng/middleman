package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/worktrees"
)

// GitHub review comments are stored with the anchor they had when first
// synced and never refreshed, so after a push their recorded lines number
// an older tree than the diff on screen. This endpoint maps them onto the
// revision the reader is looking at, using the repository's bare clone.

type reviewCommentAnchorsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	At     string `query:"at" doc:"Revision to resolve against: the new-side SHA of the diff on screen. Defaults to the pull request's diff head."`
}

type reviewCommentAnchorResponse struct {
	CommentID int64  `json:"comment_id" doc:"GitHub review comment id"`
	State     string `json:"state" doc:"current | moved | removed | unmappable"`
	Path      string `json:"path,omitempty" doc:"Set for current and moved"`
	Line      int    `json:"line,omitempty" doc:"Set for current and moved"`
	StartLine int    `json:"start_line,omitempty" doc:"First line of a multi-line comment, when it still precedes line"`
}

type reviewCommentAnchorsResponse struct {
	At      string                        `json:"at" doc:"Revision the anchors were resolved against; empty when none was available"`
	Anchors []reviewCommentAnchorResponse `json:"anchors" doc:"One entry per resolved comment. Comments absent here (left side, file-level, no clone yet, out of time) keep their recorded position."`
}

type reviewCommentAnchorsOutput struct {
	Body reviewCommentAnchorsResponse
}

func (s *Server) registerReviewCommentAnchorRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-comment-anchors", s.listReviewCommentAnchors)
}

func (s *Server) listReviewCommentAnchors(
	ctx context.Context, input *reviewCommentAnchorsInput,
) (*reviewCommentAnchorsOutput, error) {
	if isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review comment anchors are for GitHub pull requests")
	}
	// `at` reaches git as a positional revision; see listReviewThreads.
	if input.At != "" && !symbolRefsSHA.MatchString(input.At) {
		return nil, huma.Error400BadRequest("at must be a hex object id")
	}
	mr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get pull request failed")
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	out := &reviewCommentAnchorsOutput{}
	out.Body.Anchors = []reviewCommentAnchorResponse{}
	at := input.At
	if at == "" {
		at = mr.DiffHeadSHA
	}
	// DiffHeadSHA comes from git rev-parse, but it is a DB column on its
	// way to argv, so it gets the same check as the query parameter.
	if !symbolRefsSHA.MatchString(at) {
		return out, nil
	}
	out.Body.At = at

	// Without the clone on disk every comment would read as unmappable and
	// drop out of the diff, so answer "no opinion" instead.
	if s.clones == nil {
		return out, nil
	}
	repoPath := s.clones.ClonePath(s.syncer.HostForRepo(input.Owner, input.Name), input.Owner, input.Name)
	if _, err := os.Stat(filepath.Join(repoPath, "HEAD")); err != nil {
		return out, nil
	}

	events, err := s.db.ListMREvents(ctx, mr.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list mr events failed")
	}
	anchors := map[int64]reviewCommentAnchor{}
	for _, e := range events {
		if e.EventType != "review_comment" || e.PlatformID == nil {
			continue
		}
		var meta reviewCommentAnchorMeta
		if err := json.Unmarshal([]byte(e.MetadataJSON), &meta); err != nil {
			continue
		}
		if a, ok := sourceAnchor(meta); ok {
			anchors[*e.PlatformID] = a
		}
	}
	if len(anchors) == 0 {
		return out, nil
	}

	budgetCtx, cancel := context.WithTimeout(ctx, reviewAnchorBudget)
	defer cancel()
	for id, r := range s.reviewAnchors.resolve(budgetCtx, repoPath, at, anchors) {
		resp := reviewCommentAnchorResponse{CommentID: id, State: string(r.State)}
		if r.State == worktrees.AnchorCurrent || r.State == worktrees.AnchorMoved {
			resp.Path, resp.Line, resp.StartLine = r.Path, r.Line, r.StartLine
		}
		out.Body.Anchors = append(out.Body.Anchors, resp)
	}
	sort.Slice(out.Body.Anchors, func(i, j int) bool {
		return out.Body.Anchors[i].CommentID < out.Body.Anchors[j].CommentID
	})
	return out, nil
}
