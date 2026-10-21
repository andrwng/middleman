package worktrees

import (
	"bytes"
	"context"
	"fmt"

	"github.com/wesm/middleman/internal/gitclone"
)

// FileDiff is one worktree file with structured hunks.
// It mirrors gitclone.DiffFile so the API can return the same
// shape the existing PR diff machinery emits.
type FileDiff = gitclone.DiffFile

// DiffSet is the worktree's full diff against its resolved base.
type DiffSet struct {
	Base  BaseRef    `json:"base"`
	Files []FileDiff `json:"files"`
}

// WorkingTreeSentinel is the synthetic "SHA" we use to address the
// uncommitted-state placeholder commit in the commits panel. When
// the diff endpoint receives ?commit=WORKING-TREE, it serves the
// working tree vs HEAD diff (the uncommitted changes) rather than
// looking up a real commit.
//
// Keeping this in the worktrees package — not gitclone — makes
// the convention explicit at the layer that produces it.
const WorkingTreeSentinel = "WORKING-TREE"

// HasUncommittedChanges reports whether the worktree has any
// staged, unstaged, or untracked changes that `git diff HEAD`
// plus `git ls-files --others` would surface. Used by the
// commits-panel dispatch to decide whether to prepend the
// synthetic "Uncommitted changes" entry.
func HasUncommittedChanges(ctx context.Context, worktreePath string) (bool, error) {
	out, err := gitCmd(ctx, worktreePath, "status", "--porcelain=v1")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// Revspec carries the git revision arguments for one of the review pane's
// diff scopes. diffRevspec, SummaryRevspec, and DiffRevspecPaths all append
// it to a shared flag prefix, so every scope goes through one implementation
// of each operation instead of a copy per mode.
type Revspec struct {
	Args []string
}

// RevspecSingleCommit diffs one commit against its first parent. ^! is
// shorthand for that.
func RevspecSingleCommit(sha string) Revspec {
	return Revspec{Args: []string{sha + "^!"}}
}

// RevspecCommitSpan diffs the net change of every commit from oldest through
// newest, both included -- see DiffCommitSpan for why `^..` is inclusive of
// oldest where a plain `oldest..newest` would not be.
func RevspecCommitSpan(oldest, newest string) Revspec {
	return Revspec{Args: []string{oldest + "^.." + newest}}
}

// RevspecWorkingTreeVsHEAD diffs the uncommitted state against HEAD.
func RevspecWorkingTreeVsHEAD() Revspec {
	return Revspec{Args: []string{"HEAD"}}
}

// RevspecAgainstBase diffs the working tree against the resolved base SHA.
// Like RevspecWorkingTreeVsHEAD, this is a single revision arg (no `..`):
// git diffs it against the working tree, not against another revision.
func RevspecAgainstBase(baseSHA string) Revspec {
	return Revspec{Args: []string{baseSHA}}
}

// diffRevspec runs the raw+patch pair of git diffs the review pane needs and
// merges them into structured files. The callers below differ only in the
// Revspec they hand it -- they used to be separate copies of this body,
// which is how DiffRange came to leave out a commit the others included.
func diffRevspec(
	ctx context.Context, worktreePath string, r Revspec,
) ([]gitclone.DiffFile, error) {
	rawArgs := append([]string{
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder",
	}, r.Args...)
	rawOut, err := gitCmd(ctx, worktreePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw %v: %w", r.Args, err)
	}
	files := gitclone.ParseRawZ(rawOut)

	patchArgs := append([]string{
		"diff", "-M", "-C", "--find-copies-harder",
	}, r.Args...)
	patchOut, err := gitCmd(ctx, worktreePath, patchArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff %v: %w", r.Args, err)
	}
	files = gitclone.ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}
	return files, nil
}

// SummaryRevspec runs the raw+numstat pair for a Revspec scope -- no patch
// content -- the primitive behind a /files-style summary response. Mirrors
// gitclone.DiffSummary's shape: status, old_path, and per-file counts, with
// Hunks always empty.
func SummaryRevspec(
	ctx context.Context, worktreePath string, r Revspec,
) ([]gitclone.DiffFile, error) {
	rawArgs := append([]string{
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder",
	}, r.Args...)
	rawOut, err := gitCmd(ctx, worktreePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw %v: %w", r.Args, err)
	}
	files := gitclone.ParseRawZ(rawOut)

	numstatArgs := append([]string{
		"diff", "--numstat", "-z", "-M", "-C", "--find-copies-harder",
	}, r.Args...)
	numOut, err := gitCmd(ctx, worktreePath, numstatArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat %v: %w", r.Args, err)
	}
	entries := gitclone.ParseNumstatZ(numOut)

	for i := range files {
		if e, ok := entries[files[i].Path]; ok {
			files[i].Additions = e.Additions
			files[i].Deletions = e.Deletions
			files[i].IsBinary = e.Binary
		}
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}
	return files, nil
}

