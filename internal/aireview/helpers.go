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

// killProcessGroup SIGKILLs the entire process group led by cmd.Process
// (paired with setPgid). exec.CommandContext's default Cancel only kills
// the leader, which can leave a grandchild (e.g. a hung shell command)
// holding the stdout pipe open so the stream loop / cmd.Wait never
// return. Killing the group closes the pipe and lets the turn finish.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Negative pid => deliver the signal to the whole process group.
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
