package aireview

import (
	"context"
	"encoding/json"
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

// newReviewRunFixture builds a bare clone of acme/widget with a
// two-commit PR branch, a DB with the matching MR, and a Runner that
// provisions review-run worktrees from that clone.
func newReviewRunFixture(t *testing.T) (*Runner, *db.DB, int64, string, string) {
	t.Helper()
	require := require.New(t)
	root := t.TempDir()
	bareDir := filepath.Join(root, "clones", "github.com", "acme", "widget.git")
	require.NoError(os.MkdirAll(filepath.Dir(bareDir), 0o755))
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "--bare", "--initial-branch=main", bareDir)
	work := filepath.Join(root, "work")
	git(root, "clone", bareDir, work)
	git(work, "config", "user.email", "t@t")
	git(work, "config", "user.name", "T")
	require.NoError(os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o644))
	git(work, "add", ".")
	git(work, "commit", "-m", "M0")
	git(work, "push", "origin", "main")
	base := git(work, "rev-parse", "HEAD")
	git(work, "checkout", "-b", "pr")
	require.NoError(os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	git(work, "add", ".")
	git(work, "commit", "-m", "PR commit")
	head := git(work, "rev-parse", "HEAD")
	git(work, "push", "origin", "pr")

	database := openTestDB(t)
	mrID := seedMR(t, database)
	r := New(RunnerConfig{
		DB:          database,
		Clones:      gitclone.New(filepath.Join(root, "clones"), nil),
		WorktreeDir: filepath.Join(root, "ai-review"),
		HostFor:     func(string, string) string { return "github.com" },
	})
	return r, database, mrID, head, base
}

func writeFakeReviewClaude(t *testing.T, path, argsFile, body string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"prev=''\nfor a in \"$@\"; do if [ \"$prev\" = \"--mcp-config\" ]; then cp \"$a\" " + argsFile + ".mcp.json; fi; prev=\"$a\"; done\n" +
		body
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

const fakeStreamOK = `echo '{"type":"system","subtype":"init","session_id":"sess-root"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__middleman__propose_comment","input":{"path":"a.txt"}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Found one issue.","session_id":"sess-root"}'
`

func waitRunStatus(t *testing.T, database *db.DB, id int64, status string) *db.AIReviewRun {
	t.Helper()
	var got *db.AIReviewRun
	require.Eventually(t, func() bool {
		got, _ = database.GetAIReviewRun(context.Background(), id)
		return got != nil && got.Status == status
	}, 10*time.Second, 20*time.Millisecond)
	return got
}

func TestWriteMCPConfigRunMode(t *testing.T) {
	require := require.New(t)
	path, cleanup, err := writeMCPConfig(MCPConfig{Binary: "/bin/middleman", BaseURL: "http://x", Owner: "acme", Name: "widget", Number: 1, RunID: 7})
	require.NoError(err)
	t.Cleanup(cleanup)
	raw, err := os.ReadFile(path)
	require.NoError(err)
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(json.Unmarshal(raw, &cfg))
	require.Contains(strings.Join(cfg.MCPServers["middleman"].Args, " "), "--run 7")
}

func TestReviewRunSuccessStoresSessionSummaryAndArgs(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	r, database, mrID, head, base := newReviewRunFixture(t)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, argsFile, fakeStreamOK)
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })

	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head, Instructions: "check errors"})
	require.NoError(err)
	require.NoError(r.StartReviewRun(ctx, ReviewRunInput{
		RunID: run.ID, Owner: "acme", Name: "widget", Number: 1, HeadSHA: head, MergeBaseSHA: base,
		Instructions: "check errors", PromptContext: "PR #1: test",
		MCP: MCPConfig{Binary: "/bin/middleman", BaseURL: "http://127.0.0.1:1", Owner: "acme", Name: "widget", Number: 1, RunID: run.ID},
	}))

	got := waitRunStatus(t, database, run.ID, "done")
	assert.Equal("sess-root", got.ClaudeSessionID)
	assert.Equal("Found one issue.", got.Summary)
	assert.Contains(got.RawJSON, "mcp__middleman__propose_comment")
	assert.Equal(ReviewRunWorktreePath(r.rootDir, run.ID), got.WorktreePath)

	args, err := os.ReadFile(argsFile)
	require.NoError(err)
	argv := string(args)
	assert.Contains(argv, "stream-json")
	assert.Contains(argv, "--strict-mcp-config")
	assert.Contains(argv, reviewRunToolNames)
	assert.Contains(argv, "Edit,Write,NotebookEdit,Bash,Agent")
	assert.Contains(argv, "check errors")
	mcpCfg, err := os.ReadFile(argsFile + ".mcp.json")
	require.NoError(err)
	assert.Contains(string(mcpCfg), "--run")

	// The worktree is torn down once the run is over.
	require.Eventually(func() bool {
		_, statErr := os.Stat(ReviewRunWorktreePath(r.rootDir, run.ID))
		return os.IsNotExist(statErr)
	}, 5*time.Second, 20*time.Millisecond)
}

