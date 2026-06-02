package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	gh "github.com/google/go-github/v84/github"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/worktrees"
)

type listPullsInput struct {
	Repo    string `query:"repo"`
	State   string `query:"state"`
	Kanban  string `query:"kanban"`
	Starred bool   `query:"starred"`
	Q       string `query:"q"`
	Limit   int    `query:"limit"`
	Offset  int    `query:"offset"`
}

type listPullsOutput struct {
	Body []mergeRequestResponse
}

type repoNumberInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type getPullOutput struct {
	Body mergeRequestDetailResponse
}

type getMRImportMetadataOutput struct {
	Body mrImportMetadataResponse
}

type setKanbanStateInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Status string `json:"status"`
	}
}

type statusOnlyOutput struct {
	Status int `status:"200"`
}

type postCommentInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Body string `json:"body"`
	}
}

type postCommentOutput struct {
	Status int `status:"201"`
	Body   db.MREvent
}

type listIssuesInput struct {
	Repo    string `query:"repo"`
	State   string `query:"state"`
	Starred bool   `query:"starred"`
	Q       string `query:"q"`
	Limit   int    `query:"limit"`
	Offset  int    `query:"offset"`
}

type listIssuesOutput struct {
	Body []issueResponse
}

type getIssueOutput struct {
	Body issueDetailResponse
}

type postIssueCommentInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Body string `json:"body"`
	}
}

type postIssueCommentOutput struct {
	Status int `status:"201"`
	Body   db.IssueEvent
}

type starredInput struct {
	Body starredRequest
}

type getRepoInput struct {
	Owner string `path:"owner"`
	Name  string `path:"name"`
}

type getRepoOutput struct {
	Body db.Repo
}

type commentAutocompleteInput struct {
	Owner   string `path:"owner"`
	Name    string `path:"name"`
	Trigger string `query:"trigger"`
	Q       string `query:"q"`
	Limit   int    `query:"limit"`
}

type commentAutocompleteOutput struct {
	Body commentAutocompleteResponse
}

type approvePRInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Body string `json:"body"`
	}
}

type submitReviewComment struct {
	Path      string `json:"path,omitempty"       doc:"File path the comment applies to (required unless in_reply_to is set)"`
	Line      int    `json:"line,omitempty"       doc:"1-based line in the file (required unless in_reply_to is set)"`
	Side      string `json:"side,omitempty"       doc:"LEFT or RIGHT; RIGHT when omitted"`
	StartLine int    `json:"start_line,omitempty" doc:"For multi-line comments; omit for single-line"`
	Body      string `json:"body"                 doc:"Comment body (markdown)"`
	CommitID  string `json:"commit_id,omitempty"  doc:"Commit SHA this comment is anchored to; falls back to the review-level commit_id when empty"`
	InReplyTo int64  `json:"in_reply_to,omitempty" doc:"Upstream comment id this reply threads under; when set, path/line/side/commit_id are inherited from the parent"`
}

type submitReviewInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Event    string                `json:"event"             doc:"APPROVE, REQUEST_CHANGES, or COMMENT"`
		Body     string                `json:"body,omitempty"    doc:"Optional top-level review body"`
		CommitID string                `json:"commit_id,omitempty" doc:"PR head SHA; required when comments anchor to a specific commit"`
		Comments []submitReviewComment `json:"comments,omitempty" doc:"Inline review comments to include"`
	}
}

type submitReviewResponseBody struct {
	ReviewID int64  `json:"review_id"`
	State    string `json:"state"`
}

type submitReviewOutput struct {
	Body submitReviewResponseBody
}

type actionStatusBody struct {
	Status        string `json:"status"`
	ApprovedCount int    `json:"approved_count,omitempty"`
}

type actionStatusOutput struct {
	Body actionStatusBody
}

type mergePRInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		CommitTitle   string `json:"commit_title"`
		CommitMessage string `json:"commit_message"`
		Method        string `json:"method"`
	}
}

type mergePRBody struct {
	Merged  bool   `json:"merged"`
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

type mergePROutput struct {
	Body mergePRBody
}

type editPRContentInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Title *string `json:"title,omitempty"`
		Body  *string `json:"body,omitempty"`
	}
}

type editPRContentOutput struct {
	Body mergeRequestDetailResponse
}

type githubStateInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		State string `json:"state"`
	}
}

type githubStateOutput struct {
	Body struct {
		State string `json:"state"`
	}
}

type listReposOutput struct {
	Body []db.Repo
}

type acceptedOutput struct {
	Status int `status:"202"`
}

type syncPROutput struct {
	Body mergeRequestDetailResponse
}

type syncIssueOutput struct {
	Body issueDetailResponse
}

type resolveItemOutput struct {
	Body resolveItemResponse
}

type syncStatusOutput struct {
	Body *ghclient.SyncStatus
}

type rateLimitsOutput struct {
	Body rateLimitsResponse
}

type listStacksInput struct {
	Repo string `query:"repo"`
}

type listStacksOutput struct {
	Body []stackResponse
}

type getStackForPROutput struct {
	Body stackContextResponse
}

type createWorkspaceInput struct {
	Body struct {
		PlatformHost string `json:"platform_host"`
		Owner        string `json:"owner"`
		Name         string `json:"name"`
		MRNumber     int    `json:"mr_number"`
	}
}

type getWorkspaceInput struct {
	ID string `path:"id"`
}

type deleteWorkspaceInput struct {
	ID    string `path:"id"`
	Force bool   `query:"force"`
}

type listWorkspacesOutput struct {
	Body struct {
		Workspaces []workspaceResponse `json:"workspaces"`
	}
}

type getWorkspaceOutput struct {
	Body workspaceResponse
}

type createWorkspaceOutput struct {
	Status int `status:"202"`
	Body   workspaceResponse
}

type listActivityInput struct {
	Repo   string   `query:"repo"`
	Types  []string `query:"types"`
	Search string   `query:"search"`
	After  string   `query:"after"`
	Since  string   `query:"since"`
}

type listActivityOutput struct {
	Body activityResponse
}

func apiConfig(basePath string) huma.Config {
	config := huma.DefaultConfig("middleman API", "0.1.0")
	config.OpenAPIPath = "/openapi"
	config.DocsPath = "/docs"
	config.SchemasPath = "/schemas"
	config.Servers = []*huma.Server{{
		URL: strings.TrimSuffix(basePath, "/") + "/api/v1",
	}}
	return config
}

func (s *Server) registerAPI(api huma.API) {
	huma.Get(api, "/activity", s.listActivity)
	huma.Get(api, "/pulls", s.listPulls)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}", s.getPull)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/import-metadata", s.getMRImportMetadata)
	huma.Register(api, huma.Operation{
		OperationID:   "set-kanban-state",
		Method:        http.MethodPut,
		Path:          "/repos/{owner}/{name}/pulls/{number}/state",
		DefaultStatus: http.StatusOK,
	}, s.setKanbanState)
	huma.Register(api, huma.Operation{
		OperationID:   "edit-pr-content",
		Method:        http.MethodPatch,
		Path:          "/repos/{owner}/{name}/pulls/{number}",
		DefaultStatus: http.StatusOK,
	}, s.editPRContent)
	huma.Register(api, huma.Operation{
		OperationID:   "post-pr-comment",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/pulls/{number}/comments",
		DefaultStatus: http.StatusCreated,
	}, s.postComment)

	huma.Get(api, "/issues", s.listIssues)
	huma.Get(api, "/repos/{owner}/{name}/issues/{number}", s.getIssue)
	huma.Register(api, huma.Operation{
		OperationID:   "post-issue-comment",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/issues/{number}/comments",
		DefaultStatus: http.StatusCreated,
	}, s.postIssueComment)

	huma.Post(api, "/repos/{owner}/{name}/items/{number}/resolve", s.resolveItem)

	huma.Register(api, huma.Operation{
		OperationID:   "set-starred",
		Method:        http.MethodPut,
		Path:          "/starred",
		DefaultStatus: http.StatusOK,
	}, s.setStarred)
	huma.Register(api, huma.Operation{
		OperationID:   "unset-starred",
		Method:        http.MethodDelete,
		Path:          "/starred",
		DefaultStatus: http.StatusOK,
	}, s.unsetStarred)

	huma.Get(api, "/author-groups", s.listAuthorGroups)
	huma.Post(api, "/author-groups", s.createAuthorGroup)
	huma.Put(api, "/author-groups/{id}", s.updateAuthorGroup)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-author-group",
		Method:        http.MethodDelete,
		Path:          "/author-groups/{id}",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteAuthorGroup)

	huma.Get(api, "/repos", s.listRepos)
	huma.Get(api, "/worktrees", s.listWorktrees)
	huma.Get(api, "/worktrees/running-turns", s.listWorktreesRunningTurns)
	huma.Get(api, "/worktrees/{id}/changed-files", s.getWorktreeChangedFiles)
	huma.Get(api, "/worktrees/{id}/diff", s.getWorktreeDiff)
	huma.Get(api, "/local/resolve", s.resolveLocalWorktreeByPath)
	huma.Get(api, "/repos/{owner}/{name}", s.getRepo)
	huma.Get(api, "/repos/{owner}/{name}/comment-autocomplete", s.getCommentAutocomplete)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/approve", s.approvePR)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review", s.submitReview)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/approve-workflows", s.approveWorkflows)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ready-for-review", s.readyForReview)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/merge", s.mergePR)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/sync", s.syncPR)
	huma.Post(api, "/repos/{owner}/{name}/issues/{number}/sync", s.syncIssue)
	huma.Register(api, huma.Operation{
		OperationID:   "set-pr-github-state",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/pulls/{number}/github-state",
		DefaultStatus: http.StatusOK,
	}, s.setPRGitHubState)
	huma.Register(api, huma.Operation{
		OperationID:   "set-issue-github-state",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/issues/{number}/github-state",
		DefaultStatus: http.StatusOK,
	}, s.setIssueGitHubState)
	huma.Register(api, huma.Operation{
		OperationID:   "trigger-sync",
		Method:        http.MethodPost,
		Path:          "/sync",
		DefaultStatus: http.StatusAccepted,
	}, s.triggerSync)
	huma.Register(api, huma.Operation{
		OperationID:   "sync-repo",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/sync",
		DefaultStatus: http.StatusAccepted,
	}, s.syncRepo)
	huma.Get(api, "/sync/status", s.syncStatus)
	huma.Get(api, "/me", s.getViewer)
	huma.Get(api, "/ai/sessions", s.getAISessions)
	huma.Get(api, "/rate-limits", s.getRateLimits)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/commits", s.getCommits)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/patchsets", s.listPatchsets)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/diff", s.getDiff)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/files", s.getFiles)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/blob-range", s.getBlobRange)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/blob", s.getBlob)
	huma.Post(api, "/repos/{owner}/{name}/resolve-files", s.resolveFiles)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/notes", s.getPRNotes)
	huma.Put(api, "/repos/{owner}/{name}/pulls/{number}/notes", s.putPRNotes)
	huma.Register(api, huma.Operation{
		OperationID:   "hide-review-thread",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
		DefaultStatus: http.StatusNoContent,
	}, s.hideReviewThread)
	huma.Register(api, huma.Operation{
		OperationID:   "unhide-review-thread",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
		DefaultStatus: http.StatusNoContent,
	}, s.unhideReviewThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-threads", s.createAIThread)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/ai-threads", s.listAIThreads)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/ai-threads/{thread_id}", s.getAIThread)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-threads/{thread_id}/questions", s.addAIQuestion)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-ai-thread",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/ai-threads/{thread_id}",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteAIThread)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-ai-question",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/ai-threads/{thread_id}/questions/{question_id}",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteAIQuestion)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/ai-brief", s.createAIBrief)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/ai-brief", s.getAIBrief)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-ai-brief",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/ai-brief",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteAIBrief)
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/commits/{sha}/analyze", s.createAICommitAnalysis)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/commits/{sha}/analyze", s.getAICommitAnalysis)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-ai-commit-analysis",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/commits/{sha}/analyze",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteAICommitAnalysis)
	huma.Get(api, "/stacks", s.listStacks)
	huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/stack", s.getStackForPR)

	huma.Register(api, huma.Operation{
		OperationID:   "create-workspace",
		Method:        http.MethodPost,
		Path:          "/workspaces",
		DefaultStatus: http.StatusAccepted,
	}, s.createWorkspace)
	huma.Get(api, "/workspaces", s.listWorkspaces)
	huma.Get(api, "/workspaces/{id}", s.getWorkspace)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-workspace",
		Method:        http.MethodDelete,
		Path:          "/workspaces/{id}",
		DefaultStatus: http.StatusNoContent,
	}, s.deleteWorkspace)

	s.registerSessionRoutes(api)
	s.registerReviewThreadRoutes(api)
}

