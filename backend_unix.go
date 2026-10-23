//go:build unix

package termemu

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
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

// makePollableFile duplicates f, makes the duplicate non-blocking, and wraps
// it with os.NewFile. os.NewFile detects O_NONBLOCK and registers the fd with
// the runtime poller. Toggling O_NONBLOCK on the original descriptor is not
// enough: on Darwin the original wrapper is not registered, so an in-flight
// read never observes SetReadDeadline. The caller keeps ownership of f; the
// returned file must be closed separately.
func makePollableFile(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	pollable := os.NewFile(uintptr(fd), f.Name())
	if pollable == nil {
		_ = syscall.Close(fd)
		return nil, syscall.EINVAL
	}
	return pollable, nil
}

// setPTYSize applies a window size without using (*os.File).Fd(), which would
// switch the master back to blocking mode and break Pause wakeups.
func setPTYSize(f *os.File, w, h int) error {
	ws := struct {
		Rows, Cols, Xpixel, Ypixel uint16
	}{uint16(h), uint16(w), uint16(w * 8), uint16(h * 16)}

	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
