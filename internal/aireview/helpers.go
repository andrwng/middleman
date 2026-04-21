package aireview

import (
	"io"
	"os/exec"
	"syscall"
)

// readAll drains a reader into a slice. Split out so tests can stub it.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// snippet truncates b to at most n bytes for error messages.
func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "... [truncated]"
}

// setPgid puts the child in its own process group so we can kill the
// whole tree (Claude CLI spawns helper processes) when a question is
// cancelled. unix-only — noop on Windows via a build tag if we ever
// add Windows support.
func setPgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
