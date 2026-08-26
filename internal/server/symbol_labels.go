package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wesm/middleman/internal/ctags"
	"github.com/wesm/middleman/internal/gitclone"
)

// ctagsKindBuckets maps every universal-ctags kind this feature claims
// to the coarse kind the API exposes. An unclaimed kind falls back to
// the textual heuristic rather than being forced into a bucket that
// might be wrong.
//
// ctags kind names are per-language, not universal, so the same concept
// is spelled differently across languages: "function" (C, C++,
// TypeScript) vs "func" (Go); "prototype" (C++) vs "methodSpec" (Go);
// "constant" (TypeScript) vs "const" (Go). Because of that, this map
// cannot be maintained by adding whatever kind a failing case happens to
// name — that is how it has already needed two follow-up widenings and
// still shipped with a gap in the most mainstream case (a plain C++
// variable definition, kind "variable", was missing). The authoritative
// source is `ctags --list-kinds-full=<LANG>`, run against the installed
// binary for every language this design targets — C, C++, Go,
// TypeScript and Python — read down the ENABLED column. Recompute it
// from that command before adding or removing an entry, not from memory
// of what "usually" shows up.
//
// This code never passes `--extras=+r`, the flag that makes ctags also
// emit reference-role tags for call sites, so every kind it emits here
// is already a declaration by construction: there is no per-kind
// judgment call to make about whether something "counts" as a
// definition. The map therefore claims every kind each of those
// languages emits enabled-by-default, plus "prototype" (which C and C++
// emit only because this code passes --kinds-C=+p --kinds-C++=+p; see
// ctags.baseArgs), except the five named below — each excluded for a
// stated reason, not by omission:
//
//   - "header" (C, C++): names a #include target. It is a file, not a
//     symbol anyone searches for as a definition.
//   - "package" (Go), "module" (Python): the unit a file belongs to,
//     not a declaration within it.
//   - "packageName" (Go): the local alias an import binds a package to
//     (the "f" in `import f "fmt"`). Every line it appears on is already
//     classified "import" by the heuristic that unclaimed kinds fall
//     back to; forcing it into "definition" would only make that worse,
//     for a kind that names an imported dependency rather than anything
//     the file itself declares.
//   - "unknown" (Go, Python): ctags' own could-not-classify bucket.
//     Claiming it would assert a definition the parser explicitly
//     declined to make.
var ctagsKindBuckets = map[string]string{
	"function":   gitclone.KindDefinition,
	"func":       gitclone.KindDefinition,
	"method":     gitclone.KindDefinition,
	"generator":  gitclone.KindDefinition,
	"class":      gitclone.KindDefinition,
	"struct":     gitclone.KindDefinition,
	"member":     gitclone.KindDefinition,
	"anonMember": gitclone.KindDefinition,
	"macro":      gitclone.KindDefinition,
	"typedef":    gitclone.KindDefinition,
	"talias":     gitclone.KindDefinition,
	"type":       gitclone.KindDefinition,
	"alias":      gitclone.KindDefinition,
	"enum":       gitclone.KindDefinition,
	"union":      gitclone.KindDefinition,
	"namespace":  gitclone.KindDefinition,
	"prototype":  gitclone.KindDefinition,
	"interface":  gitclone.KindDefinition,
	"methodSpec": gitclone.KindDefinition,
	"property":   gitclone.KindDefinition,
	"const":      gitclone.KindDefinition,
	"constant":   gitclone.KindDefinition,
	"var":        gitclone.KindDefinition,
	"variable":   gitclone.KindDefinition,
	"enumerator": gitclone.KindDefinition,
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

// --- ctags orchestration ---
//
// The functions below drive ctags over a search's hits and hand the
// result to labelHits above. They live here rather than in the routes
// file because they are this feature's pure labelling pipeline, not
// request/response plumbing: symbolRefsResponse, the input/output
// types and the HTTP handlers that call into this file stay in
// huma_routes.go and local_dispatch.go.

// symbolRefTagsMaxFiles caps how many distinct files are sent through
// ctags in a single search. Ordinarily a handful, but a large PR
// touching a common symbol can span hundreds of changed files, each
// costing a git-cat-file fork plus a ctags fork inside the shared
// symbolRefsTimeout deadline — turning a fast search into a
// multi-second one. Past the cap, the remaining files' hits fall back
// to the heuristic, the same as any other file ctags could not tag.
// Named in the same style as symbolRefsMaxHits.
const symbolRefTagsMaxFiles = 200

// symbolRefTags runs ctags over the distinct files among hits that are
// in the PR's changed set, and returns their tags keyed by repo-relative
// path. Paths outside changed are skipped before ever reading a blob:
// buildSymbolRefsResponse only calls labelHits on the changed subset of
// hits, so tagging a file elsewhere in the tree would cost a Blob fetch
// plus a ctags subprocess spawn for a result nothing uses — on a large
// repo a common symbol can hit hundreds of tree-wide files while the PR
// touches a handful, and that waste can plausibly exhaust the request's
// timeout. readBlob supplies each file's content at the searched SHA,
// which differs by mode.
//
// A file is also skipped — absent from the result, so its hits fall
// back to the heuristic — when its blob cannot be read, it is too
// large, or ctags fails on it. One awkward file must never degrade the
// whole search. Both cases are logged at Debug, not Error: a symbol
// search touching a file ctags cannot handle is an expected, isolated,
// non-fatal event, not a failure worth paging on — but a ctags that
// fails on every file should still be visible in the logs rather than
// silently indistinguishable from "found nothing."
//
// The context is checked on every iteration since this loop can run
// for many files inside the caller's shared deadline, and
// symbolRefTagsMaxFiles bounds the file count outright; the cap is
// logged when hit so a truncated result reads as a deliberate limit,
// not as "we tagged everything."
func symbolRefTags(
	ctx context.Context,
	hits []gitclone.SymbolHit,
	changed map[string]bool,
	readBlob func(path string) ([]byte, error),
) map[string][]ctags.Tag {
	if !ctags.Available() {
		return nil
	}
	out := make(map[string][]ctags.Tag)
	attempted := 0
	for _, path := range distinctPaths(hits) {
		if ctx.Err() != nil {
			break
		}
		if !changed[path] {
			continue
		}
		if attempted >= symbolRefTagsMaxFiles {
			slog.Debug("symbol refs: ctags file cap reached",
				"limit", symbolRefTagsMaxFiles)
			break
		}
		attempted++
		content, err := readBlob(path)
		if err != nil || len(content) == 0 || len(content) > blobMaxBytes {
			slog.Debug("symbol refs: skipping file for ctags",
				"path", path, "err", err, "size", len(content))
			continue
		}
		tags, err := tagsForContent(ctx, path, content)
		if err != nil {
			slog.Debug("symbol refs: ctags failed on file",
				"path", path, "err", err)
			continue
		}
		out[path] = tags
	}
	return out
}

// distinctPaths lists each path in hits once, preserving first-seen order
// so behaviour is deterministic.
func distinctPaths(hits []gitclone.SymbolHit) []string {
	seen := make(map[string]bool, len(hits))
	var paths []string
	for _, h := range hits {
		if !seen[h.Path] {
			seen[h.Path] = true
			paths = append(paths, h.Path)
		}
	}
	return paths
}

// tagsForContent writes content to a temp file that keeps path's
// extension and runs ctags on it. The extension is what ctags uses to
// pick a language, and it cannot read source from stdin.
func tagsForContent(
	ctx context.Context, path string, content []byte,
) ([]ctags.Tag, error) {
	f, err := os.CreateTemp("", "symbolrefs-*"+filepath.Ext(path))
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return ctags.TagsForFile(ctx, f.Name())
}

// symbolRefsClassifier names the labeller used, for the response.
func symbolRefsClassifier() string {
	if ctags.Available() {
		return "ctags"
	}
	return "heuristic"
}
