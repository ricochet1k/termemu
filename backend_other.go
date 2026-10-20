//go:build !unix

package termemu

import "os/exec"

// setControllingTTY is a no-op on platforms without Unix session semantics.
// pty.Open already fails with pty.ErrUnsupported on these platforms, so
// Attach never gets far enough to need child session setup.
func setControllingTTY(c *exec.Cmd) {}
