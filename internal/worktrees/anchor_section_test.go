package worktrees

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFileSection exercises fileSection directly, against hand-written diff
// text, so this coverage runs in the default (non-integration) suite.
// fileSection is a pure []byte -> (string, []byte, bool) function: no git
// needed to call it.
//
// The main thing under test is the multi-file case: a real "git diff" for
// our path is never alone in the output, so fileSection has to isolate
// exactly one file's section without leaking a neighboring section's
// hunks into ours (which would corrupt the line arithmetic) or matching
// the wrong file by an accidental prefix collision.
func TestFileSection(t *testing.T) {
	// Two easy-to-tell-apart single-hunk sections.
	aSection := "diff --git a/a.txt b/a.txt\n" +
		"index 111..222 100644\n" +
		"--- a/a.txt\n+++ b/a.txt\n" +
		"@@ -0,0 +1,3 @@\n+new1\n+new2\n+new3\n"
	zSection := "diff --git a/z.txt b/z.txt\n" +
		"index 333..444 100644\n" +
		"--- a/z.txt\n+++ b/z.txt\n" +
		"@@ -2,0 +3 @@ yyy\n+www\n"

	// A same-prefixed pair: "api.go" is a prefix of "api.go.bak", so this
	// is where an off-by-the-space match would show up.
	apiSection := "diff --git a/api.go b/api.go\n" +
		"index 555..666 100644\n" +
		"--- a/api.go\n+++ b/api.go\n" +
		"@@ -1,0 +2,1 @@\n+x\n"
	apiBakSection := "diff --git a/api.go.bak b/api.go.bak\n" +
		"index 777..888 100644\n" +
		"--- a/api.go.bak\n+++ b/api.go.bak\n" +
		"@@ -5,0 +6,1 @@\n+y\n"

	spacedSection := "diff --git a/my file.go b/my file.go\n" +
		"--- a/my file.go\n+++ b/my file.go\n" +
		"@@ -1,0 +2,1 @@\n+x\n"

	cases := []struct {
		name      string
		diff      string
		path      string
		wantPath  string
		wantHunks []Hunk
	}{
		{
			name:      "target section followed by another file's section",
			diff:      aSection + zSection,
			path:      "a.txt",
			wantPath:  "a.txt",
			wantHunks: []Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 3}},
		},
		{
			name:      "target section last, preceded by another file's section",
			diff:      zSection + aSection,
			path:      "a.txt",
			wantPath:  "a.txt",
			wantHunks: []Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 3}},
		},
		{
			name:      "path containing a space matches by exact prefix, not a whitespace split",
			diff:      spacedSection,
			path:      "my file.go",
			wantPath:  "my file.go",
			wantHunks: []Hunk{{OldStart: 1, OldCount: 0, NewStart: 2, NewCount: 1}},
		},
		{
			name:      "prefix collision: api.go's own section is found, not api.go.bak's",
			diff:      apiSection + apiBakSection,
			path:      "api.go",
			wantPath:  "api.go",
			wantHunks: []Hunk{{OldStart: 1, OldCount: 0, NewStart: 2, NewCount: 1}},
		},
		{
			name:      "prefix collision the other way round: api.go.bak's section comes first",
			diff:      apiBakSection + apiSection,
			path:      "api.go",
			wantPath:  "api.go",
			wantHunks: []Hunk{{OldStart: 1, OldCount: 0, NewStart: 2, NewCount: 1}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			newPath, section, found := fileSection([]byte(tc.diff), tc.path)
			assert.True(found)
			assert.Equal(tc.wantPath, newPath)
			assert.Equal(tc.wantHunks, ParseHunks(section))
		})
	}
}

// TestClassifyMapped exercises the current/moved/removed decision
// AnchorDiff.Resolve makes once it already has a mapped line number and new
// path. Splitting it out as classifyMapped lets this run without shelling
// out to git -- in particular the AnchorCurrent case here (a file's diff
// section exists, but this specific line sits outside every hunk in it) is
// not reachable from any assertion in the git-shelling integration test.
func TestClassifyMapped(t *testing.T) {
	assert := assert.New(t)

	// A section exists for the file, but this line's own position and path
	// are untouched by any hunk in it: still current.
	got := classifyMapped("a.txt", 2, "a.txt", 2, false)
	assert.Equal(ResolvedAnchor{State: AnchorCurrent, Path: "a.txt", Line: 2}, got)

	// The line shifted, same path: moved.
	got = classifyMapped("a.txt", 5, "a.txt", 8, false)
	assert.Equal(ResolvedAnchor{State: AnchorMoved, Path: "a.txt", Line: 8}, got)

	// Same line number, but the file was renamed: still moved.
	got = classifyMapped("a.txt", 5, "b.txt", 5, false)
	assert.Equal(ResolvedAnchor{State: AnchorMoved, Path: "b.txt", Line: 5}, got)

	// The line itself was deleted: removed, regardless of path/line inputs.
	got = classifyMapped("a.txt", 5, "a.txt", 0, true)
	assert.Equal(ResolvedAnchor{State: AnchorRemoved}, got)
}
