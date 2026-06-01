# Commit-picker branch-head markers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the commit picker, mark any commit that is the tip of another local branch with a small icon whose hover tooltip lists the branch name(s), scoped to the local-worktree review flow.

**Architecture:** A new `worktrees.BranchHeads` resolver runs one `git for-each-ref refs/heads` and returns `fullSHA -> sorted branch names` (excluding the worktree's own branch). `getCommitsLocal` attaches those names to each commit's new `branch_heads` field (cosmetic, non-fatal on error). The Svelte `CommitListItem` renders an SVG git-branch marker (with a count badge for 2+) right after the SHA, using a native `title` tooltip.

**Tech Stack:** Go (stdlib `os/exec` via the worktrees package's `gitCmd`), Huma + oapi-codegen (`make api-generate`), Svelte 5 + Vitest + `@testing-library/svelte`.

Spec: `docs/superpowers/specs/2026-05-29-commit-picker-branch-heads-design.md`
Mockup: `docs/superpowers/specs/2026-05-29-commit-picker-branch-heads-mockup.html`

---

## File Structure

- **Create** `internal/worktrees/branches.go` — `BranchHeads` resolver (one responsibility: map local branch tips to names).
- **Create** `internal/worktrees/branches_test.go` — unit tests for the resolver.
- **Modify** `internal/server/api_types.go:151` — add `BranchHeads` to `commitResponse`.
- **Regenerate** (via `make api-generate`) — `internal/apiclient/generated/client.gen.go`, `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `packages/ui/src/api/generated/schema.ts`, `packages/ui/src/api/generated/client.ts`.
- **Modify** `packages/ui/src/api/types.ts:110` — add `branch_heads?` to the hand-written `CommitInfo`.
- **Modify** `internal/server/local_dispatch.go` — wire `BranchHeads` into `getCommitsLocal`; add `log/slog` import.
- **Modify** `internal/server/worktrees_e2e_test.go` — append the local-commits e2e test.
- **Modify** `packages/ui/src/components/diff/CommitListItem.svelte` — marker markup + CSS.
- **Create** `packages/ui/src/components/diff/CommitListItem.test.ts` — component tests.

---

## Task 1: `BranchHeads` resolver (worktrees package)

**Files:**
- Create: `internal/worktrees/branches.go`
- Test: `internal/worktrees/branches_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/worktrees/branches_test.go`:

```go
package worktrees

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitHeadT(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestBranchHeads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "c1")
	c1 := gitHeadT(t, dir)
	runGitT(t, dir, "commit", "--allow-empty", "-m", "c2")
	c2 := gitHeadT(t, dir)

	// Two branches point at c1; one at c2. main (current) is at c2.
	runGitT(t, dir, "branch", "feat/a", c1)
	runGitT(t, dir, "branch", "feat/b", c1)
	runGitT(t, dir, "branch", "release", c2)

	heads, err := BranchHeads(ctx, dir, "main")
	require.NoError(err)
	assert.Equal([]string{"feat/a", "feat/b"}, heads[c1])
	// 'main' also points at c2 but is excluded; only 'release' remains.
	assert.Equal([]string{"release"}, heads[c2])
}

func TestBranchHeadsEmptyExcludeKeepsAll(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	dir := t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	runGitT(t, dir, "commit", "--allow-empty", "-m", "c1")
	c1 := gitHeadT(t, dir)

	// excludeBranch == "" (e.g. detached worktree) keeps every branch.
	heads, err := BranchHeads(ctx, dir, "")
	require.NoError(err)
	assert.Equal([]string{"main"}, heads[c1])
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/worktrees -run TestBranchHeads -shuffle=on`
Expected: FAIL to compile — `undefined: BranchHeads`.

- [ ] **Step 3: Write the implementation**

Create `internal/worktrees/branches.go`:

```go
package worktrees

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// BranchHeads maps each local branch tip, keyed by full commit SHA, to
// the names of the branches that point at it. It enumerates every
// local branch in the repository — refs/heads are shared across all of
// a repo's worktrees — so the result covers branches checked out in
// sibling worktrees too.
//
// excludeBranch is dropped from the result; callers pass the worktree's
// own current branch so a commit is only attributed to *other*
// branches. An empty excludeBranch (e.g. a detached worktree) excludes
// nothing. Branch names for a given SHA are sorted for stable
// presentation. A non-nil (possibly empty) map is returned on success.
func BranchHeads(ctx context.Context, worktreePath, excludeBranch string) (map[string][]string, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath is required")
	}
	out, err := gitCmd(ctx, worktreePath,
		"for-each-ref", "--format=%(objectname) %(refname:short)", "refs/heads",
	)
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	heads := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// refnames cannot contain spaces, so the first space splits
		// the SHA from the (possibly slash-bearing) branch name.
		sha, name, ok := strings.Cut(line, " ")
		if !ok || name == "" || name == excludeBranch {
			continue
		}
		heads[sha] = append(heads[sha], name)
	}
	for sha := range heads {
		sort.Strings(heads[sha])
	}
	return heads, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/worktrees -run TestBranchHeads -shuffle=on`
Expected: PASS (both `TestBranchHeads` and `TestBranchHeadsEmptyExcludeKeepsAll`).

- [ ] **Step 5: Commit**

```bash
git add internal/worktrees/branches.go internal/worktrees/branches_test.go
git commit -m "feat(worktrees): resolve local branch tips by commit SHA" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: API contract — `branch_heads` field + regenerate clients

**Files:**
- Modify: `internal/server/api_types.go:151-157`
- Modify: `packages/ui/src/api/types.ts:110-116`
- Regenerate: Go client, OpenAPI specs, TS schema (via `make api-generate`)

- [ ] **Step 1: Add the field to `commitResponse`**

In `internal/server/api_types.go`, the `commitResponse` struct (lines 151-157) becomes:

```go
type commitResponse struct {
	SHA         string    `json:"sha"              doc:"Full commit SHA"`
	Message     string    `json:"message"          doc:"Subject (first line) of commit message"`
	Body        string    `json:"body,omitempty"   doc:"Commit message body (trimmed, empty when the message has no body)"`
	AuthorName  string    `json:"author_name"      doc:"Commit author display name"`
	AuthoredAt  time.Time `json:"authored_at"      doc:"Commit author date (RFC3339)"`
	BranchHeads []string  `json:"branch_heads,omitempty" doc:"Names of other local branches whose tip is this commit (local worktree review only)"`
}
```

- [ ] **Step 2: Add the field to the hand-written TS type**

In `packages/ui/src/api/types.ts`, the `CommitInfo` interface (lines 110-116) becomes:

```ts
export interface CommitInfo {
  sha: string;
  message: string;
  body?: string;
  author_name: string;
  authored_at: string;
  branch_heads?: string[];
}
```

- [ ] **Step 3: Regenerate the OpenAPI specs and clients**

Run: `make api-generate`
Expected: regenerates `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts`, and `packages/ui/src/api/generated/client.ts`.

- [ ] **Step 4: Verify regeneration and that everything compiles**

Run: `grep -n "BranchHeads" internal/apiclient/generated/client.gen.go && go build ./...`
Expected: a `BranchHeads` field appears on `CommitResponse` (mirroring `Body *string`, it will be `BranchHeads *[]string`), and the build succeeds.

> Note for Task 3: if the generator emits non-pointer `BranchHeads []string`, adjust the Task 3 assertions (drop the `*` deref; use `assert.Empty` instead of `assert.Nil`).

- [ ] **Step 5: Commit**

```bash
git add internal/server/api_types.go packages/ui/src/api/types.ts \
  frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
  internal/apiclient/generated/client.gen.go \
  packages/ui/src/api/generated/schema.ts packages/ui/src/api/generated/client.ts
git commit -m "feat(api): add branch_heads to the commit response shape" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Wire `BranchHeads` into the local commits path (+ e2e)

**Files:**
- Test: `internal/server/worktrees_e2e_test.go` (append a test)
- Modify: `internal/server/local_dispatch.go:241-287` (and the import block)

- [ ] **Step 1: Write the failing e2e test**

Append to `internal/server/worktrees_e2e_test.go`. The file already imports `context`, `os`, `os/exec`, `path/filepath`, `testing`, `Assert`, `require`, `db`, and `generated`; **add `"strings"` to the stdlib import group** (the test below uses `strings.TrimSpace`):

```go
func TestAPILocalCommitsIncludeBranchHeads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()

	dir := t.TempDir()
	canonDir, err := filepath.EvalSymlinks(dir)
	require.NoError(err)

	// Base commit on main, published to a bare origin so ResolveBase
	// finds origin/main and the feature commits fall in range.
	runGitWT(t, "", "init", "--initial-branch=main", dir)
	runGitWT(t, dir, "config", "user.email", "test@example.com")
	runGitWT(t, dir, "config", "user.name", "Test")
	require.NoError(os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644))
	runGitWT(t, dir, "add", "base.txt")
	runGitWT(t, dir, "commit", "-m", "base")
	originDir := dir + "-origin.git"
	runGitWT(t, "", "init", "--bare", originDir)
	runGitWT(t, dir, "remote", "add", "origin", originDir)
	runGitWT(t, dir, "push", "origin", "main")
	runGitWT(t, dir, "fetch", "origin")

	// Worktree branch 'feature', two commits ahead of origin/main.
	runGitWT(t, dir, "checkout", "-b", "feature")
	require.NoError(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644))
	runGitWT(t, dir, "add", "a.txt")
	runGitWT(t, dir, "commit", "-m", "add a")
	midOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	midSHA := strings.TrimSpace(string(midOut))
	require.NoError(os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644))
	runGitWT(t, dir, "add", "b.txt")
	runGitWT(t, dir, "commit", "-m", "add b")
	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(err)
	headSHA := strings.TrimSpace(string(headOut))

	// Two more local branches point at the middle commit. The current
	// branch ('feature', at HEAD) must not be attributed to its own tip.
	runGitWT(t, dir, "branch", "stack/part-1", midSHA)
	runGitWT(t, dir, "branch", "spike", midSHA)

	repoID, err := database.UpsertLocalRepo(ctx, "demo")
	require.NoError(err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path:    canonDir,
		Branch:  "feature",
		HeadSHA: headSHA,
	})
	require.NoError(err)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberCommitsWithResponse(
		ctx, "local", "demo", w.ID,
	)
	require.NoError(err)
	require.Equal(200, resp.StatusCode())
	require.NotNil(resp.JSON200)
	commits := *resp.JSON200.Commits
	require.Len(commits, 2) // feature is two commits ahead of origin/main

	var midHeads, headHeads *[]string
	for i := range commits {
		switch commits[i].Sha {
		case midSHA:
			midHeads = commits[i].BranchHeads
		case headSHA:
			headHeads = commits[i].BranchHeads
		}
	}
	require.NotNil(midHeads)
	assert.Equal([]string{"spike", "stack/part-1"}, *midHeads)
	assert.Nil(headHeads) // 'feature' (current branch) is excluded
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/server -run TestAPILocalCommitsIncludeBranchHeads -shuffle=on`
Expected: FAIL — `midHeads` is nil (`Expected value not to be nil`), because `getCommitsLocal` does not populate `branch_heads` yet.

- [ ] **Step 3: Add the `log/slog` import**

In `internal/server/local_dispatch.go`, add `"log/slog"` to the stdlib import group so it reads:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	"github.com/wesm/middleman/internal/worktrees"
)
```

