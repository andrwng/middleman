package worktrees

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wesm/middleman/internal/gitclone"
)

// ChangedFile is one entry in a worktree's change set.
// Additions/Deletions are 0 for binary files; IsBinary disambiguates.
type ChangedFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path"`
	Status    string `json:"status"` // added | modified | deleted | renamed | copied
	IsBinary  bool   `json:"is_binary"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// BaseRef describes which commit a worktree's working-tree diff is
// computed against. Ref is the symbolic ref that was matched (e.g.
// "origin/main") or empty when no candidate ref resolved; in the
// latter case Fallback is true and SHA is the worktree's HEAD.
type BaseRef struct {
	Ref      string `json:"ref"`
	SHA      string `json:"sha"`
	Fallback bool   `json:"fallback"`
}

// ChangeSet is the full picture of what a worktree has changed
// relative to its base: which base was resolved, and the file list.
type ChangeSet struct {
	Base  BaseRef       `json:"base"`
	Files []ChangedFile `json:"files"`
}

// candidateBaseRefs are tried in order. The first one that resolves
// in the worktree wins. Mirrors the convention captured in the
// design memo: default-branch tracking refs, plus a few common
// non-`main` names.
var candidateBaseRefs = []string{
	"origin/main",
	"origin/master",
	"origin/develop",
	"origin/dev",
}

// ResolveBase tries each candidate ref against the worktree's clone
// and returns the first that resolves, along with the merge-base
// against HEAD. When no candidate resolves, returns a BaseRef
// pointing at HEAD with Fallback=true.
//
// If overrideRef is non-empty, it is tried first; only on failure
// does the auto-detect candidate list run.
func ResolveBase(ctx context.Context, worktreePath, overrideRef string) (BaseRef, error) {
	if worktreePath == "" {
		return BaseRef{}, fmt.Errorf("worktreePath is required")
	}
	refs := candidateBaseRefs
	if overrideRef != "" {
		refs = append([]string{overrideRef}, candidateBaseRefs...)
	}
	for _, ref := range refs {
		// `git rev-parse --verify <ref>` exits non-zero if missing.
		if _, err := gitCmd(ctx, worktreePath, "rev-parse", "--verify", "--quiet", ref); err != nil {
			continue
		}
		mbOut, err := gitCmd(ctx, worktreePath, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		return BaseRef{
			Ref: ref,
			SHA: strings.TrimSpace(string(mbOut)),
		}, nil
	}
	// No remote base found — fall back to HEAD.
	headOut, err := gitCmd(ctx, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return BaseRef{}, fmt.Errorf("rev-parse HEAD: %w", err)
	}
	return BaseRef{
		SHA:      strings.TrimSpace(string(headOut)),
		Fallback: true,
	}, nil
}

// ChangedFilesAgainstBase resolves the worktree's base and returns
// the file change list between that base and the working tree —
// the "full draft" view (committed work + staged + unstaged +
// untracked).
//
// When no base ref is found in the worktree, falls back to HEAD vs
// working tree. Untracked files are surfaced from `git ls-files
// --others` since `git diff` ignores them by default.
func ChangedFilesAgainstBase(
	ctx context.Context, worktreePath, overrideRef string,
) (ChangeSet, error) {
	base, err := ResolveBase(ctx, worktreePath, overrideRef)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("resolve base: %w", err)
	}
	files, err := changedFilesVsRef(ctx, worktreePath, base.SHA)
	if err != nil {
		return ChangeSet{}, err
	}
	untracked, err := listUntracked(ctx, worktreePath)
	if err != nil {
		return ChangeSet{}, err
	}
	nested, err := nestedWorktreeRelPaths(ctx, worktreePath)
	if err != nil {
		// Don't fail the whole operation if the nested scan errors;
		// just skip filtering and accept some noise.
		nested = nil
	}
	for _, path := range untracked {
		if isUnderAnyWorktree(path, nested) {
			continue
		}
		files = append(files, ChangedFile{
			Path:    path,
			OldPath: path,
			Status:  "added",
		})
	}
	return ChangeSet{Base: base, Files: files}, nil
}

