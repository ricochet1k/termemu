package termemu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type Terminal interface {
	SetFrontend(f Frontend)

	// View runs fn with a read-only view of terminal state while holding
	// the terminal lock. Use it for atomic multi-reads. The view is only
	// valid for the duration of fn; do not retain it. Do not call the
	// locking read methods (Size, Line, Scrollback, ...) from fn.
	View(func(LockedView))

	Write(b []byte) (int, error)
	SendKey(KeyEvent) (int, error)
	SendMouseRaw(btn MouseBtn, press bool, mods MouseFlag, x, y int) error
	Resize(int, int) error

	// Size returns the terminal width and height. It takes the terminal
	// lock itself, so it is safe from any goroutine but must not be called
	// from inside View or a Frontend callback (use the view there).
	Size() (int, int)
	// CursorPos returns the cursor position. It locks internally; use the
	// view from callbacks.
	CursorPos() Pos
	// Line returns the plain text content of line y. It locks internally;
	// use the view from callbacks.
	Line(int) string
	// ANSILine returns the ANSI-escaped content of line y. It locks
	// internally; use the view from callbacks.
	ANSILine(y int) string
	// StyledLine returns the styled content of a line region. It locks
	// internally; use the view from callbacks.
	StyledLine(x, w, y int) Line
	// StyledLines returns the styled content of multiple lines in a region.
	// It locks internally; use the view from callbacks.
	StyledLines(r Region) []Line

	// GetViewFlag, GetViewInt and GetViewString return terminal view
	// settings. They lock internally; use the view from callbacks.
	GetViewFlag(ViewFlag) bool
	GetViewInt(ViewInt) int
	GetViewString(ViewString) string

	// PaletteColor returns the RGB color mapped to a palette index (OSC 4),
	// and the default color accessors report the OSC 10/11/12 colors. They
	// lock internally; use the LockedView counterparts in callbacks.
	PaletteColor(idx int) Color
	DefaultForeground() Color
	DefaultBackground() Color
	CursorColor() Color

	// Scrollback returns the terminal's scrollback buffer for ordinary
	// code. Its methods take the terminal lock themselves, so they must
	// not be called from inside View or a Frontend callback. Use
	// LockedView.Scrollback there instead.
	Scrollback() *Scrollback

	// Snapshot returns a deep copy of the terminal's screen state. It
	// locks internally, so it must not be called from inside View or a
	// Frontend callback (use LockedView.Snapshot there). The scrollback
	// buffer and backend/process state are not included. Mutating the
	// returned Snapshot does not affect the terminal, and vice versa.
	Snapshot() Snapshot
	// Restore replaces the terminal's screen state with a previously
	// captured Snapshot and emits a full repaint. It locks internally. A
	// snapshot of a different size is resized to the snapshot dimensions
	// and the backend is resized too. The scrollback buffer is untouched.
	Restore(Snapshot) error

	// Close shuts down the terminal: it closes the backend (if it
	// implements io.Closer) so a blocked Read returns and the read loop
	// exits. It is idempotent and never blocks waiting for the loop.
	Close() error

	// Pause stops the read loop, captures the user-space reader/parser
	// state plus a snapshot and scrollback snapshot, and leaves the
	// terminal paused. Resume continues a same-process paused loop.
	// For a cross-process handoff, Pause then transfer the backend fds
	// (see PTYBackend.Master/Slave) and close this terminal.
	Pause(context.Context) (Handoff, error)
	Resume() error

	// Done is closed when the read loop exits.
	Done() <-chan struct{}
	// Err reports how the read loop terminated: nil for a clean EOF,
	// otherwise the error that stopped the loop. It is only meaningful
	// after Done is closed.
	Err() error
}

// ErrSnapshotSizeMismatch is returned by Terminal.Restore when the snapshot
// dimensions do not match the current terminal size. Restore never resizes
// the pty or screens implicitly.
var ErrSnapshotSizeMismatch = errors.New("termemu: snapshot dimensions do not match terminal size")

// ScreenState is a deep copy of one screen buffer.
type ScreenState struct {
	Width, Height    int
	Lines            []Line // full-width rows; Spans are copies
	Wrapped          []bool // auto-wrap continuation flags, len == Height
	Cursor           Pos
	PendingWrap      bool // next printable write wraps before writing
	SavedCursor      Pos
	SavedPendingWrap bool // pending-wrap state saved by DECSC
	SavedStyle       Style
	// SavedCharsets is the G0-G3/GL state saved by DECSC; Charsets is the
	// current character set state.
	SavedCharsets charsetState
	Charsets      charsetState
	// InsertMode is IRM; OriginMode/SavedOriginMode are DECOM and its DECSC
	// slot. LastChar is the last graphic code point replayed by REP (0 means
	// none). Tabs is the horizontal tab stop map (nil means the default stops
	// every 8 columns).
	InsertMode      bool
	OriginMode      bool
	SavedOriginMode bool
	LastChar        rune
	Tabs            []bool
	Style           Style
	TopMargin       int
	BottomMargin    int
	AutoWrap        bool
}

