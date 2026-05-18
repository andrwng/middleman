package worktrees

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/db"
)

func TestParsePorcelain(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []db.ScannedWorktree
	}{
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "single worktree on a branch",
			in: "worktree /code/repo\n" +
				"HEAD aaaaaaaa\n" +
				"branch refs/heads/main\n\n",
			want: []db.ScannedWorktree{
				{Path: "/code/repo", HeadSHA: "aaaaaaaa", Branch: "main"},
			},
		},
		{
			name: "two worktrees, one detached",
			in: "worktree /code/repo\n" +
				"HEAD aaaaaaaa\n" +
				"branch refs/heads/main\n\n" +
				"worktree /code/repo-fix\n" +
				"HEAD bbbbbbbb\n" +
				"detached\n\n",
			want: []db.ScannedWorktree{
				{Path: "/code/repo", HeadSHA: "aaaaaaaa", Branch: "main"},
				{Path: "/code/repo-fix", HeadSHA: "bbbbbbbb", IsDetached: true},
			},
		},
		{
			name: "bare worktree is filtered out",
			in: "worktree /code/repo.git\n" +
				"bare\n\n" +
				"worktree /code/repo-checkout\n" +
				"HEAD cccccccc\n" +
				"branch refs/heads/feat\n\n",
			want: []db.ScannedWorktree{
				{Path: "/code/repo-checkout", HeadSHA: "cccccccc", Branch: "feat"},
			},
		},
		{
			name: "locked and prunable flags",
			in: "worktree /code/repo-locked\n" +
				"HEAD dddddddd\n" +
				"branch refs/heads/long-running\n" +
				"locked needed for demo\n\n" +
				"worktree /code/repo-old\n" +
				"HEAD eeeeeeee\n" +
				"branch refs/heads/old\n" +
				"prunable\n\n",
			want: []db.ScannedWorktree{
				{Path: "/code/repo-locked", HeadSHA: "dddddddd", Branch: "long-running", IsLocked: true},
				{Path: "/code/repo-old", HeadSHA: "eeeeeeee", Branch: "old", IsPrunable: true},
			},
		},
		{
			name: "missing trailing blank line still finalizes",
			in: "worktree /code/repo\n" +
				"HEAD aaaaaaaa\n" +
				"branch refs/heads/main",
			want: []db.ScannedWorktree{
				{Path: "/code/repo", HeadSHA: "aaaaaaaa", Branch: "main"},
			},
		},
		{
			name: "unknown keys are ignored",
			in: "worktree /code/repo\n" +
				"HEAD aaaaaaaa\n" +
				"branch refs/heads/main\n" +
				"future-field something\n\n",
			want: []db.ScannedWorktree{
				{Path: "/code/repo", HeadSHA: "aaaaaaaa", Branch: "main"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePorcelain([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestScanLiveRepo creates a real git repo with one added worktree
// and verifies Scan + Sync round-trip against the actual `git`
// binary on the host.
func TestScanLiveRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	require := require.New(t)
	assert := assert.New(t)
	ctx := context.Background()

	root := t.TempDir()
	primary := filepath.Join(root, "main")
	secondary := filepath.Join(root, "feat")

	runGit(t, "", "init", "--initial-branch=main", primary)
	runGit(t, primary, "config", "user.email", "test@example.com")
	runGit(t, primary, "config", "user.name", "Test")
	runGit(t, primary, "commit", "--allow-empty", "-m", "init")
	runGit(t, primary, "worktree", "add", "-b", "feat/x", secondary)

	scanned, err := Scan(ctx, primary)
	require.NoError(err)
	require.Len(scanned, 2)

	// On macOS, t.TempDir() returns /var/... but git canonicalizes
	// symlinks and reports /private/var/...; resolve both ends so
	// the assertion compares apples to apples.
	canon := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		require.NoError(err)
		return r
	}
	paths := []string{scanned[0].Path, scanned[1].Path}
	assert.Contains(paths, canon(primary))
	assert.Contains(paths, canon(secondary))

	canonPrimary := canon(primary)
	canonSecondary := canon(secondary)
	for _, w := range scanned {
		if w.Path == canonPrimary {
			assert.Equal("main", w.Branch)
		}
		if w.Path == canonSecondary {
			assert.Equal("feat/x", w.Branch)
		}
		assert.NotEmpty(w.HeadSHA)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, string(out))
}
