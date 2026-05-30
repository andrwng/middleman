package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewThreadsMigrationApplied proves migration 000021 ran: the
// tables exist and are queryable through the read handle.
func TestReviewThreadsMigrationApplied(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var threads int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_threads`).Scan(&threads))
	require.Equal(t, 0, threads)

	var comments int
	require.NoError(t, d.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_review_thread_comments`).Scan(&comments))
	require.Equal(t, 0, comments)
}
