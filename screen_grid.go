package termemu

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type gridScreen struct {
	chars       [][]rune
	cellText    [][]string
	cellWidth   [][]uint8
	cellCont    [][]bool
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

func newGridScreen(f Frontend) *gridScreen {
	s := &gridScreen{
		frontend: f,
	}
	s.setSize(80, 14)
	s.setColors(ColDefault, ColDefault)
	s.bottomMargin = s.size.Y - 1
	s.eraseRegion(Region{X: 0, Y: 0, X2: s.size.X, Y2: s.size.Y}, CRClear)
	return s
}

func (s *gridScreen) Size() Pos {
	return s.size
}

func (s *gridScreen) CursorPos() Pos {
	return s.cursorPos
}

func (s *gridScreen) FrontColor() Color {
	return s.frontColor
}

func (s *gridScreen) BackColor() Color {
	return s.backColor
}

func (s *gridScreen) AutoWrap() bool {
	return s.autoWrap
}

func (s *gridScreen) SetAutoWrap(value bool) {
	s.autoWrap = value
}

func (s *gridScreen) TopMargin() int {
	return s.topMargin
}

func (s *gridScreen) BottomMargin() int {
	return s.bottomMargin
}

func (s *gridScreen) SetFrontend(f Frontend) {
	s.frontend = f
}

func (s *gridScreen) getLine(y int) []rune {
	return s.chars[y]
}

func (s *gridScreen) getLineColors(y int) ([]Color, []Color) {
	return s.frontColors[y], s.backColors[y]
}

func (s *gridScreen) StyledLine(x, w, y int) *Line {
	text := s.getLine(y)
	fgs := s.frontColors[y]
	bgs := s.backColors[y]
	cellText := s.cellText[y]

	var spans []Span

	if w < 0 || x+w > len(fgs) {
		w = len(fgs) - x
	}

	for i := x; i < x+w; {
		fg := fgs[i]
		bg := bgs[i]
		width := 0
		start := i

		// Find run of identical colors
		for i < x+w && fg == fgs[i] && bg == bgs[i] {
			width++
			i++
		}

		// For this run, check if we can represent it as a single repeat or simple text
		// We have text[start:start+width] and cellText[start:start+width]

		// Optimization: check if all runes are same and simple (width 1)
		firstRune := text[start]
		isRepeat := true
		for k := 0; k < width; k++ {
			idx := start + k
			if text[idx] != firstRune || s.cellWidth[y][idx] != 1 || s.cellCont[y][idx] {
				isRepeat = false
				break
			}
		}

		if isRepeat {
			spans = append(spans, Span{FG: fg, BG: bg, Rune: firstRune, Width: width})
		} else {
			// Construct text for span
			var sb strings.Builder
			spanWidth := 0
			for k := 0; k < width; k++ {
				idx := start + k
				if s.cellCont[y][idx] {
					// continuation cell, don't add text, it's part of previous
					spanWidth++ // visual width increases
					continue
				}
				// It's a start of a char
				// If visual width is > 1, next cells will be cont.
				sb.WriteString(cellText[idx])
				spanWidth++
			}

			spans = append(spans, Span{FG: fg, BG: bg, Text: sb.String(), Width: spanWidth})
		}
	}
	return &Line{
		Spans: spans,
		Width: w,
	}
}

func (s *gridScreen) StyledLines(r Region) []*Line {
	var lines []*Line
	for y := r.Y; y < r.Y2; y++ {
		lines = append(lines, s.StyledLine(r.X, r.X2-r.X, y))
	}
	return lines
}

func (s *gridScreen) renderLineANSI(y int) string {
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
			if line[x] != 0 {
				buf.WriteRune(line[x])
			}
			x++
		}
	}
	return buf.String()
}

