package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
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

	revspec, _, err := s.localRevspec(ctx, *w, input.Commit, input.From, input.To)
	if err != nil {
		return nil, localScopeError(err, "worktree diff failed")
	}
	files, err := worktrees.DiffRevspec(ctx, w.Path, revspec)
	if err != nil {
		return nil, huma.Error500InternalServerError("worktree diff failed: " + err.Error())
	}

	return &getDiffOutput{Body: diffResponse{
		Stale:               false,
		WhitespaceOnlyCount: 0,
		Files:               files,
	}}, nil
}

// localRevspec resolves the review pane's scope query params (commit,
// from/to, or the default against-base scope) to a worktrees.Revspec plus
// the SHA that scope's diff_from resolves to, for a local worktree. It is
// the Revspec-dispatch counterpart of getDiff's resolveDiffRange: both
// getDiffLocal and getFilesLocal call this once and hand the Revspec to
// worktrees.DiffRevspec or worktrees.SummaryRevspec respectively, instead of
// switching on scope params twice.
//
// baseSHA is:
//   - commit/span scopes: the resolved parent of the oldest commit in scope
//     (`git rev-parse <sha>^`), matching the PR-backed path's ParentOf(from).
//   - WORKING-TREE scope: the worktree's current HEAD SHA.
//   - default (against-base) scope: the resolved base SHA.
//
// An invalid combination of scope params (e.g. 'commit' with 'from'/'to')
// returns a huma 400, exactly like the PR-backed /diff and /files endpoints;
// callers should propagate that error as-is rather than wrapping it in a
// 500 (see localScopeError).
func (s *Server) localRevspec(
	ctx context.Context, w db.Worktree, commit, from, to string,
) (worktrees.Revspec, string, error) {
	hasCommit := commit != ""
	hasFrom := from != ""
	hasTo := to != ""

	switch {
	case !hasCommit && !hasFrom && !hasTo:
		baseRef := s.lookupBaseRefForWorktree(ctx, w)
		base, err := worktrees.ResolveBase(ctx, w.Path, baseRef)
		if err != nil {
			return worktrees.Revspec{}, "", fmt.Errorf("resolve base: %w", err)
		}
		return worktrees.RevspecAgainstBase(base.SHA), base.SHA, nil

	case hasCommit && !hasFrom && !hasTo:
		if commit == worktrees.WorkingTreeSentinel {
			headSHA, err := worktrees.ResolveCommitSHA(ctx, w.Path, "HEAD")
			if err != nil {
				return worktrees.Revspec{}, "", fmt.Errorf("resolve HEAD: %w", err)
			}
			return worktrees.RevspecWorkingTreeVsHEAD(), headSHA, nil
		}
		parent, err := worktrees.ResolveCommitSHA(ctx, w.Path, commit+"^")
		if err != nil {
			return worktrees.Revspec{}, "", fmt.Errorf("resolve parent of %s: %w", commit, err)
		}
		return worktrees.RevspecSingleCommit(commit), parent, nil

	case !hasCommit && hasFrom && hasTo:
		// Inclusive of `from`, matching the PR-backed path's ParentOf(from).
		parent, err := worktrees.ResolveCommitSHA(ctx, w.Path, from+"^")
		if err != nil {
			return worktrees.Revspec{}, "", fmt.Errorf("resolve parent of %s: %w", from, err)
		}
		return worktrees.RevspecCommitSpan(from, to), parent, nil

	default:
		return worktrees.Revspec{}, "", huma.Error400BadRequest(
			"invalid scope: use 'commit' alone or 'from'+'to' together")
	}
}

// localDiffTo returns the diff_to side of a local scope's resolved range,
// mirroring the cases localRevspec switches on. It is a plain function
// (not folded into localRevspec) because localRevspec's single baseSHA
// return only ever carries the diff_from side.
func localDiffTo(commit, from, to string) string {
	switch {
	case commit == worktrees.WorkingTreeSentinel:
		return ""
	case commit != "":
		return commit
	case from != "" && to != "":
		return to
	default:
		return ""
	}
}

