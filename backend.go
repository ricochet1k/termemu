package termemu

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// Backend abstracts PTY operations so we can swap implementations.
type Backend interface {
	Open() (master *os.File, slave *os.File, err error)
	// Start should start the command connected to a pty and return the master
	// file for the spawned command. slave may be nil for some implementations.
	Start(c *exec.Cmd) (master *os.File, slave *os.File, err error)
	Setsize(master *os.File, w, h int) error
}

// PTYBackend implements Backend using github.com/creack/pty.
type PTYBackend struct{}

func (PTYBackend) Open() (master *os.File, slave *os.File, err error) {
	return pty.Open()
}

func (PTYBackend) Start(c *exec.Cmd) (master *os.File, slave *os.File, err error) {
	m, err := pty.Start(c)
	if err != nil {
		return nil, nil, err
	}
	// pty.Start hides the slave from us; return master and nil slave.
	return m, nil, nil
}

func (PTYBackend) Setsize(master *os.File, w, h int) error {
	return pty.Setsize(master, &pty.Winsize{
		Rows: uint16(h),
		Cols: uint16(w),
		X:    uint16(w * 8),
		Y:    uint16(h * 16),
	})
}

// NoPTYBackend implements a minimal backend for environments where a real
// pty is not available. It uses os.Pipe for basic io; Setsize is a no-op.
type NoPTYBackend struct{}

func (NoPTYBackend) Open() (master *os.File, slave *os.File, err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	// return (master=read, slave=write) so writes to slave appear on master
	return r, w, nil
}

func (NoPTYBackend) Start(c *exec.Cmd) (master *os.File, slave *os.File, err error) {
	// Start with a single pipe for stdout+stderr; stdin will be a write end
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	c.Stdin = nil
	c.Stdout = w
	c.Stderr = w

	if err := c.Start(); err != nil {
		return nil, nil, err
	}

	return r, w, nil
}

func (NoPTYBackend) Setsize(master *os.File, w, h int) error {
	return nil
}
