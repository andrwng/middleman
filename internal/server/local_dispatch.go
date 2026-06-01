package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	"github.com/wesm/middleman/internal/worktrees"
)

// alias so we can drop the package-qualified name in the for-loop.
type gitcloneDiffFile = gitclone.DiffFile

// localOwner is the synthetic owner used in PR-shaped routes
// (/repos/{owner}/{name}/pulls/{number}/...) to address a local
// worktree. The number is the worktree row id; the name is the
// worktree's repo name (basename of the configured local_path).
//
// Routing local worktrees through PR-shaped URLs lets the frontend
// reuse the entire review pane (sidebar + diff viewer + per-file
// state + draft comments + AI threads) without each component
// learning a separate "is this a worktree?" branch. The dispatch
// happens here, at the request boundary; downstream code is
// blissfully unaware.
const localOwner = "local"

// isLocalSource reports whether a PR-shaped request is actually
// addressing a local worktree. Callers gate dispatch on this.
func isLocalSource(owner string) bool {
	return owner == localOwner
}

// resolveLocalWorktree maps a (name, number) pair from a PR-shaped
// path to its worktree row. Returns an error wrapping db's
// not-found semantics when no live worktree matches.
//
// The name match guards against a stale ID being interpreted
// against the wrong repo when a user has multiple local repos
// enrolled with overlapping ids.
func (s *Server) resolveLocalWorktree(
	ctx context.Context, name string, number int,
) (*db.Worktree, error) {
	w, err := s.db.GetWorktreeByID(ctx, int64(number))
	if err != nil {
		return nil, err
	}
	if w.RemovedAt != nil {
		return nil, fmt.Errorf("worktree %d no longer exists on disk", number)
	}
	repo, err := s.db.GetRepoByID(ctx, w.RepoID)
	if err != nil || repo == nil {
		return nil, fmt.Errorf("worktree %d: missing parent repo", number)
	}
	if repo.Name != name {
		return nil, fmt.Errorf("worktree %d: name mismatch (route=%q, db=%q)",
			number, name, repo.Name)
	}
	return &w, nil
}

// resolveOrEnsureMRID returns the MR id for any PR-shaped (owner,
// name, number) route. For real GitHub repos it's a straight DB
// lookup; for local sources it lazily upserts the synthetic MR row
// the rest of the PR-anchored machinery hangs off. Handlers that
// would otherwise call lookupMRID can substitute this and get
// local-source support for free.
func (s *Server) resolveOrEnsureMRID(
	ctx context.Context, owner, name string, number int,
) (int64, error) {
	if isLocalSource(owner) {
		w, err := s.resolveLocalWorktree(ctx, name, number)
		if err != nil {
			return 0, err
		}
		return s.ensureSyntheticMRForWorktree(ctx, w)
	}
	return s.lookupMRID(ctx, repoNumberPathRef{owner: owner, name: name, number: number})
}

// ensureSyntheticMRForWorktree upserts a row in
// middleman_merge_requests for the given worktree so the rest of
// the PR-anchored machinery (AI threads, AI briefs, commit
// analyses, PR notes) can FK to it without learning a separate
// "this is a worktree" code path.
//
// The row is keyed by (repo_id, number) = (local-repo, worktree.id).
// Most fields are minimal stubs derived from the worktree state;
// the row is invisible to GitHub-only flows because the parent
// repo has platform='local'.
//
// Idempotent: subsequent calls upsert and return the same id.
func (s *Server) ensureSyntheticMRForWorktree(
	ctx context.Context, w *db.Worktree,
) (int64, error) {
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, err := worktrees.ResolveBase(ctx, w.Path, baseRef)
	if err != nil {
		// Base resolution failure shouldn't block thread creation —
		// the runner can still work without merge-base info.
		base = worktrees.BaseRef{SHA: w.HeadSHA, Fallback: true}
	}
	branch := w.Branch
	if branch == "" {
		branch = "(detached)"
	}
	mr := &db.MergeRequest{
		RepoID:          w.RepoID,
		PlatformID:      w.ID,
		Number:          int(w.ID),
		Title:           "Worktree: " + branch,
		Author:          localOwner,
		State:           "open",
		HeadBranch:      branch,
		BaseBranch:      base.Ref,
		PlatformHeadSHA: w.HeadSHA,
		PlatformBaseSHA: base.SHA,
		DiffHeadSHA:     w.HeadSHA,
		DiffBaseSHA:     base.SHA,
		MergeBaseSHA:    base.SHA,
		CreatedAt:       w.DiscoveredAt,
		UpdatedAt:       w.LastSeenAt,
		LastActivityAt:  w.LastSeenAt,
	}
	return s.db.UpsertMergeRequest(ctx, mr)
}

