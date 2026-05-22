package server

import (
	"time"

	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
)

type worktreeLinkResponse struct {
	WorktreeKey    string `json:"worktree_key"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	WorktreeBranch string `json:"worktree_branch,omitempty"`
}

// mergeRequestResponse extends db.MergeRequest with resolved repo owner/name fields.
type mergeRequestResponse struct {
	db.MergeRequest
	RepoOwner       string                 `json:"repo_owner"`
	RepoName        string                 `json:"repo_name"`
	PlatformHost    string                 `json:"platform_host"`
	WorktreeLinks   []worktreeLinkResponse `json:"worktree_links"`
	DetailLoaded    bool                   `json:"detail_loaded"`
	DetailFetchedAt string                 `json:"detail_fetched_at,omitempty"`
	// ReviewerLogins are distinct GitHub logins that have submitted at
	// least one review on this PR. GitHub removes a reviewer from
	// requested_reviewers once they submit a review, so this field
	// keeps the "already reviewed" set around for the UI's "in my
	// review queue" chip.
	ReviewerLogins []string `json:"reviewer_logins"`
	// ReviewState classifies this PR for the configured viewer:
	//   "unreviewed" — viewer has not submitted a review.
	//   "reviewed"   — viewer has reviewed; nothing has changed since.
	//   "responded"  — viewer has reviewed; the author has either
	//                  pushed a new patchset OR commented since the
	//                  viewer's last review.
	// Empty when the viewer's GitHub login hasn't been resolved yet
	// (e.g., before the first /me call lands).
	ReviewState string `json:"review_state,omitempty"`
}

type workflowApprovalResponse struct {
	Checked  bool `json:"checked"`
	Required bool `json:"required"`
	Count    int  `json:"count"`
}

type mergeRequestDetailResponse struct {
	MergeRequest     *db.MergeRequest         `json:"merge_request"`
	Events           []db.MREvent             `json:"events"`
	RepoOwner        string                   `json:"repo_owner"`
	RepoName         string                   `json:"repo_name"`
	PlatformHost     string                   `json:"platform_host"`
	WorktreeLinks    []worktreeLinkResponse   `json:"worktree_links"`
	WorkflowApproval workflowApprovalResponse `json:"workflow_approval"`
	Warnings         []string                 `json:"warnings,omitempty"`
	DetailLoaded     bool                     `json:"detail_loaded"`
	DetailFetchedAt  string                   `json:"detail_fetched_at,omitempty"`
	Workspace        *workspaceMRRef          `json:"workspace,omitempty"`
	// HiddenThreadRootIDs lists the GitHub platform comment ids of
	// review thread roots that are currently hidden in the UI for
	// this PR. Computed by ActiveHiddenReviewThreadRoots; a stored
	// hide is omitted if the thread has a reply newer than hidden_at.
	HiddenThreadRootIDs []int64 `json:"hidden_thread_root_ids"`
}

var validKanbanStates = map[string]bool{
	"new":            true,
	"reviewing":      true,
	"waiting":        true,
	"awaiting_merge": true,
}

type issueResponse struct {
	db.Issue
	RepoOwner       string `json:"repo_owner"`
	RepoName        string `json:"repo_name"`
	DetailLoaded    bool   `json:"detail_loaded"`
	DetailFetchedAt string `json:"detail_fetched_at,omitempty"`
}

type issueDetailResponse struct {
	Issue           *db.Issue       `json:"issue"`
	Events          []db.IssueEvent `json:"events"`
	RepoOwner       string          `json:"repo_owner"`
	RepoName        string          `json:"repo_name"`
	DetailLoaded    bool            `json:"detail_loaded"`
	DetailFetchedAt string          `json:"detail_fetched_at,omitempty"`
}

type commentAutocompleteResponse struct {
	Users      []string                          `json:"users,omitempty"`
	References []db.CommentAutocompleteReference `json:"references,omitempty"`
}

type resolveItemResponse struct {
	ItemType    string `json:"item_type" doc:"'pr' or 'issue'"`
	Number      int    `json:"number"`
	RepoTracked bool   `json:"repo_tracked"`
}

type diffResponse struct {
	Stale               bool                `json:"stale"`
	WhitespaceOnlyCount int                 `json:"whitespace_only_count"`
	Files               []gitclone.DiffFile `json:"files"`
	// Interdiff metadata — only set when the request scoped to a
	// patchset pair. Kind is "clean" | "conflicted" | "unrelated";
	// Reason is a human-readable explanation when not clean.
	InterdiffKind   string `json:"interdiff_kind,omitempty"`
	InterdiffReason string `json:"interdiff_reason,omitempty"`
}

type filesResponse struct {
	Stale bool                `json:"stale"`
	Files []gitclone.DiffFile `json:"files"`
}

type mrImportMetadataResponse struct {
	Number           int    `json:"number"`
	HeadBranch       string `json:"head_branch"`
	PlatformHeadSHA  string `json:"platform_head_sha"`
	HeadRepoCloneURL string `json:"head_repo_clone_url"`
	State            string `json:"state"`
	IsDraft          bool   `json:"is_draft"`
	Title            string `json:"title"`
}

type rateLimitHostStatus struct {
	RequestsHour       int    `json:"requests_hour"`
	RateRemaining      int    `json:"rate_remaining"`
	RateLimit          int    `json:"rate_limit"`
	RateResetAt        string `json:"rate_reset_at"`
	HourStart          string `json:"hour_start"`
	SyncThrottleFactor int    `json:"sync_throttle_factor"`
	SyncPaused         bool   `json:"sync_paused"`
	ReserveBuffer      int    `json:"reserve_buffer"`
	Known              bool   `json:"known"`
	BudgetLimit        int    `json:"budget_limit"`
	BudgetSpent        int    `json:"budget_spent"`
	BudgetRemaining    int    `json:"budget_remaining"`
	GQLRemaining       int    `json:"gql_remaining"`
	GQLLimit           int    `json:"gql_limit"`
	GQLResetAt         string `json:"gql_reset_at"`
	GQLKnown           bool   `json:"gql_known"`
}

type rateLimitsResponse struct {
	Hosts map[string]rateLimitHostStatus `json:"hosts"`
}

type commitResponse struct {
	SHA        string    `json:"sha"              doc:"Full commit SHA"`
	Message    string    `json:"message"          doc:"Subject (first line) of commit message"`
	Body       string    `json:"body,omitempty"   doc:"Commit message body (trimmed, empty when the message has no body)"`
	AuthorName string    `json:"author_name"      doc:"Commit author display name"`
	AuthoredAt time.Time `json:"authored_at"      doc:"Commit author date (RFC3339)"`
}

type commitsResponse struct {
	Commits []commitResponse `json:"commits" doc:"Commits in newest-first order"`
}

// viewerResponse identifies the authenticated reviewer as seen by
// the configured GitHub token. The UI uses Login to highlight PRs
// where the viewer is a requested reviewer.
type viewerResponse struct {
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
}

// authorGroupResponse is the wire shape for an author group; it
// flattens db.AuthorGroup's Members slice + timestamps into the
// JSON form the dashboard consumes.
type authorGroupResponse struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type authorGroupsResponse struct {
	Groups []authorGroupResponse `json:"groups"`
}

// patchsetResponse is the wire shape for one observed PR head
// SHA. The UI uses these as Gerrit-style "PSn" chips so a
// reviewer can compare any two pushes against each other.
type patchsetResponse struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"      doc:"Sequential PSn label, 1-based per PR"`
	HeadSHA      string `json:"head_sha"`
	BaseSHA      string `json:"base_sha,omitempty"`
	MergeBaseSHA string `json:"merge_base_sha,omitempty"`
	ObservedAt   string `json:"observed_at" doc:"UTC RFC3339 timestamp of when sync first saw this head"`
}

