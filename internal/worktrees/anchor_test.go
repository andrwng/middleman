package worktrees

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseHunks(t *testing.T) {
	assert := assert.New(t)

	// Counts default to 1 when omitted, which is how git writes a
	// single-line hunk: "@@ -5 +7,0 @@".
	out := []byte("diff --git a/a.txt b/a.txt\n" +
		"--- a/a.txt\n+++ b/a.txt\n" +
		"@@ -0,0 +1,3 @@\n" +
		"@@ -5 +7,0 @@ four\n")
	assert.Equal([]Hunk{
		{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 3},
		{OldStart: 5, OldCount: 1, NewStart: 7, NewCount: 0},
	}, ParseHunks(out))

	assert.Empty(ParseHunks(nil))
	assert.Empty(ParseHunks([]byte("diff --git a/a b/a\n")))
}

func TestMapLine(t *testing.T) {
	cases := []struct {
		name    string
		hunks   []Hunk
		old     int
		want    int
		removed bool
	}{
		{"no hunks leaves the line alone", nil, 5, 5, false},
		{
			"three lines inserted above shifts down",
			[]Hunk{{0, 0, 1, 3}},
			5, 8, false,
		},
		{
			"insertion below does not move it",
			[]Hunk{{20, 0, 20, 3}},
			5, 5, false,
		},
		{
			"the line itself deleted",
			[]Hunk{{5, 1, 7, 0}},
			5, 0, true,
		},
		{
			"lines deleted above shifts up",
			[]Hunk{{1, 2, 1, 0}},
			5, 3, false,
		},
		{
			"replaced in place keeps the position",
			[]Hunk{{5, 1, 5, 1}},
			5, 5, false,
		},
		{
			"replaced by fewer lines clamps inside the replacement",
			[]Hunk{{5, 3, 5, 1}},
			7, 5, false,
		},
		{
			"offsets accumulate across hunks",
			[]Hunk{{1, 0, 1, 2}, {10, 4, 12, 1}},
			20, 19, false,
		},
		{"first line", []Hunk{{0, 0, 1, 1}}, 1, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, removed := MapLine(tc.hunks, tc.old)
			assert.Equal(t, tc.removed, removed)
			if !tc.removed {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