// nestedWorktreeRelPaths enumerates git worktrees that live under
// parent (any sibling worktree this one isn't itself) and returns
// their paths relative to parent. Used to filter `ls-files --others`
// output so a nested worktree's directory doesn't show up as an
// untracked "added" file in its parent's change list.
//
// Canonicalises both sides via EvalSymlinks before comparing —
// `git worktree list` reports canonical paths, and the caller may
// pass an aliased path (e.g. macOS /var/... vs /private/var/...).
func nestedWorktreeRelPaths(ctx context.Context, parent string) ([]string, error) {
	scanned, err := Scan(ctx, parent)
	if err != nil {
		return nil, err
	}
	canonParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		canonParent = parent
	}
	out := make([]string, 0, len(scanned))
	for _, w := range scanned {
		canonW := w.Path
		if resolved, err := filepath.EvalSymlinks(w.Path); err == nil {
			canonW = resolved
		}
		if canonW == canonParent {
			continue
		}
		rel, err := filepath.Rel(canonParent, canonW)
		if err != nil {
			continue
		}
		// "../something" means the worktree is a sibling, not nested.
		if strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// isUnderAnyWorktree returns true if path is inside one of the
// nested worktree directories. Tolerates the trailing slash that
// `git ls-files --others` adds to directory entries.
func isUnderAnyWorktree(path string, nestedRels []string) bool {
	clean := strings.TrimRight(path, "/")
	for _, n := range nestedRels {
		if clean == n {
			return true
		}
		if strings.HasPrefix(clean, n+"/") {
			return true
		}
	}
	return false
}

// ListCommits returns commits in the worktree between baseSHA
// (exclusive) and HEAD (inclusive), newest first, following only
// the first-parent chain. Mirrors gitclone.Manager.ListCommits's
// behavior so callers can use the same gitclone.Commit shape for
// PR and worktree review surfaces.
func ListCommits(
	ctx context.Context, worktreePath, baseSHA string,
) ([]gitclone.Commit, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath is required")
	}
	if baseSHA == "" {
		return nil, fmt.Errorf("baseSHA is required")
	}
	out, err := gitCmd(ctx, worktreePath,
		"log", "-z", "--first-parent",
		"--format=%H%x00%an%x00%aI%x00%B",
		baseSHA+"..HEAD",
	)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	s := strings.TrimSuffix(string(out), "\x00")
	if s == "" {
		return nil, nil
	}
	fields := strings.Split(s, "\x00")
	if len(fields)%4 != 0 {
		return nil, fmt.Errorf("unexpected git log field count: %d", len(fields))
	}
	commits := make([]gitclone.Commit, 0, len(fields)/4)
	for i := 0; i < len(fields); i += 4 {
		t, err := time.Parse(time.RFC3339, fields[i+2])
		if err != nil {
			return nil, fmt.Errorf("parse commit date %q: %w", fields[i+2], err)
		}
		msg := strings.TrimRight(fields[i+3], "\n")
		subject, body, _ := strings.Cut(msg, "\n")
		commits = append(commits, gitclone.Commit{
			SHA:        fields[i],
			AuthorName: fields[i+1],
			AuthoredAt: t,
			Message:    subject,
			Body:       strings.TrimSpace(body),
		})
	}
	return commits, nil
}

// listUntracked returns paths of files git is aware of but not
// tracking — modulo .gitignore. Used to surface fresh files in
// the "vs base" view since `git diff` ignores them.
func listUntracked(ctx context.Context, worktreePath string) ([]string, error) {
	out, err := gitCmd(ctx, worktreePath,
		"ls-files", "--others", "--exclude-standard", "-z",
	)
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w", err)
	}
	parts := bytes.Split(out, []byte{0})
	var paths []string
	for _, p := range parts {
		s := string(p)
		if s == "" {
			continue
		}
		paths = append(paths, s)
	}
	return paths, nil
}

// ListChangedFiles returns the files modified in the worktree
// relative to its HEAD — committed changes are NOT included; only
// the live, uncommitted state (staged + unstaged + untracked-as-far-as-
// git-knows). This is the "what's currently in flight here" view.
//
// Returns a nil slice when the worktree is clean.
func ListChangedFiles(ctx context.Context, worktreePath string) ([]ChangedFile, error) {
	return changedFilesVsRef(ctx, worktreePath, "HEAD")
}

