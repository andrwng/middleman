package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/wesm/middleman/internal/worktrees"
)

const (
	// reviewAnchorBudget caps the git work one request may spend
	// re-anchoring. Anchors not reached in time are left out of the
	// response, which the UI reads as "place at the recorded line".
	reviewAnchorBudget = 5 * time.Second
	// reviewAnchorCacheCap bounds the cross-request cache. A resolution
	// for a fixed (anchor, target) pair never changes, so entries are only
	// ever dropped for size.
	reviewAnchorCacheCap = 4096
)

// reviewCommentAnchorMeta reads the anchor fields back out of a
// review_comment event's MetadataJSON (written by
// github.NormalizeReviewCommentEvent).
type reviewCommentAnchorMeta struct {
	Path              string `json:"path"`
	Side              string `json:"side"`
	SubjectType       string `json:"subject_type"`
	Line              int    `json:"line"`
	StartLine         int    `json:"start_line"`
	CommitID          string `json:"commit_id"`
	OriginalLine      int    `json:"original_line"`
	OriginalStartLine int    `json:"original_start_line"`
	OriginalCommitID  string `json:"original_commit_id"`
}

// reviewCommentAnchor is a line range known to exist in Path at CommitID.
// StartLine is 0 for a single-line comment.
type reviewCommentAnchor struct {
	CommitID  string
	Path      string
	Line      int
	StartLine int
}

// resolvedReviewAnchor is where a reviewCommentAnchor lands at a target
// revision. Path, Line and StartLine are meaningful only for
// AnchorCurrent and AnchorMoved.
type resolvedReviewAnchor struct {
	State     worktrees.AnchorState
	Path      string
	Line      int
	StartLine int
}

// sourceAnchor picks the (commit, line) pair a comment can be mapped
// forward from, or reports false when there is nothing to map.
//
// The creation anchor (Original*) is preferred because it is the only
// pair guaranteed to describe one commit: a row is written once, at first
// sync, and when GitHub had already marked the comment outdated by then,
// Line holds the original line while CommitID holds a later commit. Rows
// synced before Original* was recorded fall back to Line/CommitID, which
// agree whenever the comment was still live at first sync.
//
// LEFT-side comments are skipped: their numbers count the base side of the
// diff, not CommitID's tree, so mapping them through CommitID..target would
// answer the wrong question. resolveThreadAnchors skips them for the same
// reason, and the UI keeps placing them as it always has.
func sourceAnchor(m reviewCommentAnchorMeta) (reviewCommentAnchor, bool) {
	if m.Path == "" || m.Side == "LEFT" || strings.EqualFold(m.SubjectType, "file") {
		return reviewCommentAnchor{}, false
	}
	a := reviewCommentAnchor{CommitID: m.CommitID, Path: m.Path, Line: m.Line, StartLine: m.StartLine}
	if m.OriginalCommitID != "" && m.OriginalLine > 0 {
		a = reviewCommentAnchor{
			CommitID:  m.OriginalCommitID,
			Path:      m.Path,
			Line:      m.OriginalLine,
			StartLine: m.OriginalStartLine,
		}
	}
	if a.CommitID == "" || a.Line <= 0 {
		return reviewCommentAnchor{}, false
	}
	if a.StartLine >= a.Line {
		a.StartLine = 0
	}
	return a, true
}

// resolveAnchor maps both ends of a's range through d. When the start no
// longer lands before the end in the same file, the range collapses to its
// end line so the comment stays placed rather than spanning unrelated code.
func resolveAnchor(d worktrees.AnchorDiff, a reviewCommentAnchor) resolvedReviewAnchor {
	end := d.Resolve(a.Path, a.Line)
	if end.State != worktrees.AnchorCurrent && end.State != worktrees.AnchorMoved {
		return resolvedReviewAnchor{State: end.State}
	}
	r := resolvedReviewAnchor{Path: end.Path, Line: end.Line}
	if a.StartLine > 0 {
		start := d.Resolve(a.Path, a.StartLine)
		placed := start.State == worktrees.AnchorCurrent || start.State == worktrees.AnchorMoved
		if placed && start.Path == end.Path && start.Line < end.Line {
			r.StartLine = start.Line
		}
	}
	r.State = worktrees.AnchorMoved
	if r.Path == a.Path && r.Line == a.Line && r.StartLine == a.StartLine {
		r.State = worktrees.AnchorCurrent
	}
	return r
}

type reviewAnchorKey struct {
	target string
	anchor reviewCommentAnchor
}

// reviewAnchorCache memoizes resolutions across requests; the detail view
// re-asks on every poll and scope change. The zero value is ready to use.
type reviewAnchorCache struct {
	mu sync.Mutex
	m  map[reviewAnchorKey]resolvedReviewAnchor
}

func (c *reviewAnchorCache) get(k reviewAnchorKey) (resolvedReviewAnchor, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[k]
	return r, ok
}

func (c *reviewAnchorCache) put(k reviewAnchorKey, r resolvedReviewAnchor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= reviewAnchorCacheCap {
		c.m = make(map[reviewAnchorKey]resolvedReviewAnchor)
	}
	c.m[k] = r
}

// resolve maps each anchor to target inside the git repository at repoPath
// (a bare clone works: both revisions are explicit), running one git diff
// per distinct source commit. target must already be validated as a hex
// object id. Anchors not reached before ctx ends are absent from the
// result.
func (c *reviewAnchorCache) resolve(
	ctx context.Context, repoPath, target string, anchors map[int64]reviewCommentAnchor,
) map[int64]resolvedReviewAnchor {
	out := make(map[int64]resolvedReviewAnchor, len(anchors))
	diffs := map[string]worktrees.AnchorDiff{}
	for id, a := range anchors {
		// The commit id reaches git as argv; see listReviewThreads on why
		// only hex may get there.
		if !symbolRefsSHA.MatchString(a.CommitID) {
			out[id] = resolvedReviewAnchor{State: worktrees.AnchorUnmappable}
			continue
		}
		if strings.EqualFold(a.CommitID, target) {
			out[id] = resolvedReviewAnchor{
				State: worktrees.AnchorCurrent, Path: a.Path, Line: a.Line, StartLine: a.StartLine,
			}
			continue
		}
		key := reviewAnchorKey{target: target, anchor: a}
		if r, ok := c.get(key); ok {
			out[id] = r
			continue
		}
		d, ok := diffs[a.CommitID]
		if !ok {
			d = worktrees.FetchAnchorDiff(ctx, repoPath, a.CommitID, target)
			if ctx.Err() != nil {
				continue
			}
			diffs[a.CommitID] = d
		}
		r := resolveAnchor(d, a)
		// Unmappable here means git could not read a revision, which can
		// heal once the clone fetches it, so it is never cached.
		if r.State != worktrees.AnchorUnmappable {
			c.put(key, r)
		}
		out[id] = r
	}
	return out
}