// Snapshot is a deep copy of terminal state. It excludes the scrollback
// buffer and backend/process state.
type Snapshot struct {
	Main, Alt    ScreenState
	OnAltScreen  bool
	ViewFlags    []bool
	ViewInts     []int
	ViewStrings  []string
	KeyboardMain keyboardMode
	KeyboardAlt  keyboardMode
	Link         string // active OSC 8 hyperlink, if any
	NewlineMode  bool   // LNM (CSI 20 h/l)
	// Palette and default colors are resolved by frontends rather than stored
	// in cells, so they travel with the snapshot.
	Palette       [paletteSize]Color
	DefaultFG     Color
	DefaultBG     Color
	DefaultCursor Color
}

// LockedView is a read-only handle to terminal state. It is valid only for
// the duration of the call that received it (a Frontend callback or
// Terminal.View); do not retain it. Its methods do not lock, which makes
// Snapshot and Scrollback (and LockedScrollback.Snapshot) the callback-safe
// snapshot entry points. Terminal.Snapshot and *Scrollback.Snapshot are for
// ordinary code and would deadlock here.
type LockedView struct {
	t *terminal
}

// Size returns the terminal width and height.
func (v LockedView) Size() (w, h int) {
	if v.t == nil {
		return 0, 0
	}
	size := v.t.screen().Size()
	return size.X, size.Y
}

// CursorPos returns the cursor position.
func (v LockedView) CursorPos() Pos {
	if v.t == nil {
		return Pos{}
	}
	return v.t.screen().CursorPos()
}

// Line returns the plain text content of line y.
func (v LockedView) Line(y int) string {
	if v.t == nil || y < 0 {
		return ""
	}
	size := v.t.screen().Size()
	if y >= size.Y {
		return ""
	}
	return v.t.screen().StyledLine(0, size.X, y).PlainTextString()
}

// ANSILine returns the ANSI-escaped content of line y.
func (v LockedView) ANSILine(y int) string {
	if v.t == nil || y < 0 || y >= v.t.screen().Size().Y {
		return ""
	}
	return v.t.screen().renderLineANSI(y)
}

// appendANSILine appends the ANSI rendering of region (x,w,y) to dst,
// reusing spanScratch for the clipped spans. It is for in-package renderers
// running under the terminal lock, which must pass the returned scratch back
// on the next call.
func (v LockedView) appendANSILine(dst []byte, spanScratch []Span, x, w, y int) ([]byte, []Span) {
	if v.t == nil || y < 0 || y >= v.t.screen().Size().Y {
		return dst, spanScratch
	}
	spans, _ := v.t.screen().clippedStyledSpans(spanScratch[:0], x, w, y)
	return appendLineSpansANSI(dst, spans), spans
}

// StyledLine returns the styled content of a line region.
func (v LockedView) StyledLine(x, w, y int) Line {
	if v.t == nil {
		return Line{}
	}
	return v.t.screen().StyledLine(x, w, y)
}

// StyledLines returns the styled content of multiple lines in a region.
func (v LockedView) StyledLines(r Region) []Line {
	if v.t == nil {
		return nil
	}
	return v.t.screen().StyledLines(r)
}

// GetViewFlag returns the value of a boolean terminal flag.
func (v LockedView) GetViewFlag(f ViewFlag) bool {
	if v.t == nil {
		return false
	}
	return v.t.viewFlags[f]
}

// GetViewInt returns the value of an integer terminal setting.
func (v LockedView) GetViewInt(i ViewInt) int {
	if v.t == nil {
		return 0
	}
	return v.t.viewInts[i]
}

// GetViewString returns the value of a string terminal setting.
func (v LockedView) GetViewString(s ViewString) string {
	if v.t == nil {
		return ""
	}
	return v.t.viewStrings[s]
}

// Scrollback returns a no-lock scrollback reader for use inside callbacks.
func (v LockedView) Scrollback() LockedScrollback {
	return LockedScrollback{t: v.t}
}

// Snapshot returns a deep copy of the terminal's screen state without taking
// the terminal lock. It is the callback-safe counterpart of
// Terminal.Snapshot. Like Terminal.Snapshot, it excludes the scrollback
// buffer and backend/process state. A zero LockedView returns an empty
// Snapshot.
func (v LockedView) Snapshot() Snapshot {
	if v.t == nil {
		return Snapshot{}
	}
	return v.t.snapshotLocked()
}

