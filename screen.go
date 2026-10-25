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
	// writeStringMeta is writeString for a run whose tokenizer metadata is
	// already known (see GraphemeReader.readPrintableRun).
	writeStringMeta(text string, width int, merge bool, mode TextReadMode, meta printableRunMeta)
	rawWriteRune(x int, y int, r rune, width int, cr ChangeReason)
	deleteChars(x int, y int, n int, cr ChangeReason)
	// insertChars shifts the cells at x and to the right rightwards by n,
	// inserting n blanks at x and dropping whatever falls off the right edge.
	insertChars(x int, y int, n int, cr ChangeReason)
	// InsertMode reports and sets IRM (CSI 4 h/l): printable writes insert
	// instead of overwriting.
	InsertMode() bool
	SetInsertMode(value bool)
	// OriginMode reports and sets DECOM (CSI ? 6 h/l). In origin mode the
	// cursor cannot leave the scroll margins and CUP/VPA rows are relative
	// to the top margin.
	OriginMode() bool
	SetOriginMode(value bool)
	// homeCursor moves to the home position, which is (0,0) normally and the
	// top-left of the scroll region in origin mode.
	homeCursor()
	// repeatLastChar implements REP (CSI b); clearLastChar invalidates it.
	repeatLastChar(count int)
	clearLastChar()
	// Horizontal tab stops (HTS, TBC).
	nextTabStop(x int, count int) int
	prevTabStop(x int, count int) int
	setTabStop(x int)
	clearTabStop(x int)
	clearAllTabStops()
	resetTabStops()
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
	// designateCharset designates cs into slot (G0-G3).
	designateCharset(slot int, cs charset)
	// invokeCharset selects slot (G0-G3) as the GL set (SO/SI, LS2/LS3).
	invokeCharset(slot int)
	// resetCharsets restores the default ASCII designations with G0 invoked.
	resetCharsets()
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

	// charsets holds the G0-G3 designations and the invoked GL slot. It is
	// shared across the main/alt switch (copyTransientState) and saved by
	// DECSC.
	charsets      charsetState
	savedCharsets charsetState

	// insertMode is IRM: printable writes shift the rest of the row right
	// instead of overwriting. Per-screen, like xterm's INSERT flag.
	insertMode bool

	// originMode is DECOM: vertical cursor addressing is relative to the top
	// margin and the cursor is confined to the scroll region.
	originMode      bool
	savedOriginMode bool

	// lastChar is the last graphic character as it appeared in the input
	// stream, replayed by REP. Like xterm's parser-level lastchar it is a
	// single code point: the current charset mapping and attributes are
	// applied when it is replayed. 0 means nothing has been written yet.
	lastChar rune

	// tabs is the horizontal tab stop map, always len(size.X). Default stops
	// are every defaultTabInterval columns.
	tabs []bool

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
	s.resizeTabs(w)

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
	if s.originMode {
		s.cursorPos.Y = clamp(s.cursorPos.Y, s.topMargin, s.bottomMargin)
	} else {
		s.cursorPos.Y = clamp(s.cursorPos.Y, 0, h-1)
	}
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
	s.writeStringMeta(text, width, merge, mode, analyzePrintableRun(text))
}

