# Hide PR Review Threads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a reviewer hide a GitHub PR review thread locally so it disappears from middleman's diff/rendered-markdown view and activity timeline, with a per-PR "Show hidden (N)" toggle to restore visibility and automatic re-show when a new reply arrives.

**Architecture:** A new `middleman_hidden_review_threads` table keyed by `(merge_request_id, root_platform_comment_id)` with a `hidden_at` timestamp. A thread is "currently hidden" iff a row exists AND no review_comment in that thread has `created_at > hidden_at` — computed server-side over already-loaded events. The active hidden set is shipped in `PullDetail.hidden_thread_root_ids`. Frontend filters both the inline `ReviewCommentCard` surfaces and the `EventTimeline` against that set, gated by a `showHiddenThreads` flag on the detail store.

**Tech Stack:** Go 1.x, Huma, modernc.org/sqlite, oapi-codegen Go client, Svelte 5 (runes), Bun, openapi-typescript / openapi-fetch, vitest.

**Spec:** `docs/superpowers/specs/2026-05-21-pr-comment-hiding-design.md`

---

## File Map

**New files (Go):**
- `internal/db/migrations/000020_add_hidden_review_threads.up.sql`
- `internal/db/migrations/000020_add_hidden_review_threads.down.sql`
- `internal/db/queries_hidden_threads.go` — type + CRUD + filter helper
- `internal/db/queries_hidden_threads_test.go`
- `internal/server/huma_routes_hidden_threads.go` — input/output types + handlers
- `internal/server/hidden_threads_e2e_test.go`

**Modified files (Go):**
- `internal/server/api_types.go` — add `HiddenThreadRootIDs []int64` to `mergeRequestDetailResponse`
- `internal/server/huma_routes.go` — register two routes; extend `buildPullDetailResponse` to populate the new field

**New files (TypeScript):**
- `packages/ui/src/stores/detail.test.ts` — store unit tests for hidden-thread filter + actions

**Modified files (TypeScript):**
- `packages/ui/src/stores/detail.svelte.ts` — `PublishedReviewComment` gains `isHidden`; new helpers + actions + `showHiddenThreads` state
- `packages/ui/src/components/diff/ReviewCommentCard.svelte` — Hide/Unhide button, `rc--hidden` styling, `isHidden` prop
- `packages/ui/src/components/diff/DiffFile.svelte` — pass `isHidden` to each `<ReviewCommentCard>` (lookup against the hidden root map)
- `packages/ui/src/components/diff/RenderedMarkdownView.svelte` — same `isHidden` propagation in the dynamic mount call
- `packages/ui/src/components/diff/DiffToolbar.svelte` — Show Hidden (N) toggle (covers diff + rendered-markdown sub-views)
- `packages/ui/src/components/detail/EventTimeline.svelte` — hidden-thread filtering + own Show Hidden (N) toggle next to mechanics

**Modified files (other):**
- Regenerated artifacts touched by `make api-generate`:
  - `frontend/openapi/openapi.json`
  - `internal/apiclient/spec/openapi.json`
  - `internal/apiclient/generated/client.gen.go`
  - `packages/ui/src/api/generated/schema.ts`

---

## Conventions

- Always commit at the end of each task (per CLAUDE.md "Commit every turn").
- Never amend; new commit per task.
- Tests use `testify`'s `require` for preconditions, `assert` (via `Assert.New(t)` helper) for non-blocking checks.
- Go test invocations omit `-count=1`; use `-shuffle=on` directly (`make test` handles it for the full suite).
- No emojis.
- Datetimes in UTC across DB and API boundaries.

---

## Task 1: DB migration + `HiddenReviewThread` type and CRUD

**Files:**
- Create: `internal/db/migrations/000020_add_hidden_review_threads.up.sql`
- Create: `internal/db/migrations/000020_add_hidden_review_threads.down.sql`
- Create: `internal/db/queries_hidden_threads.go`
- Create: `internal/db/queries_hidden_threads_test.go`

- [ ] **Step 1.1: Write the failing CRUD test**

Create `internal/db/queries_hidden_threads_test.go`:

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHiddenReviewThreadsUpsertAndDelete(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	// No rows initially.
	rows, err := d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	assert.Empty(rows)

	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 42, t0))

	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Len(rows, 1)
	assert.Equal(int64(42), rows[0].RootPlatformCommentID)
	assert.True(rows[0].HiddenAt.Equal(t0))

	// Re-hide overwrites hidden_at.
	t1 := t0.Add(time.Hour)
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 42, t1))
	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Len(rows, 1)
	assert.True(rows[0].HiddenAt.Equal(t1))

	// Delete is idempotent.
	require.NoError(d.DeleteHiddenReviewThread(ctx, mrID, 42))
	require.NoError(d.DeleteHiddenReviewThread(ctx, mrID, 42))
	rows, err = d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	assert.Empty(rows)
}

