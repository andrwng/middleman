package gitclone

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGrepZ(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		revPrefix string
		want      []SymbolHit
	}{
		{
			name:      "rev-prefixed records",
			data:      "HEAD:a/b.go\x0012\x00func Foo() {\nHEAD:a/b.go\x0030\x00\tFoo()\n",
			revPrefix: "HEAD:",
			want: []SymbolHit{
				{Path: "a/b.go", Line: 12, Text: "func Foo() {"},
				{Path: "a/b.go", Line: 30, Text: "\tFoo()"},
			},
		},
		{
			name:      "working-tree records have no prefix",
			data:      "a/b.go\x007\x00Foo()\n",
			revPrefix: "",
			want:      []SymbolHit{{Path: "a/b.go", Line: 7, Text: "Foo()"}},
		},
		{
			name:      "path containing a colon survives",
			data:      "HEAD:a/we:ird.go\x001\x00Foo()\n",
			revPrefix: "HEAD:",
			want:      []SymbolHit{{Path: "a/we:ird.go", Line: 1, Text: "Foo()"}},
		},
		{
			name:      "content containing a colon survives",
			data:      "HEAD:a.go\x005\x00m := map[string]int{\"k:v\": 1}\n",
			revPrefix: "HEAD:",
			want:      []SymbolHit{{Path: "a.go", Line: 5, Text: "m := map[string]int{\"k:v\": 1}"}},
		},
		{name: "empty input", data: "", revPrefix: "HEAD:", want: nil},
		{
			name:      "malformed records are skipped",
			data:      "garbage-no-nuls\nHEAD:a.go\x00notanumber\x00x\nHEAD:a.go\x009\x00ok\n",
			revPrefix: "HEAD:",
			want:      []SymbolHit{{Path: "a.go", Line: 9, Text: "ok"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ParseGrepZ([]byte(tc.data), tc.revPrefix))
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name, symbol, text, want string
	}{
		{"cpp include", "hb", `#include "hb.h"`, KindImport},
		{"go import", "fmt", `import "fmt"`, KindImport},
		{"python from-import", "Foo", "from mod import Foo", KindImport},
		{"js require", "Foo", `const Foo = require("foo")`, KindImport},
		{"line comment", "Foo", "// Foo is retried on failure", KindComment},
		{"trailing line comment", "Foo", "bar() // Foo happens here", KindComment},
		{"hash comment", "Foo", "# Foo in a shell script", KindComment},
		{"sql dash comment", "Foo", "-- Foo in a migration", KindComment},
		{"single-line block comment", "Foo", "/* Foo */", KindComment},
		{"code after a closed block comment", "Foo", "/* note */ Foo();", KindReference},
		{"string literal", "Foo", `log.Info("Foo failed")`, KindString},
		{"apostrophe in a comment is not a string", "Foo", "// don't call Foo", KindComment},
		{"cpp class", "Foo", "class Foo final {", KindDefinition},
		{"cpp struct", "Foo", "struct Foo {", KindDefinition},
		{"go func", "Foo", "func Foo() error {", KindDefinition},
		{"go type", "Foo", "type Foo struct {", KindDefinition},
		{"python def", "Foo", "    def Foo(self):", KindDefinition},
		{"ts const", "Foo", "const Foo = 1;", KindDefinition},
		{"namespace", "Foo", "namespace Foo {", KindDefinition},
		{"preprocessor define", "FOO", "#define FOO 1", KindDefinition},
		{"other preprocessor is not a comment", "FOO", "#ifdef FOO", KindReference},
		{"plain call", "Foo", "\tauto r = Foo(g);", KindReference},
		{"member access", "Foo", "\tx.Foo = 1;", KindReference},
		{"symbol absent from the line", "Foo", "unrelated", KindReference},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Classify(tc.symbol, tc.text))
		})
	}
}