type terminal struct {
	sync.Mutex

	// lifecycleMu guards loop/pause/close state so Close and Err never wait
	// on the terminal lock, which may be held by a frontend callback.
	lifecycleMu sync.Mutex

	// sizeMu serializes backend SetSize calls so a slow resize cannot land
	// after a newer restore/resize and leave the pty at a stale size.
	sizeMu  sync.Mutex
	sizeGen uint64

	frontend    Frontend
	onAltScreen bool
	mainScreen  screen
	altScreen   screen

	backend Backend

	viewFlags   []bool
	viewInts    []int
	viewStrings []string

	// newlineMode is LNM (CSI 20 h/l): line feed also performs a carriage
	// return. Like xterm's LINEFEED flag it is terminal-wide, not per screen.
	newlineMode bool

	// palette maps the 256 palette indexes to RGB values (OSC 4/104) and
	// defaults holds the default foreground/background/cursor colors (OSC
	// 10/11/12). Cells keep palette indexes; frontends resolve them through
	// the LockedView accessors.
	palette  [paletteSize]Color
	defaults dynamicColors

	readLoopStarted bool
	readLoopDone    chan struct{}
	readLoopErr     error
	textReadMode    TextReadMode

	// pausedFlag mirrors paused for the read loop's per-iteration check, so
	// the common not-paused case does not take lifecycleMu.
	pausedFlag atomic.Bool

	// Pause/Resume handshake. All fields are guarded by lifecycleMu.
	// paused is the request flag checked by the read loop; pauseReady
	// carries the loop's ReaderState to Pause, and pauseResume carries
	// true (resume) or false (abort/exit) back to the loop.
	paused      bool
	pauseReady  chan ReaderState
	pauseResume chan bool

	closeOnce sync.Once
	closeErr  error
	closed    bool

	keyboardMain keyboardMode
	keyboardAlt  keyboardMode

	// hyperlink is the current OSC 8 hyperlink URI. Empty means no link.
	// It is pushed to both screens via setLink so content written while a
	// link is active carries it in Span.Link.
	hyperlink string

	scrollRows    []scrollRow
	scrollArena   []scrollArenaBlock
	scrollNextID  ScrollID
	scrollBaseIdx int
	scrollMaxRows int

	// scrollback is the self-locking reader returned by Scrollback.
	// Its t field points back at this terminal.
	scrollback Scrollback
}

// New makes a new terminal using the provided Frontend, Backend, and default text read mode.
// The default mode is TextReadModeGrapheme, which is the correct terminal
// behavior for emoji, combining marks and other grapheme clusters. Use
// NewWithMode to select TextReadModeRune explicitly.
func New(f Frontend, backend Backend) Terminal {
	return NewWithMode(f, backend, TextReadModeGrapheme)
}

// NewWithMode makes a new terminal using the provided Frontend, Backend, and text read mode.
// If the frontend implements SetTerminal (e.g. *TTYFrontend), it is wired automatically.
// Optional TerminalOptions configure scrollback size and other settings.
func NewWithMode(f Frontend, backend Backend, mode TextReadMode, opts ...TerminalOption) Terminal {
	if backend == nil {
		return nil
	}
	t := newTerminal(f, backend, mode)
	for _, opt := range opts {
		opt(t)
	}
	if setter, ok := f.(interface{ SetTerminal(Terminal) }); ok {
		setter.SetTerminal(t)
	}
	t.startReadLoop()
	return t
}

// newTerminal creates a terminal without starting the read loop.
// Used internally and by tests that feed data synchronously.
func newTerminal(f Frontend, backend Backend, mode TextReadMode) *terminal {
	if f == nil {
		f = &EmptyFrontend{}
	}

	t := &terminal{
		frontend:      f,
		mainScreen:    newScreen(f),
		altScreen:     newScreen(f),
		backend:       backend,
		viewFlags:     make([]bool, viewFlagCount),
		viewInts:      make([]int, viewIntCount),
		viewStrings:   make([]string, viewStringCount),
		textReadMode:  mode,
		palette:       defaultPalette,
		defaults:      defaultDynamicColors,
		scrollRows:    make([]scrollRow, 0, DefaultScrollbackRows),
		scrollNextID:  1,
		scrollMaxRows: DefaultScrollbackRows,
		readLoopDone:  make(chan struct{}),
	}

	// Alternate-screen switching is allowed unless an application clears
	// DEC private mode 1046.
	t.viewFlags[VFAltScreenAllowed] = true

	// The cursor is visible by default (DECSET 25); applications hide it
	// explicitly.
	t.viewFlags[VFShowCursor] = true

	// Wire scroll capture to feed incoming rows into the scrollback buffer.
	t.mainScreen.setScrollCapture(t.scrollCapture)
	t.altScreen.setScrollCapture(t.scrollCapture)
	// Only the main screen reports scroll deltas to the frontend.
	t.mainScreen.setScrollDelta(t.scrollLinesDelta)

	// Give screens a view so their frontend callbacks can read terminal
	// state without taking the lock again.
	view := LockedView{t: t}
	t.mainScreen.setView(view)
	t.altScreen.setView(view)
	t.scrollback.t = t

	return t
}

