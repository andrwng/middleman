package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/worktrees"
)

func TestSourceAnchor(t *testing.T) {
	const cur = "1111111111111111111111111111111111111111"
	const orig = "2222222222222222222222222222222222222222"
	tests := []struct {
		name string
		meta reviewCommentAnchorMeta
		want reviewCommentAnchor
		ok   bool
	}{
		{
			name: "creation anchor wins when recorded",
			meta: reviewCommentAnchorMeta{Path: "a.go", Side: "RIGHT", Line: 9, StartLine: 7, CommitID: cur, OriginalLine: 3, OriginalStartLine: 1, OriginalCommitID: orig},
			want: reviewCommentAnchor{CommitID: orig, Path: "a.go", Line: 3, StartLine: 1},
			ok:   true,
		},
		{
			name: "row synced before original fields existed uses line and commit_id",
			meta: reviewCommentAnchorMeta{Path: "a.go", Line: 9, StartLine: 7, CommitID: cur},
			want: reviewCommentAnchor{CommitID: cur, Path: "a.go", Line: 9, StartLine: 7},
			ok:   true,
		},
		{
			name: "original commit without a line falls back to line and commit_id",
			meta: reviewCommentAnchorMeta{Path: "a.go", Line: 9, CommitID: cur, OriginalCommitID: orig},
			want: reviewCommentAnchor{CommitID: cur, Path: "a.go", Line: 9},
			ok:   true,
		},
		{
			name: "start line not before end collapses to a single line",
			meta: reviewCommentAnchorMeta{Path: "a.go", Line: 5, StartLine: 5, CommitID: cur},
			want: reviewCommentAnchor{CommitID: cur, Path: "a.go", Line: 5},
			ok:   true,
		},
		{name: "left side is skipped", meta: reviewCommentAnchorMeta{Path: "a.go", Side: "LEFT", Line: 9, CommitID: cur}},
		{name: "file-level comment is skipped", meta: reviewCommentAnchorMeta{Path: "a.go", SubjectType: "file", Line: 1, CommitID: cur}},
		{name: "no line is skipped", meta: reviewCommentAnchorMeta{Path: "a.go", CommitID: cur}},
		{name: "no commit is skipped", meta: reviewCommentAnchorMeta{Path: "a.go", Line: 9}},
		{name: "no path is skipped", meta: reviewCommentAnchorMeta{Line: 9, CommitID: cur}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sourceAnchor(tt.meta)
			Assert.Equal(t, tt.ok, ok)
			Assert.Equal(t, tt.want, got)
		})
	}
}

// anchorHistory builds a bare repository, the shape gitclone.Manager
// keeps: TARGET at line 3 (a 2-3 range), then a line inserted above it,
// then a rename, then TARGET deleted.
func anchorHistory(t *testing.T) (bare, c1, c2, c3, c4 string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	dir := t.TempDir()
	bare = filepath.Join(dir, "repo.git")
	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, work)
	runGit(t, work, "config", "user.email", "test@test.com")
	runGit(t, work, "config", "user.name", "Test")
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(body), 0o644))
	}
	commit := func(msg string) string {
		runGit(t, work, "add", "-A")
		runGit(t, work, "commit", "-m", msg)
		runGit(t, work, "push", "origin", "main")
		return testGitSHA(t, work, "HEAD")
	}
	write("a.txt", "one\ntwo\nTARGET\nfour\n")
	c1 = commit("c1")
	write("a.txt", "zero\none\ntwo\nTARGET\nfour\n")
	c2 = commit("c2")
	runGit(t, work, "mv", "a.txt", "b.txt")
	c3 = commit("c3")
	write("b.txt", "zero\none\ntwo\nfour\n")
	c4 = commit("c4")
	return bare, c1, c2, c3, c4
}

