package server

import (
	"context"
	"fmt"
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
		ID:             -w.ID, // negative to avoid colliding with real PRs
		RepoID:         w.RepoID,
		Number:         int(w.ID),
		URL:            "", // no remote
		Title:          fmt.Sprintf("Worktree: %s", branch),
		HeadBranch:     branch,
		BaseBranch:     base.Ref,
		State:          "open",
		PlatformHeadSHA: w.HeadSHA,
		DiffHeadSHA:    w.HeadSHA,
		MergeBaseSHA:   base.SHA,
		CreatedAt:      w.DiscoveredAt,
		UpdatedAt:      w.LastSeenAt,
		LastActivityAt: w.LastSeenAt,
		DetailFetchedAt: &now,
	}
	resp := mergeRequestDetailResponse{
		MergeRequest:    mr,
		Events:          []db.MREvent{},
		RepoOwner:       localOwner,
		RepoName:        input.Name,
		PlatformHost:    "local",
		WorktreeLinks:   []worktreeLinkResponse{},
		DetailLoaded:    true,
		DetailFetchedAt: formatUTCRFC3339(now),
	}
	return &getPullOutput{Body: resp}, nil
}

// getDiffLocal dispatches the PR-shaped diff endpoint to the
// worktree's full-tree diff. Scope params (commit, from, to,
// patchsets) are ignored for v1 — worktrees don't carry the same
// history-scoped review surfaces yet.
func (s *Server) getDiffLocal(
	ctx context.Context, input *getDiffInput,
) (*getDiffOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	ds, err := worktrees.DiffAgainstBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error500InternalServerError("worktree diff failed: " + err.Error())
	}
	return &getDiffOutput{Body: diffResponse{
		Stale:               false,
		WhitespaceOnlyCount: 0,
		Files:               ds.Files,
	}}, nil
}

// getCommitsLocal returns the commits between the worktree's
// resolved base and HEAD. Surfaces in the same commitsResponse
// shape as the PR endpoint.
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
	commits, err := worktrees.ListCommits(ctx, w.Path, base.SHA)
	if err != nil {
		return nil, huma.Error500InternalServerError("list commits: " + err.Error())
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

// getFilesLocal returns the lightweight file list for a worktree.
// Strips hunks from the same DiffSet getDiffLocal would return so
// callers paying for the cheap endpoint don't get the full patch
// payload.
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