func NewOpenAPI() *huma.OpenAPI {
	mux := http.NewServeMux()
	s := &Server{}
	api := humago.NewWithPrefix(mux, "/api/v1", apiConfig("/"))
	s.registerAPI(api)
	return api.OpenAPI()
}

func (s *Server) listPulls(ctx context.Context, input *listPullsInput) (*listPullsOutput, error) {
	if input.State != "" {
		valid := map[string]bool{
			"open": true, "closed": true, "all": true,
		}
		if !valid[input.State] {
			return nil, huma.Error400BadRequest(
				"state must be one of: open, closed, all",
			)
		}
	}

	opts := db.ListMergeRequestsOpts{
		State:       input.State,
		KanbanState: input.Kanban,
		Starred:     input.Starred,
		Search:      input.Q,
		Limit:       input.Limit,
		Offset:      input.Offset,
	}
	if owner, name := parseRepoFilter(input.Repo); owner != "" {
		opts.RepoOwner = owner
		opts.RepoName = name
	}

	mrs, err := s.db.ListMergeRequests(ctx, opts)
	if err != nil {
		return nil, huma.Error500InternalServerError("list pulls failed")
	}

	repoByID, err := s.lookupRepoMap(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("repo lookup failed")
	}

	mrIDs := make([]int64, len(mrs))
	for i, mr := range mrs {
		mrIDs[i] = mr.ID
	}
	links, err := s.db.GetWorktreeLinksForMRs(ctx, mrIDs)
	if err != nil {
		return nil, huma.Error500InternalServerError("load worktree links failed")
	}
	linksByMR := indexWorktreeLinksByMR(links)

	reviewersByMR, raErr := s.db.ReviewAuthorsForMRs(ctx, mrIDs)
	if raErr != nil {
		return nil, huma.Error500InternalServerError("load review authors failed")
	}

	// Per-PR review state for the configured viewer. Resolve the
	// viewer eagerly on cache miss (one round-trip to GitHub per
	// server lifetime) so the first /pulls after a server restart
	// returns real state instead of "unreviewed" for everything,
	// which would otherwise let the legacy "REVIEW" chip fall
	// through on PRs the viewer had already reviewed. If GitHub
	// is unreachable we proceed with empty viewer; the frontend
	// is still polling and will recover on the next list refresh.
	viewer, _, _ := s.resolveViewer(ctx)
	states, _ := s.db.ReviewStatesForMRs(ctx, mrIDs, viewer)

	out := make([]mergeRequestResponse, 0, len(mrs))
	for _, mr := range mrs {
		rp, ok := repoByID[mr.RepoID]
		if !ok {
			continue
		}
		wl := linksByMR[mr.ID]
		if wl == nil {
			wl = []worktreeLinkResponse{}
		}
		reviewers := reviewersByMR[mr.ID]
		if reviewers == nil {
			reviewers = []string{}
		}
		resp := mergeRequestResponse{
			MergeRequest:   mr,
			RepoOwner:      rp.Owner,
			RepoName:       rp.Name,
			PlatformHost:   rp.PlatformHost,
			WorktreeLinks:  wl,
			DetailLoaded:   mr.DetailFetchedAt != nil,
			ReviewerLogins: reviewers,
		}
		if viewer != "" {
			if st, ok := states[mr.ID]; ok {
				resp.ReviewState = st.State
			} else {
				resp.ReviewState = "unreviewed"
			}
		}
		if mr.DetailFetchedAt != nil {
			resp.DetailFetchedAt = formatUTCRFC3339(*mr.DetailFetchedAt)
		}
		out = append(out, resp)
	}

	return &listPullsOutput{Body: out}, nil
}

func (s *Server) getPull(ctx context.Context, input *repoNumberInput) (*getPullOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getPullLocal(ctx, input)
	}
	mr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get pull request failed")
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	body, err := s.buildPullDetailResponse(ctx, mr, workflowDBOnly)
	if err != nil {
		return nil, err
	}

	return &getPullOutput{Body: body}, nil
}

func (s *Server) buildPullDetailResponse(
	ctx context.Context,
	mr *db.MergeRequest,
	wfMode workflowMode,
) (mergeRequestDetailResponse, error) {
	events, err := s.db.ListMREvents(ctx, mr.ID)
	if err != nil {
		return mergeRequestDetailResponse{}, huma.Error500InternalServerError("list mr events failed")
	}
	if events == nil {
		events = []db.MREvent{}
	}

	dbLinks, err := s.db.GetWorktreeLinksForMR(ctx, mr.ID)
	if err != nil {
		return mergeRequestDetailResponse{}, huma.Error500InternalServerError(
			"load worktree links failed",
		)
	}

	repo, err := s.db.GetRepoByID(ctx, mr.RepoID)
	if err != nil || repo == nil {
		return mergeRequestDetailResponse{}, huma.Error500InternalServerError(
			"load repo failed",
		)
	}
	hidden, err := s.db.ActiveHiddenReviewThreadRoots(ctx, mr.ID, events)
	if err != nil {
		return mergeRequestDetailResponse{}, huma.Error500InternalServerError(
			"compute hidden review threads failed",
		)
	}
	if hidden == nil {
		hidden = []int64{}
	}

	resp := mergeRequestDetailResponse{
		MergeRequest:        mr,
		Events:              events,
		RepoOwner:           repo.Owner,
		RepoName:            repo.Name,
		PlatformHost:        repo.PlatformHost,
		WorktreeLinks:       toWorktreeLinkResponses(dbLinks),
		WorkflowApproval:    s.workflowApprovalState(ctx, repo.Owner, repo.Name, mr, wfMode),
		Warnings:            s.diffWarnings(mr),
		DetailLoaded:        mr.DetailFetchedAt != nil,
		HiddenThreadRootIDs: hidden,
	}
	if mr.DetailFetchedAt != nil {
		resp.DetailFetchedAt = formatUTCRFC3339(*mr.DetailFetchedAt)
	}

	if s.workspaces != nil {
		wsRef, wsErr := s.workspaces.GetByMR(
			ctx, repo.PlatformHost, repo.Owner, repo.Name, mr.Number,
		)
		if wsErr == nil && wsRef != nil {
			resp.Workspace = &workspaceMRRef{
				ID:     wsRef.ID,
				Status: wsRef.Status,
			}
		}
	}

	return resp, nil
}

// diffWarnings returns warnings inferred from the persisted PR row. The
// resolveItem and syncPR paths log diff sync failures via slog and (in
// syncPR's case) surface them in the immediate response, but neither
// persists the failure. Without inferring from the row state, a client
// that lands on the PR detail page after resolveItem (which has no
// warnings field) or after a refresh would see no indication that the
// diff is unavailable. We therefore emit a sanitized warning whenever a
// PR that should have diff data is missing it.
func (s *Server) diffWarnings(mr *db.MergeRequest) []string {
	if mr == nil {
		return nil
	}
	if !s.syncer.HasDiffSync() {
		return nil
	}
	// Closed (including merged) PRs also get diff SHAs populated via
	// fetchAndUpdateClosed, so the warning logic must cover every state
	// that getDiff would render, not just open and merged.
	if mr.DiffHeadSHA == "" {
		return []string{"Diff data is unavailable for this pull request."}
	}
	shas := db.DiffSHAs{
		PlatformHeadSHA: mr.PlatformHeadSHA,
		PlatformBaseSHA: mr.PlatformBaseSHA,
		DiffHeadSHA:     mr.DiffHeadSHA,
		DiffBaseSHA:     mr.DiffBaseSHA,
		State:           mr.State,
	}
	if shas.Stale() {
		return []string{"Diff data is out of date for this pull request."}
	}
	return nil
}

// workflowMode controls which live GitHub calls workflowApprovalState makes.
type workflowMode int

const (
	// workflowDBOnly makes no live calls. Used by GET detail.
	workflowDBOnly workflowMode = iota
	// workflowCheckRuns reads PR state from DB but fetches
	// workflow runs live. Used by POST sync (PR just synced,
	// no need to re-fetch it).
	workflowCheckRuns
	// workflowFull fetches the PR live and then workflow runs.
	// Used by the approve-workflows action.
	workflowFull
)

func (s *Server) workflowApprovalState(
	ctx context.Context,
	owner, name string,
	mr *db.MergeRequest,
	mode workflowMode,
) workflowApprovalResponse {
	if mode == workflowDBOnly {
		return workflowApprovalResponse{}
	}

	client, err := s.syncer.ClientForRepo(owner, name)
	if err != nil {
		return workflowApprovalResponse{}
	}

	var currentState, headSHA, headRepoFullName, headRef string
	if mode == workflowFull {
		pr, prErr := client.GetPullRequest(ctx, owner, name, mr.Number)
		if prErr != nil || pr == nil {
			return workflowApprovalResponse{}
		}
		currentState = pr.GetState()
		headSHA = pr.GetHead().GetSHA()
		headRepoFullName = pr.GetHead().GetRepo().GetFullName()
		headRef = pr.GetHead().GetRef()
	} else {
		currentState = mr.State
		headSHA = mr.PlatformHeadSHA
		headRepoFullName = ghclient.ParseHeadRepoFullName(mr.HeadRepoCloneURL)
		headRef = mr.HeadBranch
	}

	if currentState != "open" || headSHA == "" {
		return workflowApprovalResponse{Checked: true}
	}

	runs, err := client.ListWorkflowRunsForHeadSHA(ctx, owner, name, headSHA)
	if err != nil {
		return workflowApprovalResponse{}
	}

	state := ghclient.WorkflowApprovalStateFromRuns(
		ghclient.FilterWorkflowRunsAwaitingApproval(runs, ghclient.PRSource{
			Number:           mr.Number,
			HeadSHA:          headSHA,
			HeadRepoFullName: headRepoFullName,
			HeadRef:          headRef,
		}),
	)
	return workflowApprovalResponse{
		Checked:  state.Checked,
		Required: state.Required,
		Count:    state.Count,
	}
}

func (s *Server) getMRImportMetadata(
	ctx context.Context, input *repoNumberInput,
) (*getMRImportMetadataOutput, error) {
	mr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"failed to query merge request",
		)
	}
	if mr == nil {
		return nil, huma.Error404NotFound("merge request not found")
	}
	return &getMRImportMetadataOutput{
		Body: mrImportMetadataResponse{
			Number:           mr.Number,
			HeadBranch:       mr.HeadBranch,
			PlatformHeadSHA:  mr.PlatformHeadSHA,
			HeadRepoCloneURL: mr.HeadRepoCloneURL,
			State:            mr.State,
			IsDraft:          mr.IsDraft,
			Title:            mr.Title,
		},
	}, nil
}

