package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
)

const draftChatMessageMaxBytes = 8192

type draftMessageResponse struct {
	ID          int64   `json:"id"`
	Author      string  `json:"author"`
	Kind        string  `json:"kind"`
	Body        string  `json:"body"`
	PrevBody    string  `json:"prev_body,omitempty"`
	Applied     bool    `json:"applied"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	RawJSON     string  `json:"raw_json,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

type draftMessagesOutput struct {
	Body struct {
		Messages []draftMessageResponse `json:"messages"`
	}
}

type draftMessageOutput struct{ Body draftMessageResponse }

type draftPathInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
}

type sendDraftMessageInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
	Body    struct {
		Body  string `json:"body"`
		Fresh bool   `json:"fresh,omitempty" doc:"Start a new Claude session instead of continuing the existing one"`
	}
}

type draftMessagePathInput struct {
	Owner     string `path:"owner"`
	Name      string `path:"name"`
	Number    int    `path:"number"`
	DraftID   string `path:"draft_id"`
	MessageID int64  `path:"message_id"`
}

type reviseDraftInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Number  int    `path:"number"`
	DraftID string `path:"draft_id"`
	Body    struct {
		Body string `json:"body"`
	}
}

func (s *Server) registerDraftChatRoutes(api huma.API) {
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}", s.getReviewDraft)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/messages", s.listDraftMessages)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/messages", s.sendDraftMessage)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/messages/{message_id}/cancel", s.cancelDraftMessage)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/messages/{message_id}/apply", s.applyDraftRevision)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/messages/{message_id}/revert", s.revertDraftRevision)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-drafts/{draft_id}/revisions", s.reviseDraft)
}

func toDraftMessageResponse(m db.ReviewDraftMessage) draftMessageResponse {
	return draftMessageResponse{
		ID: m.ID, Author: m.Author, Kind: m.Kind, Body: m.Body, PrevBody: m.PrevBody,
		Applied: m.Applied, Status: m.Status, Error: m.Error, RawJSON: m.RawJSON,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
		CompletedAt: rfc3339Ptr(m.CompletedAt),
	}
}

// draftForChat resolves the PR scope and loads the draft, mapping the
// misses to API errors.
func (s *Server) draftForChat(ctx context.Context, owner, name string, number int, draftID string) (*db.ReviewDraft, int64, string, error) {
	mrID, branch, err := s.resolveDraftScope(ctx, owner, name, number)
	if err != nil {
		return nil, 0, "", huma.Error404NotFound("pull request not found")
	}
	d, err := s.db.GetReviewDraft(ctx, mrID, branch, draftID)
	if err != nil {
		return nil, 0, "", huma.Error500InternalServerError("load draft: " + err.Error())
	}
	if d == nil {
		return nil, 0, "", huma.Error404NotFound("draft not found")
	}
	return d, mrID, branch, nil
}

func (s *Server) getReviewDraft(ctx context.Context, input *draftPathInput) (*reviewDraftOutput, error) {
	d, _, _, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID)
	if err != nil {
		return nil, err
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*d)}, nil
}

func (s *Server) draftMessages(ctx context.Context, draftID string) (*draftMessagesOutput, error) {
	ms, err := s.db.ListReviewDraftMessages(ctx, draftID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list messages: " + err.Error())
	}
	out := &draftMessagesOutput{}
	out.Body.Messages = make([]draftMessageResponse, 0, len(ms))
	for _, m := range ms {
		out.Body.Messages = append(out.Body.Messages, toDraftMessageResponse(m))
	}
	return out, nil
}

func (s *Server) listDraftMessages(ctx context.Context, input *draftPathInput) (*draftMessagesOutput, error) {
	if _, _, _, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID); err != nil {
		return nil, err
	}
	return s.draftMessages(ctx, input.DraftID)
}

