package gitclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// InterdiffKind classifies the result of an interdiff computation.
// The caller decides how to render based on this — `clean` results
// can be displayed as a normal unified diff; `conflicted` and
// `unrelated` carry a fallback (raw diff between heads) and should
// be banner-flagged in the UI so the reviewer knows the rebase
// noise wasn't subtracted.
type InterdiffKind string

const (
	// InterdiffClean: replaying the old patchset onto the new base
	// succeeded; the returned diff is the author's net change.
	InterdiffClean InterdiffKind = "clean"
	// InterdiffConflicted: the synthetic 3-way merge had conflicts
	// (the author resolved conflicts during their rebase that we
	// can't replay). The returned diff is the raw oldHead..newHead
	// and includes rebase noise.
	InterdiffConflicted InterdiffKind = "conflicted"
	// InterdiffUnrelated: ranges have no overlapping ancestry, or a
	// required SHA is missing/empty. Returned diff is the raw
	// oldHead..newHead.
	InterdiffUnrelated InterdiffKind = "unrelated"
)

// InterdiffResult carries the result of InterdiffPatchsets.
type InterdiffResult struct {
	Diff   []byte
	Kind   InterdiffKind
	Reason string
}

// StructuredInterdiff is the parsed equivalent of InterdiffResult,
// shaped the same as gitclone.DiffResult so callers can render
// it through the existing diff UI without special-casing.
type StructuredInterdiff struct {
	Result *DiffResult
	Kind   InterdiffKind
	Reason string
}

// InterdiffPatchsets computes a Gerrit-style "patchset N vs M with
// rebase noise subtracted" diff.
//
// Strategy: ask git for an in-memory 3-way merge of (newBase, oldHead)
// using oldBase as the merge base — equivalent to replaying the
// author's old commits onto the new base, but without touching a
// working tree. Diff the resulting synthetic tree against newHead.
// When merge-tree reports conflicts (the author resolved conflicts
// during their rebase) we fall back to the raw oldHead..newHead diff
// with a `conflicted` kind so the caller can banner-flag it.
func (m *Manager) InterdiffPatchsets(
	ctx context.Context,
	host, owner, name string,
	oldHead, oldBase, newHead, newBase string,
) (InterdiffResult, error) {
	cloneDir := m.ClonePath(host, owner, name)

	if oldHead == "" || oldBase == "" || newHead == "" || newBase == "" {
		raw, err := m.rawDiffBetween(ctx, host, cloneDir, oldHead, newHead)
		if err != nil {
			return InterdiffResult{}, err
		}
		return InterdiffResult{
			Kind:   InterdiffUnrelated,
			Diff:   raw,
			Reason: "missing patchset SHAs",
		}, nil
	}

	if err := m.requireSHAs(ctx, host, cloneDir, owner, name, oldHead, oldBase, newHead, newBase); err != nil {
		return InterdiffResult{}, err
	}

	if oldHead == newHead {
		return InterdiffResult{Kind: InterdiffClean}, nil
	}

	if _, err := m.git(ctx, host, cloneDir, "merge-base", oldBase, newBase); err != nil {
		raw, dErr := m.git(ctx, host, cloneDir, "diff", oldHead, newHead)
		if dErr != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff: unrelated histories and raw diff failed: %w", dErr)
		}
		return InterdiffResult{
			Kind:   InterdiffUnrelated,
			Diff:   raw,
			Reason: "patchsets share no common ancestor",
		}, nil
	}

	// Empty old range: synthetic tree == newBase, so diff newBase..newHead.
	if oldBase == oldHead {
		diff, err := m.git(ctx, host, cloneDir, "diff", newBase, newHead)
		if err != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff (empty old range) diff: %w", err)
		}
		return InterdiffResult{Kind: InterdiffClean, Diff: diff}, nil
	}

	syntheticTree, conflicted, err := m.mergeTreeReplay(ctx, host, cloneDir, oldBase, newBase, oldHead)
	if err != nil {
		return InterdiffResult{}, fmt.Errorf("interdiff: merge-tree: %w", err)
	}
	if conflicted {
		raw, dErr := m.git(ctx, host, cloneDir, "diff", oldHead, newHead)
		if dErr != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff: conflict and raw diff failed: %w", dErr)
		}
		return InterdiffResult{
			Kind:   InterdiffConflicted,
			Diff:   raw,
			Reason: "merge of old patchset onto new base had conflicts",
		}, nil
	}

	diff, err := m.git(ctx, host, cloneDir, "diff", syntheticTree, newHead)
	if err != nil {
		return InterdiffResult{}, fmt.Errorf("interdiff: synthetic diff: %w", err)
	}
	return InterdiffResult{Kind: InterdiffClean, Diff: diff}, nil
}

