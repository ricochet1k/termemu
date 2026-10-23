package termemu

import (
	"errors"
	"fmt"
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
	// slavePath is the device path of slave, used to reopen it when a
	// previous child's exit revoked it (see refreshSlaveLocked).
	slavePath string
	closed    bool

	// cmd is the most recently attached command, used by PID and Wait.
	cmd *exec.Cmd

	// reuse is signaled by Attach to wake Read when it is waiting for the
	// next command after the previous one exited.
	reuse chan struct{}

	// AutoEnv lists the environment variables Attach injects into commands,
	// in "KEY=VALUE" form. nil applies the defaults (TERM and COLORTERM);
	// set it to an empty non-nil slice to inject nothing, or to a custom
	// list to override the defaults and add your own variables. Injected
	// values replace existing entries with the same key in cmd.Env.
	AutoEnv []string
}

// defaultAutoEnv is applied by Attach when PTYBackend.AutoEnv is nil.
var defaultAutoEnv = []string{termStr, "COLORTERM=truecolor"}

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
	p.slavePath = ""
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
// constructing a Terminal. It returns an error when the backend was created
// by Adopt with a nil slave, because there is nothing to return or attach.
func (p *PTYBackend) Open() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, err
	}
	if p.slave == nil {
		return nil, errNoAdoptedSlave
	}
	return p.refreshSlaveLocked()
}

// Adopt adopts an already-open pty pair. master is required; slave may be
// nil when only the master is available. The backend takes ownership of the
// files (Close closes them). Call before constructing the Terminal or opening
// the pty backend. With slave == nil, Attach is unavailable and Read does not
// wait for reuse on EOF/EIO.
//
// Use os.NewFile(fd, "pts") to wrap raw pty file descriptors before calling
// Adopt.
func (p *PTYBackend) Adopt(master, slave *os.File) error {
	if master == nil {
		return errors.New("termemu: PTYBackend.Adopt: master is nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	if p.master != nil || p.slave != nil {
		return errors.New("termemu: PTYBackend.Adopt: backend already has a pty")
	}

	p.master = master
	p.slave = slave
	if slave != nil {
		p.slavePath = slave.Name()
	}
	return nil
}

// AdoptMaster is Adopt(master, nil). With no slave, Open and Attach are
// unavailable and Read surfaces EOF/EIO instead of waiting for reuse.
func (p *PTYBackend) AdoptMaster(master *os.File) error {
	return p.Adopt(master, nil)
}

// errNoAdoptedSlave is returned by Open and Attach for a backend adopted with
// only a master.
var errNoAdoptedSlave = errors.New("termemu: PTYBackend has no slave (created by Adopt with a nil slave)")

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
	p.slavePath = slave.Name()
	return nil
}

// readFile returns the pty master and reports whether the backend has a slave
// (and therefore supports Attach/reuse). It creates the pair if needed.
func (p *PTYBackend) readFile() (master *os.File, hasSlave bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, false, err
	}
	return p.master, p.slave != nil, nil
}

// refreshSlaveLocked returns the slave to wire into the next command,
// reopening it if a previous child's exit revoked the cached descriptor. On
// Darwin a session-leader child that used the pty as its controlling terminal
// revokes the parent's slave descriptors when it exits; the master survives
// that, so the pair itself is still shared. Caller must hold p.mu, and p.slave
// must be non-nil.
func (p *PTYBackend) refreshSlaveLocked() (*os.File, error) {
	if _, err := p.slave.Stat(); err == nil {
		return p.slave, nil
	}
	slave, err := openSlave(p.slavePath)
	if err != nil {
		return nil, fmt.Errorf("reopen pty slave: %w", err)
	}
	_ = p.slave.Close()
	p.slave = slave
	return slave, nil
}

// attachSlave returns the slave to wire into the next command. Caller must
// not hold p.mu.
func (p *PTYBackend) attachSlave() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.openLocked(); err != nil {
		return nil, err
	}
	if p.slave == nil {
		return nil, errNoAdoptedSlave
	}
	return p.refreshSlaveLocked()
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
	slave, err := p.attachSlave()
	if err != nil {
		return err
	}

	if c.Env == nil {
		c.Env = os.Environ()
	}
	autoEnv := p.AutoEnv
	if autoEnv == nil {
		autoEnv = defaultAutoEnv
	}
	for _, kv := range autoEnv {
		c.Env = setEnv(c.Env, kv)
	}

	c.Stdin = slave
	c.Stdout = slave
	c.Stderr = slave
	// Put the child in its own session with the pty as its controlling
	// terminal. The slave ends up on fd 0 in the child, so Ctty must be 0
	// (an fd number in the child's table, not the parent's).
	setControllingTTY(c)

	p.mu.Lock()
	p.cmd = c
	p.mu.Unlock()

	// A previous child may have exited, leaving Read waiting for reuse.
	p.signalReuse()
	return nil
}

