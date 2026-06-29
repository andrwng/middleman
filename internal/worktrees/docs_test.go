package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func TestMarkdownFiles(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "t")
	writeFile(t, dir, "README.md", "# r\n")
	writeFile(t, dir, "notes/design.markdown", "# d\n")
	writeFile(t, dir, "src/main.go", "package main\n")
	writeFile(t, dir, ".gitignore", "ignored.md\n")
	writeFile(t, dir, "ignored.md", "# nope\n")
	runGit(t, dir, "add", "README.md", "notes/design.markdown", "src/main.go", ".gitignore")
	runGit(t, dir, "commit", "-m", "init")
	writeFile(t, dir, "draft.mdx", "# wip\n") // untracked, not ignored

	got, err := MarkdownFiles(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"README.md", "draft.mdx", "notes/design.markdown"}, got)
}

func TestMarkdownFilesEmptyPath(t *testing.T) {
	_, err := MarkdownFiles(context.Background(), "")
	require.Error(t, err)
}
