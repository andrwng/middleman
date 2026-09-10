# Code Browser Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a collapsible, full-width-from-right code browser panel to the diff review surface: browse the full repo tree at the reviewed commit's SHA, open any file read-only, and jump in from a symbol-refs hit.

**Architecture:** Two new backend endpoints (`tree` listing, `code-browser-state` bookmark) follow the existing `getBlob`/`getBlobRange` and `getPRNotes`/`putPRNotes` shapes exactly — same `isLocalSource` dispatch, same `mr_id`-keyed storage. One new frontend panel component reuses the existing `blob`/`blob-range` endpoints for content, the existing per-line Shiki highlighting pipeline, and the existing symbol-refs store for jump-in.

**Tech Stack:** Go (huma, modernc.org/sqlite), Svelte 5 runes, generated OpenAPI client, Shiki (via `packages/ui/src/utils/highlight.ts`), Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-09-code-browser-panel-design.md`

## Global Constraints

- File scope: full repo tree at the commit's SHA, not just PR-changed files.
- No new search UI. Existing symbol-refs (`s` / "Refs" button) can open this panel at a path/line.
- Open behavior: full-width-from-right overlay covering the diff area while open, not a resizable split.
- Trigger: toolbar button plus keyboard shortcut `b`, alongside existing `j`/`k`/`[`/`]`/`m`/`s`.
- Initial file on open: the diff view's active file, only when no path has been browsed yet for this PR.
- Browsed path is sticky per-PR, independent of the diff's active file. A path missing at the current SHA shows a "missing at this commit" state, never a fallback file.
- Storage: backend, keyed by `merge_request_id`, same pattern as PR notes — not localStorage. Works identically for GitHub PRs and local worktrees via `resolveOrEnsureMRID`.
- `make api-generate` must be run after any OpenAPI-affecting backend change, regenerating `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `packages/ui/src/api/generated/{schema,client}.ts`, `internal/apiclient/generated/client.gen.go`.
- Use `testify` (`require` for setup, `assert` for checks) in Go tests; `-shuffle=on`, no `-count=1`, no `-v` unless debugging.

---

## Task 1: DB migration + queries for the browsed-file bookmark

**Files:**
- Create: `internal/db/migrations/000025_add_code_browser_state.up.sql`
- Create: `internal/db/migrations/000025_add_code_browser_state.down.sql`
- Create: `internal/db/queries_code_browser.go`
- Create: `internal/db/queries_code_browser_test.go`

**Interfaces:**
- Produces: `db.CodeBrowserState{ MergeRequestID int64; Path string; UpdatedAt time.Time }`, `(*DB) GetCodeBrowserState(ctx context.Context, mrID int64) (CodeBrowserState, error)`, `(*DB) SetCodeBrowserState(ctx context.Context, mrID int64, path string) (CodeBrowserState, error)`.

- [ ] **Step 1: Write the migration**

`internal/db/migrations/000025_add_code_browser_state.up.sql`:
```sql
CREATE TABLE IF NOT EXISTS middleman_code_browser_state (
    mr_id      INTEGER PRIMARY KEY REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path       TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

`internal/db/migrations/000025_add_code_browser_state.down.sql`:
```sql
DROP TABLE IF EXISTS middleman_code_browser_state;
```

- [ ] **Step 2: Write the failing test**

`internal/db/queries_code_browser_test.go`:
```go
package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeBrowserState_NoRowDefault(t *testing.T) {
	database := openTestDB(t)
	mrID := insertTestMergeRequest(t, database)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, mrID, got.MergeRequestID)
	assert.Equal(t, "", got.Path)
}

func TestCodeBrowserState_SetThenGet(t *testing.T) {
	database := openTestDB(t)
	mrID := insertTestMergeRequest(t, database)

	set, err := database.SetCodeBrowserState(context.Background(), mrID, "src/kafka/server.cc")
	require.NoError(t, err)
	assert.Equal(t, "src/kafka/server.cc", set.Path)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, "src/kafka/server.cc", got.Path)
}

func TestCodeBrowserState_SetOverwrites(t *testing.T) {
	database := openTestDB(t)
	mrID := insertTestMergeRequest(t, database)

	_, err := database.SetCodeBrowserState(context.Background(), mrID, "a.txt")
	require.NoError(t, err)
	_, err = database.SetCodeBrowserState(context.Background(), mrID, "b.txt")
	require.NoError(t, err)

	got, err := database.GetCodeBrowserState(context.Background(), mrID)
	require.NoError(t, err)
	assert.Equal(t, "b.txt", got.Path)
}
```

If `insertTestMergeRequest(t, database) int64` does not already exist as a test helper in this package, check `queries_notes_test.go` for the equivalent helper it uses (it must have one, since PR notes tests need an `mr_id` too) and reuse that exact helper name instead of inventing a new one.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/db -run TestCodeBrowserState -shuffle=on`
Expected: FAIL — `GetCodeBrowserState`/`SetCodeBrowserState` undefined.

- [ ] **Step 4: Write the implementation**

`internal/db/queries_code_browser.go`:
```go
package db

import (
	"context"
	"database/sql"
	"time"
)

// CodeBrowserState is the last file path browsed in the code browser panel
// for one pull request, keyed by merge_request_id like PR notes.
type CodeBrowserState struct {
	MergeRequestID int64
	Path           string
	UpdatedAt      time.Time
}

func (d *DB) GetCodeBrowserState(ctx context.Context, mrID int64) (CodeBrowserState, error) {
	var s CodeBrowserState
	row := d.conn.QueryRowContext(ctx,
		`SELECT mr_id, path, updated_at FROM middleman_code_browser_state WHERE mr_id = ?`,
		mrID)
	err := row.Scan(&s.MergeRequestID, &s.Path, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return CodeBrowserState{MergeRequestID: mrID}, nil
	}
	if err != nil {
		return CodeBrowserState{}, err
	}
	return s, nil
}

func (d *DB) SetCodeBrowserState(ctx context.Context, mrID int64, path string) (CodeBrowserState, error) {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO middleman_code_browser_state (mr_id, path, updated_at)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(mr_id) DO UPDATE SET path = excluded.path, updated_at = excluded.updated_at`,
		mrID, path)
	if err != nil {
		return CodeBrowserState{}, err
	}
	return d.GetCodeBrowserState(ctx, mrID)
}
```

Check `queries_notes.go:23-55` for the exact field name of the underlying `sql.DB`/`sql.Conn` handle on `*DB` (referenced above as `d.conn`) and use whatever it's actually called there — copy that field access verbatim rather than guessing.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/db -run TestCodeBrowserState -shuffle=on`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/000025_add_code_browser_state.up.sql \
        internal/db/migrations/000025_add_code_browser_state.down.sql \
        internal/db/queries_code_browser.go \
        internal/db/queries_code_browser_test.go
