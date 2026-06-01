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
	}, "what does this do?", nil)

	assert.Contains(t, prompt, "PR #1: fix things")
	assert.Contains(t, prompt, "File: foo.go")
	assert.Contains(t, prompt, "RIGHT side")
	assert.Contains(t, prompt, "abc1234")
	assert.Contains(t, prompt, "+new")
	assert.Contains(t, prompt, "x := 1")
	assert.Contains(t, prompt, "what does this do?")
	// No hunk range → single-line phrasing.
	assert.Contains(t, prompt, "Anchored line: 42")
}

func TestBriefPrompt_OverrideFile_WithPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/brief-prompt.md"
	require.NoError(t, os.WriteFile(path,
		[]byte("MY CUSTOM RULES\n\n{{CONTEXT}}\n\nSignoff."), 0o644))

	runner := New(RunnerConfig{BriefPromptFile: path})
	got := runner.briefPrompt(BriefInput{HeadSHA: "abc1234", Depth: "quick"}, nil)
	assert.Contains(t, got, "MY CUSTOM RULES")
	assert.Contains(t, got, "Head SHA: abc1234")
	assert.Contains(t, got, "Signoff.")
	assert.NotContains(t, got, "You are generating a structural review brief")
}

func TestBriefPrompt_OverrideFile_WithoutPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/brief-prompt.md"
	require.NoError(t, os.WriteFile(path, []byte("RULES ONLY."), 0o644))

	runner := New(RunnerConfig{BriefPromptFile: path})
	got := runner.briefPrompt(BriefInput{HeadSHA: "abc1234", Depth: "quick"}, nil)
	// Context block is appended at the end.
	assert.Contains(t, got, "RULES ONLY.")
	assert.Contains(t, got, "Head SHA: abc1234")
}

func TestBriefPrompt_FallbackOnReadError(t *testing.T) {
	runner := New(RunnerConfig{BriefPromptFile: "/nonexistent/path/brief-prompt.md"})
	got := runner.briefPrompt(BriefInput{HeadSHA: "abc1234", Depth: "quick"}, nil)
	// Falls back to the built-in prompt.
	assert.Contains(t, got, "You are generating a structural review brief")
	assert.Contains(t, got, "Head SHA: abc1234")
}

// TestBriefPrompt_StructureLock guards against accidental deletion of
// any required section heading or anti-padding rule from the built-in
// brief prompt. Brief output structure is part of the contract with
// the UI's section parser (and the reviewer's expectations); silent
// drift here would surface as missing sections in the rendered card.
func TestBriefPrompt_StructureLock(t *testing.T) {
	runner := New(RunnerConfig{})
	got := runner.briefPrompt(BriefInput{HeadSHA: "abc1234", Depth: "quick"}, nil)
	assert := assert.New(t)
	for _, heading := range []string{
		"## Subsystem", "## Layers", "## Intent", "## Before", "## After",
		"## Mechanics", "## Commits", "## Risk surface", "## Open questions",
	} {
		assert.Contains(got, heading, "missing section %q", heading)
	}
	// Anti-padding cues that callers (and Claude) rely on.
	assert.Contains(got, "Map")
	assert.Contains(got, "Compass")
	assert.Contains(got, "Padding is worse than omitting")
	// Layer-resolution rule must stay in the prompt — without it
	// Claude defaults to whatever shape it picks first.
	assert.Contains(got, "concrete identifiers")
	assert.Contains(got, "concept labels")
}

func TestBuildPrompt_MultiLineRange(t *testing.T) {
	start := 40
	end := 43
	prompt := buildPrompt(CreateThreadInput{
		Path:          "foo.go",
		AnchorSide:    "RIGHT",
		AnchorLine:    43,
		HunkStartLine: &start,
		HunkEndLine:   &end,
		CommitSHA:     "abc1234",
	}, "why?", nil)

	assert.Contains(t, prompt, "Anchored lines: 40-43 (RIGHT side)")
	assert.NotContains(t, prompt, "Anchored line: 43")
}

func TestCommitCacheNotice(t *testing.T) {
	// Empty list → no notice (don't lie about resources).
	assert.Empty(t, commitCacheNotice(nil))
	assert.Empty(t, commitCacheNotice([]string{}))

	got := commitCacheNotice([]string{"abc1234", "def5678"})
	assert.Contains(t, got, ".middleman-commits/<full-commit-sha>.diff")
	assert.Contains(t, got, "abc1234")
	assert.Contains(t, got, "def5678")
	// Buildprompt callers should be able to find this section by header.
	assert.Contains(t, got, "Per-commit context:")
}

func TestBuildPrompt_IncludesCommitCacheNotice(t *testing.T) {
	prompt := buildPrompt(CreateThreadInput{
		Path: "foo.go", AnchorSide: "RIGHT", AnchorLine: 1,
		CommitSHA: "abc1234",
	}, "?", []string{"abc1234"})
	assert.Contains(t, prompt, "Per-commit context:")
	assert.Contains(t, prompt, "abc1234")
}

