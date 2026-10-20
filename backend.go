package termemu

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Backend provides the IO connection to the terminal emulator.
// Implementations should be initialized before constructing a Terminal.
type Backend interface {
	io.Reader
	io.Writer
	io.Closer
	SetSize(w, h int) error
}

// PTYBackend implements Backend using github.com/creack/pty.
// A single pty pair is shared by every command attached to the backend, so
// commands can be run back-to-back through the same terminal state.
type PTYBackend struct {
	mu     sync.Mutex
	master *os.File
	slave  *os.File
	closed bool

	// reuse is signaled by Attach to wake Read when it is waiting for the
	// next command after the previous one exited.
	reuse chan struct{}
}

// Close shuts down the pty pair so a blocked Read returns promptly, and
// prevents any later lazy reopen: subsequent Read/Write/Attach calls fail
// with io.ErrClosedPipe. It is idempotent.
func (p *PTYBackend) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	master, slave := p.master, p.slave
	p.master, p.slave = nil, nil
	reuse := p.reuse
	if reuse == nil {
		reuse = make(chan struct{}, 1)
		p.reuse = reuse
	}
	p.mu.Unlock()

	// Wake a Read that is waiting for the next command so it can observe
	// the closed flag instead of sleeping out its backoff.
	select {
	case reuse <- struct{}{}:
	default:
	}

	var err error
	if master != nil {
		err = master.Close()
	}
	if slave != nil {
		err = errors.Join(err, slave.Close())
	}
	return err
}

// Open creates a new pty pair and returns the slave for external use.
// The pair is created lazily, so it is safe to call this before or after
// constructing a Terminal.
func (p *PTYBackend) Open() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, err
	}
	return p.slave, nil
}

// openLocked creates the pty pair if it does not exist yet. Caller must hold p.mu.
func (p *PTYBackend) openLocked() error {
	if p.closed {
		return io.ErrClosedPipe
	}
	if p.master != nil {
		return nil
	}
	master, slave, err := pty.Open()
	if err != nil {
		return err
	}
	p.master = master
	p.slave = slave
	return nil
}

// masterFile returns the pty master, creating the pair if needed.
func (p *PTYBackend) masterFile() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, err
	}
	return p.master, nil
}

// isClosed reports whether Close has been called. Caller must hold p.mu.
func (p *PTYBackend) isClosedLocked() bool {
	return p.closed
}

// signalReuse wakes a Read that is waiting for the next attached command.
func (p *PTYBackend) signalReuse() {
	p.mu.Lock()
	if p.reuse == nil {
		p.reuse = make(chan struct{}, 1)
	}
	ch := p.reuse
	p.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// waitForReuse waits up to d for Attach to signal a new command.
func (p *PTYBackend) waitForReuse(d time.Duration) {
	p.mu.Lock()
	if p.reuse == nil {
		p.reuse = make(chan struct{}, 1)
	}
	ch := p.reuse
	p.mu.Unlock()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
}

// Attach wires an *exec.Cmd to run inside the PTY.
// The PTY pair is opened lazily on first call and reused on subsequent calls.
// Caller starts the command themselves via cmd.Start().
func (p *PTYBackend) Attach(c *exec.Cmd) error {
	slave, err := p.Open()
	if err != nil {
		return err
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

	c.Stdin = slave
	c.Stdout = slave
	c.Stderr = slave
	// Put the child in its own session with the pty as its controlling
	// terminal. The slave ends up on fd 0 in the child, so Ctty must be 0
	// (an fd number in the child's table, not the parent's).
	setControllingTTY(c)

	// A previous child may have exited, leaving Read waiting for reuse.
	p.signalReuse()
	return nil
}

// Read reads from the pty master. When the current child has exited the master
// returns EIO; because the pty stays valid for the next attached command, Read
// waits for one instead of surfacing that as EOF. After Close, Read returns
// io.ErrClosedPipe instead of retrying.
func (p *PTYBackend) Read(b []byte) (int, error) {
	master, err := p.masterFile()
	if err != nil {
		return 0, err
	}

	backoff := time.Millisecond
	for {
		n, err := master.Read(b)
		if err == nil {
			return n, nil
		}
		if p.isClosed() {
			return 0, io.ErrClosedPipe
		}
		if !errors.Is(err, syscall.EIO) {
			return n, err
		}
		p.waitForReuse(backoff)
		if backoff < maxReuseBackoff {
			backoff *= 2
		}
	}
}

// isClosed reports whether Close has been called.
func (p *PTYBackend) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isClosedLocked()
}

// maxReuseBackoff caps the idle polling interval for a pty with no live child.
const maxReuseBackoff = 250 * time.Millisecond

func (p *PTYBackend) Write(b []byte) (int, error) {
	master, err := p.masterFile()
	if err != nil {
		return 0, err
	}
	return master.Write(b)
}

func (p *PTYBackend) SetSize(w, h int) error {
	master, err := p.masterFile()
	if err != nil {
		return err
	}
	return pty.Setsize(master, &pty.Winsize{
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

// Close forwards to the wrapped backend when it implements io.Closer.
func (t *TeeBackend) Close() error {
	if c, ok := t.backend.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
