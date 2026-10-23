//go:build unix

package termemu

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestPauseResumeRealPTY exercises Pause/Resume against a real pty master,
// including a resize first: pty.Setsize uses Fd() and switches the descriptor
// back to blocking mode, so the backend must re-apply non-blocking mode or an
// in-flight read cannot be interrupted by the pause deadline.
func TestPauseResumeRealPTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	backend := &PTYBackend{}
	if err := backend.Adopt(master, slave); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
	if err := term.Resize(20, 5); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	defer func() { _ = term.Close() }()

	// Leave the parser mid-CSI so Pause has to interrupt a blocked read.
	if _, err := slave.Write([]byte("\x1b[3")); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h, err := term.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := string(h.Reader.PendingCommand); got != "[3" {
		t.Fatalf("PendingCommand = %q, want %q", got, "[3")
	}
	if h.Size != (Pos{X: 20, Y: 5}) {
		t.Fatalf("handoff size = %+v", h.Size)
	}

	if err := term.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := slave.Write([]byte("1mX")); err != nil {
		t.Fatalf("write rest: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if line := term.Line(0); len(line) > 0 && line[0] == 'X' {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("X not rendered after resume, line0=%q", term.Line(0))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPauseRealPTYCloseWhilePaused verifies Close unblocks a loop parked by a
// real-pty pause.
func TestPauseRealPTYCloseWhilePaused(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	backend := &PTYBackend{}
	if err := backend.Adopt(master, slave); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	term := NewWithMode(NewMockFrontend(), backend, TextReadModeGrapheme)
	if err := term.Resize(20, 5); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := term.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	done := term.Done()
	_ = term.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after Close while paused")
	}
}

// dupFile duplicates f so a pty can be handed to a second backend while the
// first one closes its copies.
func dupFile(t *testing.T, f *os.File) *os.File {
	t.Helper()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatalf("dup %s: %v", f.Name(), err)
	}
	return os.NewFile(uintptr(fd), f.Name())
}

// TestHandoffAcrossBackends simulates the cross-process path in one process:
// pause, duplicate the fds, close the old terminal, adopt the duplicates into a
// new backend, restore via Handoff, and finish the half-consumed sequence.
func TestHandoffAcrossBackends(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	oldBackend := &PTYBackend{}
	if err := oldBackend.Adopt(master, slave); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	oldTerm := NewWithMode(NewMockFrontend(), oldBackend, TextReadModeGrapheme)
	if err := oldTerm.Resize(20, 5); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := slave.Write([]byte("\x1b[3")); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h, err := oldTerm.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := string(h.Reader.PendingCommand); got != "[3" {
		t.Fatalf("PendingCommand = %q, want %q", got, "[3")
	}

	// Duplicate before closing the old owner; keep a writer for the slave.
	newMaster := dupFile(t, master)
	newSlave := dupFile(t, slave)
	writer := dupFile(t, slave)
	defer func() { _ = writer.Close() }()

	_ = oldTerm.Close() // closes the original master/slave only

	newBackend := &PTYBackend{}
	if err := newBackend.Adopt(newMaster, newSlave); err != nil {
		t.Fatalf("Adopt new: %v", err)
	}
	newTerm, err := NewFromHandoff(NewMockFrontend(), newBackend, h)
	if err != nil {
		t.Fatalf("NewFromHandoff: %v", err)
	}
	defer func() { _ = newTerm.Close() }()

	if _, err := writer.Write([]byte("1mX")); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if line := newTerm.Line(0); len(line) > 0 && line[0] == 'X' {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("X not rendered after handoff, line0=%q", newTerm.Line(0))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pauseResumeGrapheme(t *testing.T, first, rest, want string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	term := NewWithMode(NewMockFrontend(), NewNoPTYBackend(pr, nil), TextReadModeGrapheme)
	_ = term.Resize(20, 3)
	defer func() { _ = term.Close() }()
	if _, err := pw.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := term.Pause(ctx); err != nil {
		t.Fatalf("Pause after %q: %v", first, err)
	}
	if err := term.Resume(); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte(rest)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		line := strings.TrimRight(term.Line(0), " ")
		if line == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %q + %q got %q, want %q", first, rest, line, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPauseResumeGraphemeBoundaries(t *testing.T) {
	pauseResumeGrapheme(t, "👨\u200d", "👩\u200d👧", "👨\u200d👩\u200d👧")
	pauseResumeGrapheme(t, "👨\u200d👩", "\u200d👧", "👨\u200d👩\u200d👧")
	pauseResumeGrapheme(t, "🇺", "🇸", "🇺🇸")
	pauseResumeGrapheme(t, "e", "\u0301", "e\u0301")
	pauseResumeGrapheme(t, "👍", "🏽", "👍🏽")
	pauseResumeGrapheme(t, "1", "\ufe0f\u20e3", "1\ufe0f\u20e3")
}
