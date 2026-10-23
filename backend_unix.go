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

// setNonblocking puts the pty master into non-blocking mode. os.File then
// reads through the runtime poller, which is what makes SetReadDeadline able
// to wake an in-flight read (needed by Terminal.Pause). The slave stays
// blocking for the child process.
func setNonblocking(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := rc.Control(func(fd uintptr) {
		setErr = syscall.SetNonblock(int(fd), true)
	}); err != nil {
		return err
	}
	return setErr
}