func (s *Server) setKanbanState(ctx context.Context, input *setKanbanStateInput) (*statusOnlyOutput, error) {
	if !validKanbanStates[input.Body.Status] {
		return nil, huma.Error400BadRequest("status must be one of: new, reviewing, waiting, awaiting_merge")
	}

	ref := repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}
	mrID, err := s.lookupMRID(ctx, ref)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if err := s.db.SetKanbanState(ctx, mrID, input.Body.Status); err != nil {
		return nil, huma.Error500InternalServerError("set kanban state failed")
	}

	return &statusOnlyOutput{Status: http.StatusOK}, nil
}

func (s *Server) editPRContent(
	ctx context.Context, input *editPRContentInput,
) (*editPRContentOutput, error) {
	if input.Body.Title == nil && input.Body.Body == nil {
		return nil, huma.Error400BadRequest(
			"at least one of title or body must be provided",
		)
	}
	if input.Body.Title != nil && strings.TrimSpace(*input.Body.Title) == "" {
		return nil, huma.Error400BadRequest("title must not be blank")
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	mr, err := s.db.GetMergeRequest(
		ctx, input.Owner, input.Name, input.Number,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get pull request failed",
		)
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	opts := ghclient.EditPullRequestOpts{
		Title: input.Body.Title,
		Body:  input.Body.Body,
	}
	ghPR, err := client.EditPullRequest(
		ctx, input.Owner, input.Name, input.Number, opts,
	)
	if err != nil {
		return nil, huma.Error502BadGateway(
			"GitHub API error: " + err.Error(),
		)
	}
	if ghPR == nil {
		return nil, huma.Error502BadGateway(
			"GitHub API returned no pull request",
		)
	}

	newTitle := mr.Title
	if ghPR.Title != nil {
		newTitle = ghPR.GetTitle()
	} else if input.Body.Title != nil {
		newTitle = *input.Body.Title
	}
	newBody := mr.Body
	if ghPR.Body != nil {
		newBody = ghPR.GetBody()
	} else if input.Body.Body != nil {
		newBody = *input.Body.Body
	}
	updatedAt := time.Now().UTC()
	if ghPR.UpdatedAt != nil {
		updatedAt = ghPR.UpdatedAt.UTC()
	}
	if err := s.db.UpdateMRTitleBody(
		ctx, mr.ID, newTitle, newBody, updatedAt,
	); err != nil {
		return nil, huma.Error500InternalServerError(
			"update title/body failed",
		)
	}

	mr, err = s.db.GetMergeRequest(
		ctx, input.Owner, input.Name, input.Number,
	)
	if err != nil || mr == nil {
		return nil, huma.Error500InternalServerError(
			"re-read pull request failed",
		)
	}

	body, err := s.buildPullDetailResponse(
		ctx, mr, workflowDBOnly,
	)
	if err != nil {
		return nil, err
	}

	return &editPRContentOutput{Body: body}, nil
}

func (s *Server) postComment(ctx context.Context, input *postCommentInput) (*postCommentOutput, error) {
	if strings.TrimSpace(input.Body.Body) == "" {
		return nil, huma.Error400BadRequest("comment body must not be empty")
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	comment, err := client.CreateIssueComment(ctx, input.Owner, input.Name, input.Number, input.Body.Body)
	if err != nil {
		return nil, huma.Error502BadGateway("create comment on GitHub failed")
	}

	ref := repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}
	mrID, err := s.lookupMRID(ctx, ref)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	event := ghclient.NormalizeCommentEvent(mrID, comment)
	if err := s.db.UpsertMREvents(ctx, []db.MREvent{event}); err != nil {
		_ = err
	}

	return &postCommentOutput{Status: http.StatusCreated, Body: event}, nil
}

func (s *Server) listIssues(ctx context.Context, input *listIssuesInput) (*listIssuesOutput, error) {
	if input.State != "" {
		valid := map[string]bool{
			"open": true, "closed": true, "all": true,
		}
		if !valid[input.State] {
			return nil, huma.Error400BadRequest(
				"state must be one of: open, closed, all",
			)
		}
	}

	opts := db.ListIssuesOpts{
		State:   input.State,
		Search:  input.Q,
		Starred: input.Starred,
		Limit:   input.Limit,
		Offset:  input.Offset,
	}
	if owner, name := parseRepoFilter(input.Repo); owner != "" {
		opts.RepoOwner = owner
		opts.RepoName = name
	}

	issues, err := s.db.ListIssues(ctx, opts)
	if err != nil {
		return nil, huma.Error500InternalServerError("list issues failed")
	}

	repoByID, err := s.lookupRepoMap(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("repo lookup failed")
	}

	out := make([]issueResponse, 0, len(issues))
	for _, issue := range issues {
		rp, ok := repoByID[issue.RepoID]
		if !ok {
			continue
		}
		resp := issueResponse{
			Issue:        issue,
			RepoOwner:    rp.Owner,
			RepoName:     rp.Name,
			DetailLoaded: issue.DetailFetchedAt != nil,
		}
		if issue.DetailFetchedAt != nil {
			resp.DetailFetchedAt = formatUTCRFC3339(*issue.DetailFetchedAt)
		}
		out = append(out, resp)
	}

	return &listIssuesOutput{Body: out}, nil
}

func (s *Server) getIssue(ctx context.Context, input *repoNumberInput) (*getIssueOutput, error) {
	issue, err := s.db.GetIssue(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get issue failed")
	}
	if issue == nil {
		return nil, huma.Error404NotFound("issue not found")
	}

	events, err := s.db.ListIssueEvents(ctx, issue.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list issue events failed")
	}
	if events == nil {
		events = []db.IssueEvent{}
	}

	repo, err := s.db.GetRepoByID(ctx, issue.RepoID)
	if err != nil || repo == nil {
		return nil, huma.Error500InternalServerError("load repo failed")
	}

	issueResp := issueDetailResponse{
		Issue:        issue,
		Events:       events,
		RepoOwner:    repo.Owner,
		RepoName:     repo.Name,
		DetailLoaded: issue.DetailFetchedAt != nil,
	}
	if issue.DetailFetchedAt != nil {
		issueResp.DetailFetchedAt = formatUTCRFC3339(*issue.DetailFetchedAt)
	}
	return &getIssueOutput{Body: issueResp}, nil
}

func (s *Server) postIssueComment(ctx context.Context, input *postIssueCommentInput) (*postIssueCommentOutput, error) {
	if strings.TrimSpace(input.Body.Body) == "" {
		return nil, huma.Error400BadRequest("comment body must not be empty")
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	comment, err := client.CreateIssueComment(ctx, input.Owner, input.Name, input.Number, input.Body.Body)
	if err != nil {
		return nil, huma.Error502BadGateway("create comment on GitHub failed")
	}

	ref := repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}
	issueID, err := s.lookupIssueID(ctx, ref)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	event := ghclient.NormalizeIssueCommentEvent(issueID, comment)
	if err := s.db.UpsertIssueEvents(ctx, []db.IssueEvent{event}); err != nil {
		_ = err
	}

	return &postIssueCommentOutput{Status: http.StatusCreated, Body: event}, nil
}

func (s *Server) setStarred(ctx context.Context, input *starredInput) (*statusOnlyOutput, error) {
	repoID, err := s.lookupStarredRepoID(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	if err := s.db.SetStarred(ctx, input.Body.ItemType, repoID, input.Body.Number); err != nil {
		return nil, huma.Error500InternalServerError("set starred failed")
	}
	return &statusOnlyOutput{Status: http.StatusOK}, nil
}

func (s *Server) unsetStarred(ctx context.Context, input *starredInput) (*statusOnlyOutput, error) {
	repoID, err := s.lookupStarredRepoID(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	if err := s.db.UnsetStarred(ctx, input.Body.ItemType, repoID, input.Body.Number); err != nil {
		return nil, huma.Error500InternalServerError("unset starred failed")
	}
	return &statusOnlyOutput{Status: http.StatusOK}, nil
}

func (s *Server) getRepo(ctx context.Context, input *getRepoInput) (*getRepoOutput, error) {
	repo, err := s.db.GetRepoByOwnerName(ctx, input.Owner, input.Name)
	if err != nil || repo == nil {
		return nil, huma.Error404NotFound("repo not found")
	}
	return &getRepoOutput{Body: *repo}, nil
}

func (s *Server) getCommentAutocomplete(
	ctx context.Context,
	input *commentAutocompleteInput,
) (*commentAutocompleteOutput, error) {
	repo, err := s.db.GetRepoByOwnerName(ctx, input.Owner, input.Name)
	if err != nil || repo == nil {
		return nil, huma.Error404NotFound("repo not found")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}

	switch input.Trigger {
	case "@":
		users, err := s.db.ListCommentAutocompleteUsers(
			ctx,
			repo.PlatformHost,
			input.Owner,
			input.Name,
			input.Q,
			limit,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError("list comment autocomplete users failed")
		}
		return &commentAutocompleteOutput{Body: commentAutocompleteResponse{Users: users}}, nil
	case "#":
		references, err := s.db.ListCommentAutocompleteReferences(
			ctx,
			repo.PlatformHost,
			input.Owner,
			input.Name,
			input.Q,
			limit,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError("list comment autocomplete references failed")
		}
		return &commentAutocompleteOutput{Body: commentAutocompleteResponse{References: references}}, nil
	default:
		return nil, huma.Error400BadRequest("trigger must be @ or #")
	}
}

func (s *Server) approvePR(ctx context.Context, input *approvePRInput) (*actionStatusOutput, error) {
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	review, err := client.CreateReview(ctx, input.Owner, input.Name, input.Number, ghclient.CreateReviewOpts{
		Event: "APPROVE",
		Body:  input.Body.Body,
	})
	if err != nil {
		return nil, huma.Error502BadGateway("GitHub API error")
	}

	ref := repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}
	mrID, lookupErr := s.lookupMRID(ctx, ref)
	if lookupErr == nil {
		event := ghclient.NormalizeReviewEvent(mrID, review)
		_ = s.db.UpsertMREvents(ctx, []db.MREvent{event})
	}

	return &actionStatusOutput{Body: actionStatusBody{Status: "approved"}}, nil
}

// submitReview publishes a reviewer's pending comments + optional
// review event. Each inline comment is posted individually via the
// pull-request-comments endpoint so it can carry its OWN commit_id —
// that lets reviewers draft against different commits in the PR
// series without the whole review getting anchored to HEAD (and
// failing when line numbers shifted). The review-level event (if
// APPROVE/REQUEST_CHANGES, or COMMENT with a body) is posted
// afterwards via the reviews endpoint with no inline comments, just
// the verdict + summary.
func (s *Server) submitReview(ctx context.Context, input *submitReviewInput) (*submitReviewOutput, error) {
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	switch input.Body.Event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, huma.Error400BadRequest("event must be APPROVE, REQUEST_CHANGES, or COMMENT")
	}
	if input.Body.Event == "REQUEST_CHANGES" && strings.TrimSpace(input.Body.Body) == "" && len(input.Body.Comments) == 0 {
		return nil, huma.Error400BadRequest("REQUEST_CHANGES requires a review body or at least one inline comment")
	}

	// Validate and normalize every comment before any network call so
	// partial failures are easier to reason about — invalid inputs
	// fail fast and don't leave a half-published review.
	preps := make([]preparedComment, 0, len(input.Body.Comments))
	for i, c := range input.Body.Comments {
		if strings.TrimSpace(c.Body) == "" {
			return nil, huma.Error400BadRequest(fmt.Sprintf("comment[%d]: body is required", i))
		}
		// Replies only need a body — GitHub pulls path/line/side/
		// commit_id from the parent thread. Allow callers to omit
		// those fields for a cleaner frontend payload.
		if c.InReplyTo > 0 {
			preps = append(preps, preparedComment{in: c, index: i})
			continue
		}
		if c.Path == "" {
			return nil, huma.Error400BadRequest(fmt.Sprintf("comment[%d]: path is required", i))
		}
		if c.Line <= 0 {
			return nil, huma.Error400BadRequest(fmt.Sprintf("comment[%d]: line must be positive", i))
		}
		side := c.Side
		if side == "" {
			side = "RIGHT"
		}
		if side != "LEFT" && side != "RIGHT" {
			return nil, huma.Error400BadRequest(fmt.Sprintf("comment[%d]: side must be LEFT or RIGHT", i))
		}
		sha := c.CommitID
		if sha == "" {
			sha = input.Body.CommitID // review-level fallback
		}
		if sha == "" {
			return nil, huma.Error400BadRequest(fmt.Sprintf("comment[%d]: commit_id is required (either per-comment or review-level)", i))
		}
		preps = append(preps, preparedComment{in: c, side: side, sha: sha, index: i})
	}

	// Phase 1: post each inline comment with its own commit_id. A
	// failure on any single comment aborts — partial publishes are
	// confusing, and the client can retry after the reviewer deletes
	// the bad draft.
	// Resolve reply targets to their thread roots. GitHub's dedicated
	// /pulls/{n}/comments/{id}/replies endpoint 404s unless {id} is
	// itself a root comment, so replying to a mid-thread reply fails
	// if we pass the target id through unchanged.
	var mrIDForReplyResolve int64
	hasReply := false
	for _, p := range preps {
		if p.in.InReplyTo > 0 {
			hasReply = true
			break
		}
	}
	if hasReply {
		id, err := s.lookupMRID(ctx, repoNumberPathRef{
			owner: input.Owner, name: input.Name, number: input.Number,
		})
		if err == nil {
			mrIDForReplyResolve = id
		}
	}

	postedIDs := make([]int64, 0, len(preps))
	for _, p := range preps {
		inReplyTo := p.in.InReplyTo
		if inReplyTo > 0 && mrIDForReplyResolve != 0 {
			if root, rerr := s.db.ResolveReviewCommentRootID(ctx, mrIDForReplyResolve, inReplyTo); rerr == nil && root > 0 {
				inReplyTo = root
			}
		}
		_, err := client.CreateInlineComment(ctx, input.Owner, input.Name, input.Number, ghclient.InlineCommentOpts{
			CommitID:  p.sha,
			Path:      p.in.Path,
			Body:      p.in.Body,
			Line:      p.in.Line,
			Side:      p.side,
			StartLine: p.in.StartLine,
			StartSide: p.side,
			InReplyTo: inReplyTo,
		})
		if err != nil {
			return nil, translateCreateError(err, input, &p, len(postedIDs))
		}
	}

	// Phase 2: post the review wrapper (verdict + summary) if it
	// carries meaningful content. Approve/Request-Changes always
	// publish; a bare "COMMENT" event with no body and no user-level
	// commentary gets skipped since the inline comments above are
	// enough.
	var review *gh.PullRequestReview
	wrapperNeeded := input.Body.Event != "COMMENT" || strings.TrimSpace(input.Body.Body) != ""
	if wrapperNeeded {
		review, err = client.CreateReview(ctx, input.Owner, input.Name, input.Number, ghclient.CreateReviewOpts{
			Event:    input.Body.Event,
			Body:     input.Body.Body,
			CommitID: input.Body.CommitID,
		})
		if err != nil {
			if strings.Contains(err.Error(), "one pending review per pull request") {
				prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", input.Owner, input.Name, input.Number)
				return nil, huma.Error409Conflict(
					"You already have a pending review on this PR. Cancel or submit it on GitHub before trying again: " + prURL,
				)
			}
			return nil, huma.Error502BadGateway("submit review wrapper: " + err.Error())
		}
	}

	// Persist the review body as an event so it shows up in the timeline
	// without waiting for the next sync cycle. Inline comments land via
	// the review_comment sync path on the next refresh.
	if review != nil {
		ref := repoNumberPathRef{owner: input.Owner, name: input.Name, number: input.Number}
		if mrID, lookupErr := s.lookupMRID(ctx, ref); lookupErr == nil {
			event := ghclient.NormalizeReviewEvent(mrID, review)
			_ = s.db.UpsertMREvents(ctx, []db.MREvent{event})
		}
	}

	out := submitReviewResponseBody{}
	if review != nil {
		out.ReviewID = review.GetID()
		out.State = review.GetState()
	}
	return &submitReviewOutput{Body: out}, nil
}

// preparedComment is a validated, normalised draft waiting to be
// posted. Declared at package scope so translateCreateError can
// take a typed pointer.
type preparedComment struct {
	in    submitReviewComment
	side  string
	sha   string
	index int
}

// translateCreateError wraps the individual-comment failure with
// guidance the UI can render inline in the review panel.
func translateCreateError(err error, input *submitReviewInput, failing *preparedComment, postedSoFar int) error {
	msg := err.Error()
	if strings.Contains(msg, "one pending review per pull request") {
		prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", input.Owner, input.Name, input.Number)
		return huma.Error409Conflict(
			"You already have a pending review on this PR. Cancel or submit it on GitHub before trying again: " + prURL,
		)
	}
	prefix := ""
	if postedSoFar > 0 {
		prefix = fmt.Sprintf("%d comment(s) posted before this one failed; visit GitHub to clean up if needed. ", postedSoFar)
	}
	if strings.Contains(msg, "could not be resolved") {
		return huma.Error409Conflict(
			prefix + fmt.Sprintf("GitHub couldn't anchor comment[%d] on %s at line %d against %s. "+
				"The file likely changed at that line in that commit. Either rewrite the draft against the current code or delete it before retrying. Raw: %s",
				failing.index, failing.in.Path, failing.in.Line, failing.sha, msg),
		)
	}
	return huma.Error502BadGateway(prefix + "submit inline comment: " + msg)
}

func (s *Server) approveWorkflows(ctx context.Context, input *repoNumberInput) (*actionStatusOutput, error) {
	mr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get pull request failed")
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	pr, err := client.GetPullRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error502BadGateway("GitHub API error")
	}
	if pr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	headSHA := pr.GetHead().GetSHA()
	if pr.GetState() != "open" || headSHA == "" {
		return &actionStatusOutput{Body: actionStatusBody{Status: "approved_workflows"}}, nil
	}

	runs, err := client.ListWorkflowRunsForHeadSHA(ctx, input.Owner, input.Name, headSHA)
	if err != nil {
		return nil, huma.Error502BadGateway("GitHub API error")
	}
	pending := ghclient.FilterWorkflowRunsAwaitingApproval(runs, ghclient.PRSource{
		Number:           input.Number,
		HeadSHA:          headSHA,
		HeadRepoFullName: pr.GetHead().GetRepo().GetFullName(),
		HeadRef:          pr.GetHead().GetRef(),
	})

	approvedCount := 0
	for _, run := range pending {
		if err := client.ApproveWorkflowRun(ctx, input.Owner, input.Name, run.GetID()); err != nil {
			if approvedCount > 0 {
				if syncErr := s.syncer.SyncMR(context.WithoutCancel(ctx), input.Owner, input.Name, input.Number); syncErr != nil {
					slog.Warn("sync after workflow approval failure", "err", syncErr)
				}
			}
			return nil, huma.Error502BadGateway(err.Error())
		}
		approvedCount++
	}

	if syncErr := s.syncer.SyncMR(context.WithoutCancel(ctx), input.Owner, input.Name, input.Number); syncErr != nil {
		slog.Warn("sync after workflow approval", "err", syncErr)
	}

	return &actionStatusOutput{Body: actionStatusBody{
		Status:        "approved_workflows",
		ApprovedCount: approvedCount,
	}}, nil
}

