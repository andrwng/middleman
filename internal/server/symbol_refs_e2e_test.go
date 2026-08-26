package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/apiclient"
	"github.com/wesm/middleman/internal/apiclient/generated"
	"github.com/wesm/middleman/internal/ctags"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	ghclient "github.com/wesm/middleman/internal/github"
	"github.com/wesm/middleman/internal/worktrees"
)

// TestValidateSymbolQuery covers the request-validation helper in
// isolation: bounds, forbidden bytes, and whitespace trimming.
func TestValidateSymbolQuery(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantStatus int // 0 means no error is expected
		wantSymbol string
	}{
		{name: "empty string", in: "", wantStatus: http.StatusBadRequest},
		{name: "whitespace only", in: "   \t  ", wantStatus: http.StatusBadRequest},
		{name: "129 bytes is too long", in: strings.Repeat("a", 129), wantStatus: http.StatusBadRequest},
		{name: "128 bytes is ok", in: strings.Repeat("a", 128), wantSymbol: strings.Repeat("a", 128)},
		{name: "newline is rejected", in: "foo\nbar", wantStatus: http.StatusBadRequest},
		{name: "carriage return is rejected", in: "foo\rbar", wantStatus: http.StatusBadRequest},
		{name: "NUL byte is rejected", in: "foo\x00bar", wantStatus: http.StatusBadRequest},
		{name: "surrounding whitespace is trimmed", in: "  Foo  ", wantSymbol: "Foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert := Assert.New(t)
			require := require.New(t)

			got, err := validateSymbolQuery(tc.in)

			if tc.wantStatus != 0 {
				require.Error(err)
				var statusErr huma.StatusError
				require.ErrorAs(err, &statusErr)
				assert.Equal(tc.wantStatus, statusErr.GetStatus())
				assert.Empty(got)
				return
			}
			require.NoError(err)
			assert.Equal(tc.wantSymbol, got)
		})
	}
}

