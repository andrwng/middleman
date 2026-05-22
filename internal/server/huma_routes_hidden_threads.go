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
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	if input.Body.RootCommentID <= 0 {
		return nil, huma.Error400BadRequest("root_comment_id must be positive")
	}

	known, err := s.reviewCommentExistsOnMR(ctx, mrID, input.Body.RootCommentID)
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

// reviewCommentExistsOnMR returns true when (mrID, platformID) refers
// to a review_comment event we've synced. Used as a cheap sanity check
// for write paths that take a platform comment id. COUNT(*) always
// returns a single row (0 when nothing matches), so the only Scan
// error is a real failure.
func (s *Server) reviewCommentExistsOnMR(
	ctx context.Context, mrID, platformID int64,
) (bool, error) {
	var count int
	err := s.db.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_mr_events
		  WHERE merge_request_id = ? AND event_type = 'review_comment'
		        AND platform_id = ?`,
		mrID, platformID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
