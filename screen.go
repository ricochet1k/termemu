package termemu

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
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
	writeTokens(tokens []GraphemeToken)
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

	textMode TextReadMode

	// scratch buffers for rendering
	renderBuffer   []rune
	renderColorsFG []Color
	renderColorsBG []Color
}

type spanLine struct {
	spans []Span
	width int
}

func newScreen(f Frontend) screen {
	return newSpanScreen(f)
}

func newSpanScreen(f Frontend) *spanScreen {
	s := &spanScreen{
		frontend: f,
		textMode: TextReadModeGrapheme,
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
	if len(s.renderBuffer) < s.size.X {
		s.renderBuffer = make([]rune, s.size.X)
	}
	line := s.renderBuffer[:s.size.X]
	pos := 0
	for _, sp := range s.lines[y].spans {
		if pos >= len(line) {
			break
		}
		pos = fillLineFromSpan(line, pos, sp, s.textMode)
	}
	for pos < len(line) {
		line[pos] = ' '
		pos++
	}
	return line
}

func (s *spanScreen) getLineColors(y int) ([]Color, []Color) {
	if len(s.renderColorsFG) < s.size.X {
		s.renderColorsFG = make([]Color, s.size.X)
		s.renderColorsBG = make([]Color, s.size.X)
	}
	fg := s.renderColorsFG[:s.size.X]
	bg := s.renderColorsBG[:s.size.X]
	pos := 0
	for _, sp := range s.lines[y].spans {
		for i := 0; i < sp.Width && pos < s.size.X; i++ {
			fg[pos] = sp.FG
			bg[pos] = sp.BG
			pos++
		}
		if pos >= s.size.X {
			break
		}
	}
	for pos < s.size.X {
		fg[pos] = s.frontColor
		bg[pos] = s.backColor
		pos++
	}
	return fg, bg
}

func (s *spanScreen) StyledLine(x, w, y int) *Line {
	if w < 0 || x+w > s.size.X {
		w = s.size.X - x
	}
	if w < 0 {
		w = 0
	}

	// We can construct the line by taking a slice of the spans and clipping edges
	originalSpans := s.lines[y].spans
	var spans []Span

	pos := 0
	for _, sp := range originalSpans {
		endPos := pos + sp.Width

		// If span is completely before region
		if endPos <= x {
			pos = endPos
			continue
		}

		// If span is completely after region
		if pos >= x+w {
			break
		}

		// Overlap
		// intersection: max(pos, x) to min(endPos, x+w)
		startO := max(pos, x)
		endO := min(endPos, x+w)
		width := endO - startO

		if width > 0 {
			// Offset relative to span start
			offset := startO - pos

			// If we need the whole span
			if offset == 0 && width == sp.Width {
				spans = append(spans, sp)
			} else {
				// We need a sub-span
				_, sub := splitSpan(sp, offset, s.textMode) // skip left part
				// now sub is from offset to end. we might need to truncate it if it's too long
				if sub.Width > width {
					keep, _ := splitSpan(sub, width, s.textMode)
					spans = append(spans, keep)
				} else {
					spans = append(spans, sub)
				}
			}
		}

		pos = endPos
	}

	return &Line{
		Spans: spans,
		Width: w,
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
	if s.size.X == 0 {
		return ""
	}
	var buf bytes.Buffer
	for _, sp := range s.lines[y].spans {
		if sp.Width == 0 {
			continue
		}
		buf.Write(ANSIEscape(sp.FG, sp.BG))
		if sp.Text == "" {
			for i := 0; i < sp.Width; i++ {
				buf.WriteRune(sp.Rune)
			}
		} else {
			buf.WriteString(sp.Text)
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
			line := s.lines[y]
			resizeLine(&line, w, s.frontColor, s.backColor, s.textMode)
			newLines[y] = line
		default:
			newLines[y] = blankSpanLine(w, s.frontColor, s.backColor)
		}
	}

	s.lines = newLines

	s.bottomMargin = h - (s.size.Y - s.bottomMargin)

	s.size = Pos{X: w, Y: h}

	// Resize buffers
	s.renderBuffer = make([]rune, w)
	s.renderColorsFG = make([]Color, w)
	s.renderColorsBG = make([]Color, w)

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

	// Fast path for clearing (using empty span with repeating rune)
	emptySpan := Span{FG: s.frontColor, BG: s.backColor, Rune: ' ', Width: r.X2 - r.X}

	for i := r.Y; i < r.Y2; i++ {
		debugPrintln(debugErase, "erase: ", r.X, i, emptySpan.Width)
		s.rawWriteSpan(r.X, i, emptySpan, cr)
	}
}

func (s *spanScreen) writeRunes(b []rune) {
	panic("This path should not ever be used by spanScreen")
}

func (s *spanScreen) writeTokens(tokens []GraphemeToken) {
	panic("This path should not ever be used by spanScreen")
}

func (s *spanScreen) writeString(text string, width int, merge bool, mode TextReadMode) {
	if len(text) == 0 {
		return
	}
	s.textMode = mode
	if merge {
		s.mergeIntoPreviousCell(text)
		return
	}
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
	sp := Span{FG: s.frontColor, BG: s.backColor, Text: text, Width: width}
	s.rawWriteSpan(s.cursorPos.X, s.cursorPos.Y, sp, CRText)
	s.moveCursor(width, 0, true, true)
}

func (s *spanScreen) insertRunes(b []rune) {
	y := s.cursorPos.Y
	n := len(b)
	if n <= 0 {
		return
	}
	if n > s.size.X {
		n = s.size.X
	}

	s.clearWideOverlaps(y, s.cursorPos.X, 1)

	line := &s.lines[y]
	truncateLine(line, s.size.X-n, s.textMode)
	insertSpan(line, s.cursorPos.X, Span{FG: s.frontColor, BG: s.backColor, Rune: ' ', Width: n}, s.textMode)

	s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b[:n], CRText)
}

func (s *spanScreen) rawWriteRunes(x int, y int, b []rune, cr ChangeReason) {
	panic("This path should not ever be used by spanScreen")
}

func (s *spanScreen) rawWriteSpan(x int, y int, sp Span, cr ChangeReason) {
	if sp.Width <= 0 {
		return
	}
	if y >= s.size.Y || x+sp.Width > s.size.X {
		panic(fmt.Sprintf("rawWriteSpan out of range: %v  %v,%v,%v %v\n", s.size, x, y, x+sp.Width, sp.Width))
	}
	s.clearWideOverlaps(y, x, sp.Width)
	replaceRange(&s.lines[y], x, sp.Width, sp, s.textMode)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + sp.Width}, cr)
}

