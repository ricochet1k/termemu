//go:build unix

package termemu

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// deadlinePipe wraps an *os.File so tests can observe how many Read calls the
// read loop has made (and therefore when it is blocked waiting for more input)
// while still supporting read deadlines.
type deadlinePipe struct {
	f *os.File

	mu    sync.Mutex
	reads int
}

func newDeadlinePipe(f *os.File) *deadlinePipe {
	return &deadlinePipe{f: f}
}

func (p *deadlinePipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	p.reads++
	p.mu.Unlock()
	return p.f.Read(b)
}

func (p *deadlinePipe) SetReadDeadline(t time.Time) error {
	return p.f.SetReadDeadline(t)
}

func (p *deadlinePipe) Close() error {
	return p.f.Close()
}

func (p *deadlinePipe) waitReads(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		got := p.reads
		p.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("read loop made %d reads, want at least %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func newPipeTerminal(t *testing.T) (*os.File, *deadlinePipe, *terminal) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	dp := newDeadlinePipe(r)
	term := New(NewMockFrontend(), NewNoPTYBackend(dp, io.Discard)).(*terminal)
	t.Cleanup(func() {
		_ = term.Close()
		_ = w.Close()
	})
	return w, dp, term
}

func pauseWithTimeout(t *testing.T, term *terminal) Handoff {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := term.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	return h
}

func TestPauseResumeMidCSI(t *testing.T) {
	w, dp, term := newPipeTerminal(t)

	if _, err := w.Write([]byte("\x1b[3")); err != nil {
		t.Fatalf("write: %v", err)
	}
	dp.waitReads(t, 2) // ESC [ 3 consumed, waiting for the final byte

	h := pauseWithTimeout(t, term)
	if got := string(h.Reader.PendingCommand); got != "[3" {
		t.Fatalf("PendingCommand = %q, want %q", got, "[3")
	}
	if h.Size.X <= 0 || h.Size.Y <= 0 {
		t.Fatalf("invalid handoff size %v", h.Size)
	}

	if err := term.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := w.Write([]byte("1mX")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "X")
	if ansi := term.ANSILine(0); !strings.Contains(ansi, "\x1b[31m") {
		t.Fatalf("line 0 ANSI = %q, want red SGR 31", ansi)
	}
}

func TestPauseResumeMidUTF8(t *testing.T) {
	w, dp, term := newPipeTerminal(t)

	// First byte of the three-byte euro sign.
	if _, err := w.Write([]byte{0xe2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	dp.waitReads(t, 2)

	h := pauseWithTimeout(t, term)
	if got := string(h.Reader.Pending); got != "\xe2" {
		t.Fatalf("Pending = %q, want first euro byte", got)
	}

	if err := term.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := w.Write([]byte{0x82, 0xac, 'X'}); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "\u20acX")
}

func TestPauseWhileIdleResume(t *testing.T) {
	w, dp, term := newPipeTerminal(t)

	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "abc")
	dp.waitReads(t, 2) // blocked, no pending bytes

	h1 := pauseWithTimeout(t, term)
	if len(h1.Reader.Pending) != 0 || len(h1.Reader.PendingCommand) != 0 {
		t.Fatalf("expected clean reader state, got %+v", h1.Reader)
	}
	if got := term.Line(0); !strings.Contains(got, "abc") {
		t.Fatalf("paused screen lost content: %q", got)
	}
	// Taking a snapshot while paused must be stable and must not consume
	// anything from the paused loop.
	snap := term.Snapshot()
	if snap.Main.Width != h1.Snapshot.Main.Width || snap.Main.Height != h1.Snapshot.Main.Height {
		t.Fatalf("paused Snapshot changed dimensions: %dx%d", snap.Main.Width, snap.Main.Height)
	}
	if err := term.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if _, err := w.Write([]byte("def")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "abcdef")
}

func TestPauseHandoffEndToEnd(t *testing.T) {
	w, dp, old := newPipeTerminal(t)

	if _, err := w.Write([]byte("hello\x1b[3")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, old, 0, "hello")
	dp.waitReads(t, 2)

	h := pauseWithTimeout(t, old)
	if got := string(h.Reader.PendingCommand); got != "[3" {
		t.Fatalf("PendingCommand = %q, want %q", got, "[3")
	}
	if got := old.Line(0); !strings.Contains(got, "hello") {
		t.Fatalf("snapshot content = %q", got)
	}

	// The old process pauses, packages the handoff/fd, then closes.
	if err := old.Close(); err != nil {
		t.Fatalf("old Close: %v", err)
	}
	waitDone(t, old, 5*time.Second)

	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	dp2 := newDeadlinePipe(r2)
	backend2 := NewNoPTYBackend(dp2, io.Discard)
	newTerm, err := NewFromHandoff(NewMockFrontend(), backend2, h)
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	t.Cleanup(func() {
		_ = newTerm.Close()
		_ = w2.Close()
	})

	if _, err := w2.Write([]byte("1mX")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, newTerm, 0, "helloX")
	if ansi := newTerm.ANSILine(0); !strings.Contains(ansi, "\x1b[31m") {
		t.Fatalf("line 0 ANSI = %q, want red SGR 31", ansi)
	}
}

func TestPauseCancelKeepsLoopRunning(t *testing.T) {
	w, dp, term := newPipeTerminal(t)

	if _, err := w.Write([]byte("ab")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "ab")
	dp.waitReads(t, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := term.Pause(ctx); err == nil {
		t.Fatal("Pause with cancelled context succeeded")
	}

	// The loop must still be alive after the cancelled pause.
	if _, err := w.Write([]byte("cd")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "abcd")
}

func TestCloseWhilePaused(t *testing.T) {
	w, dp, term := newPipeTerminal(t)

	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, term, 0, "x")
	dp.waitReads(t, 2)

	_ = pauseWithTimeout(t, term)
	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitDone(t, term, 5*time.Second)
	if err := term.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestNewFromHandoffRestoresScrollback(t *testing.T) {
	w, _, old := newPipeTerminal(t)
	if err := old.Resize(10, 3); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := w.Write([]byte("1\r\n2\r\n3\r\n4\r\n5\r\n6\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForLine(t, old, 1, "6")

	h := pauseWithTimeout(t, old)
	if len(h.Scrollback.Rows) == 0 {
		t.Fatal("handoff captured no scrollback rows")
	}

	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	backend2 := NewNoPTYBackend(newDeadlinePipe(r2), io.Discard)
	newTerm, err := NewFromHandoff(NewMockFrontend(), backend2, h)
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	defer func() {
		_ = newTerm.Close()
		_ = w2.Close()
	}()

	if got, want := newTerm.Scrollback().Len(), len(h.Scrollback.Rows); got != want {
		t.Fatalf("scrollback Len = %d, want %d", got, want)
	}
	if got := newTerm.Line(1); !strings.Contains(got, "6") {
		t.Fatalf("restored screen line 1 = %q", got)
	}
}

func TestNewFromHandoffResizesSnapshot(t *testing.T) {
	old := newTerminal(NewMockFrontend(), NewNoPTYBackend(nil, nil), TextReadModeRune)
	if err := old.Resize(10, 3); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	feedText(t, old, "abcdefghij\r\nsecond\r\nthird")
	h, err := old.Pause(context.Background())
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	// The receiving window is smaller than the captured one.
	h.Size = Pos{X: 5, Y: 2}

	newTerm, err := NewFromHandoff(nil, NewNoPTYBackend(bytes.NewReader(nil), io.Discard), h)
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	defer func() { _ = newTerm.Close() }()

	if w, h := newTerm.Size(); w != 5 || h != 2 {
		t.Fatalf("size = %dx%d, want 5x2", w, h)
	}
	if got := newTerm.Line(0); got != "abcde" {
		t.Fatalf("line 0 = %q, want %q", got, "abcde")
	}
	if got := newTerm.Line(1); got != "secon" {
		t.Fatalf("line 1 = %q, want %q", got, "secon")
	}
}

func TestNewFromHandoffRejectsInvalidSizes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() {
		_ = r.Close()
		_ = w.Close()
	}()

	backend := NewNoPTYBackend(r, io.Discard)
	if _, err := NewFromHandoff(nil, backend, Handoff{}); err == nil {
		t.Fatal("expected error for zero size")
	}

	backend2 := NewNoPTYBackend(r, io.Discard)
	h := Handoff{
		Size: Pos{X: 80, Y: 24},
		Snapshot: Snapshot{
			Main: ScreenState{Width: 10, Height: 10},
			Alt:  ScreenState{Width: 10, Height: 10},
		},
	}
	// A snapshot captured at another size is resized into Handoff.Size.
	newTerm, err := NewFromHandoff(nil, backend2, h)
	if err != nil {
		t.Fatalf("NewFromHandoff with a different snapshot size: %v", err)
	}
	defer func() { _ = newTerm.Close() }()
	if w, h := newTerm.Size(); w != 80 || h != 24 {
		t.Fatalf("terminal size = %dx%d, want 80x24", w, h)
	}
}

func TestPTYBackendHandoffAccessors(t *testing.T) {
	backend := &PTYBackend{}
	defer func() { _ = backend.Close() }()

	master := backend.Master()
	if master == nil {
		t.Fatal("Master returned nil")
	}
	slave := backend.Slave()
	if slave == nil {
		t.Fatal("Slave returned nil")
	}
	if err := backend.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := backend.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear SetReadDeadline: %v", err)
	}
}