func (t *terminal) SetFrontend(f Frontend) {
	t.Lock()
	defer t.Unlock()
	t.frontend = f
	t.mainScreen.SetFrontend(f)
	t.altScreen.SetFrontend(f)
}

// TerminalOption configures a Terminal at construction time.
type TerminalOption func(*terminal)

// WithScrollback sets the scrollback row capacity. Rows <= 0 uses DefaultScrollbackRows.
func WithScrollback(rows int) TerminalOption {
	return func(t *terminal) {
		if rows <= 0 {
			rows = DefaultScrollbackRows
		}
		t.scrollRows = make([]scrollRow, 0, rows)
		t.scrollMaxRows = rows
	}
}

const termStr = "TERM=xterm-256color"

// Write is for the client to write keyboard/mouse input or terminal feedback
func (t *terminal) Write(b []byte) (int, error) {
	t.Lock()
	backend := t.backend
	t.Unlock()

	if backend == nil {
		return 0, errors.New("backend is nil")
	}
	total := 0
	for len(b) > 0 {
		n, err := backend.Write(b)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		b = b[n:]
	}
	return total, nil
}

// View runs fn with a read-only view of terminal state while holding the
// terminal lock.
func (t *terminal) View(fn func(LockedView)) {
	t.Lock()
	defer t.Unlock()
	fn(LockedView{t: t})
}

// Snapshot returns a deep copy of the terminal's screen state. It locks
// internally. The scrollback buffer and backend/process state are excluded.
func (t *terminal) Snapshot() Snapshot {
	t.Lock()
	defer t.Unlock()
	return t.snapshotLocked()
}

// snapshotLocked implements Snapshot with t.Lock held.
func (t *terminal) snapshotLocked() Snapshot {
	return Snapshot{
		Main:          t.mainScreen.snapshotState(),
		Alt:           t.altScreen.snapshotState(),
		OnAltScreen:   t.onAltScreen,
		ViewFlags:     append([]bool(nil), t.viewFlags...),
		ViewInts:      append([]int(nil), t.viewInts...),
		ViewStrings:   append([]string(nil), t.viewStrings...),
		KeyboardMain:  cloneKeyboardMode(t.keyboardMain),
		KeyboardAlt:   cloneKeyboardMode(t.keyboardAlt),
		Link:          t.hyperlink,
		NewlineMode:   t.newlineMode,
		Palette:       t.palette,
		DefaultFG:     t.defaults.fg,
		DefaultBG:     t.defaults.bg,
		DefaultCursor: t.defaults.cursor,
	}
}

// cloneKeyboardMode copies a keyboard mode, including its stack slice.
func cloneKeyboardMode(km keyboardMode) keyboardMode {
	out := keyboardMode{flags: km.flags}
	if len(km.stack) > 0 {
		out.stack = append([]int(nil), km.stack...)
	}
	return out
}

// Restore replaces the terminal's screen state with a previously captured
// Snapshot and emits a full repaint. It locks internally. A snapshot captured
// at a different size is resized to its own dimensions (lines are truncated or
// padded); the backend is resized as well when a size change is needed. The
// scrollback buffer is untouched. ErrSnapshotSizeMismatch is returned for
// invalid (zero/negative) or internally inconsistent screen dimensions.
func (t *terminal) Restore(s Snapshot) error {
	if s.Main.Width <= 0 || s.Main.Height <= 0 || s.Alt.Width <= 0 || s.Alt.Height <= 0 ||
		s.Main.Width != s.Alt.Width || s.Main.Height != s.Alt.Height {
		return ErrSnapshotSizeMismatch
	}

	t.Lock()
	oldSize := t.mainScreen.Size()
	t.mainScreen.restoreState(s.Main)
	t.altScreen.restoreState(s.Alt)
	t.onAltScreen = s.OnAltScreen
	t.keyboardMain = cloneKeyboardMode(s.KeyboardMain)
	t.keyboardAlt = cloneKeyboardMode(s.KeyboardAlt)

	for i := range t.viewFlags {
		if i < len(s.ViewFlags) && t.viewFlags[i] != s.ViewFlags[i] {
			t.setViewFlag(ViewFlag(i), s.ViewFlags[i])
		}
	}
	for i := range t.viewInts {
		if i < len(s.ViewInts) && t.viewInts[i] != s.ViewInts[i] {
			t.setViewInt(ViewInt(i), s.ViewInts[i])
		}
	}
	for i := range t.viewStrings {
		if i < len(s.ViewStrings) && t.viewStrings[i] != s.ViewStrings[i] {
			t.setViewString(ViewString(i), s.ViewStrings[i])
		}
	}

	t.setHyperlink(s.Link)
	t.newlineMode = s.NewlineMode
	t.palette = s.Palette
	t.defaults = dynamicColors{fg: s.DefaultFG, bg: s.DefaultBG, cursor: s.DefaultCursor}

	view := LockedView{t: t}
	size := t.screen().Size()
	t.frontend.RegionChanged(view, Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRRestore)
	t.frontend.CursorMoved(view, t.screen().CursorPos().X, t.screen().CursorPos().Y)
	t.frontend.StyleChanged(view, t.screen().Style())
	backend := t.backend
	sizeChanged := oldSize.X != size.X || oldSize.Y != size.Y
	var gen uint64
	if sizeChanged {
		t.sizeGen++
		gen = t.sizeGen
	}
	t.Unlock()

	if backend != nil && sizeChanged {
		return t.setBackendSize(size, gen)
	}
	return nil
}

