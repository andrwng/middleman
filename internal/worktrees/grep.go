package worktrees

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/wesm/middleman/internal/gitclone"
)

// GrepSymbol returns every word-boundary occurrence of symbol in the
// worktree at sha. The match is a fixed string (never a regex) and
// case-sensitive; binary files are skipped.
//
// The WorkingTreeSentinel SHA greps the files on disk, mirroring Blob,
// so an uncommitted edit is searchable through the same entry point as
// a committed revision. Only tracked files are searched in that mode —
// with no revision argument `git grep` searches the tracked files in
// the working tree and never untracked ones, which matches the diff
// the review surface shows (`git diff HEAD` likewise ignores untracked
// files).
//
// `git grep` exits 1 when nothing matched, which is an empty result
// rather than a failure.
func GrepSymbol(
	ctx context.Context, worktreePath, sha, symbol string,
) ([]gitclone.SymbolHit, error) {
	args := []string{
		"grep", "-n", "-z", "-w", "-F", "-I", "--no-color", "-e", symbol,
	}
	revPrefix := ""
	if sha != WorkingTreeSentinel {
		args = append(args, sha)
		revPrefix = sha + ":"
	}
	out, err := gitCmd(ctx, worktreePath, args...)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("git grep %q at %s: %w", symbol, sha, err)
	}
	return gitclone.ParseGrepZ(out, revPrefix), nil
}