func (s *Server) readyForReview(ctx context.Context, input *repoNumberInput) (*actionStatusOutput, error) {
	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	pr, err := client.MarkPullRequestReadyForReview(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		type readyForReviewFailure interface {
			StatusCode() int
			IsStaleState() bool
		}

		var readyErr readyForReviewFailure
		var ghErr *gh.ErrorResponse
		staleState := errors.As(err, &readyErr) && readyErr != nil && readyErr.IsStaleState()
		if !staleState {
			staleState = errors.As(err, &ghErr) && ghErr != nil && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound
		}
		if staleState {
			if syncErr := s.syncer.SyncMR(context.WithoutCancel(ctx), input.Owner, input.Name, input.Number); syncErr != nil {
				slog.Warn(
					"sync after ready for review stale state failed",
					"owner", input.Owner,
					"repo", input.Name,
					"number", input.Number,
					"err", syncErr,
				)
			} else {
				return &actionStatusOutput{Body: actionStatusBody{Status: "ready_for_review"}}, nil
			}
		}
		slog.Warn(
			"ready for review failed",
			"owner", input.Owner,
			"repo", input.Name,
			"number", input.Number,
			"err", err,
		)
		return nil, huma.Error502BadGateway(err.Error())
	}
	if pr == nil {
		// No PR payload means we cannot verify GitHub accepted the
		// transition, so don't claim success or poison the cache.
		return nil, huma.Error502BadGateway("GitHub API returned no pull request")
	}

	repoObj, err := s.db.GetRepoByOwnerName(ctx, input.Owner, input.Name)
	if err == nil && repoObj != nil {
		normalized, normalizeErr := ghclient.NormalizePR(repoObj.ID, pr)
		if normalizeErr == nil {
			if mrID, upsertErr := s.db.UpsertMergeRequest(ctx, normalized); upsertErr == nil {
				_ = s.db.EnsureKanbanState(ctx, mrID)
			}
		}
	}

	return &actionStatusOutput{Body: actionStatusBody{Status: "ready_for_review"}}, nil
}

