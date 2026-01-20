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
	t.mu.Lock()
	defer t.mu.Unlock()

	t.region = r
	t.attached = true
	if !t.showCur {
		t.showCur = true
	}

	if t.term == nil {
		return
	}

	t.term.WithLock(func() {
		t.renderRegionLocked(t.region)
	})
}

// Detach stops updating the attached region.
func (t *TTYFrontend) Detach() {
	t.mu.Lock()
	t.attached = false
	out := t.out
	t.mu.Unlock()
	if out != nil {
		_, _ = out.Write([]byte(ansiCursorShow))
	}
}

// Focus enables cursor updates and visibility for this frontend.
func (t *TTYFrontend) Focus() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused = true
	t.renderCursorLocked()
}

// Blur disables cursor updates and restores cursor visibility.
func (t *TTYFrontend) Blur() {
	t.mu.Lock()
	t.focused = false
	out := t.out
	t.mu.Unlock()
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
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.attached || t.term == nil || t.out == nil {
		return
	}

	r = r.Intersect(t.region)

	// RegionChanged is invoked while the terminal is already locked.
	t.renderRegionLocked(r)
}

func (t *TTYFrontend) ScrollLines(y int) {}
func (t *TTYFrontend) CursorMoved(x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cursor = Pos{X: x, Y: y}
	t.renderCursorLocked()
}
func (t *TTYFrontend) StyleChanged(s Style) {}
func (t *TTYFrontend) ViewFlagChanged(v ViewFlag, value bool) {
	if v != VFShowCursor {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.showCur = value
	t.renderCursorLocked()
}
func (t *TTYFrontend) ViewIntChanged(v ViewInt, value int)          {}
func (t *TTYFrontend) ViewStringChanged(v ViewString, value string) {}

func (t *TTYFrontend) renderRegionLocked(r Region) {
	if t.out == nil || !t.attached {
		return
	}

	w, h := t.term.Size()
	r = clampRegion(r, w, h)
	if r.Empty() {
		return
	}

	var buf bytes.Buffer
	buf.WriteString(ansiSaveCursor)
	buf.WriteString(ansiWrapDisable)
	for y := r.Y; y < r.Y2; y++ {
		buf.WriteString(ansiMoveCursor(r.X, y))
		line := t.term.StyledLine(r.X, r.X2-r.X, y)
		buf.Write(renderStyledLineANSI(line))
	}
	buf.WriteString(ansiReset)
	buf.WriteString(ansiWrapEnable)
	buf.WriteString(ansiRestoreCursor)

	_, _ = t.out.Write(buf.Bytes())
	t.renderCursorLocked()
}

func (t *TTYFrontend) renderCursorLocked() {
	if t.term == nil || t.out == nil {
		return
	}
	if !t.attached || !t.showCur || !t.focused {
		_, _ = t.out.Write([]byte(ansiCursorHide))
		return
	}
	if t.cursor.X < t.region.X || t.cursor.X >= t.region.X2 || t.cursor.Y < t.region.Y || t.cursor.Y >= t.region.Y2 {
		_, _ = t.out.Write([]byte(ansiCursorHide))
		return
	}
	_, _ = t.out.Write([]byte(ansiMoveCursor(t.cursor.X, t.cursor.Y) + ansiCursorShow))
}

func renderStyledLineANSI(line Line) []byte {
	if len(line.Spans) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, span := range line.Spans {
		buf.Write(span.Style.ANSIEscape())
		if span.Text != "" {
			buf.WriteString(span.Text)
		} else {
			for i := 0; i < span.Width; i++ {
				buf.WriteRune(span.Rune)
			}
		}
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
