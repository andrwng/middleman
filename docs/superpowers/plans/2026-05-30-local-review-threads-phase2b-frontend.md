# Local Review Threads — Phase 2b (Frontend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the Phase 2a agent backend in the review UI — a submit-time mode picker, per-thread Apply + Apply-all, a left-drawer thread picker, delete for persisted threads (with its backend endpoint), and live status while the agent works — so a reviewer never needs curl.

**Architecture:** Mostly frontend wiring to endpoints that already exist (2a), plus one new backend `DELETE` endpoint. The `reviewThreads` store gains `apply`/`applyAll`/`deleteThread`/`refresh` + a `mode` arg; `ReviewThreadCard` gains Apply/Delete; a new `ReviewThreadsSection` mirrors the drafts list; `ReviewPanel` gains a mode picker; live updates piggyback `worktreeSession`'s running-turn poll (approach 5A) plus the SSE `data_changed` catch-all.

**Tech Stack:** Svelte 5 runes, openapi-fetch generated client (`packages/ui/src/api/generated`), Vitest + @testing-library/svelte (run from `frontend/`), Go + Huma v2 + SQLite for the delete endpoint, testify e2e via the generated Go client.

**Spec:** `docs/superpowers/specs/2026-05-30-local-review-threads-phase2b-design.md`. **Depends on:** Phase 1a/1b/2a (all on branch `serve-local-repo-comments`, stacked on unmerged `branch-navigation`).

---

## Conventions (apply to every task)

- **Branch:** `serve-local-repo-comments`. Commit per task (conventional message) ending with the trailer line:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
  Never `git add -A`/`git add .` (untracked HOME dotfiles must not be committed) — stage explicit paths. No `--no-verify`. No push / PR / merge / branch change.
- **Frontend tests run from `frontend/`** (its vite config has the Svelte plugin and globs `../packages/ui/src/**/*.test.*`): `cd frontend && bunx vitest run [filter]`. Frontend build/typecheck: `cd frontend && bun run build`. Use `bun`, never `npm`.
- **`make api-generate` needs the sandbox DISABLED** (bun temp-dir error) **and** `GOCACHE="$HOME/.cache/go-build"`; then `go generate ./internal/apiclient/generated`. Run those with the Bash tool's `dangerouslyDisableSandbox: true`.
- **The Go `internal/server` suite needs `tmux`, which the sandbox blocks** — run Go server tests with `dangerouslyDisableSandbox: true`. A pre-existing tmux-only `TestWorkspaceDeleteDirty` failure is unrelated.
- Go tests: `-shuffle=on`, never `-v`, never `-count=1`; testify (`require`/`assert`); no `t.Fatal`/`t.Error`/etc.
- **Trust `go build`/`go test`/`bun run build`, NOT the IDE diagnostics panel** — it emits false "undefined"/"not a type"/"cannot range over int"/post-regen-lag errors here.
- No emojis in code or output.

---

### Task 1: Backend — delete endpoint + `DeleteReviewThread` query + regen + e2e

**Files:**
- Modify: `internal/db/queries_review_threads.go`
- Test: `internal/db/queries_review_threads_test.go` (create if absent, else append)
- Modify: `internal/server/huma_routes_review_threads.go`
- Test: `internal/server/review_threads_e2e_test.go` (append)
- Regen artifacts: `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts`

