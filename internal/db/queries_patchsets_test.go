package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordPatchsetAndList(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	mrID := insertTestMR(t, d, repoID, 1, "pr1", baseTime())

	// First push becomes PS1.
	id1, created, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{
		HeadSHA:      "aaaaaaa",
		BaseSHA:      "bbbbbbb",
		MergeBaseSHA: "ccccccc",
	})
	require.NoError(err)
	assert.True(created)
	assert.NotZero(id1)

	// Re-observing the same head SHA is a no-op (bool false).
	id1b, created, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{
		HeadSHA: "aaaaaaa",
	})
	require.NoError(err)
	assert.False(created)
	assert.Equal(id1, id1b)

	// New head SHA after a rebase becomes PS2.
	id2, created, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{
		HeadSHA:      "ddddddd",
		BaseSHA:      "eeeeeee",
		MergeBaseSHA: "fffffff",
		ObservedAt:   baseTime().Add(time.Hour),
	})
	require.NoError(err)
	assert.True(created)
	assert.NotEqual(id1, id2)

	list, err := d.ListPatchsets(ctx, mrID)
	require.NoError(err)
	require.Len(list, 2)
	assert.Equal(1, list[0].Number)
	assert.Equal("aaaaaaa", list[0].HeadSHA)
	assert.Equal("bbbbbbb", list[0].BaseSHA)
	assert.Equal("ccccccc", list[0].MergeBaseSHA)
	assert.Equal(2, list[1].Number)
	assert.Equal("ddddddd", list[1].HeadSHA)
}

func TestRecordPatchsetRefreshesBases(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	repoID := insertTestRepo(t, d, "o", "r")
	mrID := insertTestMR(t, d, repoID, 1, "pr1", baseTime())

	// Record with empty bases first (merge-base may not be
	// computable on the first sync before the clone catches up).
	id, _, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{HeadSHA: "aaaa"})
	require.NoError(err)

	// Second call fills in the bases without creating a new row.
	id2, created, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{
		HeadSHA:      "aaaa",
		BaseSHA:      "bbbb",
		MergeBaseSHA: "cccc",
	})
	require.NoError(err)
	assert.False(created)
	assert.Equal(id, id2)

	list, err := d.ListPatchsets(ctx, mrID)
	require.NoError(err)
	require.Len(list, 1)
	assert.Equal("bbbb", list[0].BaseSHA)
	assert.Equal("cccc", list[0].MergeBaseSHA)
}

func TestRecordPatchsetRequiresHeadSHA(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	repoID := insertTestRepo(t, d, "o", "r")
	mrID := insertTestMR(t, d, repoID, 1, "pr1", baseTime())
	_, _, err := d.RecordPatchset(ctx, mrID, RecordPatchsetOpts{})
	require.Error(t, err)
}
