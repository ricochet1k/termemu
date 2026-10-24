package termemu

import (
	"bytes"
	"fmt"
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
// Rendering is damage-batched: RegionChanged unions the changed area into a
// pending frame instead of repainting immediately. The terminal calls
// PrepareBatch/WriteBatch (see BatchFrontend) at its batch boundaries, so a
// burst of escape-dense output costs one render pass and one write instead of
// one per callback. Rows whose rendered bytes match the previous frame are
// skipped, which makes an unchanged full-screen repaint nearly free.
//
// Lock ordering: the terminal lock always ranks above the frontend mutex.
// Callback methods (invoked with the terminal lock held) may take t.mu, but
// public methods must never hold t.mu while taking the terminal lock.
// Attach snapshots state under t.mu, releases it, then renders under the
// terminal lock. writeMu is a leaf lock serializing output writes; it is
// never taken while the terminal lock is held.
type TTYFrontend struct {
	mu      sync.Mutex
	writeMu sync.Mutex

	term     Terminal
	out      io.Writer
	attached bool
	region   Region
	cursor   Pos
	showCur  bool
	focused  bool

	// Pending damage. haveDamage covers damage; forceFull requests a repaint
	// of the whole attached region; cursorDirty means the cursor must be
	// re-emitted on the next frame.
	damage       Region
	haveDamage   bool
	forceFull    bool
	cursorDirty  bool
	scrollDamage bool

	// pending holds rendered bytes waiting for WriteBatch. pending, spare
	// and spans are guarded by mu; spans is scratch space reused across
	// line renders.
	pending []byte
	spare   []byte
	spans   []Span

	// shadow holds the last emitted ANSI bytes for each screen row, indexed
	// by absolute row. Rows with nil/empty shadow are always painted.
	shadow [][]byte
}

// NewTTYFrontend returns a frontend that writes to out (defaults to stdout).
// The terminal is set automatically when the Terminal is created with NewWithMode.
func NewTTYFrontend(out io.Writer) *TTYFrontend {
	if out == nil {
		out = os.Stdout
	}
	return &TTYFrontend{out: out, showCur: true, focused: true}
}

// appendCursorShape appends the DECSCUSR sequence that selects shape, so the
// outer terminal renders the shape the application asked for.
func appendCursorShape(dst []byte, shape int) []byte {
	if shape < 0 || shape > 6 {
		shape = CursorShapeDefault
	}
	return append(dst, fmt.Sprintf("\033[%d q", shape)...)
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
	t.cursorDirty = true
	t.forceFull = true
	t.haveDamage = true
	t.scrollDamage = false
	t.damage = r
	t.shadow = nil // force a full repaint on attach
	term := t.term
	out := t.out
	t.mu.Unlock()

	if term == nil || out == nil {
		return
	}

	term.View(func(v LockedView) {
		t.PrepareBatch(v)
		// Adopt the terminal's current cursor shape on attach.
		t.mu.Lock()
		t.pending = appendCursorShape(t.pending, v.GetViewInt(VICursorShape))
		t.mu.Unlock()
	})
	t.WriteBatch()
}

// Detach stops updating the attached region.
func (t *TTYFrontend) Detach() {
	t.mu.Lock()
	t.attached = false
	t.haveDamage = false
	t.forceFull = false
	t.cursorDirty = false
	t.scrollDamage = false
	t.damage = Region{}
	t.shadow = nil
	// Drop any prepared-but-unwritten frame: it describes a screen we no
	// longer track. Only the restore sequences are queued.
	t.pending = append(t.pending[:0], ansiCursorShow...)
	t.pending = appendCursorShape(t.pending, CursorShapeDefault)
	t.mu.Unlock()
	t.WriteBatch()
}

// Focus enables cursor updates and visibility for this frontend.
func (t *TTYFrontend) Focus() {
	t.mu.Lock()
	t.focused = true
	t.cursorDirty = true
	term := t.term
	out := t.out
	t.mu.Unlock()
	if term == nil || out == nil {
		return
	}
	term.View(func(v LockedView) { t.PrepareBatch(v) })
	t.WriteBatch()
}

// Blur disables cursor updates and restores cursor visibility.
func (t *TTYFrontend) Blur() {
	t.mu.Lock()
	t.focused = false
	term := t.term
	out := t.out
	t.mu.Unlock()
	if out == nil {
		return
	}
	// Render any pending damage first (with the cursor hidden), then restore
	// the outer cursor visibility as before.
	if term != nil {
		term.View(func(v LockedView) { t.PrepareBatch(v) })
	}
	t.mu.Lock()
	t.pending = append(t.pending, ansiCursorShow...)
	t.mu.Unlock()
	t.WriteBatch()
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

func (t *TTYFrontend) RegionChanged(view LockedView, r Region, reason ChangeReason) {
	t.mu.Lock()
	if !t.attached || t.term == nil || t.out == nil {
		t.mu.Unlock()
		return
	}
	r = r.Intersect(t.region)
	if r.Empty() {
		t.mu.Unlock()
		return
	}
	if reason == CRScroll {
		// A scrolled row almost always differs from the row that was there
		// before, so comparing against the row shadow is wasted work. Invalidate
		// the affected rows instead and let the next frame rebuild them.
		t.scrollDamage = true
	}
	if t.haveDamage {
		t.damage = unionRegion(t.damage, r)
	} else {
		t.damage = r
		t.haveDamage = true
	}
	t.mu.Unlock()
}

func (t *TTYFrontend) ScrollLines(v LockedView, y int) {}
func (t *TTYFrontend) CursorMoved(v LockedView, x, y int) {
	t.mu.Lock()
	t.cursor = Pos{X: x, Y: y}
	t.cursorDirty = true
	t.mu.Unlock()
}
func (t *TTYFrontend) StyleChanged(v LockedView, s Style) {}
func (t *TTYFrontend) ViewFlagChanged(v LockedView, flag ViewFlag, value bool) {
	if flag != VFShowCursor {
		return
	}
	t.mu.Lock()
	t.showCur = value
	t.cursorDirty = true
	t.mu.Unlock()
}
func (t *TTYFrontend) ViewIntChanged(v LockedView, flag ViewInt, value int) {
	if flag != VICursorShape {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.out == nil || !t.attached {
		return
	}
	t.pending = appendCursorShape(t.pending, value)
}
func (t *TTYFrontend) ViewStringChanged(v LockedView, flag ViewString, value string) {}

// PrepareBatch renders all damage accumulated since the last frame into the
// pending output buffer. It is called with the terminal lock held and does no
// I/O. Implements BatchFrontend.
func (t *TTYFrontend) PrepareBatch(view LockedView) {
	t.mu.Lock()
	t.prepareLocked(view)
	t.mu.Unlock()
}

// WriteBatch writes the output prepared by PrepareBatch. It is called without
// the terminal lock; concurrent calls are serialized by writeMu and preserve
// frame order. Implements BatchFrontend.
func (t *TTYFrontend) WriteBatch() {
	t.writeMu.Lock()
	t.mu.Lock()
	buf := t.pending
	t.pending = t.spareLocked()
	out := t.out
	t.mu.Unlock()

	if out != nil && len(buf) > 0 {
		_, _ = out.Write(buf)
	}

	t.mu.Lock()
	if t.pending == nil {
		t.pending = buf[:0]
	} else {
		// A newer frame was prepared while the write was in flight; keep it
		// and recycle this buffer for the frame after it.
		t.spare = buf[:0]
	}
	t.mu.Unlock()
	t.writeMu.Unlock()
}

// spareLocked returns the recycled backing array for the next pending
// buffer. Guarded by mu.
func (t *TTYFrontend) spareLocked() []byte {
	b := t.spare
	t.spare = nil
	if b == nil {
		return nil
	}
	return b[:0]
}

// prepareLocked builds the next frame from accumulated damage. The caller
// must hold t.mu and the terminal lock.
func (t *TTYFrontend) prepareLocked(view LockedView) {
	if t.out == nil || !t.attached {
		t.haveDamage = false
		t.forceFull = false
		t.cursorDirty = false
		t.scrollDamage = false
		t.damage = Region{}
		return
	}
	if !t.haveDamage && !t.forceFull && !t.cursorDirty {
		return
	}

	w, h := view.Size()
	r := t.region
	if !t.forceFull {
		if t.haveDamage {
			r = t.damage.Intersect(t.region)
		} else {
			r = Region{}
		}
	}
	r = clampRegion(r, w, h)

	cursorDirty := t.cursorDirty
	skipShadow := t.scrollDamage
	t.haveDamage = false
	t.forceFull = false
	t.cursorDirty = false
	t.scrollDamage = false
	t.damage = Region{}

	if !r.Empty() && len(t.shadow) != h {
		shadow := make([][]byte, h)
		copy(shadow, t.shadow)
		t.shadow = shadow
	}
	if !r.Empty() {
		// Render rows straight into pending. The save/wrap-disable header is
		// rolled back if every row turned out to be unchanged.
		mark := len(t.pending)
		t.pending = append(t.pending, ansiSaveCursor...)
		t.pending = append(t.pending, ansiWrapDisable...)
		changed := false
		for y := r.Y; y < r.Y2; y++ {
			rowStart := len(t.pending)
			t.pending = appendANSIMoveCursor(t.pending, r.X, y)
			t.pending, t.spans = view.appendANSILine(t.pending, t.spans, r.X, r.X2-r.X, y)
			if skipShadow {
				// Invalidate this row's shadow (keeping its backing array) so
				// the next frame repaints it once and rebuilds the shadow.
				if y < len(t.shadow) {
					t.shadow[y] = t.shadow[y][:0]
				}
			} else {
				row := t.pending[rowStart:]
				if y < len(t.shadow) && bytes.Equal(t.shadow[y], row) {
					t.pending = t.pending[:rowStart]
					continue
				}
				if y < len(t.shadow) {
					t.shadow[y] = append(t.shadow[y][:0], row...)
				}
			}
			changed = true
		}
		if changed {
			t.pending = append(t.pending, ansiReset...)
			t.pending = append(t.pending, ansiWrapEnable...)
			t.pending = append(t.pending, ansiRestoreCursor...)
		} else {
			t.pending = t.pending[:mark]
		}
	}

	if cursorDirty {
		t.pending = t.appendCursorLocked(t.pending)
	}
}

// appendCursorLocked appends the cursor update for the current frontend
// state. The caller must hold t.mu.
func (t *TTYFrontend) appendCursorLocked(dst []byte) []byte {
	if !t.attached || !t.showCur || !t.focused {
		return append(dst, ansiCursorHide...)
	}
	if t.cursor.X < t.region.X || t.cursor.X >= t.region.X2 || t.cursor.Y < t.region.Y || t.cursor.Y >= t.region.Y2 {
		return append(dst, ansiCursorHide...)
	}
	dst = appendANSIMoveCursor(dst, t.cursor.X, t.cursor.Y)
	return append(dst, ansiCursorShow...)
}

func unionRegion(a, b Region) Region {
	return Region{
		X:  min(a.X, b.X),
		Y:  min(a.Y, b.Y),
		X2: max(a.X2, b.X2),
		Y2: max(a.Y2, b.Y2),
	}
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