git commit -m "feat(db): add code browser bookmark storage keyed by merge_request_id"
```

---

## Task 2: `gitclone.Tree` — list a directory at a SHA

**Files:**
- Create: `internal/gitclone/tree.go`
- Create: `internal/gitclone/tree_test.go`

**Interfaces:**
- Consumes: `m.ClonePath(host, owner, name)`, `m.git(ctx, host, dir, args...)` (both from `clone.go`, same as `Blob`), `ErrNotFound` (`clone.go:18-19`).
- Produces: `type TreeEntry struct { Name string; Path string; Type string }` (`Type` is `"dir"` or `"file"`), `(*Manager) Tree(ctx context.Context, host, owner, name, sha, path string) ([]TreeEntry, error)`.

- [ ] **Step 1: Write the failing test**

`internal/gitclone/tree_test.go`:
```go
package gitclone

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTree_Root(t *testing.T) {
	mgr, host, owner, name, sha := setupDiffRepoForTree(t)

	entries, err := mgr.Tree(context.Background(), host, owner, name, sha, "")
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
	for _, e := range entries {
		assert.NotEmpty(t, e.Name)
		assert.Contains(t, []string{"dir", "file"}, e.Type)
	}
}

func TestTree_NonexistentPath(t *testing.T) {
	mgr, host, owner, name, sha := setupDiffRepoForTree(t)

	_, err := mgr.Tree(context.Background(), host, owner, name, sha, "does/not/exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestTree_NonexistentSHA(t *testing.T) {
	mgr, host, owner, name, _ := setupDiffRepoForTree(t)

	_, err := mgr.Tree(context.Background(), host, owner, name, "0000000000000000000000000000000000000000", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}
```

`setupDiffRepoForTree` must return a `*Manager` with a real bare clone already set up, plus the `host`/`owner`/`name`/`sha` to address it. Look at how `blob_test.go` (referenced by the backend research as sharing `internal/testutil/diff_repo.go`'s `SetupDiffRepo` fixture) obtains its `*Manager` and known SHA, and write `setupDiffRepoForTree` as a thin wrapper around that same fixture — copy the existing blob test's setup call exactly rather than inventing a new fixture.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gitclone -run TestTree -shuffle=on`
Expected: FAIL — `Tree`/`TreeEntry` undefined.

- [ ] **Step 3: Write the implementation**

`internal/gitclone/tree.go`:
```go
package gitclone

import (
	"context"
	"fmt"
	"strings"
)

// TreeEntry is one file or directory listed by Tree.
type TreeEntry struct {
	Name string
	Path string
	Type string // "dir" or "file"
}

// Tree lists the immediate entries of path (empty string for repo root)
// as they existed at sha, without recursing into subdirectories.
func (m *Manager) Tree(ctx context.Context, host, owner, name, sha, path string) ([]TreeEntry, error) {
	dir := m.ClonePath(host, owner, name)
	rev := sha + ":" + path

	out, err := m.git(ctx, host, dir, "ls-tree", rev)
	if err != nil {
		return nil, err
	}

	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// <mode> SP <type> SP <sha>\t<name>
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			return nil, fmt.Errorf("gitclone: unexpected ls-tree line %q", line)
		}
		meta, entryName := line[:tabIdx], line[tabIdx+1:]
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			return nil, fmt.Errorf("gitclone: unexpected ls-tree metadata %q", meta)
		}
		entryType := "file"
		if fields[1] == "tree" {
			entryType = "dir"
		}
		entryPath := entryName
		if path != "" {
			entryPath = path + "/" + entryName
		}
		entries = append(entries, TreeEntry{Name: entryName, Path: entryPath, Type: entryType})
	}
	return entries, nil
}
```

`m.git`'s existing stderr-sniffing (`clone.go:206+`, `isNotFoundError`) already maps "unknown revision" and similar to `ErrNotFound` for `Blob`; confirm during implementation that the same stderr patterns cover `ls-tree`'s "not a tree object"/"unknown revision" errors — if `ls-tree` emits a message `isNotFoundError` doesn't recognize, add that message to `isNotFoundError` in `clone.go` rather than duplicating detection logic here.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gitclone -run TestTree -shuffle=on`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gitclone/tree.go internal/gitclone/tree_test.go
git commit -m "feat(gitclone): add Tree for listing a directory at a SHA"
```

---

## Task 3: `worktrees.Tree` — list a directory in a local worktree

**Files:**
- Create: `internal/worktrees/tree.go`
- Create: `internal/worktrees/tree_test.go`

**Interfaces:**
- Consumes: `gitCmd` helper and `WorkingTreeSentinel` (same package, used by `worktrees.Blob`), the local `ErrNotFound` defined in `worktrees/blob.go`.
- Produces: `(*Manager) Tree(ctx context.Context, worktreePath, sha, path string) ([]gitclone.TreeEntry, error)` — reuse `gitclone.TreeEntry` rather than defining a second type, so the HTTP handler layer (Task 4) can treat both sources identically.

- [ ] **Step 1: Write the failing test**

`internal/worktrees/tree_test.go`:
```go
package worktrees

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTree_WorkingTree(t *testing.T) {
	worktreePath, headSHA := setupRepoWithRemote(t)

	entries, err := Tree(context.Background(), worktreePath, WorkingTreeSentinel, "")
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	entries2, err := Tree(context.Background(), worktreePath, headSHA, "")
	require.NoError(t, err)
	assert.NotEmpty(t, entries2)
}

func TestTree_NonexistentPath(t *testing.T) {
	worktreePath, _ := setupRepoWithRemote(t)

	_, err := Tree(context.Background(), worktreePath, WorkingTreeSentinel, "does/not/exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}
```

Confirm the exact return signature of `setupRepoWithRemote(t)` used by the existing symbol-refs worktree tests (it must already hand back a worktree path and a known SHA) and match its real signature — adjust the two-value destructuring above if it returns more or fewer values.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktrees -run TestTree -shuffle=on`
Expected: FAIL — `Tree` undefined.

- [ ] **Step 3: Write the implementation**

`internal/worktrees/tree.go`:
```go
package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/awong/middleman/internal/gitclone"
)

// Tree lists the immediate entries of path (empty string for repo root)
// in the worktree at worktreePath, either at sha or, when sha equals
// WorkingTreeSentinel, in the working tree on disk.
func Tree(ctx context.Context, worktreePath, sha, path string) ([]gitclone.TreeEntry, error) {
	if sha == WorkingTreeSentinel {
		return treeFromDisk(worktreePath, path)
	}
	return treeFromRev(ctx, worktreePath, sha, path)
}

func treeFromDisk(worktreePath, path string) ([]gitclone.TreeEntry, error) {
	full := filepath.Join(worktreePath, path)
	rel, err := filepath.Rel(worktreePath, full)
	if err != nil || rel == ".." || len(rel) >= 2 && rel[:3] == "../" {
		return nil, ErrNotFound
	}

	dirEntries, err := os.ReadDir(full)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	entries := make([]gitclone.TreeEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.Name() == ".git" {
			continue
		}
		entryType := "file"
		if de.IsDir() {
			entryType = "dir"
		}
		entryPath := de.Name()
		if path != "" {
			entryPath = path + "/" + de.Name()
		}
		entries = append(entries, gitclone.TreeEntry{Name: de.Name(), Path: entryPath, Type: entryType})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func treeFromRev(ctx context.Context, worktreePath, sha, path string) ([]gitclone.TreeEntry, error) {
	// Mirror gitclone.Manager.Tree's ls-tree parsing against this worktree
	// via gitCmd instead of Manager.git — same rev:path addressing, same
	// stderr-based ErrNotFound mapping worktrees.Blob already uses for
	// "does not exist"/"Not a valid object name".
	out, err := gitCmd(ctx, worktreePath, "ls-tree", sha+":"+path)
	if err != nil {
		if isTreeNotFoundError(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return parseLsTree(out, path)
}
```

Before finalizing this step: read the exact body of `worktrees.Blob`'s rev-mode branch (the `cat-file -p sha:path` call and its stderr string-matching for "does not exist"/"Not a valid object name") and copy its error-classification helper verbatim (or call it directly if it's already a shared unexported function) instead of writing a new `isTreeNotFoundError` — only add a new helper if no shared one exists. Similarly, factor `parseLsTree` out of `gitclone.Tree`'s parsing loop (Task 2) into a small shared function if that's cleaner than duplicating the parser — use judgment here, but don't leave two independently-written copies of the same parsing logic if a one-line extraction avoids it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktrees -run TestTree -shuffle=on`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/worktrees/tree.go internal/worktrees/tree_test.go
git commit -m "feat(worktrees): add Tree for listing a directory in a local worktree"
```

---

## Task 4: Backend `tree` endpoint

**Files:**
- Modify: `internal/server/huma_routes.go`
- Modify: `internal/server/local_dispatch.go`
- Create: `internal/server/tree_e2e_test.go`

**Interfaces:**
- Consumes: `gitclone.Tree` (Task 2), `worktrees.Tree` (Task 3), `isLocalSource`, `s.clones`, `s.db.GetMRIDByRepoAndNumber`, `s.resolveLocalWorktree`, `symbolRefsSHA`/`worktrees.WorkingTreeSentinel` validation (same as `getBlobLocal`).
- Produces: `GET /repos/{owner}/{name}/pulls/{number}/tree` registered via `huma.Get`.

- [ ] **Step 1: Write the failing e2e test**

`internal/server/tree_e2e_test.go`:
```go
package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPITree_RootAndSubdir(t *testing.T) {
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	root, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: mergeBase, Path: nil})
	require.NoError(t, err)
	assert := assert.New(t)
	assert.Equal(http.StatusOK, root.StatusCode())
	require.NotNil(t, root.JSON200)
	assert.NotEmpty(root.JSON200.Entries)
}

func TestAPITree_NonexistentPath(t *testing.T) {
	client, _, mergeBase, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badPath := "does/not/exist"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: mergeBase, Path: &badPath})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

