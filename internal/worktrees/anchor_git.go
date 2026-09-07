package worktrees

import (
	"bytes"
	"context"
)

// AnchorState says what became of a review thread's recorded line by the
// time the reader is looking at some later revision.
type AnchorState string

const (
	// AnchorCurrent: same path, same line number. Place it as recorded.
	AnchorCurrent AnchorState = "current"
	// AnchorMoved: the line survives, but at a different number or path.
	AnchorMoved AnchorState = "moved"
	// AnchorRemoved: the line itself was deleted.
	AnchorRemoved AnchorState = "removed"
	// AnchorUnmappable: the source revision could not be read at all --
	// typically a commit that was rebased away and garbage-collected. The
	// caller keeps the thread reachable and tells the reader its position
	// is unknown rather than failing the request.
	AnchorUnmappable AnchorState = "unmappable"
)

// ResolvedAnchor is where a recorded anchor lands at the target revision.
// Path and Line are meaningful only for AnchorCurrent and AnchorMoved.
type ResolvedAnchor struct {
	State AnchorState
	Path  string
	Line  int
}

// ResolveAnchor maps `line` in `path`, as recorded at `srcRev`, forward to
// `dstRev`. dstRev may be "" or WorkingTreeSentinel, in which case the
// comparison runs against the worktree's current uncommitted state instead
// of a commit.
//
// ResolveAnchor never returns an error: an unresolvable anchor is a state
// the reader is shown, not a request failure, because a review thread must
// stay reachable no matter what git reports about the revision.
func ResolveAnchor(
	ctx context.Context, worktreePath, srcRev, dstRev, path string, line int,
) ResolvedAnchor {
	// core.quotePath=false and the explicit --src-prefix/--dst-prefix stop
	// a user's own git config (core.quotePath, diff.noprefix) from reshaping
	// the "diff --git a/OLD b/NEW" header lines that fileSection below
	// depends on for exact-prefix matching. Without them the header could
	// read without any a/ b/ prefixes at all, or with an escaped path, and
	// the scan would silently find nothing.
	args := []string{
		"-c", "core.quotePath=false",
		"diff", "-U0", "-M", "--find-renames",
		"--src-prefix=a/", "--dst-prefix=b/",
		srcRev,
	}
	if dstRev != "" && dstRev != WorkingTreeSentinel {
		args = append(args, dstRev)
	}

	// Deliberately no "-- path" pathspec. With one, a rename can degrade to
	// a plain deletion of the old path (the new name falls outside the
	// spec, so git has nothing to pair it against), which would resolve a
	// renamed anchor as AnchorRemoved and lose the code the reviewer
	// commented on. Diffing the whole tree and picking out our file's
	// section ourselves costs one linear scan and keeps rename detection
	// intact.
	out, err := gitCmd(ctx, worktreePath, args...)
	if err != nil {
		// Most likely srcRev is unknown to this repository: rebased away
		// and collected, or simply never existed.
		return ResolvedAnchor{State: AnchorUnmappable}
	}

	newPath, section, found := fileSection(out, path)
	if !found {
		// The file did not change between srcRev and dstRev at all, so the
		// anchor stands exactly where it was recorded. This also covers a
		// thread anchored to a path that never existed at srcRev: git says
		// nothing about it either way, and leaving the thread at its
		// recorded position is the safe direction under "never hide a
		// thread" -- it stays openable regardless.
		return ResolvedAnchor{State: AnchorCurrent, Path: path, Line: line}
	}

	mapped, removed := MapLine(ParseHunks(section), line)
	return classifyMapped(path, line, newPath, mapped, removed)
}

// classifyMapped turns an already-computed line-mapping outcome into a
// ResolvedAnchor. It is split out of ResolveAnchor so this decision --
// "removed" when the line itself is gone, "current" when nothing about the
// anchor actually changed, "moved" otherwise -- can be unit-tested without
// shelling out to git; ResolveAnchor's other branches need a real diff to
// reach, but this one only needs its inputs.
func classifyMapped(origPath string, origLine int, newPath string, mapped int, removed bool) ResolvedAnchor {
	if removed {
		return ResolvedAnchor{State: AnchorRemoved}
	}
	state := AnchorMoved
	if mapped == origLine && newPath == origPath {
		state = AnchorCurrent
	}
	return ResolvedAnchor{State: state, Path: newPath, Line: mapped}
}

// fileSection scans a multi-file "git diff" for the section whose OLD side
// is path, and reports the NEW path named in that section's header.
//
// path is always the pre-image name -- the name the thread was anchored
// under -- and with --src-prefix=a/ --dst-prefix=b/ forced, every section
// header reads exactly "diff --git a/OLD b/NEW", for a rename or an
// unrenamed file alike. So matching on the OLD half finds the right section
// either way, and the text after the matched prefix is the NEW path.
//
// The match is an exact byte prefix, not a whitespace split: git does not
// quote a path merely for containing a space, so "diff --git a/my file.go
// b/my file.go" is a real, unquoted header that strings.Fields would
// mis-parse. Prefix-matching the fixed "diff --git a/<path> b/" text has no
// such problem, and it also can't be fooled by a same-named prefix like
// "api.go.bak": the literal " b/" immediately after path is part of the
// match, so "diff --git a/api.go.bak b/api.go.bak" does not satisfy the
// prefix built for "api.go" (the byte after "api.go" would have to be a
// space, but it is ".").
func fileSection(diffOut []byte, path string) (newPath string, section []byte, found bool) {
	prefix := []byte("diff --git a/" + path + " b/")
	nextHeader := []byte("diff --git ")

	var lines [][]byte
	for line := range bytes.SplitSeq(diffOut, []byte("\n")) {
		if !found {
			rest, ok := bytes.CutPrefix(line, prefix)
			if !ok {
				continue
			}
			newPath = string(rest)
			found = true
			lines = append(lines, line)
			continue
		}
		// The next file's header ends our section.
		if bytes.HasPrefix(line, nextHeader) {
			break
		}
		lines = append(lines, line)
	}
	if !found {
		return "", nil, false
	}
	return newPath, bytes.Join(lines, []byte("\n")), true
}