// currentWorktreeBranch reads the worktree's live current branch so a
// branch switch takes effect immediately (no wait for the periodic
// scan). On any git error it falls back to the scanned worktrees.branch
// so the path stays robust. This is the single source of truth the
// in-app UI and the external proxy both agree on.
func (s *Server) currentWorktreeBranch(ctx context.Context, w *db.Worktree) string {
	if branch, err := worktrees.CurrentBranch(ctx, w.Path); err == nil {
		return branch
	}
	return w.Branch
}

// getPullLocal synthesizes a PR-shaped detail response for a local
// worktree. The synthesized MergeRequest fills the fields the
// review pane actually reads (RepoID, Number, Title, Author,
// HeadBranch, BaseBranch, State, CreatedAt, etc.); GitHub-only
// fields (CIStatus, MergeableState, ReviewDecision, etc.) stay at
// their zero values, which the UI gates on `isLocalSource` will
// hide.
func (s *Server) getPullLocal(
	ctx context.Context, input *repoNumberInput,
) (*getPullOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, _ := worktrees.ResolveBase(ctx, w.Path, baseRef)
	branch := w.Branch
	if branch == "" {
		branch = "(detached)"
	}
	now := time.Now().UTC()
	mr := &db.MergeRequest{
		ID:              -w.ID, // negative to avoid colliding with real PRs
		RepoID:          w.RepoID,
		Number:          int(w.ID),
		URL:             "", // no remote
		Title:           fmt.Sprintf("Worktree: %s", branch),
		HeadBranch:      branch,
		BaseBranch:      base.Ref,
		State:           "open",
		PlatformHeadSHA: w.HeadSHA,
		DiffHeadSHA:     w.HeadSHA,
		MergeBaseSHA:    base.SHA,
		CreatedAt:       w.DiscoveredAt,
		UpdatedAt:       w.LastSeenAt,
		LastActivityAt:  w.LastSeenAt,
		DetailFetchedAt: &now,
	}
	resp := mergeRequestDetailResponse{
		MergeRequest:        mr,
		Events:              []db.MREvent{},
		RepoOwner:           localOwner,
		RepoName:            input.Name,
		PlatformHost:        "local",
		WorktreeLinks:       []worktreeLinkResponse{},
		HiddenThreadRootIDs: []int64{},
		DetailLoaded:        true,
		DetailFetchedAt:     formatUTCRFC3339(now),
	}
	return &getPullOutput{Body: resp}, nil
}

// getDiffLocal dispatches the PR-shaped diff endpoint to the
// right git diff invocation based on the scope query params.
//
//	(none)                 → base..working-tree (committed + uncommitted +
//	                         untracked — the full draft state)
//	?commit=WORKING-TREE   → working tree vs HEAD (uncommitted only)
//	?commit=<sha>          → that commit's diff (parent..sha)
//	?from=<sha>&to=<sha>   → arbitrary range from..to
//
// The default scope deliberately includes uncommitted work — a
// worktree is a live draft, not a frozen PR, so "show me what's
// changed" should mean everything between origin and right-now.
// The synthetic Uncommitted-changes commit lets reviewers drill
// into just the uncommitted slice when they want it.
//
// Patchset-pair scope is intentionally not handled yet — worktrees
// don't carry the same observed-patchset history.
func (s *Server) getDiffLocal(
	ctx context.Context, input *getDiffInput,
) (*getDiffOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	var files []gitcloneDiffFile
	switch {
	case input.Commit == worktrees.WorkingTreeSentinel:
		files, err = worktrees.DiffWorkingTreeVsHEAD(ctx, w.Path)
	case input.Commit != "":
		files, err = worktrees.DiffSingleCommit(ctx, w.Path, input.Commit)
	case input.From != "" && input.To != "":
		files, err = worktrees.DiffRange(ctx, w.Path, input.From, input.To)
	default:
		baseRef := s.lookupBaseRefForWorktree(ctx, *w)
		ds, dsErr := worktrees.DiffAgainstBase(ctx, w.Path, baseRef)
		if dsErr != nil {
			return nil, huma.Error500InternalServerError("worktree diff failed: " + dsErr.Error())
		}
		files = ds.Files
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("worktree diff failed: " + err.Error())
	}

	return &getDiffOutput{Body: diffResponse{
		Stale:               false,
		WhitespaceOnlyCount: 0,
		Files:               files,
	}}, nil
}