- [ ] **Step 4: Wire the decoration into `getCommitsLocal`**

In `internal/server/local_dispatch.go`, find the tail of `getCommitsLocal`:

```go
	for _, c := range commits {
		resp.Commits = append(resp.Commits, commitResponse{
			SHA:        c.SHA,
			Message:    c.Message,
			Body:       c.Body,
			AuthorName: c.AuthorName,
			AuthoredAt: c.AuthoredAt.UTC(),
		})
	}
	if resp.Commits == nil {
		resp.Commits = []commitResponse{}
	}
	return &getCommitsOutput{Body: resp}, nil
```

and replace it with:

```go
	for _, c := range commits {
		resp.Commits = append(resp.Commits, commitResponse{
			SHA:        c.SHA,
			Message:    c.Message,
			Body:       c.Body,
			AuthorName: c.AuthorName,
			AuthoredAt: c.AuthoredAt.UTC(),
		})
	}

	// Mark commits that are also the tip of another local branch.
	// Cosmetic only: on error, leave the commits undecorated rather
	// than failing the whole panel.
	if heads, err := worktrees.BranchHeads(ctx, w.Path, w.Branch); err != nil {
		slog.WarnContext(ctx, "resolve branch heads for worktree",
			"path", w.Path, "error", err)
	} else {
		for i := range resp.Commits {
			if names := heads[resp.Commits[i].SHA]; len(names) > 0 {
				resp.Commits[i].BranchHeads = names
			}
		}
	}

	if resp.Commits == nil {
		resp.Commits = []commitResponse{}
	}
	return &getCommitsOutput{Body: resp}, nil
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/server -run TestAPILocalCommitsIncludeBranchHeads -shuffle=on`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/local_dispatch.go internal/server/worktrees_e2e_test.go
git commit -m "feat(server): attach branch_heads to local worktree commits" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Frontend marker in `CommitListItem`

