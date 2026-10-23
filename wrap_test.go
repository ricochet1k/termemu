package termemu

import (
	"strings"
	"testing"
)

func newWrapTerm(t *testing.T, w, h int) *terminal {
	t.Helper()
	_, term, _ := MakeTerminalWithMock(TextReadModeRune)
	if err := term.Resize(w, h); err != nil {
		t.Fatalf("Resize(%d, %d): %v", w, h, err)
	}
	return term
}

// TestPendingWrap_ExactFill verifies that writing the last cell leaves the
// cursor on that cell with a pending wrap instead of eagerly moving to the
// next row.
func TestPendingWrap_ExactFill(t *testing.T) {
	term := newWrapTerm(t, 20, 5)
	feedText(t, term, strings.Repeat("A", 20))

	ss := term.mainScreen.(*spanScreen)
	if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
		t.Fatalf("cursor after exact fill = %+v, want %+v", got, want)
	}
	if !ss.pendingWrap {
		t.Fatal("exact fill must set pending wrap")
	}
	if ss.wrapped[0] || ss.wrapped[1] {
		t.Fatalf("exact fill must not set wrap flags: %v %v", ss.wrapped[0], ss.wrapped[1])
	}

	feedText(t, term, "B")
	if got, want := term.CursorPos(), (Pos{X: 1, Y: 1}); got != want {
		t.Fatalf("cursor after pending wrap = %+v, want %+v", got, want)
	}
	if ss.pendingWrap {
		t.Fatal("writing after the wrap must clear pending wrap")
	}
	if !ss.wrapped[1] {
		t.Fatal("the row written after a pending wrap must be a continuation")
	}
	if got := term.Line(0); got != strings.Repeat("A", 20) {
		t.Fatalf("line 0 = %q", got)
	}
	if got := strings.TrimRight(term.Line(1), " "); got != "B" {
		t.Fatalf("line 1 = %q, want %q", got, "B")
	}
}

// TestPendingWrap_CRLFNoBlankContinuation verifies that CR/LF after an exact
// fill does not leave a blank wrapped row, and that the next text starts a
// new logical line.
func TestPendingWrap_CRLFNoBlankContinuation(t *testing.T) {
	term := newWrapTerm(t, 20, 5)
	sb := term.Scrollback()
	sb.Clear()

	feedText(t, term, strings.Repeat("A", 20))
	feedText(t, term, "\r\n")
	feedText(t, term, "B")

	ss := term.mainScreen.(*spanScreen)
	if ss.wrapped[1] {
		t.Fatal("LF after an exact fill must not mark the next row as wrapped")
	}
	if ss.pendingWrap {
		t.Fatal("CR/LF must clear pending wrap")
	}
	if got, want := term.CursorPos(), (Pos{X: 1, Y: 1}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}

	// Push both lines into scrollback and check their logical lines.
	feedText(t, term, strings.Repeat("\n", 8))
	base, _ := sb.Range()
	first := sb.LogicalLine(base)
	if len(first) != 1 {
		t.Fatalf("first logical line has %d rows, want 1", len(first))
	}
	if got := strings.TrimRight(first[0].PlainTextString(), " "); got != strings.Repeat("A", 20) {
		t.Fatalf("first logical line = %q", got)
	}
	second := sb.LogicalLine(base + 1)
	if len(second) != 1 {
		t.Fatalf("second logical line has %d rows, want 1", len(second))
	}
	if got := strings.TrimRight(second[0].PlainTextString(), " "); got != "B" {
		t.Fatalf("second logical line = %q, want %q", got, "B")
	}
}

