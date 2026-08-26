package ctags

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findTag returns the tag matching kind and name, so tests can assert on a
// specific tag's fields without depending on the order ctags emits records
// in.
func findTag(tags []Tag, kind, name string) (Tag, bool) {
	for _, tag := range tags {
		if tag.Kind == kind && tag.Name == name {
			return tag, true
		}
	}
	return Tag{}, false
}

// TestTagsForFile is an integration test: it runs the real ctags binary.
// Every subtest skips cleanly when it is absent, because CI may not have
// it installed.
func TestTagsForFile(t *testing.T) {
	t.Run("C++ header yields a class tag and a prototype carrying scope and signature", func(t *testing.T) {
		if !Available() {
			t.Skip("universal-ctags not installed")
		}
		require := require.New(t)
		assert := assert.New(t)

		path := filepath.Join(t.TempDir(), "widget.h")
		const src = `namespace ns {

class Widget {
public:
    int compute(int x);
};

}  // namespace ns
`
		require.NoError(os.WriteFile(path, []byte(src), 0o644))

		tags, err := TagsForFile(context.Background(), path)
		require.NoError(err)

		class, ok := findTag(tags, "class", "Widget")
		require.True(ok, "expected a class tag for Widget")
		assert.Equal(Tag{Name: "Widget", Line: 3, Kind: "class", Scope: "ns"}, class)

		// Regression guard for --kinds-C++=+p: the prototype kind ships
		// disabled in universal-ctags, so without that flag this member
		// declaration produces no tag at all rather than a differently
		// labelled one, and this assertion would fail to find it.
		proto, ok := findTag(tags, "prototype", "compute")
		require.True(ok, "expected a prototype tag for compute (regression guard for --kinds-C++=+p)")
		assert.Equal(Tag{Name: "compute", Line: 5, Kind: "prototype", Scope: "ns::Widget", Signature: "(int x)"}, proto)
	})

	t.Run("go file yields a func tag, proving language detection follows the extension", func(t *testing.T) {
		if !Available() {
			t.Skip("universal-ctags not installed")
		}
		require := require.New(t)
		assert := assert.New(t)

		path := filepath.Join(t.TempDir(), "hello.go")
		const src = `package sample

func Hello() string {
	return "hi"
}
`
		require.NoError(os.WriteFile(path, []byte(src), 0o644))

		tags, err := TagsForFile(context.Background(), path)
		require.NoError(err)

		fn, ok := findTag(tags, "func", "Hello")
		require.True(ok, "expected a func tag for Hello")
		assert.Equal(Tag{Name: "Hello", Line: 3, Kind: "func", Scope: "sample", Signature: "()"}, fn)
	})

	t.Run("unrecognized extension yields no tags and no error", func(t *testing.T) {
		if !Available() {
			t.Skip("universal-ctags not installed")
		}
		path := filepath.Join(t.TempDir(), "notes.zzzqx")
		require.NoError(t, os.WriteFile(path, []byte("this is not a recognized language\n"), 0o644))

		tags, err := TagsForFile(context.Background(), path)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("nonexistent path returns an error", func(t *testing.T) {
		if !Available() {
			t.Skip("universal-ctags not installed")
		}
		path := filepath.Join(t.TempDir(), "does-not-exist.h")

		tags, err := TagsForFile(context.Background(), path)
		require.Error(t, err)
		require.ErrorContains(t, err, path)
		assert.Nil(t, tags)
	})

	t.Run("Available agrees with whether a Universal Ctags binary is actually on PATH", func(t *testing.T) {
		if !Available() {
			t.Skip("universal-ctags not installed")
		}
		found := false
		for _, name := range []string{"ctags", "ctags-universal"} {
			resolved, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			out, err := exec.Command(resolved, "--version").Output()
			if err != nil {
				continue
			}
			if strings.Contains(string(out), "Universal Ctags") {
				found = true
				break
			}
		}
		assert.True(t, found, "Available() reported true but no Universal Ctags binary was independently found on PATH")
	})
}