// Size returns the terminal width and height. It locks internally.
func (t *terminal) Size() (w, h int) {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.Size()
}

// CursorPos returns the cursor position. It locks internally.
func (t *terminal) CursorPos() Pos {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.CursorPos()
}

func (t *terminal) Resize(w, h int) error {
	t.Lock()
	backend := t.backend
	// Shrinking the main screen would otherwise discard the top rows. Feed
	// them to scrollback and shift the surviving content up so the newest
	// output stays visible. The alt screen only resizes (as before). The
	// single full-screen CRResize below covers the repaint.
	oldH := t.mainScreen.Size().Y
	if h < oldH && !t.onAltScreen {
		delta := oldH - h
		lines, wrapped := t.mainScreen.takeTopRows(delta)
		t.captureScrollRows(lines, wrapped)
		// Keep the cursor on the same content after the shift.
		t.mainScreen.shiftCursorUp(delta)
	}
	t.mainScreen.setSize(w, h)
	t.altScreen.setSize(w, h)
	t.sizeGen++
	gen := t.sizeGen
	// setSize preserves content and emits nothing, so tell frontends
	// to repaint the full screen at the new dimensions.
	t.frontend.RegionChanged(LockedView{t: t}, Region{X: 0, Y: 0, X2: w, Y2: h}, CRResize)
	t.Unlock()

	if backend == nil {
		return nil
	}
	return t.setBackendSize(Pos{X: w, Y: h}, gen)
}

// setBackendSize applies a size to the backend only if gen is still the
// newest screen size. sizeMu serializes the calls so a slow stale SetSize
// cannot complete after a newer one.
func (t *terminal) setBackendSize(size Pos, gen uint64) error {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()

	t.Lock()
	current := t.sizeGen
	backend := t.backend
	t.Unlock()
	if backend == nil || current != gen {
		return nil
	}
	return backend.SetSize(size.X, size.Y)
}

func (t *terminal) startReadLoop() {
	t.startReadLoopFromState(ReaderState{Mode: t.textReadMode, Initial: true})
}

// startReadLoopFromState starts the read loop from a captured ReaderState.
// NewWithMode uses startReadLoop; NewFromHandoff uses this directly.
func (t *terminal) startReadLoopFromState(initial ReaderState) {
	t.lifecycleMu.Lock()
	started := t.readLoopStarted
	backend := t.backend
	if started || backend == nil || t.closed {
		t.lifecycleMu.Unlock()
		return
	}
	t.readLoopStarted = true
	t.lifecycleMu.Unlock()

	go func() {
		err := t.ptyReadLoop(initial)
		// io.EOF is a clean shutdown (e.g. NoPTYBackend at end of input);
		// report it as nil so Err() == nil on success.
		if errors.Is(err, io.EOF) {
			err = nil
		}
		t.lifecycleMu.Lock()
		t.readLoopErr = err
		t.lifecycleMu.Unlock()
		close(t.readLoopDone)
	}()
}

// Close shuts down the terminal: it closes the backend (if it implements
// io.Closer) so a blocked Read returns and the read loop exits promptly.
// It is idempotent and does not block waiting for the read loop; use Done
// to wait for loop exit and Err to see how it terminated.
func (t *terminal) Close() error {
	t.closeOnce.Do(func() {
		// Close must not take the terminal lock: it may be called from a
		// frontend callback (which already holds it) or while a callback is
		// running. lifecycleMu is only held briefly and never by callbacks.
		t.lifecycleMu.Lock()
		t.closed = true
		paused := t.paused
		resume := t.pauseResume
		started := t.readLoopStarted
		t.lifecycleMu.Unlock()

		// A paused loop is parked on pauseResume. Wake it with false
		// before closing the backend so it exits cleanly rather than
		// surfacing the backend's closed-pipe error.
		if paused && resume != nil {
			select {
			case resume <- false:
			default:
			}
		}

		if c, ok := t.backend.(io.Closer); ok {
			t.closeErr = c.Close()
		}
		// If the read loop was never started there is no goroutine to close
		// the done channel, so close it here. When the loop is running the
		// goroutine owns the close; startReadLoop refuses to start after
		// Close so it can never double-close.
		if !started {
			close(t.readLoopDone)
		}
	})
	return t.closeErr
}

