package termemu

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		s.rawWriteRunes(0, i, []rune(chars[i]), CRText)
	}
	size := s.Size()
	if size.X != len(chars[0]) || size.Y != len(chars) {
		panic(fmt.Sprintf("Bad size: %+v", size))
	}
	return s
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
		s.rawWriteRunes(1, 1, []rune("asdf"), CRText)
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
			Text:  []rune{'1', '2'},
			Spans: []StyledSpan{{FG: ColDefault, BG: ColDefault, Width: 2}},
			Width: 2,
		}
		if diff := cmp.Diff(l, want); diff != "" {
			t.Errorf("s.StyledLine(1, 2, 0) = %#v, want %#v", l, want)
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
		// write 'hi' at 1,0
		s.rawWriteRunes(1, 0, []rune("hi"), CRText)
		line := s.getLine(0)
		if line[1] != 'h' || line[2] != 'i' {
			t.Fatalf("rawWriteRunes did not write runes: %q", string(line))
		}
		if len(mf.Regions) == 0 {
			t.Fatalf("expected RegionChanged to be called")
		}
		r := mf.Regions[len(mf.Regions)-1]
		if r.R.X != 1 || r.R.Y != 0 || r.R.X2 != 3 || r.R.Y2 != 1 || r.C != CRText {
			t.Fatalf("unexpected RegionChanged: %#v", r)
		}
	})
}

func TestSetCursorPos_ClampsAndNotifies(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		mf := NewMockFrontend()
		s := newFn(mf)
		s.setSize(5, 4)
		s.setCursorPos(10, 10)
		pos := s.CursorPos()
		if pos.X != 4 || pos.Y != 3 {
			t.Fatalf("expected cursor clamped to (4,3), got (%d,%d)", pos.X, pos.Y)
		}
		if mf.CursorMovedCount == 0 {
			t.Fatalf("expected CursorMoved to be called")
		}
		// negative values clamp to 0
		s.setCursorPos(-5, -2)
		pos = s.CursorPos()
		if pos.X != 0 || pos.Y != 0 {
			t.Fatalf("expected cursor clamped to (0,0), got (%d,%d)", pos.X, pos.Y)
		}
	})
}

func TestMoveCursor_WrapAndScroll(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		mf := NewMockFrontend()
		s := newFn(mf)
		s.setSize(5, 4)
		// wrapping disabled: move beyond right clamps
		s.SetAutoWrap(false)
		s.setCursorPos(4, 1)
		s.moveCursor(2, 0, false, false)
		pos := s.CursorPos()
		if pos.X != 4 {
			t.Fatalf("expected clamp at right edge, got X=%d", pos.X)
		}

		// wrapping enabled: should wrap to next line
		s.SetAutoWrap(true)
		s.setCursorPos(4, 1)
		s.moveCursor(1, 0, true, false)
		pos = s.CursorPos()
		if pos.X != 0 || pos.Y != 2 {
			t.Fatalf("expected wrap to (0,2), got (%d,%d)", pos.X, pos.Y)
		}

		// scrolling: move beyond bottom with scroll true should scroll and clamp
		s.setScrollMarginTopBottom(0, 2)
		s.setCursorPos(0, 2)
		// move down by 2 with scroll true
		s.moveCursor(0, 2, false, true)
		// cursor Y should be set to bottomMargin
		pos = s.CursorPos()
		if pos.Y != s.BottomMargin() {
			t.Fatalf("expected cursor Y=%d after scroll, got %d", s.BottomMargin(), pos.Y)
		}
		if len(mf.Regions) == 0 {
			// scroll triggers RegionChanged and eraseRegion which calls RegionChanged; ensure some regions recorded
			t.Fatalf("expected RegionChanged calls during scroll")
		}
	})
}

func TestRenderLineANSI(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		s := makeScreen([]string{"abc"}, newFn)
		// set different colors per cell
		s.setColors(ColWhite.SetMode(ModeBold), ColBlack)
		s.rawWriteRune(0, 0, 'a', 1, CRText)
		s.setColors(ColRed, ColGreen)
		s.rawWriteRune(1, 0, 'b', 1, CRText)
		s.setColors(ColDefault, ColDefault)
		s.rawWriteRune(2, 0, 'c', 1, CRText)
		out := s.renderLineANSI(0)
		if out == "" {
			t.Fatalf("renderLineANSI returned empty string")
		}
		// should contain ANSI sequences and the text
		// ANSI codes may separate characters; ensure characters appear in order
		idx := strings.Index(out, "a")
		if idx < 0 {
			t.Fatalf("renderLineANSI missing 'a': %q", out)
		}
		idx2 := strings.Index(out[idx+1:], "b")
		if idx2 < 0 {
			t.Fatalf("renderLineANSI missing 'b' after 'a': %q", out)
		}
		idx3 := strings.Index(out[idx+1+idx2+1:], "c")
		if idx3 < 0 {
			t.Fatalf("renderLineANSI missing 'c' after 'b': %q", out)
		}
	})
}

func TestDeleteChars_ShiftsLeft(t *testing.T) {
	forEachScreen(t, func(t *testing.T, newFn func(Frontend) screen) {
		mf := NewMockFrontend()
		s := newFn(mf)
		s.setSize(6, 1)
		s.rawWriteRunes(0, 0, []rune("abcdef"), CRText)
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
