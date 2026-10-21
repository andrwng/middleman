package testutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wesm/middleman/internal/db"
	"github.com/wesm/middleman/internal/gitenv"
)

// LargeDiffPRNumber is the acme/widgets PR that SetupLargeDiffRepo wires to
// the generated large diff.
const LargeDiffPRNumber = 2

// Shape of the generated large diff. Exported so tests can assert the
// totals the /files summary reports.
const (
	LargeDiffSrcFiles      = 700 // src/pkg<i%20>/file<i>.go, added
	LargeDiffSrcLines      = 80  // lines per src file
	LargeDiffGenFiles      = 40  // gen/file<i>.pb.go, added
	LargeDiffGenLines      = 400 // lines per gen file
	LargeDiffDeletedPath   = "legacy/schema.sql"
	LargeDiffDeletedLines  = 2000 // lines in the deleted file
	LargeDiffModifiedPath  = "internal/big_table.go"
	LargeDiffModifiedLines = 1800 // lines in the modified file (both sides)
)

// LargeDiffResult holds the SHAs of the generated large diff.
type LargeDiffResult struct {
	BaseSHA string
	HeadSHA string
}

// LargeDiffSrcPath is the path of the i-th generated src file.
func LargeDiffSrcPath(i int) string {
	return fmt.Sprintf("src/pkg%d/file%d.go", i%20, i)
}

// LargeDiffGenPath is the path of the i-th generated gen file.
func LargeDiffGenPath(i int) string {
	return fmt.Sprintf("gen/file%02d.pb.go", i)
}

// LargeDiffModifiedChanged is the number of lines the head side of the
// modified file rewrites (every even-numbered line). Each shows as one
// addition and one deletion.
const LargeDiffModifiedChanged = LargeDiffModifiedLines / 2

// LargeDiffTotals returns the expected file count, additions and deletions.
func LargeDiffTotals() (files, additions, deletions int) {
	files = LargeDiffSrcFiles + LargeDiffGenFiles + 2
	additions = LargeDiffSrcFiles*LargeDiffSrcLines +
		LargeDiffGenFiles*LargeDiffGenLines + LargeDiffModifiedChanged
	deletions = LargeDiffDeletedLines + LargeDiffModifiedChanged
	return files, additions, deletions
}

// SetupLargeDiffRepo adds a generated large diff to the acme/widgets bare
// repo that SetupDiffRepo created under tmpDir and wires it to PR #2:
//   - 700 added files of 80 lines under src/pkg<i%20>/file<i>.go
//   - 40 added files of 400 lines under gen/
//   - one deleted 2000-line file (legacy/schema.sql)
//   - one modified 1800-line file (internal/big_table.go), every other
//     line rewritten
//
// Both commits are written in one `git fast-import` run straight into the
// bare repo, so generating ~76k lines costs one process instead of a work
// tree plus hundreds of file writes. SetupDiffRepo must run first.
func SetupLargeDiffRepo(
	ctx context.Context,
	tmpDir string,
	d *db.DB,
	parentSHA string,
) (*LargeDiffResult, error) {
	barePath := filepath.Join(
		tmpDir, "clones", "github.com", "acme", "widgets.git")
	if _, err := os.Stat(barePath); err != nil {
		return nil, fmt.Errorf("bare repo (run SetupDiffRepo first): %w", err)
	}

	stream := largeDiffFastImport(parentSHA)
	if err := gitStdin(barePath, stream, "fast-import", "--quiet"); err != nil {
		return nil, fmt.Errorf("fast-import: %w", err)
	}

	baseSHA, err := revParse(barePath, "refs/heads/large-diff-base")
	if err != nil {
		return nil, fmt.Errorf("rev-parse large base: %w", err)
	}
	headSHA, err := revParse(barePath, "refs/heads/feature/large-diff")
	if err != nil {
		return nil, fmt.Errorf("rev-parse large head: %w", err)
	}

	repoID, err := d.UpsertRepo(ctx, "github.com", "acme", "widgets")
	if err != nil {
		return nil, fmt.Errorf("upsert repo: %w", err)
	}
	if err := d.UpdateDiffSHAs(
		ctx, repoID, LargeDiffPRNumber, headSHA, baseSHA, baseSHA); err != nil {
		return nil, fmt.Errorf("update diff SHAs: %w", err)
	}
	if err := d.UpdatePlatformSHAs(
		ctx, repoID, LargeDiffPRNumber, headSHA, baseSHA); err != nil {
		return nil, fmt.Errorf("update platform SHAs: %w", err)
	}

	return &LargeDiffResult{BaseSHA: baseSHA, HeadSHA: headSHA}, nil
}

// largeDiffFastImport builds the fast-import stream: a base commit on top
// of parentSHA adding the soon-to-be deleted and modified files, then a
// head commit with every change.
func largeDiffFastImport(parentSHA string) []byte {
	var b bytes.Buffer
	// 2026-03-28T12:00:00Z, matching the other fixture commits.
	const when = "1774699200 +0000"

	commit := func(ref, from, msg string) {
		fmt.Fprintf(&b, "commit %s\n", ref)
		fmt.Fprintf(&b, "author Test <test@example.com> %s\n", when)
		fmt.Fprintf(&b, "committer Test <test@example.com> %s\n", when)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		fmt.Fprintf(&b, "from %s\n", from)
	}
	file := func(path, content string) {
		fmt.Fprintf(&b, "M 100644 inline %s\ndata %d\n%s\n",
			path, len(content), content)
	}

	commit("refs/heads/large-diff-base", parentSHA, "chore: add legacy tables")
	file(LargeDiffDeletedPath, lines(LargeDiffDeletedLines, func(i int) string {
		return fmt.Sprintf(
			"INSERT INTO legacy_rows VALUES (%d, 'legacy-row-%04d');", i, i)
	}))
	file(LargeDiffModifiedPath, bigTable(false))
	b.WriteString("\n")

	commit("refs/heads/feature/large-diff", "refs/heads/large-diff-base",
		"feat: regenerate everything")
	fmt.Fprintf(&b, "D %s\n", LargeDiffDeletedPath)
	file(LargeDiffModifiedPath, bigTable(true))
	for i := range LargeDiffGenFiles {
		file(LargeDiffGenPath(i), lines(LargeDiffGenLines, func(n int) string {
			return fmt.Sprintf("var Gen%02dField%03d = %d // generated", i, n, i*1000+n)
		}))
	}
	for i := range LargeDiffSrcFiles {
		file(LargeDiffSrcPath(i), lines(LargeDiffSrcLines, func(n int) string {
			if n == 1 {
				return fmt.Sprintf("package pkg%d", i%20)
			}
			return fmt.Sprintf("var file%dValue%02d = %d", i, n, i*100+n)
		}))
	}
	b.WriteString("\n")
	return b.Bytes()
}

// bigTable is the modified file. The head side rewrites every even line.
func bigTable(head bool) string {
	return lines(LargeDiffModifiedLines, func(i int) string {
		if head && i%2 == 0 {
			return fmt.Sprintf("var tableEntry%04d = %d // updated", i, i*2)
		}
		return fmt.Sprintf("var tableEntry%04d = %d", i, i)
	})
}

// lines joins n generated lines (numbered from 1), newline-terminated.
func lines(n int, gen func(i int) string) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		sb.WriteString(gen(i))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// gitStdin runs git in dir with stdin, using the same environment
// scrubbing as git().
func gitStdin(dir string, stdin []byte, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitenv.StripAll(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"git %s: %w: %s", args[0], err, stderr.String())
	}
	return nil
}
