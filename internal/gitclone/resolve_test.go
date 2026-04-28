package gitclone

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveScenario stands up a bare repo with a synthetic tree that
// exercises every interesting case for ResolveFilenames:
//   - a unique basename in a subdirectory
//   - a basename that appears twice (ambiguous)
//   - a multi-segment path that exists
//   - a multi-segment path that doesn't exist
func resolveScenario(t *testing.T) (mgr *Manager, head string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	commitTestRun(t, root, "git", "init", "--bare", "--initial-branch=main", bare)
	commitTestRun(t, root, "git", "clone", bare, work)
	commitTestRun(t, work, "git", "config", "user.email", "alice@test.com")
	commitTestRun(t, work, "git", "config", "user.name", "Alice")

	mkdirs := []string{
		filepath.Join(work, "internal", "server"),
		filepath.Join(work, "internal", "db"),
		filepath.Join(work, "pkg", "shared"),
	}
	for _, d := range mkdirs {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}

	files := map[string]string{
		"README.md":                                  "# proj\n",
		"internal/server/huma_routes.go":             "package server\n",
		"internal/server/api_types.go":               "package server\n",
		"internal/db/queries.go":                     "package db\n",
		// types.go is intentionally duplicated under two dirs to
		// exercise the ambiguous-basename branch.
		"internal/server/types.go":                   "package server\n",
		"internal/db/types.go":                       "package db\n",
		"pkg/shared/util.go":                         "package shared\n",
	}
	for path, body := range files {
		full := filepath.Join(work, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	commitTestRun(t, work, "git", "add", ".")
	commitTestRun(t, work, "git", "commit", "-m", "seed")
	commitTestRun(t, work, "git", "push", "origin", "main")
	head = gitSHA(t, work, "HEAD")
	mgr = New(root, nil)
	// The Manager expects clones under baseDir/host/owner/name.git;
	// our scenario's bare lives at root/remote.git (host="" owner=""
	// name="remote") to match the convention used by initScenario in
	// the interdiff tests.
	return mgr, head
}

func TestResolveFilenames_UniqueBasenameLinks(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	res, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		[]string{"huma_routes.go", "queries.go", "util.go"},
	)
	require.NoError(err)
	assert.Equal("internal/server/huma_routes.go", res["huma_routes.go"])
	assert.Equal("internal/db/queries.go", res["queries.go"])
	assert.Equal("pkg/shared/util.go", res["util.go"])
}

func TestResolveFilenames_AmbiguousBasenameSkipped(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	res, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		[]string{"types.go"},
	)
	require.NoError(err)
	// types.go appears in two directories — caller should treat the
	// missing entry as "leave as plain text."
	_, ok := res["types.go"]
	assert.False(ok, "ambiguous basename should not be resolved")
}

func TestResolveFilenames_MultiSegmentVerified(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	res, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		[]string{
			"internal/server/types.go",     // exists
			"internal/server/missing.go",   // doesn't exist
			"./internal/db/queries.go",      // existing path with leading ./
		},
	)
	require.NoError(err)
	assert.Equal("internal/server/types.go", res["internal/server/types.go"])
	_, ok := res["internal/server/missing.go"]
	assert.False(ok)
	assert.Equal("internal/db/queries.go", res["./internal/db/queries.go"])
}

func TestResolveFilenames_UnknownBasenameSkipped(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	res, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		[]string{"this_does_not_exist.go"},
	)
	require.NoError(err)
	_, ok := res["this_does_not_exist.go"]
	assert.False(ok)
}

func TestResolveFilenames_EmptyInputs(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	res, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		nil,
	)
	require.NoError(err)
	assert.Empty(res)

	_, err = mgr.ResolveFilenames(
		context.Background(), "", "", "remote", "",
		[]string{"x.go"},
	)
	require.Error(err)
}

func TestResolveFilenames_CachesAcrossCalls(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	mgr, head := resolveScenario(t)

	// First call populates the cache; second should hit the cached
	// listing rather than spawning a second ls-tree. We can't observe
	// the spawn directly without instrumentation, so just verify the
	// resolveCache holds the SHA after a call.
	_, err := mgr.ResolveFilenames(
		context.Background(), "", "", "remote", head,
		[]string{"queries.go"},
	)
	require.NoError(err)

	key := treeCacheKey{host: "", owner: "", name: "remote", sha: head}
	assert.NotNil(mgr.resolveCache.get(key))
}