func (s *Server) mergePR(ctx context.Context, input *mergePRInput) (*mergePROutput, error) {
	validMethods := map[string]bool{"merge": true, "squash": true, "rebase": true}
	if !validMethods[input.Body.Method] {
		return nil, huma.Error400BadRequest("invalid merge method: must be merge, squash, or rebase")
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	result, err := client.MergePullRequest(
		ctx,
		input.Owner,
		input.Name,
		input.Number,
		input.Body.CommitTitle,
		input.Body.CommitMessage,
		input.Body.Method,
	)
	if err != nil {
		var ghErr *gh.ErrorResponse
		if errors.As(err, &ghErr) && ghErr != nil && ghErr.Response != nil {
			slog.Warn("github merge failed",
				"owner", input.Owner, "repo", input.Name,
				"number", input.Number, "method", input.Body.Method,
				"status", ghErr.Response.StatusCode,
				"message", ghErr.Message)

			if ghErr.Response.StatusCode == http.StatusMethodNotAllowed ||
				ghErr.Response.StatusCode == http.StatusConflict {
				s.runBackground(func(bgCtx context.Context) {
					if syncErr := s.syncer.SyncMR(
						bgCtx, input.Owner, input.Name, input.Number,
					); syncErr != nil {
						slog.Warn("background sync after merge failure", "err", syncErr)
					}
				})
				return nil, huma.Error409Conflict(ghErr.Message)
			}

			// Forward 4xx GitHub errors as-is so the user sees the real cause
			// (e.g. 422 validation, 403 forbidden). 5xx becomes 502.
			if ghErr.Response.StatusCode >= 400 && ghErr.Response.StatusCode < 500 {
				return nil, huma.NewError(ghErr.Response.StatusCode, ghErr.Message)
			}
			return nil, huma.Error502BadGateway("GitHub: " + ghErr.Message)
		}
		slog.Warn("github merge transport error",
			"owner", input.Owner, "repo", input.Name,
			"number", input.Number, "method", input.Body.Method,
			"err", err)
		return nil, huma.Error502BadGateway("GitHub merge error: " + err.Error())
	}

	repoObj, _ := s.db.GetRepoByOwnerName(ctx, input.Owner, input.Name)
	if repoObj != nil {
		now := time.Now().UTC()
		_ = s.db.UpdateMRState(ctx, repoObj.ID, input.Number, "merged", &now, &now)
	}

	return &mergePROutput{
		Body: mergePRBody{
			Merged:  result.GetMerged(),
			SHA:     result.GetSHA(),
			Message: result.GetMessage(),
		},
	}, nil
}

func (s *Server) setPRGitHubState(
	ctx context.Context, input *githubStateInput,
) (*githubStateOutput, error) {
	if input.Body.State != "open" && input.Body.State != "closed" {
		return nil, huma.Error400BadRequest(
			"state must be 'open' or 'closed'",
		)
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	mr, err := s.db.GetMergeRequest(
		ctx, input.Owner, input.Name, input.Number,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get pull request: " + err.Error(),
		)
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if mr.State == "merged" {
		return nil, huma.Error409Conflict(
			"cannot change state of a merged pull request",
		)
	}

	if _, err := client.EditPullRequest(
		ctx, input.Owner, input.Name,
		input.Number, ghclient.EditPullRequestOpts{State: &input.Body.State},
	); err != nil {
		var ghErr *gh.ErrorResponse
		if errors.As(err, &ghErr) && ghErr != nil && ghErr.Response != nil &&
			ghErr.Response.StatusCode == http.StatusUnprocessableEntity {
			// Re-fetch to sync local state and determine the real cause.
			repoID, repoErr := s.lookupRepoID(
				ctx, input.Owner, input.Name,
			)
			if repoErr == nil {
				ghPR, fetchErr := client.GetPullRequest(
					ctx, input.Owner, input.Name, input.Number,
				)
				if fetchErr == nil {
					if ghPR == nil {
						return nil, huma.Error502BadGateway("GitHub API returned no pull request")
					}
					normalized, normalizeErr := ghclient.NormalizePR(repoID, ghPR)
					if normalizeErr != nil {
						return nil, huma.Error502BadGateway("GitHub API error: " + normalizeErr.Error())
					}
					_, _ = s.db.UpsertMergeRequest(ctx, normalized)
					if ghPR.GetMerged() {
						return nil, huma.Error409Conflict(
							"cannot change state of a merged pull request",
						)
					}
					// Already in requested state (concurrent edit).
					if ghPR.GetState() == input.Body.State {
						out := &githubStateOutput{}
						out.Body.State = input.Body.State
						return out, nil
					}
				}
			}
		}
		return nil, huma.Error502BadGateway(
			"GitHub API error: " + err.Error(),
		)
	}

	repoID, err := s.lookupRepoID(ctx, input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get repo: " + err.Error(),
		)
	}

	var closedAt *time.Time
	if input.Body.State == "closed" {
		now := time.Now().UTC()
		closedAt = &now
	}
	if err := s.db.UpdateMRState(
		ctx, repoID, input.Number,
		input.Body.State, nil, closedAt,
	); err != nil {
		return nil, huma.Error500InternalServerError(
			"update mr state: " + err.Error(),
		)
	}

	out := &githubStateOutput{}
	out.Body.State = input.Body.State
	return out, nil
}

func (s *Server) setIssueGitHubState(
	ctx context.Context, input *githubStateInput,
) (*githubStateOutput, error) {
	if input.Body.State != "open" && input.Body.State != "closed" {
		return nil, huma.Error400BadRequest(
			"state must be 'open' or 'closed'",
		)
	}

	client, err := s.syncer.ClientForRepo(input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}

	issue, err := s.db.GetIssue(
		ctx, input.Owner, input.Name, input.Number,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get issue: " + err.Error(),
		)
	}
	if issue == nil {
		return nil, huma.Error404NotFound("issue not found")
	}

	if _, err := client.EditIssue(
		ctx, input.Owner, input.Name,
		input.Number, input.Body.State,
	); err != nil {
		var ghErr *gh.ErrorResponse
		if errors.As(err, &ghErr) && ghErr != nil && ghErr.Response != nil &&
			ghErr.Response.StatusCode == http.StatusUnprocessableEntity {
			// Re-fetch to sync local state. If already in the
			// requested state (concurrent edit), treat as success.
			repoID, repoErr := s.lookupRepoID(
				ctx, input.Owner, input.Name,
			)
			if repoErr == nil {
				ghIssue, fetchErr := client.GetIssue(
					ctx, input.Owner, input.Name, input.Number,
				)
				if fetchErr == nil {
					if ghIssue == nil {
						return nil, huma.Error502BadGateway("GitHub API returned no issue")
					}
					normalized, normalizeErr := ghclient.NormalizeIssue(repoID, ghIssue)
					if normalizeErr != nil {
						return nil, huma.Error502BadGateway("GitHub API error: " + normalizeErr.Error())
					}
					_, _ = s.db.UpsertIssue(ctx, normalized)
					if ghIssue.GetState() == input.Body.State {
						out := &githubStateOutput{}
						out.Body.State = input.Body.State
						return out, nil
					}
				}
			}
		}
		return nil, huma.Error502BadGateway(
			"GitHub API error: " + err.Error(),
		)
	}

	repoID, err := s.lookupRepoID(ctx, input.Owner, input.Name)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get repo: " + err.Error(),
		)
	}

	var closedAt *time.Time
	if input.Body.State == "closed" {
		now := time.Now().UTC()
		closedAt = &now
	}
	if err := s.db.UpdateIssueState(
		ctx, repoID, input.Number,
		input.Body.State, closedAt,
	); err != nil {
		return nil, huma.Error500InternalServerError(
			"update issue state: " + err.Error(),
		)
	}

	out := &githubStateOutput{}
	out.Body.State = input.Body.State
	return out, nil
}

func (s *Server) listRepos(ctx context.Context, _ *struct{}) (*listReposOutput, error) {
	repos, err := s.db.ListRepos(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("list repos failed")
	}
	if repos == nil {
		repos = []db.Repo{}
	}
	if s.cfg != nil {
		repos = s.filterConfiguredRepos(repos)
	}

	return &listReposOutput{Body: repos}, nil
}

type listWorktreesOutput struct {
	Body worktreesResponse
}

type getWorktreeChangedFilesInput struct {
	ID int64 `path:"id"`
}

type getWorktreeChangedFilesOutput struct {
	Body worktreeChangedFilesResponse
}

func (s *Server) getWorktreeChangedFiles(
	ctx context.Context, in *getWorktreeChangedFilesInput,
) (*getWorktreeChangedFilesOutput, error) {
	w, err := s.db.GetWorktreeByID(ctx, in.ID)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	if w.RemovedAt != nil {
		return nil, huma.Error404NotFound("worktree no longer exists on disk")
	}
	overrideRef := s.lookupBaseRefForWorktree(ctx, w)
	cs, err := worktrees.ChangedFilesAgainstBase(ctx, w.Path, overrideRef)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"reading worktree changes failed: " + err.Error(),
		)
	}
	out := worktreeChangedFilesResponse{
		Base: worktreeBaseResponse{
			Ref:      cs.Base.Ref,
			SHA:      cs.Base.SHA,
			Fallback: cs.Base.Fallback,
		},
		Files: make([]changedFileResponse, 0, len(cs.Files)),
	}
	for _, c := range cs.Files {
		out.Files = append(out.Files, changedFileResponse{
			Path:      c.Path,
			OldPath:   c.OldPath,
			Status:    c.Status,
			IsBinary:  c.IsBinary,
			Additions: c.Additions,
			Deletions: c.Deletions,
		})
	}
	return &getWorktreeChangedFilesOutput{Body: out}, nil
}

// lookupBaseRefForWorktree finds the config.Repo entry that owns
// the given worktree and returns its base_ref override (or empty
// if none is configured). Match is by the worktree's repo id →
// middleman_repos.name, cross-referenced against the synthesized
// name on each local config entry. Returns empty when the config
// isn't accessible or no match is found; callers fall through to
// auto-detect in that case.
func (s *Server) lookupBaseRefForWorktree(ctx context.Context, w db.Worktree) string {
	if s.cfg == nil {
		return ""
	}
	repo, err := s.db.GetRepoByID(ctx, w.RepoID)
	if err != nil || repo == nil {
		return ""
	}
	for _, cfgRepo := range s.cfg.Repos {
		if !cfgRepo.IsLocal() {
			continue
		}
		if cfgRepo.Name == repo.Name {
			return cfgRepo.BaseRef
		}
	}
	return ""
}

type getWorktreeDiffInput struct {
	ID int64 `path:"id"`
}

type getWorktreeDiffOutput struct {
	Body worktreeDiffResponse
}

func (s *Server) getWorktreeDiff(
	ctx context.Context, in *getWorktreeDiffInput,
) (*getWorktreeDiffOutput, error) {
	w, err := s.db.GetWorktreeByID(ctx, in.ID)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	if w.RemovedAt != nil {
		return nil, huma.Error404NotFound("worktree no longer exists on disk")
	}
	overrideRef := s.lookupBaseRefForWorktree(ctx, w)
	ds, err := worktrees.DiffAgainstBase(ctx, w.Path, overrideRef)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"reading worktree diff failed: " + err.Error(),
		)
	}
	return &getWorktreeDiffOutput{Body: worktreeDiffResponse{
		Base: worktreeBaseResponse{
			Ref:      ds.Base.Ref,
			SHA:      ds.Base.SHA,
			Fallback: ds.Base.Fallback,
		},
		Files: ds.Files,
	}}, nil
}

type runningTurnsResponse struct {
	WorktreeIDs []int64 `json:"worktree_ids" doc:"Worktree ids that currently have an active Claude session with a queued or running response turn. Suitable for fast polling (~3s)."`
}

type listRunningTurnsOutput struct {
	Body runningTurnsResponse
}

// listWorktreesRunningTurns is a focused endpoint that returns only
// the worktree ids with in-flight Claude turns. The sidebar polls
// this on a tight cadence (~3s) so the "Claude is working" indicator
// appears within a couple seconds of submit, without re-running the
// heavier /worktrees join.
func (s *Server) listWorktreesRunningTurns(
	ctx context.Context, _ *struct{},
) (*listRunningTurnsOutput, error) {
	running, err := s.db.WorktreeIDsWithRunningTurns(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"list running turns: " + err.Error(),
		)
	}
	ids := make([]int64, 0, len(running))
	for id := range running {
		ids = append(ids, id)
	}
	return &listRunningTurnsOutput{Body: runningTurnsResponse{WorktreeIDs: ids}}, nil
}

func (s *Server) listWorktrees(ctx context.Context, _ *struct{}) (*listWorktreesOutput, error) {
	rows, err := s.db.ListAllActiveWorktrees(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("list worktrees failed")
	}
	// Best-effort: a failure here just suppresses the indicator,
	// so don't fail the whole worktrees list.
	running, _ := s.db.WorktreeIDsWithRunningTurns(ctx)
	out := worktreesResponse{Worktrees: make([]worktreeResponse, 0, len(rows))}
	for _, w := range rows {
		out.Worktrees = append(out.Worktrees, worktreeResponse{
			ID:             w.ID,
			RepoOwner:      w.RepoOwner,
			RepoName:       w.RepoName,
			Path:           w.Path,
			Branch:         w.Branch,
			HeadSHA:        w.HeadSHA,
			IsDetached:     w.IsDetached,
			IsLocked:       w.IsLocked,
			IsPrunable:     w.IsPrunable,
			DiscoveredAt:   formatUTCRFC3339(w.DiscoveredAt),
			LastSeenAt:     formatUTCRFC3339(w.LastSeenAt),
			HasRunningTurn: running[w.ID],
		})
	}
	return &listWorktreesOutput{Body: out}, nil
}

