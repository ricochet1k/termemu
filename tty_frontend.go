package termemu

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	ansiSaveCursor    = "\x1b[s"
	ansiRestoreCursor = "\x1b[u"
	ansiReset         = "\x1b[0m"
	ansiCursorShow    = "\x1b[?25h"
	ansiCursorHide    = "\x1b[?25l"
	ansiWrapDisable   = "\x1b[?7l"
	ansiWrapEnable    = "\x1b[?7h"
)

// TTYFrontend renders changed regions to a terminal (tty) output.
// It can be attached to a region of the screen and detached to stop updates.
type TTYFrontend struct {
	mu       sync.Mutex
	term     Terminal
	out      io.Writer
	attached bool
	region   Region
	cursor   Pos
	showCur  bool
	focused  bool
}

// NewTTYFrontend returns a frontend that writes to out (defaults to stdout).
func NewTTYFrontend(term Terminal, out io.Writer) *TTYFrontend {
	if out == nil {
		out = os.Stdout
	}
	return &TTYFrontend{term: term, out: out, showCur: true, focused: true}
}

// SetTerminal updates the terminal used for rendering.
func (t *TTYFrontend) SetTerminal(term Terminal) {
	t.mu.Lock()
	t.term = term
	t.mu.Unlock()
}

// Attach starts updating the provided region.
func (t *TTYFrontend) Attach(r Region) {
	term, out := t.setRegion(r, true)
	if term == nil || out == nil {
		return
	}
	t.renderRegion(term, out, r)
	t.renderCursor(term, out)
}

// Detach stops updating the attached region.
func (t *TTYFrontend) Detach() {
	t.mu.Lock()
	out := t.out
	t.attached = false
	t.mu.Unlock()
	if out != nil {
		_, _ = out.Write([]byte(ansiCursorShow))
	}
}

// Focus enables cursor updates and visibility for this frontend.
func (t *TTYFrontend) Focus() {
	term, out := t.setFocus(true)
	if term == nil || out == nil {
		return
	}
	t.renderCursor(term, out)
}

// Blur disables cursor updates and restores cursor visibility.
func (t *TTYFrontend) Blur() {
	_, out := t.setFocus(false)
	if out != nil {
		_, _ = out.Write([]byte(ansiCursorShow))
	}
}

// SetFocus sets focus state. When unfocused, cursor updates are suppressed.
func (t *TTYFrontend) SetFocus(focused bool) {
	if focused {
		t.Focus()
	} else {
		t.Blur()
	}
}

func (t *TTYFrontend) Bell() {}

func (t *TTYFrontend) RegionChanged(r Region, _ ChangeReason) {
	term, out, attached, region := t.snapshot()
	if !attached || term == nil || out == nil {
		return
	}

	r = intersectRegion(r, region)
	if regionEmpty(r) {
		return
	}

	// RegionChanged is invoked while the terminal is already locked.
	t.renderRegionLocked(term, out, r)
}

func (t *TTYFrontend) ScrollLines(y int)          {}
func (t *TTYFrontend) CursorMoved(x, y int) {
	term, out, attached, region, show, focused := t.updateCursor(x, y)
	if !attached || !show || !focused || term == nil || out == nil {
		return
	}
	if x < region.X || x >= region.X2 || y < region.Y || y >= region.Y2 {
		_, _ = out.Write([]byte(ansiCursorHide))
		return
	}
	_, _ = out.Write([]byte(ansiMoveCursor(x, y) + ansiCursorShow))
}
func (t *TTYFrontend) ColorsChanged(f, b Color)   {}
func (t *TTYFrontend) ViewFlagChanged(v ViewFlag, value bool) {
	if v != VFShowCursor {
		return
	}
	term, out, attached, region, cursor, focused := t.updateShowCursor(value)
	if !attached || !focused || term == nil || out == nil {
		return
	}
	if !value {
		_, _ = out.Write([]byte(ansiCursorHide))
		return
	}
	if cursor.X < region.X || cursor.X >= region.X2 || cursor.Y < region.Y || cursor.Y >= region.Y2 {
		_, _ = out.Write([]byte(ansiCursorHide))
		return
	}
	_, _ = out.Write([]byte(ansiMoveCursor(cursor.X, cursor.Y) + ansiCursorShow))
}
func (t *TTYFrontend) ViewIntChanged(v ViewInt, value int)    {}
func (t *TTYFrontend) ViewStringChanged(v ViewString, value string) {}