func TestReviewAnchorCacheResolve(t *testing.T) {
	bare, c1, c2, c3, c4 := anchorHistory(t)
	rangeAt := func(commit string, start, end int) reviewCommentAnchor {
		return reviewCommentAnchor{CommitID: commit, Path: "a.txt", Line: end, StartLine: start}
	}
	tests := []struct {
		name   string
		anchor reviewCommentAnchor
		target string
		want   resolvedReviewAnchor
	}{
		{
			name:   "anchor at the target is current without a diff",
			anchor: rangeAt(c2, 3, 4),
			target: c2,
			want:   resolvedReviewAnchor{State: worktrees.AnchorCurrent, Path: "a.txt", Line: 4, StartLine: 3},
		},
		{
			name:   "line inserted above moves both ends",
			anchor: rangeAt(c1, 2, 3),
			target: c2,
			want:   resolvedReviewAnchor{State: worktrees.AnchorMoved, Path: "a.txt", Line: 4, StartLine: 3},
		},
		{
			name:   "an older target maps backward",
			anchor: rangeAt(c2, 3, 4),
			target: c1,
			want:   resolvedReviewAnchor{State: worktrees.AnchorMoved, Path: "a.txt", Line: 3, StartLine: 2},
		},
		{
			name:   "rename follows the file",
			anchor: rangeAt(c1, 2, 3),
			target: c3,
			want:   resolvedReviewAnchor{State: worktrees.AnchorMoved, Path: "b.txt", Line: 4, StartLine: 3},
		},
		{
			name:   "deleted line is removed",
			anchor: rangeAt(c1, 0, 3),
			target: c4,
			want:   resolvedReviewAnchor{State: worktrees.AnchorRemoved},
		},
		{
			name:   "range whose start was deleted collapses to its end",
			anchor: reviewCommentAnchor{CommitID: c1, Path: "a.txt", Line: 4, StartLine: 3},
			target: c4,
			want:   resolvedReviewAnchor{State: worktrees.AnchorMoved, Path: "b.txt", Line: 4},
		},
		{
			name:   "option-shaped commit never reaches git",
			anchor: reviewCommentAnchor{CommitID: "--output=/tmp/x", Path: "a.txt", Line: 3},
			target: c2,
			want:   resolvedReviewAnchor{State: worktrees.AnchorUnmappable},
		},
		{
			name:   "commit missing from the clone is unmappable",
			anchor: reviewCommentAnchor{CommitID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Path: "a.txt", Line: 3},
			target: c2,
			want:   resolvedReviewAnchor{State: worktrees.AnchorUnmappable},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c reviewAnchorCache
			got := c.resolve(context.Background(), bare, tt.target, map[int64]reviewCommentAnchor{1: tt.anchor})
			Assert.Equal(t, tt.want, got[1])
		})
	}
}

func TestReviewAnchorCacheKeepsOnlyStableResults(t *testing.T) {
	assert := Assert.New(t)
	bare, c1, c2, _, _ := anchorHistory(t)
	moved := reviewCommentAnchor{CommitID: c1, Path: "a.txt", Line: 3}
	missing := reviewCommentAnchor{CommitID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Path: "a.txt", Line: 3}

	var c reviewAnchorCache
	first := c.resolve(context.Background(), bare, c2, map[int64]reviewCommentAnchor{1: moved, 2: missing})
	assert.Equal(worktrees.AnchorMoved, first[1].State)
	assert.Equal(worktrees.AnchorUnmappable, first[2].State)

	// Pointed at an empty directory, git can answer nothing: only a cached
	// result can come back.
	again := c.resolve(context.Background(), t.TempDir(), c2, map[int64]reviewCommentAnchor{1: moved})
	assert.Equal(first[1], again[1])
	_, cached := c.get(reviewAnchorKey{target: c2, anchor: missing})
	assert.False(cached, "a git read failure can heal after a fetch")
}

func TestReviewAnchorCacheLeavesOutAnchorsPastTheDeadline(t *testing.T) {
	bare, c1, c2, _, _ := anchorHistory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var c reviewAnchorCache
	got := c.resolve(ctx, bare, c2, map[int64]reviewCommentAnchor{1: {CommitID: c1, Path: "a.txt", Line: 3}})
	Assert.Empty(t, got, "an anchor not reached in time keeps its recorded position rather than reading unmappable")
}
