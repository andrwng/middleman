package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wesm/middleman/internal/gitclone"
)

// sideLine returns the line number a diff line has on side, or 0 when
// the line does not exist on that side (an added line has no LEFT
// number, a deleted line no RIGHT number).
func sideLine(l gitclone.Line, side string) int {
	if side == "LEFT" {
		if l.Type == "add" {
			return 0
		}
		return l.OldNum
	}
	if l.Type == "delete" {
		return 0
	}
	return l.NewNum
}

func hunkOf(f *gitclone.DiffFile, side string, line int) int {
	for i, h := range f.Hunks {
		for _, l := range h.Lines {
			if n := sideLine(l, side); n != 0 && n == line {
				return i
			}
		}
	}
	return -1
}

// describeRanges renders the lines commentable on side as compact
// ranges, e.g. "10-13, 40-41".
func describeRanges(f *gitclone.DiffFile, side string) string {
	var parts []string
	for _, h := range f.Hunks {
		start, prev := 0, 0
		flush := func() {
			if start == 0 {
				return
			}
			if start == prev {
				parts = append(parts, strconv.Itoa(start))
			} else {
				parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
			}
			start = 0
		}
		for _, l := range h.Lines {
			n := sideLine(l, side)
			if n == 0 {
				continue
			}
			if start != 0 && n == prev+1 {
				prev = n
				continue
			}
			flush()
			start, prev = n, n
		}
		flush()
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// validateAnchor checks that (side, line[, startLine]) is commentable in
// f, the way GitHub requires for review comments. Errors name the valid
// ranges so Claude can pick a correct anchor and retry.
func validateAnchor(f *gitclone.DiffFile, side string, line int, startLine *int) error {
	invalid := func(n int) error {
		return fmt.Errorf("line %d is not in the diff on side %s; valid %s lines for %s: %s",
			n, side, side, f.Path, describeRanges(f, side))
	}
	end := hunkOf(f, side, line)
	if end < 0 {
		return invalid(line)
	}
	if startLine == nil {
		return nil
	}
	if *startLine > line {
		return fmt.Errorf("start_line %d must not be after line %d", *startLine, line)
	}
	start := hunkOf(f, side, *startLine)
	if start < 0 {
		return invalid(*startLine)
	}
	if start != end {
		return fmt.Errorf("start_line %d and line %d must be in the same hunk", *startLine, line)
	}
	return nil
}

// anchorFileDiff loads one file's diff between base and head for anchor
// validation. Returns nil when the file is not changed. Filtering a diff
// by the new path alone hides a rename (git sees a brand-new file), so
// the old path is looked up first and both are passed.
func anchorFileDiff(
	ctx context.Context, clones *gitclone.Manager, host, owner, name, base, head, path string,
) (*gitclone.DiffFile, error) {
	files, err := clones.DiffFiles(ctx, host, owner, name, base, head)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, f := range files {
		if f.Path == path {
			paths = append(paths, path)
			if f.OldPath != "" && f.OldPath != path {
				paths = append(paths, f.OldPath)
			}
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	diff, err := clones.DiffPaths(ctx, host, owner, name, base, head, false, paths)
	if err != nil {
		return nil, err
	}
	for i := range diff.Files {
		if diff.Files[i].Path == path {
			return &diff.Files[i], nil
		}
	}
	return nil, nil
}
