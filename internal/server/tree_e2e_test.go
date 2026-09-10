package server

import (
	"context"
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
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/gitclone"
)

// TestAPITree_RootAndSubdir covers both root and subdirectory
// listings. setupTestServerWithClones' shared fixture is flat
// (base.txt, file1.txt..file5.txt at the repo root), so this test
// builds its own small repo — mirroring TestAPIResolveFiles_Basics'
// pattern — with a nested directory to exercise a genuine
// subdirectory listing rather than only the root.
func TestAPITree_RootAndSubdir(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	bareDir := filepath.Join(dir, "clones")
	require.NoError(os.MkdirAll(bareDir, 0o755))
	bare := filepath.Join(bareDir, "github.com", "acme", "widget.git")

	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, work)
	runGit(t, work, "config", "user.email", "test@test.com")
	runGit(t, work, "config", "user.name", "Test")

	mkFile := func(rel, body string) {
		full := filepath.Join(work, rel)
		require.NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(os.WriteFile(full, []byte(body), 0o644))
	}
	mkFile("root.txt", "at the root\n")
	mkFile("docs/readme.md", "# nested doc\n")

	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "seed")
	runGit(t, work, "push", "origin", "main")
	headSHA := testGitSHA(t, work, "HEAD")

	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widget", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: clones})
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	// Root listing: expect the file and the nested directory.
	root, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Path: nil})
	require.NoError(err)
	require.Equal(http.StatusOK, root.StatusCode())
	require.NotNil(root.JSON200)
	require.NotNil(root.JSON200.Entries)
	rootEntries := *root.JSON200.Entries
	require.NotEmpty(rootEntries)
	assert.Contains(rootEntries, generated.TreeEntryJSON{Name: "root.txt", Path: "root.txt", Type: "file"})
	assert.Contains(rootEntries, generated.TreeEntryJSON{Name: "docs", Path: "docs", Type: "dir"})

	// Subdirectory listing: expect the nested file, with a
	// path prefixed by the directory.
	docsPath := "docs"
	sub, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Path: &docsPath})
	require.NoError(err)
	require.Equal(http.StatusOK, sub.StatusCode())
	require.NotNil(sub.JSON200)
	assert.Equal("docs", sub.JSON200.Path)
	require.NotNil(sub.JSON200.Entries)
	subEntries := *sub.JSON200.Entries
	require.Len(subEntries, 1)
	assert.Equal(generated.TreeEntryJSON{Name: "readme.md", Path: "docs/readme.md", Type: "file"}, subEntries[0])
}

// TestAPITree_Recursive reuses TestAPITree_RootAndSubdir's nested-directory
// fixture setup to prove ?recursive=true flattens the whole tree to file
// paths, ignoring Path, rather than listing one directory's immediate
// entries.
func TestAPITree_Recursive(t *testing.T) {
	require := require.New(t)
	assert := Assert.New(t)

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(err)
	t.Cleanup(func() { database.Close() })

	bareDir := filepath.Join(dir, "clones")
	require.NoError(os.MkdirAll(bareDir, 0o755))
	bare := filepath.Join(bareDir, "github.com", "acme", "widget.git")

	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, work)
	runGit(t, work, "config", "user.email", "test@test.com")
	runGit(t, work, "config", "user.name", "Test")

	mkFile := func(rel, body string) {
		full := filepath.Join(work, rel)
		require.NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(os.WriteFile(full, []byte(body), 0o644))
	}
	mkFile("root.txt", "at the root\n")
	mkFile("docs/readme.md", "# nested doc\n")

	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "seed")
	runGit(t, work, "push", "origin", "main")
	headSHA := testGitSHA(t, work, "HEAD")

	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widget", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: clones})
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	recursive := true
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Recursive: &recursive})
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	assert.Equal("", resp.JSON200.Path, "recursive listing ignores Path")
	require.NotNil(resp.JSON200.Entries)
	entries := *resp.JSON200.Entries
	assert.Contains(entries, generated.TreeEntryJSON{Name: "root.txt", Path: "root.txt", Type: "file"})
	assert.Contains(entries, generated.TreeEntryJSON{Name: "readme.md", Path: "docs/readme.md", Type: "file"})
	for _, e := range entries {
		assert.Equal("file", e.Type, "recursive listing must never include a dir entry")
	}
}

func TestAPITree_NonexistentPath(t *testing.T) {
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badPath := "does/not/exist"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &mergeBase, Path: &badPath})
	require.NoError(t, err)
	Assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

func TestAPITree_NonexistentSHA(t *testing.T) {
	client, _, _, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badSHA := "0000000000000000000000000000000000000000"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &badSHA, Path: nil})
	require.NoError(t, err)
	Assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

