package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
)

// TestAPIReviewThreadsResolveAt covers the `at` contract: absent, threads
// come back exactly as before; supplied, each carries where its anchor
// landed at that revision.
func TestAPIReviewThreadsResolveAt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)

	// A file with a known line, committed, then three lines inserted above.
	write := func(body string) {
		require.NoError(os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644))
	}
	head := func() string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		require.NoError(err)
		return string(out[:40])
	}
	write("one\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "add a.txt")
	anchorSHA := head()

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{
				Path: "a.txt", Side: "RIGHT", Line: 5,
				CommitSha: anchorSHA, Body: "this leaks",
			}},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())

	write("new1\nnew2\nnew3\none\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "insert above")
	laterSHA := head()

	// Without `at`, the response shape is unchanged.
	plain, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num, nil)
	require.NoError(err)
	require.Equal(http.StatusOK, plain.StatusCode())
	require.NotNil(plain.JSON200.Threads)
	plainThreads := *plain.JSON200.Threads
	require.Len(plainThreads, 1)
	assert.Nil(plainThreads[0].Resolved, "no `at` means no resolution")
	assert.Equal(int64(5), plainThreads[0].Line, "the recorded anchor is never rewritten")

	// With `at`, the anchor is reported three lines down.
	at := laterSHA
	resolved, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		&generated.GetReposByOwnerByNamePullsByNumberReviewThreadsParams{At: &at})
	require.NoError(err)
	require.Equal(http.StatusOK, resolved.StatusCode())
	got := (*resolved.JSON200.Threads)[0]
	require.NotNil(got.Resolved)
	assert.Equal("moved", got.Resolved.State)
	assert.Equal(int64(8), *got.Resolved.Line)
	assert.Equal("a.txt", *got.Resolved.Path)
	assert.Equal(int64(5), got.Line, "the recorded anchor is still 5")
}