type patchsetsResponse struct {
	Patchsets []patchsetResponse `json:"patchsets"`
}

// blobRangeResponse serves a slice of the file blob at a given
// sha so the diff viewer can expand context around hunks. Lines
// are returned as-is (no trailing newline); callers re-insert
// line separators when rendering.
type blobRangeResponse struct {
	Lines []string `json:"lines"`
}

// prNotesResponse is the reviewer-local scratchpad for a PR. The
// UpdatedAt field is zero when the user has never written a note.
type prNotesResponse struct {
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at,omitempty" doc:"UTC RFC3339 timestamp of last save (empty when never saved)"`
}

type workspaceResponse struct {
	ID               string  `json:"id"`
	PlatformHost     string  `json:"platform_host"`
	RepoOwner        string  `json:"repo_owner"`
	RepoName         string  `json:"repo_name"`
	MRNumber         int     `json:"mr_number"`
	MRHeadRef        string  `json:"mr_head_ref"`
	WorktreePath     string  `json:"worktree_path"`
	TmuxSession      string  `json:"tmux_session"`
	Status           string  `json:"status"`
	ErrorMessage     *string `json:"error_message,omitempty"`
	CreatedAt        string  `json:"created_at"`
	MRTitle          *string `json:"mr_title,omitempty"`
	MRState          *string `json:"mr_state,omitempty"`
	MRIsDraft        *bool   `json:"mr_is_draft,omitempty"`
	MRCIStatus       *string `json:"mr_ci_status,omitempty"`
	MRReviewDecision *string `json:"mr_review_decision,omitempty"`
	MRAdditions      *int    `json:"mr_additions,omitempty"`
	MRDeletions      *int    `json:"mr_deletions,omitempty"`
}

type workspaceMRRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func toWorkspaceResponse(
	s *db.WorkspaceSummary,
) workspaceResponse {
	return workspaceResponse{
		ID:               s.ID,
		PlatformHost:     s.PlatformHost,
		RepoOwner:        s.RepoOwner,
		RepoName:         s.RepoName,
		MRNumber:         s.MRNumber,
		MRHeadRef:        s.MRHeadRef,
		WorktreePath:     s.WorktreePath,
		TmuxSession:      s.TmuxSession,
		Status:           s.Status,
		ErrorMessage:     s.ErrorMessage,
		CreatedAt:        s.CreatedAt.UTC().Format(time.RFC3339),
		MRTitle:          s.MRTitle,
		MRState:          s.MRState,
		MRIsDraft:        s.MRIsDraft,
		MRCIStatus:       s.MRCIStatus,
		MRReviewDecision: s.MRReviewDecision,
		MRAdditions:      s.MRAdditions,
		MRDeletions:      s.MRDeletions,
	}
}

const activitySafetyCap = 5000

type activityResponse struct {
	Items  []activityItemResponse `json:"items"`
	Capped bool                   `json:"capped"`
}

type stackMemberResponse struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	CIStatus       string `json:"ci_status"`
	ReviewDecision string `json:"review_decision"`
	Position       int    `json:"position"`
	IsDraft        bool   `json:"is_draft"`
	BaseBranch     string `json:"base_branch"`
	BlockedBy      *int   `json:"blocked_by"`
}

type stackResponse struct {
	ID        int64                 `json:"id"`
	Name      string                `json:"name"`
	RepoOwner string                `json:"repo_owner"`
	RepoName  string                `json:"repo_name"`
	Health    string                `json:"health"`
	Members   []stackMemberResponse `json:"members"`
}

type stackContextResponse struct {
	// InStack is false when this PR is not part of any stack — most
	// PRs aren't, so this is the common case. The endpoint returns
	// 200 with InStack=false rather than 404 so the browser console
	// stays quiet for routine "no stack here" lookups; callers should
	// branch on InStack rather than treating an absent stack as an
	// error.
	InStack   bool                  `json:"in_stack"`
	StackID   int64                 `json:"stack_id"`
	StackName string                `json:"stack_name"`
	Position  int                   `json:"position"`
	Size      int                   `json:"size"`
	Health    string                `json:"health"`
	Members   []stackMemberResponse `json:"members"`
}

// worktreeResponse is one local git worktree discovered under a
// repo's configured local_path. Surfaces in the Open sidebar
// alongside GitHub PRs.
type worktreeResponse struct {
	ID           int64  `json:"id"`
	RepoOwner    string `json:"repo_owner"`
	RepoName     string `json:"repo_name"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	HeadSHA      string `json:"head_sha,omitempty"`
	IsDetached   bool   `json:"is_detached,omitempty"`
	IsLocked     bool   `json:"is_locked,omitempty"`
	IsPrunable   bool   `json:"is_prunable,omitempty"`
	DiscoveredAt    string `json:"discovered_at" doc:"UTC RFC3339 timestamp of when sync first saw this worktree"`
	LastSeenAt      string `json:"last_seen_at" doc:"UTC RFC3339 timestamp of the most recent scan that observed this worktree"`
	HasRunningTurn  bool   `json:"has_running_turn,omitempty" doc:"True when this worktree has an active Claude session with a queued or running response turn"`
}

type worktreesResponse struct {
	Worktrees []worktreeResponse `json:"worktrees"`
}

// changedFileResponse is one entry in a worktree's change set
// relative to its base ref.
type changedFileResponse struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status" doc:"added | modified | deleted | renamed | copied"`
	IsBinary  bool   `json:"is_binary,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// worktreeBaseResponse describes the commit a worktree's change
// list is computed against.
type worktreeBaseResponse struct {
	Ref      string `json:"ref" doc:"Matched candidate ref, e.g. \"origin/main\". Empty when no candidate resolved."`
	SHA      string `json:"sha" doc:"Merge-base SHA, or worktree HEAD when fallback is true."`
	Fallback bool   `json:"fallback,omitempty" doc:"True when no remote tracking branch resolved; SHA is the worktree HEAD instead of a merge-base."`
}

type worktreeChangedFilesResponse struct {
	Base  worktreeBaseResponse  `json:"base"`
	Files []changedFileResponse `json:"files"`
}

// worktreeDiffResponse is the structured diff between a worktree's
// resolved base and the working tree. Reuses gitclone.DiffFile so
// the hunk/line shape matches the PR-side endpoints.
type worktreeDiffResponse struct {
	Base  worktreeBaseResponse `json:"base"`
	Files []gitclone.DiffFile  `json:"files"`
}

type activityItemResponse struct {
	ID           string `json:"id"`
	Cursor       string `json:"cursor"`
	ActivityType string `json:"activity_type"`
	RepoOwner    string `json:"repo_owner"`
	RepoName     string `json:"repo_name"`
	ItemType     string `json:"item_type"`
	ItemNumber   int    `json:"item_number"`
	ItemTitle    string `json:"item_title"`
	ItemURL      string `json:"item_url"`
	ItemState    string `json:"item_state"`
	Author       string `json:"author"`
	CreatedAt    string `json:"created_at"`
	BodyPreview  string `json:"body_preview"`
}