// Done returns a channel that is closed when the read loop exits.
func (t *terminal) Done() <-chan struct{} {
	// Lock-free on purpose: watchdogs call this while another goroutine may
	// hold the terminal lock. readLoopDone is created in newTerminal and is
	// only ever closed.
	return t.readLoopDone
}

// Err returns the read-loop termination error: nil for a clean EOF,
// otherwise the error that stopped the loop. Only meaningful after Done
// is closed.
func (t *terminal) Err() error {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	return t.readLoopErr
}

// Line returns the plain text content of line y. It locks internally.
func (t *terminal) Line(y int) string {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.Line(y)
}

// ANSILine returns the ANSI-escaped content of line y. It locks internally.
func (t *terminal) ANSILine(y int) string {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.ANSILine(y)
}

// StyledLine returns the styled content of a line region. It locks internally.
func (t *terminal) StyledLine(x, w, y int) Line {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.StyledLine(x, w, y)
}

// StyledLines returns the styled content of multiple lines in a region. It
// locks internally.
func (t *terminal) StyledLines(r Region) []Line {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.StyledLines(r)
}

// PrintTerminal prints the terminal screen contents to stdout for debugging.
// It uses only the public Terminal interface, with proper locking.
func PrintTerminal(t Terminal) {
	PrintTerminalTo(os.Stdout, t)
}

// PrintTerminalTo prints the terminal screen contents to w for debugging.
// It uses only the public Terminal interface, with proper locking.
func PrintTerminalTo(w io.Writer, t Terminal) {
	t.View(func(v LockedView) {
		ww, h := v.Size()
		fmt.Fprint(w, "+")
		for i := 0; i < ww; i++ {
			fmt.Fprint(w, "-")
		}
		fmt.Fprintln(w, "+")
		for i := 0; i < h; i++ {
			lstr := v.ANSILine(i)
			lstr = strings.ReplaceAll(lstr, "\000", " ")
			fmt.Fprintf(w, "\033[m|%s\033[m|\n", lstr)
		}
		fmt.Fprint(w, "+")
		for i := 0; i < ww; i++ {
			fmt.Fprint(w, "-")
		}
		fmt.Fprintln(w, "+")
	})
}

type MouseBtn byte
type MouseFlag byte

const (
	// low 2 bits
	MBtn1     MouseBtn = 0
	MBtn2     MouseBtn = 1
	MBtn3     MouseBtn = 2
	MRelease  MouseBtn = 3
	mWhichBtn byte     = 3

	// flags
	MShift   MouseFlag = 4
	MMeta    MouseFlag = 8
	MControl MouseFlag = 16
	MMotion  MouseFlag = 32
	MWheel   MouseFlag = 64
)

// x and y should start at 1
// wheel events should use btn1 for wheel up, btn2 for wheel down, true for press, and M_wheel for mods
func (t *terminal) SendMouseRaw(btn MouseBtn, press bool, mods MouseFlag, x, y int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("termemu: mouse coordinates must be non-negative, got %d,%d", x, y)
	}
	t.Lock()
	mouseMode := t.viewInts[VIMouseMode]
	mouseEncoding := t.viewInts[VIMouseEncoding]
	altScroll := t.viewFlags[VFAltScroll] && t.onAltScreen
	t.Unlock()

	// Alternate scroll mode: on the alternate screen with no mouse reporting,
	// a wheel event is delivered as a cursor up/down key (xterm 1007).
	if mods&MWheel != 0 && mouseMode == MMNone {
		if !altScroll {
			return nil
		}
		code := KeyUp
		if byte(btn)&mWhichBtn == byte(MBtn2) {
			code = KeyDown
		}
		// #1007 only applies to press events.
		if !press {
			return nil
		}
		_, err := t.SendKey(KeyEvent{Code: code})
		return err
	}

	// Only the documented flag bits carry meaning. Mask stray bits so the
	// encoded button byte always fits the protocol (X10 has a single byte per
	// parameter, so bit 7 must not leak into it).
	mods &= MShift | MMeta | MControl | MMotion | MWheel

	switch mouseMode {
	case MMNone:
		return nil
	case MMPress:
		if !press {
			return nil
		}
	case MMPressRelease:
		if mods&MMotion != 0 {
			return nil
		}
	case MMPressReleaseMove:
		// 1002 reports motion only while a button is held. A motion event
		// with no button (X's button 3) belongs to 1003 (all-motion), so drop
		// it here.
		if mods&MMotion != 0 && btn&MRelease == MRelease {
			return nil
		}
	case MMPressReleaseMoveAll:
	}

	switch mouseEncoding {
	case MEX10:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}

		if 32+x > 255 {
			x = 255 - 32
		}
		if 32+y > 255 {
			y = 255 - 32
		}

		// X10 is a byte protocol: the parameters are raw bytes, not UTF-8
		// runes (encoding them as runes splits values above 0x7F).
		mouseCmd := []byte{ESC, '[', 'M', 32 + btnByte, byte(32 + x), byte(32 + y)}
		_, err := t.Write(mouseCmd)
		return err

	case MEUTF8:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}

		_, err := t.Write([]byte("\033[M" + string(32+btnByte) + string(rune(32+x)) + string(rune(32+y))))
		return err

	case MESGR:
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		pressByte := 'M'
		if !press {
			pressByte = 'm'
		}
		_, err := fmt.Fprintf(t, "\033[<%v;%v;%v%c", btnByte, x, y, pressByte)
		return err

	case MEURXVT:
		// urxvt's 1015 mode: decimal button/coordinates without the '<'
		// introducer, and release encoded in the button byte (like X10).
		btnByte := (byte(btn) & mWhichBtn) | byte(mods)
		if !press {
			btnByte |= byte(MRelease)
		}
		_, err := fmt.Fprintf(t, "\033[%v;%v;%vM", 32+btnByte, x, y)
		return err
	}
	return fmt.Errorf("termemu: unsupported mouse encoding %d", mouseEncoding)
}

