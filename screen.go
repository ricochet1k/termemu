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
}

type spanLine struct {
	spans []span
}

type span struct {
	fg, bg    Color
	tokens    []spanToken
	cellWidth int
}

type spanToken struct {
	text  string
	width uint8
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
	line := make([]rune, s.size.X)
	pos := 0
	for _, sp := range s.lines[y].spans {
		for _, tok := range sp.tokens {
			width := tokenWidth(tok)
			if pos >= len(line) {
				return line
			}
			line[pos] = tokenRune(tok)
			for i := 1; i < width && pos+i < len(line); i++ {
				line[pos+i] = 0
			}
			pos += width
			if pos >= len(line) {
				return line
			}
		}
	}
	for pos < len(line) {
		line[pos] = ' '
		pos++
	}
	return line
}

func (s *spanScreen) getLineColors(y int) ([]Color, []Color) {
	fg := make([]Color, s.size.X)
	bg := make([]Color, s.size.X)
	pos := 0
	for _, sp := range s.lines[y].spans {
		for _, tok := range sp.tokens {
			width := tokenWidth(tok)
			for i := 0; i < width && pos < s.size.X; i++ {
				fg[pos] = sp.fg
				bg[pos] = sp.bg
				pos++
			}
			if pos >= s.size.X {
				return fg, bg
			}
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

	text := make([]rune, w)
	spans := make([]StyledSpan, 0)
	pos := 0
	out := 0
	for _, sp := range s.lines[y].spans {
		for _, tok := range sp.tokens {
			width := tokenWidth(tok)
			r := tokenRune(tok)
			for i := 0; i < width; i++ {
				if pos >= x && pos < x+w {
					if i == 0 {
						text[out] = r
					} else {
						text[out] = 0
					}
					if len(spans) == 0 || spans[len(spans)-1].FG != sp.fg || spans[len(spans)-1].BG != sp.bg {
						spans = append(spans, StyledSpan{FG: sp.fg, BG: sp.bg, Width: 1})
					} else {
						spans[len(spans)-1].Width++
					}
					out++
				}
				pos++
				if pos >= x+w {
					break
				}
			}
			if pos >= x+w {
				break
			}
		}
		if pos >= x+w {
			break
		}
	}

	for out < w {
		text[out] = ' '
		if len(spans) == 0 || spans[len(spans)-1].FG != s.frontColor || spans[len(spans)-1].BG != s.backColor {
			spans = append(spans, StyledSpan{FG: s.frontColor, BG: s.backColor, Width: 1})
		} else {
			spans[len(spans)-1].Width++
		}
		out++
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
	if s.size.X == 0 {
		return ""
	}
	buf := bytes.NewBuffer(make([]byte, 0, s.size.X+10))
	for _, sp := range s.lines[y].spans {
		if len(sp.tokens) == 0 {
			continue
		}
		buf.Write(ANSIEscape(sp.fg, sp.bg))
		for _, tok := range sp.tokens {
			if tok.text != "" {
				buf.WriteString(tok.text)
			}
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
			line := cloneSpanLine(s.lines[y])
			resizeLine(&line, w, s.frontColor, s.backColor)
			newLines[y] = line
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

func (s *spanScreen) writeTokens(tokens []GraphemeToken) {
	if len(tokens) == 0 {
		return
	}
	batch := make([]spanToken, 0, len(tokens))
	batchWidth := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.rawWriteTokens(s.cursorPos.X, s.cursorPos.Y, batch, CRText)
		s.moveCursor(batchWidth, 0, true, true)
		batch = batch[:0]
		batchWidth = 0
	}

	for _, tok := range tokens {
		if tok.Merge {
			if len(batch) > 0 {
				batch[len(batch)-1].text += tok.Text
			} else {
				s.mergeIntoPreviousCell(tok.Text)
			}
			continue
		}
		width := tok.Width
		if width < 1 {
			width = 1
		}
		if width > s.size.X {
			width = s.size.X
		}

		if s.cursorPos.X+batchWidth+width > s.size.X {
			flush()
			if s.autoWrap {
				s.moveCursor(-s.cursorPos.X, 1, false, true)
			} else {
				s.cursorPos.X = s.size.X - width
			}
		}

		batch = append(batch, spanToken{text: tok.Text, width: uint8(width)})
		batchWidth += width
	}

	flush()
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
	truncateLine(line, s.size.X-n)
	insertSpan(line, s.cursorPos.X, s.frontColor, s.backColor, makeSpaceTokens(n))

	s.rawWriteRunes(s.cursorPos.X, s.cursorPos.Y, b[:n], CRText)
}

func (s *spanScreen) rawWriteRunes(x int, y int, b []rune, cr ChangeReason) {
	if y >= s.size.Y || x+len(b) > s.size.X {
		panic(fmt.Sprintf("rawWriteBytes out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+len(b), len(b), string(b)))
	}
	width := len(b)
	s.clearWideOverlaps(y, x, width)
	tokens := makeTokensFromRunes(b)
	replaceRange(&s.lines[y], x, width, s.frontColor, s.backColor, tokens)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + width}, cr)
}

func (s *spanScreen) rawWriteRune(x int, y int, r rune, width int, cr ChangeReason) {
	if width < 1 {
		width = 1
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteRune out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+width, width, string(r)))
	}
	prevWidth := 1
	if tok, ok := tokenAt(s.lines[y], x); ok {
		prevWidth = tokenWidth(tok)
	}

	s.clearWideOverlaps(y, x, width)
	tokens := makeTokenFromRune(r, width)
	replaceRange(&s.lines[y], x, width, s.frontColor, s.backColor, tokens)

	end := x + width
	if prevWidth > width {
		end = x + prevWidth
	}
	if end > s.size.X {
		end = s.size.X
	}
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: end}, cr)
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
	start := splitAt(line, x)
	end := splitAt(line, x+n)
	line.spans = append(line.spans[:start], line.spans[end:]...)
	line.spans = append(line.spans, span{fg: s.frontColor, bg: s.backColor, tokens: makeSpaceTokens(n), cellWidth: n})
	mergeAdjacent(line)

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

func tokenRune(tok spanToken) rune {
	for _, r := range tok.text {
		return r
	}
	return ' '
}

func tokenWidth(tok spanToken) int {
	if tok.width < 1 {
		return 1
	}
	return int(tok.width)
}

func tokensCellWidth(tokens []spanToken) int {
	width := 0
	for _, tok := range tokens {
		width += tokenWidth(tok)
	}
	return width
}

func makeSpaceTokens(n int) []spanToken {
	if n <= 0 {
		return nil
	}
	tokens := make([]spanToken, n)
	for i := range tokens {
		tokens[i] = spanToken{text: " ", width: 1}
	}
	return tokens
}

func makeTokensFromRunes(runes []rune) []spanToken {
	if len(runes) == 0 {
		return nil
	}
	tokens := make([]spanToken, len(runes))
	for i, r := range runes {
		tokens[i] = spanToken{text: string(r), width: 1}
	}
	return tokens
}

func makeTokenFromRune(r rune, width int) []spanToken {
	if width < 1 {
		width = 1
	}
	if width > 255 {
		width = 255
	}
	return []spanToken{{text: string(r), width: uint8(width)}}
}

func blankSpanLine(width int, fg Color, bg Color) spanLine {
	return spanLine{spans: []span{{fg: fg, bg: bg, tokens: makeSpaceTokens(width), cellWidth: width}}}
}

func cloneSpanLine(line spanLine) spanLine {
	spans := make([]span, len(line.spans))
	for i, sp := range line.spans {
		tokens := make([]spanToken, len(sp.tokens))
		copy(tokens, sp.tokens)
		spans[i] = span{fg: sp.fg, bg: sp.bg, tokens: tokens, cellWidth: sp.cellWidth}
	}
	return spanLine{spans: spans}
}

func resizeLine(line *spanLine, width int, fg Color, bg Color) {
	cur := lineCellWidth(*line)
	if cur > width {
		truncateLine(line, width)
		return
	}
	if cur < width {
		line.spans = append(line.spans, span{fg: fg, bg: bg, tokens: makeSpaceTokens(width - cur), cellWidth: width - cur})
		mergeAdjacent(line)
	}
}

func lineCellWidth(line spanLine) int {
	width := 0
	for _, sp := range line.spans {
		width += sp.cellWidth
	}
	return width
}

func splitTokens(tokens []spanToken, cellOffset int) ([]spanToken, []spanToken) {
	if cellOffset <= 0 {
		return nil, tokens
	}
	pos := 0
	for i, tok := range tokens {
		pos += tokenWidth(tok)
		if pos == cellOffset {
			return tokens[:i+1], tokens[i+1:]
		}
		if pos > cellOffset {
			return tokens[:i], tokens[i:]
		}
	}
	return tokens, nil
}

func splitAt(line *spanLine, x int) int {
	if x <= 0 {
		return 0
	}
	pos := 0
	for i := 0; i < len(line.spans); i++ {
		sp := line.spans[i]
		next := pos + sp.cellWidth
		if x == next {
			return i + 1
		}
		if x < next {
			offset := x - pos
			leftTokens, rightTokens := splitTokens(sp.tokens, offset)
			left := span{fg: sp.fg, bg: sp.bg, tokens: leftTokens, cellWidth: tokensCellWidth(leftTokens)}
			right := span{fg: sp.fg, bg: sp.bg, tokens: rightTokens, cellWidth: tokensCellWidth(rightTokens)}
			line.spans = append(line.spans[:i], append([]span{left, right}, line.spans[i+1:]...)...)
			return i + 1
		}
		pos = next
	}
	return len(line.spans)
}

func mergeAdjacent(line *spanLine) {
	if len(line.spans) < 2 {
		return
	}
	out := make([]span, 0, len(line.spans))
	cur := line.spans[0]
	for i := 1; i < len(line.spans); i++ {
		sp := line.spans[i]
		if sp.fg == cur.fg && sp.bg == cur.bg {
			cur.tokens = append(cur.tokens, sp.tokens...)
			cur.cellWidth += sp.cellWidth
			continue
		}
		out = append(out, cur)
		cur = sp
	}
	out = append(out, cur)
	line.spans = out
}

func replaceRange(line *spanLine, x int, n int, fg Color, bg Color, tokens []spanToken) {
	start := splitAt(line, x)
	end := splitAt(line, x+n)
	spanWidth := tokensCellWidth(tokens)
	line.spans = append(line.spans[:start], append([]span{{fg: fg, bg: bg, tokens: tokens, cellWidth: spanWidth}}, line.spans[end:]...)...)
	mergeAdjacent(line)
}

func insertSpan(line *spanLine, x int, fg Color, bg Color, tokens []spanToken) {
	idx := splitAt(line, x)
	spanWidth := tokensCellWidth(tokens)
	line.spans = append(line.spans[:idx], append([]span{{fg: fg, bg: bg, tokens: tokens, cellWidth: spanWidth}}, line.spans[idx:]...)...)
	mergeAdjacent(line)
}

func truncateLine(line *spanLine, width int) {
	if width <= 0 {
		line.spans = nil
		return
	}
	idx := splitAt(line, width)
	line.spans = line.spans[:idx]
}

func tokenAt(line spanLine, x int) (spanToken, bool) {
	pos := 0
	for _, sp := range line.spans {
		for _, tok := range sp.tokens {
			width := tokenWidth(tok)
			if x < pos+width {
				return tok, true
			}
			pos += width
		}
	}
	return spanToken{}, false
}

func (s *spanScreen) mergeIntoPreviousCell(text string) {
	if s.cursorPos.X <= 0 {
		return
	}
	pos := 0
	y := s.cursorPos.Y
	for si := range s.lines[y].spans {
		sp := &s.lines[y].spans[si]
		for ti := range sp.tokens {
			width := tokenWidth(sp.tokens[ti])
			if s.cursorPos.X-1 < pos+width {
				sp.tokens[ti].text += text
				return
			}
			pos += width
		}
	}
}

func (s *spanScreen) clearWideOverlaps(y int, x int, n int) {
	if n <= 0 {
		return
	}
	end := x + n
	line := &s.lines[y]
	pos := 0
	type rng struct {
		start int
		width int
	}
	var ranges []rng
	for _, sp := range line.spans {
		for _, tok := range sp.tokens {
			width := tokenWidth(tok)
			next := pos + width
			if width > 1 && next > x && pos < end {
				ranges = append(ranges, rng{start: pos, width: width})
			}
			pos = next
			if pos >= end && width == 1 {
				// no more overlaps if we are beyond the range and widths are single-cell
				break
			}
		}
		if pos >= end {
			break
		}
	}

	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		replaceRange(line, r.start, r.width, s.frontColor, s.backColor, makeSpaceTokens(r.width))
		s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: r.start, X2: r.start + r.width}, CRClear)
	}
}

func (s *spanScreen) rawWriteTokens(x int, y int, tokens []spanToken, cr ChangeReason) {
	width := tokensCellWidth(tokens)
	if width <= 0 {
		return
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteTokens out of range: %v  %v,%v,%v %v\n", s.size, x, y, x+width, width))
	}
	s.clearWideOverlaps(y, x, width)
	replaceRange(&s.lines[y], x, width, s.frontColor, s.backColor, tokens)
	s.frontend.RegionChanged(Region{Y: y, Y2: y + 1, X: x, X2: x + width}, cr)
}