func TestHiddenReviewThreadsCascadeDelete(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	require.NoError(d.UpsertHiddenReviewThread(
		ctx, mrID, 99, time.Now().UTC().Truncate(time.Second),
	))

	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_merge_requests WHERE id = ?`, mrID,
	)
	require.NoError(err)

	rows, err := d.ListHiddenReviewThreads(ctx, mrID)
	require.NoError(err)
	require.Empty(rows, "rows should cascade with the MR")
}
```

- [ ] **Step 1.2: Run the test to confirm it fails (no types yet)**

Run: `go test ./internal/db -run TestHiddenReviewThreads -shuffle=on`
Expected: FAIL — `undefined: db.ListHiddenReviewThreads`, `undefined: db.UpsertHiddenReviewThread`, etc.

- [ ] **Step 1.3: Write the up migration**

Create `internal/db/migrations/000020_add_hidden_review_threads.up.sql`:

```sql
-- Per-PR record of review threads the reviewer has hidden from the UI.
-- One row per (merge_request, thread root) where the root is identified
-- by the GitHub comment id of the top-level review comment in that
-- thread. We use the GitHub platform id (not our autoincrement
-- middleman_mr_events.id) because re-syncs can replace local rows
-- but GitHub ids are stable.
--
-- "Currently hidden" is a derived predicate: a row is active iff
-- no review_comment in that thread has created_at > hidden_at. The
-- active set is computed at read time; stale rows are harmless and
-- a future re-hide UPSERTs a fresher hidden_at.
CREATE TABLE IF NOT EXISTS middleman_hidden_review_threads (
    merge_request_id         INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    root_platform_comment_id INTEGER  NOT NULL,
    hidden_at                DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, root_platform_comment_id)
);
```

- [ ] **Step 1.4: Write the down migration**

Create `internal/db/migrations/000020_add_hidden_review_threads.down.sql`:

```sql
DROP TABLE IF EXISTS middleman_hidden_review_threads;
```

- [ ] **Step 1.5: Write the queries file**

Create `internal/db/queries_hidden_threads.go`:

```go
package db

import (
	"context"
	"time"
)

// HiddenReviewThread is one row in middleman_hidden_review_threads —
// a reviewer's intent to hide a review thread on a specific PR. The
// row is "active" only if no reply in the thread has a created_at
// after HiddenAt; see ActiveHiddenReviewThreadRoots.
type HiddenReviewThread struct {
	MergeRequestID        int64
	RootPlatformCommentID int64
	HiddenAt              time.Time
}

// UpsertHiddenReviewThread records (or re-records) the user's intent
// to hide the given thread. Re-hiding overwrites HiddenAt so a new
// reply that arrived between the two hides doesn't keep the thread
// visible.
func (d *DB) UpsertHiddenReviewThread(
	ctx context.Context,
	mrID, rootPlatformCommentID int64,
	hiddenAt time.Time,
) error {
	_, err := d.rw.ExecContext(ctx,
		`INSERT INTO middleman_hidden_review_threads
		     (merge_request_id, root_platform_comment_id, hidden_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(merge_request_id, root_platform_comment_id) DO UPDATE
		     SET hidden_at = excluded.hidden_at`,
		mrID, rootPlatformCommentID, hiddenAt.UTC(),
	)
	return err
}

// DeleteHiddenReviewThread clears the user's hide for a given thread.
// No-op when the row doesn't exist (DELETE on a missing row returns
// without error).
func (d *DB) DeleteHiddenReviewThread(
	ctx context.Context, mrID, rootPlatformCommentID int64,
) error {
	_, err := d.rw.ExecContext(ctx,
		`DELETE FROM middleman_hidden_review_threads
		  WHERE merge_request_id = ? AND root_platform_comment_id = ?`,
		mrID, rootPlatformCommentID,
	)
	return err
}

// ListHiddenReviewThreads returns every stored row for an MR,
// including rows that may be superseded by a newer reply. Callers
// that need only currently-active hides should pass the result through
// ActiveHiddenReviewThreadRoots.
func (d *DB) ListHiddenReviewThreads(
	ctx context.Context, mrID int64,
) ([]HiddenReviewThread, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT merge_request_id, root_platform_comment_id, hidden_at
		   FROM middleman_hidden_review_threads
		  WHERE merge_request_id = ?`,
		mrID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HiddenReviewThread
	for rows.Next() {
		var h HiddenReviewThread
		if err := rows.Scan(
			&h.MergeRequestID, &h.RootPlatformCommentID, &h.HiddenAt,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
```

- [ ] **Step 1.6: Run the test to confirm it passes**

Run: `go test ./internal/db -run TestHiddenReviewThreads -shuffle=on`
Expected: PASS, two tests.

- [ ] **Step 1.7: Commit**

```bash
git add internal/db/migrations/000020_add_hidden_review_threads.up.sql \
        internal/db/migrations/000020_add_hidden_review_threads.down.sql \
        internal/db/queries_hidden_threads.go \
        internal/db/queries_hidden_threads_test.go
git commit -m "$(cat <<'EOF'
feat(db): add middleman_hidden_review_threads table and CRUD

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Active-hidden filter (the auto-unhide predicate)

**Files:**
- Modify: `internal/db/queries_hidden_threads.go`
- Modify: `internal/db/queries_hidden_threads_test.go`

- [ ] **Step 2.1: Write the failing predicate test**

Append to `internal/db/queries_hidden_threads_test.go`:

```go
func TestActiveHiddenReviewThreadRoots(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)

	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	beforeHide := t0.Add(-time.Hour)
	afterHide := t0.Add(time.Hour)

	id100 := int64(100)
	id101 := int64(101)
	id200 := int64(200)
	id201 := int64(201)
	id300 := int64(300)

	require.NoError(d.UpsertMREvents(ctx, []MREvent{
		{
			MergeRequestID: mrID, PlatformID: &id100, EventType: "review_comment",
			Author: "u", Body: "root A", CreatedAt: beforeHide.Add(-2 * time.Hour),
			MetadataJSON: `{"path":"a.go","line":1,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-100",
		},
		{
			MergeRequestID: mrID, PlatformID: &id101, EventType: "review_comment",
			Author: "u", Body: "reply on A (before hide)", CreatedAt: beforeHide,
			MetadataJSON: `{"path":"a.go","line":1,"side":"RIGHT","in_reply_to":100}`,
			DedupeKey:    "review-comment-101",
		},
		{
			MergeRequestID: mrID, PlatformID: &id200, EventType: "review_comment",
			Author: "u", Body: "root B", CreatedAt: beforeHide.Add(-2 * time.Hour),
			MetadataJSON: `{"path":"b.go","line":2,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-200",
		},
		{
			MergeRequestID: mrID, PlatformID: &id201, EventType: "review_comment",
			Author: "u", Body: "reply on B (after hide)", CreatedAt: afterHide,
			MetadataJSON: `{"path":"b.go","line":2,"side":"RIGHT","in_reply_to":200}`,
			DedupeKey:    "review-comment-201",
		},
		{
			MergeRequestID: mrID, PlatformID: &id300, EventType: "review_comment",
			Author: "u", Body: "lone root C", CreatedAt: beforeHide.Add(-3 * time.Hour),
			MetadataJSON: `{"path":"c.go","line":3,"side":"RIGHT"}`,
			DedupeKey:    "review-comment-300",
		},
	}))

	// Hide all three roots at t0.
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 100, t0))
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 200, t0))
	require.NoError(d.UpsertHiddenReviewThread(ctx, mrID, 300, t0))

	events, err := d.ListMREvents(ctx, mrID)
	require.NoError(err)

	active, err := d.ActiveHiddenReviewThreadRoots(ctx, mrID, events)
	require.NoError(err)
	assert.ElementsMatch([]int64{100, 300}, active,
		"thread 200 has a reply after hidden_at — should not be active")
}
```

- [ ] **Step 2.2: Run the test to confirm it fails**

Run: `go test ./internal/db -run TestActiveHiddenReviewThreadRoots -shuffle=on`
Expected: FAIL — `undefined: db.ActiveHiddenReviewThreadRoots`.

- [ ] **Step 2.3: Implement `ActiveHiddenReviewThreadRoots`**

Add to `internal/db/queries_hidden_threads.go`:

```go
import (
	"context"
	"encoding/json"
	"time"
)
```

(keep the existing imports; add `encoding/json` to the import block).

Then append:

```go
// ActiveHiddenReviewThreadRoots returns the subset of stored
// hidden_review_threads rows for mrID whose hide is still in effect:
// no review_comment in the thread has created_at > hidden_at.
//
// The caller passes the pre-loaded events slice so this method doesn't
// re-query mr_events. The walk to root mirrors ResolveReviewCommentRootID
// but stays in memory.
func (d *DB) ActiveHiddenReviewThreadRoots(
	ctx context.Context, mrID int64, events []MREvent,
) ([]int64, error) {
	hides, err := d.ListHiddenReviewThreads(ctx, mrID)
	if err != nil {
		return nil, err
	}
	if len(hides) == 0 {
		return nil, nil
	}

	// Build platform_id → in_reply_to map and platform_id → created_at.
	parentByID := make(map[int64]int64, len(events))
	createdByID := make(map[int64]time.Time, len(events))
	for _, e := range events {
		if e.EventType != "review_comment" || e.PlatformID == nil {
			continue
		}
		pid := *e.PlatformID
		createdByID[pid] = e.CreatedAt
		var meta struct {
			InReplyTo int64 `json:"in_reply_to"`
		}
		if e.MetadataJSON != "" {
			// Ignore unmarshal errors — treat as root.
			_ = json.Unmarshal([]byte(e.MetadataJSON), &meta)
		}
		if meta.InReplyTo != 0 && meta.InReplyTo != pid {
			parentByID[pid] = meta.InReplyTo
		}
	}

	// Resolve every review_comment to its root (bounded chain walk).
	rootOf := func(pid int64) int64 {
		current := pid
		for i := 0; i < 32; i++ {
			parent, ok := parentByID[current]
			if !ok {
				return current
			}
			current = parent
		}
		return current
	}

	// Compute max(created_at) per root.
	maxCreatedByRoot := make(map[int64]time.Time, len(events))
	for pid, t := range createdByID {
		root := rootOf(pid)
		if cur, ok := maxCreatedByRoot[root]; !ok || t.After(cur) {
			maxCreatedByRoot[root] = t
		}
	}

	out := make([]int64, 0, len(hides))
	for _, h := range hides {
		latest, ok := maxCreatedByRoot[h.RootPlatformCommentID]
		if ok && latest.After(h.HiddenAt) {
			continue // superseded by a newer reply
		}
		out = append(out, h.RootPlatformCommentID)
	}
	return out, nil
}
```

- [ ] **Step 2.4: Run the test to confirm it passes**

Run: `go test ./internal/db -run TestActiveHiddenReviewThreadRoots -shuffle=on`
Expected: PASS.

- [ ] **Step 2.5: Run the whole package suite to verify no collisions**

Run: `go test ./internal/db -shuffle=on`
Expected: PASS.

- [ ] **Step 2.6: Commit**

```bash
git add internal/db/queries_hidden_threads.go internal/db/queries_hidden_threads_test.go
git commit -m "$(cat <<'EOF'
feat(db): ActiveHiddenReviewThreadRoots predicate for auto-unhide on new reply

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Surface the active hidden set on `PullDetail`

**Files:**
- Modify: `internal/server/api_types.go`
- Modify: `internal/server/huma_routes.go:612-666` (`buildPullDetailResponse`)

- [ ] **Step 3.1: Write the failing handler test (file is new but lives in `server` package)**

Create `internal/server/hidden_threads_e2e_test.go`:

```go
package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/db"
)

// seedReviewComment is one comment to insert via seedReviewComments.
// ID is the GitHub platform comment id; InReplyTo of 0 marks a root.
type seedReviewComment struct {
	ID        int64
	InReplyTo int64
	CreatedAt time.Time
}

func seedReviewComments(t *testing.T, database *db.DB, mrID int64, items []seedReviewComment) {
	t.Helper()
	events := make([]db.MREvent, 0, len(items))
	for _, it := range items {
		id := it.ID
		meta := `{"path":"f.go","line":1,"side":"RIGHT"}`
		if it.InReplyTo != 0 {
			meta = `{"path":"f.go","line":1,"side":"RIGHT","in_reply_to":` +
				strconv.FormatInt(it.InReplyTo, 10) + `}`
		}
		events = append(events, db.MREvent{
			MergeRequestID: mrID,
			PlatformID:     &id,
			EventType:      "review_comment",
			Author:         "reviewer",
			Body:           "comment body",
			CreatedAt:      it.CreatedAt,
			MetadataJSON:   meta,
			DedupeKey:      "review-comment-" + strconv.FormatInt(it.ID, 10),
		})
	}
	require.NoError(t, database.UpsertMREvents(context.Background(), events))
}

func TestPullDetailIncludesEmptyHiddenSetByDefault(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, resp.StatusCode())
	require.NotNil(resp.JSON200)
	require.NotNil(resp.JSON200.HiddenThreadRootIds, "field should be present and non-nil")
	assert.Empty(*resp.JSON200.HiddenThreadRootIds)
}
```

Note: `seedReviewComments` and `int64ToString`/`formatInt64Test` exist for later tests in this file (Tasks 4 & 5). Including them now keeps subsequent tasks self-contained.

- [ ] **Step 3.2: Run the test to confirm it fails on the missing field**

Run: `go test ./internal/server -run TestPullDetailIncludesEmptyHiddenSetByDefault -shuffle=on`
Expected: FAIL — generated client struct has no field `HiddenThreadRootIds` (or after Step 3.3, JSON tag missing on the response).

- [ ] **Step 3.3: Add the field to the response struct**

Edit `internal/server/api_types.go`. Locate the `mergeRequestDetailResponse` struct (around line 48) and append the new field:

```go
type mergeRequestDetailResponse struct {
	MergeRequest     *db.MergeRequest         `json:"merge_request"`
	Events           []db.MREvent             `json:"events"`
	RepoOwner        string                   `json:"repo_owner"`
	RepoName         string                   `json:"repo_name"`
	PlatformHost     string                   `json:"platform_host"`
	WorktreeLinks    []worktreeLinkResponse   `json:"worktree_links"`
	WorkflowApproval workflowApprovalResponse `json:"workflow_approval"`
	Warnings         []string                 `json:"warnings,omitempty"`
	DetailLoaded     bool                     `json:"detail_loaded"`
	DetailFetchedAt  string                   `json:"detail_fetched_at,omitempty"`
	Workspace        *workspaceMRRef          `json:"workspace,omitempty"`
	// HiddenThreadRootIDs lists the GitHub platform comment ids of
	// review thread roots that are currently hidden in the UI for
	// this PR. Computed by ActiveHiddenReviewThreadRoots; a stored
	// hide is omitted if the thread has a reply newer than hidden_at.
	HiddenThreadRootIDs []int64 `json:"hidden_thread_root_ids"`
}
```

- [ ] **Step 3.4: Populate the field in `buildPullDetailResponse`**

Edit `internal/server/huma_routes.go`. In `buildPullDetailResponse` (starts ~line 612), after the `events` are loaded and before the response is built, compute and assign the active hidden set. Locate this block:

```go
	resp := mergeRequestDetailResponse{
		MergeRequest:     mr,
		Events:           events,
		RepoOwner:        repo.Owner,
		RepoName:         repo.Name,
		PlatformHost:     repo.PlatformHost,
		WorktreeLinks:    toWorktreeLinkResponses(dbLinks),
		WorkflowApproval: s.workflowApprovalState(ctx, repo.Owner, repo.Name, mr, wfMode),
		Warnings:         s.diffWarnings(mr),
		DetailLoaded:     mr.DetailFetchedAt != nil,
	}
```

Replace with:

```go
	hidden, err := s.db.ActiveHiddenReviewThreadRoots(ctx, mr.ID, events)
	if err != nil {
		return mergeRequestDetailResponse{}, huma.Error500InternalServerError(
			"compute hidden review threads failed",
		)
	}
	if hidden == nil {
		hidden = []int64{}
	}

	resp := mergeRequestDetailResponse{
		MergeRequest:        mr,
		Events:              events,
		RepoOwner:           repo.Owner,
		RepoName:            repo.Name,
		PlatformHost:        repo.PlatformHost,
		WorktreeLinks:       toWorktreeLinkResponses(dbLinks),
		WorkflowApproval:    s.workflowApprovalState(ctx, repo.Owner, repo.Name, mr, wfMode),
		Warnings:            s.diffWarnings(mr),
		DetailLoaded:        mr.DetailFetchedAt != nil,
		HiddenThreadRootIDs: hidden,
	}
```

- [ ] **Step 3.5: Regenerate OpenAPI + Go client so the test can compile**

Run: `make api-generate`
Expected: regenerates `frontend/openapi/openapi.json`, `internal/apiclient/spec/openapi.json`, `internal/apiclient/generated/client.gen.go`, `packages/ui/src/api/generated/schema.ts`, and `packages/ui/src/api/generated/client.ts`.

- [ ] **Step 3.6: Run the new test to confirm it passes**

Run: `go test ./internal/server -run TestPullDetailIncludesEmptyHiddenSetByDefault -shuffle=on`
Expected: PASS.

- [ ] **Step 3.7: Run the whole server suite to confirm nothing else broke**

Run: `go test ./internal/server -shuffle=on -short`
Expected: PASS.

- [ ] **Step 3.8: Commit**

```bash
git add internal/server/api_types.go internal/server/huma_routes.go \
        internal/server/hidden_threads_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go \
        packages/ui/src/api/generated/schema.ts \
        packages/ui/src/api/generated/client.ts
git commit -m "$(cat <<'EOF'
feat(server): expose hidden_thread_root_ids on PullDetail

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: POST `/hidden-threads` (hide a thread)

**Files:**
- Create: `internal/server/huma_routes_hidden_threads.go`
- Modify: `internal/server/huma_routes.go` (route registration ~line 432, near AI threads)
- Modify: `internal/server/hidden_threads_e2e_test.go`

- [ ] **Step 4.1: Write the failing handler test**

Append to `internal/server/hidden_threads_e2e_test.go`:

```go
func TestHideThreadAddsToActiveHiddenSet(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 1001, InReplyTo: 0, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: 1002, InReplyTo: 1001, CreatedAt: now.Add(-time.Hour)},
	})

	client := setupTestClient(t, srv)

	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 1001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode(), string(hideResp.Body))

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	assert.ElementsMatch([]int64{1001}, *getResp.JSON200.HiddenThreadRootIds)

	// Events themselves are still in the response — the client is the
	// one that filters based on the hidden set.
	require.NotNil(getResp.JSON200.Events)
	var rootPresent, replyPresent bool
	for _, e := range *getResp.JSON200.Events {
		if e.PlatformID == nil {
			continue
		}
		switch *e.PlatformID {
		case 1001:
			rootPresent = true
		case 1002:
			replyPresent = true
		}
	}
	assert.True(rootPresent && replyPresent, "events should still include all comments")
}

func TestHideThreadIs400IfRootIsNotAReviewCommentOnPR(t *testing.T) {
	require := require.New(t)
	srv, database := setupTestServer(t)
	seedPR(t, database, "acme", "widget", 1)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 99999},
	)
	require.NoError(err)
	require.Equal(http.StatusBadRequest, resp.StatusCode())
}

func TestHideThreadIs404ForUnknownPR(t *testing.T) {
	require := require.New(t)
	srv, _ := setupTestServer(t)
	client := setupTestClient(t, srv)

	resp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 999,
		generated.HideReviewThreadInputBody{RootCommentId: 1},
	)
	require.NoError(err)
	require.Equal(http.StatusNotFound, resp.StatusCode())
}
```

You'll also need this import at the top of the file (next to existing imports):

```go
import (
	// existing imports …
	"github.com/wesm/middleman/internal/apiclient/generated"
)
```

- [ ] **Step 4.2: Run to confirm it fails (generated client missing the method)**

Run: `go test ./internal/server -run TestHideThread -shuffle=on`
Expected: FAIL — `PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse` undefined; `HideReviewThreadInputBody` undefined.

- [ ] **Step 4.3: Implement the handler file**

Create `internal/server/huma_routes_hidden_threads.go`:

```go
package server

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// --- inputs / outputs --------------------------------------------------------

type hideReviewThreadInput struct {
	Owner  string `path:"owner"`
	Name   string `path:"name"`
	Number int    `path:"number"`
	Body   struct {
		RootCommentID int64 `json:"root_comment_id" doc:"GitHub platform id of the thread's root review comment"`
	}
}

type unhideReviewThreadInput struct {
	Owner         string `path:"owner"`
	Name          string `path:"name"`
	Number        int    `path:"number"`
	RootCommentID int64  `path:"root_comment_id"`
}

// --- handlers ----------------------------------------------------------------

// hideReviewThread records the user's intent to hide a review thread
// from the UI. It validates that the supplied root_comment_id matches
// an existing review_comment platform id on this PR before writing.
func (s *Server) hideReviewThread(
	ctx context.Context, input *hideReviewThreadInput,
) (*emptyOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}

	if input.Body.RootCommentID <= 0 {
		return nil, huma.Error400BadRequest("root_comment_id must be positive")
	}

	known, err := s.reviewCommentExistsOnMR(ctx, mrID, input.Body.RootCommentID)
	if err != nil {
		return nil, huma.Error500InternalServerError("validate root_comment_id: " + err.Error())
	}
	if !known {
		return nil, huma.Error400BadRequest(
			"root_comment_id does not match any review comment on this pull request",
		)
	}

	if err := s.db.UpsertHiddenReviewThread(
		ctx, mrID, input.Body.RootCommentID, time.Now().UTC(),
	); err != nil {
		return nil, huma.Error500InternalServerError("hide thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// unhideReviewThread clears the user's hide for a thread. Idempotent.
func (s *Server) unhideReviewThread(
	ctx context.Context, input *unhideReviewThreadInput,
) (*emptyOutput, error) {
	mrID, err := s.lookupMRID(ctx, repoNumberPathRef{
		owner: input.Owner, name: input.Name, number: input.Number,
	})
	if err != nil {
		return nil, huma.Error404NotFound("pull request not found")
	}
	if err := s.db.DeleteHiddenReviewThread(ctx, mrID, input.RootCommentID); err != nil {
		return nil, huma.Error500InternalServerError("unhide thread: " + err.Error())
	}
	return &emptyOutput{}, nil
}

// reviewCommentExistsOnMR returns true when (mrID, platformID) refers
// to a review_comment event we've synced. Used as a cheap sanity check
// for write paths that take a platform comment id. COUNT(*) always
// returns a single row (0 when nothing matches), so the only Scan
// error is a real failure.
func (s *Server) reviewCommentExistsOnMR(
	ctx context.Context, mrID, platformID int64,
) (bool, error) {
	var count int
	err := s.db.ReadDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM middleman_mr_events
		  WHERE merge_request_id = ? AND event_type = 'review_comment'
		        AND platform_id = ?`,
		mrID, platformID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
```

Notes for the engineer:
- `emptyOutput` is defined in `huma_routes_ai.go`; it's `struct{}` and is the convention for 204 responses.
- `s.db.ReadDB()` is the existing public accessor for the read-only handle (`d.ro` is unexported).

- [ ] **Step 4.4: Register the routes**

Edit `internal/server/huma_routes.go`. Find the AI-thread route block (around line 431-446) and add the two new registrations directly above it (so the hidden-thread routes group with the rest of the per-PR endpoints):

```go
	huma.Register(api, huma.Operation{
		OperationID:   "hide-review-thread",
		Method:        http.MethodPost,
		Path:          "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
		DefaultStatus: http.StatusNoContent,
	}, s.hideReviewThread)
	huma.Register(api, huma.Operation{
		OperationID:   "unhide-review-thread",
		Method:        http.MethodDelete,
		Path:          "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
		DefaultStatus: http.StatusNoContent,
	}, s.unhideReviewThread)
```

- [ ] **Step 4.5: Regenerate OpenAPI + Go client**

Run: `make api-generate`
Expected: regenerates artifacts, adds `PostReposByOwnerByNamePullsByNumberHiddenThreads*` / `DeleteReposByOwnerByNamePullsByNumberHiddenThreadsByRootCommentId*` to `client.gen.go` and `HideReviewThreadInputBody` types.

- [ ] **Step 4.6: Run the new tests**

Run: `go test ./internal/server -run TestHideThread -shuffle=on`
Expected: PASS — three tests.

- [ ] **Step 4.7: Run the whole server suite**

Run: `go test ./internal/server -shuffle=on -short`
Expected: PASS.

- [ ] **Step 4.8: Commit**

```bash
git add internal/server/huma_routes_hidden_threads.go \
        internal/server/huma_routes.go \
        internal/server/hidden_threads_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go \
        packages/ui/src/api/generated/schema.ts \
        packages/ui/src/api/generated/client.ts
git commit -m "$(cat <<'EOF'
feat(server): POST /hidden-threads to hide a review thread

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: DELETE `/hidden-threads/{root_comment_id}` and auto-unhide

**Files:**
- Modify: `internal/server/hidden_threads_e2e_test.go`

The handler and route are already in place from Task 4; this task verifies end-to-end behavior.

- [ ] **Step 5.1: Write the failing unhide / auto-unhide tests**

Append to `internal/server/hidden_threads_e2e_test.go`:

```go
func TestUnhideThreadRemovesFromActiveSet(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 5001, InReplyTo: 0, CreatedAt: now.Add(-time.Hour)},
	})

	client := setupTestClient(t, srv)

	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 5001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode())

	unhideResp, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberHiddenThreadsByRootCommentIdWithResponse(
		context.Background(), "acme", "widget", 1, 5001,
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, unhideResp.StatusCode())

	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.Equal(http.StatusOK, getResp.StatusCode())
	require.NotNil(getResp.JSON200)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	assert.Empty(*getResp.JSON200.HiddenThreadRootIds)

	// Idempotent — deleting again is a no-op 204.
	unhideResp2, err := client.HTTP.DeleteReposByOwnerByNamePullsByNumberHiddenThreadsByRootCommentIdWithResponse(
		context.Background(), "acme", "widget", 1, 5001,
	)
	require.NoError(err)
	assert.Equal(http.StatusNoContent, unhideResp2.StatusCode())
}

func TestHiddenThreadAutoUnhidesOnNewReply(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	srv, database := setupTestServer(t)
	mrID := seedPR(t, database, "acme", "widget", 1)

	// Seed only the root, hide it, then add a later reply.
	now := time.Now().UTC().Truncate(time.Second)
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 7001, InReplyTo: 0, CreatedAt: now.Add(-2 * time.Hour)},
	})

	client := setupTestClient(t, srv)
	hideResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 7001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp.StatusCode())

	// Confirm it's hidden.
	getResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp.JSON200.HiddenThreadRootIds)
	require.ElementsMatch([]int64{7001}, *getResp.JSON200.HiddenThreadRootIds)

	// New reply arrives after the hide.
	seedReviewComments(t, database, mrID, []seedReviewComment{
		{ID: 7002, InReplyTo: 7001, CreatedAt: time.Now().UTC().Add(time.Hour)},
	})

	getResp2, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp2.JSON200.HiddenThreadRootIds)
	assert.Empty(*getResp2.JSON200.HiddenThreadRootIds,
		"reply newer than hidden_at should supersede the hide")

	// Re-hide refreshes the timestamp; thread is hidden again.
	hideResp2, err := client.HTTP.PostReposByOwnerByNamePullsByNumberHiddenThreadsWithResponse(
		context.Background(), "acme", "widget", 1,
		generated.HideReviewThreadInputBody{RootCommentId: 7001},
	)
	require.NoError(err)
	require.Equal(http.StatusNoContent, hideResp2.StatusCode())

	getResp3, err := client.HTTP.GetReposByOwnerByNamePullsByNumberWithResponse(
		context.Background(), "acme", "widget", 1,
	)
	require.NoError(err)
	require.NotNil(getResp3.JSON200.HiddenThreadRootIds)
	assert.ElementsMatch([]int64{7001}, *getResp3.JSON200.HiddenThreadRootIds)
}
```

- [ ] **Step 5.2: Run the new tests**

Run: `go test ./internal/server -run TestUnhideThread\|TestHiddenThreadAutoUnhides -shuffle=on`
Expected: PASS — two tests.

- [ ] **Step 5.3: Run the whole `internal/db` and `internal/server` suites to catch regressions**

Run in parallel:
```
go test ./internal/db -shuffle=on
go test ./internal/server -shuffle=on -short
```
Expected: PASS for both.

- [ ] **Step 5.4: Commit**

```bash
git add internal/server/hidden_threads_e2e_test.go
git commit -m "$(cat <<'EOF'
test(server): end-to-end unhide and auto-unhide-on-new-reply

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Detail store — hidden-set helpers, filter, and actions

**Files:**
- Modify: `packages/ui/src/stores/detail.svelte.ts`
- Create: `packages/ui/src/stores/detail.test.ts`

- [ ] **Step 6.1: Write the failing store test**

Create `packages/ui/src/stores/detail.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { createDetailStore } from "./detail.svelte.js";
import type { MiddlemanClient } from "../types.js";
import type { PullDetail, PREvent } from "../api/types.js";

function makeReviewCommentEvent(opts: {
  id: number;
  platformId: number;
  inReplyTo?: number;
  createdAt?: string;
  path?: string;
  line?: number;
}): PREvent {
  const meta: Record<string, unknown> = {
    path: opts.path ?? "f.go",
    line: opts.line ?? 1,
    side: "RIGHT",
  };
  if (opts.inReplyTo) meta.in_reply_to = opts.inReplyTo;
  return {
    ID: opts.id,
    MergeRequestID: 1,
    PlatformID: opts.platformId,
    EventType: "review_comment",
    Author: "u",
    Summary: opts.path ?? "f.go",
    Body: "body",
    MetadataJSON: JSON.stringify(meta),
    CreatedAt: opts.createdAt ?? "2026-05-21T12:00:00Z",
    DedupeKey: `review-comment-${opts.platformId}`,
  } as PREvent;
}

function buildDetailWith(opts: {
  events: PREvent[];
  hiddenRootIds: number[];
}): PullDetail {
  return {
    merge_request: { Number: 1 } as PullDetail["merge_request"],
    events: opts.events,
    repo_owner: "acme",
    repo_name: "widget",
    platform_host: "github.com",
    worktree_links: [],
    workflow_approval: { checked: false, required: false, count: 0 },
    detail_loaded: true,
    hidden_thread_root_ids: opts.hiddenRootIds,
  } as PullDetail;
}

function makeStubClient(detail: PullDetail): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: detail, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

describe("detail store hidden-thread filter", () => {
  it("excludes hidden threads from getReviewCommentsByFilePath by default", async () => {
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 100 }), // hidden root
      makeReviewCommentEvent({ id: 2, platformId: 101, inReplyTo: 100 }),
      makeReviewCommentEvent({ id: 3, platformId: 200, path: "g.go" }), // visible
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [100] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    const map = store.getReviewCommentsByFilePath();
    expect(map.get("f.go")?.map((c) => c.id) ?? []).toEqual([]);
    expect(map.get("g.go")?.map((c) => c.id) ?? []).toEqual([200]);
  });

  it("includes hidden events with isHidden=true when toggle is on", async () => {
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 100 }),
      makeReviewCommentEvent({ id: 2, platformId: 101, inReplyTo: 100 }),
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [100] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    store.setShowHiddenThreads(true);
    const map = store.getReviewCommentsByFilePath();
    const onFile = map.get("f.go") ?? [];
    expect(onFile.map((c) => ({ id: c.id, isHidden: c.isHidden }))).toEqual([
      { id: 100, isHidden: true },
      { id: 101, isHidden: true },
    ]);
  });

  it("resolves multi-level replies to the correct root", async () => {
    // A → B → C, hide root A: all three should be hidden.
    const events = [
      makeReviewCommentEvent({ id: 1, platformId: 10 }),
      makeReviewCommentEvent({ id: 2, platformId: 11, inReplyTo: 10 }),
      makeReviewCommentEvent({ id: 3, platformId: 12, inReplyTo: 11 }),
    ];
    const detail = buildDetailWith({ events, hiddenRootIds: [10] });
    const store = createDetailStore({ client: makeStubClient(detail) });
    await store.loadDetail("acme", "widget", 1);

    const map = store.getReviewCommentsByFilePath();
    expect(map.get("f.go") ?? []).toEqual([]);
  });
});

