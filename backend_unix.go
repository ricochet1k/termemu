//go:build unix

package termemu

import (
	"os/exec"
	"syscall"
)

// setControllingTTY makes the pty slave (wired to c.Stdin/Stdout/Stderr)
// the child's controlling terminal in a new session.
func setControllingTTY(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
}
