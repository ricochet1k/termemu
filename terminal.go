package termemu

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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

	// Scrollback returns the terminal's scrollback buffer for ordinary
	// code. Its methods take the terminal lock themselves, so they must
	// not be called from inside View or a Frontend callback. Use
	// LockedView.Scrollback there instead.
	Scrollback() *Scrollback

	// Snapshot returns a deep copy of the terminal's screen state. It
	// locks internally. The scrollback buffer and backend/process state
	// are not included. Mutating the returned Snapshot does not affect
	// the terminal, and vice versa.
	Snapshot() Snapshot
	// Restore replaces the terminal's screen state with a previously
	// captured Snapshot and emits a full repaint. It locks internally.
	// Restore is same-size only: it returns ErrSnapshotSizeMismatch (and
	// leaves the terminal untouched) if the snapshot dimensions differ
	// from the current terminal size. The scrollback buffer is untouched.
	Restore(Snapshot) error

	// Close shuts down the terminal: it closes the backend (if it
	// implements io.Closer) so a blocked Read returns and the read loop
	// exits. It is idempotent and never blocks waiting for the loop.
	Close() error
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
	Width, Height int
	Lines         []Line // full-width rows; Spans are copies
	Wrapped       []bool // auto-wrap continuation flags, len == Height
	Cursor        Pos
	SavedCursor   Pos
	Style         Style
	TopMargin     int
	BottomMargin  int
	AutoWrap      bool
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
}

// LockedView is a read-only handle to terminal state. It is valid only for
// the duration of the call that received it (a Frontend callback or
// Terminal.View); do not retain it. Its methods do not lock.
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

type terminal struct {
	sync.Mutex

	frontend    Frontend
	onAltScreen bool
	mainScreen  screen
	altScreen   screen

	backend Backend

	viewFlags   []bool
	viewInts    []int
	viewStrings []string

	readLoopStarted bool
	readLoopDone    chan struct{}
	readLoopErr     error
	textReadMode    TextReadMode

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
	scrollNextID  ScrollID
	scrollBaseIdx int
	scrollMaxRows int
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
		scrollRows:    make([]scrollRow, 0, DefaultScrollbackRows),
		scrollNextID:  1,
		scrollMaxRows: DefaultScrollbackRows,
	}

	// Wire scroll capture to feed incoming rows into the scrollback buffer.
	t.mainScreen.setScrollCapture(t.scrollCapture)
	t.altScreen.setScrollCapture(t.scrollCapture)

	// Give screens a view so their frontend callbacks can read terminal
	// state without taking the lock again.
	view := LockedView{t: t}
	t.mainScreen.setView(view)
	t.altScreen.setView(view)

	return t
}

func (t *terminal) SetFrontend(f Frontend) {
	t.Lock()
	defer t.Unlock()
	t.frontend = f
	t.mainScreen.SetFrontend(f)
	t.altScreen.SetFrontend(f)
}

