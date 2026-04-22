package gitclone

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeatmap(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	bare, mergeBase, headSHA := setupCommitTestRepo(t)
	mgr := New(filepath.Dir(bare), nil)

	data, err := mgr.Heatmap(context.Background(), "", "", "remote", mergeBase, headSHA)
	require.NoError(err)

	// The test repo creates 5 PR commits, each adding one file.
	// Heatmap columns = commits (oldest first).
	assert.Len(data.Commits, 5)
	assert.Equal("commit 1", data.Commits[0].Title)
	assert.Equal("commit 5", data.Commits[4].Title)

	// 5 cells, each adding one line to a unique file.
	require.Len(data.Cells, 5)
	paths := make(map[string]struct{})
	for _, c := range data.Cells {
		paths[c.Path] = struct{}{}
		assert.Equal(1, c.Additions)
		assert.Equal(0, c.Deletions)
		assert.False(c.Binary)
	}
	assert.Len(paths, 5, "each cell should reference a distinct file")
}

func TestHeatmap_EmptyRange(t *testing.T) {
	bare, mergeBase, _ := setupCommitTestRepo(t)
	mgr := New(filepath.Dir(bare), nil)

	data, err := mgr.Heatmap(context.Background(), "", "", "remote", mergeBase, mergeBase)
	require.NoError(t, err)
	assert.Empty(t, data.Commits)
	assert.Empty(t, data.Cells)
}