**Files:**
- Test: `packages/ui/src/components/diff/CommitListItem.test.ts` (create)
- Modify: `packages/ui/src/components/diff/CommitListItem.svelte`

- [ ] **Step 1: Write the failing component test**

Create `packages/ui/src/components/diff/CommitListItem.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import CommitListItem from "./CommitListItem.svelte";
import type { CommitInfo } from "../../api/types.js";

function baseCommit(overrides: Partial<CommitInfo> = {}): CommitInfo {
  return {
    sha: "abc1234deadbeef",
    message: "feat: do the thing",
    author_name: "alice",
    authored_at: new Date().toISOString(),
    ...overrides,
  };
}

function renderItem(commit: CommitInfo) {
  return render(CommitListItem, {
    props: { commit, active: false, reviewed: false, onclick: vi.fn() },
  });
}

afterEach(() => cleanup());

describe("CommitListItem branch-head marker", () => {
  it("renders no marker when branch_heads is absent", () => {
    const { container } = renderItem(baseCommit());
    expect(container.querySelector(".commit-item__branches")).toBeNull();
  });

  it("renders the marker with branch names in the title", () => {
    const { container } = renderItem(baseCommit({ branch_heads: ["feat/login"] }));
    const mark = container.querySelector(".commit-item__branches");
    expect(mark).toBeTruthy();
    expect(mark?.getAttribute("title")).toBe("feat/login");
    expect(container.querySelector(".commit-item__branch-count")).toBeNull();
  });

  it("shows a count badge when more than one branch points at the commit", () => {
    const { container } = renderItem(
      baseCommit({ branch_heads: ["selective-sync", "wip/cleanup"] }),
    );
    expect(container.querySelector(".commit-item__branch-count")?.textContent).toBe("2");
    expect(
      container.querySelector(".commit-item__branches")?.getAttribute("title"),
    ).toBe("selective-sync, wip/cleanup");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend && bunx vitest run CommitListItem`