// getCommitsLocal returns the commits between the worktree's
// resolved base and HEAD. When the worktree has uncommitted
// changes (staged, unstaged, or untracked), a synthetic
// WorkingTreeSentinel entry is prepended so reviewers can see
// and pick into the in-flight state from the same commits panel
// they use to navigate real commits.
func (s *Server) getCommitsLocal(
	ctx context.Context, input *repoNumberInput,
) (*getCommitsOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	base, err := worktrees.ResolveBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error500InternalServerError("resolve base: " + err.Error())
	}

	var resp commitsResponse

	// Synthetic "Uncommitted changes" entry first (only if the
	// worktree is dirty). The sentinel SHA flows through scope
	// params back to getDiffLocal, where it routes to
	// DiffWorkingTreeVsHEAD.
	dirty, _ := worktrees.HasUncommittedChanges(ctx, w.Path)
	if dirty {
		resp.Commits = append(resp.Commits, commitResponse{
			SHA:        worktrees.WorkingTreeSentinel,
			Message:    "Uncommitted changes",
			AuthorName: "(working tree)",
			AuthoredAt: time.Now().UTC(),
		})
	}

	commits, err := worktrees.ListCommits(ctx, w.Path, base.SHA)
	if err != nil {
		return nil, huma.Error500InternalServerError("list commits: " + err.Error())
	}
	for _, c := range commits {
		resp.Commits = append(resp.Commits, commitResponse{
			SHA:        c.SHA,
			Message:    c.Message,
			Body:       c.Body,
			AuthorName: c.AuthorName,
			AuthoredAt: c.AuthoredAt.UTC(),
		})
	}

	// Mark commits that are also the tip of another local branch.
	// Cosmetic only: on error, leave the commits undecorated rather
	// than failing the whole panel.
	if heads, err := worktrees.BranchHeads(ctx, w.Path, w.Branch); err != nil {
		slog.WarnContext(ctx, "resolve branch heads for worktree",
			"path", w.Path, "err", err)
	} else {
		for i := range resp.Commits {
			if names := heads[resp.Commits[i].SHA]; len(names) > 0 {
				resp.Commits[i].BranchHeads = names
			}
		}
	}

	if resp.Commits == nil {
		resp.Commits = []commitResponse{}
	}
	return &getCommitsOutput{Body: resp}, nil
}

// getBlobRangeLocal serves the PR-shaped /blob-range endpoint for
// a local worktree. Powers the diff viewer's "+N lines" click-to-
// expand affordance. Same dispatch shape as getBlobLocal:
// resolution flows through the worktree's on-disk path, not the
// bare-clone manager.
func (s *Server) getBlobRangeLocal(
	ctx context.Context, input *getBlobRangeInput,
) (*getBlobRangeOutput, error) {
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
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	lines, err := worktrees.BlobRange(ctx, w.Path, input.SHA, input.Path, input.Start, input.End)
	if err != nil {
		if errors.Is(err, worktrees.ErrNotFound) {
			return nil, huma.Error404NotFound("blob not found: " + err.Error())
		}
		return nil, huma.Error502BadGateway("read blob: " + err.Error())
	}
	return &getBlobRangeOutput{Body: blobRangeResponse{Lines: lines}}, nil
}

// getBlobLocal serves the PR-shaped /blob endpoint for a local
// worktree. Reads either a committed file (via git cat-file in the
// worktree's .git dir) or the working-tree state (via WorkingTreeSentinel
// → direct filesystem read). The bare-clone manager is bypassed
// entirely because local entries have no clone partition.
func (s *Server) getBlobLocal(
	ctx context.Context, input *getBlobInput,
) (*getBlobOutput, error) {
	if input.Path == "" {
		return nil, huma.Error400BadRequest("path is required")
	}
	if input.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	raw, err := worktrees.Blob(ctx, w.Path, input.SHA, input.Path)
	if err != nil {
		if errors.Is(err, worktrees.ErrNotFound) {
			return nil, huma.Error404NotFound("blob not found: " + err.Error())
		}
		return nil, huma.Error502BadGateway("read blob: " + err.Error())
	}
	if len(raw) > blobMaxBytes {
		return &getBlobOutput{Body: blobResponse{Truncated: true}}, nil
	}
	return &getBlobOutput{Body: blobResponse{Content: string(raw)}}, nil
}

// getFilesLocal returns the lightweight file list for a worktree's
// default scope — base vs working tree (the same full-draft view
// getDiffLocal serves when no scope params are passed). Hunks are
// stripped so callers paying for the cheap endpoint don't get the
// full patch payload.
func (s *Server) getFilesLocal(
	ctx context.Context, input *getFilesInput,
) (*getFilesOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	ds, err := worktrees.DiffAgainstBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error500InternalServerError("worktree files failed: " + err.Error())
	}
	files := make([]gitcloneDiffFile, 0, len(ds.Files))
	for _, f := range ds.Files {
		f.Hunks = nil
		files = append(files, f)
	}
	return &getFilesOutput{Body: filesResponse{
		Stale: false,
		Files: files,
	}}, nil
}
