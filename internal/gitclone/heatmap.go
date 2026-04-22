package gitclone

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// HeatmapCell is one (commit, file) entry describing how much that
// commit touched that file. Deletions/Additions come from
// `git log --numstat`; both can be 0 for binary files (git emits "-").
type HeatmapCell struct {
	CommitSHA string
	Path      string
	Additions int
	Deletions int
	// Binary is true when git reported "-\t-" for the numstat columns.
	Binary bool
}

// HeatmapCommit pairs a commit SHA with its subject for the column
// header in the UI.
type HeatmapCommit struct {
	SHA   string
	Title string
}

// HeatmapData bundles the commit list and every (commit, file) cell
// across the PR's first-parent range. The UI turns this into a grid.
type HeatmapData struct {
	Commits []HeatmapCommit
	Cells   []HeatmapCell
}

// Heatmap walks `mergeBase..headSHA` along first-parent and returns
// one row per (commit, changed file). Uses a single `git log
// --numstat` invocation — cheap enough that callers don't need to
// cache, though the result is immutable per (base, head) pair.
func (m *Manager) Heatmap(
	ctx context.Context,
	host, owner, name, mergeBase, headSHA string,
) (HeatmapData, error) {
	dir := m.ClonePath(host, owner, name)

	// %H = sha, %s = subject. --numstat prints one line per changed
	// file following the commit header, then a blank line between
	// commits. %x00 is git's own NUL escape — Go's exec rejects
	// literal NUL bytes in argv, so we let git emit them on stdout.
	args := []string{
		"log", "--first-parent", "--reverse", "--numstat",
		"--format=commit%x00%H%x00%s",
	}
	if mergeBase == emptyTreeSHA {
		args = append(args, headSHA)
	} else {
		args = append(args, mergeBase+".."+headSHA)
	}

	out, err := m.git(ctx, host, dir, args...)
	if err != nil {
		return HeatmapData{}, fmt.Errorf("heatmap git log: %w", err)
	}

	var data HeatmapData
	var currentSHA string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "commit\x00") {
			parts := strings.SplitN(line, "\x00", 3)
			if len(parts) != 3 {
				return HeatmapData{}, fmt.Errorf("unexpected log header: %q", line)
			}
			currentSHA = parts[1]
			data.Commits = append(data.Commits, HeatmapCommit{
				SHA:   currentSHA,
				Title: parts[2],
			})
			continue
		}
		// numstat: "<adds>\t<dels>\t<path>"; "-" for binary.
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		cell := HeatmapCell{CommitSHA: currentSHA, Path: fields[2]}
		if fields[0] == "-" && fields[1] == "-" {
			cell.Binary = true
		} else {
			if v, err := strconv.Atoi(fields[0]); err == nil {
				cell.Additions = v
			}
			if v, err := strconv.Atoi(fields[1]); err == nil {
				cell.Deletions = v
			}
		}
		data.Cells = append(data.Cells, cell)
	}
	return data, scanner.Err()
}
