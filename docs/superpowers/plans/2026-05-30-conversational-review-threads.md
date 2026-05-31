# Conversational Review Threads ("Ask Claude") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a reviewer continue the conversation with the agent inside a review thread: an explicit "Reply & ask Claude" sends the thread to a read-only `steer` turn, Claude replies as a normal thread comment, and asked comments get a subtle marker.

**Architecture:** A read-only `steer` action in the existing `SessionRunner` (reuses the shared `--resume` session; no edit tools); a `/ask` endpoint that persists the reviewer's comment then kicks a steer turn; a persisted `sent_to_agent` marker on comments; the store + card wiring (with Ask/Apply disabled while a turn runs).

**Tech Stack:** Go + SQLite (numbered migrations) + Huma v2; the generated client; Svelte 5 runes + Vitest.

**Spec:** `docs/superpowers/specs/2026-05-30-conversational-review-threads-design.md`. **Depends on:** Phases 1a/1b/2a/2b + the SessionRunner timeout fix — all on branch `serve-local-repo-comments`.

---

## Conventions (every task)

- Commit per task (conventional message) ending with: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Stage EXPLICIT paths only (NEVER `git add -A`/`.` — untracked HOME dotfiles must not be committed). No `--no-verify`. No push/PR/merge/branch-change.
- Go tests: `-shuffle=on`, never `-v`, never `-count=1`; testify (`require`/`assert`, local `require := require.New(t)` for >3 assertions); no `t.Fatal`/`t.Error`. The `internal/server` suite needs `tmux` (the COMMAND SANDBOX BLOCKS it) → run Go server tests with the Bash tool's `dangerouslyDisableSandbox: true`.
- Frontend from `frontend/`: tests `cd frontend && bunx vitest run [filter]`; **typecheck `cd frontend && bun run check`** (svelte-check — `bun run build` is only `vite build`, NOT a typecheck). Use `bun`, never `npm`.
- `make api-generate` needs `dangerouslyDisableSandbox: true` + `GOCACHE="$HOME/.cache/go-build"`; then `GOCACHE="$HOME/.cache/go-build" go generate ./internal/apiclient/generated`.
- **Trust `go build`/`go test`/`bun run check`/`bunx vitest run`, NOT the IDE diagnostics panel** (it emits false cross-file/post-regen/"not a type" errors here).
- No emojis.

## File structure

- Modify: `internal/db/queries_review_threads.go` (comment struct/scans/SELECTs + `MarkReviewThreadCommentSentToAgent`).
- Create: `internal/db/migrations/000022_add_review_thread_comment_sent_to_agent.{up,down}.sql`.
- Modify: `internal/aireview/sessions.go` (`buildSessionPrompt` `steer` branch).
- Test: `internal/aireview/sessions_discuss_test.go` (append a steer test — it already has the recording-fake + `allowedToolsArg` helper).
- Modify: `internal/server/huma_routes_review_threads.go` (`kickoffReviewTurn` `message` param; `/ask` handler + route + input type; `sent_to_agent` on the comment response + its two mapping sites).
- Test: `internal/server/review_threads_e2e_test.go` (append `/ask` e2e).
- Regen artifacts (4) after the server changes.
- Modify: `packages/ui/src/stores/reviewThreads.svelte.ts` (`ask`); `packages/ui/src/components/diff/ReviewThreadCard.svelte` ("Reply & ask Claude" + busy-disable + marker).
- Test: `packages/ui/src/stores/reviewThreads.svelte.test.ts`, `packages/ui/src/components/diff/ReviewThreadCard.test.ts`.

---

### Task 1: DB — `sent_to_agent` column + marker query

**Files:** `internal/db/migrations/000022_add_review_thread_comment_sent_to_agent.{up,down}.sql` (create); `internal/db/queries_review_threads.go` (modify); `internal/db/queries_review_threads_test.go` (append).

