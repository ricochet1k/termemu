package termemu

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

type screen interface {
	Size() Pos
	CursorPos() Pos
	FrontColor() Color
	BackColor() Color
	AutoWrap() bool
	SetAutoWrap(value bool)
	TopMargin() int
	BottomMargin() int
	SetFrontend(f Frontend)

	getLine(y int) []rune
	getLineColors(y int) ([]Color, []Color)
	StyledLine(x, w, y int) *Line
	StyledLines(r Region) []*Line
	renderLineANSI(y int) string
	setColors(front Color, back Color)
	setSize(w, h int)
	eraseRegion(r Region, cr ChangeReason)
	writeRunes(b []rune)
	insertRunes(b []rune)
	rawWriteRunes(x int, y int, b []rune, cr ChangeReason)
	rawWriteRune(x int, y int, r rune, width int, cr ChangeReason)
	deleteChars(x int, y int, n int, cr ChangeReason)
	setCursorPos(x, y int)
	setScrollMarginTopBottom(top, bottom int)
	scroll(y1 int, y2 int, dy int)
	moveCursor(dx, dy int, wrap bool, scroll bool)
	printScreen()
}

// Pos holds X/Y coordinates.
type Pos struct {
	X int
	Y int
}

type spanScreen struct {
	lines    []spanLine
	frontend Frontend

	frontColor Color
	backColor  Color

	size Pos

	cursorPos Pos

	topMargin, bottomMargin int

	autoWrap bool
}

type spanLine struct {
	spans []span
}

type span struct {
	fg, bg Color
	cells  []spanCell
}

type spanCell struct {
	r     rune
	width uint8
	cont  bool
}

type styledCell struct {
	spanCell
	fg, bg Color
}

func newScreen(f Frontend) screen {
	return newSpanScreen(f)
}

func newSpanScreen(f Frontend) *spanScreen {
	s := &spanScreen{
		frontend: f,
	}
	s.setSize(80, 14)
	s.setColors(ColDefault, ColDefault)
	s.bottomMargin = s.size.Y - 1
	s.eraseRegion(Region{X: 0, Y: 0, X2: s.size.X, Y2: s.size.Y}, CRClear)
	return s
}

func (s *spanScreen) Size() Pos {
	return s.size
}

func (s *spanScreen) CursorPos() Pos {
	return s.cursorPos
}

func (s *spanScreen) FrontColor() Color {
	return s.frontColor
}

func (s *spanScreen) BackColor() Color {
	return s.backColor
}

func (s *spanScreen) AutoWrap() bool {
	return s.autoWrap
}

func (s *spanScreen) SetAutoWrap(value bool) {
	s.autoWrap = value
}

func (s *spanScreen) TopMargin() int {
	return s.topMargin
}

func (s *spanScreen) BottomMargin() int {
	return s.bottomMargin
}

func (s *spanScreen) SetFrontend(f Frontend) {
	s.frontend = f
}

func (s *spanScreen) getLine(y int) []rune {
	cells := s.lineCells(y)
	line := make([]rune, len(cells))
	for i, c := range cells {
		line[i] = c.r
	}
	return line
}

func (s *spanScreen) getLineColors(y int) ([]Color, []Color) {
	cells := s.lineCells(y)
	fg := make([]Color, len(cells))
	bg := make([]Color, len(cells))
	for i, c := range cells {
		fg[i] = c.fg
		bg[i] = c.bg
	}
	return fg, bg
}

func (s *spanScreen) StyledLine(x, w, y int) *Line {
	cells := s.lineCells(y)
	if w < 0 || x+w > len(cells) {
		w = len(cells) - x
	}
	if w < 0 {
		w = 0
	}

	text := make([]rune, w)
	spans := make([]StyledSpan, 0)
	for i := 0; i < w; {
		cell := cells[x+i]
		fg := cell.fg
		bg := cell.bg
		width := uint32(0)
		for i < w {
			cell = cells[x+i]
			if cell.fg != fg || cell.bg != bg {
				break
			}
			text[i] = cell.r
			i++
			width++
		}
		spans = append(spans, StyledSpan{FG: fg, BG: bg, Width: width})
	}

	return &Line{
		Spans: spans,
		Text:  text,
		Width: uint32(w),
	}
}

func (s *spanScreen) StyledLines(r Region) []*Line {
	var lines []*Line
	for y := r.Y; y < r.Y2; y++ {
		lines = append(lines, s.StyledLine(r.X, r.X2-r.X, y))
	}
	return lines
}

func (s *spanScreen) renderLineANSI(y int) string {
	cells := s.lineCells(y)
	if len(cells) == 0 {
		return ""
	}
	buf := bytes.NewBuffer(make([]byte, 0, len(cells)+10))
	idx := 0
	for idx < len(cells) {
		fg := cells[idx].fg
		bg := cells[idx].bg
		buf.Write(ANSIEscape(fg, bg))
		for idx < len(cells) && fg == cells[idx].fg && bg == cells[idx].bg {
			if cells[idx].r != 0 {
				buf.WriteRune(cells[idx].r)
			}
			idx++
		}
	}
	return buf.String()
}