func (s *Server) syncStatus(_ context.Context, _ *struct{}) (*syncStatusOutput, error) {
	return &syncStatusOutput{Body: s.syncer.Status()}, nil
}

func (s *Server) getRateLimits(
	_ context.Context, _ *struct{},
) (*rateLimitsOutput, error) {
	trackers := s.syncer.RateTrackers()
	gqlTrackers := s.syncer.GQLRateTrackers()
	budgets := s.syncer.Budgets()
	hosts := make(map[string]rateLimitHostStatus, len(trackers))
	for host, rt := range trackers {
		resetStr := ""
		if resetAt := rt.ResetAt(); resetAt != nil {
			resetStr = formatUTCRFC3339(*resetAt)
		}
		status := rateLimitHostStatus{
			RequestsHour:       rt.RequestsThisHour(),
			RateRemaining:      rt.Remaining(),
			RateLimit:          rt.RateLimit(),
			RateResetAt:        resetStr,
			HourStart:          formatUTCRFC3339(rt.HourStart()),
			SyncThrottleFactor: rt.ThrottleFactor(),
			SyncPaused:         rt.IsPaused(),
			ReserveBuffer:      ghclient.RateReserveBuffer,
			Known:              rt.Known(),
			GQLRemaining:       -1,
			GQLLimit:           -1,
		}
		if gqlRT := gqlTrackers[host]; gqlRT != nil {
			status.GQLRemaining = gqlRT.Remaining()
			status.GQLLimit = gqlRT.RateLimit()
			status.GQLKnown = gqlRT.Known()
			if resetAt := gqlRT.ResetAt(); resetAt != nil {
				status.GQLResetAt = resetAt.UTC().Format(time.RFC3339)
			}
		}
		if b := budgets[host]; b != nil {
			status.BudgetLimit = b.Limit()
			status.BudgetSpent = b.Spent()
			status.BudgetRemaining = b.Remaining()
		}
		hosts[host] = status
	}
	return &rateLimitsOutput{
		Body: rateLimitsResponse{Hosts: hosts},
	}, nil
}

func (s *Server) syncPR(ctx context.Context, input *repoNumberInput) (*syncPROutput, error) {
	// SyncMR distinguishes a non-fatal diff failure from a hard sync failure
	// via DiffSyncError. The PR row, timeline, and CI status are all current
	// in either case, so degrade gracefully: keep the response, but report
	// the diff problem as a warning so the UI can explain why the diff view
	// is stale or empty.
	var diffErr *ghclient.DiffSyncError
	syncErr := s.syncer.SyncMR(ctx, input.Owner, input.Name, input.Number)
	if syncErr != nil && !errors.As(syncErr, &diffErr) {
		if strings.Contains(syncErr.Error(), "is not tracked") {
			return nil, huma.Error403Forbidden(syncErr.Error())
		}
		return nil, huma.Error502BadGateway("sync PR: " + syncErr.Error())
	}

	mr, err := s.db.GetMergeRequest(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get pull request: " + err.Error())
	}
	if mr == nil {
		return nil, huma.Error404NotFound("pull request not found after sync")
	}

	body, err := s.buildPullDetailResponse(ctx, mr, workflowCheckRuns)
	if err != nil {
		return nil, err
	}

	if diffErr != nil {
		slog.Warn("diff sync failed during sync PR",
			"owner", input.Owner,
			"name", input.Name,
			"number", input.Number,
			"code", diffErr.Code,
			"err", diffErr.Err,
		)
		// Replace inferred warnings with the explicit error, which is
		// more specific than the row-state-based diffWarnings.
		body.Warnings = []string{diffErr.UserMessage()}
	}

	return &syncPROutput{Body: body}, nil
}

func (s *Server) syncIssue(ctx context.Context, input *repoNumberInput) (*syncIssueOutput, error) {
	if err := s.syncer.SyncIssue(ctx, input.Owner, input.Name, input.Number); err != nil {
		if strings.Contains(err.Error(), "is not tracked") {
			return nil, huma.Error403Forbidden(err.Error())
		}
		return nil, huma.Error502BadGateway("sync issue: " + err.Error())
	}

	issue, err := s.db.GetIssue(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get issue: " + err.Error())
	}
	if issue == nil {
		return nil, huma.Error404NotFound("issue not found after sync")
	}

	events, err := s.db.ListIssueEvents(ctx, issue.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list issue events: " + err.Error())
	}
	if events == nil {
		events = []db.IssueEvent{}
	}

	syncIssueResp := issueDetailResponse{
		Issue:        issue,
		Events:       events,
		RepoOwner:    input.Owner,
		RepoName:     input.Name,
		DetailLoaded: issue.DetailFetchedAt != nil,
	}
	if issue.DetailFetchedAt != nil {
		syncIssueResp.DetailFetchedAt = formatUTCRFC3339(*issue.DetailFetchedAt)
	}
	return &syncIssueOutput{Body: syncIssueResp}, nil
}

func (s *Server) listActivity(ctx context.Context, input *listActivityInput) (*listActivityOutput, error) {
	opts := db.ListActivityOpts{
		Repo:   input.Repo,
		Types:  input.Types,
		Search: input.Search,
	}

	opts.Limit = activitySafetyCap + 1

	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid since: " + err.Error())
		}
		opts.Since = &t
	} else {
		defaultSince := time.Now().UTC().AddDate(0, 0, -7)
		opts.Since = &defaultSince
	}

	if input.After != "" {
		t, source, sourceID, err := db.DecodeCursor(input.After)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid after cursor: " + err.Error())
		}
		opts.AfterTime = &t
		opts.AfterSource = source
		opts.AfterSourceID = sourceID
	}

	items, err := s.db.ListActivity(ctx, opts)
	if err != nil {
		slog.Error("list activity failed", "err", err)
		return nil, huma.Error500InternalServerError("list activity failed")
	}

	if s.cfg != nil {
		filtered := make([]db.ActivityItem, 0, len(items))
		for _, it := range items {
			if s.syncer.IsTrackedRepo(it.RepoOwner, it.RepoName) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}

	capped := len(items) > activitySafetyCap
	if capped {
		items = items[:activitySafetyCap]
	}

	out := make([]activityItemResponse, len(items))
	for i, it := range items {
		out[i] = activityItemResponse{
			ID:           it.Source + ":" + strconv.FormatInt(it.SourceID, 10),
			Cursor:       db.EncodeCursor(it.CreatedAt, it.Source, it.SourceID),
			ActivityType: it.ActivityType,
			RepoOwner:    it.RepoOwner,
			RepoName:     it.RepoName,
			ItemType:     it.ItemType,
			ItemNumber:   it.ItemNumber,
			ItemTitle:    it.ItemTitle,
			ItemURL:      it.ItemURL,
			ItemState:    it.ItemState,
			Author:       it.Author,
			CreatedAt:    formatUTCRFC3339(it.CreatedAt),
			BodyPreview:  it.BodyPreview,
		}
	}

	return &listActivityOutput{
		Body: activityResponse{Items: out, Capped: capped},
	}, nil
}

func (s *Server) resolveItem(
	ctx context.Context, input *repoNumberInput,
) (*resolveItemOutput, error) {
	owner, name, number := input.Owner, input.Name, input.Number

	if !s.syncer.IsTrackedRepo(owner, name) {
		return &resolveItemOutput{
			Body: resolveItemResponse{
				Number:      number,
				RepoTracked: false,
			},
		}, nil
	}

	repo, err := s.db.GetRepoByOwnerName(ctx, owner, name)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get repo: " + err.Error(),
		)
	}
	if repo != nil {
		itemType, found, err := s.db.ResolveItemNumber(
			ctx, repo.ID, number,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"resolve item: " + err.Error(),
			)
		}
		if found {
			return &resolveItemOutput{
				Body: resolveItemResponse{
					ItemType:    itemType,
					Number:      number,
					RepoTracked: true,
				},
			}, nil
		}
	}

	itemType, err := s.syncer.SyncItemByNumber(
		ctx, owner, name, number,
	)
	// A DiffSyncError means the PR row was upserted but the diff
	// computation failed. Resolution doesn't need diff data, so treat
	// the result as success here. The resolve response has no warnings
	// field, so the staleness reaches the client when they navigate to
	// the PR detail page: getPull infers the warning from the persisted
	// row state via diffWarnings.
	var diffErr *ghclient.DiffSyncError
	if err != nil && !errors.As(err, &diffErr) {
		var ghErr *gh.ErrorResponse
		if errors.As(err, &ghErr) {
			if ghErr.Response != nil &&
				ghErr.Response.StatusCode == 404 {
				return nil, huma.Error404NotFound(
					"item not found: " + err.Error(),
				)
			}
			return nil, huma.Error502BadGateway(
				"GitHub API error: " + err.Error(),
			)
		}
		return nil, huma.Error500InternalServerError(
			"resolve item: " + err.Error(),
		)
	}
	if diffErr != nil {
		slog.Warn("resolve item: diff sync failed but PR row was synced",
			"owner", owner,
			"name", name,
			"number", number,
			"err", err,
		)
	}

	return &resolveItemOutput{
		Body: resolveItemResponse{
			ItemType:    itemType,
			Number:      number,
			RepoTracked: true,
		},
	}, nil
}

func (s *Server) lookupStarredRepoID(ctx context.Context, body starredRequest) (int64, error) {
	if !validateStarredRequest(body) {
		return 0, huma.Error400BadRequest("item_type must be 'pr' or 'issue'")
	}

	repoID, err := s.lookupRepoID(ctx, body.Owner, body.Name)
	if err != nil {
		if errors.Is(err, errRepoNotFound) {
			return 0, huma.Error404NotFound(err.Error())
		}
		return 0, huma.Error500InternalServerError("repo lookup failed")
	}

	return repoID, nil
}

// --- Commits ---

type getCommitsOutput struct {
	Body commitsResponse
}

func (s *Server) getCommits(ctx context.Context, input *repoNumberInput) (*getCommitsOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getCommitsLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("commits not available: clone manager not configured")
	}

	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to look up PR")
	}
	if shas == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error404NotFound("commits not available for this pull request")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	commits, err := s.clones.ListCommits(ctx, host, input.Owner, input.Name, shas.MergeBaseSHA, shas.DiffHeadSHA)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("commits not available: referenced commit not found")
		}
		return nil, huma.Error502BadGateway("failed to list commits: " + err.Error())
	}

	resp := commitsResponse{Commits: make([]commitResponse, len(commits))}
	for i, c := range commits {
		resp.Commits[i] = commitResponse{
			SHA:        c.SHA,
			Message:    c.Message,
			Body:       c.Body,
			AuthorName: c.AuthorName,
			AuthoredAt: c.AuthoredAt.UTC(),
		}
	}
	return &getCommitsOutput{Body: resp}, nil
}

// --- Viewer ---

type getViewerOutput struct {
	Body viewerResponse
}

// getViewer returns the GitHub login of the user whose token is
// configured in middleman. Cached after the first call so this is
// effectively free on subsequent hits.
func (s *Server) getViewer(ctx context.Context, _ *struct{}) (*getViewerOutput, error) {
	login, name, err := s.resolveViewer(ctx)
	if err != nil {
		return nil, err
	}
	return &getViewerOutput{Body: viewerResponse{Login: login, Name: name}}, nil
}

