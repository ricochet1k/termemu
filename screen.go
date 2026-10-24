package termemu

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type screen interface {
	Size() Pos
	CursorPos() Pos
	Style() Style
	AutoWrap() bool
	SetAutoWrap(value bool)
	TopMargin() int
	BottomMargin() int
	SetFrontend(f Frontend)
	setView(v LockedView)

	Line(y int) string
	StyledLine(x, w, y int) Line
	// clippedStyledSpans appends the spans of region (x,w,y) to dst, reusing
	// dst's backing array. It returns the appended spans and the effective
	// width after clamping. Used by StyledLine and in-package renderers.
	clippedStyledSpans(dst []Span, x, w, y int) ([]Span, int)
	StyledLines(r Region) []Line
	renderLineANSI(y int) string
	setStyle(style Style)
	setSize(w, h int)
	eraseRegion(r Region, cr ChangeReason)
	writeString(text string, width int, merge bool, mode TextReadMode)
	rawWriteRune(x int, y int, r rune, width int, cr ChangeReason)
	deleteChars(x int, y int, n int, cr ChangeReason)
	setScrollMarginTopBottom(top, bottom int)
	scroll(y1 int, y2 int, dy int)
	setCursorPos(x, y int)
	moveCursor(dx, dy int, wrap bool, scroll bool)
	// takeTopRows removes the top n rows and shifts the rest up without
	// emitting callbacks. shiftCursorUp moves the cursor up by n rows,
	// clamping at the top. Both support scrollback-preserving resize.
	takeTopRows(n int) ([]spanLine, []bool)
	shiftCursorUp(n int)
	saveCursorPos()
	restoreCursorPos()
	// copyTransientState copies the shared cursor/style state across a screen
	// switch without emitting callbacks.
	copyTransientState(from screen)
	decaln()
	printScreen()
	setScrollCapture(func(lines []spanLine, wrapped []bool))
	// setScrollDelta installs the callback for main-screen scroll deltas.
	setScrollDelta(func(delta int))
	// setLink records the OSC 8 hyperlink URI applied to subsequently
	// written content spans. Internal only.
	setLink(link string)

	// snapshotState returns a deep copy of the screen's state and
	// restoreState replaces it, resizing to the snapshot dimensions.
	// Internal only.
	snapshotState() ScreenState
	restoreState(ScreenState)
}

// Pos holds X/Y coordinates.
type Pos struct {
	X int
	Y int
}

type spanScreen struct {
	lines    []spanLine
	wrapped  []bool // wrapped[y] is true when line y is a continuation of the previous line due to auto-wrap
	frontend Frontend
	view     LockedView

	style Style

	// link is the OSC 8 hyperlink URI stamped onto content spans
	// written by writeString/rawWriteRune. Blank/erased spans (eraseRegion,
	// scroll fill, resize padding, deleteChars padding) never carry it.
	link string

	size Pos

	cursorPos        Pos
	pendingWrap      bool // cursor sits on the last column; the next write wraps first
	savedCursorPos   Pos
	savedPendingWrap bool
	savedStyle       Style

	topMargin, bottomMargin int

	autoWrap bool

	textMode TextReadMode

	// scrollScratch/scrollScratchW hold the rows scrolling off during a
	// scroll so their span storage can be recycled into the vacated rows
	// instead of allocating new blank lines. scrollCapture must not retain
	// these slices (the terminal copies the rows it needs).
	scrollScratch  []spanLine
	scrollScratchW []bool

	// scrollCapture is called before lines scroll off. It receives the
	// physical rows being lost, along with their wrapped flags.
	scrollCapture func(lines []spanLine, wrapped []bool)
	// scrollDelta forwards the signed scroll amount to the terminal (main
	// screen only; the terminal leaves it nil for the alt screen).
	scrollDelta func(delta int)
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
		frontend:   f,
		textMode:   TextReadModeGrapheme,
		style:      NewStyle(),
		savedStyle: NewStyle(),
		autoWrap:   true, // DECAWM (CSI ? 7) defaults to on, as in xterm
	}
	s.setSize(80, 14)
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

func (s *spanScreen) Style() Style {
	return s.style
}

func (s *spanScreen) AutoWrap() bool {
	return s.autoWrap
}

func (s *spanScreen) SetAutoWrap(value bool) {
	s.autoWrap = value
	// Pending wrap is meaningless with DECAWM off, and both DECSTR and RIS
	// restore auto-wrap through this method, so clear it here.
	s.pendingWrap = false
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

func (s *spanScreen) setView(v LockedView) {
	s.view = v
}

func (s *spanScreen) Line(y int) string {
	line := strings.Builder{}
	pos := 0
	for _, sp := range s.lines[y].spans {
		if sp.Text != "" {
			line.WriteString(sp.Text)
		} else {
			for range sp.Width {
				line.WriteRune(' ')
			}
		}
		pos += sp.Width
	}
	for pos < s.size.X {
		line.WriteRune(' ')
		pos++
	}
	return line.String()
}

func (s *spanScreen) StyledLine(x, w, y int) Line {
	spans, width := s.clippedStyledSpans(nil, x, w, y)
	return Line{
		Spans: spans,
		Width: width,
	}
}

// clippedStyledSpans collects the spans overlapping region (x,w,y) into dst.
// The spans share Text with the screen, so the returned slice must not be
// mutated in a way that changes the stored spans (append-only reuse is fine).
func (s *spanScreen) clippedStyledSpans(dst []Span, x, w, y int) ([]Span, int) {
	if w < 0 || x+w > s.size.X {
		w = s.size.X - x
	}
	if w < 0 {
		w = 0
	}
	if y < 0 || y >= len(s.lines) {
		return dst, w
	}

	originalSpans := s.lines[y].spans
	spans := dst
	if spans == nil {
		// Fresh result (StyledLine): preallocate for the worst case so the
		// append loop does not grow repeatedly.
		spans = make([]Span, 0, len(originalSpans))
	}

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
				_, sub, _ := splitSpan(sp, offset, s.textMode) // skip left part
				// now sub is from offset to end. we might need to truncate it if it's too long
				if sub.Width > width {
					keep, _, _ := splitSpan(sub, width, s.textMode)
					spans = append(spans, keep)
				} else {
					spans = append(spans, sub)
				}
			}
		}

		pos = endPos
	}

	return spans, w
}

