package gitclone

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InterdiffKind classifies the result of an interdiff computation.
// The caller decides how to render based on this — `clean` results
// can be displayed as a normal unified diff; `conflicted` and
// `unrelated` carry a fallback (raw diff between heads) and should
// be banner-flagged in the UI so the reviewer knows the rebase
// noise wasn't subtracted.
type InterdiffKind string

const (
	// InterdiffClean: cherry-picking the old patchset onto the new
	// base succeeded; the returned diff is the author's net change.
	InterdiffClean InterdiffKind = "clean"
	// InterdiffConflicted: cherry-pick failed mid-way (the rebase
	// resolved conflicts that we can't replay). The returned diff
	// is the raw oldHead..newHead and includes rebase noise.
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
// Strategy: spin up an ephemeral worktree at newBase, cherry-pick
// oldBase..oldHead into it (replaying the author's old commits onto
// the new base), then diff the synthetic head against newHead. When
// the cherry-pick fails (the author resolved conflicts during the
// rebase) we fall back to the raw oldHead..newHead diff with a
// `conflicted` kind so the caller can banner-flag it.
//
// The worktree is always cleaned up — successful path uses defer,
// the defer also runs cherry-pick --abort on conflict so the
// branch state is sane before removal.
func (m *Manager) InterdiffPatchsets(
	ctx context.Context,
	host, owner, name string,
	oldHead, oldBase, newHead, newBase string,
) (InterdiffResult, error) {
	cloneDir := m.ClonePath(host, owner, name)

	// Empty SHAs: no useful subtraction possible. Fall back to raw
	// diff (or empty when both heads match).
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

	// All SHAs must be present in the clone. Fail fast with a clear
	// error so the caller doesn't end up with a nonsense diff.
	for _, sha := range []string{oldHead, oldBase, newHead, newBase} {
		if _, err := m.git(ctx, host, cloneDir, "cat-file", "-e", sha); err != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff: sha %s not in clone %s/%s: %w", sha, owner, name, err)
		}
	}

	// Trivial case: same head SHA, no diff at all.
	if oldHead == newHead {
		return InterdiffResult{Kind: InterdiffClean}, nil
	}

	// If oldBase and newBase share no ancestry (force-push to a
	// completely different branch), cherry-picking technically
	// applies but the resulting diff is meaningless. Flag as
	// unrelated and return the raw oldHead..newHead diff so the
	// reviewer knows what they're looking at.
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

	// Empty range (oldBase == oldHead): no commits to cherry-pick;
	// the synthetic head is just newBase, so the interdiff is
	// newBase..newHead. Avoids an unnecessary worktree spin-up.
	if oldBase == oldHead {
		diff, err := m.git(ctx, host, cloneDir, "diff", newBase, newHead)
		if err != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff (empty old range) diff: %w", err)
		}
		return InterdiffResult{Kind: InterdiffClean, Diff: diff}, nil
	}

	// Spin up a unique worktree off newBase and cherry-pick
	// oldBase..oldHead onto it.
	wtPath := filepath.Join(
		filepath.Dir(cloneDir),
		"interdiff-worktrees",
		fmt.Sprintf("%s-%d-%d", filepath.Base(cloneDir), time.Now().UnixNano(), rand.Int()),
	)
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return InterdiffResult{}, fmt.Errorf("interdiff: prepare worktree dir: %w", err)
	}

	// Clean up the worktree no matter what — including aborting any
	// in-progress cherry-pick state. Use a detached background
	// context so cleanup runs even if the caller's ctx was
	// cancelled mid-flight.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = m.git(cleanupCtx, host, wtPath, "cherry-pick", "--abort")
		_, _ = m.git(cleanupCtx, host, cloneDir, "worktree", "remove", "--force", wtPath)
		_ = os.RemoveAll(wtPath)
		_, _ = m.git(cleanupCtx, host, cloneDir, "worktree", "prune")
	}()

	if _, err := m.git(ctx, host, cloneDir, "worktree", "add", "--detach", wtPath, newBase); err != nil {
		return InterdiffResult{}, fmt.Errorf("interdiff: worktree add: %w", err)
	}

	// Cherry-pick the old range onto the new base. -X theirs is
	// intentionally NOT used — we want conflicts to surface so we
	// can label the result.
	if _, err := m.git(ctx, host, wtPath, "cherry-pick", oldBase+".."+oldHead); err != nil {
		// Cherry-pick failed (conflict or otherwise). Fall back to
		// the raw oldHead..newHead diff. Cleanup defer handles the
		// abort + worktree teardown.
		raw, dErr := m.git(ctx, host, cloneDir, "diff", oldHead, newHead)
		if dErr != nil {
			return InterdiffResult{}, fmt.Errorf("interdiff: cherry-pick failed (%v) and raw diff failed (%w)", err, dErr)
		}
		return InterdiffResult{
			Kind:   InterdiffConflicted,
			Diff:   raw,
			Reason: "cherry-pick of old patchset onto new base did not apply cleanly",
		}, nil
	}

	// Cherry-pick succeeded; HEAD in the worktree is the synthetic
	// "old patchset replayed onto new base." Diff against newHead.
	diff, err := m.git(ctx, host, wtPath, "diff", "HEAD", newHead)
	if err != nil {
		return InterdiffResult{}, fmt.Errorf("interdiff: synthetic diff: %w", err)
	}
	return InterdiffResult{Kind: InterdiffClean, Diff: diff}, nil
}

