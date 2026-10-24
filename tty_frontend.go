package termemu

import (
	"io"
	"os"
	"strconv"
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

	// spans is scratch space reused across line renders. It is only touched
	// while the terminal lock is held (renderRegionLocked).
	spans []Span
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

	term.View(func(v LockedView) {
		renderSnapshotRegion(&snap, v, r)
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

func (t *TTYFrontend) Bell(v LockedView) {}

func (t *TTYFrontend) RegionChanged(view LockedView, r Region, _ ChangeReason) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.attached || t.term == nil || t.out == nil {
		return
	}

	r = r.Intersect(t.region)

	// RegionChanged is invoked while the terminal is already locked.
	t.renderRegionLocked(view, r)
}

func (t *TTYFrontend) ScrollLines(v LockedView, y int) {}
func (t *TTYFrontend) CursorMoved(v LockedView, x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cursor = Pos{X: x, Y: y}
	t.renderCursorLocked()
}
func (t *TTYFrontend) StyleChanged(v LockedView, s Style) {}
func (t *TTYFrontend) ViewFlagChanged(v LockedView, flag ViewFlag, value bool) {
	if flag != VFShowCursor {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.showCur = value
	t.renderCursorLocked()
}
func (t *TTYFrontend) ViewIntChanged(v LockedView, flag ViewInt, value int)          {}
func (t *TTYFrontend) ViewStringChanged(v LockedView, flag ViewString, value string) {}

func (t *TTYFrontend) renderRegionLocked(view LockedView, r Region) {
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
		spans:    t.spans,
	}
	renderSnapshotRegion(&snap, view, r)
	t.spans = snap.spans
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

	// spans is scratch space reused across line renders (only touched while
	// the terminal lock is held).
	spans []Span
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

// renderSnapshotRegion renders region r to snap.out using the snapshotted
// frontend state and the callback's view. The caller must hold the terminal
// lock and must not hold the TTYFrontend mutex (use a snapshot instead).
func renderSnapshotRegion(snap *ttySnapshot, view LockedView, r Region) {
	if snap.out == nil || !snap.attached {
		return
	}

	w, h := view.Size()
	r = clampRegion(r, w, h)
	if r.Empty() {
		return
	}

	// One buffer for the whole repaint: roughly one byte per cell plus
	// cursor moves and SGR sequences.
	buf := make([]byte, 0, (r.X2-r.X)*(r.Y2-r.Y)+(r.Y2-r.Y)*24+64)
	buf = append(buf, ansiSaveCursor...)
	buf = append(buf, ansiWrapDisable...)
	for y := r.Y; y < r.Y2; y++ {
		buf = appendANSIMoveCursor(buf, r.X, y)
		buf, snap.spans = view.appendANSILine(buf, snap.spans, r.X, r.X2-r.X, y)
	}
	buf = append(buf, ansiReset...)
	buf = append(buf, ansiWrapEnable...)
	buf = append(buf, ansiRestoreCursor...)

	_, _ = snap.out.Write(buf)
	renderSnapshotCursor(*snap)
}

// renderSnapshotCursor writes the cursor update for snap. It takes no locks.
func renderSnapshotCursor(snap ttySnapshot) {
	if snap.out == nil {
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
	buf := appendANSIMoveCursor(make([]byte, 0, 24), snap.cursor.X, snap.cursor.Y)
	buf = append(buf, ansiCursorShow...)
	_, _ = snap.out.Write(buf)
}

func (t *TTYFrontend) renderCursorLocked() {
	if t.term == nil {
		return
	}
	snap := t.snapshotLocked()
	renderSnapshotCursor(snap)
}

// renderStyledLineANSI renders a line's text and SGR styling. OSC 8
// hyperlinks (Span.Link) are intentionally not re-emitted.
func renderStyledLineANSI(line Line) []byte {
	return appendStyledLineANSI(nil, line)
}

// appendStyledLineANSI appends line's cells to dst, emitting one leading reset
// and then SGR deltas between adjacent spans.
func appendStyledLineANSI(dst []byte, line Line) []byte {
	return appendLineSpansANSI(dst, line.Spans)
}

func appendANSIMoveCursor(dst []byte, x, y int) []byte {
	dst = append(dst, ESC, '[')
	dst = strconv.AppendInt(dst, int64(y+1), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(x+1), 10)
	return append(dst, 'H')
}

func ansiMoveCursor(x, y int) string {
	return string(appendANSIMoveCursor(make([]byte, 0, 12), x, y))
}

func clampRegion(r Region, w, h int) Region {
	r.X = clamp(r.X, 0, w)
	r.Y = clamp(r.Y, 0, h)
	r.X2 = clamp(r.X2, 0, w)
	r.Y2 = clamp(r.Y2, 0, h)
	return r
}
