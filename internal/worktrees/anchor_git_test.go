//go:build integration

package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchorRepo builds a worktree whose history exercises every resolution
// state: a TARGET line at 5, then three lines inserted above it, then a
// rename, then the deletion of TARGET itself.
func anchorRepo(t *testing.T) (dir string, c1, c2, c3, c4 string) {
	t.Helper()
	dir = t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")

	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	head := func() string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		require.NoError(t, err)
		return string(out[:40])
	}

	write("a.txt", "one\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGitT(t, dir, "add", "a.txt")
	runGitT(t, dir, "commit", "-m", "c1")
	c1 = head()

	write("a.txt", "new1\nnew2\nnew3\none\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGitT(t, dir, "add", "a.txt")
	runGitT(t, dir, "commit", "-m", "c2")
	c2 = head()

	runGitT(t, dir, "mv", "a.txt", "b.txt")
	runGitT(t, dir, "commit", "-m", "c3")
	c3 = head()

	write("b.txt", "new1\nnew2\nnew3\none\ntwo\nthree\nfour\nsix\n")
	runGitT(t, dir, "add", "b.txt")
	runGitT(t, dir, "commit", "-m", "c4")
	c4 = head()
	return dir, c1, c2, c3, c4
}

func TestAnchorDiffResolve(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	assert := assert.New(t)
	ctx := context.Background()
	dir, c1, c2, c3, c4 := anchorRepo(t)

	// One fetched diff answers any number of (path, line) questions, which
	// is what lets a caller with many threads at one commit pay for a
	// single git call.
	resolve := func(src, dst, path string, line int) ResolvedAnchor {
		return FetchAnchorDiff(ctx, dir, src, dst).Resolve(path, line)
	}

	// Unchanged between a revision and itself.
	got := resolve(c1, c1, "a.txt", 5)
	assert.Equal(AnchorCurrent, got.State)
	assert.Equal(5, got.Line)
	assert.Equal("a.txt", got.Path)

	// Three lines inserted above: 5 -> 8.
	got = resolve(c1, c2, "a.txt", 5)
	assert.Equal(AnchorMoved, got.State)
	assert.Equal(8, got.Line)

	// The file was renamed; the anchor follows it.
	got = resolve(c1, c3, "a.txt", 5)
	assert.Equal(AnchorMoved, got.State)
	assert.Equal("b.txt", got.Path)
	assert.Equal(8, got.Line)

	// TARGET itself is gone.
	got = resolve(c1, c4, "a.txt", 5)
	assert.Equal(AnchorRemoved, got.State)

	// An unknown source revision cannot be mapped from.
	got = resolve("0000000000000000000000000000000000000000", c4, "a.txt", 5)
	assert.Equal(AnchorUnmappable, got.State)

	// The working tree is a valid destination.
	got = resolve(c1, WorkingTreeSentinel, "a.txt", 5)
	assert.Equal(AnchorRemoved, got.State)

	// Two anchors off ONE diff: the insertion shifts every line below it
	// by the same three, and the fetched diff is reusable for both.
	shared := FetchAnchorDiff(ctx, dir, c1, c2)
	assert.Equal(8, shared.Resolve("a.txt", 5).Line)
	assert.Equal(9, shared.Resolve("a.txt", 6).Line)
}
