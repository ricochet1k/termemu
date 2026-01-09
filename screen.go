package termemu

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

type screen struct {
	chars       [][]rune
	backColors  [][]Color
	frontColors [][]Color
	frontend    Frontend

	frontColor Color
	backColor  Color

	// prealocated for fast copying
	frontColorBuf []Color
	backColorBuf  []Color

	size Pos

	cursorPos Pos

	topMargin, bottomMargin int

	autoWrap bool
}

func newScreen(f Frontend) *screen {
	s := &screen{
		frontend: f,
	}
	s.setSize(80, 14)
	s.setColors(ColDefault, ColDefault)
	s.bottomMargin = s.size.Y - 1
	s.eraseRegion(Region{X: 0, Y: 0, X2: s.size.X, Y2: s.size.Y}, CRClear)
	return s
}

type Pos struct {
	X int
	Y int
}

func (s *screen) getLine(y int) []rune {
	return s.chars[y]
}

func (s *screen) getLineColors(y int) ([]Color, []Color) {
	return s.frontColors[y], s.backColors[y]
}

func (s *screen) StyledLine(x, w, y int) *Line {
	text := s.getLine(y)
	fgs := s.frontColors[y]
	bgs := s.backColors[y]

	var spans []StyledSpan

	if w < 0 || x+w > len(fgs) {
		w = len(fgs) - x
	}

	for i := x; i < x+w; {
		fg := fgs[i]
		bg := bgs[i]
		width := uint32(1)
		i++

		for i < x+w && fg == fgs[i] && bg == bgs[i] {
			i++
			width++
		}
		spans = append(spans, StyledSpan{fg, bg, width})
	}
	return &Line{
		Spans: spans,
		Text:  append([]rune(nil), text[x:x+w]...), // copy
		Width: uint32(w),
	}
}

func (s *screen) StyledLines(r Region) []*Line {
	var lines []*Line
	for y := r.Y; y < r.Y2; y++ {
		lines = append(lines, s.StyledLine(r.X, r.X2-r.X, y))
	}
	return lines
}

func (s *screen) renderLineANSI(y int) string {
	line := s.getLine(y)
	fg := s.frontColors[y][0]
	bg := s.backColors[y][0]
	buf := bytes.NewBuffer(make([]byte, 0, len(line)+10))
	x := 0
	for x < len(line) {
		fg = s.frontColors[y][x]
		bg = s.backColors[y][x]
		buf.Write(ANSIEscape(fg, bg))

		for x < len(line) && fg == s.frontColors[y][x] && bg == s.backColors[y][x] {
			buf.WriteRune(line[x])
			x++
		}
	}
	return string(buf.Bytes())
}

func (s *screen) setColors(front Color, back Color) {
	s.frontColor = front
	s.backColor = back

	for i := range s.frontColorBuf {
		s.frontColorBuf[i] = front
	}
	for i := range s.backColorBuf {
		s.backColorBuf[i] = back
	}

	s.frontend.ColorsChanged(front, back)
}

func (s *screen) setSize(w, h int) {
	if w <= 0 || h <= 0 {
		panic("Size must be > 0")
	}

	// resize screen. copy current screen to upper-left corner of new screen

	minW := w
	if w > s.size.X {
		minW = s.size.X
	}

	rect := make([][]rune, h)
	raw := make([]rune, w*h)
	for i := range rect {
		rect[i], raw = raw[:w], raw[w:]
		if i < s.size.Y {
			copy(rect[i][:minW], s.chars[i][:minW])

			for x := minW; x < w; x++ {
				rect[i][x] = ' '
			}
		} else {
			for x := 0; x < w; x++ {
				rect[i][x] = ' '
			}
		}
	}
	s.chars = rect
	// fmt.Println("setSize", w, h, s.chars, s.chars[0], len(s.chars[0]))

	for pi, p := range []*[][]Color{&s.backColors, &s.frontColors} {
		col := s.backColor
		if pi == 1 {
			col = s.frontColor
		}

		rect := make([][]Color, h)
		raw := make([]Color, w*h)
		for i := range rect {
			rect[i], raw = raw[:w], raw[w:]
			if i < s.size.Y {
				copy(rect[i][:minW], (*p)[i][:minW])

				for x := minW; x < w; x++ {
					rect[i][x] = col
				}
			} else {
				for x := 0; x < w; x++ {
					rect[i][x] = col
				}
			}
		}
		*p = rect
	}

	s.bottomMargin = h - (s.size.Y - s.bottomMargin)

	s.size = Pos{X: w, Y: h}

	// TODO: Logic for cursor position on resize?
	if s.cursorPos.X > w {
		s.cursorPos.X = 0
	}
	if s.cursorPos.Y > h {
		s.cursorPos.Y = 0
	}

	s.frontColorBuf = make([]Color, w)
	s.backColorBuf = make([]Color, w)
	s.setColors(s.frontColor, s.backColor)
}

