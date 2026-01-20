package termemu

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/creack/pty"
)

// Backend provides the IO connection to the terminal emulator.
// Implementations should be initialized before constructing a Terminal.
type Backend interface {
	io.Reader
	io.Writer
	SetSize(w, h int) error
}

// PTYBackend implements Backend using github.com/creack/pty.
type PTYBackend struct {
	master *os.File
	slave  *os.File
}

// Open creates a new pty pair and returns the slave for external use.
func (p *PTYBackend) Open() (*os.File, error) {
	if p.master != nil {
		return p.slave, nil
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	p.master = master
	p.slave = slave
	return slave, nil
}

// StartCommand starts the command connected to a new PTY master.
func (p *PTYBackend) StartCommand(c *exec.Cmd) error {
	if p.master != nil {
		return errors.New("pty already initialized; start command before using backend")
	}
	if c.Env == nil {
		c.Env = os.Environ()
	}

	found := false
	for i, v := range c.Env {
		if strings.HasPrefix(v, "TERM=") {
			found = true
			c.Env[i] = termStr
			break
		}
	}
	if !found {
		c.Env = append(c.Env, termStr)
	}

	master, err := pty.Start(c)
	if err != nil {
		return err
	}
	p.master = master
	p.slave = nil
	return nil
}

func (p *PTYBackend) Read(b []byte) (int, error) {
	if p.master == nil {
		return 0, io.EOF
	}
	return p.master.Read(b)
}

func (p *PTYBackend) Write(b []byte) (int, error) {
	if p.master == nil {
		return 0, io.ErrClosedPipe
	}
	return p.master.Write(b)
}

func (p *PTYBackend) SetSize(w, h int) error {
	if p.master == nil {
		return nil
	}
	return pty.Setsize(p.master, &pty.Winsize{
		Rows: uint16(h),
		Cols: uint16(w),
		X:    uint16(w * 8),
		Y:    uint16(h * 16),
	})
}

// NoPTYBackend implements Backend using provided IO streams.
type NoPTYBackend struct {
	r io.Reader
	w io.Writer
}

// NewNoPTYBackend returns a backend using the provided reader and writer.
func NewNoPTYBackend(r io.Reader, w io.Writer) *NoPTYBackend {
	return &NoPTYBackend{r: r, w: w}
}

func (b *NoPTYBackend) Read(p []byte) (int, error) {
	if b.r == nil {
		return 0, io.EOF
	}
	return b.r.Read(p)
}

func (b *NoPTYBackend) Write(p []byte) (int, error) {
	if b.w == nil {
		return 0, io.ErrClosedPipe
	}
	return b.w.Write(p)
}

func (b *NoPTYBackend) SetSize(w, h int) error {
	return nil
}

func (b *NoPTYBackend) Close() error {
	var err, err2 error
	if wc, ok := b.w.(io.Closer); ok {
		err = wc.Close()
	}
	if rc, ok := b.r.(io.Closer); ok {
		err2 = rc.Close()
	}
	return errors.Join(err, err2)
}

// TeeBackend duplicates all reads into tee.
type TeeBackend struct {
	backend Backend
	mu      sync.Mutex
	tee     io.Writer
}

// NewTeeBackend returns a TeeBackend wrapping the provided backend.
func NewTeeBackend(backend Backend) *TeeBackend {
	if backend == nil {
		return nil
	}
	if tb, ok := backend.(*TeeBackend); ok {
		return tb
	}
	return &TeeBackend{backend: backend}
}

// SetTee updates the tee writer.
func (t *TeeBackend) SetTee(w io.Writer) {
	t.mu.Lock()
	t.tee = w
	t.mu.Unlock()
}

func (t *TeeBackend) Read(p []byte) (int, error) {
	n, err := t.backend.Read(p)
	if n > 0 {
		t.mu.Lock()
		tee := t.tee
		t.mu.Unlock()
		if tee != nil {
			_, _ = tee.Write(p[:n])
		}
	}
	return n, err
}

func (t *TeeBackend) Write(p []byte) (int, error) {
	return t.backend.Write(p)
}

func (t *TeeBackend) SetSize(w, h int) error {
	return t.backend.SetSize(w, h)
}
