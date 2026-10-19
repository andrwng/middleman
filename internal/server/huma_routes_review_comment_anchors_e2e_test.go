package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	ghclient "github.com/wesm/middleman/internal/github"
)

type commentAnchorFixture struct {
	srv      *Server
	database *db.DB
	prID     int64
	c1, c2   string
}

// newCommentAnchorFixture serves acme/widget#1 with diff head c2; c1 is the
// push before it, which inserted a line at the top of a.txt. withClone
// false leaves the repo's clone off disk, as before its first fetch.
func newCommentAnchorFixture(t *testing.T, withClone bool) commentAnchorFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	clonesDir := filepath.Join(dir, "clones")
	bare := filepath.Join(dir, "elsewhere.git")
	if withClone {
		bare = filepath.Join(clonesDir, "github.com", "acme", "widget.git")
	}
	require.NoError(os.MkdirAll(filepath.Dir(bare), 0o755))
	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, work)
	runGit(t, work, "config", "user.email", "test@test.com")
	runGit(t, work, "config", "user.name", "Test")
	commit := func(body string) string {
		require.NoError(os.WriteFile(filepath.Join(work, "a.txt"), []byte(body), 0o644))
		runGit(t, work, "add", "a.txt")
		runGit(t, work, "commit", "-m", "update")
		runGit(t, work, "push", "origin", "main")
		return testGitSHA(t, work, "HEAD")
	}
	c1 := commit("one\ntwo\nTARGET\nfour\n")
	c2 := commit("zero\none\ntwo\nTARGET\nfour\n")

	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widget", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": &mockGH{}}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: gitclone.New(clonesDir, nil)})

	prID := seedPRWithHeadSHA(t, database, "acme", "widget", 1, c2)
	repo, err := database.GetRepoByOwnerName(context.Background(), "acme", "widget")
	require.NoError(err)
	require.NoError(database.UpdateDiffSHAs(context.Background(), repo.ID, 1, c2, c1, c1))
	return commentAnchorFixture{srv: srv, database: database, prID: prID, c1: c1, c2: c2}
}

func (f commentAnchorFixture) addComment(t *testing.T, ghID int64, meta map[string]any) {
	t.Helper()
	raw, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, f.database.UpsertMREvents(context.Background(), []db.MREvent{{
		MergeRequestID: f.prID,
		PlatformID:     &ghID,
		EventType:      "review_comment",
		Author:         "alex",
		Body:           "about TARGET",
		Summary:        "a.txt",
		MetadataJSON:   string(raw),
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		DedupeKey:      fmt.Sprintf("review-comment-%d", ghID),
	}}))
}

func (f commentAnchorFixture) anchors(
	t *testing.T, at *string,
) (*generated.GetReposByOwnerByNamePullsByNumberReviewCommentAnchorsResponse, map[int64]generated.ReviewCommentAnchorResponse) {
	t.Helper()
	client := setupTestClient(t, f.srv)
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewCommentAnchorsWithResponse(
		context.Background(), "acme", "widget", 1,
		&generated.GetReposByOwnerByNamePullsByNumberReviewCommentAnchorsParams{At: at})
	require.NoError(t, err)
	byID := map[int64]generated.ReviewCommentAnchorResponse{}
	if resp.JSON200 != nil && resp.JSON200.Anchors != nil {
		for _, a := range *resp.JSON200.Anchors {
			byID[a.CommentId] = a
		}
	}
	return resp, byID
}

func (f commentAnchorFixture) addTypicalComments(t *testing.T) {
	// Synced while still live at c1, before the push. Rows are written once,
	// so this is the shape almost every stored comment has after a push.
	f.addComment(t, 1, map[string]any{"path": "a.txt", "side": "RIGHT", "line": 3, "start_line": 2, "commit_id": f.c1})
	// Current fields disagree with the creation anchor; the creation anchor wins.
	f.addComment(t, 2, map[string]any{
		"path": "a.txt", "side": "RIGHT", "line": 3, "commit_id": f.c2,
		"original_line": 3, "original_commit_id": f.c1,
	})
	// Made on the head itself.
	f.addComment(t, 3, map[string]any{"path": "a.txt", "side": "RIGHT", "line": 4, "commit_id": f.c2})
	// Base-side numbering is not commit_id's tree.
	f.addComment(t, 4, map[string]any{"path": "a.txt", "side": "LEFT", "line": 3, "commit_id": f.c1})
	// No line to map.
	f.addComment(t, 5, map[string]any{"path": "a.txt", "subject_type": "file", "commit_id": f.c1})
}