func TestCachePRCommits_WritesFiles(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	// Stand up a bare repo + worktree at the latest commit, with a
	// PR-style range merge_base..head. We then run cachePRCommits
	// against the partition and verify each commit got a file.
	root := t.TempDir()
	bareDir := filepath.Join(root, "github.com", "acme", "widget.git")
	require.NoError(os.MkdirAll(filepath.Dir(bareDir), 0o755))

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(err, "git %v: %s", args, out)
	}
	runGit(root, "init", "--bare", "--initial-branch=main", bareDir)

	work := filepath.Join(root, "work")
	runGit(root, "clone", bareDir, work)
	runGit(work, "config", "user.email", "t@t")
	runGit(work, "config", "user.name", "T")

	require.NoError(os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o644))
	runGit(work, "add", ".")
	runGit(work, "commit", "-m", "M0")
	runGit(work, "push", "origin", "main")

	mergeBaseCmd := exec.Command("git", "rev-parse", "HEAD")
	mergeBaseCmd.Dir = work
	mbOut, err := mergeBaseCmd.Output()
	require.NoError(err)
	mergeBase := strings.TrimSpace(string(mbOut))

	runGit(work, "checkout", "-b", "pr")
	require.NoError(os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644))
	runGit(work, "add", ".")
	runGit(work, "commit", "-m", "first PR commit\n\nbody one")
	require.NoError(os.WriteFile(filepath.Join(work, "b.txt"), []byte("b\n"), 0o644))
	runGit(work, "add", ".")
	runGit(work, "commit", "-m", "second PR commit")
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = work
	hOut, err := headCmd.Output()
	require.NoError(err)
	head := strings.TrimSpace(string(hOut))
	runGit(work, "push", "origin", "pr")

	// Worktree under root/wt to receive the cache.
	worktreePath := filepath.Join(root, "wt")
	runGit(bareDir, "worktree", "add", "--detach", worktreePath, head)

	runner := New(RunnerConfig{
		Clones:  gitclone.New(root, nil),
		HostFor: func(string, string) string { return "github.com" },
	})

	got := runner.cachePRCommits(ctx, "acme", "widget", worktreePath, mergeBase, head)
	require.Len(got, 2, "expected 2 first-parent commits cached")

	for _, sha := range got {
		path := filepath.Join(worktreePath, commitCacheDirName, sha+".diff")
		body, err := os.ReadFile(path)
		require.NoError(err, "expected cache file at %s", path)
		// Each cached file must contain the header (commit + author)
		// and a diff section, since we used --format=fuller.
		assert.Contains(string(body), "commit "+sha)
		assert.Contains(string(body), "AuthorDate:")
		assert.Contains(string(body), "diff --git")
	}
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

func TestAutoCloseClosedPRThreads(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	database := openTestDB(t)
	ctx := context.Background()

	// Two MRs: one still open, one closed/merged.
	openMRID := seedMR(t, database)
	repoID, err := database.UpsertRepo(ctx, "github.com", "acme", "other")
	require.NoError(err)
	now := time.Now().UTC().Truncate(time.Second)
	closedMRID, err := database.UpsertMergeRequest(ctx, &db.MergeRequest{
		RepoID: repoID, PlatformID: 200, Number: 2,
		URL: "u2", Title: "t2", Author: "a", State: "closed",
		CreatedAt: now, UpdatedAt: now, LastActivityAt: now,
	})
	require.NoError(err)

	openThread, _, err := database.CreateAIThread(ctx, db.NewAIThreadInput{
		MergeRequestID: openMRID, Path: "x.go", AnchorSide: "RIGHT",
		AnchorLine: 1, CommitSHA: "abc", Question: "q-open",
	})
	require.NoError(err)
	closedPRThread, _, err := database.CreateAIThread(ctx, db.NewAIThreadInput{
		MergeRequestID: closedMRID, Path: "y.go", AnchorSide: "RIGHT",
		AnchorLine: 2, CommitSHA: "def", Question: "q-closed",
	})
	require.NoError(err)

	runner := New(RunnerConfig{
		DB: database, WorktreeDir: t.TempDir(),
		HostFor: func(string, string) string { return "github.com" },
	})

	closed, err := runner.AutoCloseClosedPRThreads(ctx)
	require.NoError(err)
	assert.Equal(1, closed)

	openAfter, err := database.GetAIThread(ctx, openThread.ID)
	require.NoError(err)
	assert.Equal("active", openAfter.Status, "thread on still-open PR must stay active")

	closedAfter, err := database.GetAIThread(ctx, closedPRThread.ID)
	require.NoError(err)
	assert.Equal("closed", closedAfter.Status, "thread on closed PR must auto-close")

	// Idempotent: second call closes nothing more.
	closedAgain, err := runner.AutoCloseClosedPRThreads(ctx)
	require.NoError(err)
	assert.Equal(0, closedAgain)
}

// Ensure `exec.LookPath` resolves the fake binary. If not, tests
// silently fail — catch that up front.
func TestFakeClaudeIsExecutable(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "claude.sh")
	writeFakeClaude(t, p, `{"ok":true}`)
	out, err := exec.Command(p).CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), `"ok":true`)
}