// changedFilesVsRef does the actual git work: two calls (--raw -z
// for status letters and rename detection, --numstat -z for line
// counts) cross-referenced by path. Entries present in --raw but
// missing from --numstat (e.g. binary files) are returned with
// IsBinary set. ref can be a SHA, branch name, or symbolic ref.
func changedFilesVsRef(ctx context.Context, worktreePath, ref string) ([]ChangedFile, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath is required")
	}
	if ref == "" {
		return nil, fmt.Errorf("ref is required")
	}
	rawOut, err := gitCmd(ctx, worktreePath,
		"diff", ref, "--raw", "-z", "-M", "-C", "--find-copies-harder",
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --raw: %w", err)
	}
	files := parseRawZ(rawOut)

	numstatOut, err := gitCmd(ctx, worktreePath,
		"diff", ref, "--numstat", "-z",
	)
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat: %w", err)
	}
	counts := parseNumstatZ(numstatOut)

	for i := range files {
		c, ok := counts[files[i].Path]
		if !ok {
			c, ok = counts[files[i].OldPath]
		}
		if ok {
			files[i].Additions = c.adds
			files[i].Deletions = c.dels
			files[i].IsBinary = c.binary
		} else {
			// Path missing from numstat is unusual; treat as binary.
			files[i].IsBinary = true
		}
	}
	return files, nil
}

func gitCmd(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type numstatRow struct {
	adds, dels int
	binary     bool
}

// parseNumstatZ parses `git diff --numstat -z` output. Format:
//
//	<adds>\t<dels>\t<path>\0
//
// Renames use a 3-NUL form: <adds>\t<dels>\t\0<oldpath>\0<newpath>\0.
// We key by new path so callers correlate against --raw results.
func parseNumstatZ(data []byte) map[string]numstatRow {
	out := make(map[string]numstatRow)
	parts := bytes.Split(data, []byte{0})
	for i := 0; i < len(parts); i++ {
		entry := string(parts[i])
		if entry == "" {
			continue
		}
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		adds, addsRaw := fields[0], fields[0]
		dels, delsRaw := fields[1], fields[1]
		path := fields[2]
		row := numstatRow{}
		if addsRaw == "-" && delsRaw == "-" {
			row.binary = true
		} else {
			row.adds, _ = strconv.Atoi(adds)
			row.dels, _ = strconv.Atoi(dels)
		}
		if path == "" {
			// Rename: next two NULs are oldpath, newpath.
			if i+2 >= len(parts) {
				break
			}
			_ = parts[i+1] // oldpath, not needed for keying
			newPath := string(parts[i+2])
			out[newPath] = row
			i += 2
			continue
		}
		out[path] = row
	}
	return out
}

// parseRawZ parses `git diff --raw -z` output. Each entry header line
// begins with ':' and is followed by one or two NUL-terminated paths
// (two for renames/copies). Format:
//
//	:oldmode newmode oldhash newhash status\0path\0
//	:...                          R100\0oldpath\0newpath\0
func parseRawZ(data []byte) []ChangedFile {
	parts := bytes.Split(data, []byte{0})
	var files []ChangedFile

	i := 0
	for i < len(parts) {
		part := string(parts[i])
		if !strings.HasPrefix(part, ":") {
			i++
			continue
		}
		fields := strings.Fields(part)
		if len(fields) < 5 {
			i++
			continue
		}
		statusRaw := fields[4]
		status, isRename := rawStatusToString(statusRaw)

		i++
		if i >= len(parts) {
			break
		}
		path := string(parts[i])

		var oldPath string
		if isRename {
			oldPath = path
			i++
			if i >= len(parts) {
				break
			}
			path = string(parts[i])
		}
		if oldPath == "" {
			oldPath = path
		}

		files = append(files, ChangedFile{
			Path:    path,
			OldPath: oldPath,
			Status:  status,
		})
		i++
	}
	return files
}

func rawStatusToString(s string) (status string, isRenameOrCopy bool) {
	if len(s) == 0 {
		return "modified", false
	}
	switch s[0] {
	case 'A':
		return "added", false
	case 'D':
		return "deleted", false
	case 'M':
		return "modified", false
	case 'R':
		return "renamed", true
	case 'C':
		return "copied", true
	case 'T':
		return "modified", false
	default:
		return "modified", false
	}
}