describe("hide/unhide actions", () => {
  it("hideReviewThread mutates state optimistically and POSTs", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 50 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);

    await store.hideReviewThread(50);

    expect(store.getHiddenRootSet().has(50)).toBe(true);
    expect(client.POST).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
      expect.objectContaining({
        params: { path: { owner: "acme", name: "widget", number: 1 } },
        body: { root_comment_id: 50 },
      }),
    );
  });

  it("unhideReviewThread mutates state and DELETEs", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 60 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [60] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);

    await store.unhideReviewThread(60);

    expect(store.getHiddenRootSet().has(60)).toBe(false);
    expect(client.DELETE).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
      expect.objectContaining({
        params: {
          path: { owner: "acme", name: "widget", number: 1, root_comment_id: 60 },
        },
      }),
    );
  });

  it("hideReviewThread rolls back state on POST failure", async () => {
    const events = [makeReviewCommentEvent({ id: 1, platformId: 70 })];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    (client.POST as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      data: undefined,
      error: { detail: "boom" },
    });
    const store = createDetailStore({ client });
    await store.loadDetail("acme", "widget", 1);

    await store.hideReviewThread(70);

    expect(store.getHiddenRootSet().has(70)).toBe(false);
  });
});
```

- [ ] **Step 6.2: Run the test to confirm it fails**

Run: `cd packages/ui && bun run vitest run src/stores/detail.test.ts`
Expected: FAIL — `setShowHiddenThreads`, `hideReviewThread`, `unhideReviewThread`, `getHiddenRootSet` undefined; `PublishedReviewComment.isHidden` missing; `hidden_thread_root_ids` not threaded through.

- [ ] **Step 6.3: Extend the store**

Edit `packages/ui/src/stores/detail.svelte.ts`.

(a) Extend the `PublishedReviewComment` interface near the top:

```ts
export interface PublishedReviewComment {
  id: number;
  author: string;
  body: string;
  createdAt: string;
  path: string;
  line: number;
  startLine: number | null;
  side: "LEFT" | "RIGHT";
  commitId: string;
  htmlUrl: string;
  inReplyTo: number;
  // True when this comment belongs to a thread whose root id is in
  // the active hidden set. The card renders dimmed instead of being
  // dropped from the map when `showHiddenThreads` is true.
  isHidden: boolean;
}
```

(b) Inside `createDetailStore`, just after the `detailLoaded`/`syncGeneration` declarations, add the toggle and a memoized root map keyed by detail load:

```ts
  let showHiddenThreads = $state(false);

  // platform_id → root platform_id, computed when needed. Mirrors
  // the server walk in db.ActiveHiddenReviewThreadRoots so hidden
  // replies surface the same way the server's predicate sees them.
  function buildReviewCommentRootMap(events: PullDetail["events"]): Map<number, number> {
    const parent = new Map<number, number>();
    for (const e of events) {
      if (e.EventType !== "review_comment" || e.PlatformID == null) continue;
      try {
        const meta = JSON.parse(e.MetadataJSON ?? "{}") as { in_reply_to?: number };
        const pid = e.PlatformID as number;
        if (meta.in_reply_to && meta.in_reply_to !== pid) {
          parent.set(pid, meta.in_reply_to);
        }
      } catch {
        /* ignore */
      }
    }
    const root = new Map<number, number>();
    for (const e of events) {
      if (e.EventType !== "review_comment" || e.PlatformID == null) continue;
      let cur = e.PlatformID as number;
      for (let i = 0; i < 32; i++) {
        const p = parent.get(cur);
        if (p == null) break;
        cur = p;
      }
      root.set(e.PlatformID as number, cur);
    }
    return root;
  }