// TestBuildSymbolRefsResponse covers the pure response-assembly
// helper: partitioning against the changed-file set, classification
// of the hits that are kept, ordering, truncation math, and the
// never-null Hits invariant the TS client depends on.
func TestBuildSymbolRefsResponse(t *testing.T) {
	t.Run("in-PR hits are classified and returned; others only bump OutsidePRTotal", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		hits := []gitclone.SymbolHit{
			{Path: "changed.go", Line: 4, Text: "func Zap() {}"},
			{Path: "changed.go", Line: 10, Text: "\tv := Zap(1)"},
			{Path: "unchanged.go", Line: 1, Text: "var Zap = 0"},
		}
		changed := map[string]bool{"changed.go": true}

		resp := buildSymbolRefsResponse("Zap", hits, changed, nil)

		require.Len(resp.Hits, 2)
		assert.Equal(2, resp.InPRTotal)
		assert.Equal(1, resp.OutsidePRTotal)
		assert.False(resp.Truncated)
		for _, h := range resp.Hits {
			assert.Equal("changed.go", h.Path)
		}
		// Line 4 declares the symbol, line 10 merely uses it.
		assert.Equal(gitclone.KindDefinition, resp.Hits[0].Kind)
		assert.Equal(gitclone.KindReference, resp.Hits[1].Kind)
	})

	t.Run("truncates to symbolRefsMaxHits but counts InPRTotal before truncation", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		const total = symbolRefsMaxHits + 10
		hits := make([]gitclone.SymbolHit, total)
		for i := range hits {
			line := i + 1
			hits[i] = gitclone.SymbolHit{
				Path: "f.go",
				Line: line,
				Text: fmt.Sprintf("line %d uses Sym", line),
			}
		}
		changed := map[string]bool{"f.go": true}

		resp := buildSymbolRefsResponse("Sym", hits, changed, nil)

		assert.Equal(total, resp.InPRTotal)
		require.Len(resp.Hits, symbolRefsMaxHits)
		assert.True(resp.Truncated)
		assert.Equal(1, resp.Hits[0].Line)
		assert.Equal(symbolRefsMaxHits, resp.Hits[len(resp.Hits)-1].Line)
	})

	t.Run("orders by kind rank, then path, then line", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		// Deliberately scrambled input order.
		hits := []gitclone.SymbolHit{
			{Path: "b.go", Line: 1, Text: "func Zap() {}"},
			{Path: "a.go", Line: 20, Text: "\tv := Zap(2)"},
			{Path: "a.go", Line: 5, Text: "\tv := Zap(1)"},
			{Path: "z.go", Line: 1, Text: `log.Info("Zap done")`},
			{Path: "a.go", Line: 1, Text: "func Zap() {}"},
			{Path: "c.go", Line: 1, Text: "// Zap note"},
			{Path: "d.go", Line: 1, Text: `import "Zap"`},
		}
		changed := map[string]bool{
			"a.go": true, "b.go": true, "c.go": true, "d.go": true, "z.go": true,
		}

		resp := buildSymbolRefsResponse("Zap", hits, changed, nil)

		require.Len(resp.Hits, 7)
		type key struct {
			path string
			line int
			kind string
		}
		got := make([]key, len(resp.Hits))
		for i, h := range resp.Hits {
			got[i] = key{h.Path, h.Line, h.Kind}
		}
		assert.Equal([]key{
			{"a.go", 1, gitclone.KindDefinition},
			{"b.go", 1, gitclone.KindDefinition},
			{"a.go", 5, gitclone.KindReference},
			{"a.go", 20, gitclone.KindReference},
			{"d.go", 1, gitclone.KindImport},
			{"c.go", 1, gitclone.KindComment},
			{"z.go", 1, gitclone.KindString},
		}, got)
	})

	t.Run("Hits marshals as [] and never null when empty", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		resp := buildSymbolRefsResponse("Sym", nil, map[string]bool{}, nil)

		require.Empty(resp.Hits)
		b, err := json.Marshal(resp)
		require.NoError(err)
		assert.Contains(string(b), `"hits":[]`)
		assert.NotContains(string(b), `"hits":null`)
	})
}

// TestSymbolRefTagsSkipsFilesOutsideChanged covers symbolRefTags in
// isolation: a path outside the supplied changed set must never reach
// readBlob, since buildSymbolRefsResponse only ever labels the changed
// subset of hits and a tag computed for anything else is pure waste
// (an unnecessary Blob fetch plus a ctags subprocess spawn).
func TestSymbolRefTagsSkipsFilesOutsideChanged(t *testing.T) {
	if !ctags.Available() {
		t.Skip("universal-ctags not installed")
	}
	assert := Assert.New(t)

	hits := []gitclone.SymbolHit{
		{Path: "in.cc", Line: 1, Text: "void handle() {}"},
		{Path: "out.cc", Line: 1, Text: "void handle() {}"},
	}
	changed := map[string]bool{"in.cc": true}

	var readCalls []string
	readBlob := func(p string) ([]byte, error) {
		readCalls = append(readCalls, p)
		return []byte("void handle() {}\n"), nil
	}

	tagsByPath := symbolRefTags(context.Background(), hits, changed, readBlob)

	assert.Equal([]string{"in.cc"}, readCalls,
		"out.cc is outside changed and must never be read")
	assert.Contains(tagsByPath, "in.cc")
	assert.NotContains(tagsByPath, "out.cc")
}