- [ ] **Step 1: Migration files.** Create `000022_add_review_thread_comment_sent_to_agent.up.sql`:
```sql
-- Mark which review-thread comments were sent to the agent (an "Ask Claude"
-- reply), so the UI can flag them without changing the conversation flow.
ALTER TABLE middleman_review_thread_comments
    ADD COLUMN sent_to_agent BOOLEAN NOT NULL DEFAULT 0;
```
and `000022_add_review_thread_comment_sent_to_agent.down.sql`:
```sql
ALTER TABLE middleman_review_thread_comments DROP COLUMN sent_to_agent;
```
(Numbered migrations in `internal/db/migrations/` are auto-applied; no code registration needed. Follow `context/db-migrations.md`. `DROP COLUMN` needs SQLite ≥3.35, which modernc provides; if the repo's down-migration convention differs, follow it.)

- [ ] **Step 2: Write the failing DB test.** Append to `internal/db/queries_review_threads_test.go` (reuse the existing local-MR seeding helper the other review-thread tests use — find it via the existing `CreateReviewThreads` test):
```go
func TestMarkReviewThreadCommentSentToAgent(t *testing.T) {
	require := require.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedLocalMRForThreads(t, d) // the helper the sibling tests use
	created, err := d.CreateReviewThreads(ctx, mrID, []NewReviewThread{
		{Path: "a.go", Side: "RIGHT", Line: 12, CommitSHA: "abc", Body: "rename this"},
	})
	require.NoError(err)
	tid := created[0].ID

	// A plain reply defaults to not-sent.
	plain, err := d.AddReviewThreadComment(ctx, tid, "user", "just a note", nil)
	require.NoError(err)
	require.False(plain.SentToAgent)

	// Marking flips it; the re-read reflects it.
	require.NoError(d.MarkReviewThreadCommentSentToAgent(ctx, plain.ID))
	got, err := d.ListReviewThreadComments(ctx, tid)
	require.NoError(err)
	var marked *ReviewThreadComment
	for i := range got {
		if got[i].ID == plain.ID {
			marked = &got[i]
		}
	}
	require.NotNil(marked)
	require.True(marked.SentToAgent)
}
```

- [ ] **Step 3: Run → FAIL** — `go test ./internal/db -run TestMarkReviewThreadCommentSentToAgent -shuffle=on` (FAIL: `SentToAgent` field / `MarkReviewThreadCommentSentToAgent` undefined).

- [ ] **Step 4: Implement.** In `internal/db/queries_review_threads.go`:
  - Add to the `ReviewThreadComment` struct (after `TurnID *int64`): `SentToAgent bool`.
  - In all THREE comment SELECTs, append `sent_to_agent` to the column list: `getReviewThreadComment` (`SELECT id, thread_id, author, body, turn_id, created_at, sent_to_agent FROM ...`), `ListReviewThreadCommentsForMR` (`SELECT c.id, c.thread_id, c.author, c.body, c.turn_id, c.created_at, c.sent_to_agent FROM ...`), and `ListReviewThreadComments` (`SELECT id, thread_id, author, body, turn_id, created_at, sent_to_agent FROM ...`).
  - In `scanReviewThreadComment`, scan it (avoid relying on driver int→bool):
```go
func scanReviewThreadComment(row scanner) (ReviewThreadComment, error) {
	var c ReviewThreadComment
	var turnID sql.NullInt64
	var sentToAgent int64
	err := row.Scan(&c.ID, &c.ThreadID, &c.Author, &c.Body, &turnID, &c.CreatedAt, &sentToAgent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewThreadComment{}, err
		}
		return ReviewThreadComment{}, fmt.Errorf("scan comment: %w", err)
	}
	if turnID.Valid {
		c.TurnID = &turnID.Int64
	}
	c.SentToAgent = sentToAgent != 0
	return c, nil
}
```
  - Add the marker query (near `SetReviewThreadStatus`):
```go
// MarkReviewThreadCommentSentToAgent flags a comment as one that engaged
// the agent (an "Ask Claude" reply).
func (d *DB) MarkReviewThreadCommentSentToAgent(ctx context.Context, id int64) error {
	_, err := d.rw.ExecContext(ctx,
		`UPDATE middleman_review_thread_comments SET sent_to_agent = 1 WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 5: Run → PASS** — `go test ./internal/db -run TestMarkReviewThreadCommentSentToAgent -shuffle=on`; then `go test ./internal/db -shuffle=on` (whole package green); `go build ./...` clean.

- [ ] **Step 6: Commit**
```bash
git add internal/db/migrations/000022_add_review_thread_comment_sent_to_agent.up.sql \
        internal/db/migrations/000022_add_review_thread_comment_sent_to_agent.down.sql \
        internal/db/queries_review_threads.go internal/db/queries_review_threads_test.go
git commit -m "feat(db): sent_to_agent marker on review-thread comments"
```

---

### Task 2: Runner — read-only `steer` turn

**Files:** `internal/aireview/sessions.go` (modify `buildSessionPrompt`); `internal/aireview/sessions_discuss_test.go` (append).

The gating in `runTurn` (`if in.Action == "apply" || in.Action == "" { + Edit,Write,MultiEdit,Bash }`) already leaves `steer` read-only (Read/Glob/Grep + the `mcp__middleman__*` tools when MCP is set) — **no gating change needed**. Only `buildSessionPrompt` gains a `steer` branch.

- [ ] **Step 1: Write the failing test.** Append to `internal/aireview/sessions_discuss_test.go` (it already defines `setupRecordingSessionTest`, `waitTurnDone`, and `allowedToolsArg`):
```go
func TestSteerTurnIsReadOnlyAndCarriesTheMessage(t *testing.T) {
	require := require.New(t)
	database, runner, tmp, sess, argsFile := setupRecordingSessionTest(t)
	ctx := context.Background()

	res, err := runner.SubmitTurn(ctx, SubmitTurnInput{
		SessionID:       sess.ID,
		WorktreePath:    tmp,
		IsFirstTurn:     false,
		Action:          "steer",
		UserTurnType:    "user_message",
		UserTurnContent: "Can you clarify why this needs a mutex?",
		Threads: []ThreadContext{
			{ID: 1, Path: "a.go", Line: 12, Side: "RIGHT", RootComment: "rename this"},
		},
		MCP: &MCPConfig{Binary: "/bin/true", BaseURL: "http://127.0.0.1:8091", Owner: "local", Name: "demo", Number: int(sess.ID)},
	})
	require.NoError(err)
	turn := waitTurnDone(t, database, res.ResponseTurn.ID)
	require.Equal("done", turn.Status, "raw=%s err=%s", turn.RawJSON, turn.Error)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	require.Contains(string(args), "--mcp-config")
	// steer is read-only: exact gating, no Edit/Write/Bash.
	require.Equal(
		"Read,Glob,Grep,mcp__middleman__list_threads,mcp__middleman__get_thread,mcp__middleman__reply_to_thread",
		allowedToolsArg(t, argsFile),
	)
	// The reviewer's message is carried in the prompt (the -p arg).
	require.Contains(string(args), "Can you clarify why this needs a mutex?")
}
```

- [ ] **Step 2: Run → FAIL** — `go test ./internal/aireview -run TestSteerTurn -shuffle=on`. (It will likely FAIL on the message-in-prompt assertion: without a `steer` branch, `buildSessionPrompt` falls through to the legacy path which, for a non-first turn, returns `UserTurnContent` — so the message MAY already be present, but make sure the test is red first; if the legacy fall-through happens to pass, the steer branch in Step 3 still makes the prompt correct/explicit. The gating assertion passes already since steer is read-only. Confirm the test's behavior before/after.)

> Note: a non-first legacy turn returns bare `UserTurnContent`, so the message-substring assertion could pass even pre-change. The point of Step 3 is a *purpose-built* steer prompt (worktree context + thread + "discussion only, reply via reply_to_thread"). If the test is green before Step 3 purely via the legacy fall-through, still implement Step 3 — and add `require.Contains(string(args), "reply_to_thread")` to the test so it genuinely exercises the steer branch (the legacy path doesn't mention the tool).

- [ ] **Step 3: Implement the `steer` branch** in `buildSessionPrompt` (sessions.go), inside the `switch in.Action` block, after the `case "apply":` block and before the closing `}` of the switch:
```go
	case "steer":
		var b strings.Builder
		writeWorktreeContext(&b, in)
		b.WriteString("\n")
		b.WriteString("The reviewer replied in a review thread. Read the relevant code, " +
			"respond to continue the discussion, and call the reply_to_thread tool (thread_id + body) " +
			"with your reply. Do not change any files — this is discussion only.\n\n")
		b.WriteString(formatThreads(in.Threads))
		b.WriteString("\nThe reviewer's message:\n")
		b.WriteString(in.UserTurnContent)
		return b.String()
```
(Add `require.Contains(string(args), "reply_to_thread")` to the test per the Step-2 note so it exercises this branch.)

- [ ] **Step 4: Run → PASS** — `go test ./internal/aireview -run TestSteerTurn -shuffle=on`; then `go test ./internal/aireview -shuffle=on` (whole package green); `go build ./...` clean.

- [ ] **Step 5: Commit**
```bash
git add internal/aireview/sessions.go internal/aireview/sessions_discuss_test.go
git commit -m "feat(aireview): read-only steer turn (continue a thread discussion)"
```

---

### Task 3: Server — `kickoffReviewTurn` gains a `message` param (steer support)

**Files:** `internal/server/huma_routes_review_threads.go` (modify). Pure refactor enabling steer; existing server tests must stay green.

- [ ] **Step 1: Extend `kickoffReviewTurn`.** Change its signature to take a trailing `message string`, set the steer verb/content, and skip the optimistic status for steer. Replace the verb/content/SubmitTurn/status section (sessions of `kickoffReviewTurn`) so it reads:
```go
func (s *Server) kickoffReviewTurn(
	ctx context.Context, owner, name string, number int,
	action string, threads []db.ReviewThread, message string,
) error {
	// ... unchanged: sessionRunner nil-check, resolveLocalWorktree, ensureWorktreeSession,
	//     busy gate, tcs build, base/exe ...

	// discuss = read-only review_feedback; apply = user_message (may edit);
	// steer = read-only user_message continuation carrying the reviewer's message.
	verb := "review_feedback"
	if action == "apply" || action == "steer" {
		verb = "user_message"
	}
	content := actionMessage(action, tcs)
	if action == "steer" {
		content = message
	}
	if _, err := s.sessionRunner.SubmitTurn(ctx, aireview.SubmitTurnInput{
		SessionID: sess.ID, WorktreePath: w.Path, Branch: w.Branch,
		BaseRef: base.Ref, BaseSHA: base.SHA, HeadSHA: w.HeadSHA,
		UserTurnType: verb, UserTurnContent: content, IsFirstTurn: isFirst,
		Action: action, Threads: tcs,
		MCP: &aireview.MCPConfig{Binary: exe, BaseURL: s.selfBaseURL(), Owner: owner, Name: name, Number: number},
	}); err != nil {
		return huma.Error500InternalServerError("submit turn: " + err.Error())
	}
	// steer continues an existing discussion — leave thread status unchanged.
	if action != "steer" {
		target := "discussed"
		if action == "apply" {
			target = "applied"
		}
		for _, t := range threads {
			_ = s.db.SetReviewThreadStatus(ctx, t.ID, target)
		}
	}
	return nil
}
```
(Keep the function's earlier half — the nil-check through `exe` — exactly as it is now.)

- [ ] **Step 2: Update the existing call sites** to pass `""` as the new `message` arg. There are calls in `createReviewThreads` (two: the `discuss-first` and `act-immediately` cases), `applyReviewThread`, and `applyAllReviewThreads`. Each becomes e.g. `s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "discuss", created, "")` and `..., "apply", ..., "")`. Find them with `grep -n "kickoffReviewTurn(" internal/server/huma_routes_review_threads.go` and add `, ""` to each.

- [ ] **Step 3: Verify no behavior change.** `go build ./...` clean; `go test ./internal/server -run 'TestAPIReviewThreads' -shuffle=on` (UNSANDBOXED — tmux) → still green (discuss/apply content/verb/status are unchanged for the `""`-message callers).

- [ ] **Step 4: Commit**
```bash
git add internal/server/huma_routes_review_threads.go
git commit -m "refactor(server): kickoffReviewTurn carries a message + skips status for steer"
```

---

### Task 4: Server — `/ask` endpoint + `sent_to_agent` response + regen + e2e

**Files:** `internal/server/huma_routes_review_threads.go` (modify); `internal/server/review_threads_e2e_test.go` (append); regen artifacts.

- [ ] **Step 1: Add `sent_to_agent` to the comment response + both mapping sites.** In `reviewThreadCommentResponse` (huma_routes_review_threads.go:22) add a field:
```go
type reviewThreadCommentResponse struct {
	ID          int64  `json:"id"`
	Author      string `json:"author" doc:"user | agent"`
	Body        string `json:"body"`
	SentToAgent bool   `json:"sent_to_agent" doc:"true if this comment was sent to the agent (an Ask)"`
	CreatedAt   string `json:"created_at" doc:"UTC RFC3339 timestamp"`
}
```
In `loadReviewThreadsResponse` (the `byThread[...] = append(...)` literal, ~line 131) and `oneReviewThreadOutput` (the `comments = append(...)` literal, ~line 358), add `SentToAgent: c.SentToAgent,` to each `reviewThreadCommentResponse{...}`.

- [ ] **Step 2: Add the `/ask` input + handler + route.** Add an input type near `addReviewThreadCommentInput`:
```go
type askReviewThreadInput struct {
	Owner    string `path:"owner"`
	Name     string `path:"name"`
	Number   int    `path:"number"`
	ThreadID int64  `path:"thread_id"`
	Body     struct {
		Body string `json:"body"`
	}
}
```
Add the handler (after `addReviewThreadComment`):
```go
// askReviewThread persists the reviewer's comment, then kicks off a
// read-only steer turn so the agent continues the thread's discussion.
// On success the comment is marked sent_to_agent. If the agent is busy
// the comment persists as a plain note and the busy state is surfaced
// (the reviewer's message is never lost).
func (s *Server) askReviewThread(ctx context.Context, input *askReviewThreadInput) (*reviewThreadOutput, error) {
	if !isLocalSource(input.Owner) {
		return nil, huma.Error400BadRequest("review threads are local-worktree only")
	}
	if input.Body.Body == "" {
		return nil, huma.Error400BadRequest("message is required")
	}
	if _, err := s.resolveThreadForMR(ctx, input.Owner, input.Name, input.Number, input.ThreadID); err != nil {
		return nil, err
	}
	th, err := s.db.GetReviewThread(ctx, input.ThreadID)
	if err != nil {
		return nil, huma.Error500InternalServerError("get thread: " + err.Error())
	}
	comment, err := s.db.AddReviewThreadComment(ctx, input.ThreadID, "user", input.Body.Body, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("add comment: " + err.Error())
	}
	if err := s.kickoffReviewTurn(ctx, input.Owner, input.Name, input.Number, "steer", []db.ReviewThread{th}, input.Body.Body); err != nil {
		// Comment is persisted as a plain note; surface the error (e.g. 409 busy).
		return nil, err
	}
	if err := s.db.MarkReviewThreadCommentSentToAgent(ctx, comment.ID); err != nil {
		return nil, huma.Error500InternalServerError("mark comment: " + err.Error())
	}
	return s.oneReviewThreadOutput(ctx, input.ThreadID)
}
```
Register the route in `registerReviewThreadRoutes` (after the `/comments` line):
```go
	huma.Post(api, "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask", s.askReviewThread)
```

- [ ] **Step 3: Regenerate the client** (new route + the `sent_to_agent` field). Run with `dangerouslyDisableSandbox: true`:
```bash
GOCACHE="$HOME/.cache/go-build" make api-generate
GOCACHE="$HOME/.cache/go-build" go generate ./internal/apiclient/generated
```

- [ ] **Step 4: Write the e2e test.** Append to `internal/server/review_threads_e2e_test.go`. Use a fake claude (echoes one success line) via `aireview.SetBinaryForTest`. The generated method is likely `PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse` — confirm against `client.gen.go` after regen.
```go
func TestAPIReviewThreadAskEngagesAgentAndMarksComment(t *testing.T) {
	require := require.New(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\n"+
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}'`+"\n"), 0o755))
	aireview.SetBinaryForTest(fake)
	t.Cleanup(func() { aireview.SetBinaryForTest("claude") })

	srv, database := setupTestServer(t)
	client := setupTestClient(t, srv)
	ctx := context.Background()
	num := seedReviewWorktree(t, database)

	createResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsWithResponse(
		ctx, "local", "demo", num,
		generated.CreateReviewThreadsInputBody{
			Threads: &[]generated.ReviewThreadDraft{{Path: "a.go", Side: "RIGHT", Line: 12, CommitSha: "abc", Body: "rename this"}},
		})
	require.NoError(err)
	require.Equal(http.StatusOK, createResp.StatusCode())
	threadID := (*createResp.JSON200.Threads)[0].Id

	askResp, err := client.HTTP.PostReposByOwnerByNamePullsByNumberReviewThreadsByThreadIdAskWithResponse(
		ctx, "local", "demo", num, threadID,
		generated.AskReviewThreadInputBody{Body: "why a mutex here?"})
	require.NoError(err)
	require.Equal(http.StatusOK, askResp.StatusCode())
	require.NotNil(askResp.JSON200)
	require.NotNil(askResp.JSON200.Comments)
	// The new user comment is marked sent_to_agent.
	var asked bool
	for _, c := range *askResp.JSON200.Comments {
		if c.Author == "user" && c.Body == "why a mutex here?" && c.SentToAgent {
			asked = true
		}
	}
	require.True(asked, "ask comment should be marked sent_to_agent; comments=%+v", *askResp.JSON200.Comments)

	// A session turn was kicked off.
	sessResp, err := client.HTTP.GetReposByOwnerByNamePullsByNumberSessionWithResponse(ctx, "local", "demo", num)
	require.NoError(err)
	require.Equal(http.StatusOK, sessResp.StatusCode())
	require.NotNil(sessResp.JSON200.Turns)
	require.NotEmpty(*sessResp.JSON200.Turns)
}
```
> Reconcile generated names against `client.gen.go` after regen (e.g. `AskReviewThreadInputBody`, the `SentToAgent` field on the comment type). If the create body / session method names differ, mirror the existing tests in this file.

- [ ] **Step 5: Run → PASS** (UNSANDBOXED — tmux): `go test ./internal/server -run 'TestAPIReviewThread' -shuffle=on`; then `go test ./internal/server ./internal/db ./internal/aireview -shuffle=on`; `go build ./...` clean.

- [ ] **Step 6: Commit**
```bash
git add internal/server/huma_routes_review_threads.go internal/server/review_threads_e2e_test.go \
        frontend/openapi/openapi.json internal/apiclient/spec/openapi.json \
        internal/apiclient/generated/client.gen.go packages/ui/src/api/generated/schema.ts