```

(c) Update `getReviewCommentsByFilePath` (currently at ~line 128) to filter and tag:

```ts
  function getReviewCommentsByFilePath(): Map<string, PublishedReviewComment[]> {
    const out = new Map<string, PublishedReviewComment[]>();
    const events = detail?.events;
    if (!events) return out;
    const hidden = getHiddenRootSet();
    const roots = buildReviewCommentRootMap(events);

    for (const e of events) {
      if (e.EventType !== "review_comment") continue;
      const raw = e.MetadataJSON;
      if (!raw) continue;
      try {
        const meta = JSON.parse(raw) as {
          path?: string;
          line?: number;
          start_line?: number;
          side?: string;
          commit_id?: string;
          html_url?: string;
          in_reply_to?: number;
        };
        const path = meta.path;
        if (!path) continue;
        const side = meta.side === "LEFT" ? "LEFT" : "RIGHT";
        const ghID = (e.PlatformID ?? 0) as number;
        if (!ghID) continue;

        const isHidden = hidden.has(roots.get(ghID) ?? ghID);
        if (isHidden && !showHiddenThreads) continue;

        const list = out.get(path) ?? [];
        list.push({
          id: ghID,
          author: e.Author,
          body: e.Body,
          createdAt: e.CreatedAt,
          path,
          line: meta.line ?? 0,
          startLine: meta.start_line ?? null,
          side,
          commitId: meta.commit_id ?? "",
          htmlUrl: meta.html_url ?? "",
          inReplyTo: meta.in_reply_to ?? 0,
          isHidden,
        });
        out.set(path, list);
      } catch {
        /* ignore */
      }
    }
    for (const [, list] of out) {
      list.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
    }
    return out;
  }