func (s *spanScreen) StyledLines(r Region) []Line {
	lines := make([]Line, 0, max(0, r.Y2-r.Y))
	for y := r.Y; y < r.Y2; y++ {
		lines = append(lines, s.StyledLine(r.X, r.X2-r.X, y))
	}
	return lines
}

func (s *spanScreen) renderLineANSI(y int) string {
	if s.size.X == 0 {
		return ""
	}
	buf := make([]byte, 0, s.size.X+16)
	return string(appendLineSpansANSI(buf, s.lines[y].spans))
}

// appendLineSpansANSI appends spans as an ANSI line: one leading reset, then
// style deltas between adjacent spans. A full reset per span (the old
// behavior) dominated styled-line rendering.
func appendLineSpansANSI(dst []byte, spans []Span) []byte {
	if len(spans) == 0 {
		return dst
	}
	dst = append(dst, ESC, '[', '0', 'm')
	prev := NewStyle()
	for _, sp := range spans {
		if sp.Width == 0 {
			continue
		}
		dst = appendANSIEscapeFrom(dst, prev, sp.Style)
		prev = sp.Style
		if sp.Text == "" {
			for i := 0; i < sp.Width; i++ {
				dst = append(dst, ' ')
			}
		} else {
			dst = append(dst, sp.Text...)
		}
	}
	return dst
}

func (s *spanScreen) setStyle(style Style) {
	s.style = style
	s.frontend.StyleChanged(s.view, style)
}

func (s *spanScreen) setSize(w, h int) {
	if w <= 0 || h <= 0 {
		panic("Size must be > 0")
	}

	prevH := s.size.Y
	newLines := make([]spanLine, h)
	newWrapped := make([]bool, h)
	for y := 0; y < h; y++ {
		switch {
		case y < prevH && len(s.lines) > 0:
			line := s.lines[y]
			resizeLine(&line, w, s.style, s.textMode)
			newLines[y] = line
			if y < len(s.wrapped) {
				newWrapped[y] = s.wrapped[y]
			}
		default:
			newLines[y] = blankSpanLine(w, s.style)
		}
	}

	s.lines = newLines
	s.wrapped = newWrapped

	s.bottomMargin = h - (s.size.Y - s.bottomMargin)

	s.size = Pos{X: w, Y: h}
	s.pendingWrap = false

	// Keep the scroll margins valid after the height change: a shrink can
	// leave the bottom above the top (or both outside the new screen).
	s.topMargin = clamp(s.topMargin, 0, h-1)
	s.bottomMargin = clamp(s.bottomMargin, s.topMargin, h-1)

	// Clamp rather than reset: content is preserved, so the cursor should
	// stay near the same place. A cursor exactly at the old edge (or a saved
	// cursor restored later by DECRC) must not remain out of bounds.
	s.cursorPos.X = clamp(s.cursorPos.X, 0, w-1)
	s.cursorPos.Y = clamp(s.cursorPos.Y, 0, h-1)
	s.savedCursorPos.X = clamp(s.savedCursorPos.X, 0, w-1)
	s.savedCursorPos.Y = clamp(s.savedCursorPos.Y, 0, h-1)
}

// takeTopRows removes the top n rows, shifts the remaining content up by n,
// and blanks the freed bottom rows. It returns the removed rows and their
// wrapped flags without emitting any frontend callbacks; the caller decides
// how to capture them. n must be in [1, len(s.lines)].
func (s *spanScreen) takeTopRows(n int) ([]spanLine, []bool) {
	if n <= 0 || n > len(s.lines) {
		return nil, nil
	}
	lines := make([]spanLine, n)
	copy(lines, s.lines[:n])
	wrapped := make([]bool, n)
	copy(wrapped, s.wrapped[:n])

	copy(s.lines, s.lines[n:])
	copy(s.wrapped, s.wrapped[n:])
	for y := len(s.lines) - n; y < len(s.lines); y++ {
		// The vacated slots still share storage with the shifted rows (they
		// are the pre-copy originals), so they cannot be reset in place.
		s.lines[y] = blankSpanLine(s.size.X, s.style)
		s.wrapped[y] = false
	}
	return lines, wrapped
}

// shiftCursorUp moves the cursor up by n rows without emitting callbacks,
// clamping at the top of the screen.
func (s *spanScreen) shiftCursorUp(n int) {
	s.cursorPos.Y -= n
	if s.cursorPos.Y < 0 {
		s.cursorPos.Y = 0
	}
}

