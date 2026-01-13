package termemu

import (
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type screenFactory struct {
	name string
	new  func(Frontend) screen
}

func screenFactories() []screenFactory {
	return []screenFactory{
		{name: "grid", new: func(f Frontend) screen { return newGridScreen(f) }},
		{name: "span", new: func(f Frontend) screen { return newSpanScreen(f) }},
	}
}

func forEachScreen(t *testing.T, fn func(t *testing.T, newFn func(Frontend) screen)) {
	t.Helper()
	for _, factory := range screenFactories() {
		factory := factory
		t.Run(factory.name, func(t *testing.T) {
			fn(t, factory.new)
		})
	}
}

func makeScreen(chars []string, newFn func(Frontend) screen) screen {
	s := newFn(&EmptyFrontend{})
	s.setSize(len(chars[0]), len(chars))
	for i := range chars {
		writeTextAt(s, 0, i, chars[i])
	}
	size := s.Size()
	if size.X != len(chars[0]) || size.Y != len(chars) {
		panic(fmt.Sprintf("Bad size: %+v", size))
	}
	return s
}

func writeTextAt(s screen, x, y int, text string) {
	if writer, ok := s.(interface {
		rawWriteSpan(int, int, Span, ChangeReason)
	}); ok {
		width := utf8.RuneCountInString(text)
		writer.rawWriteSpan(x, y, Span{Style: *s.FrontStyle(), Text: text, Width: width}, CRText)
		return
	}
	s.rawWriteRunes(x, y, []rune(text), CRText)
}

// writeTextAtWithWidth writes text at position (x, y) with explicit cell width.
// This is needed for wide characters like emoji that are 1 rune but 2+ cells wide.
func writeTextAtWithWidth(s screen, x, y int, text string, width int) {
	if writer, ok := s.(interface {
		rawWriteSpan(int, int, Span, ChangeReason)
	}); ok {
		writer.rawWriteSpan(x, y, Span{Style: *s.FrontStyle(), Text: text, Width: width}, CRText)
		return
	}
	s.rawWriteRunes(x, y, []rune(text), CRText)
}

func testScreen(s screen, chars []string) bool {
	for i := range chars {
		line := s.getLine(i)
		for j := range chars[i] {
			if line[j] != rune(chars[i][j]) {
				return false
			}
		}
	}
	return true
}

var ninePatch = []string{
	"112233",
	"112233",
	"445566",
	"445566",
	"778899",
	"778899",
}

func TestMe(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen(ninePatch, newFn)
		writeTextAt(s, 1, 1, "asdf")
		if testScreen(s, ninePatch) {
			s.printScreen()
			t.Errorf("Expected string to change and testScreen to report false")
		}
	})
}

func TestErase(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen(ninePatch, newFn)
		// non-inclusive
		s.eraseRegion(Region{X: 1, Y: 1, X2: 5, Y2: 5}, CRClear)
		if !testScreen(s, []string{
			"112233",
			"1    3",
			"4    6",
			"4    6",
			"7    9",
			"778899",
		}) {
			s.printScreen()
			t.Errorf("Expected middle of screen to be erased")
		}
	})
}

func TestScroll(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen(ninePatch, newFn)
		s.scroll(1, 4, -1)
		if !testScreen(s, []string{
			"112233",
			"445566",
			"445566",
			"778899",
			"      ",
			"778899",
		}) {
			s.printScreen()
			t.Errorf("Expected screen to scroll up")
		}

		s = makeScreen(ninePatch, newFn)
		s.scroll(1, 4, 1)
		if !testScreen(s, []string{
			"112233",
			"      ",
			"112233",
			"445566",
			"445566",
			"778899",
		}) {
			s.printScreen()
			t.Errorf("Expected screen to scroll down")
		}
	})
}

func TestStyledLine(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen(ninePatch, newFn)

		l := s.StyledLine(1, 2, 0)
		want := &Line{
			Spans: []Span{{Style: NewStyle(), Text: "12", Width: 2}},
			Width: 2,
		}
		if diff := cmp.Diff(l, want, cmpopts.IgnoreUnexported(Style{})); diff != "" {
			t.Errorf("s.StyledLine(1, 2, 0) diff: %s", diff)
		}
	})
}

func TestSetSize_PreservesContent(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen(ninePatch, newFn)

		old := make([][]rune, len(ninePatch))
		for y := range ninePatch {
			old[y] = append([]rune(nil), s.getLine(y)...)
		}

		// grow width and height
		s.setSize(8, 8)
		// original content should be at top-left
		for y := 0; y < len(ninePatch); y++ {
			for x := 0; x < len(ninePatch[0]); x++ {
				if got := s.getLine(y)[x]; got != old[y][x] {
					t.Fatalf("expected preserved char at %d,%d: got %q want %q", x, y, got, old[y][x])
				}
			}
		}

		// new areas should be spaces
		size := s.Size()
		for y := 0; y < size.Y; y++ {
			for x := 6; x < size.X; x++ {
				if got := s.getLine(y)[x]; got != ' ' {
					t.Fatalf("expected space at new area %d,%d; got %q", x, y, got)
				}
			}
		}
	})
}