func (s *spanScreen) setColors(front Color, back Color) {
	s.frontColor = front
	s.backColor = back

	s.frontend.ColorsChanged(front, back)
}

func (s *spanScreen) setSize(w, h int) {
	if w <= 0 || h <= 0 {
		panic("Size must be > 0")
	}

	prevH := s.size.Y

	newLines := make([]spanLine, h)
	for y := 0; y < h; y++ {
		switch {
		case y < prevH && len(s.lines) > 0:
			cells := s.lineCells(y)
			if len(cells) > w {
				cells = cells[:w]
			} else if len(cells) < w {
				cells = append(cells, makeSpaceCells(w-len(cells), s.frontColor, s.backColor)...)
			}
			newLines[y] = lineFromCells(cells)
		default:
			newLines[y] = blankSpanLine(w, s.frontColor, s.backColor)
		}
	}

	s.lines = newLines

	s.bottomMargin = h - (s.size.Y - s.bottomMargin)

	s.size = Pos{X: w, Y: h}

	if s.cursorPos.X > w {
		s.cursorPos.X = 0
	}
	if s.cursorPos.Y > h {
		s.cursorPos.Y = 0
	}

	s.setColors(s.frontColor, s.backColor)
}

func (s *spanScreen) eraseRegion(r Region, cr ChangeReason) {
	r = s.clampRegion(r)
	bytes := make([]rune, r.X2-r.X)
	for i := range bytes {
		bytes[i] = ' '
	}
	for i := r.Y; i < r.Y2; i++ {
		debugPrintln(debugErase, "erase: ", r.X, i, len(bytes))
		s.rawWriteRunes(r.X, i, bytes, cr)
	}
}

func (s *spanScreen) writeRunes(b []rune) {
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

func (s *spanScreen) insertRunes(b []rune) {
	y := s.cursorPos.Y
	fsx := s.cursorPos.X
	fex := fsx + len(b)
	tsx := s.size.X - len(b)
	tex := s.size.X

	cells := s.lineCells(y)
	copy(cells[tsx:tex], cells[fsx:fex])
	s.setLineFromCells(y, cells)

	s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b, CRText)
}

func (s *spanScreen) rawWriteRunes(x int, y int, b []rune, cr ChangeReason) {
	if y >= s.size.Y || x+len(b) > s.size.X {
		panic(fmt.Sprintf("rawWriteBytes out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+len(b), len(b), string(b)))
	}
	cells := s.lineCells(y)
	for i, r := range b {
		idx := x + i
		if cells[idx].cont {
			s.clearWideAt(cells, idx)
		}
		cells[idx] = styledCell{
			spanCell: spanCell{r: r, width: 1, cont: false},
			fg:       s.frontColor,
			bg:       s.backColor,
		}
	}
	s.setLineFromCells(y, cells)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + len(b)}, cr)
}

func (s *spanScreen) rawWriteRune(x int, y int, r rune, width int, cr ChangeReason) {
	if width < 1 {
		width = 1
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteRune out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+width, width, string(r)))
	}
	cells := s.lineCells(y)
	if cells[x].cont {
		s.clearWideAt(cells, x)
	}

	prevWidth := int(cells[x].width)
	if prevWidth < 1 {
		prevWidth = 1
	}

	cells[x] = styledCell{
		spanCell: spanCell{r: r, width: uint8(width), cont: false},
		fg:       s.frontColor,
		bg:       s.backColor,
	}
	for i := 1; i < width; i++ {
		idx := x + i
		cells[idx] = styledCell{
			spanCell: spanCell{r: 0, width: 0, cont: true},
			fg:       s.frontColor,
			bg:       s.backColor,
		}
	}
	for i := width; i < prevWidth; i++ {
		idx := x + i
		if idx >= s.size.X {
			break
		}
		cells[idx] = styledCell{
			spanCell: spanCell{r: ' ', width: 1, cont: false},
			fg:       s.frontColor,
			bg:       s.backColor,
		}
	}
	end := x + width
	if prevWidth > width {
		end = x + prevWidth
	}
	if end > s.size.X {
		end = s.size.X
	}
	s.setLineFromCells(y, cells)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: end}, cr)
}

func (s *spanScreen) clearWideAt(cells []styledCell, x int) {
	base := x
	for base > 0 && cells[base].cont {
		base--
	}
	width := int(cells[base].width)
	if width <= 1 {
		return
	}
	end := min(base+width, len(cells))
	for i := base; i < end; i++ {
		cells[i] = styledCell{
			spanCell: spanCell{r: ' ', width: 1, cont: false},
			fg:       s.frontColor,
			bg:       s.backColor,
		}
	}
}

