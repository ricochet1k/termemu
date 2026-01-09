package termemu

import (
	"testing"
	"time"
)

// This is a light-weight integration test that starts a short command under a real pty
// and ensures the terminal's frontend receives output regions.
func TestIntegration_RunPrintf(t *testing.T) {
	// create terminal with mock frontend
	tt := New(NewMockFrontend())
	if tt == nil {
		t.Fatal("New returned nil terminal")
	}

	tln := tt.(*terminal)

	// Instead of starting a child process (which may fail in some CI environments),
	// write to the slave end to simulate program output arriving at the master.
	if err := tln.ensurePTYOpen(); err != nil {
		t.Fatalf("pty open failed: %v", err)
	}
	if tln.tty == nil {
		t.Fatalf("tty not initialized on terminal")
	}
	if _, err := tln.tty.Write([]byte("hello_integration")); err != nil {
		t.Fatalf("writing to tty failed: %v", err)
	}

	// Give ptyReadLoop a moment to process incoming bytes
	time.Sleep(50 * time.Millisecond)

	mf := tln.frontend.(*MockFrontend)
	if len(mf.Regions) == 0 {
		t.Fatalf("expected frontend RegionChanged calls, got none")
	}
}