func (s *gridScreen) setColors(front Color, back Color) {
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

func (s *gridScreen) setSize(w, h int) {
	if w <= 0 || h <= 0 {
		panic("Size must be > 0")
	}

	// resize screen. copy current screen to upper-left corner of new screen

	prevW := s.size.X
	prevH := s.size.Y

	minW := w
	if w > prevW {
		minW = prevW
	}
	minH := h
	if h > prevH {
		minH = prevH
	}

	rect := make([][]rune, h)
	raw := make([]rune, w*h)
	for i := range rect {
		rect[i], raw = raw[:w], raw[w:]
		if i < prevH {
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

	cellText := makeStringGrid(w, h, " ")
	cellWidth := makeUint8Grid(w, h, 1)
	cellCont := makeBoolGrid(w, h, false)

	if len(s.cellText) > 0 {
		for y := 0; y < minH; y++ {
			copy(cellText[y][:minW], s.cellText[y][:minW])
		}
	}
	if len(s.cellWidth) > 0 {
		for y := 0; y < minH; y++ {
			copy(cellWidth[y][:minW], s.cellWidth[y][:minW])
		}
	}
	if len(s.cellCont) > 0 {
		for y := 0; y < minH; y++ {
			copy(cellCont[y][:minW], s.cellCont[y][:minW])
		}
	}

	s.cellText = cellText
	s.cellWidth = cellWidth
	s.cellCont = cellCont

	for pi, p := range []*[][]Color{&s.backColors, &s.frontColors} {
		col := s.backColor
		if pi == 1 {
			col = s.frontColor
		}

		rect := make([][]Color, h)
		raw := make([]Color, w*h)
		for i := range rect {
			rect[i], raw = raw[:w], raw[w:]
			if i < prevH {
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

func (s *gridScreen) eraseRegion(r Region, cr ChangeReason) {
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
func (s *gridScreen) writeRunes(b []rune) {
	for _, r := range b {
		width := runeCellWidth(r)
		if width > s.size.X {
			width = s.size.X
		}
		if s.cursorPos.X+width > s.size.X {
			if s.autoWrap {
				s.moveCursor(-s.cursorPos.X, 1, false, true)
			} else {
				s.cursorPos.X = s.size.X - width
			}
		}
		s.rawWriteRune(s.cursorPos.X, s.cursorPos.Y, r, width, CRText)
		s.moveCursor(width, 0, true, true)
	}
}

func (s *gridScreen) writeTokens(tokens []GraphemeToken) {
	for _, tok := range tokens {
		text := string(tok.Bytes)
		if len(text) == 0 {
			continue
		}
		if tok.Merge {
			s.mergeIntoPreviousCell(text)
			continue
		}
		if utf8.RuneCountInString(text) == 1 {
			r, _ := utf8.DecodeRuneInString(text)
			width := tok.Width
			if width < 1 {
				width = 1
			}
			if width > s.size.X {
				width = s.size.X
			}
			if s.cursorPos.X+width > s.size.X {
				if s.autoWrap {
					s.moveCursor(-s.cursorPos.X, 1, false, true)
				} else {
					s.cursorPos.X = s.size.X - width
				}
			}
			s.rawWriteRune(s.cursorPos.X, s.cursorPos.Y, r, width, CRText)
			s.moveCursor(width, 0, true, true)
			continue
		}
		for len(text) > 0 {
			r, size := utf8.DecodeRuneInString(text)
			if size == 0 || r == utf8.RuneError && size == 1 {
				break
			}
			text = text[size:]
			width := runeCellWidth(r)
			if width > s.size.X {
				width = s.size.X
			}
			if s.cursorPos.X+width > s.size.X {
				if s.autoWrap {
					s.moveCursor(-s.cursorPos.X, 1, false, true)
				} else {
					s.cursorPos.X = s.size.X - width
				}
			}
			s.rawWriteRune(s.cursorPos.X, s.cursorPos.Y, r, width, CRText)
			s.moveCursor(width, 0, true, true)
		}
	}
}

func (s *gridScreen) mergeIntoPreviousCell(text string) {
	if len(text) == 0 || s.cursorPos.X <= 0 {
		return
	}
	y := s.cursorPos.Y
	x := s.cursorPos.X - 1
	for x > 0 && s.cellCont[y][x] {
		x--
	}
	s.cellText[y][x] += text
}

// This is like writeRunes, but it moves existing runes to the right
func (s *gridScreen) insertRunes(b []rune) {
	y := s.cursorPos.Y
	fsx := s.cursorPos.X
	fex := fsx + len(b)
	tsx := s.size.X - len(b)
	tex := s.size.X

	// first: move everything over
	copy(s.chars[y][tsx:tex], s.chars[y][fsx:fex])
	copy(s.cellText[y][tsx:tex], s.cellText[y][fsx:fex])
	copy(s.cellWidth[y][tsx:tex], s.cellWidth[y][fsx:fex])
	copy(s.cellCont[y][tsx:tex], s.cellCont[y][fsx:fex])
	copy(s.frontColors[y][tsx:tex], s.frontColors[y][fsx:fex])
	copy(s.backColors[y][tsx:tex], s.backColors[y][fsx:fex])
	// TODO! RegionChanged!

	s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b, CRText)
}

// This is a very raw write function. It assumes all the bytes are printable bytes
// If you use this to write beyond the end of the line, it will panic.
func (s *gridScreen) rawWriteRunes(x int, y int, b []rune, cr ChangeReason) {
	if y >= s.size.Y || x+len(b) > s.size.X {
		panic(fmt.Sprintf("rawWriteBytes out of range: %v  %v,%v,%v %v %#v, %v,%v\n", s.size, x, y, x+len(b), len(b), string(b), len(s.chars), len(s.chars[0])))
	}
	textRow := s.cellText[y]
	widthRow := s.cellWidth[y]
	contRow := s.cellCont[y]
	for i, r := range b {
		idx := x + i
		if contRow[idx] {
			s.clearWideAt(y, idx)
		}
		s.chars[y][idx] = r
		textRow[idx] = string(r)
		widthRow[idx] = 1
		contRow[idx] = false
	}
	s.rawWriteColors(y, x, x+len(b))
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + len(b)}, cr)
}

func (s *gridScreen) rawWriteRune(x int, y int, r rune, width int, cr ChangeReason) {
	if width < 1 {
		width = 1
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteRune out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+width, width, string(r)))
	}
	if s.cellCont[y][x] {
		s.clearWideAt(y, x)
	}

	prevWidth := int(s.cellWidth[y][x])
	if prevWidth < 1 {
		prevWidth = 1
	}

	s.chars[y][x] = r
	s.cellText[y][x] = string(r)
	s.cellWidth[y][x] = uint8(width)
	s.cellCont[y][x] = false
	for i := 1; i < width; i++ {
		idx := x + i
		s.chars[y][idx] = 0
		s.cellText[y][idx] = ""
		s.cellWidth[y][idx] = 0
		s.cellCont[y][idx] = true
	}
	for i := width; i < prevWidth; i++ {
		idx := x + i
		if idx >= s.size.X {
			break
		}
		s.chars[y][idx] = ' '
		s.cellText[y][idx] = " "
		s.cellWidth[y][idx] = 1
		s.cellCont[y][idx] = false
	}
	end := x + width
	if prevWidth > width {
		end = x + prevWidth
	}
	if end > s.size.X {
		end = s.size.X
	}
	s.rawWriteColors(y, x, end)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: end}, cr)
}

func (s *gridScreen) clearWideAt(y int, x int) {
	base := x
	for base > 0 && s.cellCont[y][base] {
		base--
	}
	width := int(s.cellWidth[y][base])
	if width <= 1 {
		return
	}
	end := min(base+width, s.size.X)
	for i := base; i < end; i++ {
		s.chars[y][i] = ' '
		s.cellText[y][i] = " "
		s.cellWidth[y][i] = 1
		s.cellCont[y][i] = false
	}
	s.rawWriteColors(y, base, end)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: base, X2: end}, CRClear)
}

