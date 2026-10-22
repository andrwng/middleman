package server

import (
	"testing"

	Assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wesm/middleman/internal/gitclone"
)

func anchorFile() *gitclone.DiffFile {
	return &gitclone.DiffFile{Path: "a.go", Hunks: []gitclone.Hunk{
		{Lines: []gitclone.Line{
			{Type: "context", OldNum: 9, NewNum: 10},
			{Type: "delete", OldNum: 10},
			{Type: "add", NewNum: 11},
			{Type: "add", NewNum: 12},
			{Type: "context", OldNum: 11, NewNum: 13},
		}},
		{Lines: []gitclone.Line{
			{Type: "add", NewNum: 40},
			{Type: "context", OldNum: 38, NewNum: 41},
		}},
	}}
}

func TestValidateAnchor(t *testing.T) {
	five, eleven, forty := 5, 11, 40
	cases := []struct {
		name    string
		side    string
		line    int
		start   *int
		wantErr string
	}{
		{"right added line", "RIGHT", 12, nil, ""},
		{"right context line", "RIGHT", 13, nil, ""},
		{"left deleted line", "LEFT", 10, nil, ""},
		{"left context line", "LEFT", 9, nil, ""},
		{"right range in one hunk", "RIGHT", 13, &eleven, ""},
		{"right line outside hunks", "RIGHT", 20, nil, "valid RIGHT lines for a.go: 10-13, 40-41"},
		{"left on added-only line", "LEFT", 12, nil, "valid LEFT lines"},
		{"range across hunks", "RIGHT", 41, &eleven, "same hunk"},
		{"range start after end", "RIGHT", 11, &forty, "start_line"},
		{"range start outside diff", "RIGHT", 12, &five, "valid RIGHT lines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnchor(anchorFile(), tc.side, tc.line, tc.start)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			Assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