git commit -m "feat(server): /ask endpoint kicks a steer turn and marks the comment"
```

---

### Task 5: Frontend — `ask` store method + "Reply & ask Claude" + busy-disable + marker

**Files:** `packages/ui/src/stores/reviewThreads.svelte.ts` (modify) + `.test.ts`; `packages/ui/src/components/diff/ReviewThreadCard.svelte` (modify) + `.test.ts`.

- [ ] **Step 1: Store test.** Append to `reviewThreads.svelte.test.ts`:
```ts
it("ask posts to the ask endpoint and upserts the returned thread", async () => {
  const post = vi.fn(async () => ({
    data: thread({ comments: [{ id: 1, author: "user", body: "why?", sent_to_agent: true, created_at: "" }] }),
    error: undefined,
  }));
  const store = createReviewThreadsStore({ client: stubClient({ POST: post }) });
  await store.load("local", "demo", 7);
  const ok = await store.ask(1, "why?");
  expect(ok).toBe(true);
  expect(post).toHaveBeenCalledWith(
    "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask",
    { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } }, body: { body: "why?" } },
  );
  expect(store.getThreads()[0]!.comments?.[0]?.sent_to_agent).toBe(true);
});
```

- [ ] **Step 2: Run → FAIL** — `cd frontend && bunx vitest run reviewThreads`.

- [ ] **Step 3: Implement `ask`** in `reviewThreads.svelte.ts` (mirror `addComment`, which upserts the single returned thread):
```ts
  async function ask(threadID: number, body: string): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask",
        { params: { path: { owner, name, number, thread_id: threadID } }, body: { body } },
      );
      if (err) throw new Error(detail(err, "failed to ask the agent"));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }
```
Add `ask` to the returned object: `..., apply, applyAll, deleteThread, refresh, ask, clear,`.

- [ ] **Step 4: Run → PASS** — `cd frontend && bunx vitest run reviewThreads`; `cd frontend && bun run check` clean.

- [ ] **Step 5: Card test.** In `ReviewThreadCard.test.ts`, extend the mocked store with `ask` + add `worktreeSession` to `getStores`, and add tests:
```ts
const ask = vi.fn(async () => true);
let running = false;
vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: { resolve, hide, unhide: vi.fn(), addComment, apply, deleteThread, ask },
    worktreeSession: { hasRunningTurn: () => running },
  }),
}));
// (keep the existing resolve/hide/addComment/apply/deleteThread mocks above)
```
```ts
it("Reply & ask Claude calls ask; plain Send calls addComment", async () => {
  const { getByText, getByPlaceholderText } = render(ReviewThreadCard, { props: { thread: thread() } });
  const box = getByPlaceholderText(/Reply/i) as HTMLTextAreaElement;
  await fireEvent.input(box, { target: { value: "why a mutex?" } });
  await fireEvent.click(getByText("Ask Claude"));
  expect(ask).toHaveBeenCalledWith(5, "why a mutex?");
});

it("Ask is disabled while a turn runs", () => {
  running = true;
  const { getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
  expect((getByText("Ask Claude") as HTMLButtonElement).disabled).toBe(true);
  running = false;
});

it("marks user comments that were sent to the agent", () => {
  const { container } = render(ReviewThreadCard, {
    props: { thread: thread({ comments: [{ id: 1, author: "user", body: "ask", sent_to_agent: true, created_at: "" }] }) },
  });
  expect(container.querySelector(".review-thread__sent-badge")).toBeTruthy();
});
```

- [ ] **Step 6: Run → FAIL** — `cd frontend && bunx vitest run ReviewThreadCard`.

- [ ] **Step 7: Implement the card.** In `ReviewThreadCard.svelte`:
  - `<script>`: `const { reviewThreads, worktreeSession } = getStores();` and `const busy = $derived(worktreeSession.hasRunningTurn());`. Add an `askClaude`:
```ts
  async function askClaude(): Promise<void> {
    const text = reply.trim();
    if (!text || sending || busy) return;
    sending = true;
    try {
      const ok = await reviewThreads.ask(thread.id, text);
      if (ok) reply = "";
    } finally {
      sending = false;
    }
  }
```
  - Apply button: add `disabled={busy}` to the `{#if canApply}` button.
  - Reply box: add the "Ask Claude" button next to "Send" (Send stays a plain note; Ask engages, disabled while busy):
```svelte
        <button
          type="button"
          class="review-thread__send review-thread__ask"
          disabled={sending || busy || !reply.trim()}
          title={busy ? "The review agent is busy" : "Reply and ask Claude to respond"}
          onclick={() => void askClaude()}
        >Ask Claude</button>
```
  - Comment marker: in the comment loop, mark user comments that were asks:
```svelte
    {#each comments as c (c.id)}
      <div class="review-thread__comment">
        <span class="review-thread__author review-thread__author--{c.author}">
          {c.author === "agent" ? "Claude" : "You"}
        </span>
        {#if c.author === "user" && c.sent_to_agent}
          <span class="review-thread__sent-badge" title="Sent to Claude">asked</span>
        {/if}
        <div class="review-thread__body markdown-body">
          {@html renderMarkdown(c.body, undefined)}
        </div>
      </div>
    {/each}
```
  - `<style>`: add the badge (orange, subtle) — reuse the amber token:
```css
  .review-thread__sent-badge {
    display: inline-block;
    margin-left: 6px;
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0 5px;
    border-radius: 999px;
    color: var(--accent-amber);
    border: 1px solid var(--accent-amber);
  }
```

- [ ] **Step 8: Run → PASS** — `cd frontend && bunx vitest run ReviewThreadCard reviewThreads`; `cd frontend && bun run check` clean; `cd frontend && bunx vitest run` (full suite stays green).

- [ ] **Step 9: Commit**
```bash
git add packages/ui/src/stores/reviewThreads.svelte.ts packages/ui/src/stores/reviewThreads.svelte.test.ts \
        packages/ui/src/components/diff/ReviewThreadCard.svelte packages/ui/src/components/diff/ReviewThreadCard.test.ts
git commit -m "feat(ui): Reply & ask Claude in a thread, with sent-to-agent marker"
```

---

## Self-review

**Spec coverage:** explicit "Reply & ask Claude" → Task 5 (card) + Task 4 (`/ask`); read-only `steer` turn → Task 2 (prompt) + gating (unchanged, confirmed) + Task 3 (kickoff verb/skip-status); Claude replies as normal comments → reuses `reply_to_thread` (no change); `sent_to_agent` marker → Task 1 (db) + Task 4 (response) + Task 5 (badge); busy-disable / surface busy → Task 5 (Ask+Apply `disabled={busy}`) + the `/ask` 409 surfaced; Apply stays the edit path with conversation context → unchanged (shared `--resume`). Phase 3 out of scope — absent.

**Placeholder scan:** `seedLocalMRForThreads` (Task 1) names the existing helper to reuse (the same pointer the Task-1 delete plan used); the generated `/ask` method + `AskReviewThreadInputBody` + the comment `SentToAgent` field are flagged to reconcile post-regen against `client.gen.go` (same convention as 2a/2b). No vague steps; all code shown.

**Type consistency:** `MarkReviewThreadCommentSentToAgent` / `SentToAgent` (Task 1) ↔ used in Task 4's handler + response mapping. `kickoffReviewTurn(..., message string)` (Task 3) ↔ called by Task 4's `/ask` with the steer message and by the existing `discuss`/`apply` callers with `""`. Store `ask(threadID, body)` (Task 5) ↔ card `askClaude` ↔ the `/ask` path/params. The comment response `sent_to_agent` ↔ the TS `c.sent_to_agent` the card reads.