func (s *screen) eraseRegion(r Region, cr ChangeReason) {
	r = s.clampRegion(r)
	// fmt.Printf("eraseRegion: %#v\n", r)
	bytes := make([]rune, r.X2-r.X)
	for i := range bytes {
		bytes[i] = ' '
	}
	for i := r.Y; i < r.Y2; i++ {
		debugPrintln(debugErase, "erase: ", r.X, i, len(bytes))
		s.rawWriteRunes(r.X, i, bytes, cr)
	}
}

// This is a very raw write function. It wraps as necessary, but assumes all
// the bytes are printable bytes
func (s *screen) writeRunes(b []rune) {
	for len(b) > 0 {
		l := s.size.X - s.cursorPos.X
		if l > len(b) {
			l = len(b)
		}

		s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b[:l], CRText)
		b = b[l:]
		s.moveCursor(l, 0, true, true)
	}
}

// This is like writeRunes, but it moves existing runes to the right
func (s *screen) insertRunes(b []rune) {
	y := s.cursorPos.Y
	fsx := s.cursorPos.X
	fex := fsx + len(b)
	tsx := s.size.X - len(b)
	tex := s.size.X

	// first: move everything over
	copy(s.chars[y][tsx:tex], s.chars[y][fsx:fex])
	copy(s.frontColors[y][tsx:tex], s.frontColors[y][fsx:fex])
	copy(s.backColors[y][tsx:tex], s.backColors[y][fsx:fex])
	// TODO! RegionChanged!

	s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b, CRText)
}

// This is a very raw write function. It assumes all the bytes are printable bytes
// If you use this to write beyond the end of the line, it will panic.
func (s *screen) rawWriteRunes(x int, y int, b []rune, cr ChangeReason) {
	if y >= s.size.Y || x+len(b) > s.size.X {
		panic(fmt.Sprintf("rawWriteBytes out of range: %v  %v,%v,%v %v %#v, %v,%v\n", s.size, x, y, x+len(b), len(b), string(b), len(s.chars), len(s.chars[0])))
	}
	copy(s.chars[y][x:x+len(b)], b)
	s.rawWriteColors(y, x, x+len(b))
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + len(b)}, cr)
}

// rawWriteColors copies one line of current colors to the screen, from x1 to x2
func (s *screen) rawWriteColors(y int, x1 int, x2 int) {
	copy(s.frontColors[y][x1:x2], s.frontColorBuf[x1:x2])
	copy(s.backColors[y][x1:x2], s.backColorBuf[x1:x2])
}

