//go:build !unix

package termemu

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// setControllingTTY is a no-op on platforms without Unix session semantics.
// pty.Open already fails with pty.ErrUnsupported on these platforms, so
// Attach never gets far enough to need child session setup.
func setControllingTTY(c *exec.Cmd) {}

// openSlave is never reached on platforms without pty support: pty.Open fails
// first with pty.ErrUnsupported.
func openSlave(path string) (*os.File, error) {
	return nil, pty.ErrUnsupported
}

// setNonblocking is a no-op on platforms where creack/pty is unsupported.
func setNonblocking(f *os.File) error { return nil }
