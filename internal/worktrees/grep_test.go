package worktrees

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesm/middleman/internal/gitclone"
)

// TestGrepSymbol covers, as subtests:
//   - sentinel SHA finds a symbol in an UNCOMMITTED working-tree edit
//     (this is the case a committed-revision grep would miss)
//   - a committed revision SHA finds the committed content and does NOT
//     see the uncommitted edit
//   - paths come back repo-relative with no "<rev>:" prefix in either mode
//   - a symbol absent from the tree returns (nil, nil), not an error
//   - word boundaries hold: searching "Foo" does not match "FooBar"
//   - the match is a fixed string: a symbol containing regex
//     metacharacters (e.g. "[") is matched literally
//   - an untracked file is not searched (documented behavior)
func TestGrepSymbol(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	ctx := context.Background()

	dir := t.TempDir()
	runGitT(t, "", "init", "--initial-branch=main", dir)
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")

	// committed is the tree as of HEAD: line 2 embeds "Foo" only as a
	// substring of "FooBar", to prove -w rejects the substring match;
	// line 3 holds a symbol that exists at HEAD; line 4 holds "[" and
	// "]", regex metacharacters that -F must still match literally.
	const committed = "package widgets\n" +
		"func FooBar() {}\n" +
		"var CommittedSymbol = 1\n" +
		"var lookup map[string]int\n"
	trackedPath := filepath.Join(dir, "tracked.go")
	require.NoError(t, os.WriteFile(trackedPath, []byte(committed), 0o644))
	runGitT(t, dir, "add", "tracked.go")
	runGitT(t, dir, "commit", "-m", "add tracked.go")
	headSHA := gitHeadT(t, dir)

	// Append a 5th line, unstaged and never committed, whose symbol
	// exists only on disk. A grep of headSHA must never see it.
	edited := committed + "var UncommittedSymbol = 2\n"
	require.NoError(t, os.WriteFile(trackedPath, []byte(edited), 0o644))

	// untracked.go sits on disk but was never `git add`ed.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "untracked.go"),
		[]byte("package widgets\nvar UntrackedSymbol = 3\n"),
		0o644,
	))

	t.Run("sentinel SHA finds a symbol in an uncommitted working-tree edit", func(t *testing.T) {
		hits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "UncommittedSymbol")
		require.NoError(t, err)
		assert.Equal(t, []gitclone.SymbolHit{
			{Path: "tracked.go", Line: 5, Text: "var UncommittedSymbol = 2"},
		}, hits)
	})

	t.Run("committed revision finds committed content but not the uncommitted edit", func(t *testing.T) {
		committedHits, err := GrepSymbol(ctx, dir, headSHA, "CommittedSymbol")
		require.NoError(t, err)
		assert.Equal(t, []gitclone.SymbolHit{
			{Path: "tracked.go", Line: 3, Text: "var CommittedSymbol = 1"},
		}, committedHits)

		uncommittedHits, err := GrepSymbol(ctx, dir, headSHA, "UncommittedSymbol")
		require.NoError(t, err)
		assert.Nil(t, uncommittedHits)
	})

	t.Run("paths come back repo-relative with no rev prefix in either mode", func(t *testing.T) {
		sentinelHits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "CommittedSymbol")
		require.NoError(t, err)
		require.Len(t, sentinelHits, 1)
		assert.Equal(t, "tracked.go", sentinelHits[0].Path)

		revHits, err := GrepSymbol(ctx, dir, headSHA, "CommittedSymbol")
		require.NoError(t, err)
		require.Len(t, revHits, 1)
		assert.Equal(t, "tracked.go", revHits[0].Path)
	})

	t.Run("symbol absent from the tree returns nil, nil", func(t *testing.T) {
		hits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "NoSuchSymbolAnywhereXYZ")
		require.NoError(t, err)
		assert.Nil(t, hits)
	})

	t.Run("word boundaries hold: Foo does not match FooBar", func(t *testing.T) {
		hits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "Foo")
		require.NoError(t, err)
		assert.Nil(t, hits)
	})

	t.Run("regex metacharacters in the symbol are matched literally", func(t *testing.T) {
		// "[" and "]" are regex metacharacters; -F must still match
		// the literal text rather than treating "[string]" as a
		// bracket expression (which would match one character from
		// the set {s,t,r,i,n,g} in place of the literal brackets,
		// and so would not match this line at all).
		hits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "map[string]int")
		require.NoError(t, err)
		assert.Equal(t, []gitclone.SymbolHit{
			{Path: "tracked.go", Line: 4, Text: "var lookup map[string]int"},
		}, hits)
	})

	t.Run("an untracked file is not searched", func(t *testing.T) {
		hits, err := GrepSymbol(ctx, dir, WorkingTreeSentinel, "UntrackedSymbol")
		require.NoError(t, err)
		assert.Nil(t, hits)
	})
}
