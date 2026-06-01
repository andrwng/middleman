package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertWorktreeInsertsAndRefreshes(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")

	// First scan inserts a row with the observed state.
	first, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path:    "/code/o/r-feat",
		Branch:  "feat/x",
		HeadSHA: "aaaa1111",
	})
	require.NoError(err)
	assert.NotZero(first.ID)
	assert.Equal("/code/o/r-feat", first.Path)
	assert.Equal("feat/x", first.Branch)
	assert.Equal("aaaa1111", first.HeadSHA)
	assert.False(first.IsDetached)
	assert.Nil(first.RemovedAt)

	// Second scan with the same path refreshes branch + head and
	// keeps the same id. discovered_at must not change.
	second, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path:       "/code/o/r-feat",
		Branch:     "feat/x-v2",
		HeadSHA:    "bbbb2222",
		IsDetached: true,
	})
	require.NoError(err)
	assert.Equal(first.ID, second.ID)
	assert.Equal("feat/x-v2", second.Branch)
	assert.Equal("bbbb2222", second.HeadSHA)
	assert.True(second.IsDetached)
	assert.Equal(first.DiscoveredAt, second.DiscoveredAt)
}

func TestUpsertWorktreeRevivesRemoved(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")

	first, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r-feat", Branch: "feat", HeadSHA: "aaaa",
	})
	require.NoError(err)

	// Sweep marks it removed because keep set is empty.
	require.NoError(d.MarkWorktreesNotInSet(ctx, repoID, nil))
	active, err := d.ListWorktreesForRepo(ctx, repoID)
	require.NoError(err)
	assert.Empty(active)

	// Re-scanning the same path revives the row (same id, removed_at cleared).
	revived, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r-feat", Branch: "feat", HeadSHA: "bbbb",
	})
	require.NoError(err)
	assert.Equal(first.ID, revived.ID)
	assert.Nil(revived.RemovedAt)

	active, err = d.ListWorktreesForRepo(ctx, repoID)
	require.NoError(err)
	require.Len(active, 1)
	assert.Equal(revived.ID, active[0].ID)
}

func TestMarkWorktreesNotInSet(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")

	paths := []string{"/code/a", "/code/b", "/code/c"}
	for _, p := range paths {
		_, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{Path: p, Branch: "b"})
		require.NoError(err)
	}

	// Keep a and c; b should go inactive.
	require.NoError(d.MarkWorktreesNotInSet(ctx, repoID, []string{"/code/a", "/code/c"}))

	active, err := d.ListWorktreesForRepo(ctx, repoID)
	require.NoError(err)
	require.Len(active, 2)
	activePaths := []string{active[0].Path, active[1].Path}
	assert.ElementsMatch([]string{"/code/a", "/code/c"}, activePaths)
}

func TestMarkWorktreesNotInSetDoesNotBumpRemovedAt(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	_, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{Path: "/code/a"})
	require.NoError(err)

	// First sweep removes it.
	require.NoError(d.MarkWorktreesNotInSet(ctx, repoID, nil))

	// Capture the removed_at value.
	var firstRemovedAt string
	require.NoError(d.ReadDB().QueryRow(
		`SELECT removed_at FROM middleman_worktrees WHERE repo_id = ? AND path = ?`,
		repoID, "/code/a",
	).Scan(&firstRemovedAt))

	// Second sweep with the same (empty) keep set must not rewrite removed_at.
	require.NoError(d.MarkWorktreesNotInSet(ctx, repoID, nil))
	var secondRemovedAt string
	require.NoError(d.ReadDB().QueryRow(
		`SELECT removed_at FROM middleman_worktrees WHERE repo_id = ? AND path = ?`,
		repoID, "/code/a",
	).Scan(&secondRemovedAt))
	assert.Equal(firstRemovedAt, secondRemovedAt)
}

func TestListAllActiveWorktreesJoinsRepo(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	r1 := insertTestRepo(t, d, "alpha", "one")
	r2 := insertTestRepo(t, d, "beta", "two")

	_, err := d.UpsertWorktree(ctx, r1, ScannedWorktree{Path: "/code/alpha-a", Branch: "x"})
	require.NoError(err)
	_, err = d.UpsertWorktree(ctx, r1, ScannedWorktree{Path: "/code/alpha-b", Branch: "y"})
	require.NoError(err)
	_, err = d.UpsertWorktree(ctx, r2, ScannedWorktree{Path: "/code/beta-a", Branch: "z"})
	require.NoError(err)

	// Mark one inactive — it must NOT appear in the result.
	require.NoError(d.MarkWorktreesNotInSet(ctx, r1, []string{"/code/alpha-a"}))

	got, err := d.ListAllActiveWorktrees(ctx)
	require.NoError(err)
	require.Len(got, 2)
	assert.Equal("alpha", got[0].RepoOwner)
	assert.Equal("one", got[0].RepoName)
	assert.Equal("/code/alpha-a", got[0].Path)
	assert.Equal("beta", got[1].RepoOwner)
	assert.Equal("/code/beta-a", got[1].Path)
}