// func NewNoPTY(f Frontend) Terminal {
// 	return &terminal{
// 		frontend: f,
// 		screen: screen{
// 			frontend: f,
// 		},
// 	}
// }

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
	return Snapshot{
		Main:         t.mainScreen.snapshotState(),
		Alt:          t.altScreen.snapshotState(),
		OnAltScreen:  t.onAltScreen,
		ViewFlags:    append([]bool(nil), t.viewFlags...),
		ViewInts:     append([]int(nil), t.viewInts...),
		ViewStrings:  append([]string(nil), t.viewStrings...),
		KeyboardMain: cloneKeyboardMode(t.keyboardMain),
		KeyboardAlt:  cloneKeyboardMode(t.keyboardAlt),
		Link:         t.hyperlink,
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
// Snapshot and emits a full repaint. It locks internally. Restore is
// same-size only: it returns ErrSnapshotSizeMismatch and leaves the terminal
// untouched when the snapshot dimensions differ from the current terminal
// size. The scrollback buffer is untouched.
func (t *terminal) Restore(s Snapshot) error {
	t.Lock()
	defer t.Unlock()

	mainSize := t.mainScreen.Size()
	altSize := t.altScreen.Size()
	if s.Main.Width != mainSize.X || s.Main.Height != mainSize.Y ||
		s.Alt.Width != altSize.X || s.Alt.Height != altSize.Y {
		return ErrSnapshotSizeMismatch
	}

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

	view := LockedView{t: t}
	size := t.screen().Size()
	t.frontend.RegionChanged(view, Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRRestore)
	t.frontend.CursorMoved(view, t.screen().CursorPos().X, t.screen().CursorPos().Y)
	t.frontend.StyleChanged(view, t.screen().Style())
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
	// setSize preserves content and emits nothing, so tell frontends
	// to repaint the full screen at the new dimensions.
	t.frontend.RegionChanged(LockedView{t: t}, Region{X: 0, Y: 0, X2: w, Y2: h}, CRResize)
	t.Unlock()

	t.Lock()
	backend := t.backend
	t.Unlock()

	if backend == nil {
		return nil
	}

	return backend.SetSize(w, h)
}

func (t *terminal) startReadLoop() {
	t.Lock()
	started := t.readLoopStarted
	backend := t.backend
	if started || backend == nil || t.closed {
		t.Unlock()
		return
	}
	t.readLoopStarted = true
	if t.readLoopDone == nil {
		t.readLoopDone = make(chan struct{})
	}
	t.Unlock()

	go func() {
		err := t.ptyReadLoop()
		t.Lock()
		// io.EOF is a clean shutdown (e.g. NoPTYBackend at end of
		// input); report it as nil so Err() == nil on success.
		if errors.Is(err, io.EOF) {
			err = nil
		}
		t.readLoopErr = err
		t.Unlock()
		close(t.readLoopDone)
	}()
}

// Close shuts down the terminal: it closes the backend (if it implements
// io.Closer) so a blocked Read returns and the read loop exits promptly.
// It is idempotent and does not block waiting for the read loop; use Done
// to wait for loop exit and Err to see how it terminated.
func (t *terminal) Close() error {
	t.closeOnce.Do(func() {
		if c, ok := t.backend.(io.Closer); ok {
			t.closeErr = c.Close()
		}
		t.Lock()
		t.closed = true
		// If the read loop was never started there is no goroutine to
		// close the done channel, so close it here. When the loop is
		// running the goroutine owns the close; startReadLoop refuses to
		// start after Close so it can never double-close.
		if !t.readLoopStarted {
			if t.readLoopDone == nil {
				t.readLoopDone = make(chan struct{})
			}
			close(t.readLoopDone)
		}
		t.Unlock()
	})
	return t.closeErr
}

// Done returns a channel that is closed when the read loop exits.
func (t *terminal) Done() <-chan struct{} {
	t.Lock()
	defer t.Unlock()
	if t.readLoopDone == nil {
		t.readLoopDone = make(chan struct{})
	}
	return t.readLoopDone
}

// Err returns the read-loop termination error: nil for a clean EOF,
// otherwise the error that stopped the loop. Only meaningful after Done
// is closed.
func (t *terminal) Err() error {
	t.Lock()
	defer t.Unlock()
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
	switch t.viewInts[VIMouseMode] {
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
		if byte(mods)&mWhichBtn == byte(MRelease) {
			return nil
		}
	case MMPressReleaseMoveAll:
	}

	mouseEncoding := t.viewInts[VIMouseEncoding]
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

		mouseCmd := []byte("\033[M" + string(32+btnByte) + string(byte(32+x)) + string(byte(32+y)))
		_, err := t.Write(mouseCmd)
		if err != nil {
			panic(fmt.Sprintf("error %v", err))
		}
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
	}
	panic(fmt.Sprintf("Unhandled ViMouseEncoding?? %v", mouseEncoding))
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
	return &Scrollback{t: t}
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
	// Deep-copy spans since the screen will reuse them.
	spansCopy := make([]Span, len(spans))
	copy(spansCopy, spans)

	idx := len(t.scrollRows)
	if idx < t.scrollMaxRows {
		t.scrollRows = append(t.scrollRows, scrollRow{})
	} else {
		idx = t.scrollBaseIdx
		t.scrollBaseIdx = (t.scrollBaseIdx + 1) % cap(t.scrollRows)
	}

	t.scrollRows[idx] = scrollRow{
		id:      t.scrollNextID,
		spans:   spansCopy,
		width:   width,
		wrapped: wrapped,
	}
	t.scrollNextID++
}

// clearScrollbackLocked drops all stored scrollback rows while keeping
// ScrollIDs monotonic: Range afterwards reports base == next == scrollNextID
// and new rows continue from that counter. Caller must hold t.Lock.
// The slice is truncated (not reallocated) so capacity is preserved.
func (t *terminal) clearScrollbackLocked() {
	t.scrollRows = t.scrollRows[:0]
	t.scrollBaseIdx = 0
}

// returns screen and unlock fn, must defer the unlock fn
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
	t.onAltScreen = wantAlt
	size := t.screen().Size()
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
	if clear {
		size := t.altScreen.Size()
		t.altScreen.eraseRegion(Region{X: 0, Y: 0, X2: size.X, Y2: size.Y}, CRClear)
		t.altScreen.setCursorPos(0, 0)
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
	for _, s := range []screen{t.mainScreen, t.altScreen} {
		size := s.Size()
		s.setStyle(NewStyle())
		s.setScrollMarginTopBottom(0, size.Y-1)
		s.SetAutoWrap(true)
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