Expected: FAIL — the two positive cases fail because `.commit-item__branches` is null (marker not rendered yet).

- [ ] **Step 3: Add the marker markup**

In `packages/ui/src/components/diff/CommitListItem.svelte`, the button body (lines 34-48) currently reads:

```svelte
  {#if reviewed}
    <span class="commit-item__reviewed" title="Reviewed">&check;</span>
  {/if}
  <span class="commit-item__sha">{commit.sha.slice(0, 7)}</span>
  <span class="commit-item__msg">{commit.message}</span>
  <span class="commit-item__date">{relativeDate(commit.authored_at)}</span>
```

Insert the marker immediately after the SHA span:

```svelte
  {#if reviewed}
    <span class="commit-item__reviewed" title="Reviewed">&check;</span>
  {/if}
  <span class="commit-item__sha">{commit.sha.slice(0, 7)}</span>
  {#if commit.branch_heads && commit.branch_heads.length > 0}
    <span class="commit-item__branches" title={commit.branch_heads.join(", ")}>
      <svg class="commit-item__branch-ic" viewBox="0 0 16 16" aria-hidden="true">
        <path fill="currentColor" d="M9.5 3.25a2.25 2.25 0 1 1 3 2.122V6A2.5 2.5 0 0 1 10 8.5H6a1 1 0 0 0-1 1v1.128a2.251 2.251 0 1 1-1.5 0V5.372a2.25 2.25 0 1 1 1.5 0v1.836A2.493 2.493 0 0 1 6 7h4a1 1 0 0 0 1-1v-.628A2.25 2.25 0 0 1 9.5 3.25Zm-6 0a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Zm8.25-.75a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5ZM4.25 12a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Z" />
      </svg>
      {#if commit.branch_heads.length > 1}
        <span class="commit-item__branch-count">{commit.branch_heads.length}</span>
      {/if}
    </span>
  {/if}
  <span class="commit-item__msg">{commit.message}</span>
  <span class="commit-item__date">{relativeDate(commit.authored_at)}</span>
```

