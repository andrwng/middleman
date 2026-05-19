package worktrees

import (
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