// resolveViewer returns the viewer login + name, fetching from
// GitHub on a cache miss. Other handlers (listPulls) call this so
// viewer-relative state is available on the first /pulls response
// even when the frontend hasn't hit /me yet — closes a startup race
// where the initial /pulls would otherwise return empty review_state
// for every row and the sidebar would fall back to the legacy
// "REVIEW" chip on PRs the viewer had already reviewed.
func (s *Server) resolveViewer(ctx context.Context) (login, name string, err error) {
	s.viewerMu.Lock()
	if s.viewerLogin != "" {
		login, name = s.viewerLogin, s.viewerName
		s.viewerMu.Unlock()
		return login, name, nil
	}
	s.viewerMu.Unlock()

	client, cerr := s.syncer.PrimaryClient()
	if cerr != nil {
		return "", "", huma.Error503ServiceUnavailable("viewer not available: " + cerr.Error())
	}
	user, uerr := client.GetUser(ctx, "")
	if uerr != nil {
		return "", "", huma.Error502BadGateway("fetch viewer: " + uerr.Error())
	}
	login = user.GetLogin()
	if login == "" {
		return "", "", huma.Error502BadGateway("viewer response missing login")
	}
	name = user.GetName()

	s.viewerMu.Lock()
	s.viewerLogin = login
	s.viewerName = name
	s.viewerMu.Unlock()
	return login, name, nil
}

// --- Blob range (context expansion) ---

// blobRangeMaxLines caps how many lines a single expansion request
// can pull from a blob. Huge jumps should go through the "expand
// all" path which we'll answer with whatever fits up to this cap.
const blobRangeMaxLines = 2000

type getBlobRangeInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Path   string `query:"path"   doc:"File path within the repo"`
	SHA    string `query:"sha"    doc:"Commit SHA whose blob to read"`
	Start  int    `query:"start"  doc:"1-based start line (inclusive)"`
	End    int    `query:"end"    doc:"1-based end line (inclusive)"`
}

type getBlobRangeOutput struct {
	Body blobRangeResponse
}

// getBlobRange serves a chunk of a file blob so the diff viewer
// can show context lines outside the hunks (GitHub's "+N lines"
// affordance). We scope by PR to keep the auth model coherent
// with /diff, even though the underlying clone is already
// publicly readable.
func (s *Server) getBlobRange(ctx context.Context, input *getBlobRangeInput) (*getBlobRangeOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getBlobRangeLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("blob range not available: clone manager not configured")
	}
	if input.Path == "" {
		return nil, huma.Error400BadRequest("path is required")
	}
	if input.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	if input.Start < 1 {
		return nil, huma.Error400BadRequest("start must be >= 1")
	}
	if input.End < input.Start {
		return nil, huma.Error400BadRequest("end must be >= start")
	}
	if input.End-input.Start+1 > blobRangeMaxLines {
		return nil, huma.Error400BadRequest(fmt.Sprintf("requested range too large (max %d lines)", blobRangeMaxLines))
	}

	// Validate the PR exists so arbitrary repos aren't probed
	// through this endpoint. We don't verify the sha is reachable
	// — cat-file will fail cleanly if it isn't.
	if _, err := s.db.GetMRIDByRepoAndNumber(ctx, input.Owner, input.Name, input.Number); err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	lines, err := s.clones.BlobRange(ctx, host, input.Owner, input.Name, input.SHA, input.Path, input.Start, input.End)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("blob not found: " + err.Error())
		}
		return nil, huma.Error502BadGateway("read blob: " + err.Error())
	}
	return &getBlobRangeOutput{Body: blobRangeResponse{Lines: lines}}, nil
}

// --- Full blob (for rendered file views, e.g. markdown) ---

// blobMaxBytes caps the size of a single full-file fetch. Markdown
// files in practice are tiny; this is here to keep a malicious or
// pathological 100 MB blob from sailing through.
const blobMaxBytes = 2 * 1024 * 1024 // 2 MB

type getBlobInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Path   string `query:"path" doc:"File path within the repo"`
	SHA    string `query:"sha"  doc:"Commit/tree SHA whose blob to read"`
}

type getBlobOutput struct {
	Body blobResponse
}

type blobResponse struct {
	Content   string `json:"content" doc:"Raw file content (UTF-8). Truncated empty + truncated=true when oversized."`
	Truncated bool   `json:"truncated"`
}

// getBlob returns the entire file at the given SHA. The rendered-
// markdown view in the diff sidebar fetches this; pages with raw
// content can read it directly too. PR-scoped for auth coherence
// with /diff, /files, etc.
func (s *Server) getBlob(ctx context.Context, input *getBlobInput) (*getBlobOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getBlobLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("blob not available: clone manager not configured")
	}
	if input.Path == "" {
		return nil, huma.Error400BadRequest("path is required")
	}
	if input.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	if _, err := s.db.GetMRIDByRepoAndNumber(ctx, input.Owner, input.Name, input.Number); err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	raw, err := s.clones.Blob(ctx, host, input.Owner, input.Name, input.SHA, input.Path)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("blob not found: " + err.Error())
		}
		return nil, huma.Error502BadGateway("read blob: " + err.Error())
	}
	if len(raw) > blobMaxBytes {
		return &getBlobOutput{Body: blobResponse{Truncated: true}}, nil
	}
	return &getBlobOutput{Body: blobResponse{Content: string(raw)}}, nil
}

// --- Filename resolution (for AI prose linkification) ---

const resolveFilesMaxNames = 64

type resolveFilesInput struct {
	Owner string `path:"owner"`
	Name  string `path:"name"`
	Body  resolveFilesRequest
}

type resolveFilesRequest struct {
	SHA   string   `json:"sha" doc:"Commit/tree SHA to resolve names against"`
	Names []string `json:"names" doc:"Bare filenames or paths to resolve"`
}

type resolveFilesOutput struct {
	Body resolveFilesResponse
}

type resolveFilesResponse struct {
	// Resolutions maps each requested name to its unique full path
	// at the given SHA. Names with zero or multiple matches are
	// omitted (callers should treat missing entries as "leave as
	// plain text" rather than guessing).
	Resolutions map[string]string `json:"resolutions"`
}

// resolveFiles takes a list of basenames or paths and returns the
// unique repo path at the given SHA for each. Used by the AI review
// markdown post-processor to turn bare filenames like "huma_routes.go"
// into deep links — without server-side resolution we'd guess wrong
// when files live in subdirectories, producing 404s.
func (s *Server) resolveFiles(ctx context.Context, input *resolveFilesInput) (*resolveFilesOutput, error) {
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("file resolution not available: clone manager not configured")
	}
	if input.Body.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	if len(input.Body.Names) == 0 {
		return &resolveFilesOutput{Body: resolveFilesResponse{Resolutions: map[string]string{}}}, nil
	}
	if len(input.Body.Names) > resolveFilesMaxNames {
		return nil, huma.Error400BadRequest(fmt.Sprintf("too many names (max %d)", resolveFilesMaxNames))
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	resolved, err := s.clones.ResolveFilenames(ctx, host, input.Owner, input.Name, input.Body.SHA, input.Body.Names)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("sha not in clone: " + err.Error())
		}
		return nil, huma.Error502BadGateway("resolve files: " + err.Error())
	}
	if resolved == nil {
		resolved = map[string]string{}
	}
	return &resolveFilesOutput{Body: resolveFilesResponse{Resolutions: resolved}}, nil
}

// --- Patchsets ---

type listPatchsetsOutput struct {
	Body patchsetsResponse
}

// listPatchsets returns every patchset (distinct head SHA observed by
// sync) for the given PR in oldest-first order. Used by the review
// surface to render the Gerrit-style PS chip strip.
func (s *Server) listPatchsets(ctx context.Context, input *repoNumberInput) (*listPatchsetsOutput, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	rows, err := s.db.ListPatchsets(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list patchsets: " + err.Error())
	}
	out := patchsetsResponse{Patchsets: make([]patchsetResponse, 0, len(rows))}
	for _, p := range rows {
		out.Patchsets = append(out.Patchsets, patchsetResponse{
			ID:           p.ID,
			Number:       p.Number,
			HeadSHA:      p.HeadSHA,
			BaseSHA:      p.BaseSHA,
			MergeBaseSHA: p.MergeBaseSHA,
			ObservedAt:   p.ObservedAt.UTC().Format(time.RFC3339),
		})
	}
	return &listPatchsetsOutput{Body: out}, nil
}

// --- PR scratchpad notes ---

// prNotesMaxBytes caps the scratchpad payload. A reviewer's private
// notes should fit comfortably; the limit prevents accidental paste
// bombs from filling SQLite.
const prNotesMaxBytes = 65_536

type getPRNotesOutput struct {
	Body prNotesResponse
}

type putPRNotesInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		Content string `json:"content"`
	}
}

func (s *Server) getPRNotes(ctx context.Context, input *repoNumberInput) (*getPRNotesOutput, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	notes, err := s.db.GetPRNotes(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("load notes: " + err.Error())
	}
	return &getPRNotesOutput{Body: toPRNotesResponse(notes)}, nil
}

func (s *Server) putPRNotes(ctx context.Context, input *putPRNotesInput) (*getPRNotesOutput, error) {
	if len(input.Body.Content) > prNotesMaxBytes {
		return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf(
			"notes too large: %d bytes (max %d)", len(input.Body.Content), prNotesMaxBytes,
		))
	}
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	notes, err := s.db.UpsertPRNotes(ctx, mrID, input.Body.Content)
	if err != nil {
		return nil, huma.Error500InternalServerError("save notes: " + err.Error())
	}
	return &getPRNotesOutput{Body: toPRNotesResponse(notes)}, nil
}

func toPRNotesResponse(n db.PRNotes) prNotesResponse {
	r := prNotesResponse{Content: n.Content}
	if !n.UpdatedAt.IsZero() {
		r.UpdatedAt = n.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return r
}

// --- Diff ---

type getDiffInput struct {
	Owner        string `path:"owner"`
	Name         string `path:"name"`
	Number       int    `path:"number"`
	Whitespace   string `query:"whitespace"`
	Commit       string `query:"commit" doc:"Scope to a single commit SHA"`
	From         string `query:"from"   doc:"Start SHA for range diff (inclusive)"`
	To           string `query:"to"     doc:"End SHA for range diff (inclusive)"`
	FromPatchset int    `query:"from_patchset" doc:"Patchset number to compare FROM (Gerrit-style rebase-aware interdiff)"`
	ToPatchset   int    `query:"to_patchset"   doc:"Patchset number to compare TO"`
}

type getDiffOutput struct {
	Body diffResponse
}

func (s *Server) getDiff(ctx context.Context, input *getDiffInput) (*getDiffOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getDiffLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("diff view not available: clone manager not configured")
	}

	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to look up PR")
	}
	if shas == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error404NotFound("diff not available for this pull request")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	hideWhitespace := input.Whitespace == "hide"

	// Patchset-pair scope: serve the rebase-aware interdiff. Mutually
	// exclusive with commit / from-to SHA scopes.
	if input.FromPatchset > 0 || input.ToPatchset > 0 {
		if input.Commit != "" || input.From != "" || input.To != "" {
			return nil, huma.Error400BadRequest("invalid scope: 'from_patchset'/'to_patchset' cannot be combined with 'commit' or 'from'/'to'")
		}
		return s.getDiffByPatchsets(ctx, input, host, hideWhitespace, shas.Stale())
	}

	// Determine diff range based on scope query params.
	diffFrom := shas.MergeBaseSHA
	diffTo := shas.DiffHeadSHA

	hasCommit := input.Commit != ""
	hasFrom := input.From != ""
	hasTo := input.To != ""

	switch {
	case !hasCommit && !hasFrom && !hasTo:
		// Default: full PR diff. diffFrom/diffTo already set.

	case hasCommit && !hasFrom && !hasTo:
		if _, err := s.validateSHAs(ctx, host, input, shas, input.Commit); err != nil {
			return nil, err
		}
		parent, err := s.clones.ParentOf(ctx, host, input.Owner, input.Name, input.Commit)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to resolve parent: " + err.Error())
		}
		diffFrom = parent
		diffTo = input.Commit

	case !hasCommit && hasFrom && hasTo:
		indexMap, err := s.validateSHAs(ctx, host, input, shas, input.From, input.To)
		if err != nil {
			return nil, err
		}
		// In newest-first order, "from" (older) must have a higher index than "to" (newer).
		if indexMap[input.From] <= indexMap[input.To] {
			return nil, huma.Error400BadRequest("invalid range: 'from' must be older than 'to'")
		}
		parent, err := s.clones.ParentOf(ctx, host, input.Owner, input.Name, input.From)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to resolve parent: " + err.Error())
		}
		diffFrom = parent
		diffTo = input.To

	default:
		return nil, huma.Error400BadRequest("invalid scope: use 'commit' alone or 'from'+'to' together")
	}

	result, err := s.clones.Diff(ctx, host, input.Owner, input.Name, diffFrom, diffTo, hideWhitespace)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("diff not available: referenced commit not found")
		}
		slog.Error("failed to compute diff", "owner", input.Owner, "name", input.Name, "number", input.Number, "err", err)
		return nil, huma.Error502BadGateway("failed to compute diff")
	}

	result.Stale = shas.Stale()

	return &getDiffOutput{Body: diffResponse{
		Stale:               result.Stale,
		WhitespaceOnlyCount: result.WhitespaceOnlyCount,
		Files:               result.Files,
	}}, nil
}