// TestPendingWrap_BottomMarginScroll verifies that a pending wrap at the
// bottom margin scrolls, keeping the logical line intact in scrollback.
func TestPendingWrap_BottomMarginScroll(t *testing.T) {
	term := newWrapTerm(t, 20, 3)
	sb := term.Scrollback()
	sb.Clear()

	feedText(t, term, strings.Repeat("A", 60))
	feedText(t, term, "B")

	ss := term.mainScreen.(*spanScreen)
	if got, want := term.CursorPos(), (Pos{X: 1, Y: 2}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
	if ss.pendingWrap {
		t.Fatal("pending wrap must be cleared after wrapping")
	}
	if !ss.wrapped[2] {
		t.Fatal("wrapped row must be marked as a continuation")
	}
	base, next := sb.Range()
	if next-base != 1 {
		t.Fatalf("scrollback rows = %d, want 1", next-base)
	}
	logical := sb.LogicalLine(base)
	if len(logical) != 1 {
		t.Fatalf("scrolled logical line has %d rows, want 1", len(logical))
	}
	if got := strings.TrimRight(logical[0].PlainTextString(), " "); got != strings.Repeat("A", 20) {
		t.Fatalf("scrolled logical line = %q", got)
	}
}

// TestPendingWrap_WideGrapheme covers a wide grapheme that does not fit at the
// last column and one that exactly fills the line.
func TestPendingWrap_WideGrapheme(t *testing.T) {
	t.Run("wrap before write", func(t *testing.T) {
		term := newWrapTerm(t, 20, 3)
		feedText(t, term, strings.Repeat("A", 19))
		feedText(t, term, "🐹")

		ss := term.mainScreen.(*spanScreen)
		if got, want := term.CursorPos(), (Pos{X: 2, Y: 1}); got != want {
			t.Fatalf("cursor = %+v, want %+v", got, want)
		}
		if !ss.wrapped[1] {
			t.Fatal("wide grapheme wrapping must mark the new row as a continuation")
		}
		if got := strings.TrimRight(term.Line(0), " "); got != strings.Repeat("A", 19) {
			t.Fatalf("line 0 = %q", got)
		}
		if got := strings.TrimRight(term.Line(1), " "); got != "🐹" {
			t.Fatalf("line 1 = %q, want %q", got, "🐹")
		}
	})

	t.Run("exact fill leaves pending", func(t *testing.T) {
		term := newWrapTerm(t, 20, 3)
		feedText(t, term, strings.Repeat("A", 18))
		feedText(t, term, "🐹")

		ss := term.mainScreen.(*spanScreen)
		if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
			t.Fatalf("cursor = %+v, want %+v", got, want)
		}
		if !ss.pendingWrap {
			t.Fatal("a wide grapheme ending at the margin must leave pending wrap")
		}
		if ss.wrapped[1] {
			t.Fatal("exact fill must not mark the next row as wrapped")
		}
	})
}

// TestPendingWrap_CRClears verifies that CR clears pending wrap and returns to
// column 0 of the same row.
func TestPendingWrap_CRClears(t *testing.T) {
	term := newWrapTerm(t, 20, 3)
	feedText(t, term, strings.Repeat("A", 20))
	feedText(t, term, "\r")

	ss := term.mainScreen.(*spanScreen)
	if ss.pendingWrap {
		t.Fatal("CR must clear pending wrap")
	}
	if got, want := term.CursorPos(), (Pos{X: 0, Y: 0}); got != want {
		t.Fatalf("cursor after CR = %+v, want %+v", got, want)
	}

	feedText(t, term, "Z")
	if got, want := term.CursorPos(), (Pos{X: 1, Y: 0}); got != want {
		t.Fatalf("cursor after write = %+v, want %+v", got, want)
	}
	if got := term.Line(0); !strings.HasPrefix(got, "Z") {
		t.Fatalf("line 0 = %q, want it to start with Z", got)
	}
	if ss.wrapped[1] {
		t.Fatal("write after CR must stay on the same row")
	}
}

// TestPendingWrap_ExplicitMovesClear covers the control characters and CSI
// cursor movements that must clear pending wrap.
func TestPendingWrap_ExplicitMovesClear(t *testing.T) {
	tests := []struct {
		name string
		seq  string
	}{
		{name: "BS", seq: "\b"},
		{name: "TAB", seq: "\t"},
		{name: "CSI G", seq: "\x1b[5G"},
		{name: "CSI H", seq: "\x1b[1;1H"},
		{name: "CSI C", seq: "\x1b[1C"},
		{name: "CSI D", seq: "\x1b[1D"},
		{name: "CSI A", seq: "\x1b[1A"},
		{name: "CSI B", seq: "\x1b[1B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			term := newWrapTerm(t, 20, 5)
			feedText(t, term, strings.Repeat("A", 20))
			ss := term.mainScreen.(*spanScreen)
			if !ss.pendingWrap {
				t.Fatal("fixture did not set pending wrap")
			}

			feedText(t, term, tt.seq)
			if ss.pendingWrap {
				t.Fatalf("%s must clear pending wrap", tt.name)
			}
			// A subsequent write must not create a continuation row on
			// the moves that stay on row 0.
			if y := term.CursorPos().Y; y == 0 {
				feedText(t, term, "Z")
				if ss.wrapped[1] {
					t.Fatalf("%s: write after move unexpectedly wrapped", tt.name)
				}
			}
		})
	}
}