func TestReviewRunFailureAndTimeout(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		timeout   time.Duration
		wantError string
	}{
		{"nonzero exit", "echo 'boom' >&2\nexit 3\n", 0, "claude exited"},
		{"timeout", "sleep 30\n", 300 * time.Millisecond, "timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, database, mrID, head, base := newReviewRunFixture(t)
			if tc.timeout > 0 {
				r.reviewRunTimeout = tc.timeout
			}
			dir := t.TempDir()
			fake := filepath.Join(dir, "claude.sh")
			writeFakeReviewClaude(t, fake, filepath.Join(dir, "args.txt"), tc.body)
			orig := SetBinaryForTest(fake)
			t.Cleanup(func() { SetBinaryForTest(orig) })
			ctx := context.Background()
			run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
			require.NoError(t, err)
			require.NoError(t, r.StartReviewRun(ctx, ReviewRunInput{RunID: run.ID, Owner: "acme", Name: "widget", Number: 1, HeadSHA: head, MergeBaseSHA: base}))
			got := waitRunStatus(t, database, run.ID, "failed")
			assert.Contains(t, got.Error, tc.wantError)
		})
	}
}

func TestReviewRunCancel(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, base := newReviewRunFixture(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, filepath.Join(dir, "args.txt"), "sleep 30\n")
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(err)
	require.NoError(r.StartReviewRun(ctx, ReviewRunInput{RunID: run.ID, Owner: "acme", Name: "widget", Number: 1, HeadSHA: head, MergeBaseSHA: base}))
	waitRunStatus(t, database, run.ID, "running")
	require.NoError(r.CancelReviewRun(ctx, run.ID))
	got, err := database.GetAIReviewRun(ctx, run.ID)
	require.NoError(err)
	require.Equal("cancelled", got.Status)
	// The goroutine must not overwrite the cancelled status on exit.
	time.Sleep(200 * time.Millisecond)
	got, err = database.GetAIReviewRun(ctx, run.ID)
	require.NoError(err)
	require.Equal("cancelled", got.Status)
}

func TestReconcileMarksActiveReviewRunsFailed(t *testing.T) {
	require := require.New(t)
	r, database, mrID, head, _ := newReviewRunFixture(t)
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(err)
	require.NoError(r.ReconcileOnStartup(ctx))
	got, err := database.GetAIReviewRun(ctx, run.ID)
	require.NoError(err)
	require.Equal("failed", got.Status)
	require.Contains(got.Error, "restart")
}

// A run that fails after Claude started still records its session, so
// the suggestions it already made can be discussed (forked) later.
func TestReviewRunFailureKeepsSessionID(t *testing.T) {
	r, database, mrID, head, base := newReviewRunFixture(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude.sh")
	writeFakeReviewClaude(t, fake, filepath.Join(dir, "args.txt"),
		"echo '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"sess-early\"}'\nexit 3\n")
	orig := SetBinaryForTest(fake)
	t.Cleanup(func() { SetBinaryForTest(orig) })
	ctx := context.Background()
	run, err := database.CreateAIReviewRun(ctx, db.NewAIReviewRun{MergeRequestID: mrID, HeadSHA: head, ReadSHA: head})
	require.NoError(t, err)
	require.NoError(t, r.StartReviewRun(ctx, ReviewRunInput{RunID: run.ID, Owner: "acme", Name: "widget", Number: 1, HeadSHA: head, MergeBaseSHA: base}))
	got := waitRunStatus(t, database, run.ID, "failed")
	assert.Equal(t, "sess-early", got.ClaudeSessionID)
}
