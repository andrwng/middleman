package aireview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/db"
)

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

func TestReconcileFailsActiveDraftMessages(t *testing.T) {
	r, database, mrID, head, _ := newReviewRunFixture(t)
	_, draftID := seedClaudeDraft(t, database, mrID, head)
	msg := queueReply(t, database, draftID)
	require.NoError(t, r.ReconcileOnStartup(context.Background()))
	got, err := database.GetReviewDraftMessage(context.Background(), msg.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got.Status)
}
