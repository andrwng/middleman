# Draft Chat Implementation Plan (PR 3 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the reviewer chat with Claude about any suggestion or draft ("Ask Claude"), and let Claude revise the comment text from that conversation under the spec's revision rules.

**Architecture:** A `DraftChatRunner` in `internal/aireview` runs one `claude -p` turn per reviewer message, FIFO per draft and at most 3 concurrently overall. A Claude-origin draft's first turn forks the review run's session (`--resume <run session> --fork-session`) in the run's fixed worktree path, recreating the worktree if it was removed. Later turns resume the draft's own session. Reviewer-authored drafts, or "Start fresh chat", start a fresh session in `<ai-review>/draft-<id>`. Claude revises only through a new MCP tool, `revise_comment` (`middleman mcp --draft <id>`). It calls a server route that applies the revision only when the draft is still a suggestion and the reviewer has not edited it since the turn started. Otherwise the revision is stored unapplied, for the reviewer to Apply. The frontend gains a chat panel under the suggestion and draft cards.

**Tech Stack:** Go, Huma v2, SQLite, testify; Svelte 5, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-10-22-claude-review-suggestions-design.md` (Delivery PR 3; "Chat on a draft"). Builds on PR 1 and PR 2 (both committed). The messages table already exists (migration 000028).

## Global Constraints

- Same as PR 2's plan (`docs/superpowers/plans/2026-10-22-claude-review-runs.md`, Global Constraints): UTC, no new deps, Bun only, testify rules, `-shuffle=on`, e2e for API behavior, `make api-generate`, conventional commits with the Co-Authored-By line, detached HEAD, no emojis.
- Chat turns: allowed tools `Read,Glob,Grep,mcp__middleman__get_draft,mcp__middleman__revise_comment,mcp__middleman__get_pull`; disallowed `Edit,Write,NotebookEdit,Bash,Agent`; 10-minute turn timeout; process group killed on timeout/cancel.
- Global cap: 3 concurrent chat `claude` processes; extra turns show `queued`.
- Revision rules (spec, binding):
  - draft is `suggestion` and the reviewer has not edited it since the turn started -> applied, Revert offered;
  - draft is `draft`, or the reviewer edited during the turn -> stored unapplied, Apply offered;
  - draft is `discarded` -> rejected.
- Prompt rule: answer questions without revising; call `revise_comment` only when the reviewer asks for a change.

## Spec deviations (decided while planning)

- Local worktree drafts get chat in PR 4, with the rest of the local target. The routes return 400 for `owner=local` until then.
- Claude's chat replies render as Markdown via the existing `renderMarkdown` without repo link resolution, so bare file names are not linked.
- The chat worktree is removed after each turn and recreated at the same path on the next one. The spec allows removal when idle; per turn is the simplest idle rule.
- "Start fresh chat" is offered on any failed turn, not only on detected session loss. The CLI's error text for a missing session is not a stable contract.

## Review Focus

1. The reviewer edits the comment while Claude is revising it -> Claude's text is not applied; it shows as a suggestion with Apply. (Task 1 `TestApplyClaudeRevisionRules`, Task 4 `TestAPIDraftChatRevisionRules`)
2. Two messages sent quickly on one draft -> they run in order, and the second resumes the first's session. (Task 2 `TestDraftChatFIFOAndResume`)
3. The run's worktree was removed when the run ended -> the first chat turn recreates it at the same path and forks the run session. (Task 2 `TestDraftChatForksRunSessionInRunPath`)
4. `revise_comment` on a discarded draft, or with no turn in progress -> rejected, nothing changes. (Task 4)
5. A resume or fork fails -> the turn fails with the CLI's error and "Start fresh chat" works. (Task 4 `TestAPIDraftChatFreshAfterFailure`)

---

### Task 1: DB layer for chat turns and revisions

**Files:**
- Modify: `internal/db/queries_review_draft_messages.go`, `internal/db/queries_review_drafts.go`
- Test: `internal/db/queries_review_draft_messages_test.go`

**Interfaces (produces):**

```go
func (d *DB) ListReviewDraftMessages(ctx context.Context, draftID string) ([]ReviewDraftMessage, error) // oldest first
func (d *DB) GetReviewDraftMessage(ctx context.Context, id int64) (*ReviewDraftMessage, error)
func (d *DB) MarkDraftMessageRunning(ctx context.Context, id int64, pid int) error          // queued -> running, sets started_at
func (d *DB) UpdateDraftMessageStream(ctx context.Context, id int64, rawJSON string) error
func (d *DB) MarkDraftMessageDone(ctx context.Context, id int64, body, rawJSON string) error  // from queued|running
func (d *DB) MarkDraftMessageFailed(ctx context.Context, id int64, msg, rawJSON string) error // from queued|running; empty rawJSON keeps stored
func (d *DB) MarkDraftMessageCancelled(ctx context.Context, id int64) error                   // from queued|running
func (d *DB) RunningDraftMessage(ctx context.Context, draftID string) (*ReviewDraftMessage, error) // claude message with status running, else nil
func (d *DB) ListActiveDraftMessages(ctx context.Context) ([]ReviewDraftMessage, error)      // queued|running
func (d *DB) LastCompletedClaudeMessageAt(ctx context.Context, draftID string) (*time.Time, error)