// cleanPaths drops empty and duplicate entries, preserving the order of
// first occurrence. An empty string is not a valid git pathspec ("empty
// string is not a valid pathspec; perhaps you forgot to add either ':/' or
// '.' ?"), so a caller-supplied "" must never reach the pathspec list.
func cleanPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// DiffRevspecPaths runs the raw+patch pair for a Revspec scope, limited to
// the given new and old paths via --literal-pathspecs, and keeps only files
// whose path or old_path is in the requested set (dropping a stray partner
// the pathspec might otherwise introduce). paths is cleaned of empty and
// duplicate entries first; if nothing remains, this returns no files
// without running git. The caller is expected to pass both new and old
// paths for renames and copies.
func DiffRevspecPaths(
	ctx context.Context, worktreePath string, r Revspec, paths []string,
) ([]gitclone.DiffFile, error) {
	paths = cleanPaths(paths)
	if len(paths) == 0 {
		return []gitclone.DiffFile{}, nil
	}

	rawArgs := []string{"--literal-pathspecs", "diff", "--raw", "-z", "-M", "-C", "--find-copies-harder"}
	rawArgs = append(rawArgs, r.Args...)
	rawArgs = append(rawArgs, "--")
	rawArgs = append(rawArgs, paths...)
	rawOut, err := gitCmd(ctx, worktreePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw %v: %w", r.Args, err)
	}
	files := gitclone.ParseRawZ(rawOut)

	patchArgs := []string{"--literal-pathspecs", "diff", "-M", "-C", "--find-copies-harder"}
	patchArgs = append(patchArgs, r.Args...)
	patchArgs = append(patchArgs, "--")
	patchArgs = append(patchArgs, paths...)
	patchOut, err := gitCmd(ctx, worktreePath, patchArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff %v: %w", r.Args, err)
	}
	files = gitclone.ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}

	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	kept := files[:0]
	for _, f := range files {
		if pathSet[f.Path] || pathSet[f.OldPath] {
			kept = append(kept, f)
		}
	}
	return kept, nil
}

// DiffSingleCommit returns the diff for a single commit (against
// its first parent). Used when the user picks a specific commit
// in the review pane's commits panel.
func DiffSingleCommit(
	ctx context.Context, worktreePath, sha string,
) ([]gitclone.DiffFile, error) {
	return diffRevspec(ctx, worktreePath, RevspecSingleCommit(sha))
}

// DiffCommitSpan returns the net change of every commit from `oldest` through
// `newest`, both included. Used by the review pane's span-of-commits scope,
// where the reader shift-clicked two commits and means "these and everything
// between them".
//
// Inclusive of `oldest`, hence `^..` -- `oldest..newest` would be git's
// two-endpoint form and would drop `oldest`'s own changes, which is what this
// scope did until it was reported. Mirrors the PR-backed path, which resolves
// ParentOf(from) for the same reason (see getDiff in internal/server).
//
// Every commit the panel can offer comes from `base..HEAD`, so `oldest` always
// has a parent for `^` to resolve.
func DiffCommitSpan(
	ctx context.Context, worktreePath, oldest, newest string,
) ([]gitclone.DiffFile, error) {
	return diffRevspec(ctx, worktreePath, RevspecCommitSpan(oldest, newest))
}

// DiffRange returns the diff between two endpoints, EXCLUDING the changes
// introduced by `from` itself. That is what DiffBaseToHEAD wants -- the merge
// base's own commit belongs to the base branch, not to the branch under
// review. For a reader-selected span of commits, use DiffCommitSpan.
//
// This is git's two-endpoint form, not one of the four named Revspec
// constructors -- it is only ever reached through DiffBaseToHEAD.
func DiffRange(
	ctx context.Context, worktreePath, from, to string,
) ([]gitclone.DiffFile, error) {
	return diffRevspec(ctx, worktreePath, Revspec{Args: []string{from + ".." + to}})
}

// DiffWorkingTreeVsHEAD returns the structured diff for the
// uncommitted state — what `git diff HEAD` plus untracked-file
// awareness yields. This is the data behind the synthetic
// WorkingTreeSentinel entry in the commits panel.
func DiffWorkingTreeVsHEAD(
	ctx context.Context, worktreePath string,
) ([]gitclone.DiffFile, error) {
	return diffRevspec(ctx, worktreePath, RevspecWorkingTreeVsHEAD())
}

// DiffBaseToHEAD returns the structured diff between the resolved
// base and the worktree's HEAD — committed work only. This is the
// "default" view the PR-shaped review pane shows when no commit
// scope is selected. Uncommitted changes appear separately via
// the synthetic WorkingTreeSentinel entry.
func DiffBaseToHEAD(
	ctx context.Context, worktreePath, overrideRef string,
) (DiffSet, error) {
	base, err := ResolveBase(ctx, worktreePath, overrideRef)
	if err != nil {
		return DiffSet{}, fmt.Errorf("resolve base: %w", err)
	}
	// If HEAD == base (no committed work since base), no files.
	if base.Fallback {
		// Fallback means base is HEAD already — committed-work diff is empty.
		return DiffSet{Base: base, Files: []FileDiff{}}, nil
	}
	files, err := DiffRange(ctx, worktreePath, base.SHA, "HEAD")
	if err != nil {
		return DiffSet{}, err
	}
	return DiffSet{Base: base, Files: files}, nil
}

// DiffAgainstBase resolves the worktree's base (origin/main /
// override / fallback) and returns the structured diff between
// that base and the working tree, with per-file hunks.
//
// Hunks cover only files that git tracks at one or both ends —
// pure-untracked files do not appear here. The file list endpoint
// surfaces them separately; this endpoint is specifically the
// "show me what changed inside the files" view.
func DiffAgainstBase(
	ctx context.Context, worktreePath, overrideRef string,
) (DiffSet, error) {
	if worktreePath == "" {
		return DiffSet{}, fmt.Errorf("worktreePath is required")
	}
	base, err := ResolveBase(ctx, worktreePath, overrideRef)
	if err != nil {
		return DiffSet{}, fmt.Errorf("resolve base: %w", err)
	}

	files, err := diffRevspec(ctx, worktreePath, RevspecAgainstBase(base.SHA))
	if err != nil {
		return DiffSet{}, err
	}

	return DiffSet{Base: base, Files: files}, nil
}