// writeStringMeta is writeString for a run whose tokenizer metadata is already
// known. It avoids re-validating the UTF-8, rescanning the run for spaces, and
// decoding its final rune.
func (s *spanScreen) writeStringMeta(text string, width int, merge bool, mode TextReadMode, meta printableRunMeta) {
	if len(text) == 0 {
		return
	}
	if !meta.validUTF8 {
		text = sanitizeUTF8(text)
	}
	s.textMode = mode
	// Remember the last graphic character for REP before the charset mapping:
	// REP re-maps and re-styles it when it is replayed. The reader reports the
	// final rune even for invalid input (it tokenizes invalid bytes as U+FFFD).
	s.lastChar = meta.lastRune
	if merge {
		s.mergeIntoPreviousCell(text)
		return
	}
	if width < 1 {
		width = 1
	}
	// DEC Special Graphics replaces the printable stream characters, so the
	// mapping happens before the run is measured against the screen width.
	// Mapped runes are one cell wide, so width stays valid.
	if s.charsets.gl() == charsetDECGraphics {
		text = mapDECGraphics(text)
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
	if s.insertMode {
		// IRM: push the rest of the row right so the run is inserted rather
		// than overwritten.
		s.insertChars(s.cursorPos.X, s.cursorPos.Y, width, CRText)
	}
	sp := Span{Style: s.style, Text: text, Width: width, Link: s.link}
	// Canonicalize runs of plain ASCII spaces as blank spans (empty Text),
	// matching how blank fills are stored. Blank spans never carry links.
	// Mixed text runs (e.g. "a  ") are left untouched. The tokenizer reports
	// this, and DEC Special Graphics does not map ASCII space, so the flag is
	// still correct after mapDECGraphics.
	if meta.allSpaces {
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
	if replaceRange(&s.lines[y], x, sp.Width, sp, s.textMode) || cr != CRText {
		s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: x + sp.Width}, cr)
	}
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

	if replaceRange(&s.lines[y], x, width, sp, s.textMode) || cr != CRText {
		s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: x + width}, cr)
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

// insertChars implements ICH: the cells from x onwards shift right by n,
// blank cells are inserted at x, and cells pushed past the right edge are
// discarded. The cursor does not move.
func (s *spanScreen) insertChars(x, y, n int, cr ChangeReason) {
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
	if n > s.size.X-x {
		n = s.size.X - x
	}

	line := &s.lines[y]
	// Drop the cells that no longer fit, then insert the blanks. Inserted
	// blanks carry the current style but never a hyperlink, matching
	// deleteChars' padding.
	truncateLine(line, s.size.X-n, s.textMode)
	replaceRange(line, x, 0, Span{Style: s.style, Width: n}, s.textMode)
	if curWidth := lineCellWidth(line); curWidth < s.size.X {
		line.spans = append(line.spans, Span{Style: s.style, Width: s.size.X - curWidth})
		line.width = s.size.X
	}

	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: x, X2: s.size.X}, cr)
}

func (s *spanScreen) InsertMode() bool {
	return s.insertMode
}

func (s *spanScreen) SetInsertMode(value bool) {
	s.insertMode = value
}

func (s *spanScreen) OriginMode() bool {
	return s.originMode
}

func (s *spanScreen) SetOriginMode(value bool) {
	s.originMode = value
}

func (s *spanScreen) homeCursor() {
	y := 0
	if s.originMode {
		y = s.topMargin
	}
	s.setCursorPos(0, y)
}

// maxRepeatChars bounds REP so a single escape cannot wedge the terminal.
const maxRepeatChars = 65535

// repeatLastChar implements REP (CSI b): re-emit the last graphic character
// count times. Like xterm, the repeat uses the current attributes and charset,
// and a zero-width last character (a combining mark, for example) repeats
// nothing.
func (s *spanScreen) repeatLastChar(count int) {
	if s.lastChar == 0 || count <= 0 {
		return
	}
	width := cellWidth(s.lastChar)
	if width <= 0 {
		return
	}
	if count > maxRepeatChars {
		count = maxRepeatChars
	}
	text := string(s.lastChar)
	for i := 0; i < count; i++ {
		s.writeString(text, width, false, s.textMode)
	}
}

// clearLastChar invalidates REP. Any control character or escape sequence
// does this: xterm only repeats when the immediately preceding parsed item was
// a graphic character (sp->lastchar is reset to -1 for everything else), and
// tmux behaves the same way.
func (s *spanScreen) clearLastChar() {
	s.lastChar = 0
}

// analyzePrintableRun derives the metadata GraphemeReader reports, for callers
// that have a text run but not the reader's bookkeeping (REP replay, tests, and
// internal helpers). Invalid UTF-8 is measured after sanitization so lastRune
// matches what writeStringMeta stores.
func analyzePrintableRun(text string) printableRunMeta {
	meta := printableRunMeta{validUTF8: utf8.ValidString(text), allSpaces: true}
	for i := 0; i < len(text); i++ {
		if text[i] != ' ' {
			meta.allSpaces = false
			break
		}
	}
	last := text
	if !meta.validUTF8 {
		last = sanitizeUTF8(text)
	}
	meta.lastRune, _ = utf8.DecodeLastRuneInString(last)
	return meta
}