// TestAPILocalDispatchTreeServesWorktreeFiles mirrors
// TestAPILocalDispatchBlobServesWorktreeFiles's fixture shape and
// covers getTreeLocal's own validation/error-mapping logic, which
// has no coverage from the PR-mode tests above: SHA regex rejection
// (400), a missing directory (404 via worktrees.ErrNotFound), an
// unknown worktree (404 via resolveLocalWorktree), and the
// WORKING-TREE sentinel reading straight from disk.
func TestAPILocalDispatchTreeServesWorktreeFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")

	require.NoError(os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(os.WriteFile(filepath.Join(dir, "doc.md"), []byte("committed\n"), 0o644))
	require.NoError(os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested\n"), 0o644))
	runGitWT(t, dir, "add", "doc.md", "sub/nested.txt")
	runGitWT(t, dir, "commit", "-m", "add doc and nested file")

	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := string(headOut[:len(headOut)-1]) // strip trailing newline

	// A new, uncommitted file — only visible via the WORKING-TREE
	// sentinel's on-disk read.
	require.NoError(os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("wip\n"), 0o644))

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:   canonDir,
		Branch: "main",
	})
	require.NoError(err)

	// 1. Root listing at HEAD SHA: doc.md and sub/, no untracked.txt.
	rootResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA})
	require.NoError(err)
	require.Equal(http.StatusOK, rootResp.StatusCode())
	require.NotNil(rootResp.JSON200)
	require.NotNil(rootResp.JSON200.Entries)
	rootEntries := *rootResp.JSON200.Entries
	assert.Contains(rootEntries, generated.TreeEntryJSON{Name: "doc.md", Path: "doc.md", Type: "file"})
	assert.Contains(rootEntries, generated.TreeEntryJSON{Name: "sub", Path: "sub", Type: "dir"})
	for _, e := range rootEntries {
		assert.NotEqual("untracked.txt", e.Name, "HEAD listing should not see uncommitted files")
	}

	// 2. Subdirectory listing at HEAD SHA.
	subPath := "sub"
	subResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Path: &subPath})
	require.NoError(err)
	require.Equal(http.StatusOK, subResp.StatusCode())
	require.NotNil(subResp.JSON200)
	require.NotNil(subResp.JSON200.Entries)
	require.Len(*subResp.JSON200.Entries, 1)
	assert.Equal(generated.TreeEntryJSON{Name: "nested.txt", Path: "sub/nested.txt", Type: "file"}, (*subResp.JSON200.Entries)[0])

	// 3. WORKING-TREE sentinel: sees the uncommitted file too.
	wtSentinel := "WORKING-TREE"
	wtResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &wtSentinel})
	require.NoError(err)
	require.Equal(http.StatusOK, wtResp.StatusCode())
	require.NotNil(wtResp.JSON200)
	require.NotNil(wtResp.JSON200.Entries)
	assert.Contains(*wtResp.JSON200.Entries, generated.TreeEntryJSON{Name: "untracked.txt", Path: "untracked.txt", Type: "file"})

	// 4. Recursive listing at HEAD SHA: every file flattened, including
	// the nested one, still no untracked.txt (HEAD, not working tree).
	recursive := true
	recResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Recursive: &recursive})
	require.NoError(err)
	require.Equal(http.StatusOK, recResp.StatusCode())
	require.NotNil(recResp.JSON200)
	require.NotNil(recResp.JSON200.Entries)
	recEntries := *recResp.JSON200.Entries
	assert.Contains(recEntries, generated.TreeEntryJSON{Name: "doc.md", Path: "doc.md", Type: "file"})
	assert.Contains(recEntries, generated.TreeEntryJSON{Name: "nested.txt", Path: "sub/nested.txt", Type: "file"})
	for _, e := range recEntries {
		assert.Equal("file", e.Type, "recursive listing must never include a dir entry")
		assert.NotEqual("untracked.txt", e.Name, "HEAD recursive listing should not see uncommitted files")
	}

	// 4a. Missing path: 404 (not 502).
	missingPath := "does-not-exist"
	missResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA, Path: &missingPath})
	require.NoError(err)
	assert.Equal(http.StatusNotFound, missResp.StatusCode())

	// 5. Malformed sha (option-shaped): rejected before it reaches
	// `git ls-tree` (worktrees.Tree) — same guard shape as getBlobLocal's.
	malformedSHA := "-Otouch /tmp/should-not-be-created"
	badResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &malformedSHA})
	require.NoError(err)
	assert.Equal(http.StatusBadRequest, badResp.StatusCode())

	// 6. Unknown worktree number: 404 via resolveLocalWorktree.
	unknownResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "local", "demo", w.ID+999,
		&generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: &headSHA})
	require.NoError(err)
	assert.Equal(http.StatusNotFound, unknownResp.StatusCode())
}
