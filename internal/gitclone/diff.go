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
	for _, rec := range bytes.Split(data, []byte{0}) {
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
