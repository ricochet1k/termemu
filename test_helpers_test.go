package termemu

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// mustHandleCommand is a test helper that calls testHandleCommand and fails on error.
// Use this for synchronous calls. For goroutine calls, use testHandleCommand directly.
func (t *terminal) mustHandleCommand(te *testing.T, cmd string) {
	te.Helper()
	if err := t.testHandleCommand(te, cmd); err != nil {
		te.Fatal(err)
	}
}

// MockFrontend implements Frontend for tests and records calls.
type MockFrontend struct {
	mu sync.Mutex

	BellCount int

	Regions []struct {
		R Region
		C ChangeReason
	}

	CursorX          int
	CursorY          int
	CursorMovedCount int

	Styles []Style

	ViewFlags   map[ViewFlag]bool
	ViewInts    map[ViewInt]int
	ViewStrings map[ViewString]string
}

func NewMockFrontend() *MockFrontend {
	return &MockFrontend{
		ViewFlags:   make(map[ViewFlag]bool),
		ViewInts:    make(map[ViewInt]int),
		ViewStrings: make(map[ViewString]string),
	}
}

func (m *MockFrontend) Bell() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BellCount++
}

func (m *MockFrontend) RegionChanged(r Region, c ChangeReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Regions = append(m.Regions, struct {
		R Region
		C ChangeReason
	}{r, c})
}

func (m *MockFrontend) ScrollLines(y int) {
	// no-op for now
	_ = y
}

func (m *MockFrontend) CursorMoved(x, y int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CursorX = x
	m.CursorY = y
	m.CursorMovedCount++
}

func (m *MockFrontend) StyleChanged(s Style) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Styles = append(m.Styles, s)
}

func (m *MockFrontend) ViewFlagChanged(vs ViewFlag, value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewFlags[vs] = value
}

// RegionCount returns the number of recorded region changes (thread-safe).
func (m *MockFrontend) RegionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Regions)
}

func (m *MockFrontend) ViewIntChanged(vs ViewInt, value int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewInts[vs] = value
}

func (m *MockFrontend) ViewStringChanged(vs ViewString, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ViewStrings[vs] = value
}

// helper to create a screen with a MockFrontend and return both
func MakeScreenWithMock() (screen, *MockFrontend) {
	mf := NewMockFrontend()
	s := newScreen(mf)
	return s, mf
}

// MakeTerminalWithMock constructs a terminal without opening a pty for tests.
// The terminal does NOT start a read loop; tests use testFeedTerminalInputFromBackend
// to feed data synchronously, avoiding data races.
func MakeTerminalWithMock(mode TextReadMode) (io.ReadCloser, *terminal, *MockFrontend) {
	mf := NewMockFrontend()
	// Create two pipes: one for input TO terminal, one for output FROM terminal
	// terminalInput_r, terminalInput_w := io.Pipe()
	terminalOutput_r, terminalOutput_w := NewBufPipe(10)

	// Backend reads from terminalInput_r (for ptyReadLoop to consume program output)
	// Backend writes to terminalOutput_w (for SendMouseRaw/SendKey to write user input)
	// Use newTerminal (no read loop) since tests feed data synchronously
	t := newTerminal(mf, NewNoPTYBackend(bytes.NewReader(nil), terminalOutput_w), mode)

	// Return terminalOutput_r (where test reads terminal output like mouse events)
	// and terminalInput_w (where test writes program output to terminal)
	return terminalOutput_r, t, mf
}

func (t *terminal) testFeedTerminalInputFromBackend(data []byte, mode TextReadMode) error {
	r := bytes.NewReader(data)
	gr := NewGraphemeReaderWithMode(r, mode)
	for r.Len() > 0 || gr.Buffered() > 0 {
		if err := t.ptyReadOne(gr); err != nil {
			return err
		}
	}
	return nil
}

// // small helper to fail test with recorded state for easier debugging
// func dumpMock(t *testing.T, m *MockFrontend) {
// 	t.Logf("MockFrontend: Bell=%d, Cursor=(%d,%d), Regions=%d, Styles=%d", m.BellCount, m.CursorX, m.CursorY, len(m.Regions), len(m.Styles))
// }

type BufPipeReader struct {
	c      <-chan []byte
	buf    []byte
	closed *atomic.Bool
}

// Close implements io.ReadCloser.
func (b *BufPipeReader) Close() error {
	b.closed.Store(true)
	return nil
}

// Read implements io.ReadCloser.
func (b *BufPipeReader) Read(p []byte) (n int, err error) {
	if b.buf != nil {
		n = copy(p, b.buf)
		if n < len(b.buf) {
			b.buf = b.buf[n:]
		} else {
			b.buf = nil
		}
	}
	for n < len(p) {
		if b.closed.Load() {
			return 0, io.EOF
		}
		var buf []byte
		var ok bool
		// log.Printf("bufpipe start read %v, %v", n, len(b.c))
		if n > 0 {
			select {
			case buf, ok = <-b.c:
				if !ok {
					return n, nil
				}
			default:
				return n, nil
			}
		} else {
			buf = <-b.c
		}
		// log.Printf("bufpipe read %v %q", len(buf), string(buf))
		nn := copy(p[n:], buf)
		if nn < len(b.buf) {
			b.buf = buf[nn:]
		}
		n += nn
	}
	return n, nil
}

type BufPipeWriter struct {
	c      chan<- []byte
	closed *atomic.Bool
}

// Close implements io.WriteCloser.
func (b *BufPipeWriter) Close() error {
	close(b.c)
	b.closed.Store(true)
	return nil
}

// Write implements io.WriteCloser.
func (b *BufPipeWriter) Write(p []byte) (n int, err error) {
	if b.closed.Load() {
		return 0, io.EOF
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	b.c <- buf
	return len(p), nil
}

func NewBufPipe(writesBuffered int) (io.ReadCloser, io.WriteCloser) {
	c := make(chan []byte, writesBuffered)
	closed := new(atomic.Bool)

	return &BufPipeReader{c, nil, closed}, &BufPipeWriter{c, closed}
}
