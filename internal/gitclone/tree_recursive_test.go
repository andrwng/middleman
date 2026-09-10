package gitclone

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeRecursive_ListsEveryFileFlattened(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	mgr, host, owner, name, sha := setupDiffRepoForTree(t)

	entries, err := mgr.TreeRecursive(context.Background(), host, owner, name, sha)
	require.NoError(err)
	require.NotEmpty(entries)

	var foundNested bool
	for _, e := range entries {
		assert.Equal("file", e.Type, "TreeRecursive must never return a dir entry")
		if e.Path == "internal/handler.go" {
			foundNested = true
			assert.Equal("handler.go", e.Name)
		}
	}
	assert.True(foundNested, "should find internal/handler.go by its full path")
}

func TestTreeRecursive_NonexistentSHA(t *testing.T) {
	mgr, host, owner, name, _ := setupDiffRepoForTree(t)

	_, err := mgr.TreeRecursive(context.Background(), host, owner, name, "0000000000000000000000000000000000000000")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}
