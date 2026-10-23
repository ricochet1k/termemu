package termemu

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedChunkReader hands out chunks in order and blocks once, just before
// serving chunk index gateAfter (or EOF when gateAfter == len(chunks)). Tests
// use it to park the parser mid-escape without relying on sleeps.
type gatedChunkReader struct {
	chunks    [][]byte
	gateAfter int

	mu         sync.Mutex
	idx        int
	gated      bool
	blocked    chan struct{}
	blockOnce  sync.Once
	release    chan struct{}
	releaseOne sync.Once
}

func newGatedChunkReader(chunks [][]byte, gateAfter int) *gatedChunkReader {
	return &gatedChunkReader{
		chunks:    chunks,
		gateAfter: gateAfter,
		blocked:   make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (r *gatedChunkReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.idx == r.gateAfter && !r.gated {
		r.gated = true
		r.mu.Unlock()
		r.blockOnce.Do(func() { close(r.blocked) })
		<-r.release
		r.mu.Lock()
	}
	if r.idx >= len(r.chunks) {
		r.mu.Unlock()
		return 0, io.EOF
	}
	data := r.chunks[r.idx]
	r.idx++
	r.mu.Unlock()
	return copy(p, data), nil
}

func (r *gatedChunkReader) releaseNow() {
	r.releaseOne.Do(func() { close(r.release) })
}

// newGatedTerminal starts a terminal whose backend is a gated reader.
func newGatedTerminal(t *testing.T, chunks [][]byte, gateAfter int) (*gatedChunkReader, *terminal) {
	t.Helper()
	r := newGatedChunkReader(chunks, gateAfter)
	term := New(NewMockFrontend(), NewNoPTYBackend(r, io.Discard)).(*terminal)
	t.Cleanup(func() {
		r.releaseNow()
		_ = term.Close()
	})
	return r, term
}

func waitBlocked(t *testing.T, r *gatedChunkReader) {
	t.Helper()
	select {
	case <-r.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("parser never blocked mid-escape")
	}
}

// TestSnapshotWhileEscapePending is the blocking-pipe repro: the child output
// ends after "ESC [ 1 2" with no final byte, and Snapshot must still return.
func TestSnapshotWhileEscapePending(t *testing.T) {
	r, term := newGatedTerminal(t, [][]byte{[]byte("\x1b[12")}, 1)
	waitBlocked(t, r)

	done := make(chan Snapshot, 1)
	go func() { done <- term.Snapshot() }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot blocked while the read loop was mid-escape")
	}
}

// TestRestoreWhileEscapePending checks Restore (which also needs the lock)
// does not block behind a partial escape sequence.
func TestRestoreWhileEscapePending(t *testing.T) {
	r, term := newGatedTerminal(t, [][]byte{[]byte("\x1b[12")}, 1)
	waitBlocked(t, r)

	snap := term.Snapshot()

	done := make(chan error, 1)
	go func() { done <- term.Restore(snap) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Restore failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Restore blocked while the read loop was mid-escape")
	}
}

// TestPartialEscapeThenRest verifies a sequence split across reads still
// parses as one command once the remaining bytes arrive.
func TestPartialEscapeThenRest(t *testing.T) {
	r, term := newGatedTerminal(t, [][]byte{
		[]byte("\x1b[2;"),
		[]byte("3H"),
	}, 1)
	waitBlocked(t, r)

	r.releaseNow()

	deadline := time.Now().Add(2 * time.Second)
	for {
		pos := term.CursorPos()
		if pos.X == 2 && pos.Y == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cursor = (%d,%d), want (2,1)", pos.X, pos.Y)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The final byte of the CSI must not leak onto the screen as text.
	if line := term.Line(0); strings.Contains(line, "3H") || strings.Contains(line, "H") {
		t.Fatalf("partial escape leaked text: %q", line)
	}
}

func TestReadEscapeCommand(t *testing.T) {
	const st = "\x1b\\"
	cases := []struct {
		name string
		in   string
		cmd  string
		rest string
	}{
		{"csi", "[12;3Hrest", "[12;3H", "rest"},
		{"csi private", "[?1049halt", "[?1049h", "alt"},
		{"csi intermediates", "[?1$pX", "[?1$p", "X"},
		{"csi malformed stops", "[12\x7frest", "[12\x7f", "rest"},
		{"osc bel", "]0;title\x07rest", "]0;title\x07", "rest"},
		{"osc st", "]8;;http://example" + st + "x", "]8;;http://example" + st, "x"},
		{"osc utf8 continuation", "]0;A\xf0\x9c\x80\x80" + st + "tail", "]0;A\xf0\x9c\x80\x80" + st, "tail"},
		{"dcs", "P1;2|x" + st + "y", "P1;2|x" + st, "y"},
		{"apc", "_payload" + st + "z", "_payload" + st, "z"},
		{"pm bel", "^payload\x07z", "^payload\x07", "z"},
		{"sos 8bit st", "Xpayload\x9cz", "Xpayload\x9c", "z"},
		{"two byte percent", "%Gtail", "%G", "tail"},
		{"two byte hash", "#8tail", "#8", "tail"},
		{"two byte charset", "(B)rest", "(B", ")rest"},
		{"single 7", "7rest", "7", "rest"},
		{"single D", "Drest", "D", "rest"},
		{"single ST", "\\rest", "\\", "rest"},
		{"unknown single", "qrest", "q", "rest"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader([]byte(tc.in))
			cmd, err := readEscapeCommand(r, nil)
			if err != nil {
				t.Fatalf("readEscapeCommand(%q): %v", tc.in, err)
			}
			if string(cmd) != tc.cmd {
				t.Fatalf("command = %q, want %q", cmd, tc.cmd)
			}
			rest, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("read remainder: %v", err)
			}
			if string(rest) != tc.rest {
				t.Fatalf("remainder = %q, want %q", rest, tc.rest)
			}
		})
	}
}
