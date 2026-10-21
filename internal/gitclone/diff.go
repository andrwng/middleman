package gitclone

import (
	"bytes"
	"context"
	"fmt"
)

// DiffFiles returns file metadata (path, status, renames) without patch
// content. It runs only git diff --raw, which is much faster than a full
// diff for large PRs.
func (m *Manager) DiffFiles(
	ctx context.Context,
	host, owner, name, mergeBase, headSHA string,
) ([]DiffFile, error) {
	clonePath := m.ClonePath(host, owner, name)
	rawOut, err := m.git(ctx, host, clonePath,
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder", mergeBase, headSHA,
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw: %w", err)
	}
	files := ParseRawZ(rawOut)
	if files == nil {
		files = []DiffFile{}
	}
	// Ensure Hunks is never nil so JSON serializes as [] not null.
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []Hunk{}
		}
	}
	return files, nil
}

// Diff runs a two-dot git diff between mergeBase and headSHA and returns
// structured diff data. If hideWhitespace is true, passes -w to git diff.
func (m *Manager) Diff(
	ctx context.Context,
	host, owner, name, mergeBase, headSHA string,
	hideWhitespace bool,
) (*DiffResult, error) {
	clonePath := m.ClonePath(host, owner, name)

	// Step 1: Compute whitespace-only file count.
	wsCount, err := m.computeWhitespaceOnlyCount(
		ctx, host, clonePath, mergeBase, headSHA)
	if err != nil {
		return nil, fmt.Errorf("whitespace count: %w", err)
	}

	// Step 2: Get file metadata from --raw -z (with rename/copy detection).
	rawArgs := []string{
		"diff", "--raw", "-z", "-M", "-C",
		"--find-copies-harder", mergeBase, headSHA,
	}
	if hideWhitespace {
		rawArgs = append(rawArgs[:2],
			append([]string{"-w"}, rawArgs[2:]...)...)
	}
	rawOut, err := m.git(ctx, host, clonePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw: %w", err)
	}
	files := ParseRawZ(rawOut)

	// Step 3: Get patch content.
	patchArgs := []string{
		"diff", "-M", "-C", "--find-copies-harder",
		"-U3", mergeBase, headSHA,
	}
	if hideWhitespace {
		patchArgs = append(patchArgs[:2],
			append([]string{"-w"}, patchArgs[2:]...)...)
	}
	patchOut, err := m.git(ctx, host, clonePath, patchArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff patch: %w", err)
	}

	files = ParsePatch(patchOut, files)
	if files == nil {
		files = []DiffFile{}
	}
	// Binary/rename-only files appear in --raw but not in patch output,
	// so ParsePatch leaves their Hunks as nil. Marshal that as `[]`
	// (not `null`) so the frontend's `file.hunks[0]` access is safe.
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []Hunk{}
		}
	}

	// Step 4: Resolve whitespace-only files. `git diff --raw -w` in Step 2
	// does NOT drop them on git 2.43, so handle them explicitly here: in
	// hide mode remove them from the list; otherwise mark them so the
	// frontend can badge them.
	wsFiles := m.getWhitespaceOnlyFiles(
		ctx, host, clonePath, mergeBase, headSHA)
	if hideWhitespace {
		kept := files[:0]
		for _, f := range files {
			if !wsFiles[f.Path] {
				kept = append(kept, f)
			}
		}
		files = kept
	} else {
		for i := range files {
			if wsFiles[files[i].Path] {
				files[i].IsWhitespaceOnly = true
			}
		}
	}

	return &DiffResult{
		WhitespaceOnlyCount: wsCount,
		Files:               files,
	}, nil
}

// DiffSummary returns per-file numstat counts (additions, deletions,
// binary) without patch content -- the primitive behind the /files
// endpoint, sized to stay cheap on huge diffs. Status, old_path, and
// whitespace-only handling match Diff exactly; only Hunks differs (always
// empty).
func (m *Manager) DiffSummary(
	ctx context.Context,
	host, owner, name, from, to string,
	hideWhitespace bool,
) (*DiffResult, error) {
	clonePath := m.ClonePath(host, owner, name)

	wsCount, err := m.computeWhitespaceOnlyCount(ctx, host, clonePath, from, to)
	if err != nil {
		return nil, fmt.Errorf("whitespace count: %w", err)
	}

	rawArgs := withWhitespaceFlag([]string{
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder", from, to,
	}, hideWhitespace)
	rawOut, err := m.git(ctx, host, clonePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw: %w", err)
	}
	files := ParseRawZ(rawOut)

	numstatArgs := withWhitespaceFlag([]string{
		"diff", "--numstat", "-z", "-M", "-C", "--find-copies-harder", from, to,
	}, hideWhitespace)
	numOut, err := m.git(ctx, host, clonePath, numstatArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat: %w", err)
	}
	entries := ParseNumstatZ(numOut)

	for i := range files {
		if e, ok := entries[files[i].Path]; ok {
			files[i].Additions = e.Additions
			files[i].Deletions = e.Deletions
			files[i].IsBinary = e.Binary
		}
		if files[i].Hunks == nil {
			files[i].Hunks = []Hunk{}
		}
	}

	files = applyWhitespaceOnly(
		files, m.getWhitespaceOnlyFiles(ctx, host, clonePath, from, to), hideWhitespace)

	if files == nil {
		files = []DiffFile{}
	}
	return &DiffResult{
		WhitespaceOnlyCount: wsCount,
		Files:               files,
	}, nil
}

