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
//
// Lock ordering: the terminal lock always ranks above the frontend mutex.
// Callback methods (invoked with the terminal lock held) may take t.mu, but
// public methods must never hold t.mu while taking the terminal lock.
// Attach snapshots state under t.mu, releases it, then renders under the
// terminal lock.
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
// The terminal is set automatically when the Terminal is created with NewWithMode.
func NewTTYFrontend(out io.Writer) *TTYFrontend {
	if out == nil {
		out = os.Stdout
	}
	return &TTYFrontend{out: out, showCur: true, focused: true}
}

// SetTerminal updates the terminal used for rendering.
func (t *TTYFrontend) SetTerminal(term Terminal) {
	t.mu.Lock()
	t.term = term
	t.mu.Unlock()
}

// Attach starts updating the provided region.
//
// Locking: this takes t.mu only to update fields and snapshot state, then
// releases it before taking the terminal lock to render. It must never hold
// t.mu while taking the terminal lock: RegionChanged runs with the terminal
// lock held and takes t.mu, so the opposite order would deadlock.
func (t *TTYFrontend) Attach(r Region) {
	t.mu.Lock()
	t.region = r
	t.attached = true
	t.showCur = true
	snap := t.snapshotLocked()
	term := t.term
	t.mu.Unlock()

	if term == nil || snap.out == nil {
		return
	}

	term.WithLock(func() {
		renderSnapshotRegion(snap, term, r)
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

	snap := ttySnapshot{
		out:      t.out,
		attached: t.attached,
		region:   t.region,
		cursor:   t.cursor,
		showCur:  t.showCur,
		focused:  t.focused,
	}
	renderSnapshotRegion(snap, t.term, r)
	t.renderCursorLocked()
}

// ttySnapshot is a copy of the TTYFrontend state needed for rendering,
// taken under t.mu so rendering can proceed without holding t.mu.
type ttySnapshot struct {
	out      io.Writer
	attached bool
	region   Region
	cursor   Pos
	showCur  bool
	focused  bool
}

// snapshotLocked copies the render state. Caller must hold t.mu.
func (t *TTYFrontend) snapshotLocked() ttySnapshot {
	return ttySnapshot{
		out:      t.out,
		attached: t.attached,
		region:   t.region,
		cursor:   t.cursor,
		showCur:  t.showCur,
		focused:  t.focused,
	}
}

// renderSnapshotRegion renders region r of term to snap.out using the
// snapshotted frontend state. The caller must hold the terminal lock and
// must not hold the TTYFrontend mutex (use a snapshot instead).
func renderSnapshotRegion(snap ttySnapshot, term Terminal, r Region) {
	if snap.out == nil || !snap.attached || term == nil {
		return
	}

	w, h := term.Size()
	r = clampRegion(r, w, h)
	if r.Empty() {
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

	_, _ = snap.out.Write(buf.Bytes())
	renderSnapshotCursor(snap, term)
}

// renderSnapshotCursor writes the cursor update for snap. It takes no locks.
func renderSnapshotCursor(snap ttySnapshot, term Terminal) {
	if term == nil || snap.out == nil {
		return
	}
	if !snap.attached || !snap.showCur || !snap.focused {
		_, _ = snap.out.Write([]byte(ansiCursorHide))
		return
	}
	if snap.cursor.X < snap.region.X || snap.cursor.X >= snap.region.X2 || snap.cursor.Y < snap.region.Y || snap.cursor.Y >= snap.region.Y2 {
		_, _ = snap.out.Write([]byte(ansiCursorHide))
		return
	}
	_, _ = snap.out.Write([]byte(ansiMoveCursor(snap.cursor.X, snap.cursor.Y) + ansiCursorShow))
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