func TestAPITree_NonexistentSHA(t *testing.T) {
	client, _, _, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()

	badSHA := "0000000000000000000000000000000000000000"
	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberTreeWithResponse(
		ctx, "acme", "widget", 1, &generated.GetReposByOwnerByNamePullsByNumberTreeParams{Sha: badSHA, Path: nil})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}
```

Import path and exact generated-method name (`GetReposByOwnerByNamePullsByNumberTreeWithResponse`) won't exist until Step 3's `make api-generate` runs — this test necessarily fails to compile first, which is the expected "RED" state for this step; add the `generated` import matching the one used elsewhere in this test file's package (check any neighboring `_e2e_test.go` for the exact import path/alias).

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./internal/server/...`
Expected: FAIL to compile — `generated.GetReposByOwnerByNamePullsByNumberTreeParams` and the `WithResponse` method don't exist yet.

- [ ] **Step 3: Add the input/output types and handler, register the route, regenerate the API**

In `huma_routes.go`, add near `getBlob`/`getBlobInput` (`:2452-2502`):
```go
type getTreeInput struct {
	Owner string `path:"owner"`
	Name  string `path:"name"`
	Number int   `path:"number"`
	Path  string `query:"path" doc:"Directory path within the repo, empty for root"`
	SHA   string `query:"sha"  doc:"Commit/tree SHA to list"`
}
type getTreeOutput struct{ Body treeResponse }
type treeResponse struct {
	Path    string          `json:"path"`
	Entries []treeEntryJSON `json:"entries"`
}
type treeEntryJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type" doc:"'dir' or 'file'"`
}

func (s *Server) getTree(ctx context.Context, input *getTreeInput) (*getTreeOutput, error) {
	if isLocalSource(input.Owner) {
		return s.getTreeLocal(ctx, input)
	}
	if s.clones == nil {
		return nil, huma.Error502BadGateway("git clones not configured")
	}
	if input.SHA == "" {
		return nil, huma.Error400BadRequest("sha is required")
	}
	if _, err := s.db.GetMRIDByRepoAndNumber(ctx, input.Owner, input.Name, input.Number); err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	entries, err := s.clones.Tree(ctx, s.githubHost, input.Owner, input.Name, input.SHA, input.Path)
	if errors.Is(err, gitclone.ErrNotFound) {
		return nil, huma.Error404NotFound("path not found at that sha")
	}
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	return &getTreeOutput{Body: treeResponse{Path: input.Path, Entries: toTreeEntryJSON(entries)}}, nil
}

