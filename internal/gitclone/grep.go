package gitclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// SymbolHit is one word-boundary match for a symbol, addressed by
// repo-relative path and 1-based line number in the searched tree.
type SymbolHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// Hit kinds assigned by Classify.
const (
	KindDefinition = "definition"
	KindReference  = "reference"
	KindImport     = "import"
	KindComment    = "comment"
	KindString     = "string"
)

// ParseGrepZ parses `git grep -n -z` output into hits. Records are
// newline-terminated; within a record the path and the line number are
// NUL-terminated, so a path or a line of code containing a colon still
// parses. When the grep names a revision, git prefixes every path with
// "<rev>:"; revPrefix strips that. Pass "" for a working-tree grep.
// Malformed records are skipped rather than failing the whole batch —
// one odd line should not cost the user every other hit.
func ParseGrepZ(data []byte, revPrefix string) []SymbolHit {
	var hits []SymbolHit
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		parts := bytes.SplitN(raw, []byte{0}, 3)
		if len(parts) != 3 {
			continue
		}
		line, err := strconv.Atoi(string(parts[1]))
		if err != nil || line < 1 {
			continue
		}
		path := string(parts[0])
		if revPrefix != "" {
			path = strings.TrimPrefix(path, revPrefix)
		}
		hits = append(hits, SymbolHit{
			Path: path,
			Line: line,
			Text: string(parts[2]),
		})
	}
	return hits
}

// definitionKeywords are the tokens that, when they appear before the
// symbol on the same line, suggest the line declares it rather than
// using it.
var definitionKeywords = map[string]bool{
	"class": true, "struct": true, "enum": true, "union": true,
	"interface": true, "trait": true, "type": true, "func": true,
	"fn": true, "def": true, "const": true, "var": true, "let": true,
	"using": true, "typedef": true, "namespace": true,
}

// preprocessorDirectives are the C/C++ directives we recognize so a
// leading '#' is not mistaken for a comment starter.
var preprocessorDirectives = map[string]bool{
	"include": true, "define": true, "undef": true, "ifdef": true,
	"ifndef": true, "if": true, "else": true, "elif": true,
	"endif": true, "pragma": true, "error": true, "warning": true,
	"line": true,
}

// Classify labels a hit from its own line of text. It is a heuristic
// over characters, not a parser: it carries no state between lines, so
// a symbol inside a block comment that opened on an earlier line reads
// as a plain reference, and it cannot tell two same-named methods on
// different types apart. Callers use it to order and group results,
// never to decide whether a hit is real.
func Classify(symbol, text string) string {
	before, _, ok := strings.Cut(text, symbol)
	if !ok {
		return KindReference
	}
	trimmed := strings.TrimSpace(text)

	if isImportLine(trimmed) {
		return KindImport
	}
	// Checked before the comment scan so '#define FOO' reads as a
	// definition and '#ifdef FOO' as a reference, rather than both
	// looking like '#' comments.
	if directive, ok := preprocessorDirective(trimmed); ok {
		if directive == "define" {
			return KindDefinition
		}
		return KindReference
	}
	commented, quoted := scanBefore(before)
	switch {
	case commented:
		return KindComment
	case quoted:
		return KindString
	case hasDefinitionKeyword(before):
		return KindDefinition
	}
	return KindReference
}

// isImportLine matches the common include/import forms. Anchored at the
// start of the line so a commented-out include is classified as a
// comment instead.
func isImportLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "#include"):
		return true
	case strings.HasPrefix(trimmed, "import "), strings.HasPrefix(trimmed, "use "):
		return true
	case strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import "):
		return true
	case strings.Contains(trimmed, "require("):
		return true
	}
	return false
}

// preprocessorDirective reports the directive word of a C/C++
// preprocessor line, e.g. "define" for "#define FOO 1".
func preprocessorDirective(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	word := strings.TrimLeft(trimmed[1:], " \t")
	end := strings.IndexFunc(word, func(r rune) bool {
		return r < 'a' || r > 'z'
	})
	if end >= 0 {
		word = word[:end]
	}
	if word == "" || !preprocessorDirectives[word] {
		return "", false
	}
	return word, true
}

// scanBefore walks the text preceding the symbol and reports whether
// the symbol sits inside a comment or inside a string literal. One pass
// tracks quote state so a comment marker inside a string (and an
// apostrophe inside a comment) does not fool either answer.
func scanBefore(before string) (commented, quoted bool) {
	var inSingle, inDouble, inBack bool
	for i := 0; i < len(before); i++ {
		c := before[i]
		if (inSingle || inDouble || inBack) && c == '\\' {
			i++
			continue
		}
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			}
		case inBack:
			if c == '`' {
				inBack = false
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '`':
			inBack = true
		case c == '/' && i+1 < len(before) && before[i+1] == '/':
			return true, false
		case c == '#':
			return true, false
		case c == '-' && i+1 < len(before) && before[i+1] == '-' &&
			(i+2 >= len(before) || before[i+2] == ' '):
			return true, false
		case c == '/' && i+1 < len(before) && before[i+1] == '*':
			// A block comment that closes before the symbol is not
			// comment context; one that stays open is.
			if close := strings.Index(before[i+2:], "*/"); close >= 0 {
				i += 2 + close + 1
				continue
			}
			return true, false
		}
	}
	return false, inSingle || inDouble || inBack
}

// hasDefinitionKeyword reports whether a declaration keyword appears
// among the identifier-ish tokens preceding the symbol.
func hasDefinitionKeyword(before string) bool {
	for _, tok := range strings.FieldsFunc(before, func(r rune) bool {
		return r != '_' &&
			(r < 'a' || r > 'z') &&
			(r < 'A' || r > 'Z') &&
			(r < '0' || r > '9')
	}) {
		if definitionKeywords[tok] {
			return true
		}
	}
	return false
}

// GrepSymbol returns every word-boundary occurrence of symbol in the
// tree at sha. The match is a fixed string (never a regex) and
// case-sensitive; binary files are skipped. Hits come back
// unclassified — callers classify only the ones they keep.
//
// A revision is required because the clone is bare and has no working
// tree. `git grep` exits 1 when nothing matched, which is a normal
// empty result rather than a failure.
func (m *Manager) GrepSymbol(
	ctx context.Context, host, owner, name, sha, symbol string,
) ([]SymbolHit, error) {
	clonePath := m.ClonePath(host, owner, name)
	out, err := m.git(ctx, host, clonePath,
		"grep", "-n", "-z", "-w", "-F", "-I", "--no-color", "-e", symbol, sha)
	if err != nil {
		if isNoMatch(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("git grep %q at %s: %w", symbol, sha, err)
	}
	return ParseGrepZ(out, sha+":"), nil
}

// isNoMatch reports whether a git-grep failure is just "nothing
// matched" (exit status 1) rather than a real error.
func isNoMatch(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
}