// getDiffByPatchsets serves the rebase-aware interdiff for a patchset
// pair. Looks up the recorded head/base SHAs for each patchset and
// defers to gitclone.InterdiffPatchsetsStructured; callers receive a
// normal diff plus interdiff_kind / interdiff_reason metadata so the
// UI can banner-flag conflicted or unrelated results.
func (s *Server) getDiffByPatchsets(
	ctx context.Context,
	input *getDiffInput,
	host string,
	hideWhitespace, stale bool,
) (*getDiffOutput, error) {
	if input.FromPatchset <= 0 || input.ToPatchset <= 0 {
		return nil, huma.Error400BadRequest("both from_patchset and to_patchset are required")
	}
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	rows, err := s.db.ListPatchsets(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("list patchsets: " + err.Error())
	}
	byNumber := make(map[int]db.PRPatchset, len(rows))
	for _, p := range rows {
		byNumber[p.Number] = p
	}
	from, ok := byNumber[input.FromPatchset]
	if !ok {
		return nil, huma.Error404NotFound(fmt.Sprintf("patchset PS%d not found", input.FromPatchset))
	}
	to, ok := byNumber[input.ToPatchset]
	if !ok {
		return nil, huma.Error404NotFound(fmt.Sprintf("patchset PS%d not found", input.ToPatchset))
	}

	// Trivial same-patchset comparison: empty diff, clean kind.
	if from.HeadSHA == to.HeadSHA {
		return &getDiffOutput{Body: diffResponse{
			Stale:         stale,
			Files:         []gitclone.DiffFile{},
			InterdiffKind: string(gitclone.InterdiffClean),
		}}, nil
	}

	si, err := s.clones.InterdiffPatchsetsStructured(
		ctx, host, input.Owner, input.Name,
		from.HeadSHA, from.MergeBaseSHA, to.HeadSHA, to.MergeBaseSHA,
		hideWhitespace,
	)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("interdiff not available: referenced commit not found")
		}
		slog.Error("failed to compute interdiff",
			"owner", input.Owner, "name", input.Name, "number", input.Number,
			"from_ps", input.FromPatchset, "to_ps", input.ToPatchset, "err", err)
		return nil, huma.Error502BadGateway("failed to compute interdiff")
	}

	res := si.Result
	if res == nil {
		res = &gitclone.DiffResult{Files: []gitclone.DiffFile{}}
	}
	return &getDiffOutput{Body: diffResponse{
		Stale:               stale,
		WhitespaceOnlyCount: res.WhitespaceOnlyCount,
		Files:               res.Files,
		InterdiffKind:       string(si.Kind),
		InterdiffReason:     si.Reason,
	}}, nil
}

// --- Files (lightweight) ---

type getFilesInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
}

type getFilesOutput struct {
	Body filesResponse
}

func (s *Server) getFiles(ctx context.Context, input *getFilesInput) (*getFilesOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getFilesLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error503ServiceUnavailable("files view not available: clone manager not configured")
	}

	shas, err := s.db.GetDiffSHAs(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to look up PR")
	}
	if shas == nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if shas.DiffHeadSHA == "" || shas.MergeBaseSHA == "" {
		return nil, huma.Error404NotFound("file list not available for this pull request")
	}

	host := s.syncer.HostForRepo(input.Owner, input.Name)
	files, err := s.clones.DiffFiles(ctx, host, input.Owner, input.Name, shas.MergeBaseSHA, shas.DiffHeadSHA)
	if err != nil {
		if errors.Is(err, gitclone.ErrNotFound) {
			return nil, huma.Error404NotFound("file list not available: referenced commit not found")
		}
		slog.Error("failed to list files", "owner", input.Owner, "name", input.Name, "number", input.Number, "err", err)
		return nil, huma.Error502BadGateway("failed to list files")
	}

	return &getFilesOutput{Body: filesResponse{
		Stale: shas.Stale(),
		Files: files,
	}}, nil
}

// validateSHAs checks that all provided SHAs are in the PR's first-parent commit list.
// Returns a SHA -> index map (newest-first order) so callers can check range ordering.
func (s *Server) validateSHAs(
	ctx context.Context,
	host string,
	input *getDiffInput,
	shas *db.DiffSHAs,
	userSHAs ...string,
) (map[string]int, error) {
	commits, err := s.clones.ListCommits(ctx, host, input.Owner, input.Name, shas.MergeBaseSHA, shas.DiffHeadSHA)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list commits for validation: " + err.Error())
	}
	indexMap := make(map[string]int, len(commits))
	for i, c := range commits {
		indexMap[c.SHA] = i
	}
	for _, sha := range userSHAs {
		if _, ok := indexMap[sha]; !ok {
			return nil, huma.Error400BadRequest("sha not in pull request: " + sha)
		}
	}
	return indexMap, nil
}

// --- Stacks ---

func (s *Server) listStacks(ctx context.Context, input *listStacksInput) (*listStacksOutput, error) {
	if input.Repo != "" {
		if strings.Count(input.Repo, "/") != 1 {
			return nil, huma.Error400BadRequest("invalid repo filter: expected owner/name")
		}
		owner, name, _ := strings.Cut(input.Repo, "/")
		if owner == "" || name == "" {
			return nil, huma.Error400BadRequest("invalid repo filter: expected owner/name")
		}
	}
	stackList, memberMap, err := s.db.ListStacksWithMembers(ctx, input.Repo)
	if err != nil {
		return nil, huma.Error500InternalServerError("list stacks failed")
	}

	out := make([]stackResponse, 0, len(stackList))
	for _, st := range stackList {
		members := memberMap[st.ID]
		out = append(out, stackResponse{
			ID:        st.ID,
			Name:      st.Name,
			RepoOwner: st.RepoOwner,
			RepoName:  st.RepoName,
			Health:    computeStackHealth(members),
			Members:   toStackMemberResponses(members),
		})
	}

	return &listStacksOutput{Body: out}, nil
}

func (s *Server) getStackForPR(ctx context.Context, input *repoNumberInput) (*getStackForPROutput, error) {
	stack, members, err := s.db.GetStackForPR(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error500InternalServerError("get stack for pr failed")
	}
	if stack == nil {
		// PR isn't in a stack — common case for most PRs. Return 200
		// with InStack=false so the browser console isn't peppered
		// with harmless 404s.
		return &getStackForPROutput{
			Body: stackContextResponse{InStack: false, Members: []stackMemberResponse{}},
		}, nil
	}

	var position int
	for _, m := range members {
		if m.Number == input.Number {
			position = m.Position
			break
		}
	}

	return &getStackForPROutput{
		Body: stackContextResponse{
			InStack:   true,
			StackID:   stack.ID,
			StackName: stack.Name,
			Position:  position,
			Size:      len(members),
			Health:    computeStackHealth(members),
			Members:   toStackMemberResponses(members),
		},
	}, nil
}

// --- Workspaces ---

func (s *Server) createWorkspace(
	ctx context.Context, input *createWorkspaceInput,
) (*createWorkspaceOutput, error) {
	if s.workspaces == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workspace manager not configured",
		)
	}

	ws, err := s.workspaces.Create(
		ctx,
		input.Body.PlatformHost,
		input.Body.Owner,
		input.Body.Name,
		input.Body.MRNumber,
	)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not tracked") {
			return nil, huma.Error404NotFound(msg)
		}
		if strings.Contains(msg, "not synced") {
			return nil, huma.Error404NotFound(msg)
		}
		if strings.Contains(msg, "UNIQUE constraint") {
			return nil, huma.Error409Conflict(
				"workspace already exists for this MR")
		}
		return nil, huma.Error500InternalServerError(
			"create workspace: " + msg,
		)
	}

	s.runBackground(func(bgCtx context.Context) {
		setupErr := s.workspaces.Setup(bgCtx, ws)
		summary, getErr := s.workspaces.GetSummary(
			bgCtx, ws.ID,
		)
		if getErr != nil {
			slog.Warn("get workspace summary after setup",
				"id", ws.ID, "err", getErr,
			)
			return
		}
		if summary == nil {
			return
		}
		resp := toWorkspaceResponse(summary)
		if setupErr != nil {
			slog.Warn("workspace setup failed",
				"id", ws.ID, "err", setupErr,
			)
		}
		s.hub.Broadcast(Event{
			Type: "workspace_status",
			Data: resp,
		})
	})

	summary, err := s.workspaces.GetSummary(ctx, ws.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get workspace summary: " + err.Error(),
		)
	}
	if summary == nil {
		return nil, huma.Error500InternalServerError(
			"workspace summary missing after create",
		)
	}
	return &createWorkspaceOutput{
		Status: http.StatusAccepted,
		Body:   toWorkspaceResponse(summary),
	}, nil
}

func (s *Server) listWorkspaces(
	ctx context.Context, _ *struct{},
) (*listWorkspacesOutput, error) {
	if s.workspaces == nil {
		out := &listWorkspacesOutput{}
		out.Body.Workspaces = []workspaceResponse{}
		return out, nil
	}

	summaries, err := s.workspaces.ListSummaries(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"list workspaces failed",
		)
	}

	list := make([]workspaceResponse, len(summaries))
	for i := range summaries {
		list[i] = toWorkspaceResponse(&summaries[i])
	}

	out := &listWorkspacesOutput{}
	out.Body.Workspaces = list
	return out, nil
}

func (s *Server) getWorkspace(
	ctx context.Context, input *getWorkspaceInput,
) (*getWorkspaceOutput, error) {
	if s.workspaces == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workspace manager not configured",
		)
	}

	summary, err := s.workspaces.GetSummary(ctx, input.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"get workspace failed",
		)
	}
	if summary == nil {
		return nil, huma.Error404NotFound("workspace not found")
	}

	return &getWorkspaceOutput{
		Body: toWorkspaceResponse(summary),
	}, nil
}

func (s *Server) deleteWorkspace(
	ctx context.Context, input *deleteWorkspaceInput,
) (*struct{}, error) {
	if s.workspaces == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workspace manager not configured",
		)
	}

	dirty, err := s.workspaces.Delete(
		ctx, input.ID, input.Force,
	)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, huma.Error500InternalServerError(
			"delete workspace: " + err.Error(),
		)
	}
	if len(dirty) > 0 {
		return nil, huma.Error409Conflict(
			"workspace has uncommitted changes: " +
				strings.Join(dirty, ", "),
		)
	}

	return nil, nil
}