- [ ] **Step 4: Add the marker styles**

In the same file's `<style>` block, add these rules after the `.commit-item__sha` rules (before `.commit-item__msg`):

```css
  .commit-item__branches {
    display: inline-flex;
    align-items: center;
    gap: 1px;
    flex-shrink: 0;
    color: var(--text-muted);
  }

  .commit-item:hover .commit-item__branches,
  .commit-item--active .commit-item__branches {
    color: var(--accent-blue);
  }

  .commit-item__branch-ic {
    width: 11px;
    height: 11px;
    display: block;
  }

  .commit-item__branch-count {
    font-family: var(--font-mono);
    font-size: 9px;
    line-height: 1;
  }
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd frontend && bunx vitest run CommitListItem`
Expected: PASS (all three cases).

- [ ] **Step 6: Commit**

```bash
git add packages/ui/src/components/diff/CommitListItem.svelte packages/ui/src/components/diff/CommitListItem.test.ts
git commit -m "feat(ui): mark commits that are tips of other local branches" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Full verification

**Files:** none (verification gate)

- [ ] **Step 1: Run the Go test suite**

Run: `make test`
Expected: PASS (includes `internal/worktrees` and `internal/server`).

- [ ] **Step 2: Run the frontend tests**

Run: `cd frontend && bun run test`
Expected: PASS (includes the new `CommitListItem` cases).

- [ ] **Step 3: Run frontend type + lint checks**

Run: `make frontend-check`
Expected: no type errors and no lint errors (covers the `branch_heads` type addition and the Svelte change).

- [ ] **Step 4: Run Go lint + vet**

Run: `make vet && make lint`
Expected: clean.

- [ ] **Step 5: Manual smoke (optional but recommended)**

Run `make dev` and `make frontend-dev` in parallel. Open a local worktree that has a couple of side branches whose tips fall inside the reviewed commit range. Confirm: a git-branch icon appears right after the SHA on those commits, hovering shows the branch name(s), commits with 2+ branches show the count badge, and the worktree's own branch never decorates its own HEAD commit. Move a branch in a terminal and refresh — the markers update.

- [ ] **Step 6: Final commit (only if Steps 1-4 produced fixups)**

```bash
git add -A
git commit -m "test: verify commit-picker branch-head markers end to end" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```
