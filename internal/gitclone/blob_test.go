package gitclone

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlobRange(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	commitTestRun(t, dir, "git", "init", "--bare", "--initial-branch=main", bare)

	work := filepath.Join(dir, "work")
	commitTestRun(t, dir, "git", "clone", bare, work)
	commitTestRun(t, work, "git", "config", "user.email", "test@test.com")
	commitTestRun(t, work, "git", "config", "user.name", "Test")

	content := "line1\nline2\nline3\nline4\nline5\n"
	require.NoError(os.WriteFile(filepath.Join(work, "hello.txt"), []byte(content), 0o644))
	commitTestRun(t, work, "git", "add", ".")
	commitTestRun(t, work, "git", "commit", "-m", "add hello")
	commitTestRun(t, work, "git", "push", "origin", "main")
	headSHA := gitSHA(t, work, "HEAD")

	mgr := New(filepath.Dir(bare), nil)
	ctx := context.Background()

	// Inner range.
	lines, err := mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", 2, 4)
	require.NoError(err)
	assert.Equal([]string{"line2", "line3", "line4"}, lines)

	// Start at 1.
	lines, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", 1, 2)
	require.NoError(err)
	assert.Equal([]string{"line1", "line2"}, lines)

	// End past EOF clamps silently.
	lines, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", 4, 100)
	require.NoError(err)
	assert.Equal([]string{"line4", "line5"}, lines)

	// Start past EOF returns empty.
	lines, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", 100, 200)
	require.NoError(err)
	assert.Empty(lines)

	// Missing file is an error.
	_, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "missing.txt", 1, 5)
	require.Error(err)

	// start < 1 gets clamped.
	lines, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", -5, 2)
	require.NoError(err)
	assert.Equal([]string{"line1", "line2"}, lines)

	// end < start errors out.
	_, err = mgr.BlobRange(ctx, "", "", "remote", headSHA, "hello.txt", 3, 2)
	require.Error(err)
}
