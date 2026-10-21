package testutil

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func TestSetupLargeDiffRepoSummaryCounts(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	_, err = SeedFixtures(ctx, database)
	require.NoError(err)
	small, err := SetupDiffRepo(ctx, dir, database)
	require.NoError(err)
	large, err := SetupLargeDiffRepo(ctx, dir, database, small.BaseSHA)
	require.NoError(err)

	repoID, err := database.UpsertRepo(ctx, "github.com", "acme", "widgets")
	require.NoError(err)
	pr, err := database.GetMergeRequestByRepoIDAndNumber(ctx, repoID, LargeDiffPRNumber)
	require.NoError(err)
	require.NotNil(pr)
	assert.Equal(large.HeadSHA, pr.DiffHeadSHA)
	assert.Equal(large.BaseSHA, pr.MergeBaseSHA)

	summary, err := small.Manager.DiffSummary(
		ctx, "github.com", "acme", "widgets", large.BaseSHA, large.HeadSHA, false)
	require.NoError(err)

	wantFiles, wantAdd, wantDel := LargeDiffTotals()
	require.Len(summary.Files, wantFiles)

	byPath := make(map[string]int, len(summary.Files))
	add, del := 0, 0
	for i, f := range summary.Files {
		byPath[f.Path] = i
		add += f.Additions
		del += f.Deletions
	}
	assert.Equal(wantAdd, add)
	assert.Equal(wantDel, del)

	cases := []struct {
		path      string
		status    string
		additions int
		deletions int
	}{
		{LargeDiffSrcPath(0), "added", LargeDiffSrcLines, 0},
		{LargeDiffSrcPath(LargeDiffSrcFiles - 1), "added", LargeDiffSrcLines, 0},
		{LargeDiffGenPath(0), "added", LargeDiffGenLines, 0},
		{LargeDiffGenPath(LargeDiffGenFiles - 1), "added", LargeDiffGenLines, 0},
		{LargeDiffDeletedPath, "deleted", 0, LargeDiffDeletedLines},
		{LargeDiffModifiedPath, "modified", LargeDiffModifiedChanged, LargeDiffModifiedChanged},
	}
	for _, tc := range cases {
		i, ok := byPath[tc.path]
		if !assert.True(ok, "missing %s", tc.path) {
			continue
		}
		f := summary.Files[i]
		assert.Equal(tc.status, f.Status, tc.path)
		assert.Equal(tc.additions, f.Additions, tc.path)
		assert.Equal(tc.deletions, f.Deletions, tc.path)
	}

	// No generated file is mistaken for a rename or copy of another.
	for _, f := range summary.Files {
		assert.NotContains([]string{"renamed", "copied"}, f.Status, f.Path)
	}
	gen := 0
	for _, f := range summary.Files {
		if strings.HasPrefix(f.Path, "gen/") {
			gen++
		}
	}
	assert.Equal(LargeDiffGenFiles, gen)
}
