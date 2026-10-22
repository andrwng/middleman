package aireview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/wesm/middleman/internal/gitenv"
)

// SnapshotRef is the ref that keeps a local review run's snapshot commit
// alive for as long as its suggestions may be discussed.
func SnapshotRef(runID int64) string {
	return fmt.Sprintf("refs/middleman/review-snapshots/%d", runID)
}

// SnapshotWorkingTree commits the worktree's current state (tracked,
// staged, unstaged and untracked files; ignored files excluded) with
// HEAD as parent and pins it at SnapshotRef(runID). It builds the tree in
// a throwaway index, so the reviewer's index, files, and branch are never
// touched.
func SnapshotWorkingTree(ctx context.Context, worktreePath string, runID int64) (string, error) {
	idx, err := os.CreateTemp("", "middleman-snapshot-index-*")
	if err != nil {
		return "", err
	}
	idxPath := idx.Name()
	_ = idx.Close()
	_ = os.Remove(idxPath) // git wants to create the index itself
	defer func() { _ = os.Remove(idxPath) }()

	env := append(gitenv.StripAll(os.Environ()),
		"GIT_INDEX_FILE="+idxPath,
		"GIT_AUTHOR_NAME=middleman", "GIT_AUTHOR_EMAIL=middleman@localhost",
		"GIT_COMMITTER_NAME=middleman", "GIT_COMMITTER_EMAIL=middleman@localhost",
	)
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = worktreePath
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			msg := err.Error()
			if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
				msg = strings.TrimSpace(string(ee.Stderr))
			}
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run("read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := run("add", "-A"); err != nil {
		return "", err
	}
	tree, err := run("write-tree")
	if err != nil {
		return "", err
	}
	sha, err := run("commit-tree", tree, "-p", "HEAD", "-m", fmt.Sprintf("middleman review snapshot %d", runID))
	if err != nil {
		return "", err
	}
	if _, err := run("update-ref", SnapshotRef(runID), sha); err != nil {
		return "", err
	}
	return sha, nil
}

// DeleteSnapshotRef drops a run's snapshot ref so git can collect it.
func DeleteSnapshotRef(ctx context.Context, worktreePath string, runID int64) error {
	cmd := exec.CommandContext(ctx, "git", "update-ref", "-d", SnapshotRef(runID))
	cmd.Dir = worktreePath
	cmd.Env = gitenv.StripAll(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-ref -d: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