```

(d) Add the new public helpers. Find the section that returns the store API at the bottom of `createDetailStore`. Right above the return, add:

```ts
  function getHiddenRootSet(): Set<number> {
    const ids = detail?.hidden_thread_root_ids ?? [];
    return new Set(ids);
  }

  function getHiddenThreadCount(): number {
    return detail?.hidden_thread_root_ids?.length ?? 0;
  }

  function isShowingHiddenThreads(): boolean {
    return showHiddenThreads;
  }

  function setShowHiddenThreads(next: boolean): void {
    showHiddenThreads = next;
  }

  function getReviewCommentRootForPlatformID(platformID: number): number {
    const events = detail?.events ?? [];
    const roots = buildReviewCommentRootMap(events);
    return roots.get(platformID) ?? platformID;
  }

  async function hideReviewThread(rootPlatformID: number): Promise<void> {
    if (!detail) return;
    const ownerRepo = {
      owner: detail.repo_owner,
      name: detail.repo_name,
      number: detail.merge_request.Number,
    };
    const prev = detail.hidden_thread_root_ids ?? [];
    if (prev.includes(rootPlatformID)) return;
    detail = { ...detail, hidden_thread_root_ids: [...prev, rootPlatformID] } as PullDetail;
    const { error } = await apiClient.POST(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads",
      {
        params: { path: ownerRepo },
        body: { root_comment_id: rootPlatformID },
      },
    );
    if (error) {
      // Roll back
      const reverted = (detail?.hidden_thread_root_ids ?? []).filter(
        (id) => id !== rootPlatformID,
      );
      if (detail) {
        detail = { ...detail, hidden_thread_root_ids: reverted } as PullDetail;
      }
    }
  }

  async function unhideReviewThread(rootPlatformID: number): Promise<void> {
    if (!detail) return;
    const ownerRepo = {
      owner: detail.repo_owner,
      name: detail.repo_name,
      number: detail.merge_request.Number,
    };
    const prev = detail.hidden_thread_root_ids ?? [];
    if (!prev.includes(rootPlatformID)) return;
    detail = {
      ...detail,
      hidden_thread_root_ids: prev.filter((id) => id !== rootPlatformID),
    } as PullDetail;
    const { error } = await apiClient.DELETE(
      "/repos/{owner}/{name}/pulls/{number}/hidden-threads/{root_comment_id}",
      {
        params: {
          path: { ...ownerRepo, root_comment_id: rootPlatformID },
        },
      },
    );
    if (error) {
      if (detail) {
        detail = {
          ...detail,
          hidden_thread_root_ids: [...(detail.hidden_thread_root_ids ?? []), rootPlatformID],
        } as PullDetail;
      }
    }
  }
