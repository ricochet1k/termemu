package termemu

import (
	"bytes"
	"strings"
	"testing"
)

func TestLineFeed_KeepsColumn(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abc\ndef")
	if got, want := term.Line(0), "abc                 "; got != want {
		t.Fatalf("line 0 = %q, want %q", got, want)
	}
	if got, want := term.Line(1), "   def              "; got != want && strings.TrimRight(got, " ") != strings.TrimRight(want, " ") {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
	if got, want := term.CursorPos(), (Pos{X: 6, Y: 1}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
}

func TestLineFeed_AtRightMargin(t *testing.T) {
	term := tabTerm(t, 5, 3)
	feedText(t, term, "abcde\nX")
	if got, want := term.Line(0), "abcde"; got != want {
		t.Fatalf("line 0 = %q, want %q", got, want)
	}
	if got, want := term.Line(1), "    X"; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
}

func TestVerticalTabAndFormFeed_KeepColumn(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "ab\vcd\fef")
	if got, want := strings.TrimRight(term.Line(0), " "), "ab"; got != want {
		t.Fatalf("line 0 = %q, want %q", got, want)
	}
	if got, want := strings.TrimRight(term.Line(1), " "), "  cd"; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
	if got, want := strings.TrimRight(term.Line(2), " "), "    ef"; got != want {
		t.Fatalf("line 2 = %q, want %q", got, want)
	}
}

func TestLNM_MakesLineFeedCarriageReturn(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abc\x1b[20h\ndef")
	if got, want := strings.TrimRight(term.Line(1), " "), "def"; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}

	feedText(t, term, "\x1b[20l") // and back to column-preserving feeds
	feedText(t, term, "\nX")
	if got, want := strings.TrimRight(term.Line(2), " "), "   X"; got != want {
		t.Fatalf("line 2 = %q, want %q", got, want)
	}
}

func TestLNM_Resets(t *testing.T) {
	// RIS clears LNM.
	term := tabTerm(t, 20, 4)
	feedText(t, term, "\x1b[20h")
	if !term.newlineMode {
		t.Fatal("LNM not enabled")
	}
	feedText(t, term, "\x1bc")
	if term.newlineMode {
		t.Fatal("RIS did not clear LNM")
	}

	// DECSTR leaves it alone: xterm's soft reset clears INSERT and ORIGIN but
	// not LINEFEED.
	term = tabTerm(t, 20, 4)
	feedText(t, term, "\x1b[20h\x1b[!p")
	if !term.newlineMode {
		t.Fatal("DECSTR must not clear LNM")
	}
}

func TestLNM_SnapshotRestore(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "\x1b[20h")
	snap := term.Snapshot()
	if !snap.NewlineMode {
		t.Fatal("snapshot lost LNM")
	}

	other := tabTerm(t, 20, 4)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if !other.newlineMode {
		t.Fatal("restored terminal lost LNM")
	}
}

func TestNEL_NextLine(t *testing.T) {
	term := tabTerm(t, 20, 4)
	feedText(t, term, "abc\x1bEdef")
	if got, want := strings.TrimRight(term.Line(1), " "), "def"; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
	// NEL is an index plus a carriage return, independent of LNM.
	term = tabTerm(t, 20, 4)
	feedText(t, term, "abc\x1b[20l\x1bEdef")
	if got, want := strings.TrimRight(term.Line(1), " "), "def"; got != want {
		t.Fatalf("line 1 = %q, want %q", got, want)
	}
}

func TestDECRQM_ReportsANSIModes(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)

	feedQueryExpectReply(t, t1, r, "\x1b[4$p", "\x1b[4;2$y") // IRM reset
	t1.mustHandleCommand(t, "[4h")
	feedQueryExpectReply(t, t1, r, "\x1b[4$p", "\x1b[4;1$y") // IRM set

	feedQueryExpectReply(t, t1, r, "\x1b[20$p", "\x1b[20;2$y") // LNM reset
	t1.mustHandleCommand(t, "[20h")
	feedQueryExpectReply(t, t1, r, "\x1b[20$p", "\x1b[20;1$y") // LNM set

	feedQueryExpectReply(t, t1, r, "\x1b[7$p", "\x1b[7;0$y") // unknown ANSI mode
}

