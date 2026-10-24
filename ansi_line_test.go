package termemu

import (
	"testing"
)

// TestANSILineRoundTripsStyles checks that renderLineANSI output is
// self-contained and reproduces the screen cell-for-cell, including mode
// resets and colors returning to default. The renderer emits a single leading
// reset plus style deltas, so this guards the diff logic.
func TestANSILineRoundTripsStyles(t *testing.T) {
	const input = "\x1b[31mred\x1b[0m default \x1b[1;44mboldbg\x1b[0m tail" +
		"\x1b[38;2;10;20;30mtruecolor\x1b[39m end"

	src := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	if err := src.Resize(80, 3); err != nil {
		t.Fatal(err)
	}
	if err := src.testFeedTerminalInputFromBackend([]byte(input), TextReadModeRune); err != nil {
		t.Fatal(err)
	}

	ansi := src.ANSILine(0)
	if ansi == "" {
		t.Fatal("ANSILine returned empty string")
	}

	dst := newTerminal(&EmptyFrontend{}, NewNoPTYBackend(nil, nil), TextReadModeRune)
	if err := dst.Resize(80, 3); err != nil {
		t.Fatal(err)
	}
	if err := dst.testFeedTerminalInputFromBackend([]byte(ansi), TextReadModeRune); err != nil {
		t.Fatal(err)
	}

	want := src.StyledLine(0, 80, 0)
	got := dst.StyledLine(0, 80, 0)
	if !want.Equivalent(got) {
		t.Fatalf("ANSILine round-trip differs:\n want %#v\n got  %#v\n ansi %q", want, got, ansi)
	}
}