// TestPendingWrap_AutoWrapOff verifies DECAWM off keeps clamping and never
// sets pending wrap.
func TestPendingWrap_AutoWrapOff(t *testing.T) {
	term := newWrapTerm(t, 20, 3)
	feedText(t, term, "\x1b[?7l")
	feedText(t, term, strings.Repeat("A", 20))

	ss := term.mainScreen.(*spanScreen)
	if ss.pendingWrap {
		t.Fatal("DECAWM off must never set pending wrap")
	}
	if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}

	feedText(t, term, "B")
	if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
		t.Fatalf("cursor after clamp = %+v, want %+v", got, want)
	}
	if ss.wrapped[1] {
		t.Fatal("DECAWM off must not wrap onto the next row")
	}
	want := strings.Repeat("A", 19) + "B"
	if got := term.Line(0); got != want {
		t.Fatalf("line 0 = %q, want %q", got, want)
	}
}

// TestPendingWrap_CombiningMerges verifies a combining mark after an exact
// fill merges into the last written cell without wrapping.
func TestPendingWrap_CombiningMerges(t *testing.T) {
	term := newWrapTerm(t, 20, 3)
	feedText(t, term, strings.Repeat("A", 20))
	feedText(t, term, "\u0301")

	ss := term.mainScreen.(*spanScreen)
	if !ss.pendingWrap {
		t.Fatal("combining mark must not consume the pending wrap")
	}
	if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
	if ss.wrapped[1] {
		t.Fatal("combining mark must not wrap")
	}
	want := strings.Repeat("A", 19) + "A\u0301"
	if got := strings.TrimRight(term.Line(0), " "); got != want {
		t.Fatalf("line 0 = %q, want %q", got, want)
	}
}

// TestPendingWrap_DECSCRestore verifies DECSC/DECRC save and restore pending
// wrap alongside the cursor position.
func TestPendingWrap_DECSCRestore(t *testing.T) {
	term := newWrapTerm(t, 20, 3)
	feedText(t, term, strings.Repeat("A", 20))
	feedText(t, term, "\x1b7")
	feedText(t, term, "\r")

	ss := term.mainScreen.(*spanScreen)
	if ss.pendingWrap {
		t.Fatal("CR did not clear pending wrap")
	}

	feedText(t, term, "\x1b8")
	if !ss.pendingWrap {
		t.Fatal("DECRC did not restore pending wrap")
	}
	if got, want := term.CursorPos(), (Pos{X: 19, Y: 0}); got != want {
		t.Fatalf("cursor after DECRC = %+v, want %+v", got, want)
	}

	feedText(t, term, "B")
	if !ss.wrapped[1] {
		t.Fatal("write after DECRC did not wrap")
	}
	if got := strings.TrimRight(term.Line(1), " "); got != "B" {
		t.Fatalf("line 1 = %q, want %q", got, "B")
	}
}

// TestPendingWrap_ResetClears covers the reset paths that must clear pending
// wrap.
func TestPendingWrap_ResetClears(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, term *terminal)
	}{
		{name: "RIS", run: func(t *testing.T, term *terminal) {
			feedText(t, term, "\x1bc")
		}},
		{name: "DECSTR", run: func(t *testing.T, term *terminal) {
			feedText(t, term, "\x1b[!p")
		}},
		{name: "DECALN", run: func(t *testing.T, term *terminal) {
			feedText(t, term, "\x1b#8")
		}},
		{name: "resize", run: func(t *testing.T, term *terminal) {
			if err := term.Resize(20, 4); err != nil {
				t.Fatalf("Resize: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			term := newWrapTerm(t, 20, 5)
			feedText(t, term, strings.Repeat("A", 20))
			ss := term.mainScreen.(*spanScreen)
			if !ss.pendingWrap {
				t.Fatal("fixture did not set pending wrap")
			}
			tt.run(t, term)
			if ss.pendingWrap {
				t.Fatalf("%s must clear pending wrap", tt.name)
			}
		})
	}
}
