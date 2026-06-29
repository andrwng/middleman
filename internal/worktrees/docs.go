package worktrees

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// MarkdownFiles lists the markdown files in the worktree at repoPath:
// tracked plus untracked-but-not-ignored, as repo-relative paths,
// sorted and deduped. Mirrors the worktree's live (working-tree) state.
func MarkdownFiles(ctx context.Context, repoPath string) ([]string, error) {
	if repoPath == "" {
		return nil, fmt.Errorf("repoPath is required")
	}
	// Pathspecs without a leading directory match recursively, so
	// '*.md' covers nested paths too.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
		"*.md", "*.mdx", "*.markdown",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files at %s: %w: %s", repoPath, err, strings.TrimSpace(stderr.String()))
	}
	seen := map[string]struct{}{}
	var files []string
	for p := range bytes.SplitSeq(out, []byte{0}) {
		s := string(p)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		files = append(files, s)
	}
	sort.Strings(files)
	return files, nil
}