func TestAPIReviewCommentAnchorsDefaultToDiffHead(t *testing.T) {
	assert := Assert.New(t)
	f := newCommentAnchorFixture(t, true)
	f.addTypicalComments(t)

	resp, got := f.anchors(t, nil)

	require.Equal(t, http.StatusOK, resp.StatusCode())
	assert.Equal(f.c2, resp.JSON200.At)
	require.Contains(t, got, int64(1))
	assert.Equal("moved", got[1].State)
	assert.Equal("a.txt", *got[1].Path)
	assert.EqualValues(4, *got[1].Line)
	assert.EqualValues(3, *got[1].StartLine)
	require.Contains(t, got, int64(2))
	assert.Equal("moved", got[2].State)
	assert.EqualValues(4, *got[2].Line)
	require.Contains(t, got, int64(3))
	assert.Equal("current", got[3].State)
	assert.EqualValues(4, *got[3].Line)
	assert.NotContains(got, int64(4))
	assert.NotContains(got, int64(5))
}

func TestAPIReviewCommentAnchorsFollowTheRequestedRevision(t *testing.T) {
	assert := Assert.New(t)
	f := newCommentAnchorFixture(t, true)
	f.addTypicalComments(t)

	// Reader is looking at the c1 commit, where the head comment's line sits
	// one higher.
	resp, got := f.anchors(t, &f.c1)

	require.Equal(t, http.StatusOK, resp.StatusCode())
	assert.Equal(f.c1, resp.JSON200.At)
	assert.Equal("current", got[1].State)
	assert.EqualValues(3, *got[1].Line)
	assert.Equal("moved", got[3].State)
	assert.EqualValues(3, *got[3].Line)
}

func TestAPIReviewCommentAnchorsWithoutCloneHaveNoOpinion(t *testing.T) {
	f := newCommentAnchorFixture(t, false)
	f.addTypicalComments(t)

	resp, got := f.anchors(t, nil)

	require.Equal(t, http.StatusOK, resp.StatusCode())
	Assert.Empty(t, got, "with no clone every comment would read unmappable and drop out of the diff")
}

func TestAPIReviewCommentAnchorsForAnUnknownRevisionHaveNoOpinion(t *testing.T) {
	f := newCommentAnchorFixture(t, true)
	f.addTypicalComments(t)
	unknown := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	resp, got := f.anchors(t, &unknown)

	require.Equal(t, http.StatusOK, resp.StatusCode())
	Assert.Empty(t, got, "a revision the clone lacks would otherwise mark every comment unmappable")
}

func TestAPIReviewCommentAnchorsRejectBadRequests(t *testing.T) {
	f := newCommentAnchorFixture(t, true)
	client := setupTestClient(t, f.srv)
	ctx := context.Background()
	optionShaped := "--output=/tmp/x"
	tests := []struct {
		name   string
		owner  string
		number int64
		at     *string
		status int
	}{
		{name: "option-shaped at", owner: "acme", number: 1, at: &optionShaped, status: http.StatusBadRequest},
		{name: "local worktree", owner: "local", number: 1, status: http.StatusBadRequest},
		{name: "unknown pull request", owner: "acme", number: 99, status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberReviewCommentAnchorsWithResponse(
				ctx, tt.owner, "widget", tt.number,
				&generated.GetReposByOwnerByNamePullsByNumberReviewCommentAnchorsParams{At: tt.at})
			require.NoError(t, err)
			Assert.Equal(t, tt.status, resp.StatusCode())
		})
	}
}
