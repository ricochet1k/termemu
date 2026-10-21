package termemu

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// errBackend is a Backend whose Read always fails, to exercise read-loop
// error surfacing.
type errBackend struct {
	err    error
	closed bool
}

func (b *errBackend) Read(p []byte) (int, error) { return 0, b.err }
func (b *errBackend) Write(p []byte) (int, error) {
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func (b *errBackend) SetSize(w, h int) error { return nil }
func (b *errBackend) Close() error {
	b.closed = true
	return nil
}

func waitDone(t *testing.T, term Terminal, d time.Duration) {
	t.Helper()
	select {
	case <-term.Done():
	case <-time.After(d):
		t.Fatal("timed out waiting for Done to close")
	}
}

func TestTerminalCloseClosesDone(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	done := term.Done()
	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after Close")
	}
}

func TestTerminalCloseIsIdempotent(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	select {
	case <-term.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
}

func TestTerminalReadAfterCloseFails(t *testing.T) {
	backend := NewNoPTYBackend(bytes.NewReader(nil), io.Discard)
	term := New(&EmptyFrontend{}, backend)
	waitDone(t, term, 5*time.Second) // empty reader: EOF exits the loop at once
	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := backend.Read(make([]byte, 16)); err == nil {
		t.Fatal("expected Read after Close to fail")
	}
}

// TestTerminalCleanEOFReportsNilErr checks that a backend hitting EOF is a
// clean shutdown: Done closes and Err is nil.
func TestTerminalCleanEOFReportsNilErr(t *testing.T) {
	term := New(&EmptyFrontend{}, NewNoPTYBackend(bytes.NewReader([]byte("hi")), io.Discard))
	waitDone(t, term, 5*time.Second)
	if err := term.Err(); err != nil {
		t.Fatalf("expected nil Err after EOF, got %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestTerminalReadLoopErrorSurfaced checks that a failing backend Read is
// recorded and visible via Err after Done closes.
func TestTerminalReadLoopErrorSurfaced(t *testing.T) {
	boom := errors.New("boom")
	term := New(&EmptyFrontend{}, &errBackend{err: boom})
	waitDone(t, term, 5*time.Second)
	if err := term.Err(); !errors.Is(err, boom) {
		t.Fatalf("expected Err to wrap %v, got %v", boom, err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Closing an already-exited terminal still reports the loop error.
	if err := term.Err(); !errors.Is(err, boom) {
		t.Fatalf("expected Err to still wrap %v, got %v", boom, err)
	}
}

// TestTerminalCloseUnblocksBlockedRead checks that closing the backend wakes
// a goroutine stuck in Read instead of leaving it blocked forever.
func TestTerminalCloseUnblocksBlockedRead(t *testing.T) {
	// NoPTYBackend over a pipe that never yields: Read blocks until Close.
	r, w := io.Pipe()
	backend := NewNoPTYBackend(r, io.Discard)
	term := New(&EmptyFrontend{}, backend)

	readErr := make(chan error, 1)
	go func() {
		_, err := backend.Read(make([]byte, 16))
		readErr <- err
	}()

	// Give the read loop a moment to block inside Read.
	time.Sleep(100 * time.Millisecond)

	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	_ = w.Close()

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("expected blocked Read to fail after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Read did not return after Close")
	}
	waitDone(t, term, 5*time.Second)
}

func TestResizeEmitsSingleFullRegion(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)

	mf.mu.Lock()
	mf.Regions = nil
	mf.mu.Unlock()

	if err := term.Resize(100, 30); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	mf.mu.Lock()
	regions := append([]struct {
		R Region
		C ChangeReason
	}(nil), mf.Regions...)
	mf.mu.Unlock()

	if len(regions) != 1 {
		t.Fatalf("expected exactly 1 RegionChanged after Resize, got %d", len(regions))
	}
	got := regions[0]
	want := Region{X: 0, Y: 0, X2: 100, Y2: 30}
	if got.R != want {
		t.Fatalf("expected full region %v, got %v", want, got.R)
	}
	if got.C != CRResize {
		t.Fatalf("expected reason CRResize, got %v", got.C)
	}

	w, h := term.Size()
	if w != 100 || h != 30 {
		t.Fatalf("expected size 100x30, got %dx%d", w, h)
	}
}
