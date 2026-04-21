package aireview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
)

func TestParseClaudeResult(t *testing.T) {
	r, err := parseClaudeResult([]byte(`{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"result": "Here's the answer.",
		"session_id": "sess-123"
	}`))
	require.NoError(t, err)
	assert.Equal(t, "sess-123", r.SessionID)
	assert.Equal(t, "Here's the answer.", r.Text)
}

func TestParseClaudeResult_Error(t *testing.T) {
	_, err := parseClaudeResult([]byte(`{
		"type": "result",
		"subtype": "error",
		"is_error": true,
		"result": "boom"
	}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestBuildPrompt(t *testing.T) {
	sel := "x := 1"
	prompt := buildPrompt(CreateThreadInput{
		Path:          "foo.go",
		AnchorSide:    "RIGHT",
		AnchorLine:    42,
		CommitSHA:     "abc1234",
		HunkText:      "@@ -40,3 +40,3 @@\n-old\n+new",
		SelectionText: &sel,
		PromptContext: "PR #1: fix things",
	}, "what does this do?")

	assert.Contains(t, prompt, "PR #1: fix things")
	assert.Contains(t, prompt, "File: foo.go")
	assert.Contains(t, prompt, "RIGHT side")
	assert.Contains(t, prompt, "abc1234")
	assert.Contains(t, prompt, "+new")
	assert.Contains(t, prompt, "x := 1")
	assert.Contains(t, prompt, "what does this do?")
}

// writeFakeClaude installs a shell script at path that emits the given
// JSON on stdout and exits 0. Used to exercise runQuestion without
// actually invoking the real Claude CLI.
func writeFakeClaude(t *testing.T, path, json string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\n", json)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

// openTestDB opens a temp SQLite DB and runs migrations. Mirrors the
// helper in internal/db but duplicated here to keep test boundaries
// clean.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// seedMR inserts a dummy MR and returns its ID.
func seedMR(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	repoID, err := d.UpsertRepo(ctx, "github.com", "acme", "widget")
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	mrID, err := d.UpsertMergeRequest(ctx, &db.MergeRequest{
		RepoID: repoID, PlatformID: 100, Number: 1,
		URL: "u", Title: "t", Author: "a", State: "open",
		CreatedAt: now, UpdatedAt: now, LastActivityAt: now,
	})
	require.NoError(t, err)
	return mrID
}

func TestRunQuestion_HappyPath(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	writeFakeClaude(t, fakeClaude, `{"type":"result","subtype":"success","is_error":false,"result":"the answer","session_id":"sess-xyz"}`)

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	mrID := seedMR(t, database)

	worktreePath := filepath.Join(tmp, "fake-worktree")
	require.NoError(os.MkdirAll(worktreePath, 0o755))

	runner := New(RunnerConfig{
		DB:          database,
		Clones:      gitclone.New(tmp, nil),
		WorktreeDir: tmp,
		HostFor:     func(string, string) string { return "github.com" },
	})

	thread, q, err := database.CreateAIThread(context.Background(), db.NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "go",
	})
	require.NoError(err)
	require.NoError(database.UpdateAIThreadSession(context.Background(), thread.ID, "", worktreePath))
	thread.WorktreePath = &worktreePath

	// Invoke runQuestion directly so we don't depend on real git.
	runner.runQuestion(context.Background(), thread, q, "hello")

	got, err := database.GetAIQuestion(context.Background(), q.ID)
	require.NoError(err)
	assert.Equal("done", got.Status)
	assert.Equal("the answer", got.Answer)

	ft, err := database.GetAIThread(context.Background(), thread.ID)
	require.NoError(err)
	require.NotNil(ft.ClaudeSessionID)
	assert.Equal("sess-xyz", *ft.ClaudeSessionID)
}

func TestRunQuestion_SubprocessFails(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	// Exits non-zero with some stderr.
	script := "#!/bin/sh\necho 'nope' >&2\nexit 2\n"
	require.NoError(os.WriteFile(fakeClaude, []byte(script), 0o755))

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	mrID := seedMR(t, database)

	wt := filepath.Join(tmp, "wt")
	require.NoError(os.MkdirAll(wt, 0o755))

	runner := New(RunnerConfig{
		DB: database, Clones: gitclone.New(tmp, nil),
		WorktreeDir: tmp, HostFor: func(string, string) string { return "github.com" },
	})

	thread, q, err := database.CreateAIThread(context.Background(), db.NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "go",
	})
	require.NoError(err)
	require.NoError(database.UpdateAIThreadSession(context.Background(), thread.ID, "", wt))
	thread.WorktreePath = &wt

	runner.runQuestion(context.Background(), thread, q, "hello")

	got, err := database.GetAIQuestion(context.Background(), q.ID)
	require.NoError(err)
	assert.Equal("failed", got.Status)
	assert.Contains(got.Error, "nope")
}

func TestCancelQuestion(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmp := t.TempDir()
	fakeClaude := filepath.Join(tmp, "claude.sh")
	// Sleep long enough to cancel mid-flight.
	script := "#!/bin/sh\nsleep 10\necho '{}'\n"
	require.NoError(os.WriteFile(fakeClaude, []byte(script), 0o755))

	orig := claudeBinary
	claudeBinary = fakeClaude
	t.Cleanup(func() { claudeBinary = orig })

	database := openTestDB(t)
	mrID := seedMR(t, database)

	wt := filepath.Join(tmp, "wt")
	require.NoError(os.MkdirAll(wt, 0o755))

	runner := New(RunnerConfig{
		DB: database, Clones: gitclone.New(tmp, nil),
		WorktreeDir: tmp, HostFor: func(string, string) string { return "github.com" },
	})

	thread, q, err := database.CreateAIThread(context.Background(), db.NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "go",
	})
	require.NoError(err)
	require.NoError(database.UpdateAIThreadSession(context.Background(), thread.ID, "", wt))
	thread.WorktreePath = &wt

	runner.spawnQuestion(thread, q, "sleep please")

	// Wait until the question shows as running before cancelling.
	require.Eventually(func() bool {
		got, _ := database.GetAIQuestion(context.Background(), q.ID)
		return got.Status == "running"
	}, 2*time.Second, 20*time.Millisecond, "never started running")

	require.NoError(runner.CancelQuestion(context.Background(), q.ID))

	got, err := database.GetAIQuestion(context.Background(), q.ID)
	require.NoError(err)
	assert.Equal("cancelled", got.Status)
}

func TestReconcileOnStartup(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	database := openTestDB(t)
	mrID := seedMR(t, database)
	ctx := context.Background()

	_, q, err := database.CreateAIThread(ctx, db.NewAIThreadInput{
		MergeRequestID: mrID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "go",
	})
	require.NoError(err)
	require.NoError(database.MarkAIQuestionRunning(ctx, q.ID, 9999))

	runner := New(RunnerConfig{
		DB: database, WorktreeDir: t.TempDir(),
		HostFor: func(string, string) string { return "github.com" },
	})
	require.NoError(runner.ReconcileOnStartup(ctx))

	got, err := database.GetAIQuestion(ctx, q.ID)
	require.NoError(err)
	assert.Equal("failed", got.Status)
	assert.Contains(got.Error, "interrupted")
}

// Ensure `exec.LookPath` resolves the fake binary. If not, tests
// silently fail — catch that up front.
func TestFakeClaudeIsExecutable(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "claude.sh")
	writeFakeClaude(t, p, `{"ok":true}`)
	out, err := exec.Command(p).CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, true, strings.Contains(string(out), `"ok":true`))
}
