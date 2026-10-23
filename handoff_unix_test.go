//go:build unix

package termemu

import (
	"context"
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
