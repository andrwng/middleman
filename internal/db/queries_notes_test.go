package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPRNotesCRUD(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	// Missing row = empty scratchpad.
	got, err := d.GetPRNotes(ctx, mrID)
	require.NoError(err)
	assert.Equal(mrID, got.MergeRequestID)
	assert.Empty(got.Content)
	assert.True(got.UpdatedAt.IsZero())

	// First write creates the row.
	saved, err := d.UpsertPRNotes(ctx, mrID, "initial note")
	require.NoError(err)
	assert.Equal("initial note", saved.Content)
	assert.False(saved.UpdatedAt.IsZero())

	// Subsequent write replaces content; updated_at bumps.
	first := saved.UpdatedAt
	saved2, err := d.UpsertPRNotes(ctx, mrID, "edited")
	require.NoError(err)
	assert.Equal("edited", saved2.Content)
	assert.False(saved2.UpdatedAt.Before(first))

	// Re-fetch returns the latest content.
	got, err = d.GetPRNotes(ctx, mrID)
	require.NoError(err)
	assert.Equal("edited", got.Content)

	// Clearing is a valid value — we preserve the row so the UI
	// knows the user deliberately emptied it.
	_, err = d.UpsertPRNotes(ctx, mrID, "")
	require.NoError(err)
	got, err = d.GetPRNotes(ctx, mrID)
	require.NoError(err)
	assert.Empty(got.Content)
	assert.False(got.UpdatedAt.IsZero())
}

func TestPRNotesCascadeDelete(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	_, err := d.UpsertPRNotes(ctx, mrID, "some note")
	require.NoError(err)

	_, err = d.rw.ExecContext(ctx, `DELETE FROM middleman_merge_requests WHERE id = ?`, mrID)
	require.NoError(err)

	got, err := d.GetPRNotes(ctx, mrID)
	require.NoError(err)
	require.Empty(got.Content, "notes row should be removed along with MR")
	require.True(got.UpdatedAt.IsZero())
}
