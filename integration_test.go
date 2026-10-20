//go:build unix

package termemu

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// waitForLine polls the terminal until line y contains want, or the timeout
// expires. Polling is used because the pty read loop is asynchronous.
func waitForLine(t *testing.T, term Terminal, y int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var line string
		term.WithLock(func() {
			line = term.Line(y)
		})
		if strings.Contains(line, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("line %d never contained %q, got %q", y, want, line)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// This is a light-weight integration test that starts a short command under a real pty
// and ensures the terminal's frontend receives output regions.
func TestIntegration_RunPrintf(t *testing.T) {
	backend := &PTYBackend{}
	tt := New(NewMockFrontend(), backend)
	if tt == nil {
		t.Fatal("New returned nil terminal")
	}

	cmd := exec.Command("sh", "-c", "printf hello_integration")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	waitForLine(t, tt, 0, "hello_integration")

	mf := tt.(*terminal).frontend.(*MockFrontend)
	if mf.RegionCount() == 0 {
		t.Fatalf("expected frontend RegionChanged calls, got none")
	}
}

// TestIntegration_WritesToSlave checks the read loop without a child process,
// by writing to the slave end directly. This keeps coverage for environments
// where spawning a child is not possible.
func TestIntegration_WritesToSlave(t *testing.T) {
	backend := &PTYBackend{}
	slave, err := backend.Open()
	if err != nil {
		t.Fatalf("pty open failed: %v", err)
	}

	tt := New(NewMockFrontend(), backend)
	if tt == nil {
		t.Fatal("New returned nil terminal")
	}

	if slave == nil {
		t.Fatalf("pty slave not initialized on backend")
	}
	if _, err := slave.Write([]byte("hello_integration")); err != nil {
		t.Fatalf("writing to tty failed: %v", err)
	}

	waitForLine(t, tt, 0, "hello_integration")

	mf := tt.(*terminal).frontend.(*MockFrontend)
	if mf.RegionCount() == 0 {
		t.Fatalf("expected frontend RegionChanged calls, got none")
	}
}

// TestIntegration_ReusePTY runs two commands sequentially through the same
// backend and terminal, verifying that the pty stays usable after the first
// command exits.
func TestIntegration_ReusePTY(t *testing.T) {
	backend := &PTYBackend{}
	tt := New(NewMockFrontend(), backend)

	run := func(script string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		if err := backend.Attach(cmd); err != nil {
			t.Fatalf("attach failed: %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("wait failed: %v", err)
		}
	}

	run("printf first_marker")
	waitForLine(t, tt, 0, "first_marker")

	run(`printf '\nsecond_marker'`)
	waitForLine(t, tt, 1, "second_marker")
}

// TestIntegration_BuildTerminalBeforeAttach verifies that a terminal created
// before the pty exists still captures output once a command is attached.
func TestIntegration_BuildTerminalBeforeAttach(t *testing.T) {
	backend := &PTYBackend{}
	tt := New(NewMockFrontend(), backend)

	cmd := exec.Command("sh", "-c", "printf late_attach")
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	waitForLine(t, tt, 0, "late_attach")
}

// TestIntegration_AttachSetsTerm verifies Attach sets a TERM the emulator
// understands, without clobbering unrelated environment variables.
func TestIntegration_AttachSetsTerm(t *testing.T) {
	backend := &PTYBackend{}
	cmd := exec.Command("true")
	cmd.Env = []string{"TERM=dumb", "KEEP=me"}
	if err := backend.Attach(cmd); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	var gotTerm, gotKeep bool
	for _, v := range cmd.Env {
		switch v {
		case termStr:
			gotTerm = true
		case "KEEP=me":
			gotKeep = true
		}
	}
	if !gotTerm {
		t.Errorf("expected %q in child env, got %v", termStr, cmd.Env)
	}
	if !gotKeep {
		t.Errorf("expected KEEP=me preserved in child env, got %v", cmd.Env)
	}
}