func (s *spanScreen) setCursorPos(x, y int) {
	s.pendingWrap = false
	oldY := s.cursorPos.Y
	s.cursorPos.X = clamp(x, 0, s.size.X-1)
	if s.originMode {
		s.cursorPos.Y = clamp(y, s.topMargin, s.bottomMargin)
	} else {
		s.cursorPos.Y = clamp(y, 0, s.size.Y-1)
	}
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

// defaultTabInterval is the default distance between horizontal tab stops.
const defaultTabInterval = 8

// resizeTabs keeps the existing stops that still fit and adds default stops
// for any new columns.
func (s *spanScreen) resizeTabs(width int) {
	tabs := make([]bool, width)
	copy(tabs, s.tabs)
	for x := len(s.tabs); x < width; x++ {
		tabs[x] = x%defaultTabInterval == 0
	}
	s.tabs = tabs
}

// resetTabStops restores the default stops (every 8 columns).
func (s *spanScreen) resetTabStops() {
	s.tabs = nil
	s.resizeTabs(s.size.X)
}

// isTabStop reports whether a stop is set at column x. Columns outside the
// screen count as default stops so movement never runs off the edge.
func (s *spanScreen) isTabStop(x int) bool {
	if x < 0 || x >= len(s.tabs) {
		return x%defaultTabInterval == 0
	}
	return s.tabs[x]
}

// nextTabStop moves x forward to the count-th tab stop after it. With no stop
// left, the cursor lands on the last column (xterm behavior).
func (s *spanScreen) nextTabStop(x int, count int) int {
	if count < 1 {
		count = 1
	}
	for i := 0; i < count; i++ {
		next := -1
		for t := x + 1; t < s.size.X; t++ {
			if s.isTabStop(t) {
				next = t
				break
			}
		}
		if next < 0 {
			return s.size.X - 1
		}
		x = next
	}
	return x
}

// prevTabStop moves x back to the count-th tab stop before it, stopping at
// column 0 when none remains.
func (s *spanScreen) prevTabStop(x int, count int) int {
	if count < 1 {
		count = 1
	}
	for i := 0; i < count; i++ {
		prev := 0
		for t := x - 1; t >= 0; t-- {
			if s.isTabStop(t) {
				prev = t
				break
			}
		}
		if prev == x {
			return 0
		}
		x = prev
	}
	return x
}

// setTabStop implements HTS at column x.
func (s *spanScreen) setTabStop(x int) {
	if x < 0 || x >= len(s.tabs) {
		return
	}
	s.tabs[x] = true
}

// clearTabStop implements TBC 0: clear the stop at column x only.
func (s *spanScreen) clearTabStop(x int) {
	if x < 0 || x >= len(s.tabs) {
		return
	}
	s.tabs[x] = false
}

// clearAllTabStops implements TBC 3.
func (s *spanScreen) clearAllTabStops() {
	for x := range s.tabs {
		s.tabs[x] = false
	}
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
		lo, hi := 0, s.size.Y-1
		if s.originMode {
			// Origin mode confines the cursor to the scroll region, even
			// for explicit cursor movements.
			lo, hi = s.topMargin, s.bottomMargin
		}
		s.cursorPos.Y = clamp(s.cursorPos.Y, lo, hi)
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
	s.savedCharsets = s.charsets
	s.savedOriginMode = s.originMode
}

func (s *spanScreen) designateCharset(slot int, cs charset) {
	s.charsets.designate(slot, cs)
}

func (s *spanScreen) invokeCharset(slot int) {
	s.charsets.invoke(slot)
}

func (s *spanScreen) resetCharsets() {
	s.charsets.reset()
}

func (s *spanScreen) copyTransientState(from screen) {
	if other, ok := from.(*spanScreen); ok {
		s.cursorPos = other.cursorPos
		s.pendingWrap = other.pendingWrap
		s.style = other.style
		s.charsets = other.charsets
		s.lastChar = other.lastChar
		// IRM and DECOM behave as terminal-wide modes (xterm keeps them in
		// xw->flags, not per screen), so they follow the cursor across a
		// screen switch.
		s.insertMode = other.insertMode
		s.originMode = other.originMode
		if s.originMode {
			// Each screen keeps its own margins, so the incoming cursor has
			// to be clamped into this screen's region.
			s.cursorPos.Y = clamp(s.cursorPos.Y, s.topMargin, s.bottomMargin)
		}
		return
	}
	s.cursorPos = from.CursorPos()
	s.style = from.Style()
}

func (s *spanScreen) restoreCursorPos() {
	s.cursorPos = s.savedCursorPos
	s.pendingWrap = s.savedPendingWrap
	s.style = s.savedStyle
	s.charsets = s.savedCharsets
	s.originMode = s.savedOriginMode
	if s.originMode {
		// xterm clamps a restored cursor into the margins that are current
		// now, which may differ from the ones in effect at DECSC time.
		s.cursorPos.Y = clamp(s.cursorPos.Y, s.topMargin, s.bottomMargin)
	}
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
		SavedCharsets:    s.savedCharsets,
		Charsets:         s.charsets,
		InsertMode:       s.insertMode,
		OriginMode:       s.originMode,
		SavedOriginMode:  s.savedOriginMode,
		LastChar:         s.lastChar,
		Tabs:             append([]bool(nil), s.tabs...),
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
	s.savedCharsets = st.SavedCharsets
	s.charsets = st.Charsets
	s.insertMode = st.InsertMode
	s.originMode = st.OriginMode
	s.savedOriginMode = st.SavedOriginMode
	s.lastChar = st.LastChar
	s.tabs = make([]bool, w)
	copy(s.tabs, st.Tabs)
	for x := len(st.Tabs); x < w; x++ {
		s.tabs[x] = x%defaultTabInterval == 0
	}
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

// locateSpanRange locates the start and end of a replacement window in the
// forward direction. startIdx == len(spans) means x is past the end of the
// line. endIdx defaults to the last span with offset 0 when the end is past
// the line, matching the boundary semantics replaceRange relies on. It is kept
// small enough to inline; replaceRange picks the backward scan when that
// is cheaper.
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

// locateSpanRangeBackward is locateSpanRange scanned from the end of the line.
// startIdx is the rightmost span containing cell x (a boundary selects the
// span after it) and endIdx is the rightmost span containing cell end-1. That
// reproduces the forward scan's picks exactly, including zero-width spans,
// which cover no cell and are skipped by both tests.
func locateSpanRangeBackward(spans []Span, total, x, end int) (startIdx, startOffset, endIdx, endOffset int) {
	startIdx = len(spans)
	endIdx = 0
	foundStart := false
	// An empty window before the line starts: the forward scan picks span 0,
	// offset 0 for the end regardless of that span's width.
	foundEnd := end <= 0
	endCell := end - 1
	endPos := total
	for i := len(spans) - 1; i >= 0; i-- {
		startPos := endPos - spans[i].Width
		if !foundStart && startPos <= x && x < endPos {
			startIdx = i
			startOffset = x - startPos
			foundStart = true
		}
		if !foundEnd && startPos <= endCell && endCell < endPos {
			endIdx = i
			endOffset = end - startPos
			foundEnd = true
		}
		if foundStart && foundEnd {
			if endIdx < startIdx {
				endIdx = startIdx
				endOffset = 0
			}
			return
		}
		endPos = startPos
	}
	if !foundStart {
		// x is at or past the line end: the forward scan never enters its end
		// check and keeps these defaults.
		startIdx = len(spans)
		startOffset = 0
		endIdx = len(spans) - 1
		endOffset = 0
		return
	}
	// The end cell can lie in an earlier span than the start when the window
	// starts on a span boundary; the forward scan only considers spans from
	// startIdx on, so clamp to the same choice.
	if endIdx < startIdx {
		endIdx = startIdx
		endOffset = 0
	}
	return
}

// replaceRange replaces the n cells at x with insert. It reports whether the
// line changed; an exact single-span match with the same text, width, style,
// and link is left untouched so callers can skip the repaint callback.
func replaceRange(line *spanLine, x int, n int, insert Span, mode TextReadMode) bool {
	// Fast return for a no-op insert.
	if n == 0 && insert.Width == 0 {
		return false
	}

	spans := line.spans
	// Handle empty lines by inserting directly.
	if len(spans) == 0 {
		line.spans = append(line.spans[:0], insert)
		line.width = insert.Width
		return insert.Width > 0
	}

	// spanLine.width is kept in sync with the sum of span widths (the screen
	// invariants assert it), so trust it instead of re-summing every call.
	// Never derive it from the boundary locator below: that scan stops at the
	// end of the replacement window, not at the end of the line.
	totalWidth := lineCellWidth(line)

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
	var startIdx, startOffset, endIdx, endOffset int
	if x > 0 && x >= totalWidth/2 {
		startIdx, startOffset, endIdx, endOffset = locateSpanRangeBackward(spans, totalWidth, x, x+n)
	} else {
		startIdx, startOffset, endIdx, endOffset = locateSpanRange(spans, x, x+n)
	}
	// Idempotent repaints (progress bars, watch, status lines, full-frame
	// redraws of unchanged rows) put the same canonical span back where it
	// already is. Leaving it untouched and skipping the callback avoids span
	// churn and a redundant frontend repaint. A fragmented or differently
	// packed window falls through so the line stays canonical.
	if startIdx == endIdx && startIdx < len(spans) && startOffset == 0 &&
		endOffset == spans[startIdx].Width && spans[startIdx] == insert {
		return false
	}
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
		if x > 0 && x >= totalWidth/2 {
			startIdx, startOffset, endIdx, endOffset = locateSpanRangeBackward(spans, totalWidth, x, x+n)
		} else {
			startIdx, startOffset, endIdx, endOffset = locateSpanRange(spans, x, x+n)
		}
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
		if x > 0 && x >= totalWidth/2 {
			startIdx, startOffset, endIdx, endOffset = locateSpanRangeBackward(spans, totalWidth, x, x+n)
		} else {
			startIdx, startOffset, endIdx, endOffset = locateSpanRange(spans, x, x+n)
		}
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
		return true
	}

	// If we never intersected, treat it like an append.
	if startIdx == len(spans) {
		if insertWidth > 0 {
			line.spans = append(line.spans, insertParts...)
			line.width = totalWidth + insertWidth
		}
		return insertWidth > 0
	}

	// Fast paths for in-place replacements inside a single span. Padding adds
	// multiple spans, so those cases fall through to the general path.
	if startIdx == endIdx && len(insertParts) == 1 {
		sp := spans[startIdx]
		ins := insertParts[0]
		if startOffset == 0 && endOffset == sp.Width {
			spans[startIdx] = ins
			line.width = totalWidth - n + insertWidth
			return true
		}
		// Compare the complete style and link. Color-only comparison used to
		// keep the old span's mode bits (bold, italic, underline, ...) when
		// overwriting text in place, and could skip the update entirely.
		if ins.Width == n && sp.Style == ins.Style && sp.Link == ins.Link {
			if sp.Text == "" && ins.Text == "" {
				return false
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
					return true
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
		return true
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
	return true
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
	//
	// The target's first cell is the target cell itself, except when the
	// target is a wide cluster that contains it: the mark then belongs to a
	// cluster starting offset cells earlier.
	left, target, right := isolateCell(line.spans[idx], offset, s.textMode)
	targetStart := cell
	if offset > 0 && target.Width > 1 {
		targetStart = cell - offset
	}
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
				if cell+2 >= s.size.X {
					// The widened cell now fills the last column; defer the
					// wrap exactly like a write that filled the final cell.
					s.pendingWrap = true
				} else {
					s.cursorPos.X++
				}
				s.frontend.CursorMoved(s.view, s.cursorPos.X, s.cursorPos.Y)
			}
			s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: cell, X2: cell + 2}, CRText)
			return
		}
		sp.Text = base + text
		// Width doesn't change for an ordinary merge.
	}

	// Repaint the whole merged span: for a wide cluster the mark changed a
	// cell range that starts before the cursor.
	end := targetStart + line.spans[idx].Width
	s.frontend.RegionChanged(s.view, Region{Y: y, Y2: y + 1, X: targetStart, X2: end}, CRText)
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
