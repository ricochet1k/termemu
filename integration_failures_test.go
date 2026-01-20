package termemu

import (
	"strings"
	"testing"
)

// TestCursorUp tests the cursor up escape sequence (CSI A)
// Integration test "cursor_up" was failing - cursor moves up but text placement is wrong
func TestCursorUp(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	screen := term.screen()
	width := screen.Size().X

	// Trace what happens step by step
	term.testFeedTerminalInputFromBackend([]byte("Line1"), TextReadModeRune)
	t.Logf("After 'Line1':")
	t.Logf("  Cursor: (%d, %d)", screen.CursorPos().X, screen.CursorPos().Y)
	t.Logf("  Line 0: %q", strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " "))

	term.testFeedTerminalInputFromBackend([]byte("\x1b[A"), TextReadModeRune)
	t.Logf("After cursor up:")
	t.Logf("  Cursor: (%d, %d)", screen.CursorPos().X, screen.CursorPos().Y)

	term.testFeedTerminalInputFromBackend([]byte("Line0"), TextReadModeRune)
	t.Logf("After 'Line0':")
	t.Logf("  Cursor: (%d, %d)", screen.CursorPos().X, screen.CursorPos().Y)
	t.Logf("  Line 0: %q", strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " "))
	t.Logf("  Line 1: %q", strings.TrimRight(screen.StyledLine(0, width, 1).PlainTextString(), " "))

	// Get the screen output
	line0 := strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " ")

	// Integration test shows tmux expects "Line1Line0" on a single line
	// This means after writing "Line1", cursor up should keep cursor at same X position
	// Then "Line0" overwrites starting from that position
	expected := "Line1Line0"
	if line0 != expected {
		t.Errorf("Expected %q on line 0, got %q", expected, line0)
	}
}

// TestBrightColorsForeground tests SGR 90-97 (bright foreground colors)
// Integration test shows we're outputting \x1b[38;5;8m instead of \x1b[90m
func TestBrightColorsForeground(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	screen := term.screen()
	width := screen.Size().X

	// *debugCmd = true
	// *debugTxt = true

	// Write bright colors
	input := "\x1b[90mDark\x1b[91mRed\x1b[92mGreen\x1b[93mYellow\x1b[39m"
	if err := term.testFeedTerminalInputFromBackend([]byte(input), TextReadModeRune); err != nil {
		t.Fatal(err)
	}

	// Check what was actually written
	line := strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " ")
	t.Logf("Text on line 0: %q", line)

	t.Logf("FirstSpanStyle line 0: %#v %q", screen.StyledLine(0, width, 0).Spans[0].Style, string(screen.StyledLine(0, width, 0).Spans[0].Style.ANSIEscape()))

	// Get ANSI output for the line
	ansi := term.screen().renderLineANSI(0)
	t.Logf("ANSI output: %q", ansi)

	// The text should at least be present
	if !strings.Contains(line, "Dark") {
		t.Errorf("Text 'Dark' not found on line, got: %q", line)
	}

	// Check if we're using the short form (90-97) or extended form (38;5;8-15)
	// tmux expects short form like \x1b[90m
	if !strings.Contains(ansi, "\x1b[90m") {
		if strings.Contains(ansi, "\x1b[38;5;8m") {
			t.Errorf("Using extended form \\x1b[38;5;8m instead of short form \\x1b[90m for bright black")
		} else {
			t.Errorf("Expected \\x1b[90m for bright black, ANSI output doesn't contain expected sequences")
		}
	}

	if !strings.Contains(ansi, "\x1b[91m") {
		if strings.Contains(ansi, "\x1b[38;5;9m") {
			t.Errorf("Using extended form \\x1b[38;5;9m instead of short form \\x1b[91m for bright red")
		}
	}
}

// TestBrightColorsBackground tests SGR 100-107 (bright background colors)
func TestBrightColorsBackground(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	// Write bright background colors
	term.testFeedTerminalInputFromBackend([]byte("\x1b[100mBG Dark\x1b[101mBG Red\x1b[102mBG Green\x1b[49m"), TextReadModeRune)

	// Get ANSI output
	ansi := term.screen().renderLineANSI(0)

	t.Logf("ANSI output: %q", ansi)

	// Check for short form (100-107) vs extended form (48;5;8-15)
	if !strings.Contains(ansi, "\x1b[100m") {
		if strings.Contains(ansi, "\x1b[48;5;8m") {
			t.Errorf("Using extended form \\x1b[48;5;8m instead of short form \\x1b[100m for bright black bg")
		}
	}

	if !strings.Contains(ansi, "\x1b[101m") {
		if strings.Contains(ansi, "\x1b[48;5;9m") {
			t.Errorf("Using extended form \\x1b[48;5;9m instead of short form \\x1b[101m for bright red bg")
		}
	}
}

// TestEraseAbove tests CSI 1J (erase from cursor to beginning of display)
func TestEraseAbove(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	screen := term.screen()
	width := screen.Size().X

	// Write 3 lines, then erase above current cursor position
	term.testFeedTerminalInputFromBackend([]byte("Top\nMiddle\nBottom\x1b[1JEnd"), TextReadModeRune)

	line1 := strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " ")
	line2 := strings.TrimRight(screen.StyledLine(0, width, 1).PlainTextString(), " ")
	line3 := strings.TrimRight(screen.StyledLine(0, width, 2).PlainTextString(), " ")

	t.Logf("Line 0: %q", line1)
	t.Logf("Line 1: %q", line2)
	t.Logf("Line 2: %q", line3)

	// After CSI 1J, everything above and including the current cursor position should be erased
	// Cursor is at line 2 after "Bottom", so lines 0-2 should be cleared up to cursor
	// Expected line 2: "      End" (6 spaces from "Bottom" cleared, then "End")
	// Actual line 2: "BottomEnd" (not erased)

	if line3 == "BottomEnd" {
		t.Errorf("Line 2 should have 'Bottom' erased before 'End', got: %q", line3)
	}
}

// TestEraseBelow tests CSI 0J or CSI J (erase from cursor to end of display)
func TestEraseBelow(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)

	screen := term.screen()
	width := screen.Size().X

	// Write 3 lines, then erase below current cursor position
	term.testFeedTerminalInputFromBackend([]byte("Top\nMiddle\nBottom\x1b[0JEnd"), TextReadModeRune)

	line1 := strings.TrimRight(screen.StyledLine(0, width, 0).PlainTextString(), " ")
	line2 := strings.TrimRight(screen.StyledLine(0, width, 1).PlainTextString(), " ")
	line3 := strings.TrimRight(screen.StyledLine(0, width, 2).PlainTextString(), " ")

	t.Logf("Line 0: %q", line1)
	t.Logf("Line 1: %q", line2)
	t.Logf("Line 2: %q", line3)

	// After CSI 0J, everything from cursor to end should be erased, then "End" written
	// Expected line 2: "BottomEnd" (erase from cursor onward, write End)
	// Actual line 2: "      End" (6 spaces)

	if line3 == "      End" || strings.HasPrefix(line3, "      ") {
		t.Errorf("Line 2 should be 'BottomEnd' not erased/spaced, got: %q", line3)
	}
}
