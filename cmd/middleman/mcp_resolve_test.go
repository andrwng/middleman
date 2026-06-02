package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCwdHandleHit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := Assert.New(t)

	dir := t.TempDir()
	runGitInit(t, dir)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"owner":"local","name":"demo","number":7,"branch":"feat/a"}`))
	}))
	defer srv.Close()

	owner, name, number, err := resolveCwdHandle(srv.URL, dir)
	require.NoError(err)
	assert.Equal("local", owner)
	assert.Equal("demo", name)
	assert.Equal(7, number)
	assert.Contains(gotPath, "/api/v1/local/resolve?path=")
}

func TestResolveCwdHandleUnresolvable(t *testing.T) {
	require := require.New(t)
	// A non-git directory: git rev-parse --show-toplevel fails before any HTTP.
	_, _, _, err := resolveCwdHandle("http://127.0.0.1:0", t.TempDir())
	require.Error(err)
}

func runGitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"-C", dir, "init", "--initial-branch=feat/a", dir},
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "commit", "--allow-empty", "-m", "c1"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, string(out))
	}
}