func (s *spanScreen) eraseRegion(r Region, cr ChangeReason) {
	r = s.clampRegion(r)

	// Fast path for clearing (using empty Text to represent spaces).
	emptySpan := Span{Style: s.style, Width: r.X2 - r.X}

	for i := r.Y; i < r.Y2; i++ {
		debugPrintln(debugErase, "erase: ", r.X, i, emptySpan.Width)
		s.rawWriteSpan(r.X, i, emptySpan, cr)
		if r.X == 0 && r.X2 == s.size.X && i < len(s.wrapped) {
			s.wrapped[i] = false
		}
	}
}

func (s *spanScreen) writeString(text string, width int, merge bool, mode TextReadMode) {
	if len(text) == 0 {
		return
	}
	text = sanitizeUTF8(text)
	s.textMode = mode
	if merge {
		s.mergeIntoPreviousCell(text)
		return
	}
	if width < 1 {
		width = 1
	}
	if width > s.size.X {
		// The run was read with a stale maxWidth (for example the terminal
		// was resized while the read loop was blocked). Split it at the
		// current screen width instead of clamping the width and storing
		// text that is wider than its cells.
		s.writeLongString(text, width, mode)
		return
	}
	if s.pendingWrap {
		s.pendingWrap = false
		s.wrapLine()
	}
	if s.cursorPos.X+width > s.size.X {
		if s.autoWrap {
			s.wrapLine()
		} else {
			s.cursorPos.X = s.size.X - width
		}
	}
	sp := Span{Style: s.style, Text: text, Width: width, Link: s.link}
	// Canonicalize runs of plain ASCII spaces as blank spans (empty Text),
	// matching how blank fills are stored. Blank spans never carry links.
	// Mixed text runs (e.g. "a  ") are left untouched.
	allSpaces := true
	for i := 0; i < len(text); i++ {
		if text[i] != ' ' {
			allSpaces = false
			break
		}
	}
	if allSpaces {
		sp.Text = ""
		sp.Link = ""
	}
	s.rawWriteSpan(s.cursorPos.X, s.cursorPos.Y, sp, CRText)

	newX := s.cursorPos.X + width
	if s.autoWrap {
		if newX >= s.size.X {
			// Defer the wrap: stay on the last column until the next
			// printable write actually needs a new row.
			s.cursorPos.X = s.size.X - 1
			s.pendingWrap = true
		} else {
			s.cursorPos.X = newX
		}
	} else {
		s.cursorPos.X = clamp(newX, 0, s.size.X-1)
	}
	s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
}

// writeLongString writes a text run wider than the screen, splitting it at
// grapheme boundaries and letting each chunk wrap normally. It is used when a
// run was buffered before a resize made the screen narrower.
func (s *spanScreen) writeLongString(text string, width int, mode TextReadMode) {
	sp := Span{Style: s.style, Text: text, Width: width}
	for sp.Width > 0 {
		remaining := s.size.X - s.cursorPos.X
		if s.pendingWrap || remaining < 1 {
			remaining = s.size.X
		}
		if remaining >= sp.Width {
			s.writeString(sp.Text, sp.Width, false, mode)
			return
		}
		left, right, wide := splitSpan(sp, remaining, mode)
		if left.Width > 0 {
			s.writeString(left.Text, left.Width, false, mode)
		}
		if wide.Width > 0 {
			// The boundary fell inside a wide cluster. Write it as a whole
			// when it can fit on the screen; otherwise it cannot be shown.
			if wide.Width <= s.size.X {
				s.writeString(wide.Text, wide.Width, false, mode)
			}
			sp = right
			continue
		}
		sp = right
	}
}

// wrapLine advances to column 0 of the next row, scrolling the scroll region
// when already at its bottom margin, and marks the new row as a continuation
// of the previous one. It does not emit CursorMoved; the caller does that once
// it knows the final position.
func (s *spanScreen) wrapLine() {
	s.cursorPos.X = 0
	s.cursorPos.Y++
	if s.cursorPos.Y > s.bottomMargin {
		s.scroll(s.topMargin, s.bottomMargin, s.bottomMargin-s.cursorPos.Y)
		s.cursorPos.Y = s.bottomMargin
	}
	if s.cursorPos.Y < len(s.wrapped) {
		s.wrapped[s.cursorPos.Y] = true
	}
}

func (s *spanScreen) rawWriteSpan(x int, y int, sp Span, cr ChangeReason) {
	if sp.Width <= 0 {
		return
	}
	if y >= s.size.Y || x+sp.Width > s.size.X {
		panic(fmt.Sprintf("rawWriteSpan out of range: %v  %v,%v,%v %v\n", s.size, x, y, x+sp.Width, sp.Width))
	}
	replaceRange(&s.lines[y], x, sp.Width, sp, s.textMode)
	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: x + sp.Width}, cr)
}

func (s *spanScreen) rawWriteRune(x int, y int, r rune, width int, cr ChangeReason) {
	if width < 1 {
		width = 1
	}
	if y >= s.size.Y || x+width > s.size.X {
		panic(fmt.Sprintf("rawWriteRune out of range: %v  %v,%v,%v %v %#v\n", s.size, x, y, x+width, width, string(r)))
	}

	var sp Span
	if width == 1 && r == ' ' {
		sp = Span{Style: s.style, Width: 1, Link: s.link}
	} else {
		sp = Span{Style: s.style, Text: string(r), Width: width, Link: s.link}
	}

	replaceRange(&s.lines[y], x, width, sp, s.textMode)
	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: x + width}, cr)
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

	line := &s.lines[y]
	// Delete characters from x to x+n, shift remaining chars left, and append spaces at the end
	replaceRange(line, x, n, Span{}, s.textMode)
	// Now append spaces to fill the end to width s.size.X
	curWidth := lineCellWidth(line)
	if curWidth < s.size.X {
		line.spans = append(line.spans, Span{Style: s.style, Width: s.size.X - curWidth})
		line.width = s.size.X
	}

	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