func TestRawWriteRunes_RegionChanged(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		mf := NewMockFrontend()
		s := newFn(mf)
		s.setSize(6, 2)
		writeTextAt(s, 0, 0, "abcdef")

		s.setCursorPos(2, 0)

		s.deleteChars(2, 0, 2, CRClear)

		if got := string(s.getLine(0)); got != "abef  " {
			t.Fatalf("deleteChars result = %q; want %q", got, "abef  ")
		}
		if len(mf.Regions) == 0 {
			t.Fatalf("expected RegionChanged to be called")
		}
	})
}

func TestClearWideOverlaps_WriteOverWideChar(t *testing.T) {
	// This test specifically tests the span screen's clearWideOverlaps behavior
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(8, 2)

	// Write emoji at position 1 (takes cells 1-2) with width 2
	writeTextAtWithWidth(s, 1, 0, "🎉", 2)

	// Get the styled line to check what's there
	styledBefore := s.StyledLine(0, 8, 0)
	if len(styledBefore.Spans) == 0 {
		t.Errorf("Expected spans after writing emoji")
	}

	// Now write a narrow char at position 1 (overlaps the first half of emoji)
	// This should trigger clearWideOverlaps
	writeTextAt(s, 1, 0, "A")

	// Check that the emoji was cleared and 'A' was written
	line := s.getLine(0)
	if line[1] != 'A' {
		t.Errorf("Expected 'A' at position 1, got %q", line[1])
	}
	// Position 2 should be space (second cell of emoji was cleared)
	if line[2] != ' ' {
		t.Errorf("Expected space at position 2 (was second cell of emoji), got %q", line[2])
	}
}

func TestClearWideOverlaps_WriteAfterWideChar_NoInterference(t *testing.T) {
	// Test that writing AFTER a wide character doesn't affect it
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(8, 2)

	// Write emoji at position 1 with width 2
	writeTextAtWithWidth(s, 1, 0, "🎉", 2)
	line := s.getLine(0)

	// Verify emoji is there
	if line[1] == ' ' {
		t.Errorf("Expected emoji at position 1")
	}
	if line[2] != 0 {
		t.Errorf("Expected null byte at position 2")
	}

	// Write a char at position 3 (after the emoji, doesn't overlap)
	writeTextAt(s, 3, 0, "X")
	line = s.getLine(0)

	// Emoji should still be there
	if line[1] == ' ' {
		t.Errorf("Expected emoji still at position 1 after writing after it")
	}
	if line[2] != 0 {
		t.Errorf("Expected null byte still at position 2")
	}
	// X should be at position 3
	if line[3] != 'X' {
		t.Errorf("Expected 'X' at position 3, got %q", line[3])
	}
}

func TestClearWideOverlaps_EdgeCase_SplitInMiddle(t *testing.T) {
	// Test writing exactly at the second cell of a wide character
	s := newSpanScreen(&EmptyFrontend{})
	s.setSize(8, 2)

	// Write emoji at position 0 with width 2
	writeTextAtWithWidth(s, 0, 0, "🎉", 2)
	line := s.getLine(0)
	if line[0] == ' ' || line[1] != 0 {
		t.Errorf("Expected emoji at positions 0-1, got line: %q", string(line))
	}

	// Write at position 1 (the second cell of the emoji)
	// This should clear the emoji and write 'Y'
	writeTextAt(s, 1, 0, "Y")

	line = s.getLine(0)

	// Position 0 should be space (emoji cleared)
	if line[0] != ' ' {
		t.Errorf("Expected space at position 0 after write at emoji's second cell, got %q", line[0])
	}
	// Position 1 should be 'Y'
	if line[1] != 'Y' {
		t.Errorf("Expected 'Y' at position 1, got %q", line[1])
	}
}

func TestSplitSpan_RepeatMode(t *testing.T) {
	// Repeat mode spans (no wide clusters)
	sp := Span{Rune: ' ', Width: 5, Style: NewStyle()}

	left, right, brokeWide := splitSpan(sp, 2, TextReadModeGrapheme)

	if brokeWide {
		t.Errorf("Expected brokeWide=false for repeat mode")
	}
	if left.Width != 2 {
		t.Errorf("Expected left.Width=2, got %d", left.Width)
	}
	if right.Width != 3 {
		t.Errorf("Expected right.Width=3, got %d", right.Width)
	}
	if left.Rune != ' ' || right.Rune != ' ' {
		t.Errorf("Expected rune ' ' preserved in both, got left=%q right=%q", left.Rune, right.Rune)
	}
}