func (t *terminal) setViewFlag(flag ViewFlag, value bool) {
	t.viewFlags[flag] = value
	t.frontend.ViewFlagChanged(LockedView{t: t}, flag, value)
}

// GetViewFlag returns a boolean terminal flag. It locks internally.
func (t *terminal) GetViewFlag(flag ViewFlag) bool {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.GetViewFlag(flag)
}

func (t *terminal) setViewInt(flag ViewInt, value int) {
	t.viewInts[flag] = value
	t.frontend.ViewIntChanged(LockedView{t: t}, flag, value)
}

// GetViewInt returns an integer terminal setting. It locks internally.
func (t *terminal) GetViewInt(flag ViewInt) int {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.GetViewInt(flag)
}

func (t *terminal) setViewString(flag ViewString, value string) {
	t.viewStrings[flag] = value
	t.frontend.ViewStringChanged(LockedView{t: t}, flag, value)
}

// GetViewString returns a string terminal setting. It locks internally.
func (t *terminal) GetViewString(flag ViewString) string {
	t.Lock()
	defer t.Unlock()
	return LockedView{t: t}.GetViewString(flag)
}

// Scrollback returns the terminal's scrollback buffer.
func (t *terminal) Scrollback() *Scrollback {
	return &t.scrollback
}

// scrollLinesDelta forwards a signed main-screen scroll delta to the
// frontend. Negative means content moved up (rows left the top); positive
// means content moved down. Called with the terminal lock held.
func (t *terminal) scrollLinesDelta(delta int) {
	t.frontend.ScrollLines(LockedView{t: t}, delta)
}

// scrollCapture is the callback wired to the screen's scrollCapture.
// Called with t.Lock held.
func (t *terminal) scrollCapture(lines []spanLine, wrapped []bool) {
	if t.onAltScreen {
		return
	}
	t.captureScrollRows(lines, wrapped)
}

// captureScrollRows appends rows to the scrollback buffer and notifies the
// optional listener. Unlike scrollCapture it ignores the alt-screen flag:
// resize captures belong to the main screen and must happen regardless of
// which screen is currently active. Caller must hold t.Lock.
func (t *terminal) captureScrollRows(lines []spanLine, wrapped []bool) {
	first := t.scrollNextID
	for i, sl := range lines {
		if i >= len(wrapped) {
			break
		}
		t.addScrollRow(sl.spans, sl.width, wrapped[i])
	}
	if t.scrollNextID == first {
		return
	}
	if listener, ok := t.frontend.(ScrollbackListener); ok {
		listener.ScrollbackRowsAppended(LockedView{t: t}, first, t.scrollNextID)
	}
}

// addScrollRow appends a physical row to the scrollback ring buffer.
// Caller must hold t.Lock.
func (t *terminal) addScrollRow(spans []Span, width int, wrapped bool) {
	t.addScrollRowWithID(t.scrollNextID, spans, width, wrapped)
	t.scrollNextID++
}

