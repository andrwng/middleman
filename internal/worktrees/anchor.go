package worktrees

import (
	"bytes"
	"strconv"
)

// Hunk is one "@@ -a,b +c,d @@" header from a unified diff. Counts are
// resolved to their real value, so an omitted count reads as 1 (git writes
// "@@ -5 +7,0 @@" for a single old line replaced by nothing).
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
}

// ParseHunks pulls the hunk headers out of a unified diff. Only the headers
// matter here: with -U0 there is no context to interpret, and the line
// arithmetic needs nothing but the four numbers.
func ParseHunks(diffOut []byte) []Hunk {
	var out []Hunk
	for line := range bytes.SplitSeq(diffOut, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("@@ ")) {
			continue
		}
		rest := line[3:]
		before, _, ok := bytes.Cut(rest, []byte(" @@"))
		if !ok {
			continue
		}
		fields := bytes.Fields(before)
		if len(fields) != 2 ||
			!bytes.HasPrefix(fields[0], []byte("-")) ||
			!bytes.HasPrefix(fields[1], []byte("+")) {
			continue
		}
		oldStart, oldCount, ok := parseRange(fields[0][1:])
		if !ok {
			continue
		}
		newStart, newCount, ok := parseRange(fields[1][1:])
		if !ok {
			continue
		}
		out = append(out, Hunk{oldStart, oldCount, newStart, newCount})
	}
	return out
}

// parseRange reads "start" or "start,count"; an absent count means 1.
func parseRange(b []byte) (start, count int, ok bool) {
	count = 1
	if i := bytes.IndexByte(b, ','); i >= 0 {
		n, err := strconv.Atoi(string(b[i+1:]))
		if err != nil {
			return 0, 0, false
		}
		count = n
		b = b[:i]
	}
	n, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, 0, false
	}
	return n, count, true
}

// MapLine translates a line number from the diff's old side to its new
// side. It reports only the number and whether the line itself was deleted;
// whether that counts as "moved" is the caller's comparison, so this stays
// free of any UI vocabulary.
//
// Hunks must be in the order git emitted them (ascending old start).
func MapLine(hunks []Hunk, old int) (newLine int, removed bool) {
	offset := 0
	for _, h := range hunks {
		if old < h.OldStart {
			return old + offset, false
		}
		if old < h.OldStart+h.OldCount {
			if h.NewCount == 0 {
				return 0, true
			}
			within := min(old-h.OldStart, h.NewCount-1)
			return h.NewStart + within, false
		}
		offset += h.NewCount - h.OldCount
	}
	return old + offset, false
}
