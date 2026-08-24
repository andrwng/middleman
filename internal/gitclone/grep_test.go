package gitclone_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitclone"
	"github.com/wesm/middleman/internal/testutil"
)

func TestParseGrepZ(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		revPrefix string
		want      []gitclone.SymbolHit
	}{
		{
			name:      "rev-prefixed records",
			data:      "HEAD:a/b.go\x0012\x00func Foo() {\nHEAD:a/b.go\x0030\x00\tFoo()\n",
			revPrefix: "HEAD:",
			want: []gitclone.SymbolHit{
				{Path: "a/b.go", Line: 12, Text: "func Foo() {"},
				{Path: "a/b.go", Line: 30, Text: "\tFoo()"},
			},
		},
		{
			name:      "working-tree records have no prefix",
			data:      "a/b.go\x007\x00Foo()\n",
			revPrefix: "",
			want:      []gitclone.SymbolHit{{Path: "a/b.go", Line: 7, Text: "Foo()"}},
		},
		{
			name:      "path containing a colon survives",
			data:      "HEAD:a/we:ird.go\x001\x00Foo()\n",
			revPrefix: "HEAD:",
			want:      []gitclone.SymbolHit{{Path: "a/we:ird.go", Line: 1, Text: "Foo()"}},
		},
		{
			name:      "content containing a colon survives",
			data:      "HEAD:a.go\x005\x00m := map[string]int{\"k:v\": 1}\n",
			revPrefix: "HEAD:",
			want:      []gitclone.SymbolHit{{Path: "a.go", Line: 5, Text: "m := map[string]int{\"k:v\": 1}"}},
		},
		{name: "empty input", data: "", revPrefix: "HEAD:", want: nil},
		{
			name:      "malformed records are skipped",
			data:      "garbage-no-nuls\nHEAD:a.go\x00notanumber\x00x\nHEAD:a.go\x009\x00ok\n",
			revPrefix: "HEAD:",
			want:      []gitclone.SymbolHit{{Path: "a.go", Line: 9, Text: "ok"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, gitclone.ParseGrepZ([]byte(tc.data), tc.revPrefix))
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name, symbol, text, want string
	}{
		{"cpp include", "hb", `#include "hb.h"`, gitclone.KindImport},
		{"go import", "fmt", `import "fmt"`, gitclone.KindImport},
		{"python from-import", "Foo", "from mod import Foo", gitclone.KindImport},
		{"js require", "Foo", `const Foo = require("foo")`, gitclone.KindImport},
		{"line comment", "Foo", "// Foo is retried on failure", gitclone.KindComment},
		{"trailing line comment", "Foo", "bar() // Foo happens here", gitclone.KindComment},
		{"hash comment", "Foo", "# Foo in a shell script", gitclone.KindComment},
		{"sql dash comment", "Foo", "-- Foo in a migration", gitclone.KindComment},
		{"single-line block comment", "Foo", "/* Foo */", gitclone.KindComment},
		{"code after a closed block comment", "Foo", "/* note */ Foo();", gitclone.KindReference},
		{"string literal", "Foo", `log.Info("Foo failed")`, gitclone.KindString},
		{"apostrophe in a comment is not a string", "Foo", "// don't call Foo", gitclone.KindComment},
		{"cpp class", "Foo", "class Foo final {", gitclone.KindDefinition},
		{"cpp struct", "Foo", "struct Foo {", gitclone.KindDefinition},
		{"go func", "Foo", "func Foo() error {", gitclone.KindDefinition},
		{"go type", "Foo", "type Foo struct {", gitclone.KindDefinition},
		{"python def", "Foo", "    def Foo(self):", gitclone.KindDefinition},
		{"ts const", "Foo", "const Foo = 1;", gitclone.KindDefinition},
		{"namespace", "Foo", "namespace Foo {", gitclone.KindDefinition},
		{"preprocessor define", "FOO", "#define FOO 1", gitclone.KindDefinition},
		{"other preprocessor is not a comment", "FOO", "#ifdef FOO", gitclone.KindReference},
		{"plain call", "Foo", "\tauto r = Foo(g);", gitclone.KindReference},
		{"member access", "Foo", "\tx.Foo = 1;", gitclone.KindReference},
		{"symbol absent from the line", "Foo", "unrelated", gitclone.KindReference},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, gitclone.Classify(tc.symbol, tc.text))
		})
	}
}

// TestManagerGrepSymbol exercises GrepSymbol against a real bare clone
// built by testutil.SetupDiffRepo. It lives in package gitclone_test
// (rather than being appended to the internal gitclone package tests)
// because testutil imports gitclone: an internal-package test file that
// imports testutil would create an import cycle.
func TestManagerGrepSymbol(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	database, err := db.Open(filepath.Join(tmp, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	repo, err := testutil.SetupDiffRepo(ctx, tmp, database)
	require.NoError(t, err)

	m := repo.Manager
	const host, owner, name = "github.com", "acme", "widgets"

	tests := []struct {
		name   string
		symbol string
		want   []gitclone.SymbolHit
	}{
		{
			name:   "symbol present in the tree",
			symbol: "NewCache",
			want: []gitclone.SymbolHit{
				{Path: "internal/cache.go", Line: 20, Text: "// NewCache creates a cache with the given TTL."},
				{Path: "internal/cache.go", Line: 21, Text: "func NewCache(ttl time.Duration) *Cache {"},
			},
		},
		{
			name:   "symbol absent from the tree",
			symbol: "ThisSymbolDoesNotExistAnywhereXYZ",
			want:   nil,
		},
		{
			// "Event" appears only embedded in "ProcessEvent" (in both
			// the doc comment and the func signature); -w must not
			// treat that substring as a match.
			name:   "word-boundary non-match",
			symbol: "Event",
			want:   nil,
		},
		{
			// "[" and "]" are regex metacharacters; -F must still match
			// the literal text rather than treating them as a character
			// class.
			name:   "regex metacharacters are treated literally",
			symbol: "map[string]entry",
			want: []gitclone.SymbolHit{
				{Path: "internal/cache.go", Line: 11, Text: "\tentries map[string]entry"},
				{Path: "internal/cache.go", Line: 23, Text: "\t\tentries: make(map[string]entry),"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := m.GrepSymbol(ctx, host, owner, name, repo.HeadSHA, tc.symbol)
			require.NoError(t, err)
			assert.Equal(t, tc.want, hits)
		})
	}

	t.Run("unknown revision returns ErrNotFound", func(t *testing.T) {
		hits, err := m.GrepSymbol(ctx, host, owner, name, "nonexistent-branch-xyz", "NewCache")
		assert.Nil(t, hits)
		assert.ErrorIs(t, err, gitclone.ErrNotFound)
	})
}
