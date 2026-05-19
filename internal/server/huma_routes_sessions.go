package server

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/worktrees"
)

// Session-conversation routes for local worktree drafts. Live at
// the PR-shaped path so the rest of middleman keeps its single
// addressing convention — owner=="local" guards the local-only
// behavior. Non-local callers get a clean 4xx.

type sessionTurnResponse struct {
	ID           int64  `json:"id"`
	TurnType     string `json:"turn_type" doc:"review_feedback | user_message | claude_response | state"`
	Content      string `json:"content"`
	Status       string `json:"status" doc:"For claude_response: queued | running | done | failed | cancelled. User turns are always done."`
	Error        string `json:"error,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
	CreatedAt    string `json:"created_at" doc:"UTC RFC3339 timestamp"`
}

type sessionResponse struct {
	ID              int64  `json:"id"`
	Status          string `json:"status"`
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	StartedAt       string `json:"started_at"`
	LastActivityAt  string `json:"last_activity_at"`
}

type getSessionResponse struct {
	Session *sessionResponse      `json:"session"`
	Turns   []sessionTurnResponse `json:"turns"`
}

type getSessionInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type getSessionOutput struct {
	Body getSessionResponse
}

type submitTurnInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Type         string `json:"type" doc:"review_feedback or user_message"`
		Content      string `json:"content"`
		MetadataJSON string `json:"metadata_json,omitempty"`
	}
}

type submitTurnOutput struct {
	Body struct {
		UserTurn     sessionTurnResponse `json:"user_turn"`
		ResponseTurn sessionTurnResponse `json:"response_turn"`
		Session      sessionResponse     `json:"session"`
	}
}

type cancelTurnInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	TurnID int64  `path:"turn_id"`
}

func (s *Server) registerSessionRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/session", s.getWorktreeSession)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/session/turns", s.submitWorktreeSessionTurn)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/session/turns/{turn_id}/cancel", s.cancelWorktreeSessionTurn)
}

// getWorktreeSession returns the active session (if any) and all
// its turns. Used by the Activity tab to render the conversation
// and by polling to track in-flight turns.
func (s *Server) getWorktreeSession(
	ctx context.Context, input *getSessionInput,
) (*getSessionOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest(
			"sessions are local-worktree only; this endpoint isn't valid for GitHub PRs",
		)
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	sess, err := s.db.GetActiveWorktreeSession(ctx, w.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return &getSessionOutput{Body: getSessionResponse{Turns: []sessionTurnResponse{}}}, nil
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("get active session: " + err.Error())
	}

	turns, err := s.db.ListWorktreeSessionTurns(ctx, sess.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list session turns: " + err.Error())
	}
	respTurns := make([]sessionTurnResponse, 0, len(turns))
	for _, t := range turns {
		respTurns = append(respTurns, toSessionTurnResponse(t))
	}
	respSess := toSessionResponse(sess)
	return &getSessionOutput{Body: getSessionResponse{
		Session: &respSess,
		Turns:   respTurns,
	}}, nil
}

// submitWorktreeSessionTurn inserts a user turn + a queued
// claude_response turn, then spawns Claude in the background. The
// session is created lazily on first call.
func (s *Server) submitWorktreeSessionTurn(
	ctx context.Context, input *submitTurnInput,
) (*submitTurnOutput, error) {
	if s.sessionRunner == nil {
		return nil, huma.Error503ServiceUnavailable("sessions not available: runner not configured")
	}
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("sessions are local-worktree only")
	}
	turnType := input.Body.Type
	if turnType != "review_feedback" && turnType != "user_message" {
		return nil, huma.Error400BadRequest("invalid turn type: " + turnType)
	}
	if input.Body.Content == "" {
		return nil, huma.Error400BadRequest("content is required")
	}

	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	// Ensure an active session exists.
	sess, err := s.db.GetActiveWorktreeSession(ctx, w.ID)
	isFirstTurn := false
	if errors.Is(err, sql.ErrNoRows) {
		sess, err = s.db.CreateWorktreeSession(ctx, w.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("create session: " + err.Error())
		}
		isFirstTurn = true
	} else if err != nil {
		return nil, huma.Error500InternalServerError("get session: " + err.Error())
	} else if sess.ClaudeSessionID == "" {
		// Session row exists but Claude hasn't ack'd it yet — treat
		// this as a first turn so the prompt re-primes context.
		isFirstTurn = true
	}

	// Pull worktree context for the prompt.
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, _ := worktrees.ResolveBase(ctx, w.Path, baseRef)

	res, err := s.sessionRunner.SubmitTurn(ctx, aireview.SubmitTurnInput{
		SessionID:            sess.ID,
		WorktreePath:         w.Path,
		Branch:               w.Branch,
		BaseRef:              base.Ref,
		BaseSHA:              base.SHA,
		HeadSHA:              w.HeadSHA,
		UserTurnType:         turnType,
		UserTurnContent:      input.Body.Content,
		UserTurnMetadataJSON: input.Body.MetadataJSON,
		IsFirstTurn:          isFirstTurn,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("submit turn: " + err.Error())
	}

	out := &submitTurnOutput{}
	out.Body.UserTurn = toSessionTurnResponse(res.UserTurn)
	out.Body.ResponseTurn = toSessionTurnResponse(res.ResponseTurn)
	out.Body.Session = toSessionResponse(sess)
	return out, nil
}

// cancelWorktreeSessionTurn kills the subprocess for a running
// claude_response turn and flips its status to cancelled.
func (s *Server) cancelWorktreeSessionTurn(
	ctx context.Context, input *cancelTurnInput,
) (*emptyOutput, error) {
	if s.sessionRunner == nil {
		return nil, huma.Error503ServiceUnavailable("sessions not available")
	}
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("sessions are local-worktree only")
	}
	if err := s.sessionRunner.CancelTurn(ctx, input.TurnID); err != nil {
		return nil, huma.Error500InternalServerError("cancel turn: " + err.Error())
	}
	return &emptyOutput{}, nil
}

func toSessionResponse(s db.WorktreeSession) sessionResponse {
	return sessionResponse{
		ID:              s.ID,
		Status:          s.Status,
		ClaudeSessionID: s.ClaudeSessionID,
		StartedAt:       s.StartedAt.UTC().Format(time.RFC3339),
		LastActivityAt:  s.LastActivityAt.UTC().Format(time.RFC3339),
	}
}

func toSessionTurnResponse(t db.WorktreeSessionTurn) sessionTurnResponse {
	return sessionTurnResponse{
		ID:           t.ID,
		TurnType:     t.TurnType,
		Content:      t.Content,
		Status:       t.Status,
		Error:        t.Error,
		MetadataJSON: t.MetadataJSON,
		CreatedAt:    t.CreatedAt.UTC().Format(time.RFC3339),
	}
}
