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
	"github.com/wesm/middleman/internal/worktrees"
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
	anchorSHA, laterSHA := seedAnchorRepoCommits(t, dir)

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

	// Without `at`, the response shape is unchanged.
	plain, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num, nil)
	require.NoError(err)
	require.Equal(http.StatusOK, plain.StatusCode())
	require.NotNil(plain.JSON200.Threads)
	plainThreads := *plain.JSON200.Threads
	require.Len(plainThreads, 1)
	assert.Nil(plainThreads[0].Resolved, "no `at` means no resolution")
	// Nil is not enough on its own: Go's decoder maps both "key absent"
	// and an explicit "resolved": null to a nil pointer, and only the
	// former honours the byte-identical-without-`at` contract the MCP
	// tools external agents read depend on. Assert on the wire bytes.
	assert.NotContains(string(plain.Body), "resolved",
		"without `at` the response must not carry the key at all")
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

// seedAnchorRepoCommits writes a.txt with TARGET on line 5, commits it,
// then inserts three lines above and commits again. Returns the two SHAs,
// so the anchor recorded at the first maps to line 8 at the second.
func seedAnchorRepoCommits(t *testing.T, dir string) (anchorSHA, laterSHA string) {
	t.Helper()
	write := func(body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644))
	}
	head := func() string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		require.NoError(t, err)
		return string(out[:40])
	}
	write("one\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "add a.txt")
	anchorSHA = head()
	write("new1\nnew2\nnew3\none\ntwo\nthree\nfour\nTARGET\nsix\n")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "insert above")
	return anchorSHA, head()
}

// TestAPIReviewThreadsRejectsOptionLikeAt pins the argument-injection
// defense on `at`. The revision reaches `git diff` as a positional
// argument, and git parses options anywhere on its command line, so an
// `at` of "--output=<path>" would truncate that path with diff text over
// a plain GET (no CSRF token, no auth). It must never reach argv.
func TestAPIReviewThreadsRejectsOptionLikeAt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num, dir := seedReviewWorktreeGit(t, database)
	anchorSHA, _ := seedAnchorRepoCommits(t, dir)

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

	// Files git would clobber if --output were honoured: one absolute,
	// one worktree-relative, since git resolves --output against its own
	// working directory.
	victim := filepath.Join(t.TempDir(), "victim.txt")
	const untouched = "do not overwrite me\n"
	require.NoError(os.WriteFile(victim, []byte(untouched), 0o644))
	relVictim := filepath.Join(dir, "rel-victim.txt")
	require.NoError(os.WriteFile(relVictim, []byte(untouched), 0o644))

	for _, at := range []string{
		"--output=" + victim,
		"--output=rel-victim.txt",
		"-Otouch /tmp/pwned",
		"HEAD^{/x}",
	} {
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
			ctx, "local", "demo", num,
			&generated.GetReposByOwnerByNamePullsByNumberReviewThreadsParams{At: &at})
		require.NoError(err, at)
		assert.Equal(http.StatusBadRequest, resp.StatusCode(), "at=%q must be rejected", at)
	}

	got, err := os.ReadFile(victim)
	require.NoError(err)
	assert.Equal(untouched, string(got), "the absolute --output target was written to")
	gotRel, err := os.ReadFile(relVictim)
	require.NoError(err)
	assert.Equal(untouched, string(gotRel), "the relative --output target was written to")

	// The sentinel and a real sha still work, so the guard is not a
	// blanket rejection of everything.
	for _, at := range []string{anchorSHA, worktrees.WorkingTreeSentinel} {
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
			ctx, "local", "demo", num,
			&generated.GetReposByOwnerByNamePullsByNumberReviewThreadsParams{At: &at})
		require.NoError(err, at)
		assert.Equal(http.StatusOK, resp.StatusCode(), "at=%q must be accepted", at)
	}
}

// TestResolveThreadAnchorsSkipsUntrustworthyAnchors covers the threads
// resolveThreadAnchors deliberately leaves alone, at the level where the
// choice is made rather than through the handler:
//
//   - a stored commit_sha that is not a hex object id (createReviewThreads
//     keeps a non-resolving sha verbatim, so the column is not safe argv);
//   - a LEFT-side anchor, whose pre-image revision the request does not
//     carry and whose forward mapping would land in the wrong numbering
//     space anyway;
//   - a hidden thread, which never renders as a placed card.
//
// A valid RIGHT-side sibling in the same list must still resolve, so a
// skipped thread costs nothing but its own resolution.
func TestResolveThreadAnchorsSkipsUntrustworthyAnchors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	_, dir := seedReviewWorktreeGit(t, database)
	anchorSHA, laterSHA := seedAnchorRepoCommits(t, dir)

	threads := []reviewThreadResponse{
		{ID: 1, Path: "a.txt", Side: "RIGHT", Line: 5, CommitSHA: anchorSHA},
		{ID: 2, Path: "a.txt", Side: "RIGHT", Line: 5, CommitSHA: "--output=/tmp/pwned"},
		{ID: 3, Path: "a.txt", Side: "LEFT", Line: 5, CommitSHA: anchorSHA},
		{ID: 4, Path: "a.txt", Side: "RIGHT", Line: 5, CommitSHA: anchorSHA, Hidden: true},
	}
	srv.resolveThreadAnchors(context.Background(), dir, laterSHA, threads)

	require.NotNil(threads[0].Resolved, "a valid RIGHT anchor still resolves")
	assert.Equal("moved", threads[0].Resolved.State)
	assert.Equal(8, threads[0].Resolved.Line)
	assert.Nil(threads[1].Resolved, "a non-hex commit_sha is never handed to git")
	assert.Nil(threads[2].Resolved, "LEFT anchors are not forward-mapped")
	assert.Nil(threads[3].Resolved, "hidden threads are not resolved")
}
