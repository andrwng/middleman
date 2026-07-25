package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/mcp"
)

// seedNamedWorktreeGit registers a local repo named name plus a worktree
// backed by a REAL git repo, mirroring seedReviewWorktreeGit
// (review_threads_branch_e2e_test.go) but parametrized on repo name so a
// single test can seed multiple distinctly-named local repos — that seeder
// hardcodes the repo name "demo", so it can't be reused as-is here. Returns
// the worktree id (PR-shaped "number") and its on-disk path.
func seedNamedWorktreeGit(t *testing.T, database *db.DB, name string) (int64, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=feat/a", dir)
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")

	repoID, err := database.UpsertLocalRepo(ctx, name)
	require.NoError(t, err)
	w, err := database.UpsertWorktree(ctx, repoID, db.ScannedWorktree{
		Path: dir, Branch: "feat/a", HeadSHA: "deadbeef",
	})
	require.NoError(t, err)
	return w.ID, dir
}

// callTool drives one MCP tool call through the real newline-delimited
// JSON-RPC 2.0 protocol that mcp.Server.Serve speaks (mirroring
// TestMCPProxyReplyHitsRealAPIPath in mcp_proxy_e2e_test.go) and returns the
// tool result's text content. It requires the call did not error: MCP tool
// errors surface as a result with isError=true rather than a
// transport-level failure, so asserting that here keeps every call site a
// clean one-liner instead of repeating the isError check everywhere.
func callTool(t *testing.T, m *mcp.Server, name string, args map[string]any) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	require.NoError(t, err)

	var out strings.Builder
	require.NoError(t, m.Serve(context.Background(), strings.NewReader(string(line)+"\n"), &out))

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.String()), &resp), "response: %s", out.String())
	require.False(t, resp.Result.IsError, "tool %q errored: %s", name, out.String())
	require.NotEmpty(t, resp.Result.Content, "tool %q returned no content: %s", name, out.String())
	return resp.Result.Content[0].Text
}

// TestMCPCrossRepoEndToEnd proves the cross-repo MCP feature end-to-end: a
// real MCP server (internal/mcp) driven over its real JSON-RPC protocol,
// talking to a real middleman HTTP server (httptest) backed by real SQLite,
// with TWO distinctly-named local git worktrees ("alpha" and "beta"). It
// covers discovery (list_repos), cross-repo create+read (start_thread /
// list_threads with an explicit repo), backward-compatible isolation of the
// bound default repo (no repo arg), and {repo, path} file-scoped listing.
func TestMCPCrossRepoEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	assert := Assert.New(t)

	srv, database := setupTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	alphaID, _ := seedNamedWorktreeGit(t, database, "alpha")
	_, _ = seedNamedWorktreeGit(t, database, "beta")

	// Bound to alpha; every scenario below either relies on that default or
	// explicitly targets beta to prove the cross-repo path.
	m := mcp.New(mcp.Config{
		ServerName: "middleman", BaseURL: ts.URL,
		ReviewOwner: "local", ReviewName: "alpha", ReviewNumber: int(alphaID),
	})

	// 1. Discovery: list_repos enumerates both local repos.
	repos := callTool(t, m, "list_repos", map[string]any{})
	assert.Contains(repos, "alpha")
	assert.Contains(repos, "beta")

	// 2. Cross-repo create + read: start_thread explicitly targets beta
	// while the server is bound to alpha; list_threads{repo:beta} sees it.
	callTool(t, m, "start_thread", map[string]any{
		"repo": "beta", "path": "README.md", "side": "RIGHT", "line": float64(1),
		"body": "cross-repo note",
	})
	betaThreads := callTool(t, m, "list_threads", map[string]any{"repo": "beta"})
	assert.Contains(betaThreads, "cross-repo note")

	// 3. Backward-compat / isolation: no repo arg falls back to the bound
	// default (alpha) — beta's thread must not leak into alpha's listing.
	alphaThreads := callTool(t, m, "list_threads", map[string]any{})
	assert.NotContains(alphaThreads, "cross-repo note")

	// 4. {repo, path} file-scope: a second beta thread on a different file
	// must be excluded when listing scoped to README.md.
	callTool(t, m, "start_thread", map[string]any{
		"repo": "beta", "path": "OTHER.md", "side": "RIGHT", "line": float64(1),
		"body": "other-file note",
	})
	scoped := callTool(t, m, "list_threads", map[string]any{"repo": "beta", "path": "README.md"})
	assert.Contains(scoped, "cross-repo note")
	assert.NotContains(scoped, "other-file note")
}