// PID returns the process ID of the most recently attached command, or 0 if
// no command has been attached or it has not been started yet. It is safe to
// call from any goroutine.
func (p *PTYBackend) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Wait waits for the most recently attached command to exit and returns the
// error from (*exec.Cmd).Wait. It errors if no command has been attached.
//
// The caller must not also call cmd.Wait() on the attached command: like
// os/exec, only one Wait may consume the process result. A second call to
// PTYBackend.Wait returns an "exec: Wait was already called" error. Wait may
// be called after Close (the child still needs reaping) and after a later
// Attach, in which case it waits for that newer command.
func (p *PTYBackend) Wait() error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		return errors.New("PTYBackend: no command attached")
	}
	return cmd.Wait()
}

// setEnv sets KEY=VALUE in env, replacing an existing entry with the same
// key, and returns the (possibly reallocated) slice.
func setEnv(env []string, kv string) []string {
	key, _, ok := strings.Cut(kv, "=")
	if !ok || key == "" {
		return env
	}
	for i, existing := range env {
		if k, _, _ := strings.Cut(existing, "="); k == key {
			env[i] = kv
			return env
		}
	}
	return append(env, kv)
}

// Read reads from the pty master. When the current child has exited the
// master returns syscall.EIO on Linux and io.EOF on some platforms (Darwin);
// because the pty stays valid for the next attached command, Read waits for
// one instead of surfacing that as EOF. That reuse behavior requires a slave:
// for an adopted master-only pty, Read performs a plain master read and
// surfaces EOF/EIO so the read loop can exit. After Close, Read returns
// io.ErrClosedPipe instead of retrying. NoPTYBackend keeps EOF terminal.
func (p *PTYBackend) Read(b []byte) (int, error) {
	master, hasSlave, err := p.readFile()
	if err != nil {
		return 0, err
	}
	if !hasSlave {
		return master.Read(b)
	}
	return readRetryingReuse(master, b, p.isClosed, p.waitForReuse)
}

// readRetryingReuse reads from r, treating a child exit (syscall.EIO on Linux,
// io.EOF on some platforms) as "wait for the next attached command" rather
// than end of stream. It stops and reports io.ErrClosedPipe once closed reports
// true, or returns any unrelated read error. Split out from Read so the retry
// policy (in particular EOF handling) can be unit tested without a real pty.
func readRetryingReuse(r io.Reader, b []byte, closed func() bool, waitForReuse func(time.Duration)) (int, error) {
	backoff := time.Millisecond
	for {
		n, err := r.Read(b)
		if err == nil {
			return n, nil
		}
		if n > 0 {
			// Deliver any data that accompanied the error; the next Read
			// will observe the child exit.
			return n, nil
		}
		if closed() {
			return 0, io.ErrClosedPipe
		}
		if !errors.Is(err, syscall.EIO) && !errors.Is(err, io.EOF) {
			return n, err
		}
		waitForReuse(backoff)
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

// Master returns the pty master file. The backend still owns it; do not
// close it while handing it off. It returns nil if the pty cannot be opened
// (for example after Close).
func (p *PTYBackend) Master() *os.File {
	master, err := p.masterFile()
	if err != nil {
		return nil
	}
	return master
}

// Slave returns the pty slave file, or nil when the backend was adopted with
// only a master (see AdoptMaster). The backend still owns it; do not close it
// while handing it off.
func (p *PTYBackend) Slave() *os.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.slave
}

// SetReadDeadline sets a read deadline on the pty master. It is used by
// Terminal.Pause to interrupt a blocked read.
func (p *PTYBackend) SetReadDeadline(t time.Time) error {
	master, err := p.masterFile()
	if err != nil {
		return err
	}
	return master.SetReadDeadline(t)
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

// SetReadDeadline forwards to the underlying reader when it supports
// deadlines (for example *os.File or an os.Pipe). Otherwise it returns
// errors.ErrUnsupported. Terminal.Pause then cannot interrupt a blocked read
// until the next byte arrives.
func (b *NoPTYBackend) SetReadDeadline(t time.Time) error {
	if b.r == nil {
		return errors.ErrUnsupported
	}
	if d, ok := b.r.(interface{ SetReadDeadline(time.Time) error }); ok {
		return d.SetReadDeadline(t)
	}
	return errors.ErrUnsupported
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

// SetReadDeadline forwards to the wrapped backend when it supports deadlines.
func (t *TeeBackend) SetReadDeadline(d time.Time) error {
	if sb, ok := t.backend.(interface{ SetReadDeadline(time.Time) error }); ok {
		return sb.SetReadDeadline(d)
	}
	return errors.ErrUnsupported
}

// Close forwards to the wrapped backend when it implements io.Closer.
func (t *TeeBackend) Close() error {
	if c, ok := t.backend.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