func (s *Server) sendDraftMessage(ctx context.Context, input *sendDraftMessageInput) (*draftMessagesOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("Claude chat is not available: clone manager or worktree dir not configured")
	}
	if isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("chat on local drafts arrives with local Review with Claude")
	}
	text := strings.TrimSpace(input.Body.Body)
	if text == "" {
		return nil, huma.Error400BadRequest("message is required")
	}
	if len(text) > draftChatMessageMaxBytes {
		return nil, huma.Error400BadRequest("message too long")
	}
	d, mrID, _, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID)
	if err != nil {
		return nil, err
	}
	if d.State == "discarded" {
		return nil, huma.Error409Conflict("this suggestion was discarded")
	}
	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil || shas == nil || shas.DiffHeadSHA == "" {
		return nil, huma.Error409Conflict("PR's diff SHAs aren't synced yet; try refreshing the PR first")
	}
	pr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil || pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return nil, huma.Error500InternalServerError("cannot resolve middleman executable for the MCP server")
	}

	if input.Body.Fresh && d.ClaudeSessionID != "" {
		if err := s.db.SetReviewDraftChatSession(ctx, d.ID, "", ""); err != nil {
			return nil, huma.Error500InternalServerError("reset chat session: " + err.Error())
		}
		d.ClaudeSessionID, d.ChatWorktreePath = "", ""
	}
	var run *db.AIReviewRun
	if d.RunID != nil {
		if run, err = s.db.GetAIReviewRun(ctx, *d.RunID); err != nil {
			return nil, huma.Error500InternalServerError("load review run: " + err.Error())
		}
	}

	turn := aireview.DraftTurnInput{
		DraftID: d.ID, Owner: input.Owner, Name: input.Name, Number: input.Number,
		MCP: aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: input.Owner, Name: input.Name, Number: input.Number, DraftID: d.ID},
	}
	switch {
	case d.ClaudeSessionID != "" && d.ChatWorktreePath != "":
		turn.Mode, turn.ParentSessionID, turn.WorktreePath = aireview.DraftChatResume, d.ClaudeSessionID, d.ChatWorktreePath
		turn.CheckoutSHA = d.CommitSHA
		if run != nil && d.ChatWorktreePath == s.aiReview.ReviewRunPath(run.ID) {
			turn.CheckoutSHA = run.HeadSHA
		}
	case !input.Body.Fresh && run != nil && run.ClaudeSessionID != "":
		turn.Mode, turn.ParentSessionID = aireview.DraftChatFork, run.ClaudeSessionID
		turn.WorktreePath, turn.CheckoutSHA = s.aiReview.ReviewRunPath(run.ID), run.HeadSHA
	default:
		turn.Mode, turn.WorktreePath, turn.CheckoutSHA = aireview.DraftChatFresh, s.aiReview.DraftChatWorktreePath(d.ID), d.CommitSHA
	}
	if turn.CheckoutSHA == "" {
		turn.CheckoutSHA = shas.DiffHeadSHA
	}
	prContext := ""
	if turn.Mode == aireview.DraftChatFresh {
		prContext = s.prContextBlock(ctx, input.Owner, input.Name, mrID, shas, pr)
	}
	// Built when the turn starts: the reviewer may edit the comment, or an
	// earlier turn may revise it, while this one waits in the queue.
	draftID := d.ID
	turn.PromptFor = func(ctx context.Context) (string, error) {
		cur, err := s.db.GetReviewDraftByID(ctx, draftID)
		if err != nil {
			return "", err
		}
		if cur == nil {
			return "", errors.New("draft no longer exists")
		}
		lastReply, err := s.db.LastCompletedClaudeMessageAt(ctx, draftID)
		if err != nil {
			return "", err
		}
		editedSince := lastReply != nil && cur.BodyEditedAt != nil && cur.BodyEditedAt.After(*lastReply)
		return buildDraftChatPrompt(*cur, text, editedSince, prContext), nil
	}

	if _, err := s.db.InsertReviewDraftMessage(ctx, db.NewReviewDraftMessage{
		DraftID: d.ID, Author: "user", Kind: "message", Body: text, Status: "done",
	}); err != nil {
		return nil, huma.Error500InternalServerError("store message: " + err.Error())
	}
	reply, err := s.db.InsertReviewDraftMessage(ctx, db.NewReviewDraftMessage{
		DraftID: d.ID, Author: "claude", Kind: "message", Status: "queued",
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("store message: " + err.Error())
	}
	turn.MessageID = reply.ID
	s.aiReview.SubmitDraftTurn(turn)
	return s.draftMessages(ctx, d.ID)
}

// buildDraftChatPrompt frames one reviewer message about one comment.
func buildDraftChatPrompt(d db.ReviewDraft, message string, editedSince bool, prContext string) string {
	var b strings.Builder
	if prContext != "" {
		b.WriteString("Pull request context:\n")
		b.WriteString(prContext)
		b.WriteString("\n")
	}
	side := "new"
	if d.Side == "LEFT" {
		side = "old"
	}
	fmt.Fprintf(&b, "We are discussing one review comment on %s:%d (%s side).\n", d.Path, d.Line, side)
	b.WriteString("Current comment text:\n")
	b.WriteString(d.Body)
	b.WriteString("\n")
	if editedSince {
		b.WriteString("The reviewer edited the comment since your last reply; the text above is current.\n")
	}
	b.WriteString("\nThe reviewer says:\n")
	b.WriteString(message)
	b.WriteString("\n\nAnswer the reviewer. Do not change the comment unless the reviewer asks for a change; " +
		"when they do, call mcp__middleman__revise_comment with the complete new comment text, then say briefly what you changed.\n")
	return b.String()
}

