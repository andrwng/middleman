package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorGroupsCRUD(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	// Empty initial state.
	groups, err := d.ListAuthorGroups(ctx)
	require.NoError(err)
	assert.Empty(groups)

	// Create.
	created, err := d.CreateAuthorGroup(ctx, "team-a", []string{"alice", "bob", "carol"})
	require.NoError(err)
	assert.Equal("team-a", created.Name)
	assert.Equal([]string{"alice", "bob", "carol"}, created.Members)

	// List sees the new row.
	groups, err = d.ListAuthorGroups(ctx)
	require.NoError(err)
	require.Len(groups, 1)
	assert.Equal(created.ID, groups[0].ID)

	// Update rename + change membership.
	updated, err := d.UpdateAuthorGroup(ctx, created.ID, "team-alpha", []string{"bob", "dave"})
	require.NoError(err)
	assert.Equal("team-alpha", updated.Name)
	assert.Equal([]string{"bob", "dave"}, updated.Members)

	// Duplicate name fails with the sentinel error.
	_, err = d.CreateAuthorGroup(ctx, "team-alpha", []string{"eve"})
	require.ErrorIs(err, ErrAuthorGroupNameTaken)

	// Delete.
	require.NoError(d.DeleteAuthorGroup(ctx, created.ID))
	_, err = d.GetAuthorGroup(ctx, created.ID)
	require.Error(err)
	assert.ErrorIs(err, sql.ErrNoRows)
}

func TestAuthorGroupsNormalizesMembers(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	// Whitespace, blanks, and case-insensitive dedup.
	g, err := d.CreateAuthorGroup(ctx, "mix",
		[]string{"alice", "  bob ", "", "Alice", "carol"})
	require.NoError(err)
	assert.Equal([]string{"alice", "bob", "carol"}, g.Members)
}