func toTreeEntryJSON(entries []gitclone.TreeEntry) []treeEntryJSON {
	out := make([]treeEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = treeEntryJSON{Name: e.Name, Path: e.Path, Type: e.Type}
	}
	return out
}
```

Before writing `s.githubHost`/`s.clones.Tree(...)` call arguments verbatim, re-check `getBlob`'s handler body (`huma_routes.go:2473-2502`) for the exact receiver expression it uses for the host argument to `s.clones.Blob(...)` — copy that expression exactly, since the field/method name for "host" on `*Server` was not independently re-verified for this step and must match `getBlob`'s usage exactly, not be guessed.

In `local_dispatch.go`, add near `getBlobLocal`:
```go
func (s *Server) getTreeLocal(ctx context.Context, input *getTreeInput) (*getTreeOutput, error) {
	w, err := s.resolveLocalWorktree(ctx, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("local worktree not found")
	}
	if input.SHA != worktrees.WorkingTreeSentinel {
		if err := validateSymbolRefsSHA(input.SHA); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
	}

	entries, err := worktrees.Tree(ctx, w.Path, input.SHA, input.Path)
	if errors.Is(err, worktrees.ErrNotFound) {
		return nil, huma.Error404NotFound("path not found at that sha")
	}
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	return &getTreeOutput{Body: treeResponse{Path: input.Path, Entries: toTreeEntryJSON(entries)}}, nil
}
```

`validateSymbolRefsSHA` is a placeholder name — find the actual function `getBlobLocal` calls to validate `SHA` against `symbolRefsSHA`-or-sentinel (mentioned in the spec's facts section) and call that exact function instead.

Register the route next to the existing blob registrations:
```go
huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/tree", s.getTree)
```

Then run:
```bash
make api-generate
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server -run TestAPITree -shuffle=on`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/huma_routes.go internal/server/local_dispatch.go \
        internal/server/tree_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        packages/ui/src/api/generated/schema.ts packages/ui/src/api/generated/client.ts \
        internal/apiclient/generated/client.gen.go
git commit -m "feat(server): add tree listing endpoint for the code browser panel"
```

---

## Task 5: Backend `code-browser-state` endpoint

**Files:**
- Modify: `internal/server/huma_routes.go`
- Create: `internal/server/code_browser_state_e2e_test.go`

**Interfaces:**
- Consumes: `db.GetCodeBrowserState`/`SetCodeBrowserState` (Task 1), `resolveOrEnsureMRID` (`local_dispatch.go:74-85`, already exists).
- Produces: `GET`/`PUT /repos/{owner}/{name}/pulls/{number}/code-browser-state`.

- [ ] **Step 1: Write the failing e2e test**

`internal/server/code_browser_state_e2e_test.go`, modeled directly on the existing `TestAPIPRNotes_CRUD`:
```go
package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPICodeBrowserState_CRUD(t *testing.T) {
	client, _, _, _, _ := setupTestServerWithClones(t)
	ctx := context.Background()
	as := assert.New(t)

	empty, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(ctx, "acme", "widget", 1)
	require.NoError(t, err)
	as.Equal(http.StatusOK, empty.StatusCode())
	require.NotNil(t, empty.JSON200)
	as.Equal("", empty.JSON200.Path)

	put, err := client.HTTP.PutReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(
		ctx, "acme", "widget", 1,
		generated.PutReposByOwnerByNamePullsByNumberCodeBrowserStateJSONRequestBody{Path: "src/kafka/server.cc"})
	require.NoError(t, err)
	as.Equal(http.StatusOK, put.StatusCode())
	require.NotNil(t, put.JSON200)
	as.Equal("src/kafka/server.cc", put.JSON200.Path)

	got, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCodeBrowserStateWithResponse(ctx, "acme", "widget", 1)
	require.NoError(t, err)
	require.NotNil(t, got.JSON200)
	as.Equal("src/kafka/server.cc", got.JSON200.Path)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./internal/server/...`
Expected: FAIL to compile — generated methods don't exist yet.

- [ ] **Step 3: Add types, handlers, registration; regenerate**

In `huma_routes.go`, near `getPRNotes`/`putPRNotes` (`:2832,2844`):
```go
type getCodeBrowserStateOutput struct{ Body codeBrowserStateResponse }
type putCodeBrowserStateInput struct {
	repoNumberInput
	Body struct {
		Path string `json:"path"`
	}
}
type codeBrowserStateResponse struct {
	Path      string     `json:"path"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func (s *Server) getCodeBrowserState(ctx context.Context, input *repoNumberInput) (*getCodeBrowserStateOutput, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	state, err := s.db.GetCodeBrowserState(ctx, mrID)
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	resp := codeBrowserStateResponse{Path: state.Path}
	if state.Path != "" {
		resp.UpdatedAt = &state.UpdatedAt
	}
	return &getCodeBrowserStateOutput{Body: resp}, nil
}

func (s *Server) putCodeBrowserState(ctx context.Context, input *putCodeBrowserStateInput) (*getCodeBrowserStateOutput, error) {
	mrID, err := s.resolveOrEnsureMRID(ctx, input.Owner, input.Name, input.Number)
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	state, err := s.db.SetCodeBrowserState(ctx, mrID, input.Body.Path)
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	return &getCodeBrowserStateOutput{Body: codeBrowserStateResponse{Path: state.Path, UpdatedAt: &state.UpdatedAt}}, nil
}
```

Confirm `repoNumberInput` (`huma_routes.go:39-43`) is the exact struct `getPRNotes` embeds/uses as its input type, and match `putPRNotes`'s exact input-struct shape (embedding vs. separate fields, and whether its body field is `Content string` directly or wrapped) instead of guessing the shape above — copy `putPRNotes`'s input struct pattern verbatim, changing only field names.

Register:
```go
huma.Get(api, "/repos/{owner}/{name}/pulls/{number}/code-browser-state", s.getCodeBrowserState)
huma.Put(api, "/repos/{owner}/{name}/pulls/{number}/code-browser-state", s.putCodeBrowserState)
```

Then:
```bash
make api-generate
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server -run TestAPICodeBrowserState -shuffle=on`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/huma_routes.go internal/server/code_browser_state_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        packages/ui/src/api/generated/schema.ts packages/ui/src/api/generated/client.ts \
        internal/apiclient/generated/client.gen.go
git commit -m "feat(server): add code browser bookmark GET/PUT endpoint"
```

---

## Task 6: Frontend `codeBrowser` store

**Files:**
- Create: `packages/ui/src/stores/codeBrowser.svelte.ts`
- Create: `packages/ui/src/stores/codeBrowser.svelte.test.ts`
- Modify: `packages/ui/src/context.js` or wherever `Provider.svelte` registers stores (see Interfaces)

**Interfaces:**
- Consumes: `client.GET`/`client.PUT` typed client (same shape as `symbolRefs.svelte.ts:165-167`), `stubClient()` test helper (must gain a `PUT` stub key — none exists yet per research).
- Produces: `createCodeBrowserStore({ client }) -> CodeBrowserStore` with `{ open(owner, name, number, sha, activeFilePath): Promise<void>; close(): void; navigateTo(path): void; isOpen: boolean; path: string | null; entriesByDir: Map<string, TreeEntry[]>; status; error }`, registered in `Provider.svelte`'s store map under key `codeBrowser`.

- [ ] **Step 1: Write the failing test**

