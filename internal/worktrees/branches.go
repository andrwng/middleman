package worktrees

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// BranchHeads maps each local branch tip, keyed by full commit SHA, to
// the names of the branches that point at it. It enumerates every
// local branch in the repository — refs/heads are shared across all of
// a repo's worktrees — so the result covers branches checked out in
// sibling worktrees too.
//
// excludeBranch is dropped from the result; callers pass the worktree's
// own current branch so a commit is only attributed to *other*
// branches. An empty excludeBranch (e.g. a detached worktree) excludes
// nothing. Branch names for a given SHA are sorted for stable
// presentation. A non-nil (possibly empty) map is returned on success.
func BranchHeads(ctx context.Context, worktreePath, excludeBranch string) (map[string][]string, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath is required")
	}
	out, err := gitCmd(ctx, worktreePath,
		"for-each-ref", "--format=%(objectname) %(refname:short)", "refs/heads",
	)
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	heads := make(map[string][]string)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// refnames cannot contain spaces, so the first space splits
		// the SHA from the (possibly slash-bearing) branch name.
		sha, name, ok := strings.Cut(line, " ")
		if !ok || name == "" || name == excludeBranch {
			continue
		}
		heads[sha] = append(heads[sha], name)
	}
	for sha := range heads {
		sort.Strings(heads[sha])
	}
	return heads, nil
}

// CurrentBranch returns the worktree's live checked-out branch via
// `git rev-parse --abbrev-ref HEAD`. A detached HEAD prints "HEAD",
// which we normalize to "" — the same convention the synthetic MR uses
// for a detached worktree. Returns an error when the path is not a git
// worktree (callers fall back to the scanned branch).
func CurrentBranch(ctx context.Context, worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("worktreePath is required")
	}
	out, err := gitCmd(ctx, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse --abbrev-ref HEAD: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "", nil // detached
	}
	return branch, nil
}