func TestSplitSpan_SimpleText(t *testing.T) {
	// Simple ASCII text (no wide characters)
	sp := Span{Text: "hello", Width: 5, Style: NewStyle()}

	left, right, brokeWide := splitSpan(sp, 2, TextReadModeRune)

	if brokeWide {
		t.Errorf("Expected brokeWide=false for simple ASCII")
	}
	if left.Text != "he" {
		t.Errorf("Expected left.Text='he', got %q", left.Text)
	}
	if right.Text != "llo" {
		t.Errorf("Expected right.Text='llo', got %q", right.Text)
	}
	if left.Width != 2 || right.Width != 3 {
		t.Errorf("Expected widths 2,3 got %d,%d", left.Width, right.Width)
	}
}

func TestSplitSpan_WideCharacterBeforeSplit(t *testing.T) {
	// Wide character before the split point - should split normally
	sp := Span{Text: "🎉hello", Width: 7, Style: NewStyle()} // emoji=2 cells, hello=5 cells

	left, right, brokeWide := splitSpan(sp, 3, TextReadModeGrapheme)

	if brokeWide {
		t.Errorf("Expected brokeWide=false, split is after the emoji")
	}
	if left.Width != 3 {
		t.Errorf("Expected left.Width=3, got %d", left.Width)
	}
	if right.Width != 4 {
		t.Errorf("Expected right.Width=4, got %d", right.Width)
	}
}

func TestSplitSpan_SplitInWideCharacter(t *testing.T) {
	// Split point falls within a wide emoji (width 2)
	sp := Span{Text: "🎉", Width: 2, Style: NewStyle()}

	left, right, brokeWide := splitSpan(sp, 1, TextReadModeGrapheme)

	if !brokeWide {
		t.Errorf("Expected brokeWide=true when splitting within wide char")
	}
	// Neither span should contain the broken wide character
	if left.Text != "" {
		t.Errorf("Expected left.Text='', got %q (should not include broken emoji)", left.Text)
	}
	if right.Text != "" {
		t.Errorf("Expected right.Text='', got %q (should not include broken emoji)", right.Text)
	}
	if left.Width != 0 || right.Width != 0 {
		t.Errorf("Expected both widths=0, got left=%d right=%d", left.Width, right.Width)
	}
}

func TestSplitSpan_WideCharAtEndOfSpan(t *testing.T) {
	// Wide character is at the end of the span
	sp := Span{Text: "hello🎉", Width: 7, Style: NewStyle()} // hello=5 cells, emoji=2 cells

	left, right, brokeWide := splitSpan(sp, 6, TextReadModeGrapheme)

	if !brokeWide {
		t.Errorf("Expected brokeWide=true when splitting within emoji at end")
	}
	// Left should have "hello", right should be empty
	if left.Text != "hello" {
		t.Errorf("Expected left.Text='hello', got %q", left.Text)
	}
	if right.Text != "" {
		t.Errorf("Expected right.Text='', got %q", right.Text)
	}
	if left.Width != 5 || right.Width != 0 {
		t.Errorf("Expected widths 5,0 got %d,%d", left.Width, right.Width)
	}
}

func TestSplitSpan_MultipleWideCharacters(t *testing.T) {
	// Multiple wide characters, split in the middle of one
	sp := Span{Text: "🎉🎉hello", Width: 9, Style: NewStyle()} // emoji+emoji=4, hello=5

	left, right, brokeWide := splitSpan(sp, 3, TextReadModeGrapheme)

	if !brokeWide {
		t.Errorf("Expected brokeWide=true when splitting within second emoji")
	}
	// Left should have first emoji (2 cells), right should have "hello" (5 cells)
	if left.Text != "🎉" {
		t.Errorf("Expected left.Text='🎉', got %q", left.Text)
	}
	if right.Text != "hello" {
		t.Errorf("Expected right.Text='hello', got %q", right.Text)
	}
	if left.Width != 2 || right.Width != 5 {
		t.Errorf("Expected widths 2,5 got %d,%d", left.Width, right.Width)
	}
}

func TestSplitSpan_BoundaryConditions(t *testing.T) {
	sp := Span{Text: "abc", Width: 3, Style: NewStyle()}

	// Split at 0
	left, right, brokeWide := splitSpan(sp, 0, TextReadModeRune)
	if left.Text != "" || left.Width != 0 || right.Text != "abc" {
		t.Errorf("Split at 0: expected empty left, full right; got left=%q(%d), right=%q", left.Text, left.Width, right.Text)
	}

	// Split at end
	left, right, brokeWide = splitSpan(sp, 3, TextReadModeRune)
	if right.Text != "" || right.Width != 0 || left.Text != "abc" {
		t.Errorf("Split at end: expected full left, empty right; got left=%q, right=%q(%d)", left.Text, right.Text, right.Width)
	}

	// Both should not break wide for normal text
	if brokeWide {
		t.Errorf("Expected brokeWide=false for normal ASCII boundaries")
	}
}