`packages/ui/src/stores/codeBrowser.svelte.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { createCodeBrowserStore } from "./codeBrowser.svelte.js";
import { stubClient } from "../testing/stubClient.js"; // match the real helper's import path used by symbolRefs.svelte.test.ts

describe("codeBrowser store", () => {
  it("seeds path from the active diff file on first open", async () => {
    const client = stubClient();
    client.GET.mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hello", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.path).toBe("src/active.txt");
    expect(store.isOpen).toBe(true);
  });

  it("uses the stored bookmark over the active file when one exists", async () => {
    const client = stubClient();
    client.GET.mockImplementation(async (path: string) => {
      if (path.includes("code-browser-state")) return { data: { path: "src/bookmarked.txt" } };
      if (path.includes("/tree")) return { data: { path: "", entries: [] } };
      if (path.includes("/blob")) return { data: { content: "hi", truncated: false } };
      throw new Error(`unexpected GET ${path}`);
    });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");

    expect(store.path).toBe("src/bookmarked.txt");
  });

  it("navigateTo persists the new path via PUT", async () => {
    const client = stubClient();
    client.GET.mockResolvedValue({ data: { path: "" } });
    client.PUT.mockResolvedValue({ data: { path: "src/new.txt" } });

    const store = createCodeBrowserStore({ client });
    await store.open("acme", "widget", 1, "deadbeef", "src/active.txt");
    await store.navigateTo("src/new.txt");

    expect(store.path).toBe("src/new.txt");
    expect(client.PUT).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      expect.objectContaining({ body: { path: "src/new.txt" } }),
    );
  });
});
```

Check `stubClient()`'s real module path and export name before importing it — the research noted it currently exposes `GET`/`POST`/`DELETE` but no `PUT`; add a `PUT: vi.fn()` key to that shared helper as part of this step rather than creating a second stub helper.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/ui && bun run test -- codeBrowser`
Expected: FAIL — module doesn't exist.

- [ ] **Step 3: Write the implementation**

`packages/ui/src/stores/codeBrowser.svelte.ts`:
```ts
export interface TreeEntry {
  name: string;
  path: string;
  type: "dir" | "file";
}

export interface CodeBrowserDeps {
  client: {
    GET: (path: string, opts: unknown) => Promise<{ data?: unknown; error?: unknown }>;
    PUT: (path: string, opts: unknown) => Promise<{ data?: unknown; error?: unknown }>;
  };
}

export function createCodeBrowserStore({ client }: CodeBrowserDeps) {
  let isOpen = $state(false);
  let path = $state<string | null>(null);
  let sha = $state<string | null>(null);
  let owner = "";
  let name = "";
  let number = 0;
  let entriesByDir = $state<Map<string, TreeEntry[]>>(new Map());
  let content = $state<string | null>(null);
  let status = $state<"idle" | "loading" | "ready" | "error" | "missing">("idle");
  let error = $state<string | null>(null);

  async function loadTree(dirPath: string) {
    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/tree",
      { params: { path: { owner, name, number }, query: { sha, path: dirPath } } },
    );
    if (err || !data) return;
    entriesByDir.set(dirPath, (data as { entries: TreeEntry[] }).entries);
    entriesByDir = new Map(entriesByDir);
  }

  async function loadFile(filePath: string) {
    status = "loading";
    const { data, error: err } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/blob",
      { params: { path: { owner, name, number }, query: { sha, path: filePath } } },
    );
    if (err || !data) {
      status = "missing";
      content = null;
      return;
    }
    content = (data as { content: string }).content;
    status = "ready";
  }

  async function open(o: string, n: string, num: number, s: string, activeFilePath: string) {
    owner = o; name = n; number = num; sha = s;
    isOpen = true;

    const { data } = await client.GET(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      { params: { path: { owner, name, number } } },
    );
    const bookmarked = (data as { path?: string } | undefined)?.path;
    path = bookmarked && bookmarked !== "" ? bookmarked : activeFilePath;

    await loadTree("");
    if (path) await loadFile(path);
  }

  function close() {
    isOpen = false;
  }

  async function navigateTo(newPath: string) {
    path = newPath;
    await client.PUT(
      "/repos/{owner}/{name}/pulls/{number}/code-browser-state",
      { params: { path: { owner, name, number } }, body: { path: newPath } },
    );
    await loadFile(newPath);
  }

  return {
    get isOpen() { return isOpen; },
    get path() { return path; },
    get entriesByDir() { return entriesByDir; },
    get content() { return content; },
    get status() { return status; },
    get error() { return error; },
    open,
    close,
    navigateTo,
    loadTree,
  };
}

export type CodeBrowserStore = ReturnType<typeof createCodeBrowserStore>;
```

Before finalizing, re-check `symbolRefs.svelte.ts:165-167`'s exact `client.GET` call shape (params nesting, how `sha`/query params are typed) and match it precisely — the sketch above may not match the generated client's real parameter typing, since generated types are strict; if TypeScript errors appear on `params.query`/`params.path` shapes, follow the generated schema's actual type rather than adjusting types loosely.

Register the store in `Provider.svelte`, following the exact three-step pattern used for `symbolRefs` (`Provider.svelte:92,226,289`): import `createCodeBrowserStore`, instantiate it as `createCodeBrowserStore({ client: cl })`, add it to the store map under key `codeBrowser`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd packages/ui && bun run test -- codeBrowser`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/stores/codeBrowser.svelte.ts \
        packages/ui/src/stores/codeBrowser.svelte.test.ts \
        packages/ui/src/testing/stubClient.ts \
        packages/ui/src/components/Provider.svelte
git commit -m "feat(ui): add codeBrowser store for panel state and bookmark sync"
```

---

## Task 7: `CodeBrowserPanel.svelte` — tree pane and layout shell

**Files:**
- Create: `packages/ui/src/components/diff/CodeBrowserPanel.svelte`
- Create: `packages/ui/src/components/diff/CodeBrowserPanel.test.ts`

**Interfaces:**
- Consumes: `getStores().codeBrowser` (Task 6), `getClient()` (same `../../context.js` pattern as `ReviewPanel.svelte:1-14`).
- Produces: `<CodeBrowserPanel owner name number sha initialPath onclose />`, rendering a full-width-from-right overlay with a lazy-loaded directory tree in a left column.

- [ ] **Step 1: Write the failing test**