// ReviewDraft gains ClaudeSessionID, ChatWorktreePath (scanned from existing columns)
func (d *DB) SetReviewDraftChatSession(ctx context.Context, draftID, sessionID, worktreePath string) error

// Revisions
func (d *DB) ApplyClaudeRevision(ctx context.Context, mrID int64, branch, draftID, newBody string, turnStartedAt time.Time) (ReviewDraftMessage, error)
	// ErrInvalidDraftTransition when discarded; nil draft -> sql.ErrNoRows wrapped as ErrDraftNotFound
func (d *DB) ApplyRevisionMessage(ctx context.Context, mrID int64, branch string, msgID int64) (*ReviewDraft, error)  // unapplied revision -> applied, body = msg.body, body_edited_at = now
func (d *DB) RevertRevisionMessage(ctx context.Context, mrID int64, branch string, msgID int64) (*ReviewDraft, error) // applied revision -> unapplied, body = prev_body, body_edited_at = now
var ErrDraftNotFound = errors.New("draft not found")
```

`ApplyClaudeRevision` runs in one transaction. It loads the draft's state, body, and `body_edited_at` scoped to (mr, branch). It decides `applied := state == "suggestion" && (body_edited_at IS NULL || body_edited_at < turnStartedAt)`. When applied, it updates `body` and `updated_at` but not `body_edited_at`, because Claude's edit is not a reviewer edit. It always inserts a `kind='revision', author='claude', status='done'` message with `body=newBody, prev_body=<old body>, applied`. `ApplyRevisionMessage` and `RevertRevisionMessage` return `ErrInvalidDraftTransition` when the message is not a revision of that draft, is in the wrong applied state, or the draft is discarded.

- [ ] **Step 1: Failing tests** (`queries_review_draft_messages_test.go`):

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedSuggestion(t *testing.T, d *DB, id string) int64 {
	t.Helper()
	ctx := context.Background()
	mrID := seedAIQuestionTestMR(t, d)
	run, err := d.CreateAIReviewRun(ctx, NewAIReviewRun{MergeRequestID: mrID, HeadSHA: "h", ReadSHA: "h"})
	require.NoError(t, err)
	_, err = d.InsertClaudeSuggestion(ctx, mrID, "", run.ID, NewReviewDraft{ID: id, Path: "a.go", Side: "RIGHT", Line: 1, CommitSHA: "h", Body: "original"})
	require.NoError(t, err)
	return mrID
}

func TestDraftMessageLifecycle(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedSuggestion(t, d, "s1")

	user, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "user", Kind: "message", Body: "why?", Status: "done"})
	require.NoError(err)
	reply, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "claude", Kind: "message", Status: "queued"})
	require.NoError(err)

	active, err := d.ListActiveDraftMessages(ctx)
	require.NoError(err)
	require.Len(active, 1)

	require.NoError(d.MarkDraftMessageRunning(ctx, reply.ID, 99))
	running, err := d.RunningDraftMessage(ctx, "s1")
	require.NoError(err)
	require.NotNil(running)
	require.NotNil(running.StartedAt)
	require.NoError(d.UpdateDraftMessageStream(ctx, reply.ID, `{"events":[]}`))
	require.NoError(d.MarkDraftMessageDone(ctx, reply.ID, "because", `{"events":[1]}`))
	require.NoError(d.MarkDraftMessageFailed(ctx, reply.ID, "late", "")) // terminal: ignored

	msgs, err := d.ListReviewDraftMessages(ctx, "s1")
	require.NoError(err)
	require.Len(msgs, 2)
	assert.Equal(user.ID, msgs[0].ID)
	assert.Equal("done", msgs[1].Status)
	assert.Equal("because", msgs[1].Body)
	at, err := d.LastCompletedClaudeMessageAt(ctx, "s1")
	require.NoError(err)
	assert.NotNil(at)

	require.NoError(d.SetReviewDraftChatSession(ctx, "s1", "sess-fork", "/tmp/run-1"))
	got, err := d.GetReviewDraft(ctx, mrID, "", "s1")
	require.NoError(err)
	assert.Equal("sess-fork", got.ClaudeSessionID)
	assert.Equal("/tmp/run-1", got.ChatWorktreePath)
}
```

