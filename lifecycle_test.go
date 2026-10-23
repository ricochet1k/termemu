package termemu

import (
	"bytes"
	"errors"
	"io"
	"sync"
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

// TestCloseAndErrDoNotTakeTerminalLock checks that lifecycle calls made while
// the terminal lock is already held (as from a frontend callback) do not
// deadlock.
func TestCloseAndErrDoNotTakeTerminalLock(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	done := make(chan struct{})
	go func() {
		term.View(func(LockedView) {
			_ = term.Close()
			_ = term.Err()
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close/Err deadlocked while the terminal lock was held")
	}
}

func TestScrollLinesReportsSignedDelta(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(10, 3); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	// Filling the last line scrolls once; each LF after that scrolls again.
	feedText(t, term, "1\r\n2\r\n3\r\n4\r\n5\r\n")
	mf.mu.Lock()
	deltas := append([]int(nil), mf.ScrollDeltas...)
	mf.mu.Unlock()
	if len(deltas) == 0 {
		t.Fatal("ScrollLines was not called")
	}
	for _, d := range deltas {
		if d != -1 {
			t.Fatalf("scroll deltas = %v, want all -1 (content moved up)", deltas)
		}
	}

	// Inserting a line at the top of the scroll region scrolls down.
	mf.mu.Lock()
	mf.ScrollDeltas = nil
	mf.mu.Unlock()
	term.mustHandleCommand(t, "[1;2H")
	term.mustHandleCommand(t, "[L")
	mf.mu.Lock()
	deltas = append([]int(nil), mf.ScrollDeltas...)
	mf.mu.Unlock()
	if len(deltas) != 1 || deltas[0] != 1 {
		t.Fatalf("insert-line scroll deltas = %v, want [1]", deltas)
	}
}

// sizeRecordingBackend applies SetSize after a delay and records the size that
// was actually applied, to expose stale concurrent resize ordering.
type sizeRecordingBackend struct {
	mu    sync.Mutex
	size  Pos
	delay time.Duration
}

func (b *sizeRecordingBackend) Read(p []byte) (int, error)  { return 0, io.EOF }
func (b *sizeRecordingBackend) Write(p []byte) (int, error) { return len(p), nil }
func (b *sizeRecordingBackend) Close() error                { return nil }
func (b *sizeRecordingBackend) SetSize(w, h int) error {
	time.Sleep(b.delay)
	b.mu.Lock()
	b.size = Pos{X: w, Y: h}
	b.mu.Unlock()
	return nil
}
func (b *sizeRecordingBackend) appliedSize() Pos {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

func TestConcurrentResizeAndRestoreLeaveBackendAtScreenSize(t *testing.T) {
	for i := 0; i < 25; i++ {
		backend := &sizeRecordingBackend{delay: time.Millisecond}
		term := newTerminal(&EmptyFrontend{}, backend, TextReadModeRune)
		snap := term.Snapshot()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = term.Resize(100+i, 30)
		}()
		go func() {
			defer wg.Done()
			_ = term.Restore(snap)
		}()
		wg.Wait()

		w, h := term.Size()
		want := Pos{X: w, Y: h}
		if got := backend.appliedSize(); got != want {
			t.Fatalf("iteration %d: backend size = %+v, screen size = %+v", i, got, want)
		}
	}
}

// signalReader reports when its first Read starts (i.e. the read loop has
// computed its maxWidth and is now blocked waiting for output).
type signalReader struct {
	io.ReadCloser
	once    sync.Once
	started chan struct{}
}

func (r *signalReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestResizeWhileReadBlockedUsesNewWidth(t *testing.T) {
	pr, pw := io.Pipe()
	started := make(chan struct{})
	backend := NewNoPTYBackend(&signalReader{ReadCloser: pr, started: started}, io.Discard)
	term := New(&EmptyFrontend{}, backend)
	defer func() {
		_ = term.Close()
		_ = pw.Close()
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("read loop did not reach Read")
	}

	// The loop computed maxWidth for 80 columns before blocking. Shrink the
	// terminal, then deliver a run that only fits at the old width.
	if err := term.Resize(5, 3); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := pw.Write([]byte("abcdefghij")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if term.Line(0) == "abcde" && term.Line(1) == "fghij" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lines after resize = %q / %q, want %q / %q",
				term.Line(0), term.Line(1), "abcde", "fghij")
		}
		time.Sleep(time.Millisecond)
	}
}