`packages/ui/src/components/diff/CodeBrowserPanel.test.ts`, following `SymbolRefsGutter.test.ts`'s `@testing-library/svelte` + manual `STORES_KEY` context + `vi.mock()` pattern:
```ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, it, expect, vi } from "vitest";
import CodeBrowserPanel from "./CodeBrowserPanel.svelte";
// mirror the exact STORES_KEY / context setup from SymbolRefsGutter.test.ts here

describe("CodeBrowserPanel", () => {
  it("renders the root tree entries", async () => {
    // Arrange a codeBrowser store double with entriesByDir set for "" to a
    // known list of TreeEntry, matching whatever context-injection approach
    // SymbolRefsGutter.test.ts actually uses.
    render(CodeBrowserPanel, {
      props: { owner: "acme", name: "widget", number: 1, sha: "deadbeef", initialPath: "src/a.txt", onclose: vi.fn() },
    });
    expect(await screen.findByText("src")).toBeInTheDocument();
  });

  it("calls onclose when the close control is activated", async () => {
    const onclose = vi.fn();
    render(CodeBrowserPanel, {
      props: { owner: "acme", name: "widget", number: 1, sha: "deadbeef", initialPath: "src/a.txt", onclose },
    });
    await fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(onclose).toHaveBeenCalled();
  });
});
```

Copy `SymbolRefsGutter.test.ts:1-7`'s exact context/mock setup before filling in the arrange step above — this plan intentionally leaves that block as a instruction rather than fabricated code, since it depends on details (mock module paths) not independently re-verified for this task.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/ui && bun run test -- CodeBrowserPanel`
Expected: FAIL — component doesn't exist.

- [ ] **Step 3: Write the implementation**

`packages/ui/src/components/diff/CodeBrowserPanel.svelte`:
```svelte
<script lang="ts">
  import { getClient, getStores } from "../../context.js";

  let { owner, name, number, sha, initialPath, onclose }: {
    owner: string;
    name: string;
    number: number;
    sha: string;
    initialPath: string;
    onclose: () => void;
  } = $props();

  const stores = getStores();
  const browser = stores.codeBrowser;

  $effect(() => {
    browser.open(owner, name, number, sha, initialPath);
  });

  function toggleDir(path: string) {
    if (!browser.entriesByDir.has(path)) {
      browser.loadTree(path);
    }
  }
</script>

<div class="code-browser-overlay">
  <div class="code-browser-panel">
    <div class="code-browser-header">
      <span class="code-browser-title">{browser.path ?? "Browse files"}</span>
      <button type="button" aria-label="Close code browser" onclick={onclose}>Close</button>
    </div>
    <div class="code-browser-body">
      <nav class="code-browser-tree">
        {#each browser.entriesByDir.get("") ?? [] as entry (entry.path)}
          <button
            type="button"
            class="code-browser-entry code-browser-entry--{entry.type}"
            onclick={() => (entry.type === "dir" ? toggleDir(entry.path) : browser.navigateTo(entry.path))}
          >
            {entry.name}
          </button>
        {/each}
      </nav>
      <div class="code-browser-content">
        {#if browser.status === "missing"}
          <p class="code-browser-empty">This file doesn't exist at this commit.</p>
        {:else if browser.status === "loading"}
          <p class="code-browser-empty">Loading…</p>
        {:else}
          <pre class="code-browser-file">{browser.content ?? ""}</pre>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .code-browser-overlay {
    position: absolute;
    inset: 0;
    z-index: 60;
    display: flex;
    justify-content: flex-end;
    background: var(--overlay-scrim, rgba(0, 0, 0, 0.35));
  }
  .code-browser-panel {
    width: 100%;
    max-width: 100%;
    height: 100%;
    background: var(--diff-bg);
    display: flex;
    flex-direction: column;
    box-shadow: -2px 0 12px rgba(0, 0, 0, 0.2);
  }
  .code-browser-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 8px 12px;
    border-bottom: 1px solid var(--diff-border, #d0d7de);
  }
  .code-browser-body {
    flex: 1;
    display: flex;
    overflow: hidden;
  }
  .code-browser-tree {
    width: 260px;
    overflow-y: auto;
    border-right: 1px solid var(--diff-border, #d0d7de);
    display: flex;
    flex-direction: column;
  }
  .code-browser-entry {
    text-align: left;
    padding: 4px 12px;
    background: none;
    border: none;
    cursor: pointer;
  }
  .code-browser-content {
    flex: 1;
    overflow: auto;
    padding: 8px 12px;
  }
  .code-browser-file {
    font-family: var(--mono-font, monospace);
    white-space: pre;
  }
</style>
```

Check `DiffFile`/`DiffFile.svelte`'s stylesheet for the real CSS variable names (`--diff-bg`, `--diff-border`, `--mono-font`) — the spec's fact list only confirms `--diff-bg`/`--diff-stale-bg` exist; verify `--diff-border` and any monospace-font token actually exist under those names before using them, substituting the real names if different.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd packages/ui && bun run test -- CodeBrowserPanel`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/components/diff/CodeBrowserPanel.svelte \
        packages/ui/src/components/diff/CodeBrowserPanel.test.ts
git commit -m "feat(ui): add CodeBrowserPanel shell with lazy directory tree"
```

---

## Task 8: Wire per-line Shiki highlighting into the file viewer

**Files:**
- Modify: `packages/ui/src/components/diff/CodeBrowserPanel.svelte`
- Modify: `packages/ui/src/components/diff/CodeBrowserPanel.test.ts`

**Interfaces:**
- Consumes: `langFromPath(path): string | undefined` and `tokenizeLineDual(code, lang): Promise<DualToken[]>` (`packages/ui/src/utils/highlight.ts:47-52,64`).

- [ ] **Step 1: Write the failing test**

Add to `CodeBrowserPanel.test.ts`:
```ts
it("renders highlighted tokens instead of raw <pre> text for a known language", async () => {
  // Arrange the codeBrowser store double so browser.path = "src/a.ts" and
  // browser.content = "const x = 1;", matching whatever context-injection
  // approach the earlier tests in this file already established.
  render(CodeBrowserPanel, {
    props: { owner: "acme", name: "widget", number: 1, sha: "deadbeef", initialPath: "src/a.ts", onclose: vi.fn() },
  });
  expect(await screen.findByText("const")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/ui && bun run test -- CodeBrowserPanel`
Expected: FAIL — content still renders as one plain `<pre>` block with no per-token markup, so `screen.findByText("const")` either fails or the assertion needs adjusting to detect a `<span>` token vs. the whole line; if this ambiguity blocks a clean RED/GREEN cycle, replace the assertion with a check for a `data-shiki-token` (or whatever attribute Step 3 emits) attribute's presence instead of text content.

- [ ] **Step 3: Implement per-line batched tokenizing**

Replace the `<pre class="code-browser-file">` block in `CodeBrowserPanel.svelte` with per-line rendering, following `DiffFile.svelte:15,682`'s existing batching pattern exactly — read that code before writing this step, since the real batching constant (`BATCH_SIZE`) and the `requestAnimationFrame`-yield loop must be copied, not reinvented:

