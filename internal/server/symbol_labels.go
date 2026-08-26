package server

import (
	"github.com/wesm/middleman/internal/ctags"
	"github.com/wesm/middleman/internal/gitclone"
)

// ctagsKindBuckets maps the universal-ctags kinds this feature claims to
// the coarse kind the API exposes. Everything else is deliberately
// absent: an unclaimed kind falls back to the textual heuristic rather
// than being forced into a bucket that might be wrong.
//
// prototype is a definition on purpose. A header declaration belongs
// beside the definition when scanning; the problem being solved is that
// declarations were indistinguishable FROM definitions, not that they
// should be hidden.
var ctagsKindBuckets = map[string]string{
	"function":  gitclone.KindDefinition,
	"class":     gitclone.KindDefinition,
	"struct":    gitclone.KindDefinition,
	"member":    gitclone.KindDefinition,
	"macro":     gitclone.KindDefinition,
	"typedef":   gitclone.KindDefinition,
	"enum":      gitclone.KindDefinition,
	"namespace": gitclone.KindDefinition,
	"prototype": gitclone.KindDefinition,
}

// symbolKindForCtagsKind returns the coarse kind for a ctags kind, or ""
// when this feature makes no claim about it.
func symbolKindForCtagsKind(ctagsKind string) string {
	return ctagsKindBuckets[ctagsKind]
}

// labelHits assigns every hit a coarse kind, and a precise tag where one
// applies. tagsByPath holds the tags found in each file; a path missing
// from it has no tag information — ctags was unavailable, the file failed
// to parse, or its language is unknown — and its hits are labelled by the
// textual heuristic instead.
//
// A tag labels a hit only when it sits on the hit's line AND names the
// searched symbol. Without the name check, searching for "string" would
// pick up the tag for the function it is a parameter of.
func labelHits(
	symbol string,
	hits []gitclone.SymbolHit,
	tagsByPath map[string][]ctags.Tag,
) []gitclone.SymbolHit {
	out := make([]gitclone.SymbolHit, 0, len(hits))
	for _, h := range hits {
		if t, ok := findTag(symbol, h.Path, h.Line, tagsByPath); ok {
			if bucket := symbolKindForCtagsKind(t.Kind); bucket != "" {
				h.Kind = bucket
				h.Tag = &gitclone.SymbolTag{
					Kind:      t.Kind,
					Scope:     t.Scope,
					Signature: t.Signature,
				}
				out = append(out, h)
				continue
			}
		}
		h.Kind = gitclone.Classify(symbol, h.Text)
		h.Tag = nil
		out = append(out, h)
	}
	return out
}

// findTag returns the tag naming symbol on the given line, if any.
func findTag(
	symbol, path string, line int, tagsByPath map[string][]ctags.Tag,
) (ctags.Tag, bool) {
	for _, t := range tagsByPath[path] {
		if t.Line == line && t.Name == symbol {
			return t, true
		}
	}
	return ctags.Tag{}, false
}