func TestDECREQTPARM_ReportsParameters(t *testing.T) {
	r, t1, _ := MakeTerminalWithMock(TextReadModeRune)
	feedQueryExpectReply(t, t1, r, "\x1b[x", "\x1b[2;1;1;128;128;1;0x")
	feedQueryExpectReply(t, t1, r, "\x1b[1x", "\x1b[3;1;1;128;128;1;0x")
	// Unknown parameter values are ignored (no reply to wait for).
	if err := t1.testFeedTerminalInputFromBackend([]byte("\x1b[2x"), TextReadModeRune); err != nil {
		t.Fatal(err)
	}
}

func TestDECSCUSR_CursorShape(t *testing.T) {
	_, term, mf := MakeTerminalWithMock(TextReadModeRune)

	for _, shape := range []int{0, 1, 2, 3, 4, 5, 6} {
		feedText(t, term, "\x1b["+string(rune('0'+shape))+" q")
		if got := term.viewInts[VICursorShape]; got != shape {
			t.Fatalf("DECSCUSR %d stored as %d", shape, got)
		}
		if got := mf.ViewInts[VICursorShape]; got != shape {
			t.Fatalf("frontend saw shape %d, want %d", got, shape)
		}
	}

	// Missing parameter means 0.
	feedText(t, term, "\x1b[5 q\x1b[ q")
	if got := term.viewInts[VICursorShape]; got != CursorShapeDefault {
		t.Fatalf("DECSCUSR with no param = %d, want 0", got)
	}

	// Unknown shapes are ignored.
	feedText(t, term, "\x1b[3 q\x1b[9 q")
	if got := term.viewInts[VICursorShape]; got != CursorShapeBlinkUnderline {
		t.Fatalf("DECSCUSR 9 changed the shape to %d", got)
	}
}

func TestDECSCUSR_Resets(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"RIS", "\x1bc"},
		{"DECSTR", "\x1b[!p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, term, _ := MakeTerminalWithMock(TextReadModeRune)
			feedText(t, term, "\x1b[5 q")
			feedText(t, term, tc.seq)
			if got := term.viewInts[VICursorShape]; got != CursorShapeDefault {
				t.Fatalf("%s left shape %d, want 0", tc.name, got)
			}
		})
	}
}

func TestDECSCUSR_SnapshotRestore(t *testing.T) {
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	feedText(t, term, "\x1b[5 q")
	snap := term.Snapshot()

	_, other, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := other.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if got := other.viewInts[VICursorShape]; got != CursorShapeBlinkBar {
		t.Fatalf("restored shape = %d, want %d", got, CursorShapeBlinkBar)
	}
}

func TestTTYFrontend_ForwardsCursorShape(t *testing.T) {
	var buf bytes.Buffer
	frontend := NewTTYFrontend(&buf)
	term := NewWithMode(frontend, NewNoPTYBackend(bytes.NewReader(nil), nil), TextReadModeRune).(*terminal)
	if err := term.Resize(10, 2); err != nil {
		t.Fatal(err)
	}
	frontend.Attach(Region{X: 0, Y: 0, X2: 10, Y2: 2})
	buf.Reset()

	feedText(t, term, "\x1b[5 q")
	if got := buf.String(); !strings.Contains(got, "\x1b[5 q") {
		t.Fatalf("frontend output %q does not select the bar cursor", got)
	}

	buf.Reset()
	frontend.Detach()
	if got := buf.String(); !strings.Contains(got, "\x1b[0 q") {
		t.Fatalf("Detach output %q does not restore the default cursor", got)
	}
}

func TestTTYFrontend_AdoptsCursorShapeOnAttach(t *testing.T) {
	var buf bytes.Buffer
	frontend := NewTTYFrontend(&buf)
	term := NewWithMode(frontend, NewNoPTYBackend(bytes.NewReader(nil), nil), TextReadModeRune).(*terminal)
	if err := term.Resize(10, 2); err != nil {
		t.Fatal(err)
	}
	feedText(t, term, "\x1b[3 q")

	buf.Reset()
	frontend.Attach(Region{X: 0, Y: 0, X2: 10, Y2: 2})
	if got := buf.String(); !strings.Contains(got, "\x1b[3 q") {
		t.Fatalf("Attach output %q does not adopt the underline cursor", got)
	}
}