```svelte
<script lang="ts">
  import { langFromPath, tokenizeLineDual } from "../../utils/highlight.js";
  // ...existing imports/props/state from Task 7...

  let lines = $state<string[]>([]);
  let tokens = $state<Map<number, unknown>>(new Map());

  $effect(() => {
    if (browser.content == null) {
      lines = [];
      tokens = new Map();
      return;
    }
    lines = browser.content.split("\n");
    const lang = langFromPath(browser.path ?? "");
    tokens = new Map();
    void tokenizeAllLines(lines, lang);
  });

  async function tokenizeAllLines(currentLines: string[], lang: string | undefined) {
    // Mirror DiffFile.svelte's BATCH_SIZE + requestAnimationFrame batching
    // loop here verbatim (same constant, same yield mechanism), tokenizing
    // currentLines[i] via tokenizeLineDual(currentLines[i], lang) and
    // writing into a fresh `tokens` Map per batch, exactly as DiffFile does
    // for diff lines. Bail out early if `currentLines` no longer matches
    // `lines` (the user navigated to a different file mid-tokenize).
  }
</script>

<!-- replace the old <pre> block with: -->
<div class="code-browser-file">
  {#each lines as line, i (i)}
    <div class="code-browser-line">
      <!-- render tokens.get(i) the same way DiffLine.svelte renders its
           per-line dual-theme tokens; copy that rendering markup rather
           than inventing new span/theme-variable logic here. -->
      {#if tokens.has(i)}
        <!-- token spans go here, matching DiffLine's approach -->
      {:else}
        <span>{line}</span>
      {/if}
    </div>
  {/each}
</div>
```

This step is intentionally left with two explicit copy-from-`DiffFile`/`DiffLine` instructions rather than fabricated token-rendering markup: the exact `BATCH_SIZE` value, the yield mechanism, and the dual-theme span structure were reported by research as existing but were not quoted verbatim, so invented markup here would likely diverge from the real theming (light/dark CSS variable names, span nesting) in a way that would silently break dark mode. Read `DiffFile.svelte` and `DiffLine.svelte` in full before completing this step, and copy the tokenizing loop and the per-token render block as closely as the different data shape (whole-file lines vs. diff hunks) allows.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd packages/ui && bun run test -- CodeBrowserPanel`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/components/diff/CodeBrowserPanel.svelte \
        packages/ui/src/components/diff/CodeBrowserPanel.test.ts
git commit -m "feat(ui): syntax-highlight code browser file content via Shiki"
```

---

## Task 9: Toolbar button, keyboard shortcut, and panel wiring in `DiffView`

**Files:**
- Modify: `packages/ui/src/components/diff/DiffToolbar.svelte`
- Modify: `packages/ui/src/components/diff/DiffView.svelte`
- Modify: any existing `DiffToolbar.test.ts`/`DiffView.test.ts` (add cases; do not replace existing ones)

**Interfaces:**
- Consumes: `reviewPanelOpen` pattern (`DiffView.svelte:30,341,381-382`), `handleKeydown` (`:162-217`), `diffStore.getActiveFile()` (`diff.svelte.ts:323-325`), `codeBrowser` store (Task 6), `CodeBrowserPanel` (Tasks 7-8).
- Produces: a `b` shortcut and a toolbar button that both open the panel seeded from the active file; `[`/`]` while the panel is open keeps it showing the same bookmarked path at the new SHA.

- [ ] **Step 1: Write the failing tests**

Add to `DiffToolbar.test.ts` (mirror whatever existing test covers `onRefsClick`):
```ts
it("calls onBrowseClick when the Browse button is clicked", async () => {
  const onBrowseClick = vi.fn();
  render(DiffToolbar, { props: { /* existing required props */, onBrowseClick } });
  await fireEvent.click(screen.getByRole("button", { name: /browse/i }));
  expect(onBrowseClick).toHaveBeenCalled();
});
```

Add to `DiffView.test.ts` (mirror whatever existing test covers the `s` shortcut and `reviewPanelOpen`):
```ts
it("opens the code browser panel on 'b' when a SHA is resolved", async () => {
  // render DiffView with a resolved currentSha, matching the existing 's'
  // shortcut test's setup exactly.
  await fireEvent.keyDown(window, { key: "b" });
  expect(screen.getByText(/close code browser/i)).toBeInTheDocument(); // or equivalent panel-open assertion
});

it("does not open the code browser panel on 'b' when no SHA is resolved", async () => {
  // render DiffView with currentSha === "", matching the existing 's'
  // shortcut's negative-case setup.
  await fireEvent.keyDown(window, { key: "b" });
  expect(screen.queryByText(/close code browser/i)).not.toBeInTheDocument();
});
```

Copy the existing `s`-shortcut tests' exact setup/props (not sketched above) before writing these — this plan is intentionally not fabricating that scaffolding, since guessing it wrong would produce tests that pass for the wrong reason.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/ui && bun run test -- DiffToolbar DiffView`
Expected: FAIL — no `onBrowseClick` prop/button, no `b` handling, no panel state.

- [ ] **Step 3: Implement**

In `DiffToolbar.svelte`, add the prop next to the existing ones (`:5-9`):
```ts
let { onReviewClick, onRefsClick, onBrowseClick, ...rest } = $props();
```
and a button next to the Refs button (`:120-131` pattern):
```svelte
<button type="button" class="refresh-btn" onclick={onBrowseClick} title="Browse files at this commit">
  Browse
</button>
```

In `DiffView.svelte`, add state next to `reviewPanelOpen` (`:30`):
```ts
let codeBrowserOpen = $state(false);
```

Add a new `if` block inside `handleKeydown` (`:162-217`), after the existing `s` block, following its exact gating:
```ts
if (e.key === "b" && currentSha !== "") {
  codeBrowserOpen = true;
  return;
}
```

Wire the toolbar callback (same call site pattern as `onReviewClick` at `:341`):
```svelte
<DiffToolbar
  ...
  onBrowseClick={() => { if (currentSha !== "") codeBrowserOpen = true; }}
/>
```

Render the panel (same conditional pattern as the review panel at `:381-382`):
```svelte
{#if codeBrowserOpen}
  <CodeBrowserPanel
    {owner}
    {name}
    {number}
    sha={currentSha}
    initialPath={diffStore.getActiveFile() ?? ""}
    onclose={() => (codeBrowserOpen = false)}
  />
{/if}
```

