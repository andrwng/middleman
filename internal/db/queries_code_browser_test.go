package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBrowserState_NoRowDefault(t *testing.T) {
	database := openTestDB(t)
	mrID := seedAIQuestionTestMR(t, database)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, mrID, got.MergeRequestID)
	assert.Equal(t, "", got.Path)
}

func TestCodeBrowserState_SetThenGet(t *testing.T) {
	database := openTestDB(t)
	mrID := seedAIQuestionTestMR(t, database)

	set, err := database.SetCodeBrowserState(context.Background(), mrID, "src/kafka/server.cc")
	require.NoError(t, err)
	assert.Equal(t, "src/kafka/server.cc", set.Path)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, "src/kafka/server.cc", got.Path)
}

func TestCodeBrowserState_SetOverwrites(t *testing.T) {
	database := openTestDB(t)
	mrID := seedAIQuestionTestMR(t, database)

	_, err := database.SetCodeBrowserState(context.Background(), mrID, "a.txt")
	require.NoError(t, err)
	_, err = database.SetCodeBrowserState(context.Background(), mrID, "b.txt")
	require.NoError(t, err)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, "b.txt", got.Path)
}