// DiffPaths runs the same raw+patch diff as Diff, limited to the given new
// and old paths via --literal-pathspecs, and keeps only files whose path or
// old_path is in the requested set (dropping a stray partner the pathspec
// might otherwise introduce). Output shape matches Diff, with one
// deliberate difference: WhitespaceOnlyCount is always 0. This backs the
// per-batch hunks endpoint, called many times per page, so its cost must
// scale with the batch, not the whole from..to diff; the batch-wide count
// isn't needed anyway since the /files summary already carries the total.
// Per-file IsWhitespaceOnly is still computed correctly, scoped to the
// requested paths rather than the full diff, and the caller is expected to
// pass both new and old paths for renames and copies.
func (m *Manager) DiffPaths(
	ctx context.Context,
	host, owner, name, from, to string,
	hideWhitespace bool,
	paths []string,
) (*DiffResult, error) {
	paths = cleanPaths(paths)
	if len(paths) == 0 {
		return &DiffResult{Files: []DiffFile{}}, nil
	}

	clonePath := m.ClonePath(host, owner, name)

	rawArgs := withWhitespaceFlag([]string{
		"diff", "--raw", "-z", "-M", "-C", "--find-copies-harder", from, to,
	}, hideWhitespace)
	rawArgs = pathLimitedArgs(rawArgs, paths)
	rawOut, err := m.git(ctx, host, clonePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw: %w", err)
	}
	files := ParseRawZ(rawOut)

	patchArgs := withWhitespaceFlag([]string{
		"diff", "-M", "-C", "--find-copies-harder", "-U3", from, to,
	}, hideWhitespace)
	patchArgs = pathLimitedArgs(patchArgs, paths)
	patchOut, err := m.git(ctx, host, clonePath, patchArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff patch: %w", err)
	}
	files = ParsePatch(patchOut, files)
	for i := range files {
		if files[i].Hunks == nil {
			files[i].Hunks = []Hunk{}
		}
	}

	wsFiles, err := m.getWhitespaceOnlyFilesForPaths(ctx, host, clonePath, from, to, paths)
	if err != nil {
		return nil, fmt.Errorf("whitespace-only files: %w", err)
	}
	files = applyWhitespaceOnly(files, wsFiles, hideWhitespace)

	// Keep only files the caller actually asked for; a pathspec can pull in
	// a stray partner (e.g. matching only one side of a rename).
	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	kept := files[:0]
	for _, f := range files {
		if pathSet[f.Path] || pathSet[f.OldPath] {
			kept = append(kept, f)
		}
	}
	files = kept

	if files == nil {
		files = []DiffFile{}
	}
	return &DiffResult{
		Files: files,
	}, nil
}

// withWhitespaceFlag inserts -w right after the leading "diff" argument
// when hide is true, leaving args untouched otherwise.
func withWhitespaceFlag(args []string, hide bool) []string {
	if !hide {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[0], "-w")
	out = append(out, args[1:]...)
	return out
}

// cleanPaths drops empty and duplicate entries, preserving the order of
// first occurrence. An empty string is not a valid git pathspec ("empty
// string is not a valid pathspec; perhaps you forgot to add either ':/' or
// '.' ?"), so a caller-supplied "" must never reach the pathspec list.
func cleanPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// pathLimitedArgs appends --literal-pathspecs (as a leading global flag) and
// a "-- <paths>" pathspec list to args, so paths containing characters like
// "[" or spaces are matched literally rather than as globs. paths is
// cleaned of empty and duplicate entries first.
func pathLimitedArgs(args []string, paths []string) []string {
	paths = cleanPaths(paths)
	out := make([]string, 0, len(args)+len(paths)+2)
	out = append(out, "--literal-pathspecs")
	out = append(out, args...)
	out = append(out, "--")
	out = append(out, paths...)
	return out
}