```

(e) Find the `return` object at the bottom of `createDetailStore` and add the new helpers to it. Look for the existing return shape; add these keys alongside the others:

```ts
    getReviewCommentsByFilePath,
    getHiddenRootSet,
    getHiddenThreadCount,
    isShowingHiddenThreads,
    setShowHiddenThreads,
    getReviewCommentRootForPlatformID,
    hideReviewThread,
    unhideReviewThread,
```

- [ ] **Step 6.4: Run the test to confirm it passes**

Run: `cd packages/ui && bun run vitest run src/stores/detail.test.ts`
Expected: PASS — six tests.

- [ ] **Step 6.5: Run the full UI test suite to catch type or unrelated regressions**

Run: `cd packages/ui && bun run test`
Expected: PASS (existing suite plus the new file).

- [ ] **Step 6.6: Commit**

```bash
git add packages/ui/src/stores/detail.svelte.ts packages/ui/src/stores/detail.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): detail store filters hidden review threads + hide/unhide actions

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: ReviewCommentCard — Hide button + `rc--hidden` styling

**Files:**
- Modify: `packages/ui/src/components/diff/ReviewCommentCard.svelte`
- Create: `packages/ui/src/components/diff/ReviewCommentCard.test.ts`

- [ ] **Step 7.1: Write the failing component test**

Create `packages/ui/src/components/diff/ReviewCommentCard.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ReviewCommentCard from "./ReviewCommentCard.svelte";
import { STORES_KEY } from "../../context.js";
import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

function baseComment(over: Partial<PublishedReviewComment> = {}): PublishedReviewComment {
  return {
    id: 100,
    author: "alice",
    body: "looks good",
    createdAt: "2026-05-21T10:00:00Z",
    path: "f.go",
    line: 1,
    startLine: null,
    side: "RIGHT",
    commitId: "",
    htmlUrl: "",
    inReplyTo: 0,
    isHidden: false,
    ...over,
  };
}

function withStores(stores: object) {
  return new Map<symbol, unknown>([[STORES_KEY, stores]]);
}

function makeDetailStub(over: Record<string, unknown> = {}) {
  return {
    hideReviewThread: vi.fn(async () => {}),
    unhideReviewThread: vi.fn(async () => {}),
    ...over,
  };
}

function makeDiffStub() {
  return {
    getCommits: () => [{ sha: "deadbeef" }],
    addDraftComment: vi.fn(),
  };
}

describe("ReviewCommentCard hide controls", () => {
  it("shows Hide button on a thread root", () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment(),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(screen.getByLabelText("Hide thread")).toBeTruthy();
  });

  it("does not show Hide button on a reply", () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 101, inReplyTo: 100 }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(screen.queryByLabelText("Hide thread")).toBeNull();
  });

  it("clicking Hide calls hideReviewThread with the root id", async () => {
    const detailStub = makeDetailStub();
    render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 555 }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    await fireEvent.click(screen.getByLabelText("Hide thread"));
    expect(detailStub.hideReviewThread).toHaveBeenCalledWith(555);
  });

  it("applies rc--hidden class and Unhide button when isHidden=true", async () => {
    const detailStub = makeDetailStub();
    const { container } = render(ReviewCommentCard, {
      props: {
        comment: baseComment({ id: 600, isHidden: true }),
        repoOwner: "acme",
        repoName: "widget",
        currentHeadSha: "deadbeef",
      },
      context: withStores({ detail: detailStub, diff: makeDiffStub() }),
    });
    expect(container.querySelector(".rc.rc--hidden")).toBeTruthy();
    await fireEvent.click(screen.getByLabelText("Unhide thread"));
    expect(detailStub.unhideReviewThread).toHaveBeenCalledWith(600);
  });
});
```

- [ ] **Step 7.2: Run the test to confirm it fails**

Run: `cd packages/ui && bun run vitest run src/components/diff/ReviewCommentCard.test.ts`
Expected: FAIL — no Hide button rendered; no `rc--hidden` class.

- [ ] **Step 7.3: Update `ReviewCommentCard.svelte`**

Edit `packages/ui/src/components/diff/ReviewCommentCard.svelte`.

(a) Update `getStores` destructuring at the top of the `<script>` block to pull in `detail`:

```ts
  const { diff: diffStore, detail: detailStore } = getStores();
```

(b) Below the existing `outdated` and `anchorLabel` derived blocks, add:

```ts
  const isRoot = $derived(comment.inReplyTo === 0);
  const isHidden = $derived(comment.isHidden === true);

  function onHideClick(): void {
    if (!isRoot) return;
    if (isHidden) {
      void detailStore.unhideReviewThread(comment.id);
    } else {
      void detailStore.hideReviewThread(comment.id);
    }
  }
```

(c) Update the root `<div>` opener to include the `rc--hidden` modifier:

```svelte
<div class="rc" class:rc--outdated={outdated} class:rc--reply={!!comment.inReplyTo} class:rc--hidden={isHidden}>
```

(d) Add the Hide button to the header. Place it directly after the existing reply button (the `<button ...class="rc__action" ... title="Draft a reply">`). Match the styling pattern of the reply button:

```svelte
{#if isRoot}
  <button
    type="button"
    class="rc__action rc__action--hide"
    onclick={onHideClick}
    title={isHidden ? "Show this thread again" : "Hide this thread from the review window"}
    aria-label={isHidden ? "Unhide thread" : "Hide thread"}
  >
    {#if isHidden}
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M2 8s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" stroke-linecap="round" stroke-linejoin="round"/>
        <circle cx="8" cy="8" r="2" />
      </svg>
    {:else}
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6">
        <path d="M3 3l10 10" stroke-linecap="round"/>
        <path d="M2 8s2.5-4 6-4c1.5 0 2.7.5 3.6 1.2M14 8s-2.5 4-6 4c-1.4 0-2.6-.4-3.5-1" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    {/if}
  </button>
{/if}
```

(e) At the bottom of the `<style>` block, add the hidden modifier:

```css
  .rc--hidden {
    opacity: 0.55;
    border-left-color: var(--text-muted);
  }
```

- [ ] **Step 7.4: Run the test to confirm it passes**

Run: `cd packages/ui && bun run vitest run src/components/diff/ReviewCommentCard.test.ts`
Expected: PASS — four tests.

- [ ] **Step 7.5: Run the diff component suite to catch any breakage**

Run: `cd packages/ui && bun run vitest run src/components/diff`
Expected: PASS.

- [ ] **Step 7.6: Commit**

```bash
git add packages/ui/src/components/diff/ReviewCommentCard.svelte \
        packages/ui/src/components/diff/ReviewCommentCard.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): hide/unhide button on root ReviewCommentCard

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: `DiffFile.svelte` — propagate `isHidden` through dynamic mounts

**Files:**
- Modify: `packages/ui/src/components/diff/DiffFile.svelte`
- Modify: `packages/ui/src/components/diff/RenderedMarkdownView.svelte`

`getReviewCommentsByFilePath` now stamps `isHidden` on each `PublishedReviewComment`. `ReviewCommentCard` reads `comment.isHidden` directly via $props. So mostly no caller change is needed — but two spots create cards via the imperative `mount()` API and pass a snapshot of the comment shape. Verify their `comment` payloads include the new field.

- [ ] **Step 8.1: Audit and adjust `DiffFile.svelte`**

Open `packages/ui/src/components/diff/DiffFile.svelte`. The three `<ReviewCommentCard ... comment={rc} ... />` blocks at lines ~876, ~900, ~997 pass `rc` directly — these already carry the new `isHidden` field since they come straight from `getReviewCommentsByFilePath()`. No code change required, but skim each site to confirm there's no manual destructuring of `rc` that would drop the field.

- [ ] **Step 8.2: Audit and adjust `RenderedMarkdownView.svelte`**

Open `packages/ui/src/components/diff/RenderedMarkdownView.svelte`. Find the dynamic `mount(ReviewCommentCard, ...)` call around line 423. The current call shape is:

```ts
const inst = mount(ReviewCommentCard, {
  target: anchorEl,
  props: { comment, repoOwner, repoName, currentHeadSha },
});
```

`comment` already comes from `getReviewCommentsByFilePath()`, so it includes `isHidden`. Confirm — and if any local mapping strips fields, restore them. If a snapshot must be made for reactivity reasons, include `isHidden`:

```ts
const inst = mount(ReviewCommentCard, {
  target: anchorEl,
  props: { comment: { ...comment }, repoOwner, repoName, currentHeadSha },
});
```

- [ ] **Step 8.3: Run the diff/markdown component tests**

Run: `cd packages/ui && bun run vitest run src/components/diff`
Expected: PASS.

- [ ] **Step 8.4: Commit (only if any code actually changed)**

If neither file required changes, skip this commit. Otherwise:

```bash
git add packages/ui/src/components/diff/DiffFile.svelte \
        packages/ui/src/components/diff/RenderedMarkdownView.svelte