```go
func TestApplyClaudeRevisionRules(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		accept      bool // move to draft first
		editAfter   bool // reviewer edits after the turn started
		discard     bool
		wantApplied bool
		wantErr     error
		wantBody    string
	}{
		{name: "suggestion, no edit -> applied", wantApplied: true, wantBody: "revised"},
		{name: "suggestion edited during turn -> pending", editAfter: true, wantApplied: false, wantBody: "reviewer text"},
		{name: "draft -> pending", accept: true, wantApplied: false, wantBody: "original"},
		{name: "discarded -> rejected", discard: true, wantErr: ErrInvalidDraftTransition, wantBody: "original"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := require.New(t)
			d := openTestDB(t)
			mrID := seedSuggestion(t, d, "s1")
			turnStarted := time.Now().UTC().Add(-time.Second)
			if tc.accept {
				_, err := d.AcceptReviewDraft(ctx, mrID, "", "s1")
				must.NoError(err)
			}
			if tc.editAfter {
				_, err := d.UpdateReviewDraftBody(ctx, mrID, "", "s1", "reviewer text")
				must.NoError(err)
			}
			if tc.discard {
				_, err := d.DiscardReviewDraft(ctx, mrID, "", "s1")
				must.NoError(err)
			}
			msg, err := d.ApplyClaudeRevision(ctx, mrID, "", "s1", "revised", turnStarted)
			if tc.wantErr != nil {
				must.ErrorIs(err, tc.wantErr)
			} else {
				must.NoError(err)
				must.Equal("revision", msg.Kind)
				must.Equal(tc.wantApplied, msg.Applied)
				must.Equal("revised", msg.Body)
			}
			got, err := d.GetReviewDraft(ctx, mrID, "", "s1")
			must.NoError(err)
			must.Equal(tc.wantBody, got.Body)
		})
	}
}

func TestApplyAndRevertRevisionMessage(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	d := openTestDB(t)
	ctx := context.Background()
	mrID := seedSuggestion(t, d, "s1")
	_, err := d.AcceptReviewDraft(ctx, mrID, "", "s1")
	require.NoError(err)

	pending, err := d.ApplyClaudeRevision(ctx, mrID, "", "s1", "revised", time.Now().UTC())
	require.NoError(err)
	require.False(pending.Applied)

	got, err := d.ApplyRevisionMessage(ctx, mrID, "", pending.ID)
	require.NoError(err)
	assert.Equal("revised", got.Body)
	assert.Equal("draft", got.State) // applying never changes state
	_, err = d.ApplyRevisionMessage(ctx, mrID, "", pending.ID)
	require.ErrorIs(err, ErrInvalidDraftTransition)

	got, err = d.RevertRevisionMessage(ctx, mrID, "", pending.ID)
	require.NoError(err)
	assert.Equal("original", got.Body)

	user, err := d.InsertReviewDraftMessage(ctx, NewReviewDraftMessage{DraftID: "s1", Author: "user", Kind: "message", Body: "x", Status: "done"})
	require.NoError(err)
	_, err = d.ApplyRevisionMessage(ctx, mrID, "", user.ID)
	require.ErrorIs(err, ErrInvalidDraftTransition)
}
```

Run: `go test ./internal/db -run 'TestDraftMessage|TestApplyClaudeRevision|TestApplyAndRevert' -shuffle=on` -> build failure.

- [ ] **Step 2: Implement.** Scan columns for messages: `id, draft_id, author, kind, body, prev_body, applied, status, raw_json, error, created_at, started_at, completed_at`, with a `scanReviewDraftMessage` helper modeled on `scanAIReviewRun`. Status transitions use `WHERE id = ? AND status IN ('queued','running')`, the same as the run functions. Add `ClaudeSessionID`, `ChatWorktreePath` to `ReviewDraft` and to `reviewDraftColumns`/`scanReviewDraft` (append after `run_id`). `SetReviewDraftChatSession` updates those two columns by id. Revisions:

