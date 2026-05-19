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

// DiffSingleCommit returns the diff for a single commit (against
// its first parent). Used when the user picks a specific commit
// in the review pane's commits panel.
func DiffSingleCommit(
	ctx context.Context, worktreePath, sha string,
) ([]gitclone.DiffFile, error) {
	rawOut, err := gitCmd(ctx, worktreePath,
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder",
		sha+"^!", // ^! is shorthand for the commit's diff vs its parent
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw %s: %w", sha, err)
	}
	files := gitclone.ParseRawZ(rawOut)
	patchOut, err := gitCmd(ctx, worktreePath,
		"diff", "-M", "-C", "--find-copies-harder", sha+"^!",
	)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", sha, err)
	}
	files = gitclone.ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}
	return files, nil
}

// DiffRange returns the diff between two commits (from..to) in
// the worktree. Used by the review pane's range-of-commits scope.
func DiffRange(
	ctx context.Context, worktreePath, from, to string,
) ([]gitclone.DiffFile, error) {
	rawOut, err := gitCmd(ctx, worktreePath,
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder",
		from+".."+to,
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw %s..%s: %w", from, to, err)
	}
	files := gitclone.ParseRawZ(rawOut)
	patchOut, err := gitCmd(ctx, worktreePath,
		"diff", "-M", "-C", "--find-copies-harder", from+".."+to,
	)
	if err != nil {
		return nil, fmt.Errorf("git diff %s..%s: %w", from, to, err)
	}
	files = gitclone.ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}
	return files, nil
}

// DiffWorkingTreeVsHEAD returns the structured diff for the
// uncommitted state — what `git diff HEAD` plus untracked-file
// awareness yields. This is the data behind the synthetic
// WorkingTreeSentinel entry in the commits panel.
func DiffWorkingTreeVsHEAD(
	ctx context.Context, worktreePath string,
) ([]gitclone.DiffFile, error) {
	rawOut, err := gitCmd(ctx, worktreePath,
		"diff", "HEAD", "--raw", "-z", "-M", "-C", "--find-copies-harder",
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw HEAD: %w", err)
	}
	files := gitclone.ParseRawZ(rawOut)
	patchOut, err := gitCmd(ctx, worktreePath,
		"diff", "HEAD", "-M", "-C", "--find-copies-harder",
	)
	if err != nil {
		return nil, fmt.Errorf("git diff HEAD: %w", err)
	}
	files = gitclone.ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}
	return files, nil
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

	rawOut, err := gitCmd(ctx, worktreePath,
		"diff", base.SHA, "--raw", "-z", "-M", "-C", "--find-copies-harder",
	)
	if err != nil {
		return DiffSet{}, fmt.Errorf("git diff --raw: %w", err)
	}
	files := gitclone.ParseRawZ(rawOut)

	patchOut, err := gitCmd(ctx, worktreePath,
		"diff", base.SHA, "-M", "-C", "--find-copies-harder",
	)
	if err != nil {
		return DiffSet{}, fmt.Errorf("git diff (patch): %w", err)
	}
	files = gitclone.ParsePatch(patchOut, files)

	// Ensure Hunks is never nil so JSON serializes as [] not null,
	// matching the PR-side endpoints' convention.
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []gitclone.Hunk{}
		}
	}

	return DiffSet{Base: base, Files: files}, nil
}
