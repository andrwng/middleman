package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffCollapseGlobs_EmptyByDefault(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)

	got, err := database.GetDiffCollapseGlobs(ctx, repoID)
	require.NoError(err)
	assert.Equal(t, []string{}, got)
}

func TestDiffCollapseGlobs_RoundTripPreservesOrder(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)

	globs := []string{"proto/gen/**", "*.generated.go", "vendor/**"}
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, globs))

	got, err := database.GetDiffCollapseGlobs(ctx, repoID)
	require.NoError(err)
	assert.Equal(t, globs, got)
}

func TestDiffCollapseGlobs_SetReplacesExistingList(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)

	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, []string{"a/**", "b/**"}))
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, []string{"c/**"}))

	got, err := database.GetDiffCollapseGlobs(ctx, repoID)
	require.NoError(err)
	assert.Equal(t, []string{"c/**"}, got)
}

func TestDiffCollapseGlobs_SetEmptyClearsList(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)

	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, []string{"a/**"}))
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, []string{}))

	got, err := database.GetDiffCollapseGlobs(ctx, repoID)
	require.NoError(err)
	assert.Equal(t, []string{}, got)
}

func TestDiffCollapseGlobs_CascadeDeleteOnRepoDelete(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoID, []string{"a/**"}))

	_, err = database.WriteDB().ExecContext(ctx, `DELETE FROM middleman_repos WHERE id = ?`, repoID)
	require.NoError(err)

	var count int
	err = database.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_repo_diff_collapse_globs WHERE repo_id = ?`, repoID,
	).Scan(&count)
	require.NoError(err)
	assert.Equal(t, 0, count)
}

func TestDiffCollapseGlobs_IsolatedPerRepo(t *testing.T) {
	require := require.New(t)
	database := openTestDB(t)
	ctx := context.Background()
	repoA, err := database.UpsertLocalRepo(ctx, "repo-a")
	require.NoError(err)
	repoB, err := database.UpsertLocalRepo(ctx, "repo-b")
	require.NoError(err)

	require.NoError(database.SetDiffCollapseGlobs(ctx, repoA, []string{"a/**"}))
	require.NoError(database.SetDiffCollapseGlobs(ctx, repoB, []string{"b/**"}))

	gotA, err := database.GetDiffCollapseGlobs(ctx, repoA)
	require.NoError(err)
	assert.Equal(t, []string{"a/**"}, gotA)

	gotB, err := database.GetDiffCollapseGlobs(ctx, repoB)
	require.NoError(err)
	assert.Equal(t, []string{"b/**"}, gotB)
}
