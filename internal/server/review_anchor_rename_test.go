package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/gitclone"
)

// renameRepo builds a bare clone of acme/widget where a.txt (30 lines)
// is renamed to b.txt with line 25 changed.
func renameRepo(t *testing.T) (*gitclone.Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "github.com", "acme", "widget.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(bare), 0o755))
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "--bare", "--initial-branch=main", bare)
	work := filepath.Join(root, "work")
	git(root, "clone", bare, work)
	git(work, "config", "user.email", "t@t")
	git(work, "config", "user.name", "T")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	git(work, "add", ".")
	git(work, "commit", "-m", "base")
	base := git(work, "rev-parse", "HEAD")
	git(work, "mv", "a.txt", "b.txt")
	lines[24] = "line 25 changed"
	require.NoError(t, os.WriteFile(filepath.Join(work, "b.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	git(work, "add", ".")
	git(work, "commit", "-m", "rename")
	head := git(work, "rev-parse", "HEAD")
	git(work, "push", "origin", "main")
	return gitclone.New(root, nil), base, head
}

func TestAnchorFileDiffFollowsRenames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	clones, base, head := renameRepo(t)
	f, err := anchorFileDiff(context.Background(), clones, "github.com", "acme", "widget", base, head, "b.txt")
	require.NoError(err)
	require.NotNil(f)
	assert.Equal("a.txt", f.OldPath)

	// Only the changed region is commentable, on both sides.
	assert.NoError(validateAnchor(f, "RIGHT", 25, nil))
	assert.NoError(validateAnchor(f, "LEFT", 25, nil))
	assert.Error(validateAnchor(f, "RIGHT", 5, nil))

	missing, err := anchorFileDiff(context.Background(), clones, "github.com", "acme", "widget", base, head, "nope.txt")
	require.NoError(err)
	assert.Nil(missing)
}