// deleteChars removes n characters from (x,y), shifts the remainder left,
// and fills the tail with spaces using current colors.
func (s *screen) deleteChars(x int, y int, n int, cr ChangeReason) {
	if y < 0 || y >= s.size.Y || n <= 0 {
		return
	}
	if x < 0 {
		n += x
		x = 0
	}
	if x >= s.size.X || n <= 0 {
		return
	}
	if x+n > s.size.X {
		n = s.size.X - x
	}

	line := s.chars[y]
	copy(line[x:], line[x+n:])
	for i := s.size.X - n; i < s.size.X; i++ {
		line[i] = ' '
	}

	fg := s.frontColors[y]
	bg := s.backColors[y]
	copy(fg[x:], fg[x+n:])
	copy(bg[x:], bg[x+n:])
	s.rawWriteColors(y, s.size.X-n, s.size.X)

	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

// func (s *screen) advanceLine(auto bool) {
// 	s.cursorPos.X = 0
// 	s.cursorPos.Y += 1
// 	if s.cursorPos.Y >= s.size.Y {
// 		// s.cursorPos.Y = 0 // TODO: scroll?
// 		s.cursorPos.Y = s.size.Y - 1
// 		s.scroll(s.topMargin, s.bottomMargin, -1)
// 	}
// 	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
// }

func (s *screen) setCursorPos(x, y int) {
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
	debugPrintln(debugCursor, "cursor set: ", x, y, s.cursorPos, s.size)
}

func (s *screen) setScrollMarginTopBottom(top, bottom int) {
	debugPrintln(debugScroll, "scroll margins:", top, bottom)
	s.topMargin = clamp(top, 0, s.size.Y-1)
	s.bottomMargin = clamp(bottom, 0, s.size.Y-1)
}

func (s *screen) scroll(y1 int, y2 int, dy int) {
	debugPrintln(debugScroll, "scroll:", y1, y2, dy)
	y1 = clamp(y1, 0, s.size.Y-1)
	y2 = clamp(y2, 0, s.size.Y-1)
	// if y < s.topMargin || y > s.bottomMargin {
	// 	fmt.Println("scroll outside margin", y, s.topMargin, s.bottomMargin)
	// }
	if y1 > y2 {
		fmt.Fprintln(os.Stderr, "scroll ys out of order", y1, y2, dy)
	}

	if dy > 0 {
		for y := y2; y >= y1+dy; y-- {
			// fmt.Println("   2: ", y, y2, y1+dy)
			copy(s.chars[y], s.chars[y-dy])
			copy(s.frontColors[y], s.frontColors[y-dy])
			copy(s.backColors[y], s.backColors[y-dy])
		}
		// these are non-inclusive, so need +1
		s.frontend.RegionChanged(Region{Y: y1 + dy, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
		debugPrintln(debugScroll, "scroll changed region:", Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X})
		s.eraseRegion(Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X}, CRScroll)
	} else {
		for y := y1; y <= y2+dy; y++ {
			// fmt.Println("   3: ", y, y1, y2+dy)
			copy(s.chars[y], s.chars[y-dy])
			copy(s.frontColors[y], s.frontColors[y-dy])
			copy(s.backColors[y], s.backColors[y-dy])
		}
		// these are non-inclusive, so need +1
		s.frontend.RegionChanged(Region{Y: y1, Y2: y2 + dy + 1, X: 0, X2: s.size.X}, CRScroll)
		s.eraseRegion(Region{Y: y2 + dy + 1, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
	}
}

func clamp(v int, low, high int) int {
	if v < low {
		v = low
	}
	if v > high {
		v = high
	}
	return v
}

func (s *screen) clampRegion(r Region) Region {
	r.X = clamp(r.X, 0, s.size.X)
	r.Y = clamp(r.Y, 0, s.size.Y)
	r.X2 = clamp(r.X2, 0, s.size.X)
	r.Y2 = clamp(r.Y2, 0, s.size.Y)
	return r
}

func (s *screen) moveCursor(dx, dy int, wrap bool, scroll bool) {
	if wrap && s.autoWrap {
		s.cursorPos.X += dx
		for s.cursorPos.X < 0 {
			s.cursorPos.X += s.size.X
			s.cursorPos.Y--
		}
		for s.cursorPos.X >= s.size.X {
			s.cursorPos.X -= s.size.X
			s.cursorPos.Y++
		}
	} else {
		s.cursorPos.X += dx
		s.cursorPos.X = clamp(s.cursorPos.X, 0, s.size.X-1)
	}

	s.cursorPos.Y += dy
	if scroll {
		if s.cursorPos.Y < s.topMargin {
			s.scroll(s.topMargin, s.bottomMargin, s.topMargin-s.cursorPos.Y)
			s.cursorPos.Y = s.topMargin
		}
		if s.cursorPos.Y > s.bottomMargin {
			s.scroll(s.topMargin, s.bottomMargin, s.bottomMargin-s.cursorPos.Y)
			s.cursorPos.Y = s.bottomMargin
		}
	} else {
		/*for s.cursorPos.Y < 0 {
			s.cursorPos.Y += s.size.Y
		}
		for s.cursorPos.Y >= s.size.Y {
			s.cursorPos.Y -= s.size.Y
		}*/
		s.cursorPos.Y = clamp(s.cursorPos.Y, 0, s.size.Y-1)
	}
	if s.cursorPos.Y >= s.size.Y {
		panic(fmt.Sprintf("moveCursor outside, %v %v  %v, %v, %v, %v", s.cursorPos, s.size, dx, dy, wrap, scroll))
	}
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
	//debugPrintf(debugCursor, "cursor move: %v, %v  %v, %v: %v %v\n", s.cursorPos.X, s.cursorPos.Y, dx, dy, wrap, scroll)
}

func (s *screen) printScreen() {
	w, h := s.size.X, s.size.Y
	fmt.Print("+")
	for i := 0; i < w; i++ {
		fmt.Print("-")
	}
	fmt.Println("+")
	for i := 0; i < h; i++ {
		lstr := string(s.renderLineANSI(i))
		lstr = strings.Replace(lstr, "\000", " ", -1)
		fmt.Printf("\033[m|%s\033[m|\n", lstr)
	}
	fmt.Print("+")
	for i := 0; i < w; i++ {
		fmt.Print("-")
	}
	fmt.Println("+")
}