`owner`/`name`/`number` must be read from whatever props/store `DiffView.svelte` already uses to build other PR-scoped API calls (check how `reviewPanelOpen`'s `ReviewPanel` instantiation gets its own `owner`/`name`/`number` a few lines below `:381-382` and copy that source exactly) rather than assuming they're bare local variables.

Since the panel is keyed by `sha={currentSha}`, stepping commits via the existing `[`/`]` handlers naturally re-renders `CodeBrowserPanel` with a new `sha` prop while it stays mounted; confirm the `$effect` in `CodeBrowserPanel` (Task 7) re-runs `browser.open(...)` when `sha` changes — if Svelte's `$effect` dependency tracking doesn't pick up the prop change because `open()`'s body doesn't read `sha` reactively at the top of the effect, add `sha` to an explicit reactive read at the top of the effect so it re-triggers.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd packages/ui && bun run test -- DiffToolbar DiffView`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/components/diff/DiffToolbar.svelte \
        packages/ui/src/components/diff/DiffToolbar.test.ts \
        packages/ui/src/components/diff/DiffView.svelte \
        packages/ui/src/components/diff/DiffView.test.ts
git commit -m "feat(ui): trigger the code browser panel from the toolbar and 'b' shortcut"
```

---

## Task 10: Symbol-refs jump-in

**Files:**
- Modify: `packages/ui/src/components/diff/SymbolRefsGutter.svelte`
- Modify: existing `SymbolRefsGutter.test.ts`

**Interfaces:**
- Consumes: `codeBrowser` store's `open`/`navigateTo` (Task 6), a hit's `path`/`line` fields (`SymbolHit` shape from the symbol-refs spec: `{ path, line, text, kind }`).
- Produces: a secondary action per row that opens `CodeBrowserPanel` at that hit's path, in addition to the existing in-diff reveal-and-jump behavior (per the design spec, this is additive, not a replacement — the existing row click keeps its current behavior).

- [ ] **Step 1: Write the failing test**

Add to `SymbolRefsGutter.test.ts`, following its existing render/mock setup:
```ts
it("opens the code browser panel at the hit's path when its browse action is clicked", async () => {
  // Arrange the same store doubles this file's existing tests already use,
  // plus a codeBrowser store double whose `open` is a vi.fn().
  await fireEvent.click(screen.getByRole("button", { name: /browse/i, hidden: true }));
  // assert the codeBrowser double's open() was called with the hit's owner/name/number/sha/path
});
```

Fill in the arrange/assert details from this file's real existing mock scaffolding before running — left as an instruction here since that scaffolding wasn't independently re-verified for this task.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd packages/ui && bun run test -- SymbolRefsGutter`
Expected: FAIL — no browse action exists on a row yet.

- [ ] **Step 3: Implement**

In `SymbolRefsGutter.svelte`, add a per-row secondary button next to the existing row click target (find the row template — likely a `<button>` or `<div onclick>` per hit) that calls:
```ts
function browseHit(hit: SymbolHit) {
  stores.codeBrowser.open(owner, name, number, sha, hit.path);
  // then, once open, navigate to hit.path if open() didn't already seed it there
}
```
Reuse whatever `owner`/`name`/`number`/`sha` values the row's existing click handler already closes over (the reveal-and-jump mechanism needs the same PR/SHA context) rather than threading new props.

Also render `<CodeBrowserPanel>` conditionally wherever `codeBrowser.isOpen` is true, if `DiffView.svelte`'s own panel instance (Task 9) doesn't already cover this — check whether `SymbolRefsGutter` lives inside `DiffView`'s subtree (per the spec, the gutter is a column inside `DiffView`) and if so, rely on `DiffView`'s existing `{#if codeBrowserOpen}<CodeBrowserPanel .../>{/if}` block by setting `codeBrowserOpen = true` via a callback prop passed down to `SymbolRefsGutter`, rather than mounting a second `CodeBrowserPanel` instance.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd packages/ui && bun run test -- SymbolRefsGutter`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/components/diff/SymbolRefsGutter.svelte \
        packages/ui/src/components/diff/SymbolRefsGutter.test.ts
git commit -m "feat(ui): open the code browser panel from a symbol-refs hit"
```

---

## Task 11: Playwright e2e

**Files:**
- Create: `frontend/tests/e2e-full/code-browser.spec.ts`

**Interfaces:**
- Consumes: `acquireExclusiveLock` and the shared e2e fixture server/repo (same as `symbol-refs.spec.ts`).

- [ ] **Step 1: Write the e2e spec**

`frontend/tests/e2e-full/code-browser.spec.ts`, structured like `symbol-refs.spec.ts`:
```ts
import { test, expect } from "@playwright/test";
import { acquireExclusiveLock } from "./helpers/lock.js"; // match the real import path used by symbol-refs.spec.ts

test.describe("code browser panel", () => {
  test("opens via toolbar, browses, opens a file, and survives a commit step", async ({ page }) => {
    await acquireExclusiveLock(async () => {
      // Navigate to the fixture PR's diff view, same setup as symbol-refs.spec.ts.

      await page.getByRole("button", { name: /browse/i }).click();
      await expect(page.locator(".code-browser-panel")).toBeVisible();

      // Expand into a subdirectory known to exist in the fixture repo, open a
      // file, and wait for tokenized spans the same way symbol-refs.spec.ts
      // waits on Shiki output rather than a fixed sleep.
      await page.getByRole("button", { name: "src" }).click();
      await page.getByRole("button", { name: /\.ts$/ }).first().click();
      await expect(page.locator(".code-browser-file")).toBeVisible();

      const openedPath = await page.locator(".code-browser-title").textContent();

      await page.keyboard.press("]"); // step to the next commit
      await expect(page.locator(".code-browser-title")).toHaveText(openedPath ?? "");

      await page.keyboard.press("Escape"); // or the real close mechanism, matching onclose wiring
      await expect(page.locator(".code-browser-panel")).not.toBeVisible();

      await page.getByRole("button", { name: /browse/i }).click();
      await expect(page.locator(".code-browser-title")).toHaveText(openedPath ?? "");
    });
  });
});
```

Before finalizing, read `symbol-refs.spec.ts` in full and replace every "match the real X" instruction above with the actual import path, selector convention, and fixture repo file names it uses — do not run this spec with fabricated selectors.

- [ ] **Step 2: Run the spec**

Run: `cd frontend && bun run test:e2e-full -- code-browser`
Expected: PASS once selectors are corrected to match the real fixture repo's file tree and the real close-panel control.

- [ ] **Step 3: Commit**

```bash
git add frontend/tests/e2e-full/code-browser.spec.ts
git commit -m "test(e2e): cover opening, browsing, and persisting the code browser panel"
```

---

## Out of scope (per spec)

- Search UI inside the panel beyond symbol-refs jump-in.
- Highlighting languages the diff viewer doesn't already support.
- Editing file content from the panel.
- Recursive/eager full-tree fetch or client-side tree caching across PRs.
- Deciding whether a symbol-refs row click should open this panel *instead of* the current in-diff reveal, rather than *alongside* it — Task 10 implements "alongside," per the spec's explicit deferral of that product decision.