// addScrollRowWithID stores a row under an explicit ScrollID. Restore uses it to
// preserve snapshot IDs while rebuilding the arena. Caller must hold t.Lock.
func (t *terminal) addScrollRowWithID(id ScrollID, spans []Span, width int, wrapped bool) {
	idx := len(t.scrollRows)
	if idx < t.scrollMaxRows {
		t.scrollRows = append(t.scrollRows, scrollRow{})
	} else {
		idx = t.scrollBaseIdx
		t.scrollBaseIdx = (t.scrollBaseIdx + 1) % cap(t.scrollRows)
	}

	t.scrollRows[idx] = scrollRow{
		id:      id,
		spans:   t.storeScrollRowSpans(idx, spans),
		width:   width,
		wrapped: wrapped,
	}
}

// clearScrollbackLocked drops all stored scrollback rows while keeping
// ScrollIDs monotonic: Range afterwards reports base == next == scrollNextID
// and new rows continue from that counter. Caller must hold t.Lock.
// The slice is truncated (not reallocated) and the arena blocks are kept, so
// capacity and backing storage survive for later captures.
func (t *terminal) clearScrollbackLocked() {
	t.clearScrollArenaLocked()
	t.scrollRows = t.scrollRows[:0]
	t.scrollBaseIdx = 0
}

// returns the active screen (main or alt)
func (t *terminal) screen() screen {
	if t.onAltScreen {
		return t.altScreen
	}
	return t.mainScreen
}

// withLock calls f with the terminal locked. Internal use only.
func (t *terminal) withLock(f func()) {
	t.Lock()
	defer t.Unlock()
	f()
}

// setAltScreen switches to the alternate (wantAlt=true) or main screen.
// It is idempotent: requesting the current screen is a no-op.
func (t *terminal) setAltScreen(wantAlt bool) {
	if t.onAltScreen == wantAlt {
		return
	}
	// DEC private mode 1046 lets an application forbid the alternate screen.
	if wantAlt && !t.viewFlags[VFAltScreenAllowed] {
		return
	}
	from := t.screen()
	t.onAltScreen = wantAlt
	to := t.screen()
	// xterm's screen switch moves nothing: the cursor position and graphic
	// rendition carry over to the other screen. Saved cursors stay per-screen.
	to.copyTransientState(from)
	size := to.Size()
	t.frontend.RegionChanged(LockedView{t: t}, Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRScreenSwitch)
}

// enterAltScreen switches to the alternate screen. When clear is true the
// alt screen is erased and its cursor homed first (DECSET 1049 semantics);
// otherwise alt content and cursor are preserved (DECSET 47/1047 semantics).
// Re-entering the current screen is a no-op.
func (t *terminal) enterAltScreen(clear bool) {
	if t.onAltScreen {
		return
	}
	if !t.viewFlags[VFAltScreenAllowed] {
		return
	}
	if clear {
		// Clear the alt screen with the current cursor/style; the switch
		// itself carries them over below.
		t.altScreen.copyTransientState(t.screen())
		size := t.altScreen.Size()
		t.altScreen.eraseRegion(Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRClear)
	}
	t.setAltScreen(true)
}

// exitAltScreen switches back to the main screen (no-op if already there).
func (t *terminal) exitAltScreen() {
	t.setAltScreen(false)
}

// setHyperlink updates the current OSC 8 hyperlink URI (empty ends the
// link) and pushes it to both screens. Caller must hold t.Lock.
func (t *terminal) setHyperlink(uri string) {
	t.hyperlink = uri
	t.mainScreen.setLink(uri)
	t.altScreen.setLink(uri)
}

// resetTerminal implements RIS (ESC c): return to the main screen and reset
// both screens to a clean default state (cleared, cursor homed, default
// style, full scroll margins, auto-wrap on). Keyboard, pty, and view-flag
// state beyond the screens is left untouched.
// Any active OSC 8 hyperlink is ended.
func (t *terminal) resetTerminal() {
	t.setHyperlink("")
	t.setAltScreen(false)
	t.newlineMode = false
	t.setViewInt(VICursorShape, CursorShapeDefault)
	t.setViewFlag(VFAltScreenAllowed, true)
	t.palette = defaultPalette
	t.defaults = defaultDynamicColors
	for _, s := range []screen{t.mainScreen, t.altScreen} {
		size := s.Size()
		s.setStyle(NewStyle())
		s.setScrollMarginTopBottom(0, size.Y-1)
		s.SetAutoWrap(true)
		s.resetCharsets()
		s.SetInsertMode(false)
		s.SetOriginMode(false)
		s.resetTabStops()
		s.eraseRegion(Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRClear)
		s.setCursorPos(0, 0)
	}
}

// testHandleCommand is only for testing.
func (t *terminal) testHandleCommand(te *testing.T, cmd string) error {
	te.Helper()
	if !t.handleCommand(bufio.NewReader(strings.NewReader(cmd))) {
		return fmt.Errorf("handleCommand %q failed", cmd)
	}
	return nil
}