// localScopeError passes a huma.StatusError (e.g. the 400 localRevspec
// returns for an invalid scope combo) through unwrapped, and otherwise
// wraps the error as a 500 with msg as context. Keeps getDiffLocal and
// getFilesLocal from turning localRevspec's intentional 400s into 500s.
func localScopeError(err error, msg string) error {
	var statusErr huma.StatusError
	if errors.As(err, &statusErr) {
		return statusErr
	}
	return huma.Error500InternalServerError(msg + ": " + err.Error())
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
	// Same defense as getSymbolRefs: sha reaches `git cat-file` as a
	// positional revision argument (worktrees.BlobRange), so reject
	// anything that isn't a hex object id or the working-tree sentinel
	// before it gets there. Reuses symbolRefsSHA rather than a second
	// copy of the pattern.
	if input.SHA != worktrees.WorkingTreeSentinel && !symbolRefsSHA.MatchString(input.SHA) {
		return nil, huma.Error400BadRequest("sha must be a hex object id")
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
	// Same defense as getSymbolRefs: sha reaches `git cat-file` as a
	// positional revision argument (worktrees.Blob), so reject anything
	// that isn't a hex object id or the working-tree sentinel before it
	// gets there. Reuses symbolRefsSHA rather than a second copy of the
	// pattern.
	if input.SHA != worktrees.WorkingTreeSentinel && !symbolRefsSHA.MatchString(input.SHA) {
		return nil, huma.Error400BadRequest("sha must be a hex object id")
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

// getTreeLocal serves the PR-shaped /tree endpoint for a local
// worktree. Reads either a committed directory listing (via git
// ls-tree in the worktree's .git dir) or the working-tree state (via
// WorkingTreeSentinel -> direct filesystem read). The bare-clone
// manager is bypassed entirely because local entries have no clone
// partition.
func (s *Server) getTreeLocal(
	ctx context.Context, input *getTreeInput,
) (*getTreeOutput, error) {
	if input.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	// Same defense as getBlobLocal: sha reaches `git ls-tree` as a
	// positional revision argument (worktrees.Tree), so reject anything
	// that isn't a hex object id or the working-tree sentinel before it
	// gets there. Reuses symbolRefsSHA rather than a second copy of the
	// pattern.
	if input.SHA != worktrees.WorkingTreeSentinel && !symbolRefsSHA.MatchString(input.SHA) {
		return nil, huma.Error400BadRequest("sha must be a hex object id")
	}
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	var entries []gitclone.TreeEntry
	if input.Recursive {
		entries, err = worktrees.TreeRecursive(ctx, w.Path, input.SHA)
	} else {
		entries, err = worktrees.Tree(ctx, w.Path, input.SHA, input.Path)
	}
	if err != nil {
		if errors.Is(err, worktrees.ErrNotFound) {
			return nil, huma.Error404NotFound("path not found: " + err.Error())
		}
		return nil, huma.Error502BadGateway("read tree: " + err.Error())
	}
	responsePath := input.Path
	if input.Recursive {
		responsePath = ""
	}
	return &getTreeOutput{Body: treeResponse{Path: responsePath, Entries: toTreeEntryJSON(entries)}}, nil
}

// getFilesLocal returns the lightweight file list for a worktree,
// honoring the same commit / from-to scope params as getDiffLocal (default
// scope: base vs working tree, the full-draft view). It runs only the
// raw+numstat pair (worktrees.SummaryRevspec) — no patch content — so this
// stays cheap even for a worktree with a huge diff; it must never fall back
// to the full-patch DiffAgainstBase.
//
// whitespace is accepted on the input for shape-parity with the PR-backed
// path but is not honored here: local mode has no whitespace-only pass, so
// whitespace_only_count is always 0, matching today's behavior.
func (s *Server) getFilesLocal(
	ctx context.Context, input *getFilesInput,
) (*getFilesOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}

	revspec, diffFrom, err := s.localRevspec(ctx, *w, input.Commit, input.From, input.To)
	if err != nil {
		return nil, localScopeError(err, "worktree files failed")
	}
	files, err := worktrees.SummaryRevspec(ctx, w.Path, revspec)
	if err != nil {
		return nil, huma.Error500InternalServerError("worktree files failed: " + err.Error())
	}

	return &getFilesOutput{Body: filesResponse{
		Stale:               false,
		WhitespaceOnlyCount: 0,
		Files:               files,
		DiffFrom:            diffFrom,
		DiffTo:              localDiffTo(input.Commit, input.From, input.To),
	}}, nil
}

// getSymbolRefsLocal serves the symbol search for a local worktree. The
// changed-file set comes from the same base-vs-working-tree diff
// getFilesLocal uses, so the two views agree on what "in the PR" means.
func (s *Server) getSymbolRefsLocal(
	ctx context.Context, input *getSymbolRefsInput, symbol string,
) (*getSymbolRefsOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("worktree not found")
	}
	baseRef := s.lookupBaseRefForWorktree(ctx, *w)
	ds, err := worktrees.DiffAgainstBase(ctx, w.Path, baseRef)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"symbol search changed-file lookup failed: " + err.Error())
	}

	gctx, cancel := context.WithTimeout(ctx, symbolRefsTimeout)
	defer cancel()
	hits, err := worktrees.GrepSymbol(gctx, w.Path, input.SHA, symbol)
	if err != nil {
		slog.Error("symbol search failed",
			"owner", input.Owner, "name", input.Name, "number", input.Number, "err", err)
		return nil, huma.Error502BadGateway("symbol search failed")
	}

	readBlob := func(p string) ([]byte, error) {
		return worktrees.Blob(gctx, w.Path, input.SHA, p)
	}
	changedSet := changedPathSet(ds.Files)
	tagsByPath := symbolRefTags(gctx, hits, changedSet, readBlob)
	resp := buildSymbolRefsResponse(symbol, hits, changedSet, tagsByPath)
	return &getSymbolRefsOutput{Body: resp}, nil
}

// resolveLocalWorktreeByPath powers GET /local/resolve: given an absolute
// worktree path, return the PR-shaped review handle and the live current
// branch. 404 when no active worktree matches. Loopback only, like the
// rest of the API. The path is canonicalized (EvalSymlinks) so an aliased
// path resolves the same row `git worktree list` reported.
func (s *Server) resolveLocalWorktreeByPath(
	ctx context.Context, input *localResolveInput,
) (*localResolveOutput, error) {
	if input.Path == "" {
		return nil, huma.Error400BadRequest("path is required")
	}
	canon := input.Path
	if resolved, err := filepath.EvalSymlinks(input.Path); err == nil {
		canon = resolved
	}
	wr, err := s.db.GetActiveWorktreeByPath(ctx, canon)
	if errors.Is(err, sql.ErrNoRows) {
		// Retry with the raw path in case the stored row isn't canonicalized.
		wr, err = s.db.GetActiveWorktreeByPath(ctx, input.Path)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("no middleman review for this directory: " + input.Path)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("resolve worktree by path: " + err.Error())
	}
	w := wr.Worktree
	return &localResolveOutput{Body: localResolveResponse{
		Owner:  localOwner,
		Name:   wr.RepoName,
		Number: w.ID,
		Branch: s.currentWorktreeBranch(ctx, &w),
	}}, nil
}
