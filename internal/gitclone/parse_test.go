package gitclone

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRawZ(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// git diff --raw -z output: status\0path\0 (NUL-delimited)
	// M for modified, A for added, D for deleted
	// Renames: R100\0oldpath\0newpath\0
	raw := ":100644 100644 abc def M\x00src/main.go\x00" +
		":000000 100644 000 abc A\x00src/new.go\x00" +
		":100644 000000 abc 000 D\x00src/old.go\x00" +
		":100644 100644 abc def R100\x00src/before.go\x00src/after.go\x00"

	files := ParseRawZ([]byte(raw))
	require.Len(files, 4)

	assert.Equal("src/main.go", files[0].Path)
	assert.Equal("modified", files[0].Status)

	assert.Equal("src/new.go", files[1].Path)
	assert.Equal("added", files[1].Status)

	assert.Equal("src/old.go", files[2].Path)
	assert.Equal("deleted", files[2].Status)

	assert.Equal("src/after.go", files[3].Path)
	assert.Equal("src/before.go", files[3].OldPath)
	assert.Equal("renamed", files[3].Status)
}

func TestParsePatch(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	patch := `diff --git a/src/main.go b/src/main.go
index abc..def 100644
--- a/src/main.go
+++ b/src/main.go
@@ -10,6 +10,8 @@ func main() {
 	fmt.Println("hello")
 	fmt.Println("world")
+	fmt.Println("new line 1")
+	fmt.Println("new line 2")
 	fmt.Println("end")
-	fmt.Println("removed")
 }
`

	// Provide pre-populated file metadata from --raw -z.
	rawFiles := []DiffFile{
		{Path: "src/main.go", OldPath: "src/main.go", Status: "modified"},
	}

	files := ParsePatch([]byte(patch), rawFiles)
	require.Len(files, 1)

	f := files[0]
	assert.Equal("src/main.go", f.Path)
	assert.Equal(2, f.Additions)
	assert.Equal(1, f.Deletions)
	assert.False(f.IsBinary)

	require.Len(f.Hunks, 1)
	h := f.Hunks[0]
	assert.Equal(10, h.OldStart)
	assert.Equal(6, h.OldCount)
	assert.Equal(10, h.NewStart)
	assert.Equal(8, h.NewCount)
	assert.Equal("func main() {", h.Section)

	// Check line types.
	types := make([]string, len(h.Lines))
	for i, l := range h.Lines {
		types[i] = l.Type
	}
	assert.Equal([]string{
		"context", "context", "add", "add", "context", "delete", "context",
	}, types)
}