func (s *Server) cancelDraftMessage(ctx context.Context, input *draftMessagePathInput) (*draftMessageOutput, error) {
	if _, _, _, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID); err != nil {
		return nil, err
	}
	m, err := s.db.GetReviewDraftMessage(ctx, input.MessageID)
	if err != nil {
		return nil, huma.Error500InternalServerError("load message: " + err.Error())
	}
	if m == nil || m.DraftID != input.DraftID {
		return nil, huma.Error404NotFound("message not found")
	}
	if s.aiReview != nil {
		err = s.aiReview.CancelDraftTurn(ctx, m.ID)
	} else {
		err = s.db.MarkDraftMessageCancelled(ctx, m.ID)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("cancel: " + err.Error())
	}
	m, err = s.db.GetReviewDraftMessage(ctx, input.MessageID)
	if err != nil || m == nil {
		return nil, huma.Error500InternalServerError("reload message")
	}
	return &draftMessageOutput{Body: toDraftMessageResponse(*m)}, nil
}

func (s *Server) revisionAction(
	ctx context.Context, input *draftMessagePathInput,
	fn func(context.Context, int64, string, int64) (*db.ReviewDraft, error),
) (*reviewDraftOutput, error) {
	_, mrID, branch, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID)
	if err != nil {
		return nil, err
	}
	m, err := s.db.GetReviewDraftMessage(ctx, input.MessageID)
	if err != nil {
		return nil, huma.Error500InternalServerError("load message: " + err.Error())
	}
	if m == nil || m.DraftID != input.DraftID {
		return nil, huma.Error404NotFound("message not found")
	}
	d, err := fn(ctx, mrID, branch, input.MessageID)
	if errors.Is(err, db.ErrInvalidDraftTransition) {
		return nil, huma.Error409Conflict("this revision can't be applied or reverted now")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("update draft: " + err.Error())
	}
	if d == nil {
		return nil, huma.Error404NotFound("draft not found")
	}
	return &reviewDraftOutput{Body: toReviewDraftResponse(*d)}, nil
}

func (s *Server) applyDraftRevision(ctx context.Context, input *draftMessagePathInput) (*reviewDraftOutput, error) {
	return s.revisionAction(ctx, input, s.db.ApplyRevisionMessage)
}

func (s *Server) revertDraftRevision(ctx context.Context, input *draftMessagePathInput) (*reviewDraftOutput, error) {
	return s.revisionAction(ctx, input, s.db.RevertRevisionMessage)
}

// reviseDraft is what Claude's revise_comment tool calls. It only works
// while a chat turn for the draft is running, and the revision rules
// decide whether the text replaces the comment or is offered.
func (s *Server) reviseDraft(ctx context.Context, input *reviseDraftInput) (*draftMessageOutput, error) {
	if err := validateDraftBody(input.Body.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_, mrID, branch, err := s.draftForChat(ctx, input.Owner, input.Name, input.Number, input.DraftID)
	if err != nil {
		return nil, err
	}
	running, err := s.db.RunningDraftMessage(ctx, input.DraftID)
	if err != nil {
		return nil, huma.Error500InternalServerError("load chat: " + err.Error())
	}
	if running == nil {
		return nil, huma.Error409Conflict("no conversation turn is in progress for this draft")
	}
	// The turn counts from when the reviewer sent the message: an edit made
	// while it waited in the queue is still an edit "during the turn".
	m, err := s.db.ApplyClaudeRevision(ctx, mrID, branch, input.DraftID, input.Body.Body, running.CreatedAt)
	switch {
	case errors.Is(err, db.ErrInvalidDraftTransition):
		return nil, huma.Error409Conflict("draft is not in a state that allows this")
	case errors.Is(err, db.ErrDraftNotFound):
		return nil, huma.Error404NotFound("draft not found")
	case err != nil:
		return nil, huma.Error500InternalServerError("revise draft: " + err.Error())
	}
	return &draftMessageOutput{Body: toDraftMessageResponse(m)}, nil
}