git commit -m "$(cat <<'EOF'
chore(ui): preserve isHidden field through ReviewCommentCard mounts

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: EventTimeline — hide thread filtering + Show Hidden toggle

**Files:**
- Modify: `packages/ui/src/components/detail/EventTimeline.svelte`
- Modify: `packages/ui/src/components/detail/EventTimeline.test.ts`

- [ ] **Step 9.1a: Wrap existing render calls in a default-context helper**

Open `packages/ui/src/components/detail/EventTimeline.test.ts`. After my Task 9 changes, `EventTimeline` calls `getStores()` unconditionally, so every `render(EventTimeline, …)` must provide at least an empty detail stub via context.

Replace the existing imports at the top of the file:

```ts
import { cleanup, render, screen, fireEvent } from "@testing-library/svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import EventTimeline from "./EventTimeline.svelte";
import { STORES_KEY } from "../../context.js";
import type { PREvent } from "../../api/types.js";
```

Just below the existing `makeEvent` helper, add:

```ts
function timelineDetailStub(opts: { hidden?: number[]; showing?: boolean } = {}) {
  const hidden = opts.hidden ?? [];
  return {
    getHiddenRootSet: () => new Set<number>(hidden),
    getHiddenThreadCount: () => hidden.length,
    isShowingHiddenThreads: () => opts.showing ?? false,
    setShowHiddenThreads: vi.fn(),
    getReviewCommentRootForPlatformID: (pid: number) => pid,
    hideReviewThread: vi.fn(async () => {}),
    unhideReviewThread: vi.fn(async () => {}),
  };
}

function renderTimeline(
  events: PREvent[],
  detail: ReturnType<typeof timelineDetailStub> = timelineDetailStub(),
) {
  return render(EventTimeline, {
    props: { events, repoOwner: "acme", repoName: "widget" },
    context: new Map<symbol, unknown>([[STORES_KEY, { detail }]]),
  });
}
```

Now rewrite the existing `render(EventTimeline, { props: { events: [makeEvent()] } });` call sites (plus any others that pass `{ events: […] }`) to use `renderTimeline(events)` instead. The behavior is identical for those tests because the default stub reports zero hidden threads.

- [ ] **Step 9.1b: Write the new failing component tests**

Append to `packages/ui/src/components/detail/EventTimeline.test.ts`:

```ts
describe("EventTimeline hidden-thread filtering", () => {
  afterEach(() => {
    cleanup();
  });

  it("skips hidden review_comment events when showing is false", () => {
    const events: PREvent[] = [
      makeEvent({
        ID: 1, PlatformID: 500, EventType: "review_comment",
        Body: "hidden body",
        MetadataJSON: JSON.stringify({ path: "f.go", line: 1, side: "RIGHT" }),
        DedupeKey: "review-comment-500",
        Summary: "f.go",
      }),
      makeEvent({
        ID: 2, PlatformID: 600, EventType: "review_comment",
        Body: "visible body",
        MetadataJSON: JSON.stringify({ path: "g.go", line: 2, side: "RIGHT" }),
        DedupeKey: "review-comment-600",
        Summary: "g.go",
      }),
    ];
    const { container } = renderTimeline(events, timelineDetailStub({ hidden: [500] }));
    expect(container.textContent).not.toContain("hidden body");
    expect(container.textContent).toContain("visible body");
  });

  it("does not render Show Hidden toggle when there are zero hidden threads", () => {
    const { container } = renderTimeline([]);
    expect(container.querySelector(".hidden-toggle")).toBeNull();
  });

  it("renders Show Hidden (N) toggle when there is a hidden thread", () => {
    renderTimeline([], timelineDetailStub({ hidden: [123] }));
    expect(screen.getByText(/Show hidden/).textContent).toContain("1");
  });

  it("clicking Show Hidden calls setShowHiddenThreads(true)", async () => {
    const detail = timelineDetailStub({ hidden: [123] });
    renderTimeline([], detail);
    await fireEvent.click(screen.getByText(/Show hidden/));
    expect(detail.setShowHiddenThreads).toHaveBeenCalledWith(true);
  });
});
```

- [ ] **Step 9.2: Run the test to confirm it fails**

Run: `cd packages/ui && bun run vitest run src/components/detail/EventTimeline.test.ts`
Expected: FAIL — `EventTimeline` doesn't filter hidden events; no `.hidden-toggle` element exists.

- [ ] **Step 9.3: Update `EventTimeline.svelte`**

Edit `packages/ui/src/components/detail/EventTimeline.svelte`.

(a) At the top of the `<script>` block, replace the prop destructuring to also pull in the detail store and parse review-comment metadata's platform id:

```ts
  import type { IssueEvent, PREvent } from "../../api/types.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import { timeAgo } from "../../utils/time.js";
  import { copyToClipboard } from "../../utils/clipboard.js";
  import { getStores } from "../../context.js";

  interface Props {
    events: Array<PREvent | IssueEvent>;
    repoOwner?: string;
    repoName?: string;
  }

  const { events, repoOwner, repoName }: Props = $props();
  const { detail: detailStore } = getStores();
```

(b) Replace the `visibleEvents` derived block (currently filters mechanics only) with one that also filters hidden review threads:

```ts
  const hiddenSet = $derived(detailStore.getHiddenRootSet());
  const showingHidden = $derived(detailStore.isShowingHiddenThreads());
  const hiddenCount = $derived(detailStore.getHiddenThreadCount());

  function isReviewCommentInHiddenThread(event: PREvent | IssueEvent): boolean {
    if (event.EventType !== "review_comment") return false;
    if (event.PlatformID == null) return false;
    const root = detailStore.getReviewCommentRootForPlatformID(event.PlatformID as number);
    return hiddenSet.has(root);
  }

  const visibleEvents = $derived(
    events.filter((e) => {
      if (!showMechanics && isMechanics(e.EventType)) return false;
      if (!showingHidden && isReviewCommentInHiddenThread(e)) return false;
      return true;
    }),
  );
```

(c) Add a hidden toggle button. Place it inside the existing mechanics toggle row so both controls live side-by-side. Locate the block that renders `<div class="mechanics-toggle">` and convert it to a wrapper that may also host the hidden toggle. Replace:

```svelte
  {#if mechanicsCount > 0}
    <div class="mechanics-toggle">
      <button … >
        {showMechanics ? "Hide" : "Show"} mechanics
        <span class="mechanics-toggle__count">{mechanicsCount}</span>
      </button>
    </div>
  {/if}
```

with:

```svelte
  {#if mechanicsCount > 0 || hiddenCount > 0}
    <div class="timeline-toggles">
      {#if mechanicsCount > 0}
        <button
          type="button"
          class="toggle-pill"
          class:toggle-pill--on={showMechanics}
          onclick={toggleMechanics}
          title="Show or hide commit and force-push events"
        >
          {showMechanics ? "Hide" : "Show"} mechanics
          <span class="toggle-pill__count">{mechanicsCount}</span>
        </button>
      {/if}
      {#if hiddenCount > 0}
        <button
          type="button"
          class="toggle-pill hidden-toggle"
          class:toggle-pill--on={showingHidden}
          onclick={() => detailStore.setShowHiddenThreads(!showingHidden)}
          title={showingHidden ? "Hide these threads again" : "Show threads you've hidden"}
        >
          {showingHidden ? "Hide hidden" : "Show hidden"}
          <span class="toggle-pill__count">{hiddenCount}</span>
        </button>
      {/if}
    </div>
  {/if}
```

(d) In the `<style>` block, replace the `.mechanics-toggle*` selectors with the renamed-to-`.timeline-toggles` / `.toggle-pill*` equivalents:

```css
  .timeline-toggles {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    padding: 0 0 8px;
  }

  .toggle-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    padding: 3px 10px;
    border-radius: 999px;
    color: var(--text-muted);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    cursor: pointer;
  }

  .toggle-pill:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .toggle-pill--on {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .toggle-pill__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }
```

Remove the obsolete `.mechanics-toggle*` rules to keep styles tidy.

- [ ] **Step 9.4: Run the test to confirm it passes**

Run: `cd packages/ui && bun run vitest run src/components/detail/EventTimeline.test.ts`
Expected: PASS — pre-existing tests + the two new ones.

- [ ] **Step 9.5: Commit**

```bash
git add packages/ui/src/components/detail/EventTimeline.svelte \
        packages/ui/src/components/detail/EventTimeline.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): EventTimeline filters hidden threads + show-hidden toggle

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: DiffToolbar — Show Hidden (N) toggle for diff + rendered-markdown views

**Files:**
- Modify: `packages/ui/src/components/diff/DiffToolbar.svelte`

`DiffToolbar` is the shared parent for both the diff view and (via `DiffFile` → `RenderedMarkdownView`) the rendered-markdown view, so one toggle here covers both surfaces.

- [ ] **Step 10.1: Update `DiffToolbar.svelte`**

Edit `packages/ui/src/components/diff/DiffToolbar.svelte`.

(a) Update the `getStores` destructuring at the top to also pull in `detail`:

```ts
  const { diff, detail: detailStore } = getStores();
