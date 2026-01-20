package termemu

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestEmojiOverwriteSequence(t *testing.T) {
	// This reproduces the emoji_overwrite integration test scenario
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	term.Resize(80, 8)

	// Test case from integration: "🐹c\x1b[D\x1b[Dy"
	if err := term.testFeedTerminalInputFromBackend([]byte(
		"🐹a\n"+
			"🐹b\x1b[Dz\n"+
			"🐹c\x1b[D\x1b[Dy\n"+
			"🐹c\x1b[D\x1b[D\x1b[Dx\n",
	), TextReadModeRune); err != nil {
		t.Fatal(err)
	}

	expectedLines := []string{
		"🐹a",
		"🐹z",
		"🐹yc",
		"x c",
	}

	for i, expectedLine := range expectedLines {
		line := strings.TrimRight(term.Line(i), " ")
		t.Logf("Line %v: %q", i, line)

		if line != expectedLine {
			t.Errorf("Line %v expected %q, got line: %q", i, expectedLine, line)
		}
	}
}

func TestSimpleTextWrite(t *testing.T) {
	// Test the most basic case - just writing text
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	term.Resize(80, 24)

	screen := term.screen()
	if writer, ok := screen.(interface {
		writeString(string, int, bool, TextReadMode)
	}); ok {
		writer.writeString("Hello, World!", 13, false, TextReadModeRune)
	}

	line := term.Line(0)
	t.Logf("Line 0: %q", line)

	if len(line) < 13 {
		t.Fatalf("Line too short: %d characters, got: %q", len(line), line)
	}

	if line[0:13] != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!' at start, got: %q", line)
	}
}

func TestCaptureANSILikeIntegrationTest(t *testing.T) {
	// This replicates exactly what the integration test does
	r, w := io.Pipe()
	defer r.Close()

	backend := NewNoPTYBackend(r, w)
	frontend := &EmptyFrontend{}
	term := New(frontend, backend)
	if term == nil {
		t.Fatalf("failed to create terminal")
	}

	if err := term.Resize(80, 8); err != nil {
		t.Fatalf("resize: %v", err)
	}

	// Write a simple sequence
	sequence := "Hello, World!"
	if _, err := w.Write([]byte(sequence)); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	w.Close()

	// Give time for processing
	time.Sleep(50 * time.Millisecond)

	// Get ANSI output line by line
	line0 := term.ANSILine(0)
	t.Logf("ANSILine(0): %q", line0)

	if line0 == "" {
		t.Errorf("ANSILine returned empty string!")
	}

	if !bytes.Contains([]byte(line0), []byte("Hello")) {
		t.Errorf("Expected 'Hello' in line, got: %q", line0)
	}
}

func TestEmojiOverwriteThroughTTY(t *testing.T) {
	// Test the round-trip: write through TTY frontend, capture ANSI output,
	// parse it back into another terminal
	var outBuf bytes.Buffer

	outerFrontend := NewTTYFrontend(nil, &outBuf)
	pr, pw := io.Pipe()
	backend := NewNoPTYBackend(pr, io.Discard)
	outer := NewWithMode(outerFrontend, backend, TextReadModeRune).(*terminal)
	outerFrontend.SetTerminal(outer)
	outer.Resize(80, 8)

	// Write the sequence: "🐹c\x1b[D\x1b[Dy"
	sequence := "🐹c\x1b[D\x1b[Dy"
	if _, err := pw.Write([]byte(sequence)); err != nil {
		t.Fatalf("Failed to write sequence: %v", err)
	}

	// Give it time to process
	time.Sleep(10 * time.Millisecond)

	// Check what the outer terminal has
	line := outer.Line(0)
	t.Logf("Outer line 0: %q", line)

	if len(line) < 4 || line[0:4] != "🐹" {
		t.Errorf("Expected emoji at start, got: %q", line)
	}
	// The line should be "🐹yc"
	if len(line) < 6 || line[0:6] != "🐹yc" {
		t.Errorf("Expected '🐹yc' but got: %q", line[0:min(10, len(line))])
	}
}