// InterdiffPatchsetsStructured is the structured (parsed) equivalent
// of InterdiffPatchsets. Runs the same cherry-pick strategy but
// returns a *DiffResult so callers can render through the existing
// diff UI without parsing raw bytes.
//
// On a clean result the returned DiffResult is the synthetic diff
// (old patchset replayed onto new base → newHead). On conflicted or
// unrelated outcomes it is the raw oldHead..newHead diff.
func (m *Manager) InterdiffPatchsetsStructured(
	ctx context.Context,
	host, owner, name string,
	oldHead, oldBase, newHead, newBase string,
	hideWhitespace bool,
) (StructuredInterdiff, error) {
	cloneDir := m.ClonePath(host, owner, name)

	// Missing SHAs: fall back to raw diff between heads when we have
	// both, else return an empty structured result.
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

	// All SHAs must be reachable.
	for _, sha := range []string{oldHead, oldBase, newHead, newBase} {
		if _, err := m.git(ctx, host, cloneDir, "cat-file", "-e", sha); err != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff: sha %s not in clone %s/%s: %w", sha, owner, name, err)
		}
	}

	// Trivial case: same head.
	if oldHead == newHead {
		return StructuredInterdiff{
			Result: &DiffResult{Files: []DiffFile{}},
			Kind:   InterdiffClean,
		}, nil
	}

	// Unrelated bases: raw diff, labeled unrelated.
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

	// Empty old range: synthetic head == newBase, so diff newBase..newHead.
	if oldBase == oldHead {
		res, err := m.Diff(ctx, host, owner, name, newBase, newHead, hideWhitespace)
		if err != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff (empty old range) diff: %w", err)
		}
		return StructuredInterdiff{Result: res, Kind: InterdiffClean}, nil
	}

	// Spin up a worktree at newBase and cherry-pick oldBase..oldHead.
	wtPath := filepath.Join(
		filepath.Dir(cloneDir),
		"interdiff-worktrees",
		fmt.Sprintf("%s-%d-%d", filepath.Base(cloneDir), time.Now().UnixNano(), rand.Int()),
	)
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: prepare worktree dir: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = m.git(cleanupCtx, host, wtPath, "cherry-pick", "--abort")
		_, _ = m.git(cleanupCtx, host, cloneDir, "worktree", "remove", "--force", wtPath)
		_ = os.RemoveAll(wtPath)
		_, _ = m.git(cleanupCtx, host, cloneDir, "worktree", "prune")
	}()

	if _, err := m.git(ctx, host, cloneDir, "worktree", "add", "--detach", wtPath, newBase); err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: worktree add: %w", err)
	}

	if _, err := m.git(ctx, host, wtPath, "cherry-pick", oldBase+".."+oldHead); err != nil {
		// Conflict: fall back to raw oldHead..newHead diff.
		res, dErr := m.Diff(ctx, host, owner, name, oldHead, newHead, hideWhitespace)
		if dErr != nil {
			return StructuredInterdiff{}, fmt.Errorf("interdiff: cherry-pick failed (%v) and raw diff failed (%w)", err, dErr)
		}
		return StructuredInterdiff{
			Result: res,
			Kind:   InterdiffConflicted,
			Reason: "cherry-pick of old patchset onto new base did not apply cleanly",
		}, nil
	}

	// Capture the synthetic head SHA; the commit objects are in the
	// shared object store so Diff against the bare clone resolves them.
	syntheticOut, err := m.git(ctx, host, wtPath, "rev-parse", "HEAD")
	if err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: rev-parse synthetic HEAD: %w", err)
	}
	syntheticHead := strings.TrimSpace(string(syntheticOut))
	if syntheticHead == "" {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: empty synthetic HEAD after cherry-pick")
	}

	res, err := m.Diff(ctx, host, owner, name, syntheticHead, newHead, hideWhitespace)
	if err != nil {
		return StructuredInterdiff{}, fmt.Errorf("interdiff: structured synthetic diff: %w", err)
	}
	return StructuredInterdiff{Result: res, Kind: InterdiffClean}, nil
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