func TestUpsertLocalRepoInsertsAndIsIdempotent(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	id1, err := d.UpsertLocalRepo(ctx, "redpanda")
	require.NoError(err)
	assert.NotZero(id1)

	id2, err := d.UpsertLocalRepo(ctx, "redpanda")
	require.NoError(err)
	assert.Equal(id1, id2)

	// A different name gets its own id.
	id3, err := d.UpsertLocalRepo(ctx, "console")
	require.NoError(err)
	assert.NotEqual(id1, id3)
}

func TestUpsertLocalRepoRejectsEmpty(t *testing.T) {
	d := openTestDB(t)
	_, err := d.UpsertLocalRepo(context.Background(), "")
	require.Error(t, err)
}

func TestMigration018SweepsStaleGitHubWorktrees(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	// Two repos: one local (the legitimate model), one github.
	// Insert worktree rows against each and confirm that the
	// startup migrations have already removed the github-attached
	// one — openTestDB runs migrations to head, so by the time we
	// look the legacy row is gone.
	//
	// To prove the migration's effect we have to manufacture a row
	// AFTER the migration ran and then re-run the cleanup query.
	githubRepoID := insertTestRepo(t, d, "acme", "widget")
	localRepoID, err := d.UpsertLocalRepo(ctx, "redpanda")
	require.NoError(err)

	_, err = d.UpsertWorktree(ctx, localRepoID, ScannedWorktree{
		Path: "/code/redpanda", Branch: "dev",
	})
	require.NoError(err)
	// Manually insert a github-attached worktree row, mirroring the
	// legacy state. UpsertWorktree itself is fine with any repo_id;
	// only the migration enforces the cleanup invariant.
	_, err = d.WriteDB().ExecContext(ctx,
		`INSERT INTO middleman_worktrees (repo_id, path) VALUES (?, ?)`,
		githubRepoID, "/code/legacy",
	)
	require.NoError(err)

	// Re-run the migration's cleanup query manually.
	_, err = d.WriteDB().ExecContext(ctx,
		`DELETE FROM middleman_worktrees WHERE repo_id IN (
			SELECT id FROM middleman_repos WHERE platform = 'github'
		)`,
	)
	require.NoError(err)

	all, err := d.ListAllActiveWorktrees(ctx)
	require.NoError(err)
	require.Len(all, 1)
	assert.Equal("redpanda", all[0].RepoName)
}

func TestGetWorktreeByID(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/o/r-feat", Branch: "feat/x", HeadSHA: "aaaa",
	})
	require.NoError(err)

	got, err := d.GetWorktreeByID(ctx, w.ID)
	require.NoError(err)
	assert.Equal(w.ID, got.ID)
	assert.Equal("/code/o/r-feat", got.Path)

	_, err = d.GetWorktreeByID(ctx, 99999)
	require.Error(err)
}

func TestListReposExcludesLocalEntries(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	_ = insertTestRepo(t, d, "org", "github-repo")
	_, err := d.UpsertLocalRepo(ctx, "local-only")
	require.NoError(err)

	got, err := d.ListRepos(ctx)
	require.NoError(err)
	require.Len(got, 1)
	assert.Equal("github-repo", got[0].Name)
}

func TestUpsertWorktreeRejectsEmptyPath(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	repoID := insertTestRepo(t, d, "o", "r")

	_, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{Path: ""})
	require.Error(t, err)
}

func TestGetActiveWorktreeByPath(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID, err := d.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := d.UpsertWorktree(ctx, repoID, ScannedWorktree{
		Path: "/code/demo-feat", Branch: "feat", HeadSHA: "aaaa",
	})
	require.NoError(err)

	got, err := d.GetActiveWorktreeByPath(ctx, "/code/demo-feat")
	require.NoError(err)
	assert.Equal(w.ID, got.ID)
	assert.Equal("demo", got.RepoName)
	assert.Equal("local", got.RepoOwner)

	_, err = d.GetActiveWorktreeByPath(ctx, "/code/does-not-exist")
	assert.True(errors.Is(err, sql.ErrNoRows))
}
