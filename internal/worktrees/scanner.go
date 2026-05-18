// Package worktrees discovers git worktrees living under a repo's
// configured local_path and reconciles them with the DB. It is the
// data source for middleman's "local drafts" surface.
package worktrees

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/wesm/middleman/internal/db"
)

// Scan runs `git worktree list --porcelain` at repoPath and returns
// the parsed worktrees. Bare worktrees are filtered out — they are
// not reviewable. A nil slice is returned with no error when the
// repo has no worktrees (e.g. not a git repo yet).
func Scan(ctx context.Context, repoPath string) ([]db.ScannedWorktree, error) {
	if repoPath == "" {
		return nil, fmt.Errorf("repoPath is required")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "list", "--porcelain")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list at %s: %w: %s", repoPath, err, strings.TrimSpace(stderr.String()))
	}
	return ParsePorcelain(out)
}

// ParsePorcelain parses the output of `git worktree list --porcelain`.
//
// Format (from `git help worktree`): records are separated by blank
// lines. Within a record each line is one of:
//
//	worktree <absolute-path>
//	HEAD <sha>
//	branch refs/heads/<name>     (mutually exclusive with `detached`)
//	bare                          (the primary worktree of a bare repo)
//	detached                      (HEAD is not on a branch)
//	locked [reason]
//	prunable [reason]
//
// The `worktree` line always comes first; the rest may appear in any
// order. Records ending with `bare` are filtered out because they
// have no reviewable content.
func ParsePorcelain(raw []byte) ([]db.ScannedWorktree, error) {
	var out []db.ScannedWorktree
	var cur db.ScannedWorktree
	var inRecord bool
	var bareRecord bool

	finalize := func() {
		if !inRecord {
			return
		}
		if !bareRecord && cur.Path != "" {
			out = append(out, cur)
		}
		cur = db.ScannedWorktree{}
		bareRecord = false
		inRecord = false
	}

	for _, rawLine := range bytes.Split(raw, []byte{'\n'}) {
		line := strings.TrimRight(string(rawLine), "\r")
		if line == "" {
			finalize()
			continue
		}

		key, value := splitKV(line)
		switch key {
		case "worktree":
			// New record. If a previous record wasn't terminated by a
			// blank line (last record before EOF), finalize it first.
			finalize()
			inRecord = true
			cur.Path = value
		case "HEAD":
			cur.HeadSHA = value
		case "branch":
			cur.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			cur.IsDetached = true
		case "bare":
			bareRecord = true
		case "locked":
			cur.IsLocked = true
		case "prunable":
			cur.IsPrunable = true
		default:
			// Unknown keys are ignored; future git versions may add fields.
		}
	}
	finalize()
	return out, nil
}

func splitKV(line string) (key, value string) {
	idx := strings.IndexByte(line, ' ')
	if idx < 0 {
		return line, ""
	}
	return line[:idx], line[idx+1:]
}
