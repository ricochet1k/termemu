//go:build unix

package termemu

import (
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"
)

// TestTerminalCloseStopsEIORetry attaches a command that exits immediately
// (leaving Read in its EIO "wait for the next command" retry) and checks
// that Close stops the retry promptly instead of sleeping out the backoff.
func TestTerminalCloseStopsEIORetry(t *testing.T) {
	backend := &PTYBackend{}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	term := New(NewMockFrontend(), backend)
	if term == nil {
		t.Fatal("New returned nil terminal")
	}

	// Let the child exit and the read loop settle into EIO retry.
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	waitDone(t, term, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Close took too long: %v", elapsed)
	}

	if _, err := backend.Read(make([]byte, 16)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected ErrClosedPipe after Close, got %v", err)
	}
	if _, err := backend.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected ErrClosedPipe on Write after Close, got %v", err)
	}
}

// TestTerminalCloseRunningPTY closes a terminal while its child is still
// running and checks the read loop exits.
func TestTerminalCloseRunningPTY(t *testing.T) {
	backend := &PTYBackend{}
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	term := New(NewMockFrontend(), backend)
	if term == nil {
		t.Fatal("New returned nil terminal")
	}

	if err := term.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	waitDone(t, term, 5*time.Second)
}

// TestPTYBackendCloseUnblocksRead checks that closing the master wakes a
// goroutine blocked in Read, and that later Read/Write/Attach fail instead
// of reopening the pair.
func TestPTYBackendCloseUnblocksRead(t *testing.T) {
	backend := &PTYBackend{}
	if _, err := backend.Open(); err != nil {
		t.Fatalf("open failed: %v", err)
	}

	readErr := make(chan error, 1)
	go func() {
		_, err := backend.Read(make([]byte, 16))
		readErr <- err
	}()

	time.Sleep(100 * time.Millisecond)
	if err := backend.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, io.EOF) {
			t.Fatalf("expected closed-pipe error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Read did not return after Close")
	}

	if err := backend.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if _, err := backend.Read(make([]byte, 16)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected ErrClosedPipe after Close, got %v", err)
	}
	if err := backend.Attach(exec.Command("true")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected Attach after Close to fail, got %v", err)
	}
}

// TestPTYBackendPIDAndWait covers the process helpers: PID is 0 before Start,
// matches the child after Start, Wait reaps the child, and a second Wait
// reports exec's "already called" error.
func TestPTYBackendPIDAndWait(t *testing.T) {
	backend := &PTYBackend{}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if pid := backend.PID(); pid != 0 {
		t.Fatalf("PID before Start = %d, want 0", pid)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if pid, want := backend.PID(), cmd.Process.Pid; pid != want {
		t.Fatalf("PID = %d, want %d", pid, want)
	}

	if err := backend.Wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if err := backend.Wait(); err == nil {
		t.Fatal("second Wait succeeded, want exec's already-called error")
	}
}

// TestPTYBackendWaitAfterClose verifies the attached command can still be
// reaped after Close (closing the pty may signal it, so only the fact that
// Wait returns the process result is asserted).
func TestPTYBackendWaitAfterClose(t *testing.T) {
	backend := &PTYBackend{}
	cmd := exec.Command("sh", "-c", "exit 3")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err := backend.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Wait after Close = %v, want *exec.ExitError", err)
	}
}

// TestPTYBackendWaitUsesLatestAttach checks Wait tracks the most recently
// attached command after the pty is reused.
func TestPTYBackendWaitUsesLatestAttach(t *testing.T) {
	backend := &PTYBackend{}

	first := exec.Command("sh", "-c", "exit 1")
	if err := backend.Attach(first); err != nil {
		t.Fatalf("attach first: %v", err)
	}
	if err := first.Start(); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if err := backend.Wait(); err == nil {
		t.Fatal("first Wait succeeded, want exit status 1")
	}

	second := exec.Command("sh", "-c", "exit 0")
	if err := backend.Attach(second); err != nil {
		t.Fatalf("attach second: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("start second: %v", err)
	}
	if pid, want := backend.PID(), second.Process.Pid; pid != want {
		t.Fatalf("PID = %d, want %d (latest command)", pid, want)
	}
	if err := backend.Wait(); err != nil {
		t.Fatalf("Wait for second command failed: %v", err)
	}
}
