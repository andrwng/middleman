package gitclone

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
)

// resolveCacheCap caps the per-clone tree-listing cache so a long-
// running server with many distinct SHAs doesn't grow unboundedly.
// Each entry is the file-path list for one SHA; for the largest repo
// in practice (~6500 files) that's well under 1 MB. 32 entries is
// plenty for "the SHAs reviewers are actively looking at right now."
const resolveCacheCap = 32

type treeCacheKey struct {
	host, owner, name, sha string
}

// resolveCache holds the parsed list of paths for a tree at a given
// SHA. Lookups across multiple basenames within one resolve call
// share the same listing; callers across requests share it too as
// long as the SHA stays in the LRU window.
type resolveCache struct {
	mu      sync.Mutex
	entries map[treeCacheKey]*treeEntry
	order   []treeCacheKey // FIFO eviction; newest at the back
}

type treeEntry struct {
	paths []string
	// byBase is a precomputed basename → full-paths index so each
	// resolve call costs O(1) per filename instead of O(N) over the
	// whole tree.
	byBase map[string][]string
}

func newResolveCache() *resolveCache {
	return &resolveCache{entries: make(map[treeCacheKey]*treeEntry)}
}

func (c *resolveCache) get(k treeCacheKey) *treeEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[k]
}

func (c *resolveCache) put(k treeCacheKey, e *treeEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[k]; ok {
		c.entries[k] = e
		return
	}
	c.entries[k] = e
	c.order = append(c.order, k)
	for len(c.order) > resolveCacheCap {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, evict)
	}
}

// ResolveFilenames resolves bare basenames (e.g. "huma_routes.go")
// to their full repo path at the given SHA. Returns a map from each
// requested name to its unique path; names that have zero or multiple
// matches map to the empty string. Callers can then choose to deep-
// link only the unique matches and leave ambiguous ones as plain
// text, which is the right call for review-prose linkification.
//
// Multi-segment inputs are passed through verified: if "internal/x.go"
// is requested, we check that exact path exists in the tree and
// return it as-is, otherwise return "". This lets the same endpoint
// validate multi-segment refs without a second code path on the
// frontend.
func (m *Manager) ResolveFilenames(
	ctx context.Context,
	host, owner, name, sha string,
	names []string,
) (map[string]string, error) {
	out := make(map[string]string, len(names))
	if len(names) == 0 {
		return out, nil
	}
	if sha == "" {
		return out, fmt.Errorf("resolve filenames: empty sha")
	}

	key := treeCacheKey{host: host, owner: owner, name: name, sha: sha}
	entry := m.resolveCache.get(key)
	if entry == nil {
		dir := m.ClonePath(host, owner, name)
		// -r recurses, --name-only suppresses the mode/type/oid
		// columns. -z is NUL-separated so paths with spaces stay
		// intact.
		raw, err := m.git(ctx, host, dir,
			"ls-tree", "-r", "--name-only", "-z", sha,
		)
		if err != nil {
			return nil, fmt.Errorf("ls-tree %s: %w", sha, err)
		}
		entry = parseTreeListing(raw)
		m.resolveCache.put(key, entry)
	}

	for _, n := range names {
		if n == "" {
			continue
		}
		// Multi-segment: verify the exact path exists.
		if strings.Contains(n, "/") {
			cleaned := strings.TrimPrefix(n, "./")
			if _, ok := entry.byBase[path.Base(cleaned)]; ok {
				// Walk the candidates to check exact match.
				for _, full := range entry.byBase[path.Base(cleaned)] {
					if full == cleaned {
						out[n] = full
						break
					}
				}
			}
			continue
		}
		// Bare filename: link only when the basename has exactly one
		// match. Multiple matches stay ambiguous (the reviewer would
		// land on a 404 or the wrong file otherwise).
		matches := entry.byBase[n]
		if len(matches) == 1 {
			out[n] = matches[0]
		}
	}
	return out, nil
}

func parseTreeListing(raw []byte) *treeEntry {
	e := &treeEntry{byBase: map[string][]string{}}
	if len(raw) == 0 {
		return e
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(splitNUL)
	for sc.Scan() {
		p := sc.Text()
		if p == "" {
			continue
		}
		e.paths = append(e.paths, p)
		base := path.Base(p)
		e.byBase[base] = append(e.byBase[base], p)
	}
	return e
}

func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