func (s *spanScreen) rawWriteRune(x int, y int, r rune, width int, cr ChangeReason) {
	if width < 1 {
		width = 1
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteRune out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+width, width, string(r)))
	}
	s.clearWideOverlaps(y, x, width)

	var sp Span
	if width == 1 && r == ' ' {
		sp = Span{FG: s.frontColor, BG: s.backColor, Rune: ' ', Width: 1}
	} else {
		sp = Span{FG: s.frontColor, BG: s.backColor, Text: string(r), Width: width}
	}

	replaceRange(&s.lines[y], x, width, sp, s.textMode)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + width}, cr)
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

	s.clearWideOverlaps(y, x, n)

	line := &s.lines[y]
	// Delete characters from x to x+n, shift remaining chars left, and append spaces at the end
	replaceRange(line, x, n, Span{}, s.textMode)
	// Now append spaces to fill the end to width s.size.X
	curWidth := lineCellWidth(line)
	if curWidth < s.size.X {
		line.spans = append(line.spans, Span{FG: s.frontColor, BG: s.backColor, Rune: ' ', Width: s.size.X - curWidth})
		line.width = s.size.X
	}

	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

func (s *spanScreen) setCursorPos(x, y int) {
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	s.frontend.CursorMoved(s.cursorPos.X, s.cursorPos.Y)
	// debugPrintln(debugCursor, "cursor set: ", x, y, s.cursorPos, s.size)
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
			s.lines[y] = s.lines[y-dy]
		}
		for y := y1; y < y1+dy; y++ {
			s.lines[y] = blankSpanLine(s.size.X, s.frontColor, s.backColor)
		}
		s.frontend.RegionChanged(Region{Y: y1 + dy, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
		debugPrintln(debugScroll, "scroll changed region:", Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X})
		s.frontend.RegionChanged(Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X}, CRScroll)
	} else {
		for y := y1; y <= y2+dy; y++ {
			s.lines[y] = s.lines[y-dy]
		}
		for y := y2 + dy + 1; y <= y2; y++ {
			s.lines[y] = blankSpanLine(s.size.X, s.frontColor, s.backColor)
		}
		s.frontend.RegionChanged(Region{Y: y1, Y2: y2 + dy + 1, X: 0, X2: s.size.X}, CRScroll)
		s.frontend.RegionChanged(Region{Y: y2 + dy + 1, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
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

func fillLineFromSpan(line []rune, pos int, sp Span, mode TextReadMode) int {
	if pos >= len(line) || sp.Width <= 0 {
		return pos
	}

	if sp.Text == "" {
		// Repeat mode
		for i := 0; i < sp.Width && pos < len(line); i++ {
			line[pos] = sp.Rune
			pos++
		}
		return pos
	}

	// Text mode
	idx := 0
	state := -1
	textBytes := []byte(sp.Text) // TODO: Avoid this cast if stepTextCluster supports string

	for idx < len(textBytes) && pos < len(line) {
		cluster, consumed, width, newState, ok := stepTextCluster(textBytes[idx:], state, mode)
		if !ok || consumed <= 0 {
			break
		}
		if width < 1 {
			width = 0
		}
		if width > 0 {
			r, _ := utf8.DecodeRune(cluster)
			line[pos] = r
			for i := 1; i < width && pos+i < len(line); i++ {
				line[pos+i] = 0
			}
			pos += width
		}
		idx += consumed
		state = newState
	}
	return pos
}

func blankSpanLine(width int, fg Color, bg Color) spanLine {
	return spanLine{spans: []Span{{FG: fg, BG: bg, Rune: ' ', Width: width}}, width: width}
}

func resizeLine(line *spanLine, width int, fg Color, bg Color, mode TextReadMode) {
	cur := lineCellWidth(line)
	if cur > width {
		truncateLine(line, width, mode)
		return
	}
	if cur < width {
		line.spans = append(line.spans, Span{FG: fg, BG: bg, Rune: ' ', Width: width - cur})
	}
	line.width = width
}

func lineCellWidth(line *spanLine) int {
	if line == nil {
		return 0
	}
	if line.width != 0 || len(line.spans) == 0 {
		return line.width
	}
	width := 0
	for _, sp := range line.spans {
		width += sp.Width
	}
	return width
}

func splitSpan(sp Span, cellOffset int, mode TextReadMode) (Span, Span) {
	if cellOffset <= 0 {
		return Span{}, sp
	}
	if cellOffset >= sp.Width {
		return sp, Span{}
	}

	if sp.Text == "" {
		// Repeat mode
		left := sp
		left.Width = cellOffset
		right := sp
		right.Width = sp.Width - cellOffset
		return left, right
	}

	if mode == TextReadModeRune && sp.Width == len(sp.Text) {
		leftText := sp.Text[:cellOffset]
		rightText := sp.Text[cellOffset:]
		left := sp
		left.Text = leftText
		left.Width = cellOffset
		right := sp
		right.Text = rightText
		right.Width = sp.Width - cellOffset
		return left, right
	}

	// Text mode
	idx, leftWidth := byteIndexForCell([]byte(sp.Text), cellOffset, mode)
	leftText := sp.Text[:idx]
	rightText := sp.Text[idx:]

	left := sp
	left.Text = leftText
	left.Width = leftWidth

	right := sp
	right.Text = rightText
	right.Width = sp.Width - leftWidth

	return left, right
}

func byteIndexForCell(text []byte, cellOffset int, mode TextReadMode) (int, int) {
	if cellOffset <= 0 || len(text) == 0 {
		return 0, 0
	}
	idx := 0
	width := 0
	state := -1
	for idx < len(text) && width < cellOffset {
		_, consumed, cellWidth, newState, ok := stepTextCluster(text[idx:], state, mode)
		if !ok || consumed <= 0 {
			break
		}
		idx += consumed
		state = newState
		if cellWidth > 0 {
			width += cellWidth
		}
	}
	return idx, width
}

// findSpanAtX returns the span index at position x and the offset within that span.
// It does NOT modify the line. The returned index points to the span that contains x.
// If x exactly matches a span boundary, it returns the index of the span after the boundary.
func findSpanAtX(line *spanLine, x int) (int, int) {
	if x <= 0 {
		return 0, 0
	}
	pos := 0
	for i := 0; i < len(line.spans); i++ {
		sp := line.spans[i]
		next := pos + sp.Width
		if x == next {
			return i + 1, 0
		}
		if x < next {
			offset := x - pos
			return i, offset
		}
		pos = next
	}
	return len(line.spans), 0
}

func mergeAdjacent(line *spanLine) {
	if len(line.spans) < 2 {
		return
	}

	writeIdx := 0
	for i := 1; i < len(line.spans); i++ {
		cur := line.spans[i]
		prev := &line.spans[writeIdx]

		canMerge := cur.FG == prev.FG && cur.BG == prev.BG
		if canMerge {
			// Check if mergeable types
			if prev.Text == "" && cur.Text == "" && prev.Rune == cur.Rune {
				// Both repeats of same rune
				prev.Width += cur.Width
				continue
			}

			// Convert both to text if mixing?
			// Or if both text
			if prev.Text != "" && cur.Text != "" {
				prev.Text += cur.Text
				prev.Width += cur.Width
				continue
			}

			// If one is repeat and other is text, convert repeat to text
			// But only if repeat is small? For now, let's keep them separate if different types
			// to avoid allocating huge strings for spaces.
			// Actually, better to merge if we can to reduce span count.
			// But for space runs, keeping them as Rune=' ' is better.
			// If we have "abc" and "   ", keep separate.
			// If we have "   " and "   ", merge (handled above).
		}

		writeIdx++
		line.spans[writeIdx] = cur
	}
	line.spans = line.spans[:writeIdx+1]
}

func replaceRange(line *spanLine, x int, n int, insert Span, mode TextReadMode) {
	// Fast return for a no-op insert.
	if n == 0 && insert.Width == 0 {
		return
	}

	spans := line.spans
	// Handle empty lines by inserting directly.
	if len(spans) == 0 {
		line.spans = append(line.spans[:0], insert)
		line.width = insert.Width
		return
	}

	// Clamp negative inputs and scan once to find boundaries.
	if x < 0 {
		x = 0
	}
	if n < 0 {
		n = 0
	}

	// Locate the spans that intersect the replacement window.
	startFound := false
	startIdx := len(spans)
	startOffset := 0
	pos := 0
	i := 0
	for ; i < len(spans); i++ {
		endPos := pos + spans[i].Width
		if !startFound && x < endPos {
			startIdx = i
			startOffset = x - pos
			break
		}
		pos = endPos
	}
	endIdx := len(spans) - 1
	endOffset := 0
	for ; i < len(spans); i++ {
		endPos := pos + spans[i].Width
		if x+n <= endPos {
			endIdx = i
			endOffset = x + n - pos
			pos = endPos
			break
		}
		pos = endPos
	}

	// Clamp to total width (tracked by pos after the scan).
	totalWidth := pos
	if x > totalWidth {
		x = totalWidth
	}
	if x+n > totalWidth {
		n = totalWidth - x
		endIdx = len(spans) - 1
		if endIdx >= 0 {
			endOffset = spans[endIdx].Width
		}
	}
	if x == 0 && n >= totalWidth {
		line.spans = append(line.spans[:0], insert)
		line.width = insert.Width
		return
	}

	// If we never intersected, treat it like an append.
	if startIdx == len(spans) {
		if insert.Width > 0 {
			line.spans = append(line.spans, insert)
			line.width = totalWidth + insert.Width
		}
		return
	}

	// Fast paths for in-place replacements inside a single span.
	if startIdx == endIdx {
		sp := spans[startIdx]
		if startOffset == 0 && endOffset == sp.Width {
			spans[startIdx] = insert
			line.width = totalWidth - n + insert.Width
			return
		}
		if n == 1 && insert.Width == 1 && startOffset == 0 && endOffset == 1 && sp.Width == 1 {
			spans[startIdx] = insert
			line.width = totalWidth
			return
		}
		if insert.Width == n && sp.FG == insert.FG && sp.BG == insert.BG {
			if sp.Text == "" && insert.Text == "" && sp.Rune == insert.Rune {
				return
			}
			if sp.Text != "" && insert.Text != "" && mode == TextReadModeRune && sp.Width == len(sp.Text) && insert.Width == len(insert.Text) {
				sp.Text = sp.Text[:startOffset] + insert.Text + sp.Text[startOffset+n:]
				spans[startIdx] = sp
				line.width = totalWidth
				return
			}
		}
	}

	// Split the boundary spans so we can keep the untouched parts.
	var left Span
	hasLeft := false
	if startIdx < len(spans) && startOffset > 0 {
		left, _ = splitSpan(spans[startIdx], startOffset, mode)
		hasLeft = left.Width > 0
	}

	var right Span
	hasRight := false
	if endIdx >= 0 && endIdx < len(spans) {
		if endOffset < spans[endIdx].Width {
			_, right = splitSpan(spans[endIdx], endOffset, mode)
			hasRight = right.Width > 0
		}
	}

	// Compute the new slice length and where the suffix begins.
	prefixLen := startIdx
	suffixStart := endIdx + 1
	newLen := prefixLen
	insertCount := 0
	if hasLeft {
		insertCount++
	}
	if insert.Width > 0 {
		insertCount++
	}
	if hasRight {
		insertCount++
	}
	newLen += insertCount
	if suffixStart < len(spans) {
		newLen += len(spans) - suffixStart
	}
	newWidth := totalWidth - n + insert.Width

	// Ensure capacity then populate the new span layout.
	if cap(spans) < newLen {
		newSpans := make([]Span, newLen)
		copy(newSpans, spans[:prefixLen])
		dest := prefixLen
		if hasLeft {
			newSpans[dest] = left
			dest++
		}
		if insert.Width > 0 {
			newSpans[dest] = insert
			dest++
		}
		if hasRight {
			newSpans[dest] = right
			dest++
		}
		copy(newSpans[dest:], spans[suffixStart:])
		line.spans = newSpans
		line.width = newWidth
		return
	}

	// Reuse the existing backing array and shift the suffix if needed.
	line.spans = spans[:newLen]
	destStart := prefixLen
	destAfter := destStart + insertCount
	suffixLen := len(spans) - suffixStart
	if suffixLen > 0 && destAfter > suffixStart {
		copy(line.spans[destAfter:], spans[suffixStart:])
	}

	dest := destStart
	if hasLeft {
		line.spans[dest] = left
		dest++
	}
	if insert.Width > 0 {
		line.spans[dest] = insert
		dest++
	}
	if hasRight {
		line.spans[dest] = right
		dest++
	}

	if suffixLen > 0 && destAfter <= suffixStart {
		copy(line.spans[destAfter:], spans[suffixStart:])
	}
	line.width = newWidth
}

func insertSpan(line *spanLine, x int, insert Span, mode TextReadMode) {
	// Use replaceRange with zero-width deletion to insert at position x
	// This properly handles splitting spans at boundaries without extra allocations
	replaceRange(line, x, 0, insert, mode)
}

func truncateLine(line *spanLine, width int, mode TextReadMode) {
	if width <= 0 {
		line.spans = nil
		line.width = 0
		return
	}
	// Use replaceRange to truncate: delete everything from width to end
	replaceRange(line, width, lineCellWidth(line)-width, Span{}, mode)
}

func (s *spanScreen) mergeIntoPreviousCell(text string) {
	if s.cursorPos.X <= 0 {
		return
	}
	y := s.cursorPos.Y
	line := &s.lines[y]
	cell := s.cursorPos.X - 1

	// Find the span at cell position
	idx, offset := findSpanAtX(line, cell)
	if idx >= len(line.spans) {
		return
	}

	sp := &line.spans[idx]

	// If we're at an offset within the span, we need to split it first
	if offset > 0 {
		left, right := splitSpan(*sp, offset, s.textMode)

		// Update spans: replace current span with left and right
		newSpans := make([]Span, 0, len(line.spans)+1)
		newSpans = append(newSpans, line.spans[:idx]...)
		newSpans = append(newSpans, left, right)
		newSpans = append(newSpans, line.spans[idx+1:]...)
		line.spans = newSpans

		// Now the cell we want to merge into is at idx+1
		idx++
	}

	// Convert repeat to text if needed and merge
	sp = &line.spans[idx]
	if sp.Text == "" {
		sp.Text = strings.Repeat(string(sp.Rune), sp.Width)
	}
	sp.Text += text
	// Width doesn't change for merge

	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: cell, X2: cell + 1}, CRText)
}

func (s *spanScreen) clearWideOverlaps(y int, x int, n int) {
	if n <= 0 {
		return
	}
	end := x + n
	line := &s.lines[y]
	pos := 0

	// Collect all wide-char positions that overlap [x, end) in a single pass.
	// Then clear them in reverse order to avoid index shifts.
	wideChars := make([]struct {
		start, width int
	}, 0, 10)

	spans := line.spans
	for _, sp := range spans {
		spanStart := pos
		spanEnd := pos + sp.Width
		if spanEnd <= x {
			pos = spanEnd
			continue
		}
		if spanStart >= end {
			break
		}

		if sp.Text != "" {
			// Iterate grapheme clusters to find wide characters overlapping the range.
			idx := 0
			state := -1
			cellPos := spanStart
			textBytes := []byte(sp.Text)

			for idx < len(textBytes) && cellPos < end {
				_, consumed, width, newState, ok := stepTextCluster(textBytes[idx:], state, s.textMode)
				if !ok || consumed <= 0 {
					break
				}
				if width > 1 {
					clusterStart := cellPos
					clusterEnd := cellPos + width
					if clusterStart < end && clusterEnd > x {
						wideChars = append(wideChars, struct {
							start, width int
						}{clusterStart, width})
					}
				}
				if width < 1 {
					width = 0
				}
				cellPos += width
				idx += consumed
				state = newState
			}
		}
		pos = spanEnd
	}

	// Clear wide chars in reverse order so indices don't shift
	for i := len(wideChars) - 1; i >= 0; i-- {
		wc := wideChars[i]
		replaceRange(line, wc.start, wc.width, Span{FG: s.frontColor, BG: s.backColor, Rune: ' ', Width: wc.width}, s.textMode)
		s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: wc.start, X2: wc.start + wc.width}, CRClear)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