```

(b) Add derived state below the existing `pendingCount` / `draftEvent`:

```ts
  const hiddenCount = $derived(detailStore.getHiddenThreadCount());
  const showingHidden = $derived(detailStore.isShowingHiddenThreads());
```

(c) Add a new toolbar group rendered only when `hiddenCount > 0`. Place it inside the right-aligned group, just before the refresh button. Locate:

```svelte
  <div class="toolbar-group toolbar-group--right">
    <button
      type="button"
      class="refresh-btn"
```

and insert the new control directly above the existing refresh button (still inside the same `.toolbar-group--right`):

```svelte
    {#if hiddenCount > 0}
      <button
        type="button"
        class="hidden-toggle"
        class:hidden-toggle--on={showingHidden}
        title={showingHidden ? "Hide these threads again" : "Show threads you've hidden"}
        onclick={() => detailStore.setShowHiddenThreads(!showingHidden)}
      >
        {showingHidden ? "Hide hidden" : "Show hidden"}
        <span class="hidden-toggle__count">{hiddenCount}</span>
      </button>
    {/if}
```

(d) Add matching style rules at the bottom of the `<style>` block:

```css
  .hidden-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    padding: 3px 10px;
    border-radius: 999px;
    color: var(--text-muted);
    background: var(--bg-inset);
    border: 1px solid var(--border-muted);
    cursor: pointer;
  }

  .hidden-toggle:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .hidden-toggle--on {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }

  .hidden-toggle__count {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
  }
```

- [ ] **Step 10.2: Run the diff component test suite (no new test file added — toolbar wiring is exercised by the existing diff visual flow)**

Run: `cd packages/ui && bun run vitest run src/components/diff`
Expected: PASS.

- [ ] **Step 10.3: Type-check the frontend**

Run: `cd packages/ui && bun run typecheck`
Expected: PASS (no TypeScript errors introduced).

- [ ] **Step 10.4: Commit**

```bash
git add packages/ui/src/components/diff/DiffToolbar.svelte
git commit -m "$(cat <<'EOF'
feat(ui): DiffToolbar Show Hidden (N) toggle for diff + rendered-markdown

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Reset `showHiddenThreads` on PR navigation

**Files:**
- Modify: `packages/ui/src/stores/detail.svelte.ts`
- Modify: `packages/ui/src/stores/detail.test.ts`

Per the spec: "Lives in memory only; resets to off on PR navigation."

- [ ] **Step 11.1: Add a failing test**

Append to `packages/ui/src/stores/detail.test.ts`:

```ts
describe("showHiddenThreads lifecycle", () => {
  it("resets to off when the user navigates to another PR", async () => {
    const events: PREvent[] = [];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const client = makeStubClient(detail);
    const store = createDetailStore({ client });

    await store.loadDetail("acme", "widget", 1);
    store.setShowHiddenThreads(true);
    expect(store.isShowingHiddenThreads()).toBe(true);

    await store.loadDetail("acme", "widget", 2);
    expect(store.isShowingHiddenThreads()).toBe(false);
  });

  it("also resets on clearDetail", async () => {
    const events: PREvent[] = [];
    const detail = buildDetailWith({ events, hiddenRootIds: [] });
    const store = createDetailStore({ client: makeStubClient(detail) });

    await store.loadDetail("acme", "widget", 1);
    store.setShowHiddenThreads(true);
    store.clearDetail();
    expect(store.isShowingHiddenThreads()).toBe(false);
  });
});
```

- [ ] **Step 11.2: Run the test to confirm it fails**

Run: `cd packages/ui && bun run vitest run src/stores/detail.test.ts -t "showHiddenThreads lifecycle"`
Expected: FAIL — state persists across `loadDetail`.

- [ ] **Step 11.3: Wire the reset**

Edit `packages/ui/src/stores/detail.svelte.ts`.

In `loadDetail`, near the top where `loading = true; …; storeError = null;` is set, also reset `showHiddenThreads = false;`.

In `clearDetail`, add `showHiddenThreads = false;` alongside the other resets.

Concrete diff in `loadDetail`:

```ts
    loading = true;
    syncing = false;
    storeError = null;
    detail = null;
    detailLoaded = false;
    showHiddenThreads = false;
```

Concrete diff in `clearDetail`:

```ts
  function clearDetail(): void {
    ++syncGeneration;
    detail = null;
    loading = false;
    syncing = false;
    storeError = null;
    detailLoaded = false;
    showHiddenThreads = false;
  }
```

- [ ] **Step 11.4: Run the test to confirm it passes**

Run: `cd packages/ui && bun run vitest run src/stores/detail.test.ts`
Expected: PASS — entire file.

- [ ] **Step 11.5: Commit**

```bash
git add packages/ui/src/stores/detail.svelte.ts packages/ui/src/stores/detail.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): reset showHiddenThreads on PR navigation / clearDetail

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Full-stack smoke verification

**Files:** (none modified)

- [ ] **Step 12.1: Run the full Go suite**

Run: `make test`
Expected: PASS.

- [ ] **Step 12.2: Run lint**

Run: `make lint`
Expected: PASS.

- [ ] **Step 12.3: Run the frontend test + type-check**

Run: `cd packages/ui && bun run test && bun run typecheck`
Expected: PASS.

- [ ] **Step 12.4: Manual UI verification**

This is mandatory per CLAUDE.md "For UI or frontend changes, start the dev server and use the feature in a browser before reporting the task as complete."

In two terminals:

```bash
make dev          # terminal A
make frontend-dev # terminal B
```

Steps to verify:

1. Open a PR that has at least one published review thread (sync from GitHub or use a fixture PR seeded via the API).
2. In the diff view, click the new Hide icon on a thread's root card. The card vanishes. The "Show hidden (1)" toggle appears in `DiffToolbar`.
3. Open the Activity tab. The same thread's events are gone, and "Show hidden (1)" also appears next to "Show mechanics."
4. Toggle "Show hidden" on (either pill). The thread re-renders dimmed with an Unhide button.
5. Click Unhide; the card returns to normal and the toggle disappears.
6. Re-hide; navigate away and back to the PR; the thread is still hidden but the toggle is collapsed (showHiddenThreads reset to off).
7. Trigger a sync that delivers a new reply on the hidden thread (or insert one manually via the DB). After the next detail refresh, the thread re-appears in the inline view and the "Show hidden (N)" count drops to 0.

If any of these do not behave as expected, treat the step as a failure and revisit the related task.

- [ ] **Step 12.5: Final tidy commit (only if anything was adjusted during verification)**

If no fixes were needed during 12.4, skip. Otherwise commit using a focused subject describing the fix.

---

## Spec Coverage Audit

Walking through `docs/superpowers/specs/2026-05-21-pr-comment-hiding-design.md`:

- Data model migration → Task 1 (Steps 1.3–1.4).
- Thread-granularity hide (parent + replies vanish together) → Task 6 (`buildReviewCommentRootMap` + filter) and Task 9 (timeline filter via `getReviewCommentRootForPlatformID`).
- Hide affects both the inline view and the activity timeline → Tasks 7, 8, 9 (cards + timeline).
- Persistence across reloads + auto-unhide on new reply → Tasks 1, 2, 5 (DB + predicate + e2e).
- `Show hidden (N)` toggle to reverse → Tasks 9, 10.
- POST/DELETE API + 400/404 cases → Tasks 4, 5.
- Server-side predicate with multi-level replies → Task 2 (test seeds A→B chain; `ActiveHiddenReviewThreadRoots` walks the chain).
- Frontend store filter + `isHidden` flag + optimistic actions with revert → Task 6.
- Two render locations for the Show Hidden toggle → Tasks 9 and 10.
- Reset on PR navigation → Task 11.
- Test coverage (Go + frontend) → Tasks 1–6, 7, 9, 11.
- Future GitHub-resolve seam → documented only (not implemented), per spec.

No gaps.

## Placeholder Audit

Searched the plan for: TBD, TODO, "fill in", "appropriate error handling", "etc". None found in non-test prose. The one `placeholder` block in Task 2 Step 2.1 was rewritten in-place with the working test body — engineer reading the section sequentially must use the second, complete test function (the first lines are explicitly marked as a placeholder to replace).

## Type / Name Consistency

- `HiddenReviewThread` (Go type) — used consistently across Tasks 1, 2, 4.
- `RootPlatformCommentID` (Go) / `root_comment_id` (JSON) — paired everywhere (Tasks 1, 3, 4, 5).
- `ActiveHiddenReviewThreadRoots` (Go) → `hidden_thread_root_ids` (JSON) → `HiddenThreadRootIDs` (Go field) — naming triple verified (Tasks 2, 3, 5, 6).
- `hideReviewThread` / `unhideReviewThread` (TS store) — same names in Tasks 6, 7, 9.
- `showHiddenThreads` / `setShowHiddenThreads` / `isShowingHiddenThreads` — all three appear together (Tasks 6, 9, 10, 11).
- `isHidden` field on `PublishedReviewComment` — defined Task 6, consumed Task 7.
- `getReviewCommentRootForPlatformID` — defined Task 6, consumed Task 9.

No drift detected.
