package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/aireview"
	"github.com/wesm/middleman/internal/db"
)

// --- shared response shapes -------------------------------------------------

type aiThreadResponse struct {
	ID              int64     `json:"id"`
	MergeRequestID  int64     `json:"mr_id"`
	Path            string    `json:"path"`
	AnchorSide      string    `json:"anchor_side"`
	AnchorLine      int       `json:"anchor_line"`
	HunkStartLine   *int      `json:"hunk_start_line,omitempty"`
	HunkEndLine     *int      `json:"hunk_end_line,omitempty"`
	SelectionText   *string   `json:"selection_text,omitempty"`
	CommitSHA       string    `json:"commit_sha"`
	ClaudeSessionID *string   `json:"claude_session_id,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
}

type aiQuestionResponse struct {
	ID            int64      `json:"id"`
	ThreadID      int64      `json:"thread_id"`
	Question      string     `json:"question"`
	Answer        string     `json:"answer"`
	CitationsJSON string     `json:"citations_json"`
	Error         string     `json:"error,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type aiThreadDetailResponse struct {
	Thread    aiThreadResponse     `json:"thread"`
	Questions []aiQuestionResponse `json:"questions"`
}

func toThreadResponse(t db.AIThread) aiThreadResponse {
	r := aiThreadResponse{
		ID:             t.ID,
		MergeRequestID: t.MergeRequestID,
		Path:           t.Path,
		AnchorSide:     t.AnchorSide,
		AnchorLine:     t.AnchorLine,
		HunkStartLine:  t.HunkStartLine,
		HunkEndLine:    t.HunkEndLine,
		SelectionText:  t.SelectionText,
		CommitSHA:      t.CommitSHA,
		Status:         t.Status,
		CreatedAt:      t.CreatedAt.UTC(),
		ClosedAt:       utcPtr(t.ClosedAt),
	}
	if t.ClaudeSessionID != nil && *t.ClaudeSessionID != "" {
		r.ClaudeSessionID = t.ClaudeSessionID
	}
	return r
}

func toQuestionResponse(q db.AIQuestion) aiQuestionResponse {
	return aiQuestionResponse{
		ID:            q.ID,
		ThreadID:      q.ThreadID,
		Question:      q.Question,
		Answer:        q.Answer,
		CitationsJSON: q.CitationsJSON,
		Error:         q.Error,
		Status:        q.Status,
		CreatedAt:     q.CreatedAt.UTC(),
		StartedAt:     utcPtr(q.StartedAt),
		CompletedAt:   utcPtr(q.CompletedAt),
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

// --- inputs -----------------------------------------------------------------

type createAIThreadInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Path          string  `json:"path"              doc:"File path the question is about"`
		AnchorSide    string  `json:"anchor_side"       doc:"LEFT or RIGHT"`
		AnchorLine    int     `json:"anchor_line"       doc:"1-based line in the file at the anchor SHA"`
		HunkStartLine *int    `json:"hunk_start_line,omitempty" doc:"Optional start of the hunk the reviewer was looking at"`
		HunkEndLine   *int    `json:"hunk_end_line,omitempty"   doc:"Optional end of the hunk"`
		HunkText      string  `json:"hunk_text,omitempty"       doc:"Raw hunk contents, quoted into the prompt"`
		SelectionText *string `json:"selection_text,omitempty"  doc:"Text the reviewer selected"`
		CommitSHA     string  `json:"commit_sha"        doc:"Commit the question is anchored to"`
		Question      string  `json:"question"          doc:"Reviewer's question, free-form natural language"`
		PromptContext string  `json:"prompt_context,omitempty" doc:"Extra orientation text appended to the prompt (PR title, branch, etc.)"`
	}
}

type addAIQuestionInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Question string `json:"question"`
	}
}

type aiThreadPathInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
}

type aiQuestionPathInput struct {
	Owner      string `path:"owner"`
	Name       string `path:"name"`
	Number     int    `path:"number"`
	ThreadID   int64  `path:"thread_id"`
	QuestionID int64  `path:"question_id"`
}

type listAIThreadsInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	// SinceID lets the UI poll cheaply — return only questions newer
	// than this. Threads are always returned in full so the UI can
	// reconcile closed-state changes.
	SinceID int64 `query:"since_id"`
}

// --- outputs ----------------------------------------------------------------

type aiThreadCreatedOutput struct {
	Body struct {
		Thread   aiThreadResponse   `json:"thread"`
		Question aiQuestionResponse `json:"question"`
	}
}

type aiQuestionCreatedOutput struct {
	Body aiQuestionResponse
}

type aiThreadsListOutput struct {
	Body struct {
		Threads   []aiThreadResponse   `json:"threads"`
		Questions []aiQuestionResponse `json:"questions"`
	}
}

type aiThreadDetailOutput struct {
	Body aiThreadDetailResponse
}

type emptyOutput struct{}

// --- handlers ---------------------------------------------------------------