func (s *spanScreen) setCursorPos(x, y int) {
	s.pendingWrap = false
	oldY := s.cursorPos.Y
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	// Moving to a different row breaks that row's wrap continuation. A
	// same-row cursor move (e.g. the column reset part of LF) must not:
	// the physical row is still a continuation of the one above it.
	if s.cursorPos.Y != oldY && s.cursorPos.Y < len(s.wrapped) {
		s.wrapped[s.cursorPos.Y] = false
	}
	s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
}

func (s *spanScreen) setScrollMarginTopBottom(top, bottom int) {
	debugPrintln(debugScroll, "scroll margins:", top, bottom)
	top = clamp(top, 0, s.size.Y-1)
	bottom = clamp(bottom, 0, s.size.Y-1)
	if top > bottom {
		// Invalid margins are ignored, leaving the previous region intact.
		return
	}
	s.topMargin = top
	s.bottomMargin = bottom
}

func (s *spanScreen) scroll(y1 int, y2 int, dy int) {
	debugPrintln(debugScroll, "scroll:", y1, y2, dy)
	y1 = clamp(y1, 0, s.size.Y-1)
	y2 = clamp(y2, 0, s.size.Y-1)
	if y1 > y2 {
		debugPrintln(debugScroll, "scroll ys out of order: ", y1, y2, dy)
		return
	}
	// Scrolling by more than the region height has the same effect as
	// scrolling by exactly the region height; clamping keeps the row
	// indices (and the capture count) inside the region.
	height := y2 - y1 + 1
	dy = clamp(dy, -height, height)
	if dy != 0 && s.scrollDelta != nil {
		s.scrollDelta(dy)
	}

	// Save the |dy| rows that scroll out of the region so their span storage
	// can be recycled into the vacated rows instead of allocating new blank
	// lines. The scratch slices are reused between scrolls; scrollCapture
	// copies the rows it needs (it must not retain them).
	n := dy
	if n < 0 {
		n = -n
	}
	if cap(s.scrollScratch) < n {
		s.scrollScratch = make([]spanLine, n)
	}
	if cap(s.scrollScratchW) < n {
		s.scrollScratchW = make([]bool, n)
	}
	saved := s.scrollScratch[:n]
	savedW := s.scrollScratchW[:n]

	var saveStart int
	if dy < 0 {
		saveStart = y1 // rows scrolling off the top (captured into scrollback)
	} else {
		saveStart = y2 - n + 1 // rows scrolling off the bottom
	}
	for i := 0; i < n; i++ {
		y := saveStart + i
		saved[i] = s.lines[y]
		savedW[i] = false
		if y < len(s.wrapped) {
			savedW[i] = s.wrapped[y]
		}
	}

	if dy < 0 {
		// Content shifts up; lines at the top scroll off. Only capture when
		// the scroll region starts at the top of the screen (y1 == 0, already
		// clamped above). A mid-screen scroll region (e.g. CSI r margins with
		// top > 1) recycles lines within the region, so pushing those interior
		// rows into scrollback would corrupt history; xterm only scrolls into
		// scrollback from the top of the screen.
		if s.scrollCapture != nil && y1 == 0 {
			s.scrollCapture(saved, savedW)
		}

		for y := y1; y <= y2+dy; y++ {
			s.lines[y] = s.lines[y-dy]
			if y < len(s.wrapped) {
				s.wrapped[y] = s.wrapped[y-dy]
			}
		}
	} else {
		for y := y2; y >= y1+dy; y-- {
			s.lines[y] = s.lines[y-dy]
			if y < len(s.wrapped) {
				s.wrapped[y] = s.wrapped[y-dy]
			}
		}
	}

	// Fill the vacated rows: bottom rows when scrolling up, top rows when
	// scrolling down.
	fillStart := y1
	if dy < 0 {
		fillStart = y2 + dy + 1
	}
	for i := 0; i < n; i++ {
		y := fillStart + i
		resetSpanLine(&saved[i], s.size.X, s.style)
		s.lines[y] = saved[i]
		if y < len(s.wrapped) {
			s.wrapped[y] = false
		}
	}

	if dy > 0 {
		s.frontend.RegionChanged(s.view, Region{Y: y1 + dy, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
		debugPrintln(debugScroll, "scroll changed region:", Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X})
		s.frontend.RegionChanged(s.view, Region{Y: y1, Y2: y1 + dy, X: 0, X2: s.size.X}, CRScroll)
	} else {
		s.frontend.RegionChanged(s.view, Region{Y: y1, Y2: y2 + dy + 1, X: 0, X2: s.size.X}, CRScroll)
		s.frontend.RegionChanged(s.view, Region{Y: y2 + dy + 1, Y2: y2 + 1, X: 0, X2: s.size.X}, CRScroll)
	}
}

func (s *spanScreen) clampRegion(r Region) Region {
	return r.Clamp(Region{0, 0, s.size.X, s.size.Y})
}

func (s *spanScreen) moveCursor(dx, dy int, wrap bool, scroll bool) {
	s.pendingWrap = false
	if wrap && s.autoWrap {
		s.cursorPos.X += dx
		for s.cursorPos.X < 0 {
			s.cursorPos.X += s.size.X
			s.cursorPos.Y--
		}
		for s.cursorPos.X >= s.size.X {
			s.cursorPos.X -= s.size.X
			s.cursorPos.Y++
			if s.cursorPos.Y < len(s.wrapped) {
				s.wrapped[s.cursorPos.Y] = true
			}
		}
	} else {
		s.cursorPos.X += dx
		s.cursorPos.X = clamp(s.cursorPos.X, 0, s.size.X-1)
	}

	s.cursorPos.Y += dy
	if scroll && dy != 0 {
		// Margins only affect vertical movement. A carriage return at a row
		// outside the scroll region must not scroll it.
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
	s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
}

// decaln implements DECALN (ESC # 8): fill the whole screen with 'E' using
// the current style, clear wrap continuation flags, and home the cursor.
func (s *spanScreen) decaln() {
	for y := 0; y < s.size.Y; y++ {
		s.rawWriteSpan(0, y, Span{Style: s.style, Text: strings.Repeat("E", s.size.X), Width: s.size.X}, CRClear)
		if y < len(s.wrapped) {
			s.wrapped[y] = false
		}
	}
	s.setCursorPos(0, 0)
}

func (s *spanScreen) saveCursorPos() {
	s.savedCursorPos = s.cursorPos
	s.savedPendingWrap = s.pendingWrap
	s.savedStyle = s.style
}

func (s *spanScreen) copyTransientState(from screen) {
	if other, ok := from.(*spanScreen); ok {
		s.cursorPos = other.cursorPos
		s.pendingWrap = other.pendingWrap
		s.style = other.style
		return
	}
	s.cursorPos = from.CursorPos()
	s.style = from.Style()
}

func (s *spanScreen) restoreCursorPos() {
	s.cursorPos = s.savedCursorPos
	s.pendingWrap = s.savedPendingWrap
	s.style = s.savedStyle
	s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
	s.frontend.StyleChanged(s.view, s.style)
}

func (s *spanScreen) setScrollCapture(cb func(lines []spanLine, wrapped []bool)) {
	s.scrollCapture = cb
}

func (s *spanScreen) setScrollDelta(cb func(delta int)) {
	s.scrollDelta = cb
}

func (s *spanScreen) setLink(link string) {
	s.link = link
}

// snapshotState returns a deep copy of the screen buffer and cursor state.
func (s *spanScreen) snapshotState() ScreenState {
	lines := make([]Line, len(s.lines))
	for y, sl := range s.lines {
		spans := make([]Span, len(sl.spans))
		copy(spans, sl.spans)
		lines[y] = Line{Spans: spans, Width: sl.width}
	}
	wrapped := make([]bool, len(s.wrapped))
	copy(wrapped, s.wrapped)
	return ScreenState{
		Width:            s.size.X,
		Height:           s.size.Y,
		Lines:            lines,
		Wrapped:          wrapped,
		Cursor:           s.cursorPos,
		PendingWrap:      s.pendingWrap,
		SavedCursor:      s.savedCursorPos,
		SavedPendingWrap: s.savedPendingWrap,
		SavedStyle:       s.savedStyle,
		Style:            s.style,
		TopMargin:        s.topMargin,
		BottomMargin:     s.bottomMargin,
		AutoWrap:         s.autoWrap,
	}
}

// restoreState replaces the screen state with st, resizing the screen to
// st.Width/st.Height. Existing lines are truncated/padded to the new width so
// a snapshot captured at another size can be restored. It emits no callbacks;
// the terminal is responsible for the single full repaint.
func (s *spanScreen) restoreState(st ScreenState) {
	if st.Width <= 0 || st.Height <= 0 {
		return
	}
	w, h := st.Width, st.Height
	lines := make([]spanLine, h)
	for y := 0; y < h; y++ {
		if y >= len(st.Lines) {
			lines[y] = blankSpanLine(w, st.Style)
			continue
		}
		spans := make([]Span, len(st.Lines[y].Spans))
		copy(spans, st.Lines[y].Spans)
		line := spanLine{spans: spans, width: st.Lines[y].Width}
		resizeLine(&line, w, st.Style, s.textMode)
		lines[y] = line
	}
	wrapped := make([]bool, h)
	copy(wrapped, st.Wrapped)

	s.lines = lines
	s.wrapped = wrapped
	s.size = Pos{X: w, Y: h}
	s.cursorPos = clampPos(st.Cursor, w, h)
	s.pendingWrap = st.PendingWrap
	s.savedCursorPos = clampPos(st.SavedCursor, w, h)
	s.savedPendingWrap = st.SavedPendingWrap
	s.savedStyle = st.SavedStyle
	s.style = st.Style
	s.topMargin = clamp(st.TopMargin, 0, h-1)
	s.bottomMargin = clamp(st.BottomMargin, s.topMargin, h-1)
	s.autoWrap = st.AutoWrap
}

func clampPos(p Pos, w, h int) Pos {
	return Pos{X: clamp(p.X, 0, w-1), Y: clamp(p.Y, 0, h-1)}
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
		lstr = strings.ReplaceAll(lstr, "\000", " ")
		fmt.Printf("\033[m|%s\033[m|\n", lstr)
	}
	fmt.Print("+")
	for i := 0; i < w; i++ {
		fmt.Print("-")
	}
	fmt.Println("+")
}

func blankSpanLine(width int, style Style) spanLine {
	return spanLine{spans: []Span{{Style: style, Width: width}}, width: width}
}

// resetSpanLine turns line into a blank line of width cells, reusing its span
// backing array when possible so scrolling does not allocate per row.
func resetSpanLine(line *spanLine, width int, style Style) {
	if cap(line.spans) >= 1 {
		line.spans = line.spans[:1]
		line.spans[0] = Span{Style: style, Width: width}
		line.width = width
		return
	}
	*line = blankSpanLine(width, style)
}

func resizeLine(line *spanLine, width int, style Style, mode TextReadMode) {
	cur := lineCellWidth(line)
	if cur > width {
		truncateLine(line, width, mode)
		// Truncating may drop a whole wide cluster at the boundary, which
		// can leave the line narrower than the requested width. Pad it back
		// so every resized line is exactly the screen width.
		cur = lineCellWidth(line)
	}
	if cur < width {
		line.spans = append(line.spans, Span{Style: style, Width: width - cur})
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

// sanitizeUTF8 replaces each invalid byte with U+FFFD without collapsing runs
// of invalid bytes. The reader counts one cell per invalid byte, so collapsing
// a run (as strings.ToValidUTF8 does) would desynchronize text from width.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
			s = s[1:]
			continue
		}
		b.WriteString(s[:size])
		s = s[size:]
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// splitSpan splits a span at cellOffset. Returns (left, right, brokeWideCluster).
// brokeWideCluster is not an empty span if the split point falls within a wide grapheme cluster.
// When brokeWideCluster is not empty, neither left nor right contains the broken cluster.
func splitSpan(sp Span, cellOffset int, mode TextReadMode) (left Span, right Span, splitWide Span) {
	if cellOffset <= 0 {
		return Span{}, sp, Span{}
	}
	if cellOffset >= sp.Width {
		return sp, Span{}, Span{}
	}

	if sp.Text == "" {
		// Blank runs are made of single-cell spaces, so they never break a wide cluster.
		left := sp
		left.Width = cellOffset
		right := sp
		right.Width = sp.Width - cellOffset
		return left, right, Span{}
	}

	if sp.Width == len(sp.Text) && isASCII(sp.Text) {
		// One byte per cell, so no cluster can straddle the split. This holds
		// in grapheme mode too: all-ASCII text is one cluster per byte.
		leftText := sp.Text[:cellOffset]
		rightText := sp.Text[cellOffset:]
		left := sp
		left.Text = leftText
		left.Width = cellOffset
		right := sp
		right.Text = rightText
		right.Width = sp.Width - cellOffset
		return left, right, Span{}
	}

	// Text mode - check if we're breaking a wide grapheme cluster
	idx := 0
	state := -1
	cellPos := 0
	textBytes := []byte(sp.Text)

	for idx < len(textBytes) {
		cluster, consumed, width, newState, ok := stepTextCluster(textBytes[idx:], state, mode)
		if !ok || consumed <= 0 {
			break
		}
		if width < 1 {
			width = 0
		}

		clusterEnd := cellPos + width
		if cellOffset > cellPos && cellOffset < clusterEnd {
			// Split point is within this cluster
			if width > 1 {
				// We're breaking a wide cluster - return the wide char we're splitting
				leftText := sp.Text[:idx]
				rightText := sp.Text[idx+consumed:]

				left := sp
				left.Text = leftText
				left.Width = cellPos

				right := sp
				right.Text = rightText
				right.Width = sp.Width - clusterEnd

				// Return the wide character that was split
				wideChar := sp
				wideChar.Text = string(cluster)
				wideChar.Width = width

				return left, right, wideChar
			}
			break
		}

		cellPos = clusterEnd
		idx += consumed
		state = newState
	}

	// Normal split that doesn't break a wide cluster
	byteIdx, leftWidth := byteIndexForCell(textBytes, cellOffset, mode)
	leftText := sp.Text[:byteIdx]
	rightText := sp.Text[byteIdx:]

	left = sp
	left.Text = leftText
	left.Width = leftWidth

	right = sp
	right.Text = rightText
	right.Width = sp.Width - leftWidth

	return left, right, Span{}
}

func byteIndexForCell(text []byte, cellOffset int, mode TextReadMode) (int, int) {
	if cellOffset <= 0 || len(text) == 0 {
		return 0, 0
	}
	// ASCII text has one byte (and one cell) per rune in both modes, so the
	// cell boundary is the byte boundary. This is the common case when
	// overwriting an ASCII run in place.
	if cellOffset <= len(text) {
		ascii := true
		for _, b := range text {
			if b >= utf8.RuneSelf {
				ascii = false
				break
			}
		}
		if ascii {
			return cellOffset, cellOffset
		}
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
	// Zero-width clusters immediately after the boundary belong to the
	// preceding cell (combining marks, format controls). Consume them with
	// the left side of the split so overwriting the next cell does not drop
	// them.
	for width >= cellOffset && idx < len(text) {
		_, consumed, cellWidth, newState, ok := stepTextCluster(text[idx:], state, mode)
		if !ok || consumed <= 0 || cellWidth > 0 {
			break
		}
		idx += consumed
		state = newState
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

// locateSpanRange locates the start and end of a replacement window in one
// pass. startIdx == len(spans) means x is past the end of the line. endIdx
// defaults to the last span with offset 0 when the end is past the line,
// matching the boundary semantics replaceRange relies on.
func locateSpanRange(spans []Span, x, end int) (startIdx, startOffset, endIdx, endOffset int) {
	startIdx = len(spans)
	endIdx = len(spans) - 1
	pos := 0
	for i := 0; i < len(spans); i++ {
		endPos := pos + spans[i].Width
		if startIdx == len(spans) {
			if x < endPos {
				startIdx = i
				startOffset = x - pos
			} else {
				pos = endPos
				continue
			}
		}
		if end <= endPos {
			endIdx = i
			endOffset = end - pos
			return
		}
		pos = endPos
	}
	return
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

	// The line width is the sum of all span widths. Never derive it from the
	// boundary scan below: the scan stops at the end of the replacement window,
	// not at the end of the line.
	totalWidth := 0
	for _, sp := range spans {
		totalWidth += sp.Width
	}

	// Clamp negative inputs.
	if x < 0 {
		x = 0
	}
	if n < 0 {
		n = 0
	}
	if x > totalWidth {
		x = totalWidth
	}
	if n > totalWidth-x {
		n = totalWidth - x
	}

	// Expand the replacement window so it never covers only part of a wide
	// grapheme cluster. The old wide character must be consumed exactly once;
	// leaving half of it behind would corrupt both the span widths and the
	// rendered line. Locate both boundaries in one pass; only a mid-cluster
	// boundary (rare) forces a second look.
	startPadCells := 0
	endPadCells := 0
	startIdx, startOffset, endIdx, endOffset := locateSpanRange(spans, x, x+n)
	relocate := false
	if x < totalWidth {
		if startIdx < len(spans) && startOffset > 0 && startOffset < spans[startIdx].Width {
			left, _, wide := splitSpan(spans[startIdx], startOffset, mode)
			if wide.Width > 0 {
				wideStart := startOffset - left.Width
				x -= wideStart
				n += wideStart
				startPadCells = wideStart
				relocate = true
			}
		}
	}
	if relocate {
		startIdx, startOffset, endIdx, endOffset = locateSpanRange(spans, x, x+n)
	}
	if end := x + n; end > 0 && end < totalWidth {
		if endIdx < len(spans) && endOffset > 0 && endOffset < spans[endIdx].Width {
			left, _, wide := splitSpan(spans[endIdx], endOffset, mode)
			if wide.Width > 0 {
				wideEnd := left.Width + wide.Width
				endPadCells = wideEnd - endOffset
				n += endPadCells
				relocate = true
			}
		}
	}
	if relocate {
		startIdx, startOffset, endIdx, endOffset = locateSpanRange(spans, x, x+n)
	}

	// A real write (insert.Width > 0) must keep the line's total width. Pad
	// the inserted content with blanks on the side(s) where the window was
	// expanded, plus any cells by which the original window exceeded the
	// inserted content. Deletions/truncations (insert.Width == 0) keep their
	// shrink behavior; deleteChars re-pads and truncateLine must shrink.
	padLeft := 0
	padRight := 0
	if insert.Width > 0 && n > insert.Width {
		need := n - insert.Width
		padLeft = min(startPadCells, need)
		need -= padLeft
		padRight = min(endPadCells, need)
		need -= padRight
		padRight += need
	}
	insertWidth := insert.Width + padLeft + padRight
	var paddedInserts [3]Span
	insertParts := paddedInserts[:0]
	if padLeft > 0 {
		insertParts = append(insertParts, Span{Style: insert.Style, Width: padLeft})
	}
	if insert.Width > 0 {
		insertParts = append(insertParts, insert)
	}
	if padRight > 0 {
		insertParts = append(insertParts, Span{Style: insert.Style, Width: padRight})
	}

	if x == 0 && n >= totalWidth {
		line.spans = append(line.spans[:0], insertParts...)
		line.width = insertWidth
		return
	}

	// If we never intersected, treat it like an append.
	if startIdx == len(spans) {
		if insertWidth > 0 {
			line.spans = append(line.spans, insertParts...)
			line.width = totalWidth + insertWidth
		}
		return
	}

	// Fast paths for in-place replacements inside a single span. Padding adds
	// multiple spans, so those cases fall through to the general path.
	if startIdx == endIdx && len(insertParts) == 1 {
		sp := spans[startIdx]
		ins := insertParts[0]
		if startOffset == 0 && endOffset == sp.Width {
			spans[startIdx] = ins
			line.width = totalWidth - n + insertWidth
			return
		}
		// Compare the complete style and link. Color-only comparison used to
		// keep the old span's mode bits (bold, italic, underline, ...) when
		// overwriting text in place, and could skip the update entirely.
		if ins.Width == n && sp.Style == ins.Style && sp.Link == ins.Link {
			if sp.Text == "" && ins.Text == "" {
				return
			}
			if sp.Text != "" && ins.Text != "" {
				// Both boundaries are cluster-aligned (the window was expanded
				// above), so the text can be spliced without changing widths.
				textBytes := []byte(sp.Text)
				startByte, _ := byteIndexForCell(textBytes, startOffset, mode)
				endByte, _ := byteIndexForCell(textBytes, startOffset+n, mode)
				if startByte <= endByte && endByte <= len(textBytes) {
					sp.Text = sp.Text[:startByte] + ins.Text + sp.Text[endByte:]
					spans[startIdx] = sp
					line.width = totalWidth
					return
				}
			}
		}
	}

	// Split the boundary spans so we can keep the untouched parts. The window
	// was expanded above, so these splits fall on cluster boundaries.
	var left Span
	hasLeft := false
	if startIdx < len(spans) && startOffset > 0 {
		left, _, _ = splitSpan(spans[startIdx], startOffset, mode)
		hasLeft = left.Width > 0
	}

	var right Span
	hasRight := false
	if endIdx >= 0 && endIdx < len(spans) {
		if endOffset < spans[endIdx].Width {
			_, right, _ = splitSpan(spans[endIdx], endOffset, mode)
			hasRight = right.Width > 0
		}
	}

	// Compute the new slice length and where the suffix begins.
	prefixLen := startIdx
	suffixStart := endIdx + 1
	newLen := prefixLen
	insertCount := len(insertParts)
	if hasLeft {
		insertCount++
	}
	if hasRight {
		insertCount++
	}
	newLen += insertCount
	if suffixStart < len(spans) {
		newLen += len(spans) - suffixStart
	}
	newWidth := totalWidth - n + insertWidth

	// Ensure capacity then populate the new span layout. Growth leaves
	// headroom (~1.5x, at least 4 extra spans) so appending a few spans does
	// not reallocate and copy the whole line on every write; allocating an
	// exact-size slice here made styled output allocate on nearly every cell.
	if cap(spans) < newLen {
		headroom := newLen / 2
		if headroom < 4 {
			headroom = 4
		}
		if headroom > 8 {
			headroom = 8
		}
		newCap := newLen + headroom
		newSpans := make([]Span, newLen, newCap)
		copy(newSpans, spans[:prefixLen])
		dest := prefixLen
		if hasLeft {
			newSpans[dest] = left
			dest++
		}
		if len(insertParts) > 0 {
			dest += copy(newSpans[dest:], insertParts)
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
	if len(insertParts) > 0 {
		dest += copy(line.spans[dest:], insertParts)
	}
	if hasRight {
		line.spans[dest] = right
	}

	if suffixLen > 0 && destAfter <= suffixStart {
		copy(line.spans[destAfter:], spans[suffixStart:])
	}
	line.width = newWidth
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

// isolateCell splits sp so the cell starting at offset is its own span.
// When the split would break a wide cluster, sp is returned as the target
// unchanged (the whole cluster is the merge target).
func isolateCell(sp Span, offset int, mode TextReadMode) (left, target, right Span) {
	if offset > 0 {
		l, r, wide := splitSpan(sp, offset, mode)
		if wide.Width > 0 {
			return Span{}, sp, Span{}
		}
		left = l
		sp = r
	}
	if sp.Width <= 1 {
		return left, sp, Span{}
	}
	t, r, wide := splitSpan(sp, 1, mode)
	if wide.Width > 0 {
		return left, sp, Span{}
	}
	return left, t, r
}

func (s *spanScreen) mergeIntoPreviousCell(text string) {
	text = sanitizeUTF8(text)
	y := s.cursorPos.Y
	line := &s.lines[y]
	cell := s.cursorPos.X - 1
	// With a pending wrap the cursor still sits on the cell that was just
	// written, so the merge target is that cell rather than the one before.
	if s.pendingWrap {
		cell = s.cursorPos.X
	}
	if cell < 0 {
		return
	}

	// Find the span at cell position
	idx, offset := findSpanAtX(line, cell)
	if idx >= len(line.spans) {
		return
	}

	// Isolate the target cell from its neighbours. A blank run (or a text
	// run) can cover many cells, and appending the mark to the whole span
	// would attach it to the wrong cell. A split inside a wide cluster
	// cannot isolate one cell; keep the whole wide span as the target.
	left, target, right := isolateCell(line.spans[idx], offset, s.textMode)
	if left.Width > 0 || right.Width > 0 {
		origIdx := idx
		newSpans := make([]Span, 0, len(line.spans)+2)
		newSpans = append(newSpans, line.spans[:origIdx]...)
		if left.Width > 0 {
			newSpans = append(newSpans, left)
		}
		idx = len(newSpans)
		newSpans = append(newSpans, target)
		if right.Width > 0 {
			newSpans = append(newSpans, right)
		}
		newSpans = append(newSpans, line.spans[origIdx+1:]...)
		line.spans = newSpans
	}

	// Never merge text with a different style or link into the previous
	// span. Keep it as a zero-width span so the cell content and style
	// boundary both survive.
	sp := &line.spans[idx]
	if sp.Style != s.style || sp.Link != s.link {
		line.spans = append(line.spans, Span{})
		copy(line.spans[idx+2:], line.spans[idx+1:])
		line.spans[idx+1] = Span{Style: s.style, Text: text, Width: 0, Link: s.link}
	} else {
		// Convert repeat to text if needed and merge.
		base := sp.Text
		if base == "" {
			base = strings.Repeat(" ", sp.Width)
		}
		// A variation selector / keycap / emoji modifier can turn a
		// one-cell base into a two-cell emoji presentation. Widen the
		// cell and consume the following column (or skip the widening at
		// the right edge).
		if sp.Width == 1 && emojiPresentationAdding(text) && cell+2 <= s.size.X && !s.pendingWrap {
			widened := Span{Style: sp.Style, Text: base + text, Width: 2, Link: sp.Link}
			replaceRange(line, cell, 2, widened, s.textMode)
			if s.cursorPos.X == cell+1 {
				s.cursorPos.X++
				s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
			}
			s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: cell, X2: cell + 2}, CRText)
			return
		}
		sp.Text = base + text
		// Width doesn't change for an ordinary merge.
	}

	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: cell, X2: cell + 1}, CRText)
}

// emojiPresentationAdding reports whether text contains a code point that can
// turn a one-cell base into a two-cell emoji presentation.
func emojiPresentationAdding(text string) bool {
	for _, r := range text {
		switch {
		case r == 0xfe0f, r == 0x20e3:
			return true
		case r >= 0x1f3fb && r <= 0x1f3ff:
			return true
		}
	}
	return false
}
