package worktrees

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func TestSyncEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	store := openTestDB(t)
	repoID := insertTestRepo(t, store, "o", "r")

	root := t.TempDir()
	primary := filepath.Join(root, "main")
	feat := filepath.Join(root, "feat")
	hotfix := filepath.Join(root, "hotfix")

	runGit(t, "", "init", "--initial-branch=main", primary)
	runGit(t, primary, "config", "user.email", "test@example.com")
	runGit(t, primary, "config", "user.name", "Test")
	runGit(t, primary, "commit", "--allow-empty", "-m", "init")
	runGit(t, primary, "worktree", "add", "-b", "feat/x", feat)
	runGit(t, primary, "worktree", "add", "-b", "hotfix/y", hotfix)

	// First sync: all three appear active.
	live, err := Sync(ctx, store, repoID, primary)
	require.NoError(err)
	assert.Len(live, 3)

	active, err := store.ListWorktreesForRepo(ctx, repoID)
	require.NoError(err)
	require.Len(active, 3)

	// Remove the hotfix worktree and re-sync. It should be marked removed.
	runGit(t, primary, "worktree", "remove", hotfix)

	_, err = Sync(ctx, store, repoID, primary)
	require.NoError(err)

	active, err = store.ListWorktreesForRepo(ctx, repoID)
	require.NoError(err)
	require.Len(active, 2)
	// macOS reports canonical /private/var/... paths; canonicalize the
	// expected path before comparing.
	canonHotfix, err := filepath.EvalSymlinks(hotfix)
	if err != nil {
		// EvalSymlinks fails for a path that no longer exists, which is
		// expected after `git worktree remove`. Fall back to the raw path.
		canonHotfix = hotfix
	}
	paths := []string{active[0].Path, active[1].Path}
	assert.NotContains(paths, hotfix)
	assert.NotContains(paths, canonHotfix)
}

// openTestDB / insertTestRepo are minimal wrappers around the
// production helpers exposed by the db package's test code; here
// we re-implement them so this package keeps its own test fixtures.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func insertTestRepo(t *testing.T, d *db.DB, owner, name string) int64 {
	t.Helper()
	res, err := d.WriteDB().ExecContext(
		context.Background(),
		`INSERT INTO middleman_repos (platform, platform_host, owner, name) VALUES ('github', 'github.com', ?, ?)`,
		owner, name,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