// applyWhitespaceOnly mirrors Diff's step 4: in hide mode it drops
// whitespace-only files; otherwise it flags them for the frontend badge.
func applyWhitespaceOnly(files []DiffFile, wsFiles map[string]bool, hide bool) []DiffFile {
	if hide {
		kept := files[:0]
		for _, f := range files {
			if !wsFiles[f.Path] {
				kept = append(kept, f)
			}
		}
		return kept
	}
	for i := range files {
		if wsFiles[files[i].Path] {
			files[i].IsWhitespaceOnly = true
		}
	}
	return files
}

func (m *Manager) computeWhitespaceOnlyCount(
	ctx context.Context, host, clonePath, mergeBase, headSHA string,
) (int, error) {
	// Non-whitespace-ignoring pass.
	out1, err := m.git(ctx, host, clonePath,
		"diff", "--raw", "-z", "--no-renames", mergeBase, headSHA)
	if err != nil {
		return 0, err
	}
	// Whitespace-ignoring pass. Use --numstat, not --raw: on git 2.43
	// `git diff --raw -w` still lists whitespace-only files, whereas
	// --numstat -w omits them — which is the set this subtraction needs.
	out2, err := m.git(ctx, host, clonePath,
		"diff", "--numstat", "-z", "--no-renames", "-w", mergeBase, headSHA)
	if err != nil {
		return 0, err
	}

	allFiles := parseRawZPaths(out1)
	wFiles := parseNumstatZPaths(out2)

	count := 0
	for f := range allFiles {
		if !wFiles[f] {
			count++
		}
	}
	return count, nil
}

func (m *Manager) getWhitespaceOnlyFiles(
	ctx context.Context, host, clonePath, mergeBase, headSHA string,
) map[string]bool {
	out1, err := m.git(ctx, host, clonePath,
		"diff", "--raw", "-z", "--no-renames", mergeBase, headSHA)
	if err != nil {
		return nil
	}
	// See computeWhitespaceOnlyCount: --numstat -w omits whitespace-only
	// files (git 2.43's --raw -w does not), so it is the correct primitive.
	out2, err := m.git(ctx, host, clonePath,
		"diff", "--numstat", "-z", "--no-renames", "-w", mergeBase, headSHA)
	if err != nil {
		return nil
	}

	allFiles := parseRawZPaths(out1)
	wFiles := parseNumstatZPaths(out2)

	result := make(map[string]bool)
	for f := range allFiles {
		if !wFiles[f] {
			result[f] = true
		}
	}
	return result
}

// getWhitespaceOnlyFilesForPaths is the path-limited counterpart to
// getWhitespaceOnlyFiles, used by DiffPaths so whitespace-only detection
// costs scale with the requested batch, not the full from..to diff.
// Whitespace-only status is a per-file property (comparing a file's own
// with- and without-whitespace diff), so scoping the underlying commands to
// paths does not change the answer for any file in that scope.
func (m *Manager) getWhitespaceOnlyFilesForPaths(
	ctx context.Context, host, clonePath, from, to string, paths []string,
) (map[string]bool, error) {
	rawArgs := pathLimitedArgs(
		[]string{"diff", "--raw", "-z", "--no-renames", from, to}, paths)
	out1, err := m.git(ctx, host, clonePath, rawArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw --no-renames: %w", err)
	}
	// See computeWhitespaceOnlyCount: --numstat -w omits whitespace-only
	// files (git 2.43's --raw -w does not), so it is the correct primitive.
	numstatArgs := pathLimitedArgs(
		[]string{"diff", "--numstat", "-z", "--no-renames", "-w", from, to}, paths)
	out2, err := m.git(ctx, host, clonePath, numstatArgs...)
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat --no-renames -w: %w", err)
	}

	allFiles := parseRawZPaths(out1)
	wFiles := parseNumstatZPaths(out2)

	result := make(map[string]bool)
	for f := range allFiles {
		if !wFiles[f] {
			result[f] = true
		}
	}
	return result, nil
}

// parseRawZPaths extracts just the file paths from --raw -z output.
func parseRawZPaths(data []byte) map[string]bool {
	files := ParseRawZ(data)
	paths := make(map[string]bool, len(files))
	for _, f := range files {
		paths[f.Path] = true
	}
	return paths
}

// parseNumstatZPaths extracts file paths from `git diff --numstat -z`
// output. Each NUL-terminated record is "<added>\t<deleted>\t<path>".
// Callers pass --no-renames so the three-field rename form never appears.
func parseNumstatZPaths(data []byte) map[string]bool {
	paths := make(map[string]bool)
	for rec := range bytes.SplitSeq(data, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		parts := bytes.SplitN(rec, []byte("\t"), 3)
		if len(parts) < 3 {
			continue
		}
		paths[string(parts[2])] = true
	}
	return paths
}