```go
var ErrDraftNotFound = errors.New("draft not found")

func (d *DB) ApplyClaudeRevision(
	ctx context.Context, mrID int64, branch, draftID, newBody string, turnStartedAt time.Time,
) (ReviewDraftMessage, error) {
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		state, body string
		editedAt    sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT state, body, body_edited_at FROM middleman_review_drafts
		  WHERE merge_request_id = ? AND branch = ? AND id = ?`, mrID, branch, draftID,
	).Scan(&state, &body, &editedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDraftMessage{}, ErrDraftNotFound
	}
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	if state == "discarded" {
		return ReviewDraftMessage{}, ErrInvalidDraftTransition
	}
	applied := state == "suggestion" && (!editedAt.Valid || editedAt.Time.Before(turnStartedAt))
	now := time.Now().UTC()
	if applied {
		if _, err := tx.ExecContext(ctx,
			`UPDATE middleman_review_drafts SET body = ?, updated_at = ? WHERE id = ?`,
			newBody, now, draftID); err != nil {
			return ReviewDraftMessage{}, err
		}
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO middleman_review_draft_messages
		     (draft_id, author, kind, body, prev_body, applied, status, created_at, completed_at)
		 VALUES (?, 'claude', 'revision', ?, ?, ?, 'done', ?, ?)`,
		draftID, newBody, body, applied, now, now)
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ReviewDraftMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReviewDraftMessage{}, err
	}
	return ReviewDraftMessage{ID: id, DraftID: draftID, Author: "claude", Kind: "revision",
		Body: newBody, PrevBody: body, Applied: applied, Status: "done", CreatedAt: now}, nil
}
```

`ApplyRevisionMessage` / `RevertRevisionMessage` share a helper: in a transaction, load the message (must be `kind='revision'` with the expected `applied` value, else `ErrInvalidDraftTransition`), load the draft scoped to (mr, branch, msg.draft_id) (missing -> `nil, nil`; discarded -> `ErrInvalidDraftTransition`), set the draft body to `msg.body` (apply) or `msg.prev_body` (revert) with `body_edited_at = now, updated_at = now, submit_error = ''`, flip `applied`, commit, and return `GetReviewDraft`.

Run: `go test ./internal/db -shuffle=on` -> PASS. Commit `feat(db): add draft chat turns and revision rules`.

---

### Task 2: Draft chat runner

**Files:**
- Create: `internal/aireview/draftchat.go`, `internal/aireview/draftchat_test.go`
- Modify: `internal/aireview/runner.go` (accessors; reconcile)
- Modify: `internal/aireview/sessions.go` (`MCPConfig.DraftID`; `writeMCPConfig` adds `--draft`)

**Interfaces (produces):**

```go
const draftChatToolNames = "mcp__middleman__get_draft,mcp__middleman__revise_comment,mcp__middleman__get_pull"

type DraftChatMode string
const (
	DraftChatFork   DraftChatMode = "fork"   // --resume <ParentSessionID> --fork-session
	DraftChatResume DraftChatMode = "resume" // --resume <ParentSessionID>
	DraftChatFresh  DraftChatMode = "fresh"  // no --resume
)

type DraftTurnInput struct {
	DraftID         string
	MessageID       int64  // the queued claude message to fill
	Owner, Name     string
	Number          int
	Mode            DraftChatMode
	ParentSessionID string // run session (fork) or draft session (resume)
	WorktreePath    string // fixed cwd for this conversation
	CheckoutSHA     string // what to check out at WorktreePath if missing
	Prompt          string
	MCP             MCPConfig // DraftID set
}

func (r *Runner) DraftChatWorktreePath(draftID string) string // <root>/draft-<id>
func (r *Runner) ReviewRunPath(runID int64) string             // ReviewRunWorktreePath(r.rootDir, runID)
func (r *Runner) SubmitDraftTurn(in DraftTurnInput)            // enqueue; never blocks
func (r *Runner) CancelDraftTurn(ctx context.Context, messageID int64) error
```

Runner fields: `draftQueues map[string][]DraftTurnInput`, `draftBusy map[string]bool`, `draftSem chan struct{}` (cap 3, `RunnerConfig.DraftChatConcurrency` default 3), `draftTurnsRunning map[int64]context.CancelFunc`, `draftTurnTimeout` (`RunnerConfig.DraftTurnTimeout` default 10m). `ReconcileOnStartup` also fails every `ListActiveDraftMessages` row ("interrupted by middleman restart").

Turn execution (`runDraftTurn`):
1. Acquire the semaphore (the message stays `queued` until then).
2. If `WorktreePath` does not exist, `provisionWorktreeAt(owner, name, CheckoutSHA, WorktreePath)`; on failure, mark the message failed `"cannot check out <sha7> for this conversation: <err>"`.
3. Build args: `-p <prompt> --output-format stream-json --verbose --permission-mode bypassPermissions --allowedTools Read,Glob,Grep,<draftChatToolNames> --disallowedTools Edit,Write,NotebookEdit,Bash,Agent`, MCP config with `--strict-mcp-config`, plus `--resume <parent>` for fork/resume and `--fork-session` for fork.
4. Stream with `applyStreamMessage`, flushing to `UpdateDraftMessageStream`.
5. On success `MarkDraftMessageDone(id, resultText, raw)`; for fork/fresh also `SetReviewDraftChatSession(draftID, state.SessionID, WorktreePath)`. Timeout -> failed "turn timed out after 10m0s". Cancel -> leave cancelled. Exit error -> failed with stderr.
6. Always remove the worktree afterwards (`cleanWorktreePath`), then release the semaphore and start the draft's next queued turn. A resume needs only the same path, not the old checkout.

Per-draft FIFO: `SubmitDraftTurn` appends to `draftQueues[id]`. When `!draftBusy[id]` it sets busy and starts a goroutine that drains the queue in order. A queued resume turn submitted while the first turn (fork) is still running must use the session the first turn produces. So when a turn's `Mode` is fork/fresh and an earlier turn for the same draft is still queued or running, the dispatcher re-reads the draft's `ClaudeSessionID` from the DB just before running. If it is now set, the turn switches to `DraftChatResume` with that session and the draft's `ChatWorktreePath`. Add `DraftChatDB` methods as needed (`GetReviewDraftByID(ctx, id)` — an unscoped lookup by id for the runner only).

- [ ] **Step 1: Failing tests** (`draftchat_test.go`, reusing `newReviewRunFixture`, `writeFakeReviewClaude`):

```go
func seedClaudeDraft(t *testing.T, database *db.DB, mrID int64, head string) (db.AIReviewRun, string) {
	t.Helper()
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(t, err)
	require.NoError(t, database.MarkAIReviewRunDone(ctx, run.ID, "sess-root", "", ""))
	_, err = database.InsertClaudeSuggestion(ctx, mrID, "", run.ID, db.NewReviewDraft{ID: "s1", Path: "a.txt", Side: "RIGHT", Line: 1, CommitSHA: head, Body: "b"})
	require.NoError(t, err)
	return run, "s1"
}

func queueReply(t *testing.T, database *db.DB, draftID string) db.ReviewDraftMessage {
	t.Helper()
	m, err := database.InsertReviewDraftMessage(context.Background(), db.NewReviewDraftMessage{DraftID: draftID, Author: "claude", Kind: "message", Status: "queued"})
	require.NoError(t, err)
	return m
}

func waitMessage(t *testing.T, database *db.DB, id int64, status string) *db.ReviewDraftMessage {
	t.Helper()
	var got *db.ReviewDraftMessage
	require.Eventually(t, func() bool {
		got, _ = database.GetReviewDraftMessage(context.Background(), id)
		return got != nil && got.Status == status
	}, 10*time.Second, 20*time.Millisecond)
	return got
}

const forkStream = `echo '{"type":"system","subtype":"init","session_id":"sess-fork"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"It drops the error.","session_id":"sess-fork"}'
`

func TestDraftChatForksRunSessionInRunPath(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	run, draftID := seedClaudeDraft(t, database, mrID, head)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, argsFile, "pwd > "+argsFile+".cwd\n"+forkStream)
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	msg := queueReply(t, database, draftID)
	runPath := r.ReviewRunPath(run.ID)
	r.SubmitDraftTurn(DraftTurnInput{
		DraftID: draftID, MessageID: msg.ID, Owner: "acme", Name: "widget", Number: 1,
		Mode: DraftChatFork, ParentSessionID: "sess-root", WorktreePath: runPath, CheckoutSHA: head,
		Prompt: "why?", MCP: MCPConfig{Binary: "/bin/middleman", BaseURL: "http://x", Owner: "acme", Name: "widget", Number: 1, DraftID: draftID},
	})
	got := waitMessage(t, database, msg.ID, "done")
	assert.Equal("It drops the error.", got.Body)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	argv := string(args)
	assert.Contains(argv, "--resume\nsess-root")
	assert.Contains(argv, "--fork-session")
	assert.Contains(argv, draftChatToolNames)
	cwd, err := os.ReadFile(argsFile + ".cwd")
	require.NoError(err)
	assert.Equal(runPath, strings.TrimSpace(string(cwd)))
	mcpCfg, err := os.ReadFile(argsFile + ".mcp.json")
	require.NoError(err)
	assert.Contains(string(mcpCfg), "--draft")

	d, err := database.GetReviewDraft(context.Background(), mrID, "", draftID)
	require.NoError(err)
	assert.Equal("sess-fork", d.ClaudeSessionID)
	assert.Equal(runPath, d.ChatWorktreePath)
	require.Eventually(func() bool {
		_, statErr := os.Stat(runPath)
		return os.IsNotExist(statErr)
	}, 5*time.Second, 20*time.Millisecond)
}

func TestDraftChatFIFOAndResume(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	run, draftID := seedClaudeDraft(t, database, mrID, head)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	fake := filepath.Join(dir, "claude.sh")
	// Each call appends its argv on one line, sleeps briefly, and reports
	// a session id. The first call (fork) yields sess-fork.
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\nsleep 0.3\n" + forkStream
	require.NoError(os.WriteFile(fake, []byte(script), 0o755))
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	first := queueReply(t, database, draftID)
	second := queueReply(t, database, draftID)
	base := DraftTurnInput{DraftID: draftID, Owner: "acme", Name: "widget", Number: 1,
		Mode: DraftChatFork, ParentSessionID: "sess-root", WorktreePath: r.ReviewRunPath(run.ID), CheckoutSHA: head, Prompt: "q"}
	a, b := base, base
	a.MessageID, b.MessageID = first.ID, second.ID
	r.SubmitDraftTurn(a)
	r.SubmitDraftTurn(b) // submitted before the fork produced a session

	waitMessage(t, database, second.ID, "done")
	calls, err := os.ReadFile(log)
	require.NoError(err)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	require.Len(lines, 2)
	require.Contains(lines[0], "--fork-session")
	require.Contains(lines[1], "--resume sess-fork")
	require.NotContains(lines[1], "--fork-session")
}

func TestDraftChatConcurrencyCap(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	r.draftSem = make(chan struct{}, 1)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\nsleep 0.5\n"+forkStream), 0o755))
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(err)
	var msgs []db.ReviewDraftMessage
	for _, id := range []string{"c1", "c2"} {
		_, err := database.InsertClaudeSuggestion(ctx, mrID, "", run.ID, db.NewReviewDraft{ID: id, Path: "a.txt", Side: "RIGHT", Line: 1, CommitSHA: head, Body: "b"})
		require.NoError(err)
		m := queueReply(t, database, id)
		msgs = append(msgs, m)
		r.SubmitDraftTurn(DraftTurnInput{DraftID: id, MessageID: m.ID, Owner: "acme", Name: "widget", Number: 1,
			Mode: DraftChatFresh, WorktreePath: r.DraftChatWorktreePath(id), CheckoutSHA: head, Prompt: "q"})
	}
	// With a cap of 1, one turn waits queued while the other runs.
	require.Eventually(func() bool {
		a, _ := database.GetReviewDraftMessage(ctx, msgs[0].ID)
		b, _ := database.GetReviewDraftMessage(ctx, msgs[1].ID)
		return (a.Status == "running" && b.Status == "queued") || (a.Status == "queued" && b.Status == "running")
	}, 5*time.Second, 10*time.Millisecond)
	waitMessage(t, database, msgs[0].ID, "done")
	waitMessage(t, database, msgs[1].ID, "done")
}

func TestDraftChatCheckoutFailure(t *testing.T) {
	r, database, mrID, head, _ := newReviewRunFixture(t)
	_, draftID := seedClaudeDraft(t, database, mrID, head)
	msg := queueReply(t, database, draftID)
	r.SubmitDraftTurn(DraftTurnInput{DraftID: draftID, MessageID: msg.ID, Owner: "acme", Name: "widget", Number: 1,
		Mode: DraftChatFresh, WorktreePath: r.DraftChatWorktreePath(draftID), CheckoutSHA: "0000000000000000000000000000000000000000", Prompt: "q"})
	got := waitMessage(t, database, msg.ID, "failed")
	assert.Contains(t, got.Error, "cannot check out")
}

func TestDraftChatCancel(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	_, draftID := seedClaudeDraft(t, database, mrID, head)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	require.NoError(os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	msg := queueReply(t, database, draftID)
	r.SubmitDraftTurn(DraftTurnInput{DraftID: draftID, MessageID: msg.ID, Owner: "acme", Name: "widget", Number: 1,
		Mode: DraftChatFresh, WorktreePath: r.DraftChatWorktreePath(draftID), CheckoutSHA: head, Prompt: "q"})
	waitMessage(t, database, msg.ID, "running")
	require.NoError(r.CancelDraftTurn(context.Background(), msg.ID))
	time.Sleep(200 * time.Millisecond)
	got, err := database.GetReviewDraftMessage(context.Background(), msg.ID)
	require.NoError(err)
	require.Equal("cancelled", got.Status)
}
```

Also extend `TestReconcileMarksActiveReviewRunsFailed` (or add a sibling) to cover a queued claude draft message.

Run -> build failure.

- [ ] **Step 2: Implement** `draftchat.go` per the steps above (mirror `runReviewRun` for exec/stream/timeout handling; DB writes on `context.Background()` after the turn context ends). `CancelDraftTurn` marks the message cancelled. It cancels the running context if the turn is running, and removes the turn from its queue if it is still queued. Add `DraftID string` to `MCPConfig`, with `writeMCPConfig` appending `--draft <id>` when set. Run `go test ./internal/aireview -shuffle=on`, then `-count=5 -race` on the new tests. Commit `feat(aireview): run per-draft chat turns with forked review sessions`.

---

### Task 3: MCP draft-chat tool set

**Files:** `internal/mcp/server.go` (`Config.DraftID`), `internal/mcp/review_tools.go` (`draftChatTools()`), `internal/mcp/review_tools_test.go`, `cmd/middleman/main.go` (`--draft` flag).

Tools when `DraftID != ""`:
- `get_draft` -> `GET /api/v1/repos/{o}/{n}/pulls/{num}/review-drafts/{draft_id}`
- `revise_comment(body)` -> `POST .../review-drafts/{draft_id}/revisions {body}`. The description says to use it only when the reviewer asks for a change, and that `body` is the complete new comment text.
- `get_pull` (shared def)

Tests mirror Task 3 of PR 2: draft mode lists exactly these three tools; `revise_comment` posts `{body}` to the right path with `url.PathEscape` on the id; an empty body is rejected without a request; a 409 from the server surfaces as an error. `main.go`: `draft := fs.String("draft", "", ...)`, `cfg.DraftID = *draft`, and skip cwd resolution when set. Commit `feat(mcp): add the draft-chat tool set behind --draft`.

---

### Task 4: Server routes

**Files:**
- Create: `internal/server/huma_routes_draft_chat.go`, `internal/server/draft_chat_e2e_test.go`
- Modify: `internal/server/huma_routes_review_drafts.go` (register; single-draft GET)
- Regenerate API artifacts

Routes (`/api/v1`):

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/repos/{o}/{n}/pulls/{num}/review-drafts/{draft_id}` | - | `reviewDraftResponse` (any state; used by MCP `get_draft`) |
| GET | `.../review-drafts/{draft_id}/messages` | - | `{messages: [draftMessageResponse]}` |
| POST | `.../review-drafts/{draft_id}/messages` | `{body, fresh?}` | `{messages: [...]}` (the full list after insert) |
| POST | `.../review-drafts/{draft_id}/messages/{message_id}/cancel` | - | `draftMessageResponse` |
| POST | `.../review-drafts/{draft_id}/messages/{message_id}/apply` | - | `reviewDraftResponse` |
| POST | `.../review-drafts/{draft_id}/messages/{message_id}/revert` | - | `reviewDraftResponse` |
| POST | `.../review-drafts/{draft_id}/revisions` | `{body}` | `draftMessageResponse`; 409 discarded or no running turn |

```go
type draftMessageResponse struct {
	ID          int64   `json:"id"`
	Author      string  `json:"author"`
	Kind        string  `json:"kind"`
	Body        string  `json:"body"`
	PrevBody    string  `json:"prev_body,omitempty"`
	Applied     bool    `json:"applied"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	RawJSON     string  `json:"raw_json,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
}
```

Send-message handler:
- 503 without AI review; 400 for `owner=local` ("chat on local drafts arrives with local Review with Claude"); 400 for an empty body (limit 8192 bytes); 404 for a missing draft; 409 for a discarded one.
- Insert the user message (`done`) and the queued Claude message.
- Choose the mode:
  - `fresh=true`: clear the draft's chat session, then fresh.
  - draft has `ClaudeSessionID`: resume in `ChatWorktreePath`. Checkout SHA is the run's `HeadSHA` when `ChatWorktreePath == ReviewRunPath(run)`, else the draft's `CommitSHA`.
  - draft has a run with a non-empty `ClaudeSessionID`: fork in `ReviewRunPath(run.ID)` at `run.HeadSHA`.
  - otherwise: fresh in `DraftChatWorktreePath(id)` at `CommitSHA` (falls back to the PR head SHA when empty).
- Build the prompt (`buildDraftChatPrompt`, below).
- `SubmitDraftTurn` with `MCPConfig{..., DraftID: id}`.
- Return the list.

Prompt (fresh sessions also get the PR context block built like `startAIReviewRun`'s, factored into `s.prContextBlock(ctx, owner, name, number, mrID)`):

```
We are discussing one review comment on <path>:<line> (<side: new|old> side).
Current comment text:
<body>
[The reviewer edited the comment since your last reply; the text above is current.]   (when body_edited_at > LastCompletedClaudeMessageAt)
The reviewer says:
<message>

Answer the reviewer. Do not change the comment unless the reviewer asks for a change; when they do, call mcp__middleman__revise_comment with the complete new comment text, then say briefly what you changed.
```

Revisions handler (called by MCP):
- Find the running Claude message with `RunningDraftMessage`. If none -> 409 "no conversation turn is in progress for this draft".
- Call `ApplyClaudeRevision(..., running.StartedAt)`. Map `ErrInvalidDraftTransition` -> 409 and `ErrDraftNotFound` -> 404.
- `validateDraftBody` applies to the body.

Apply/revert map `ErrInvalidDraftTransition` -> 409.

- [ ] **Step 1: Failing e2e tests** (`draft_chat_e2e_test.go`, reusing `setupReviewRunServer` from PR 2's e2e file; the fake claude script receives the prompt as an argument):

1. `TestAPIDraftChatForkThenResume`:
   - Start a run with a fake that prints `{"type":"system","session_id":"sess-root"}` and a result.
   - Wait for done.
   - Post a proposal while a second "sleep" run is active? Not needed: insert the suggestion by starting a run whose fake sleeps briefly, posting a proposal during the sleep, then letting it finish with session `sess-root`. Script: `sleep 1` then emit init + result.
   - Send "why?" -> `messages` has user + queued claude.
   - Wait until the Claude message is done.
   - The recorded argv contains `--resume`, `sess-root` and `--fork-session`.
   - Send "and?" -> the second argv contains `--resume` with the fork's session id and no `--fork-session`.
   - The fake prints a distinct session id per call; use `$$` in the script.
2. `TestAPIDraftChatRevisionRules`. Table over:
   - (suggestion, no edit) -> applied, the draft body changes.
   - (suggestion, reviewer PATCHes the body while the turn runs) -> unapplied, body stays the reviewer's.
   - (accepted draft) -> unapplied.

   For each case, start a turn whose fake sleeps 2s and post to `/revisions` while it runs (the test plays Claude's MCP call). Then assert the revision message's `applied` flag and the draft body. Apply the unapplied one -> body changes and state is unchanged. Revert -> body back.
3. `TestAPIDraftChatRevisionRejected`:
   - `/revisions` with no running turn -> 409.
   - Discard the draft, start nothing, `/revisions` -> 409.
   - Body unchanged.
4. `TestAPIDraftChatFreshAfterFailure`:
   - The fake exits 1 when `--resume` is present and succeeds otherwise.
   - Send -> the Claude message fails with the stderr text.
   - Send with `fresh: true` -> argv has no `--resume`, the turn succeeds, and the draft's chat session is set.
5. `TestAPIDraftChatUserDraftStartsFresh`: a reviewer-authored draft (POST review-drafts) -> the first turn has no `--resume`, and cwd is the draft chat path (record `pwd`).
6. `TestAPIDraftChatRejects`: local owner 400; empty body 400; unknown draft 404.

Run -> build failure.

- [ ] **Step 2: Implement** the handlers, register them in `registerReviewDraftRoutes`, `make api-generate`, and run the tests until they pass, then `go test ./internal/server -shuffle=on`. Commit `feat(server): add per-draft Claude chat and revision routes`.

---

### Task 5: Frontend chat store

**Files:**
- Modify: `packages/ui/src/stores/reviewDraftsApi.ts` (+ test): `listMessages`, `sendMessage(pr, id, body, fresh?)`, `cancelMessage`, `applyRevision`, `revertRevision`; type `ServerDraftMessage` (fields of `draftMessageResponse`).
- Create: `packages/ui/src/stores/draftChat.svelte.ts` (+ `draftChat.svelte.test.ts`)
- Modify: `Provider.svelte`, `types.ts` (register `draftChat`, with `onRevision: () => void diffStore.reloadDrafts()`)

```ts
export function createDraftChatStore(opts?: { getBasePath?: () => string; onRevision?: () => void }): {
  open(pr: PRRef, draftId: string): void;   // loads messages, starts polling while a claude message is queued/running
  close(draftId: string): void;
  isOpen(draftId: string): boolean;
  getMessages(draftId: string): ServerDraftMessage[];
  isBusy(draftId: string): boolean;          // a claude message queued/running
  getError(draftId: string): string | null;
  send(draftId: string, body: string, fresh?: boolean): Promise<boolean>;
  cancel(draftId: string, messageId: number): Promise<void>;
  apply(draftId: string, messageId: number): Promise<void>;
  revert(draftId: string, messageId: number): Promise<void>;
};
```

Polls each open, busy chat every 1500 ms. Calls `onRevision` whenever a poll brings a new `kind === "revision"` message, and after apply/revert, so the card body refreshes. Tests (fake timers + stubbed fetch):
- send posts the body and starts polling;
- polling stops when no Claude message is active;
- a new revision triggers `onRevision`;
- apply/revert call their routes and `onRevision`;
- errors from send surface through `getError`.

Commit `feat(ui): add the draft chat store`.

---

### Task 6: Chat UI

**Files:**
- Create: `packages/ui/src/components/diff/DraftChat.svelte` (+ `DraftChat.test.ts`)
- Modify: `SuggestionCard.svelte` (+ test), `PendingCommentCard.svelte` (+ test): "Ask Claude" toggles `<DraftChat draftId=... />` under the card. The PendingCommentCard button appears only when `diff.getCurrentPR()?.owner !== "local"` and the draft is not a reply (`inReplyTo` unset).

DraftChat (layout and keyboard follow `AIThreadCard`):
- A message list.
- User messages are right-labelled "You".
- Claude messages carry the "Claude" badge, with the body rendered via `renderMarkdown(body)` as `{@html}`. They show a status line while queued ("Waiting for a free slot…") or running ("Thinking…" + Cancel). On failure they show the error and a "Start fresh chat" button, which calls `send(lastUserBody, true)`.
- Revision messages show "Claude revised the comment" with old/new text in two muted blocks. An applied revision shows "Revert"; an unapplied one shows "Claude suggests this wording" with "Apply".
- A collapsible "Activity" toggle per Claude message renders `SessionToolTimeline rawJSON={m.raw_json}`.
- A composer textarea (placeholder "Ask about this comment, or ask Claude to change it"). Ctrl/Cmd-Enter sends, Esc closes. It is disabled while a send is in flight.

On mount: `draftChat.open(pr, draftId)`; on destroy: `close`.

Tests:
- renders user/Claude messages;
- Ctrl-Enter sends;
- Apply/Revert call the store;
- "Start fresh chat" after a failed message calls `send(..., true)`;
- SuggestionCard's "Ask Claude" shows the chat;
- PendingCommentCard shows "Ask Claude" for GitHub drafts but not for replies or local.

Commit `feat(ui): chat with Claude about a suggestion or draft`.

---

### Task 7: Full verification

Same as PR 2 Task 9: `make test-short`, `make vet`, `make testify-helper-check` (only pre-existing failures), `gofmt -l` on changed Go files, `cd frontend && bun run test`, `make frontend-check`, `make build`. Record results in the ledger.