// TestParsePatchMatchesByPathNotPosition pins the fix for a positional
// mismatch bug: when the patch pass omits a section for a raw file (e.g. a
// whitespace-only file under `git diff -w`), every later raw file used to
// get the wrong hunks/counts because ParsePatch matched by index. Here the
// raw list has 3 files but the patch has sections only for files 1 and 3
// (file 2's section is missing, simulating the omission); file 3's hunks
// must land on file 3, and file 2 must end up with empty hunks rather than
// stealing file 3's.
func TestParsePatchMatchesByPathNotPosition(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	patch := `diff --git a/one.go b/one.go
index aaa..bbb 100644
--- a/one.go
+++ b/one.go
@@ -1,1 +1,2 @@
 line1
+added-in-one
diff --git a/three.go b/three.go
index ccc..ddd 100644
--- a/three.go
+++ b/three.go
@@ -1,1 +1,2 @@
 line1
+added-in-three
`

	rawFiles := []DiffFile{
		{Path: "one.go", OldPath: "one.go", Status: "modified"},
		{Path: "two.go", OldPath: "two.go", Status: "modified"},
		{Path: "three.go", OldPath: "three.go", Status: "modified"},
	}

	files := ParsePatch([]byte(patch), rawFiles)
	require.Len(files, 3)

	byPath := make(map[string]DiffFile, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	three := byPath["three.go"]
	require.Len(three.Hunks, 1)
	assert.Equal(1, three.Additions)
	assert.Equal("added-in-three", three.Hunks[0].Lines[1].Content)

	two := byPath["two.go"]
	assert.Empty(two.Hunks)
	assert.Zero(two.Additions)
	assert.Zero(two.Deletions)

	one := byPath["one.go"]
	require.Len(one.Hunks, 1)
	assert.Equal(1, one.Additions)
}

// TestParsePatchNonASCIIPath covers a C-quoted path (git quotes paths with
// non-ASCII bytes by default). The quoted "a/caf\303\251.txt" /
// "b/caf\303\251.txt" headers must resolve to "café.txt" so the section
// matches its raw file and carries its hunk.
func TestParsePatchNonASCIIPath(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	patch := "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n" +
		"index aaa..bbb 100644\n" +
		"--- \"a/caf\\303\\251.txt\"\n" +
		"+++ \"b/caf\\303\\251.txt\"\n" +
		"@@ -1,1 +1,2 @@\n" +
		" line1\n" +
		"+line2\n"

	// Lead with an extra raw file that has no patch section of its own (as
	// a whitespace-only file would), so a stale positional match would
	// misassign café.txt's hunk to it instead.
	rawFiles := []DiffFile{
		{Path: "aardvark.go", OldPath: "aardvark.go", Status: "modified"},
		{Path: "café.txt", OldPath: "café.txt", Status: "modified"},
	}

	files := ParsePatch([]byte(patch), rawFiles)
	require.Len(files, 2)

	byPath := make(map[string]DiffFile, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	cafe := byPath["café.txt"]
	require.Len(cafe.Hunks, 1)
	assert.Equal(1, cafe.Additions)

	aardvark := byPath["aardvark.go"]
	assert.Empty(aardvark.Hunks)
}

func TestParseNumstatZ(t *testing.T) {
	assert := assert.New(t)

	// Normal records, a binary record, and the rename form: an empty path
	// field followed by old\0new.
	raw := "1\t1\tsrc/main.go\x00" +
		"0\t3\tsrc/old.go\x00" +
		"-\t-\tsrc/image.png\x00" +
		"2\t0\t\x00src/before.go\x00src/after.go\x00"

	entries := ParseNumstatZ([]byte(raw))
	require.Len(t, entries, 4)

	assert.Equal(NumstatEntry{Additions: 1, Deletions: 1}, entries["src/main.go"])
	assert.Equal(NumstatEntry{Additions: 0, Deletions: 3}, entries["src/old.go"])
	assert.Equal(NumstatEntry{Binary: true}, entries["src/image.png"])

	// Keyed by new path for renames; old path does not appear as a key.
	assert.Equal(NumstatEntry{Additions: 2, Deletions: 0}, entries["src/after.go"])
	assert.NotContains(entries, "src/before.go")
}

func TestParseNumstatZBinaryRename(t *testing.T) {
	assert := assert.New(t)

	// A binary rename combines both forms: "-\t-\t" counts with the
	// empty-path rename layout.
	raw := "-\t-\t\x00old.dat\x00new.dat\x00"

	entries := ParseNumstatZ([]byte(raw))
	require.Len(t, entries, 1)
	assert.Equal(NumstatEntry{Binary: true}, entries["new.dat"])
}

func TestParsePatchNoNewline(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	patch := `diff --git a/file.txt b/file.txt
index abc..def 100644
--- a/file.txt
+++ b/file.txt
@@ -1,2 +1,2 @@
 line1
-line2
\ No newline at end of file
+line2-modified
\ No newline at end of file
`
	rawFiles := []DiffFile{
		{Path: "file.txt", OldPath: "file.txt", Status: "modified"},
	}
	files := ParsePatch([]byte(patch), rawFiles)
	require.Len(files, 1)
	require.Len(files[0].Hunks, 1)

	lines := files[0].Hunks[0].Lines
	require.Len(lines, 3) // context + delete + add

	assert.True(lines[1].NoNewline, "deleted line should have no_newline")
	assert.True(lines[2].NoNewline, "added line should have no_newline")
}