// setupSymbolRefsPRServer builds a clones-backed test server with PR
// #1 in acme/widget. base.go is committed on main and left untouched
// by the PR; changed.go is added by the PR and refers to the same
// symbol, so one query against "AnchorSymbol" exercises both sides
// of the in-PR/outside-PR partition in a single request. server.cc
// and notes.zzzlang are also added by the PR, for the ctags-labelling
// subtests below; the symbol they share ("handle") does not appear in
// base.go or changed.go, so they do not affect the AnchorSymbol
// assertions.
func setupSymbolRefsPRServer(t *testing.T) (client *apiclient.Client, headSHA string) {
	t.Helper()

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	bareDir := filepath.Join(dir, "clones")
	require.NoError(t, os.MkdirAll(bareDir, 0o755))
	bare := filepath.Join(bareDir, "github.com", "acme", "widget.git")

	tmpWork := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", "--initial-branch=main", bare)
	runGit(t, dir, "clone", bare, tmpWork)
	runGit(t, tmpWork, "config", "user.email", "test@test.com")
	runGit(t, tmpWork, "config", "user.name", "Test")

	require.NoError(t, os.WriteFile(filepath.Join(tmpWork, "base.go"),
		[]byte("package widgets\n\nvar AnchorSymbol = 0\n"), 0o644))
	runGit(t, tmpWork, "add", ".")
	runGit(t, tmpWork, "commit", "-m", "base commit")
	runGit(t, tmpWork, "push", "origin", "main")
	mergeBase := testGitSHA(t, tmpWork, "HEAD")

	runGit(t, tmpWork, "checkout", "-b", "pr")
	require.NoError(t, os.WriteFile(filepath.Join(tmpWork, "changed.go"),
		[]byte("package widgets\n\nfunc lookup() int {\n\treturn AnchorSymbol\n}\n"), 0o644))
	// server.cc and notes.zzzlang exercise ctags labelling: "handle" is
	// defined once, inside a namespace and a class so its ctags tag
	// carries a scope; called once, a plain reference ctags does not
	// tag; and mentioned once more in a file whose extension ctags
	// does not recognize as any language, so that hit falls back to
	// the textual heuristic instead of erroring or vanishing.
	require.NoError(t, os.WriteFile(filepath.Join(tmpWork, "server.cc"), []byte(
		"namespace kafka {\n"+
			"class server {\n"+
			" public:\n"+
			"  void handle(int x) {}\n"+
			"};\n"+
			"}  // namespace kafka\n"+
			"\n"+
			"void dispatch() {\n"+
			"  kafka::server s;\n"+
			"  s.handle(1);\n"+
			"}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpWork, "notes.zzzlang"),
		[]byte("// handle should not be tagged\n"), 0o644))
	runGit(t, tmpWork, "add", ".")
	runGit(t, tmpWork, "commit", "-m", "add changed.go, server.cc, notes.zzzlang")
	runGit(t, tmpWork, "push", "origin", "pr")
	headSHA = testGitSHA(t, tmpWork, "HEAD")

	clones := gitclone.New(bareDir, nil)
	mock := &mockGH{}
	repos := []ghclient.RepoRef{{Owner: "acme", Name: "widget", PlatformHost: "github.com"}}
	syncer := ghclient.NewSyncer(map[string]ghclient.Client{"github.com": mock}, database, nil, repos, time.Minute, nil, nil)
	t.Cleanup(syncer.Stop)
	srv := New(database, syncer, nil, "/", nil, ServerOptions{Clones: clones})

	seedPR(t, database, "acme", "widget", 1)
	ctx := context.Background()
	repoID, err := database.UpsertRepo(ctx, "github.com", "acme", "widget")
	require.NoError(t, err)
	require.NoError(t, database.UpdateDiffSHAs(ctx, repoID, 1, headSHA, mergeBase, mergeBase))

	client = setupTestClient(t, srv)
	return client, headSHA
}

// TestSymbolRefsEndpointE2E exercises GET .../symbol-refs over the
// generated apiclient for both diff modes: PR mode against a
// clones-backed bare repo, and local mode against a worktree with an
// uncommitted edit.
func TestSymbolRefsEndpointE2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	t.Run("PR mode", func(t *testing.T) {
		client, headSHA := setupSymbolRefsPRServer(t)
		ctx := context.Background()

		t.Run("symbol in a changed file is classified; the same symbol elsewhere only bumps outside_pr_total", func(t *testing.T) {
			assert := Assert.New(t)
			require := require.New(t)

			q := "AnchorSymbol"
			resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
				ctx, "acme", "widget", 1,
				&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &headSHA},
			)
			require.NoError(err)
			require.Equal(http.StatusOK, resp.StatusCode())
			require.NotNil(resp.JSON200)
			require.NotNil(resp.JSON200.Hits)

			hits := *resp.JSON200.Hits
			require.Len(hits, 1)
			assert.Equal("changed.go", hits[0].Path)
			assert.EqualValues(4, hits[0].Line)
			assert.Contains([]string{
				gitclone.KindDefinition, gitclone.KindReference, gitclone.KindImport,
				gitclone.KindComment, gitclone.KindString,
			}, hits[0].Kind)
			assert.EqualValues(1, resp.JSON200.InPrTotal)
			assert.EqualValues(1, resp.JSON200.OutsidePrTotal)
			assert.False(resp.JSON200.Truncated)
		})

		t.Run("classifier reflects ctags.Available()", func(t *testing.T) {
			assert := Assert.New(t)
			require := require.New(t)

			q := "AnchorSymbol"
			resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
				ctx, "acme", "widget", 1,
				&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &headSHA},
			)
			require.NoError(err)
			require.Equal(http.StatusOK, resp.StatusCode())
			require.NotNil(resp.JSON200)

			// Derived rather than hardcoded so this is correct on CI
			// whether or not universal-ctags happens to be installed.
			want := "heuristic"
			if ctags.Available() {
				want = "ctags"
			}
			assert.Equal(want, resp.JSON200.Classifier)
		})

		t.Run("ctags labelling", func(t *testing.T) {
			if !ctags.Available() {
				t.Skip("universal-ctags not installed")
			}

			// Not locally bound: nested t.Run subtests below each
			// bind their own require, which would shadow a package-
			// level binding made here.
			q := "handle"
			resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
				ctx, "acme", "widget", 1,
				&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &headSHA},
			)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode())
			require.NotNil(t, resp.JSON200)
			require.NotNil(t, resp.JSON200.Hits)

			// Definition ranks before reference, which ranks before
			// comment, so this order is exact, not incidental.
			hits := *resp.JSON200.Hits
			require.Len(t, hits, 3)

			t.Run("a C++ definition line carries a claimed-kind tag with a populated scope", func(t *testing.T) {
				assert := Assert.New(t)
				require := require.New(t)

				h := hits[0]
				assert.Equal("server.cc", h.Path)
				assert.EqualValues(4, h.Line)
				assert.Equal(gitclone.KindDefinition, h.Kind)
				require.NotNil(h.Tag)
				// Through the real function rather than a hand-copied
				// list of ctags kinds, so this cannot drift from
				// ctagsKindBuckets the way the list it replaces had.
				assert.Equal(gitclone.KindDefinition, symbolKindForCtagsKind(h.Tag.Kind))
				require.NotNil(h.Tag.Scope)
				assert.NotEmpty(*h.Tag.Scope)
			})

			t.Run("a plain call line carries no tag and kind reference", func(t *testing.T) {
				assert := Assert.New(t)

				h := hits[1]
				assert.Equal("server.cc", h.Path)
				assert.EqualValues(10, h.Line)
				assert.Equal(gitclone.KindReference, h.Kind)
				assert.Nil(h.Tag)
			})

			t.Run("a file in a language ctags does not know still returns a coarse kind and no tag", func(t *testing.T) {
				assert := Assert.New(t)

				h := hits[2]
				assert.Equal("notes.zzzlang", h.Path)
				assert.Equal(gitclone.KindComment, h.Kind)
				assert.Nil(h.Tag)
			})
		})

		t.Run("symbol absent from the tree returns 200 with empty hits, not 404 or 502", func(t *testing.T) {
			assert := Assert.New(t)
			require := require.New(t)

			q := "ZzzNoSuchSymbolAnywhere999"
			resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
				ctx, "acme", "widget", 1,
				&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &headSHA},
			)
			require.NoError(err)
			require.Equal(http.StatusOK, resp.StatusCode())
			require.NotNil(resp.JSON200)
			// Non-nil proves the wire payload was `[]`, not `null`:
			// oapi-codegen leaves a nullable slice field nil only
			// when the JSON value actually was null.
			require.NotNil(resp.JSON200.Hits)
			assert.Empty(*resp.JSON200.Hits)
			assert.EqualValues(0, resp.JSON200.InPrTotal)
			assert.EqualValues(0, resp.JSON200.OutsidePrTotal)
		})

		validQ := "AnchorSymbol"
		// malformedSHA is option-shaped: git parses flags anywhere on
		// its command line, so a sha reaching `git grep` unvalidated
		// would be read as --open-files-in-pager, which runs a shell
		// command. This case would fail if the sha guard in
		// getSymbolRefs were removed or loosened.
		malformedSHA := "-Otouch /tmp/should-not-be-created"
		errCases := []struct {
			name       string
			number     int64
			q, sha     *string
			wantStatus int
		}{
			{name: "missing q", number: 1, q: nil, sha: &headSHA, wantStatus: http.StatusBadRequest},
			{name: "missing sha", number: 1, q: &validQ, sha: nil, wantStatus: http.StatusBadRequest},
			{name: "unknown PR number", number: 99999, q: &validQ, sha: &headSHA, wantStatus: http.StatusNotFound},
			{name: "malformed sha (option-shaped)", number: 1, q: &validQ, sha: &malformedSHA, wantStatus: http.StatusBadRequest},
		}
		for _, tc := range errCases {
			t.Run(tc.name, func(t *testing.T) {
				assert := Assert.New(t)
				require := require.New(t)

				resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
					ctx, "acme", "widget", tc.number,
					&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: tc.q, Sha: tc.sha},
				)
				require.NoError(err)
				assert.Equal(tc.wantStatus, resp.StatusCode())
			})
		}
	})

	t.Run("local mode: an uncommitted edit is found via the working-tree sentinel", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		dir := t.TempDir()
		runGitWT(t, "", "init", "--initial-branch=main", dir)
		runGitWT(t, dir, "config", "user.email", "test@example.com")
		runGitWT(t, dir, "config", "user.name", "Test")

		committed := "package widgets\n\nvar Committed = 1\n"
		require.NoError(os.WriteFile(filepath.Join(dir, "tracked.go"), []byte(committed), 0o644))
		runGitWT(t, dir, "add", "tracked.go")
		runGitWT(t, dir, "commit", "-m", "add tracked.go")

		// Uncommitted: this symbol exists only on disk, never committed.
		edited := committed + "var UncommittedWidget = 2\n"
		require.NoError(os.WriteFile(filepath.Join(dir, "tracked.go"), []byte(edited), 0o644))

		srv, database := setupTestServer(t)
		client := setupTestClient(t, srv)
		ctx := context.Background()

		repoID, err := database.UpsertLocalRepo(ctx, "demo")
		require.NoError(err)
		canonDir, err := filepath.EvalSymlinks(dir)
		require.NoError(err)
		w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
			Path:   canonDir,
			Branch: "main",
		})
		require.NoError(err)

		q := "UncommittedWidget"
		sentinel := worktrees.WorkingTreeSentinel
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
			ctx, "local", "demo", w.ID,
			&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &sentinel},
		)
		require.NoError(err)
		require.Equal(http.StatusOK, resp.StatusCode())
		require.NotNil(resp.JSON200)
		require.NotNil(resp.JSON200.Hits)

		hits := *resp.JSON200.Hits
		require.Len(hits, 1)
		assert.Equal("tracked.go", hits[0].Path)
		assert.EqualValues(4, hits[0].Line)
		assert.Equal(gitclone.KindDefinition, hits[0].Kind)
		assert.EqualValues(1, resp.JSON200.InPrTotal)
	})

	t.Run("local mode: ctags labels an uncommitted C++ definition using the working-tree sentinel", func(t *testing.T) {
		if !ctags.Available() {
			t.Skip("universal-ctags not installed")
		}
		assert := Assert.New(t)
		require := require.New(t)

		dir := t.TempDir()
		runGitWT(t, "", "init", "--initial-branch=main", dir)
		runGitWT(t, dir, "config", "user.email", "test@example.com")
		runGitWT(t, dir, "config", "user.name", "Test")

		committed := "int Committed = 1;\n"
		require.NoError(os.WriteFile(filepath.Join(dir, "tracked.cc"), []byte(committed), 0o644))
		runGitWT(t, dir, "add", "tracked.cc")
		runGitWT(t, dir, "commit", "-m", "add tracked.cc")

		// Uncommitted: this function exists only on disk, never
		// committed, so its tag can only come from the working tree
		// via the sentinel SHA, never from a git blob.
		edited := committed + "\nvoid UncommittedWidget() {}\n"
		require.NoError(os.WriteFile(filepath.Join(dir, "tracked.cc"), []byte(edited), 0o644))

		srv, database := setupTestServer(t)
		client := setupTestClient(t, srv)
		ctx := context.Background()

		repoID, err := database.UpsertLocalRepo(ctx, "demo")
		require.NoError(err)
		canonDir, err := filepath.EvalSymlinks(dir)
		require.NoError(err)
		w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
			Path:   canonDir,
			Branch: "main",
		})
		require.NoError(err)

		q := "UncommittedWidget"
		sentinel := worktrees.WorkingTreeSentinel
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
			ctx, "local", "demo", w.ID,
			&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &sentinel},
		)
		require.NoError(err)
		require.Equal(http.StatusOK, resp.StatusCode())
		require.NotNil(resp.JSON200)
		require.NotNil(resp.JSON200.Hits)

		hits := *resp.JSON200.Hits
		require.Len(hits, 1)
		assert.Equal("tracked.cc", hits[0].Path)
		assert.EqualValues(3, hits[0].Line)
		assert.Equal(gitclone.KindDefinition, hits[0].Kind)
		require.NotNil(hits[0].Tag)
		assert.Equal("function", hits[0].Tag.Kind)
		assert.Equal("ctags", resp.JSON200.Classifier)
	})

	t.Run("local mode: an oversized file's hits fall back to the heuristic without failing the search, and a healthy file alongside it still gets tagged", func(t *testing.T) {
		if !ctags.Available() {
			t.Skip("universal-ctags not installed")
		}
		assert := Assert.New(t)
		require := require.New(t)

		dir := t.TempDir()
		runGitWT(t, "", "init", "--initial-branch=main", dir)
		runGitWT(t, dir, "config", "user.email", "test@example.com")
		runGitWT(t, dir, "config", "user.name", "Test")

		// A baseline commit so DiffAgainstBase's no-upstream fallback
		// (base = HEAD) has a HEAD to fall back to.
		require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
		runGitWT(t, dir, "add", "base.txt")
		runGitWT(t, dir, "commit", "-m", "base commit")

		require.NoError(os.WriteFile(filepath.Join(dir, "healthy.cc"),
			[]byte("void isolationProbe() {}\n"), 0o644))

		// Padded well past blobMaxBytes so it is symbolRefTags's size
		// cap — not ctags, and not the changed-set filter, since both
		// files are staged as additions relative to the base commit —
		// that skips this file. The padding sits in a single comment
		// line; ctags would tag a real definition shaped like
		// healthy.cc's (verified above) if it were ever allowed to
		// run on this file.
		huge := "// " + strings.Repeat("a", blobMaxBytes+1024) + " isolationProbe\n"
		require.NoError(os.WriteFile(filepath.Join(dir, "huge.cc"), []byte(huge), 0o644))

		// Staged, not committed: DiffAgainstBase's `git diff HEAD
		// --raw` reports a staged-but-uncommitted new file as an
		// addition (verified directly against git), which is enough
		// to land both paths in the changed set without a second
		// commit. `git grep` with no revision (the sentinel path)
		// likewise searches the working tree's tracked content, so
		// it finds them the same way.
		runGitWT(t, dir, "add", "healthy.cc", "huge.cc")

		srv, database := setupTestServer(t)
		client := setupTestClient(t, srv)
		ctx := context.Background()

		repoID, err := database.UpsertLocalRepo(ctx, "demo")
		require.NoError(err)
		canonDir, err := filepath.EvalSymlinks(dir)
		require.NoError(err)
		w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
			Path:   canonDir,
			Branch: "main",
		})
		require.NoError(err)

		q := "isolationProbe"
		sentinel := worktrees.WorkingTreeSentinel
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
			ctx, "local", "demo", w.ID,
			&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &sentinel},
		)
		require.NoError(err)
		require.Equal(http.StatusOK, resp.StatusCode())
		require.NotNil(resp.JSON200)
		require.NotNil(resp.JSON200.Hits)

		// Definition (healthy.cc) ranks before comment (huge.cc), so
		// this order is exact, not incidental.
		hits := *resp.JSON200.Hits
		require.Len(hits, 2)

		assert.Equal("healthy.cc", hits[0].Path)
		assert.Equal(gitclone.KindDefinition, hits[0].Kind)
		require.NotNil(hits[0].Tag)
		assert.Equal("function", hits[0].Tag.Kind)

		assert.Equal("huge.cc", hits[1].Path)
		assert.Equal(gitclone.KindComment, hits[1].Kind)
		assert.Nil(hits[1].Tag)
	})

	t.Run("local mode: a symbol absent from the worktree returns 200 with empty hits", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		dir := t.TempDir()
		runGitWT(t, "", "init", "--initial-branch=main", dir)
		runGitWT(t, dir, "config", "user.email", "test@example.com")
		runGitWT(t, dir, "config", "user.name", "Test")

		require.NoError(os.WriteFile(filepath.Join(dir, "tracked.go"),
			[]byte("package widgets\n\nvar Committed = 1\n"), 0o644))
		runGitWT(t, dir, "add", "tracked.go")
		runGitWT(t, dir, "commit", "-m", "add tracked.go")

		srv, database := setupTestServer(t)
		client := setupTestClient(t, srv)
		ctx := context.Background()

		repoID, err := database.UpsertLocalRepo(ctx, "demo")
		require.NoError(err)
		canonDir, err := filepath.EvalSymlinks(dir)
		require.NoError(err)
		w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
			Path:   canonDir,
			Branch: "main",
		})
		require.NoError(err)

		// This symbol appears nowhere in the worktree: git grep exits
		// 1, which worktrees.GrepSymbol (grep.go) treats as an empty
		// result rather than an error, so this must surface as 200
		// with an empty list — never 404, never 502.
		q := "ZzzNoSuchSymbolAnywhere999"
		sentinel := worktrees.WorkingTreeSentinel
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
			ctx, "local", "demo", w.ID,
			&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &sentinel},
		)
		require.NoError(err)
		require.Equal(http.StatusOK, resp.StatusCode())
		require.NotNil(resp.JSON200)
		// Non-nil proves the wire payload was `[]`, not `null`.
		require.NotNil(resp.JSON200.Hits)
		assert.Empty(*resp.JSON200.Hits)
		assert.EqualValues(0, resp.JSON200.InPrTotal)
	})

	t.Run("local mode: an unknown worktree number returns 404", func(t *testing.T) {
		assert := Assert.New(t)
		require := require.New(t)

		srv, _ := setupTestServer(t)
		client := setupTestClient(t, srv)
		ctx := context.Background()

		q := "AnySymbol"
		sentinel := worktrees.WorkingTreeSentinel
		resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSymbolRefsWithResponse(
			ctx, "local", "demo", 99999,
			&generated.GetReposByOwnerByNamePullsByNumberSymbolRefsParams{Q: &q, Sha: &sentinel},
		)
		require.NoError(err)
		assert.Equal(http.StatusNotFound, resp.StatusCode())
	})
}