func (t *TTYFrontend) snapshot() (Terminal, io.Writer, bool, Region) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.term, t.out, t.attached, t.region
}

func (t *TTYFrontend) updateCursor(x, y int) (Terminal, io.Writer, bool, Region, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cursor = Pos{X: x, Y: y}
	return t.term, t.out, t.attached, t.region, t.showCur, t.focused
}

func (t *TTYFrontend) updateShowCursor(show bool) (Terminal, io.Writer, bool, Region, Pos, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.showCur = show
	return t.term, t.out, t.attached, t.region, t.cursor, t.focused
}

func (t *TTYFrontend) setRegion(r Region, attached bool) (Terminal, io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.region = r
	t.attached = attached
	if attached && !t.showCur {
		t.showCur = true
	}
	return t.term, t.out
}

func (t *TTYFrontend) setFocus(focused bool) (Terminal, io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused = focused
	return t.term, t.out
}

func (t *TTYFrontend) renderRegion(term Terminal, out io.Writer, r Region) {
	term.WithLock(func() {
		t.renderRegionLocked(term, out, r)
	})
}

func (t *TTYFrontend) renderRegionLocked(term Terminal, out io.Writer, r Region) {
	w, h := term.Size()
	r = clampRegion(r, w, h)
	if regionEmpty(r) {
		return
	}

	var buf bytes.Buffer
	buf.WriteString(ansiSaveCursor)
	buf.WriteString(ansiWrapDisable)
	for y := r.Y; y < r.Y2; y++ {
		buf.WriteString(ansiMoveCursor(r.X, y))
		line := term.StyledLine(r.X, r.X2-r.X, y)
		buf.Write(renderStyledLineANSI(line))
	}
	buf.WriteString(ansiReset)
	buf.WriteString(ansiWrapEnable)
	buf.WriteString(ansiRestoreCursor)

	_, _ = out.Write(buf.Bytes())
	t.renderCursor(term, out)
}

func (t *TTYFrontend) renderCursor(term Terminal, out io.Writer) {
	t.mu.Lock()
	cursor := t.cursor
	region := t.region
	attached := t.attached
	show := t.showCur
	focused := t.focused
	t.mu.Unlock()

	if !attached || !show || !focused {
		_, _ = out.Write([]byte(ansiCursorHide))
		return
	}
	if cursor.X < region.X || cursor.X >= region.X2 || cursor.Y < region.Y || cursor.Y >= region.Y2 {
		_, _ = out.Write([]byte(ansiCursorHide))
		return
	}
	_, _ = out.Write([]byte(ansiMoveCursor(cursor.X, cursor.Y) + ansiCursorShow))
}

func renderStyledLineANSI(line *Line) []byte {
	if line == nil || len(line.Spans) == 0 {
		return nil
	}

	var buf bytes.Buffer
	pos := 0
	for _, span := range line.Spans {
		buf.Write(ANSIEscape(span.FG, span.BG))
		end := pos + int(span.Width)
		if end > len(line.Text) {
			end = len(line.Text)
		}
		for _, r := range line.Text[pos:end] {
			buf.WriteRune(r)
		}
		pos = end
	}
	return buf.Bytes()
}

func ansiMoveCursor(x, y int) string {
	return fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)
}

func clampRegion(r Region, w, h int) Region {
	r.X = clamp(r.X, 0, w)
	r.Y = clamp(r.Y, 0, h)
	r.X2 = clamp(r.X2, 0, w)
	r.Y2 = clamp(r.Y2, 0, h)
	return r
}

func intersectRegion(a, b Region) Region {
	return Region{
		X:  max(a.X, b.X),
		Y:  max(a.Y, b.Y),
		X2: min(a.X2, b.X2),
		Y2: min(a.Y2, b.Y2),
	}
}

func regionEmpty(r Region) bool {
	return r.X >= r.X2 || r.Y >= r.Y2
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