func (s *Server) createAIThread(ctx context.Context, input *createAIThreadInput) (*aiThreadCreatedOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available: clone manager or worktree dir not configured")
	}

	if err := validateAIThreadInput(input); err != nil {
		return nil, err
	}

	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	thread, question, err := s.aiReview.CreateThread(ctx, aireview.CreateThreadInput{
		MergeRequestID: mrID,
		Owner:          input.Owner,
		Name:           input.Name,
		Path:           input.Body.Path,
		AnchorSide:     input.Body.AnchorSide,
		AnchorLine:     input.Body.AnchorLine,
		HunkStartLine:  input.Body.HunkStartLine,
		HunkEndLine:    input.Body.HunkEndLine,
		HunkText:       input.Body.HunkText,
		SelectionText:  input.Body.SelectionText,
		CommitSHA:      input.Body.CommitSHA,
		Question:       input.Body.Question,
		PromptContext:  input.Body.PromptContext,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("create thread: " + err.Error())
	}

	out := &aiThreadCreatedOutput{}
	out.Body.Thread = toThreadResponse(thread)
	out.Body.Question = toQuestionResponse(question)
	return out, nil
}

func (s *Server) addAIQuestion(ctx context.Context, input *addAIQuestionInput) (*aiQuestionCreatedOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	if strings.TrimSpace(input.Body.Question) == "" {
		return nil, huma.Error400BadRequest("question is required")
	}

	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}

	q, err := s.aiReview.AddFollowUp(ctx, input.ThreadID, input.Body.Question)
	if err != nil {
		return nil, huma.Error400BadRequest("add follow-up: " + err.Error())
	}
	return &aiQuestionCreatedOutput{Body: toQuestionResponse(q)}, nil
}

func (s *Server) listAIThreads(ctx context.Context, input *listAIThreadsInput) (*aiThreadsListOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	threads, err := s.db.ListAIThreadsForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list threads: " + err.Error())
	}
	questions, err := s.db.ListAIQuestionsForMR(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list questions: " + err.Error())
	}

	out := &aiThreadsListOutput{}
	out.Body.Threads = make([]aiThreadResponse, 0, len(threads))
	for _, t := range threads {
		out.Body.Threads = append(out.Body.Threads, toThreadResponse(t))
	}
	out.Body.Questions = make([]aiQuestionResponse, 0, len(questions))
	for _, q := range questions {
		if q.ID <= input.SinceID {
			continue
		}
		out.Body.Questions = append(out.Body.Questions, toQuestionResponse(q))
	}
	return out, nil
}

func (s *Server) getAIThread(ctx context.Context, input *aiThreadPathInput) (*aiThreadDetailOutput, error) {
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}
	questions, err := s.db.ListAIQuestionsForThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list questions: " + err.Error())
	}
	out := &aiThreadDetailOutput{}
	out.Body.Thread = toThreadResponse(thread)
	out.Body.Questions = make([]aiQuestionResponse, 0, len(questions))
	for _, q := range questions {
		out.Body.Questions = append(out.Body.Questions, toQuestionResponse(q))
	}
	return out, nil
}

func (s *Server) deleteAIThread(ctx context.Context, input *aiThreadPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}
	if err := s.aiReview.DeleteThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("delete thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}

func (s *Server) deleteAIQuestion(ctx context.Context, input *aiQuestionPathInput) (*emptyOutput, error) {
	if s.aiReview == nil {
		return nil, huma.Error503ServiceUnavailable("AI Q&A not available")
	}
	q, err := s.db.GetAIQuestion(ctx, input.QuestionID)
	if err != nil {
		return nil, huma.Error404NotFound("question not found")
	}
	if q.ThreadID != input.ThreadID {
		return nil, huma.Error404NotFound("question not in thread")
	}
	thread, err := s.db.GetAIThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error404NotFound("thread not found")
	}
	if err := checkThreadOwnership(ctx, s, input.Owner, input.Name, input.Number, thread); err != nil {
		return nil, err
	}

	// If still in-flight, cancel before deleting.
	if q.Status == "running" || q.Status == "queued" {
		_ = s.aiReview.CancelQuestion(ctx, q.ID)
	}
	if err := s.db.DeleteAIQuestion(ctx, q.ID); err != nil {
		return nil, huma.Error500InternalServerError("delete question: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// checkThreadOwnership ensures the thread belongs to the PR in the URL.
// Prevents thread IDs from being used across PRs via URL tampering.
func checkThreadOwnership(ctx context.Context, s *Server, owner, name string, number int, thread db.AIThread) error {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{owner: owner, name: name, number: number})
	if err != nil {
		return huma.Error404NotFound("pull request not found")
	}
	if thread.MergeRequestID != mrID {
		return huma.Error404NotFound("thread not in this pull request")
	}
	return nil
}

func validateAIThreadInput(input *createAIThreadInput) error {
	if input.Body.Path == "" {
		return huma.Error400BadRequest("path is required")
	}
	switch input.Body.AnchorSide {
	case "LEFT", "RIGHT":
	default:
		return huma.Error400BadRequest("anchor_side must be LEFT or RIGHT")
	}
	if input.Body.AnchorLine <= 0 {
		return huma.Error400BadRequest("anchor_line must be positive")
	}
	if strings.TrimSpace(input.Body.Question) == "" {
		return huma.Error400BadRequest("question is required")
	}
	if input.Body.CommitSHA == "" {
		return huma.Error400BadRequest("commit_sha is required")
	}
	return nil
}

// Keep parseInt available for callers that may want to interpret
// thread IDs out of bodies; unused right now but prevents a dead import.
var _ = json.Marshal
var _ = http.MethodPost
var _ = fmt.Sprintf
