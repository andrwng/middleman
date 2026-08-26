package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wesm/middleman/internal/ctags"
	"github.com/wesm/middleman/internal/gitclone"
)

func TestSymbolKindForCtagsKind(t *testing.T) {
	assert := assert.New(t)
	for _, k := range []string{
		"function", "func", "method", "generator", "class", "struct",
		"member", "anonMember", "macro", "typedef", "talias", "type",
		"alias", "enum", "union", "namespace", "prototype", "interface",
		"methodSpec", "property", "const", "constant", "var", "variable",
		"enumerator",
	} {
		assert.Equal(gitclone.KindDefinition, symbolKindForCtagsKind(k), k)
	}
	// Kinds this feature does not claim fall back to the heuristic.
	// Each deliberate exclusion is pinned on its own rather than lumped
	// in with the generic placeholders below, so a future widening pass
	// cannot accidentally erase one:
	//   - "package" (Go) and "module" (Python) are declarations like
	//     everything else in the positive set above, but the unit a
	//     file belongs to is not a symbol anyone searches for as a
	//     definition.
	//   - "packageName" (Go) is the local alias an import binds a
	//     package to; every line it appears on is already classified
	//     "import" by the heuristic, so claiming it here would only
	//     make that worse.
	//   - "header" (C, C++) names a #include target -- a file, not a
	//     symbol.
	//   - "unknown" (Go, Python) is ctags' own could-not-classify
	//     bucket; claiming it would assert a definition the parser
	//     explicitly declined to make.
	assert.Empty(symbolKindForCtagsKind("package"))
	assert.Empty(symbolKindForCtagsKind("module"))
	assert.Empty(symbolKindForCtagsKind("packageName"))
	assert.Empty(symbolKindForCtagsKind("header"))
	assert.Empty(symbolKindForCtagsKind("unknown"))
	assert.Empty(symbolKindForCtagsKind("local"))
	assert.Empty(symbolKindForCtagsKind("parameter"))
	assert.Empty(symbolKindForCtagsKind(""))
	assert.Empty(symbolKindForCtagsKind("nonsense"))
}

func TestLabelHits(t *testing.T) {
	tagsFor := func(tags ...ctags.Tag) map[string][]ctags.Tag {
		return map[string][]ctags.Tag{"a.cc": tags}
	}

	t.Run("a matching tag on the line supplies kind and detail", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{{Path: "a.cc", Line: 12, Text: "void handle(request r) {"}}
		got := labelHits("handle", hits, tagsFor(ctags.Tag{
			Name: "handle", Line: 12, Kind: "function",
			Scope: "kafka::server", Signature: "(request r)",
		}))
		assert.Equal(gitclone.KindDefinition, got[0].Kind)
		assert.NotNil(got[0].Tag)
		assert.Equal("function", got[0].Tag.Kind)
		assert.Equal("kafka::server", got[0].Tag.Scope)
		assert.Equal("(request r)", got[0].Tag.Signature)
	})

	t.Run("a prototype is a definition, and keeps its precise kind", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{{Path: "a.cc", Line: 3, Text: "  int handle(int);"}}
		got := labelHits("handle", hits, tagsFor(ctags.Tag{
			Name: "handle", Line: 3, Kind: "prototype", Scope: "ns::c", Signature: "(int)",
		}))
		assert.Equal(gitclone.KindDefinition, got[0].Kind)
		assert.Equal("prototype", got[0].Tag.Kind)
	})

	t.Run("a tag for a DIFFERENT symbol on the line does not label the hit", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{{Path: "a.cc", Line: 30, Text: "int Get(key string)"}}
		got := labelHits("string", hits, tagsFor(ctags.Tag{
			Name: "Get", Line: 30, Kind: "function",
		}))
		assert.Nil(got[0].Tag, "the tag names Get, not string")
		assert.Equal(gitclone.KindReference, got[0].Kind)
	})

	t.Run("a tag on a different line does not label the hit", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{{Path: "a.cc", Line: 40, Text: "  handle(r);"}}
		got := labelHits("handle", hits, tagsFor(ctags.Tag{
			Name: "handle", Line: 12, Kind: "function",
		}))
		assert.Nil(got[0].Tag)
		assert.Equal(gitclone.KindReference, got[0].Kind)
	})

	t.Run("a file with no tag information falls back to the heuristic", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{
			{Path: "b.cc", Line: 1, Text: "// handle is retried"},
			{Path: "b.cc", Line: 2, Text: `log("handle failed")`},
			{Path: "b.cc", Line: 3, Text: "class handle final {"},
		}
		got := labelHits("handle", hits, map[string][]ctags.Tag{})
		assert.Equal(gitclone.KindComment, got[0].Kind)
		assert.Equal(gitclone.KindString, got[1].Kind)
		assert.Equal(gitclone.KindDefinition, got[2].Kind)
		for _, h := range got {
			assert.Nil(h.Tag)
		}
	})

	t.Run("an unclaimed ctags kind falls back to the heuristic", func(t *testing.T) {
		assert := assert.New(t)
		hits := []gitclone.SymbolHit{{Path: "a.cc", Line: 5, Text: "  int handle = 0;"}}
		got := labelHits("handle", hits, tagsFor(ctags.Tag{
			Name: "handle", Line: 5, Kind: "local",
		}))
		assert.Nil(got[0].Tag)
		assert.Equal(gitclone.KindReference, got[0].Kind)
	})

	t.Run("empty hits returns empty, not nil-panics", func(t *testing.T) {
		assert.Empty(t, labelHits("x", nil, nil))
	})
}
