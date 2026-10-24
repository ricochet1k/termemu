package termemu

import "testing"

// TestIdempotentWriteSkipsRepaint verifies that rewriting cells with exactly
// the content they already hold does not churn spans or emit RegionChanged,
// while the cursor still advances as usual.
func TestIdempotentWriteSkipsRepaint(t *testing.T) {
	f := NewMockFrontend()
	s := newSpanScreen(f)
	s.setSize(10, 2)

	s.setCursorPos(0, 0)
	s.writeString("hello", 5, false, TextReadModeRune)
	regions := len(f.Regions)
	cursorMoves := f.CursorMovedCount
	if len(s.lines[0].spans) != 2 || s.lines[0].spans[0] != (Span{Style: NewStyle(), Text: "hello", Width: 5}) {
		t.Fatalf("initial write produced %+v, want text plus blank tail", s.lines[0].spans)
	}

	s.setCursorPos(0, 0)
	s.writeString("hello", 5, false, TextReadModeRune)
	if len(f.Regions) != regions {
		t.Fatalf("identical write emitted %d regions, want none", len(f.Regions)-regions)
	}
	if f.CursorMovedCount <= cursorMoves {
		t.Fatal("identical write did not report the cursor move")
	}
	if len(s.lines[0].spans) != 2 || s.lines[0].spans[0] != (Span{Style: NewStyle(), Text: "hello", Width: 5}) {
		t.Fatalf("identical write altered the line: %+v", s.lines[0].spans)
	}

	// A real change still reports exactly the affected cells.
	s.setCursorPos(2, 0)
	s.writeString("XY", 2, false, TextReadModeRune)
	if len(f.Regions) != regions+1 {
		t.Fatalf("changed write emitted %d regions, want 1", len(f.Regions)-regions)
	}
	got := f.Regions[len(f.Regions)-1].R
	if want := (Region{X: 2, X2: 4, Y: 0, Y2: 1}); got != want {
		t.Fatalf("changed write region = %+v, want %+v", got, want)
	}
	if text := s.Line(0); text != "heXYo     " {
		t.Fatalf("line = %q, want %q", text, "heXYo     ")
	}
}

// TestIdempotentStyledWriteSkipsRepaint covers the styled variant: the same
// style and text must not repaint, but a style change must.
func TestIdempotentStyledWriteSkipsRepaint(t *testing.T) {
	f := NewMockFrontend()
	s := newSpanScreen(f)
	s.setSize(10, 2)

	style := NewStyle()
	style.SetMode(ModeBold)
	s.setStyle(style)
	s.setCursorPos(0, 0)
	s.writeString("abc", 3, false, TextReadModeRune)
	regions := len(f.Regions)

	s.setCursorPos(0, 0)
	s.writeString("abc", 3, false, TextReadModeRune)
	if len(f.Regions) != regions {
		t.Fatalf("identical styled write emitted %d regions, want none", len(f.Regions)-regions)
	}

	other := NewStyle()
	other.SetMode(ModeItalic)
	s.setStyle(other)
	s.setCursorPos(0, 0)
	s.writeString("abc", 3, false, TextReadModeRune)
	if len(f.Regions) != regions+1 {
		t.Fatalf("style change emitted %d regions, want 1", len(f.Regions)-regions)
	}
	if s.lines[0].spans[0].Style != other {
		t.Fatal("style change was not applied")
	}
}

// TestIdempotentWriteCanonicalizesFragmentedWindow verifies that a write over
// an equivalent but multiply-packed window still goes through replaceRange so
// the resulting line is canonical.
func TestIdempotentWriteCanonicalizesFragmentedWindow(t *testing.T) {
	f := NewMockFrontend()
	s := newSpanScreen(f)
	s.setSize(10, 2)

	s.setCursorPos(0, 0)
	s.writeString("ab", 2, false, TextReadModeRune)
	s.setCursorPos(2, 0)
	s.writeString("cdef", 4, false, TextReadModeRune)
	if len(s.lines[0].spans) != 3 {
		t.Fatalf("setup produced %d spans, want 3", len(s.lines[0].spans))
	}

	s.setCursorPos(0, 0)
	s.writeString("abcdef", 6, false, TextReadModeRune)
	if len(s.lines[0].spans) != 2 || s.lines[0].spans[0] != (Span{Style: NewStyle(), Text: "abcdef", Width: 6}) {
		t.Fatalf("fragmented window was not canonicalized: %+v", s.lines[0].spans)
	}
}