// InterdiffPatchsetsStructured returns the same result as
// InterdiffPatchsets but as a parsed *DiffResult so callers can
// render through the existing diff UI without parsing raw bytes.
func (m *Manager) InterdiffPatchsetsStructured(
	ctx context.Context,
	host, owner, name string,
	oldHead, oldBase, newHead, newBase string,
	hideWhitespace bool,
) (StructuredInterdiff, error) {
	cloneDir := m.ClonePath(host, owner, name)

	if oldHead == "" || oldBase == "" || newHead == "" || newBase == "" {
		if oldHead == "" || newHead == "" || oldHead == newHead {
			return StructuredInterdiff{
				Result: &DiffResult{Files: []DiffFile{}},
				Kind:   InterdiffUnrelated,
				Reason: "missing patchset SHAs",
			}, nil
		}
		res, err := m.Diff(ctx, host, owner, name, oldHead, newHead, hideWhitespace)
		if err != nil {
			return StructuredInterdiff{}, err
		}
		return StructuredInterdiff{
			Result: res,
			Kind:   InterdiffUnrelated,
			Reason: "missing patchset SHAs",
		}, nil
	}

	if err := m.requireSHAs(ctx, host, cloneDir, owner, name, oldHead, oldBase, newHead, newBase); err != nil {
		return StructuredInterdiff{}, err
	}

	if oldHead == newHead {
		return StructuredInterdiff{
			Result: &DiffResult{Files: []DiffFile{}},
			Kind:   InterdiffClean,
		}, nil
	}

	if _, err := m.git(ctx, host, cloneDir, "merge-base", oldBase, newBase); err != nil {
		res, dErr := m.Diff(ctx, host, owner, name, oldHead, newHead, hideWhitespace)
		if dErr != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff: unrelated histories and raw diff failed: %w", dErr)
		}
		return StructuredInterdiff{
			Result: res,
			Kind:   InterdiffUnrelated,
			Reason: "patchsets share no common ancestor",
		}, nil
	}

	if oldBase == oldHead {
		res, err := m.Diff(ctx, host, owner, name, newBase, newHead, hideWhitespace)
		if err != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff (empty old range) diff: %w", err)
		}
		return StructuredInterdiff{Result: res, Kind: InterdiffClean}, nil
	}

	syntheticTree, conflicted, err := m.mergeTreeReplay(ctx, host, cloneDir, oldBase, newBase, oldHead)
	if err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: merge-tree: %w", err)
	}
	if conflicted {
		res, dErr := m.Diff(ctx, host, owner, name, oldHead, newHead, hideWhitespace)
		if dErr != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff: conflict and raw diff failed: %w", dErr)
		}
		return StructuredInterdiff{
			Result: res,
			Kind:   InterdiffConflicted,
			Reason: "merge of old patchset onto new base had conflicts",
		}, nil
	}

	res, err := m.Diff(ctx, host, owner, name, syntheticTree, newHead, hideWhitespace)
	if err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: structured synthetic diff: %w", err)
	}
	return StructuredInterdiff{Result: res, Kind: InterdiffClean}, nil
}

// mergeTreeReplay runs an in-memory 3-way merge of (newBase, oldHead)
// — semantically equivalent to cherry-picking oldBase..oldHead onto
// newBase, but without touching a working tree. Returns the
// resulting tree SHA and whether the merge had conflicts.
//
// Apple Git 2.39 doesn't support --merge-base=; we rely on git's
// auto-detection from the two refs. In our model oldHead descends
// from oldBase and newBase shares oldBase, so merge-base(newBase,
// oldHead) will resolve to oldBase (or its ancestor) — exactly what
// we want. The caller has already pre-checked shared ancestry.
//
// Exit codes:
//
//	0  → clean merge, output is the tree SHA on a single line
//	1  → conflicts present, first line is still the tree SHA
//	>1 → genuine error (missing object, IO problem, etc.)
func (m *Manager) mergeTreeReplay(
	ctx context.Context, host, cloneDir, oldBase, newBase, oldHead string,
) (treeSHA string, conflicted bool, err error) {
	_ = oldBase // currently passed in for symmetry / future --merge-base wiring
	out, err := m.git(ctx, host, cloneDir,
		"merge-tree", "--write-tree", "--name-only",
		newBase, oldHead,
	)
	if err != nil {
		// Exit code 1 = conflict (still useful — first line is the
		// tree SHA, but we don't trust it for a "clean" interdiff).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", true, nil
		}
		// Stderr is wrapped in the error; check for the "unrelated
		// histories" message to translate it into our caller-visible
		// kind. The pre-check via `git merge-base` should catch most
		// of these, but some edge cases (e.g. one side is a tree
		// already merged-in) only surface here.
		if strings.Contains(err.Error(), "unrelated histories") {
			return "", true, nil
		}
		return "", false, err
	}
	first := bytes.IndexByte(out, '\n')
	if first < 0 {
		first = len(out)
	}
	treeSHA = strings.TrimSpace(string(out[:first]))
	if treeSHA == "" {
		return "", false, fmt.Errorf("merge-tree: empty tree SHA in output: %q", out)
	}
	return treeSHA, false, nil
}

// requireSHAs verifies that every SHA passed to interdiff is
// reachable in the bare clone, so the caller doesn't end up with a
// nonsense fallback diff produced by git silently treating a missing
// SHA as an empty tree.
func (m *Manager) requireSHAs(
	ctx context.Context, host, cloneDir, owner, name string, shas ...string,
) error {
	for _, sha := range shas {
		if _, err := m.git(ctx, host, cloneDir, "cat-file", "-e", sha); err != nil {
			return fmt.Errorf("interdiff: sha %s not in clone %s/%s: %w", sha, owner, name, err)
		}
	}
	return nil
}

// rawDiffBetween is the fallback `git diff a b` for cases where
// interdiff math doesn't apply (missing SHAs, unrelated ancestry).
// Empty inputs short-circuit to nil so callers can render an empty
// diff without distinguishing "no change" from "no data".
func (m *Manager) rawDiffBetween(
	ctx context.Context, host, dir, oldHead, newHead string,
) ([]byte, error) {
	if oldHead == "" || newHead == "" || oldHead == newHead {
		return nil, nil
	}
	out, err := m.git(ctx, host, dir, "diff", oldHead, newHead)
	if err != nil {
		return nil, fmt.Errorf("raw diff %s..%s: %w", oldHead, newHead, err)
	}
	return out, nil
}
