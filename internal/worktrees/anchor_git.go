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

// AnchorDiff is one `git diff srcRev dstRev` already run and held, ready
// to resolve any number of anchors against.
//
// It exists so a caller with many threads pays for one git call per
// (srcRev, dstRev) pair rather than one per thread: the diff text for a
// given pair says nothing about which path or line the caller is asking
// about, so every thread anchored at the same commit shares it. The zero
// value is not usable -- build one with FetchAnchorDiff.
type AnchorDiff struct {
	out []byte
	// unreadable records that the git call itself failed, which every
	// anchor resolved through this diff must report as AnchorUnmappable.
	unreadable bool
}

// FetchAnchorDiff runs the single diff anchor resolution needs, from
// `srcRev` to `dstRev`. dstRev may be "" or WorkingTreeSentinel, in which
// case the comparison runs against the worktree's current uncommitted
// state instead of a commit.
//
// Never returns an error: an unresolvable revision is a state the reader
// is shown, not a request failure, because a review thread must stay
// reachable no matter what git reports.
//
// Both revisions reach git as positional arguments. Callers must have
// already established that they are hex object ids (or the working-tree
// sentinel) -- see the --end-of-options note below for why that matters
// even with the guard this function adds.
func FetchAnchorDiff(ctx context.Context, worktreePath, srcRev, dstRev string) AnchorDiff {
	// core.quotePath=false and the explicit --src-prefix/--dst-prefix stop
	// a user's own git config (core.quotePath, diff.noprefix) from reshaping
	// the "diff --git a/OLD b/NEW" header lines that fileSection below
	// depends on for exact-prefix matching. Without them the header could
	// read without any a/ b/ prefixes at all, or with an escaped path, and
	// the scan would silently find nothing.
	//
	// --end-of-options is defense in depth for the revision slots. git
	// parses options anywhere on its command line, including where a
	// revision is expected, so a srcRev/dstRev of "--output=/path" would
	// otherwise reach `git diff` as a real option and truncate that file
	// with diff text. Callers validate both revisions (they are hex object
	// ids or the working-tree sentinel), but this makes the argv itself
	// incapable of carrying an option past this point -- the same hazard
	// class that `git grep -O<cmd>` turned into an RCE here once before.
	// Verified to leave both the two-revision and source-only diffs
	// byte-identical.
	args := []string{
		"-c", "core.quotePath=false",
		"diff", "-U0", "-M",
		"--src-prefix=a/", "--dst-prefix=b/",
		"--end-of-options",
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
		return AnchorDiff{unreadable: true}
	}
	return AnchorDiff{out: out}
}

// Resolve maps `line` in `path`, as numbered on this diff's old side,
// forward to its new side.
func (d AnchorDiff) Resolve(path string, line int) ResolvedAnchor {
	if d.unreadable {
		return ResolvedAnchor{State: AnchorUnmappable}
	}

	newPath, section, found := fileSection(d.out, path)
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

// ResolveAnchor maps `line` in `path`, as recorded at `srcRev`, forward to
// `dstRev` -- FetchAnchorDiff plus one Resolve, for the single-anchor case
// where there is nothing to share the diff with.
func ResolveAnchor(
	ctx context.Context, worktreePath, srcRev, dstRev, path string, line int,
) ResolvedAnchor {
	return FetchAnchorDiff(ctx, worktreePath, srcRev, dstRev).Resolve(path, line)
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
// mis-parse. Prefix-matching the fixed "diff --git a/<path> b/" text
// narrows that, and it also can't be fooled by a same-named prefix like
// "api.go.bak": the literal " b/" immediately after path is part of the
// match, so "diff --git a/api.go.bak b/api.go.bak" does not satisfy the
// prefix built for "api.go" (the byte after "api.go" would have to be a
// space, but it is ".").
//
// Two residual cases remain, both benign:
//   - core.quotePath=false suppresses quoting of non-ASCII bytes, but git
//     still C-quotes a path containing a literal '"', '\' or a newline. The
//     header for such a file reads "diff --git \"a/we\\\"ird.go\" ..." and
//     the prefix built here will not match it, so the anchor falls through
//     to AnchorCurrent -- the thread stays placed where it was recorded and
//     stays reachable, which is the safe direction.
//   - For path "foo" the prefix "diff --git a/foo b/" also matches a file
//     literally named "foo b/bar.go" (its header reads
//     "diff --git a/foo b/bar.go b/foo b/bar.go"). Such a name would have
//     to exist alongside the real "foo" for this to matter, and the worst
//     outcome is a line mapped through the wrong file's hunks.
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