- [ ] **Step 1: Write the failing DB test.** Append to `internal/db/queries_review_threads_test.go` (create the file with `package db` + imports if it doesn't exist). Use the repo's `openTestDB(t)` helper and the existing local-MR seeding pattern (mirror another `queries_review_threads`-area test in the package for how it makes an MR id — typically `UpsertLocalRepo` + `UpsertWorktree` + `ensureSyntheticMRForWorktree` equivalent, or a direct `UpsertMergeRequest`). If unsure of the exact seeding helper, search the package test files for an existing review-thread DB test and reuse its setup verbatim.

```go
func TestDeleteReviewThreadRemovesThreadAndComments(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()

	// Seed a local MR id the threads hang off (reuse the package's
	// existing helper if present; otherwise upsert a local repo +
	// merge request directly as other review-thread tests do).
	mrID := seedLocalMRForThreads(t, d)

	created, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSHA: "abc", Body: "rename this"},
	})
	require.NoError(err)
	require.Len(created, 1)
	id := created[0].ID

	// Add an agent reply so we can confirm comments are removed too.
	_, err = d.AddReviewThreadComment(ctx, id, "agent", "done", nil)
	require.NoError(err)

	require.NoError(d.DeleteReviewThread(ctx, id))

	_, err = d.GetReviewThread(ctx, id)
	require.ErrorIs(err, sql.ErrNoRows)
	comments, err := d.ListReviewThreadComments(ctx, id)
	require.NoError(err)
	require.Empty(comments)
}
```

> `seedLocalMRForThreads` is a placeholder for the package's existing review-thread seeding — find the helper an existing `queries_review_threads`-area test uses (it produces a valid `mr_id`) and call that. If none exists, inline: `repoID,_ := d.UpsertLocalRepo(ctx,"demo"); w,_ := d.UpsertWorktree(ctx, repoID, ScannedWorktree{Path: t.TempDir(), Branch:"f", HeadSHA:"h"})` then `UpsertMergeRequest` for `(repoID, int(w.ID))` and use its id — mirror `ensureSyntheticMRForWorktree` in `internal/server/local_dispatch.go`.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/db -run TestDeleteReviewThread -shuffle=on` → FAIL (`DeleteReviewThread` undefined).

- [ ] **Step 3: Implement `DeleteReviewThread`** in `internal/db/queries_review_threads.go` (add near `SetReviewThreadStatus`):

```go
// DeleteReviewThread permanently removes a thread and its comments in one
// transaction. Comments are deleted explicitly so the call is correct
// regardless of whether the schema declares an ON DELETE CASCADE.
func (d *DB) DeleteReviewThread(ctx context.Context, id int64) error {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_review_thread_comments WHERE thread_id = ?`, id); err != nil {
		return fmt.Errorf("delete comments: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM middleman_review_threads WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete thread: %w", err)
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run the DB test** — `go test ./internal/db -run TestDeleteReviewThread -shuffle=on` → PASS.

- [ ] **Step 5: Add the handler + route** in `internal/server/huma_routes_review_threads.go`. In `registerReviewThreadRoutes`, add:

```go
	huma.Delete(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}", s.deleteReviewThread)
```

And the handler (mirrors the existing action handlers; reuses `reviewThreadActionInput` + `listReviewThreadsOutput`):

```go
func (s *Server) deleteReviewThread(ctx context.Context, input *reviewThreadActionInput) (*listReviewThreadsOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	mrID, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID)
	if err != nil {
		return nil, err
	}
	if err := s.db.DeleteReviewThread(ctx, input.ThreadID); err != nil {
		return nil, huma.Error500InternalServerError("delete thread: " + err.Error())
	}
	threads, err := s.loadReviewThreadsResponse(ctx, mrID)
	if err != nil {
		return nil, huma.Error500InternalServerError("reload review threads: " + err.Error())
	}
	out := &listReviewThreadsOutput{}
	out.Body.Threads = threads
	return out, nil
}
```

- [ ] **Step 6: Regenerate the client** (the new `DELETE` route). Run with `dangerouslyDisableSandbox: true`:

```bash
GOCACHE="$HOME/.cache/go-build" make api-generate
GOCACHE="$HOME/.cache/go-build" go generate ./internal/apiclient/generated
```

- [ ] **Step 7: Write the e2e delete test.** Append to `internal/server/review_threads_e2e_test.go` (it already imports `generated`, `db`, `net/http`, `context`, testify). The generated method name should be `DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse` — confirm it against `internal/apiclient/generated/client.gen.go` after regen and adjust if the codegen named it differently.

```go
func TestAPIReviewThreadDelete(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{
				{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"},
				{Path: "b.go", Side: "RIGHT", Line: 20, CommitSha: "abc", Body: "extract"},
			},
		},
	)
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	created := *createResp.JSON200.Threads
	require.Len(created, 2)
	threadID := created[0].Id

	// Delete one → 200, returns the remaining list (1 thread).
	delResp, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, delResp.StatusCode())
	require.NotNil(delResp.JSON200)
	require.NotNil(delResp.JSON200.Threads)
	require.Len(*delResp.JSON200.Threads, 1)
	require.Equal(created[1].Id, (*delResp.JSON200.Threads)[0].Id)

	// Comments cascaded; deleting again is a 404.
	delAgain, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdWithResponse(
		ctx, "local", "demo", num, threadID,
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, delAgain.StatusCode())
}
```

- [ ] **Step 8: Run the e2e + regression** (UNSANDBOXED for tmux):
`go test ./internal/server -run 'TestAPIReviewThread' -shuffle=on` → PASS; then `go test ./internal/server ./internal/db -shuffle=on` → PASS; `go build ./...` → clean.

- [ ] **Step 9: Commit:**

```bash
git add internal/db/queries_review_threads.go internal/db/queries_review_threads_test.go \
        internal/server/huma_routes_review_threads.go internal/server/review_threads_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go packages/ui/src/api/generated/schema.ts
git commit -m "feat(server): delete endpoint for review threads"
```

---

### Task 2: `reviewThreads` store — `apply`/`applyAll`/`deleteThread`/`refresh` + `mode`

**Files:**
- Modify: `packages/ui/src/stores/reviewThreads.svelte.ts`
- Test: `packages/ui/src/stores/reviewThreads.svelte.test.ts` (append)

> Naming note: the store method is `deleteThread` (not `delete` — `delete` is a reserved word and cannot be a function declaration name). Callers use `reviewThreads.deleteThread(id)`.

- [ ] **Step 1: Write the failing tests.** Append to `reviewThreads.svelte.test.ts`. First extend `stubClient` to include `DELETE`:

```ts
function stubClient(
  over: Partial<Record<"GET" | "POST" | "DELETE", unknown>> = {},
): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined })),
    POST: vi.fn(async () => ({ data: thread(), error: undefined })),
    DELETE: vi.fn(async () => ({ data: { threads: [] }, error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}
```

Then add these tests:

```ts
it("createThreads forwards a mode", async () => {
  const post = vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined }));
  const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
  await store.load("local", "demo", 7);
  await store.createThreads(
    [{ path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "x" }],
    "discuss-first",
  );
  expect(post).toHaveBeenCalledWith(
    "/repos/{owner}/{name}/pulls/{number}/review-threads",
    {
      params: { path: { owner: "local", name: "demo", number: 7 } },
      body: {
        mode: "discuss-first",
        threads: [{ path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc", body: "x" }],
      },
    },
  );
});

it("apply posts to the apply endpoint and replaces state", async () => {
  const post = vi.fn(async () => ({ data: { threads: [thread({ status: "applied" })] }, error: undefined }));
  const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
  await store.load("local", "demo", 7);
  const ok = await store.apply(1);
  expect(ok).toBe(true);
  expect(post).toHaveBeenCalledWith(
    "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply",
    { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
  );
  expect(store.getThreads()[0]!.status).toBe("applied");
});

it("applyAll posts to apply-all and replaces state", async () => {
  const post = vi.fn(async () => ({ data: { threads: [thread({ status: "applied" })] }, error: undefined }));
  const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
  await store.load("local", "demo", 7);
  const ok = await store.applyAll();
  expect(ok).toBe(true);
  expect(post).toHaveBeenCalledWith(
    "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all",
    { params: { path: { owner: "local", name: "demo", number: 7 } } },
  );
  expect(store.getThreads()[0]!.status).toBe("applied");
});

it("deleteThread DELETEs and replaces state with the remaining list", async () => {
  const del = vi.fn(async () => ({ data: { threads: [] }, error: undefined }));
  const store = createReviewThreadsStore({ client: stubClient({ DELETE: del }) });
  await store.load("local", "demo", 7);
  const ok = await store.deleteThread(1);
  expect(ok).toBe(true);
  expect(del).toHaveBeenCalledWith(
    "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}",
    { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
  );
  expect(store.getThreads()).toHaveLength(0);
});

it("refresh re-reads threads without toggling loading", async () => {
  const get = vi.fn(async () => ({ data: { threads: [thread(), thread({ id: 2 })] }, error: undefined }));
  const store = createReviewThreadsStore({ client: stubClient({ GET: get }) });
  await store.load("local", "demo", 7);
  expect(store.getThreads()).toHaveLength(1);
  await store.refresh();
  expect(store.getThreads()).toHaveLength(2);
  expect(store.isLoading()).toBe(false);
});
```

- [ ] **Step 2: Run to verify they fail** — `cd frontend && bunx vitest run reviewThreads` → FAIL (`apply`/`applyAll`/`deleteThread`/`refresh` undefined; `createThreads` arity).

- [ ] **Step 3: Implement the store changes** in `reviewThreads.svelte.ts`.

Change `createThreads` to accept `mode` and include it in the body:

```ts
  async function createThreads(
    drafts: ReviewThreadDraftInput[], mode?: string,
  ): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        {
          params: { path: { owner, name, number } },
          body: {
            ...(mode ? { mode } : {}),
            threads: drafts.map((d) => ({
              path: d.path,
              side: d.side,
              line: d.line,
              ...(d.startLine != null ? { start_line: d.startLine } : {}),
              commit_sha: d.commitSha,
              body: d.body,
            })),
          },
        },
      );
      if (err) throw new Error(detail(err, "failed to create review threads"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }
```

Add these functions (near `resolve`):

```ts
  async function apply(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to apply thread"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function applyAll(): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all",
        { params: { path: { owner, name, number } } },
      );
      if (err) throw new Error(detail(err, "failed to apply all threads"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function deleteThread(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.DELETE(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to delete thread"));
      threads = data?.threads ?? threads.filter((t) => t.id !== threadID);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  // refresh re-reads the current review's threads without toggling the
  // loading flag — used by the live poll while an agent turn runs and by
  // the SSE data_changed catch-all. No-op when not on a loaded local review.
  async function refresh(): Promise<void> {
    if (owner !== "local" || number === 0) return;
    try {
      const { data, error: err } = await client.GET(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        { params: { path: { owner, name, number } } },
      );
      if (err) return; // best-effort; keep current state on transient errors
      threads = data?.threads ?? threads;
    } catch {
      // swallow — refresh is best-effort
    }
  }
```

Extend the returned object:

```ts
  return {
    getThreads, getThreadsAtAnchor, isLoading, getError,
    load, createThreads, addComment, hide, unhide, resolve,
    apply, applyAll, deleteThread, refresh, clear,
  };
```

- [ ] **Step 4: Run to verify they pass** — `cd frontend && bunx vitest run reviewThreads` → PASS. Then `cd frontend && bun run build` → clean (typecheck).

- [ ] **Step 5: Commit:**

```bash
git add packages/ui/src/stores/reviewThreads.svelte.ts packages/ui/src/stores/reviewThreads.svelte.test.ts
git commit -m "feat(ui): reviewThreads store apply/applyAll/delete/refresh + mode"
```

---

### Task 3: `ReviewThreadCard` — Apply + Delete (inline confirm)

**Files:**
- Modify: `packages/ui/src/components/diff/ReviewThreadCard.svelte`
- Test: `packages/ui/src/components/diff/ReviewThreadCard.test.ts` (append + extend the store mock)

- [ ] **Step 1: Write the failing tests.** In `ReviewThreadCard.test.ts`, extend the mocked store and add tests. Update the `vi.mock` to include `apply` + `deleteThread`:

```ts
const resolve = vi.fn(async () => true);
const hide = vi.fn(async () => true);
const addComment = vi.fn(async () => true);
const apply = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { resolve, hide, unhide: vi.fn(), addComment, apply, deleteThread },
  }),
}));
```

Add tests:

```ts
it("shows Apply for open/discussed threads and calls the store", async () => {
  const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "discussed" }) } });
  await fireEvent.click(getByTitle("Apply this thread's change"));
  expect(apply).toHaveBeenCalledWith(5);
});

it("hides Apply once applied/resolved", () => {
  const { queryByTitle } = render(ReviewThreadCard, { props: { thread: thread({ status: "applied" }) } });
  expect(queryByTitle("Apply this thread's change")).toBeNull();
});

it("delete requires a confirm click before calling the store", async () => {
  const { getByText, getByTitle } = render(ReviewThreadCard, { props: { thread: thread() } });
  await fireEvent.click(getByTitle("Delete this thread permanently"));
  expect(deleteThread).not.toHaveBeenCalled();
  expect(getByText("Confirm?")).toBeTruthy();
  await fireEvent.click(getByText("Confirm?"));
  expect(deleteThread).toHaveBeenCalledWith(5);
});
```

- [ ] **Step 2: Run to verify they fail** — `cd frontend && bunx vitest run ReviewThreadCard` → FAIL (no Apply/Delete buttons).

- [ ] **Step 3: Implement** in `ReviewThreadCard.svelte`. Add to the `<script>` (after `let sending`):

```ts
  let confirmingDelete = $state(false);
  const canApply = $derived(thread.status === "open" || thread.status === "discussed");

  async function onDelete(): Promise<void> {
    if (!confirmingDelete) {
      confirmingDelete = true;
      return;
    }
    confirmingDelete = false;
    await reviewThreads.deleteThread(thread.id);
  }
```

In the header (`<div class="review-thread__header">`), add an Apply button BEFORE the Resolve button, and a Delete button AFTER Hide:

```svelte
      {#if canApply}
        <button
          type="button"
          class="review-thread__action"
          title="Apply this thread's change"
          onclick={() => void reviewThreads.apply(thread.id)}
        >Apply</button>
      {/if}
```

(...existing Resolve and Hide buttons...)

```svelte
      <button
        type="button"
        class="review-thread__action review-thread__action--delete"
        title="Delete this thread permanently"
        onclick={() => void onDelete()}
      >{confirmingDelete ? "Confirm?" : "Delete"}</button>
```

> The existing rule `.review-thread__action:first-of-type { margin-left: auto; }` right-aligns the action cluster: when Apply renders it becomes `:first-of-type`; otherwise Resolve does. Either way the cluster stays right-aligned — no CSS change needed for layout.

Add a delete-hover color to the `<style>` block (after `.review-thread__action:hover`):

```css
  .review-thread__action--delete:hover {
    color: var(--accent-red);
    border-color: var(--accent-red);
  }
```

- [ ] **Step 4: Run to verify they pass** — `cd frontend && bunx vitest run ReviewThreadCard` → PASS. Then `cd frontend && bun run build` → clean.

- [ ] **Step 5: Commit:**

```bash
git add packages/ui/src/components/diff/ReviewThreadCard.svelte packages/ui/src/components/diff/ReviewThreadCard.test.ts
git commit -m "feat(ui): Apply + Delete actions on ReviewThreadCard"
```

---

### Task 4: `ReviewThreadsSection` (thread picker) + mount in `DiffSidebar`

**Files:**
- Create: `packages/ui/src/components/diff/ReviewThreadsSection.svelte`
- Modify: `packages/ui/src/components/diff/DiffSidebar.svelte`
- Test: `packages/ui/src/components/diff/ReviewThreadsSection.test.ts`

- [ ] **Step 1: Write the failing test** — `ReviewThreadsSection.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const applyAll = vi.fn(async () => true);
let running = false;
const threadsRef: { value: unknown[] } = { value: [] };

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { getThreads: () => threadsRef.value, applyAll },
    worktreeSession: { hasRunningTurn: () => running },
  }),
}));

import ReviewThreadsSection from "./ReviewThreadsSection.svelte";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 1, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [{ id: 1, author: "user", body: "rename this please", created_at: "" }],
    ...over,
  };
}

afterEach(() => { cleanup(); vi.clearAllMocks(); running = false; threadsRef.value = []; });

describe("ReviewThreadsSection", () => {
  it("renders nothing when there are no threads", () => {
    threadsRef.value = [];
    const { queryByText } = render(ReviewThreadsSection);
    expect(queryByText("Review threads")).toBeNull();
  });

  it("lists non-hidden threads with status + preview", () => {
    threadsRef.value = [thread(), thread({ id: 2, hidden: true })];
    const { getByText, queryByText } = render(ReviewThreadsSection);
    expect(getByText("Review threads")).toBeTruthy();
    expect(getByText(/rename this please/)).toBeTruthy();
    expect(getByText("1")).toBeTruthy(); // count = 1 non-hidden
    expect(queryByText("2")).toBeNull();
  });

  it("Apply all calls the store and is disabled while a turn runs", async () => {
    threadsRef.value = [thread({ status: "discussed" })];
    running = true;
    const { getByText } = render(ReviewThreadsSection);
    const btn = getByText("Apply all") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.click(btn);
    expect(applyAll).not.toHaveBeenCalled();
  });

  it("Apply all triggers when idle", async () => {
    threadsRef.value = [thread({ status: "open" })];
    running = false;
    const { getByText } = render(ReviewThreadsSection);
    await fireEvent.click(getByText("Apply all"));
    expect(applyAll).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run to verify it fails** — `cd frontend && bunx vitest run ReviewThreadsSection` → FAIL (component absent).

- [ ] **Step 3: Implement `ReviewThreadsSection.svelte`** (mirrors `PendingCommentsSection`, reads persisted threads):

```svelte
<script lang="ts">
  import { getStores } from "../../context.js";
  import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

  const { reviewThreads, worktreeSession } = getStores();

  const threads = $derived(reviewThreads.getThreads().filter((t) => !t.hidden));
  const applicable = $derived(
    threads.filter((t) => t.status === "open" || t.status === "discussed"),
  );
  const busy = $derived(worktreeSession.hasRunningTurn());

  let expanded = $state(false);
  let userCollapsed = $state(false);
  $effect(() => {
    if (threads.length > 0 && !userCollapsed) expanded = true;
  });
  function toggle(): void {
    expanded = !expanded;
    userCollapsed = !expanded;
  }

  function anchorLabel(t: ReviewThread): string {
    const sign = t.side === "LEFT" ? "−" : "+";
    if (t.start_line != null && t.start_line !== t.line) {
      return `${sign}${t.start_line}–${t.line}`;
    }
    return `${sign}${t.line}`;
  }
  function rootBody(t: ReviewThread): string {
    return t.comments?.[0]?.body ?? "";
  }
  function truncate(s: string, n: number): string {
    return s.length <= n ? s : s.slice(0, n).trimEnd() + "…";
  }
  function scrollToThread(t: ReviewThread): void {
    const selector =
      `.diff-file[data-file-path="${CSS.escape(t.path)}"] ` +
      `.line-wrap[data-anchor-line="${t.line}"]` +
      `[data-anchor-side="${t.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) el.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  async function onApplyAll(): Promise<void> {
    if (busy) return;
    await reviewThreads.applyAll();
  }
</script>

{#if threads.length > 0}
  <div class="threads-section">
    <div class="threads-section__header">
      <button class="threads-section__toggle" onclick={toggle}>
        <span class="threads-section__chevron" class:threads-section__chevron--open={expanded}>&#8250;</span>
        <span class="threads-section__label">Review threads</span>
        <span class="threads-section__count">{threads.length}</span>
      </button>
      {#if applicable.length > 0}
        <button
          type="button"
          class="threads-section__apply-all"
          disabled={busy}
          title={busy ? "The review agent is busy" : `Apply ${applicable.length} thread(s)`}
          onclick={() => void onApplyAll()}
        >Apply all</button>
      {/if}
    </div>

    {#if expanded}
      <div class="threads-section__body">
        {#each threads as t (t.id)}
          <button
            type="button"
            class="thread-item"
            onclick={() => scrollToThread(t)}
            title="Scroll to this thread in the diff"
          >
            <span class="thread-item__anchor">{anchorLabel(t)}</span>
            <span class="thread-item__status">{t.status}</span>
            <span class="thread-item__path">{t.path}</span>
            <span class="thread-item__preview">{truncate(rootBody(t), 80)}</span>
            <span class="thread-item__count" title="comments">{(t.comments ?? []).length}</span>
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .threads-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }
  .threads-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }
  .threads-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    border-radius: var(--radius-sm);
  }
  .threads-section__toggle:hover { background: var(--bg-surface-hover); }
  .threads-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }
  .threads-section__chevron--open { transform: rotate(90deg); }
  .threads-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }
  .threads-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }
  .threads-section__apply-all {
    margin-left: auto;
    font-size: 10px;
    font-weight: 600;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
    color: var(--accent-blue);
    cursor: pointer;
  }
  .threads-section__apply-all:hover:not(:disabled) { filter: brightness(1.1); }
  .threads-section__apply-all:disabled { opacity: 0.5; cursor: not-allowed; }
  .threads-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }
  .thread-item {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 10px 4px 12px;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    color: inherit;
  }
  .thread-item:hover { background: var(--bg-surface-hover); }
  .thread-item__anchor {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
    padding: 1px 6px;
    border-radius: 999px;
    flex-shrink: 0;
  }
  .thread-item__status {
    font-size: 10px;
    color: var(--text-muted);
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-sm);
    padding: 1px 6px;
    flex-shrink: 0;
  }
  .thread-item__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }
  .thread-item__preview {
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
    min-width: 0;
  }
  .thread-item__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    flex-shrink: 0;
  }
</style>
```

- [ ] **Step 4: Mount it in `DiffSidebar.svelte`.** Add the import next to the others (after the `PendingCommentsSection` import):

```ts
  import ReviewThreadsSection from "./ReviewThreadsSection.svelte";
```

And render it right after `<PendingCommentsSection />` (it renders nothing when there are no threads, so no gating is needed — threads only exist for local worktrees):

```svelte
  <PendingCommentsSection />
  <ReviewThreadsSection />
```

- [ ] **Step 5: Run to verify it passes** — `cd frontend && bunx vitest run ReviewThreadsSection` → PASS. Then `cd frontend && bun run build` → clean.

- [ ] **Step 6: Commit:**

```bash
git add packages/ui/src/components/diff/ReviewThreadsSection.svelte \
        packages/ui/src/components/diff/ReviewThreadsSection.test.ts \
        packages/ui/src/components/diff/DiffSidebar.svelte
git commit -m "feat(ui): review-threads picker in the diff sidebar"
```

---

### Task 5: Mode picker in `ReviewPanel`

**Files:**
- Modify: `packages/ui/src/components/diff/ReviewPanel.svelte`
- Test: `packages/ui/src/components/diff/ReviewPanel.test.ts` (create)

- [ ] **Step 1: Write the failing test** — `ReviewPanel.test.ts`. Mock context (`getStores` + `getClient`); provide a one-comment draft so the local submit path runs.

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const createThreads = vi.fn(async () => true);
const clearDraft = vi.fn();
const draft = {
  comments: [{ id: 1, path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "x", inReplyTo: null }],
  event: "COMMENT",
  body: "",
};

vi.mock("../../context.js", () => ({
  getStores: () => ({
    diff: { getDraft: () => draft, clearDraft, getCommits: () => [] },
    pulls: { loadPulls: vi.fn() },
    reviewThreads: { createThreads, getError: () => null },
  }),
  getClient: () => ({ POST: vi.fn() }),
}));

import ReviewPanel from "./ReviewPanel.svelte";

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("ReviewPanel mode picker (local)", () => {
  it("defaults to persist-only and submits without a mode", async () => {
    const { getByText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByText("Create review threads"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), undefined);
  });

  it("submits the selected mode and reflects it in the button", async () => {
    const { getByText, getByLabelText } = render(ReviewPanel, {
      props: { owner: "local", name: "demo", number: 7, onclose: vi.fn() },
    });
    await fireEvent.click(getByLabelText("Discuss first"));
    await fireEvent.click(getByText("Create & discuss"));
    expect(createThreads).toHaveBeenCalledWith(expect.any(Array), "discuss-first");
  });
});
```

> If the `diff` store mock is missing a field the component reads at runtime, reconcile the mock against `packages/ui/src/stores/diff.svelte.ts` (the component reads `getDraft()` → `{ comments, event, body }`, `clearDraft()`, `getCommits()`). The local path never calls `getClient().POST`.

- [ ] **Step 2: Run to verify it fails** — `cd frontend && bunx vitest run ReviewPanel` → FAIL (no mode picker; submit passes no mode).

- [ ] **Step 3: Implement** in `ReviewPanel.svelte`. Add to `<script>` (after `let errorMsg`):

```ts
  // Local-only: what the agent does with the submitted threads.
  type ThreadMode = "persist-only" | "discuss-first" | "act-immediately";
  let mode = $state<ThreadMode>("persist-only");
  const submitLabel = $derived(
    mode === "discuss-first" ? "Create & discuss"
      : mode === "act-immediately" ? "Create & apply"
        : "Create review threads",
  );
```

In the local submit branch, pass the mode (persist-only → `undefined` so the request body stays minimal):

```ts
        const ok = await reviewThreadsStore.createThreads(
          drafts,
          mode === "persist-only" ? undefined : mode,
        );
```

Add the picker AFTER the `{#if !isLocal} ... {/if}` verdict fieldset (so local shows the mode picker, non-local shows the verdict radios). Reuse the existing `panel__events`/`panel__event` classes:

```svelte
  {#if isLocal}
  <fieldset class="panel__events">
    <legend class="visually-hidden">Agent mode</legend>
    <label class="panel__event">
      <input type="radio" name="thread-mode" value="persist-only"
        checked={mode === "persist-only"} onchange={() => (mode = "persist-only")} />
      <span>Persist only</span>
      <small>Save threads, no agent</small>
    </label>
    <label class="panel__event">
      <input type="radio" name="thread-mode" value="discuss-first"
        checked={mode === "discuss-first"} onchange={() => (mode = "discuss-first")} />
      <span>Discuss first</span>
      <small>Agent replies (read-only)</small>
    </label>
    <label class="panel__event">
      <input type="radio" name="thread-mode" value="act-immediately"
        checked={mode === "act-immediately"} onchange={() => (mode = "act-immediately")} />
      <span>Act immediately</span>
      <small>Agent edits the worktree</small>
    </label>
  </fieldset>
  {/if}
```

Update the primary-button label (the `{#if isLocal}` branch) to use `submitLabel`:

```svelte
      {#if isLocal}
        {submitting ? "Creating…" : submitLabel}
      {:else}
        {submitting ? "Publishing…" : "Publish review"}
      {/if}
```

> The `<label>` wraps its text, so `getByLabelText("Discuss first")` resolves the radio. The `<small>` text is part of the label's accessible name; if the matcher is finicky, use `getByRole("radio", { name: /discuss first/i })`.

- [ ] **Step 4: Run to verify it passes** — `cd frontend && bunx vitest run ReviewPanel` → PASS. Then `cd frontend && bun run build` → clean.

- [ ] **Step 5: Commit:**

```bash
git add packages/ui/src/components/diff/ReviewPanel.svelte packages/ui/src/components/diff/ReviewPanel.test.ts
git commit -m "feat(ui): submit-time mode picker for local review threads"
```

---

### Task 6: Live status (approach 5A) — Provider poll-while-running + SSE catch-all

**Files:**
- Modify: `packages/ui/src/Provider.svelte`

This wires the store's `refresh()` (Task 2) to run while an agent turn is in flight and on SSE `data_changed`, so threads flip `discussed`/`applied` and agent replies appear without a manual refresh. The `refresh()` behavior itself is unit-tested in Task 2; this task is integration wiring verified by build + a manual smoke (an automated test of a 1.5s-interval `$effect` across two stores is low-value, matching how `worktreeSession`'s own poll is integration-verified).

- [ ] **Step 1: Hoist the `reviewThreads` store creation** above `eventsStore` in `Provider.svelte`'s `init()`. Find the inline creation in the `si` object literal:

```ts
      worktreeSession: createWorktreeSessionStore({ client: cl }),
      reviewThreads: createReviewThreadsStore({ client: cl }),
```

Replace the `reviewThreads:` line so it references a local created earlier. First, BEFORE `const eventsStore = createEventsStore({` (currently ~line 215), add:

```ts
    const reviewThreadsStore = createReviewThreadsStore({ client: cl });
```

Then change the `si` literal entry to:

```ts
      worktreeSession: createWorktreeSessionStore({ client: cl }),
      reviewThreads: reviewThreadsStore,
```

- [ ] **Step 2: Add the SSE catch-all.** In the `createEventsStore({ ... onDataChanged: () => { ... } })` callback, add a thread refresh alongside the existing reloads:

```ts
      onDataChanged: () => {
        void pullsStore.loadPulls();
        void issuesStore.loadIssues();
        void activityStore.loadActivity();
        void worktreesStore.loadWorktrees();
        void reviewThreadsStore.refresh();
      },
```

- [ ] **Step 3: Add the poll-while-running effect** at the top level of the `<script>`, AFTER `stores = init(...)` (and before/after `onDestroy` — either is fine):

```svelte
  // Phase 2b (5A): while an agent turn is running, re-read the review's
  // threads on the same cadence as the session poll so statuses flip
  // (discussed/applied) and agent replies appear live. The $derived
  // boolean means this effect only re-runs when the running state
  // flips, so the interval is set up/torn down once per turn.
  const sessionRunning = $derived(
    stores?.worktreeSession?.hasRunningTurn() ?? false,
  );
  $effect(() => {
    if (!sessionRunning) return;
    const rt = stores?.reviewThreads;
    if (!rt) return;
    const id = setInterval(() => {
      void rt.refresh();
    }, 1500);
    return () => clearInterval(id);
  });
```

- [ ] **Step 4: Verify** — `cd frontend && bun run build` → clean (typecheck). Then a manual smoke (optional but recommended): run `make build && ./middleman`, open a local worktree, submit with `discuss-first`, and confirm the thread flips to `discussed` and the agent reply appears without refreshing.

- [ ] **Step 5: Commit:**

```bash
git add packages/ui/src/Provider.svelte
git commit -m "feat(ui): live review-thread status while the agent runs"
```

---

## Self-review

**Spec coverage:**
- Mode picker (persist-only default), local-only → Task 5. ✓
- Per-thread Apply + Apply-all → Task 3 (Apply) + Task 4 (Apply-all in the section header). ✓
- `reviewThreads` store `apply`/`applyAll`/`deleteThread`/`refresh`/`mode` → Task 2. ✓
- Thread picker in the left drawer (covers obs 1 + obs 3) → Task 4. ✓
- Delete + backend `DELETE` endpoint + `DeleteReviewThread` + regen → Task 1 (backend) + Task 3 (card button). ✓
- Live status (5A): poll-while-running + SSE catch-all → Task 6. ✓
- Out of scope (Phase 3) correctly absent. ✓

**Placeholder scan:** The only deferred-to-execution items are (a) the DB-test seeding helper name in Task 1 Step 1 (`seedLocalMRForThreads`), with an explicit inline fallback + the exact source to mirror, and (b) the generated delete-method name in Task 1 Step 7, flagged to reconcile post-regen against `client.gen.go` (same as the 2a plan). No vague "add error handling"/"TBD" steps; all component/store/endpoint code is complete.

**Type consistency:** Store method names (`apply`, `applyAll`, `deleteThread`, `refresh`, `createThreads(drafts, mode?)`) match between Task 2 (definition), Task 3 (`reviewThreads.apply`/`deleteThread`), Task 4 (`reviewThreads.applyAll`), and Task 5 (`createThreads(drafts, mode)`). The card/section call `reviewThreads.deleteThread`/`apply`/`applyAll` exactly as exported. The delete endpoint + store DELETE path + e2e method all target `/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}`. `listReviewThreadsOutput` (Body.Threads) is the delete handler's return, matching the store's `data?.threads`.

**Risks (from the spec):** poll lifecycle scoped via the `$derived` running flag (interval torn down when the turn ends); delete-during-turn 404s harmlessly; optimistic 2a status is surfaced/reconciled by the live poll showing real replies.