func runeCellWidth(r rune) int {
	if r == 0 {
		return 1
	}
	width := uniseg.StringWidth(string(r))
	if width <= 0 {
		return 1
	}
	return width
}

// rawWriteColors copies one line of current colors to the screen, from x1 to x2
func (s *gridScreen) rawWriteColors(y int, x1 int, x2 int) {
	copy(s.frontColors[y][x1:x2], s.frontColorBuf[x1:x2])
	copy(s.backColors[y][x1:x2], s.backColorBuf[x1:x2])
}

// deleteChars removes n characters from (x,y), shifts the remainder left,
// and fills the tail with spaces using current colors.
func (s *gridScreen) deleteChars(x int, y int, n int, cr ChangeReason) {
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
	textLine := s.cellText[y]
	copy(textLine[x:], textLine[x+n:])
	widthLine := s.cellWidth[y]
	copy(widthLine[x:], widthLine[x+n:])
	contLine := s.cellCont[y]
	copy(contLine[x:], contLine[x+n:])
	for i := s.size.X - n; i < s.size.X; i++ {
		line[i] = ' '
		textLine[i] = " "
		widthLine[i] = 1
		contLine[i] = false
	}

	fg := s.frontColors[y]
	bg := s.backColors[y]
	copy(fg[x:], fg[x+n:])
	copy(bg[x:], bg[x+n:])
	s.rawWriteColors(y, s.size.X-n, s.size.X)

	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

// func (s *gridScreen) advanceLine(auto bool) {
// 	s.cursorPos.X = 0
// 	s.cursorPos.Y += 1
// 	if s.cursorPos.Y >= s.size.Y {
// 		// s.cursorPos.Y = 0 // TODO: scroll?
// 		s.cursorPos.Y = s.size.Y - 1
// 		s.scroll(s.topMargin, s.bottomMargin, -1)
// 	}
// 	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
// }

func (s *gridScreen) setCursorPos(x, y int) {
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
	debugPrintln(debugCursor, "cursor set: ", x, y, s.cursorPos, s.size)
}

func (s *gridScreen) setScrollMarginTopBottom(top, bottom int) {
	debugPrintln(debugScroll, "scroll margins:", top, bottom)
	s.topMargin = clamp(top, 0, s.size.Y-1)
	s.bottomMargin = clamp(bottom, 0, s.size.Y-1)
}

func (s *gridScreen) scroll(y1 int, y2 int, dy int) {
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
			copy(s.cellText[y], s.cellText[y-dy])
			copy(s.cellWidth[y], s.cellWidth[y-dy])
			copy(s.cellCont[y], s.cellCont[y-dy])
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
			copy(s.cellText[y], s.cellText[y-dy])
			copy(s.cellWidth[y], s.cellWidth[y-dy])
			copy(s.cellCont[y], s.cellCont[y-dy])
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

func (s *gridScreen) clampRegion(r Region) Region {
	r.X = clamp(r.X, 0, s.size.X)
	r.Y = clamp(r.Y, 0, s.size.Y)
	r.X2 = clamp(r.X2, 0, s.size.X)
	r.Y2 = clamp(r.Y2, 0, s.size.Y)
	return r
}

func (s *gridScreen) moveCursor(dx, dy int, wrap bool, scroll bool) {
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

func (s *gridScreen) printScreen() {
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

func makeStringGrid(w, h int, fill string) [][]string {
	grid := make([][]string, h)
	raw := make([]string, w*h)
	for i := range grid {
		grid[i], raw = raw[:w], raw[w:]
		for x := 0; x < w; x++ {
			grid[i][x] = fill
		}
	}
	return grid
}

func makeUint8Grid(w, h int, fill uint8) [][]uint8 {
	grid := make([][]uint8, h)
	raw := make([]uint8, w*h)
	for i := range grid {
		grid[i], raw = raw[:w], raw[w:]
		for x := 0; x < w; x++ {
			grid[i][x] = fill
		}
	}
	return grid
}

func makeBoolGrid(w, h int, fill bool) [][]bool {
	grid := make([][]bool, h)
	raw := make([]bool, w*h)
	for i := range grid {
		grid[i], raw = raw[:w], raw[w:]
		for x := 0; x < w; x++ {
			grid[i][x] = fill
		}
	}
	return grid
}
