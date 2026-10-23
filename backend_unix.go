//go:build unix

package termemu

import (
	"os"
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

// openSlave reopens the pty slave device at path. O_NOCTTY keeps this process
// from adopting the pty as its controlling terminal while reopening it.
func openSlave(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
}
