// Package ctags labels source positions using universal-ctags. It runs
// the binary one shot per file, the way this codebase already runs git,
// and parses its JSON output. It knows nothing about git or HTTP.
package ctags

import (
	"bytes"
	"encoding/json"
)

// Tag is one declaration universal-ctags found, reduced to the fields
// this feature displays. ctags emits more (pattern, typeref, scopeKind,
// end); they are deliberately ignored so a ctags version that changes
// or drops them cannot break parsing.
type Tag struct {
	Name      string
	Line      int
	Kind      string
	Scope     string
	Signature string
}

// jsonTag mirrors the subset of ctags' JSON object we read.
type jsonTag struct {
	Name      string `json:"name"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Signature string `json:"signature"`
}

// ParseJSONLines parses `ctags --output-format=json` output, one object
// per line. A line that is not valid JSON, or that lacks a name or a
// usable line number, is skipped rather than failing the batch: one odd
// record should not cost the caller every other tag in the file.
func ParseJSONLines(data []byte) []Tag {
	var tags []Tag
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var jt jsonTag
		if err := json.Unmarshal(raw, &jt); err != nil {
			continue
		}
		if jt.Name == "" || jt.Line < 1 {
			continue
		}
		tags = append(tags, Tag(jt))
	}
	return tags
}