func (s *spanScreen) deleteChars(x int, y int, n int, cr ChangeReason) {
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

	cells := s.lineCells(y)
	copy(cells[x:], cells[x+n:])
	for i := s.size.X - n; i < s.size.X; i++ {
		cells[i] = styledCell{
			spanCell: spanCell{r: ' ', width: 1, cont: false},
			fg:       s.frontColor,
			bg:       s.backColor,
		}
	}
	s.setLineFromCells(y, cells)

	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

func (s *spanScreen) setCursorPos(x, y int) {
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
	debugPrintln(debugCursor, "cursor set: ", x, y, s.cursorPos, s.size)
}

func (s *spanScreen) setScrollMarginTopBottom(top, bottom int) {
	debugPrintln(debugScroll, "scroll margins:", top, bottom)
	s.topMargin = clamp(top, 0, s.size.Y-1)
	s.bottomMargin = clamp(bottom, 0, s.size.Y-1)
}

func (s *spanScreen) scroll(y1 int, y2 int, dy int) {
	debugPrintln(debugScroll, "scroll:", y1, y2, dy)
	y1 = clamp(y1, 0, s.size.Y-1)
	y2 = clamp(y2, 0, s.size.Y-1)
	if y1 > y2 {
		fmt.Fprintln(os.Stderr, "scroll ys out of order", y1, y2, dy)
	}

	if dy > 0 {
		for y := y2; y >= y1+dy; y-- {
			s.lines[y] = cloneSpanLine(s.lines[y-dy])
		}
		s.frontend.RegionChanged(Region{Y: y1 + dy, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
		debugPrintln(debugScroll, "scroll changed region:", Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X})
		s.eraseRegion(Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X}, CRScroll)
	} else {
		for y := y1; y <= y2+dy; y++ {
			s.lines[y] = cloneSpanLine(s.lines[y-dy])
		}
		s.frontend.RegionChanged(Region{Y: y1, Y2: y2 + dy + 1, X: 0, X2: s.size.X}, CRScroll)
		s.eraseRegion(Region{Y: y2 + dy + 1, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
	}
}

func (s *spanScreen) clampRegion(r Region) Region {
	r.X = clamp(r.X, 0, s.size.X)
	r.Y = clamp(r.Y, 0, s.size.Y)
	r.X2 = clamp(r.X2, 0, s.size.X)
	r.Y2 = clamp(r.Y2, 0, s.size.Y)
	return r
}

func (s *spanScreen) moveCursor(dx, dy int, wrap bool, scroll bool) {
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
		s.cursorPos.Y = clamp(s.cursorPos.Y, 0, s.size.Y-1)
	}
	if s.cursorPos.Y >= s.size.Y {
		panic(fmt.Sprintf("moveCursor outside, %v %v  %v, %v, %v, %v", s.cursorPos, s.size, dx, dy, wrap, scroll))
	}
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
}

func (s *spanScreen) printScreen() {
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

func (s *spanScreen) lineCells(y int) []styledCell {
	line := s.lines[y]
	cells := make([]styledCell, 0, s.size.X)
	for _, sp := range line.spans {
		for _, c := range sp.cells {
			cells = append(cells, styledCell{spanCell: c, fg: sp.fg, bg: sp.bg})
		}
	}
	if len(cells) < s.size.X {
		cells = append(cells, makeSpaceCells(s.size.X-len(cells), s.frontColor, s.backColor)...)
	} else if len(cells) > s.size.X {
		cells = cells[:s.size.X]
	}
	return cells
}

func (s *spanScreen) setLineFromCells(y int, cells []styledCell) {
	s.lines[y] = lineFromCells(cells)
}

func blankSpanLine(width int, fg Color, bg Color) spanLine {
	cells := make([]spanCell, width)
	for i := range cells {
		cells[i] = spanCell{r: ' ', width: 1, cont: false}
	}
	return spanLine{spans: []span{{fg: fg, bg: bg, cells: cells}}}
}

func makeSpaceCells(n int, fg Color, bg Color) []styledCell {
	cells := make([]styledCell, n)
	for i := range cells {
		cells[i] = styledCell{
			spanCell: spanCell{r: ' ', width: 1, cont: false},
			fg:       fg,
			bg:       bg,
		}
	}
	return cells
}

func lineFromCells(cells []styledCell) spanLine {
	if len(cells) == 0 {
		return spanLine{}
	}
	spans := make([]span, 0, 4)
	cur := span{fg: cells[0].fg, bg: cells[0].bg}
	for _, c := range cells {
		if c.fg != cur.fg || c.bg != cur.bg {
			spans = append(spans, cur)
			cur = span{fg: c.fg, bg: c.bg}
		}
		cur.cells = append(cur.cells, spanCell{r: c.r, width: c.width, cont: c.cont})
	}
	spans = append(spans, cur)
	return spanLine{spans: spans}
}

func cloneSpanLine(line spanLine) spanLine {
	spans := make([]span, len(line.spans))
	for i, sp := range line.spans {
		cells := make([]spanCell, len(sp.cells))
		copy(cells, sp.cells)
		spans[i] = span{fg: sp.fg, bg: sp.bg, cells: cells}
	}
	return spanLine{spans: spans}
}
