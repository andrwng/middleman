package server

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// --- inputs / outputs --------------------------------------------------------

type hideReviewThreadInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		RootCommentID int64 `json:"root_comment_id" doc:"GitHub platform id of the thread's root review comment"`
	}
}

type unhideReviewThreadInput struct {
	Owner         string `path:"owner"`
	Name          string `path:"name"`
	Number        int    `path:"number"`
	RootCommentID int64  `path:"root_comment_id"`
}

// --- handlers ----------------------------------------------------------------

// hideReviewThread records the user's intent to hide a review thread
// from the UI. It validates that the supplied root_comment_id matches
// an existing review_comment platform id on this PR before writing.
func (s *Server) hideReviewThread(
	ctx context.Context, input *hideReviewThreadInput,
) (*emptyOutput, error) {
	if input.Body.RootCommentID <= 0 {
		return nil, huma.Error400BadRequest("root_comment_id must be positive")
	}

	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	known, err := s.db.HasReviewCommentOnMR(ctx, mrID, input.Body.RootCommentID)
	if err != nil {
		return nil, huma.Error500InternalServerError("validate root_comment_id: " + err.Error())
	}
	if !known {
		return nil, huma.Error400BadRequest(
			"root_comment_id does not match any review comment on this pull request",
		)
	}

	if err := s.db.UpsertHiddenReviewThread(
		ctx, mrID, input.Body.RootCommentID, time.Now().UTC(),
	); err != nil {
		return nil, huma.Error500InternalServerError("hide thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// unhideReviewThread clears the user's hide for a thread. Idempotent.
func (s *Server) unhideReviewThread(
	ctx context.Context, input *unhideReviewThreadInput,
) (*emptyOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if err := s.db.DeleteHiddenReviewThread(ctx, mrID, input.RootCommentID); err != nil {
		return nil, huma.Error500InternalServerError("unhide thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}
