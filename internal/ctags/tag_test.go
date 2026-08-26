package ctags

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseJSONLines(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []Tag
	}{
		{
			name: "real C++ header output, mixed kinds",
			data: `{"_type": "tag", "name": "api", "path": "handler.h", "pattern": "/^    using api = RequestApi;$/", "line": 58, "typeref": "typename:RequestApi", "kind": "typedef", "scope": "kafka::handler_template", "scopeKind": "struct"}
{"_type": "tag", "name": "default_estimate_adaptor", "path": "handler.h", "pattern": "/^x$/", "line": 27, "typeref": "typename:size_t", "kind": "function", "signature": "(size_t request_size,connection_context &)", "scope": "kafka", "scopeKind": "namespace", "end": 29}
`,
			want: []Tag{
				{Name: "api", Line: 58, Kind: "typedef", Scope: "kafka::handler_template"},
				{Name: "default_estimate_adaptor", Line: 27, Kind: "function",
					Scope: "kafka", Signature: "(size_t request_size,connection_context &)"},
			},
		},
		{
			name: "prototype carries scope and signature",
			data: `{"_type": "tag", "name": "m", "path": "X.h", "pattern": "/^  int m(int x);$/", "line": 3, "typeref": "typename:int", "kind": "prototype", "signature": "(int x)", "scope": "ns::c", "scopeKind": "class", "end": 3}
`,
			want: []Tag{{Name: "m", Line: 3, Kind: "prototype", Scope: "ns::c", Signature: "(int x)"}},
		},
		{
			name: "tag with no scope",
			data: `{"_type": "tag", "name": "ns", "path": "X.h", "pattern": "/^namespace ns {$/", "line": 1, "kind": "namespace", "end": 5}
`,
			want: []Tag{{Name: "ns", Line: 1, Kind: "namespace"}},
		},
		{
			name: "malformed lines are skipped, valid ones survive",
			data: `not json at all
{"_type": "tag", "name": "ok", "line": 9, "kind": "function"}
{"name": "no line number", "kind": "function"}
`,
			want: []Tag{{Name: "ok", Line: 9, Kind: "function"}},
		},
		{name: "empty input", data: "", want: nil},
		{name: "blank lines only", data: "\n\n", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ParseJSONLines([]byte(tc.data)))
		})
	}
}
