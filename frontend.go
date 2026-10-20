package termemu

// ViewFlag is an enum of boolean flags on a terminal
type ViewFlag int

const (
	VFBlinkCursor ViewFlag = iota
	VFShowCursor
	VFReportFocus
	VFBracketedPaste
	VFAppCursorKeys
	VFAppKeypad
	// VFSynchronizedOutput tracks DECSET/DECRST 2026 (synchronized output).
	// While set, the application is emitting a batch of updates and
	// frontends may defer repaints until it is cleared. The core performs
	// no batching itself; it only exposes the flag.
	VFSynchronizedOutput
	viewFlagCount
)

// ViewInt is an enum of integer settings on a terminal
type ViewInt int

const (
	VIMouseMode ViewInt = iota
	VIMouseEncoding
	VIModifyOtherKeys
	viewIntCount
)

// ViewString is an enum of string settings on a terminal
type ViewString int

const (
	VSWindowTitle ViewString = iota
	VSCurrentDirectory
	VSCurrentFile
	viewStringCount
)

// Mouse modes for VI_MouseMode
const (
	MMNone int = iota
	MMPress
	MMPressRelease
	MMPressReleaseMove
	MMPressReleaseMoveAll
)

// Mouse encodings for VI_MouseEncoding
const (
	MEX10 int = iota
	MEUTF8
	MESGR
)

// ChangeReason says what kind of change caused the region to change, for optimization etc.
type ChangeReason int

const (
	// CRText means text is being printed normally.
	CRText ChangeReason = iota

	// CRClear means some area has been cleared
	CRClear

	// CRScroll means an area has been scrolled
	CRScroll

	// CRScreenSwitch means the screen has been switched between main and alt
	CRScreenSwitch

	// CRResize means the screen was resized. Content is preserved where
	// possible (not cleared), so frontends must repaint the full region
	// at the new dimensions. A dedicated reason (rather than reusing
	// CRClear) lets frontends skip clear-specific work such as resetting
	// scroll state.
	CRResize
)

// Frontend is a type that can display data from a Terminal.
//
// All Frontend methods are called with the terminal lock held.
// Implementations may safely call terminal read methods (Size, Line,
// ANSILine, StyledLine, StyledLines) without acquiring additional locks.
// Implementations must not call terminal write methods or attempt to
// acquire the terminal lock, as this would cause deadlock. For the same
// reason they must not call Scrollback methods (which take the terminal
// lock themselves); buffer scrollback rows from ScrollLines instead.
type Frontend interface {
	Bell()
	RegionChanged(Region, ChangeReason)

	// ScrollLines is called when lines are about to be scrolled off the
	// top of the main (not alternate) screen. If you want to save to a scrollback
	// buffer, do it now.
	ScrollLines(y int)
	CursorMoved(x, y int)
	StyleChanged(s Style)
	ViewFlagChanged(vs ViewFlag, value bool)
	ViewIntChanged(vs ViewInt, value int)
	ViewStringChanged(vs ViewString, value string)
}

// ClipboardSink is an optional Frontend extension for the OSC 52 clipboard
// write path. If the Frontend implements it, the base64-decoded payload of
// an OSC 52 sequence is forwarded via Clipboard with the requested
// selection (usually "c") and the raw bytes.
//
// Clipboard is called with the terminal lock held, so implementations must
// not call back into the terminal (including Scrollback methods, which take
// the lock themselves). Clipboard queries (data == "?") are never answered
// or forwarded.
type ClipboardSink interface {
	Clipboard(selection string, data []byte)
}

// EmptyFrontend is a simple frontend that does nothing
type EmptyFrontend struct{}

func (d *EmptyFrontend) Bell()                                         {}
func (d *EmptyFrontend) RegionChanged(r Region, c ChangeReason)        {}
func (d *EmptyFrontend) CursorMoved(x, y int)                          {}
func (d *EmptyFrontend) ScrollLines(y int)                             {}
func (d *EmptyFrontend) StyleChanged(s Style)                          {}
func (d *EmptyFrontend) ViewFlagChanged(vs ViewFlag, value bool)       {}
func (d *EmptyFrontend) ViewIntChanged(vs ViewInt, value int)          {}
func (d *EmptyFrontend) ViewStringChanged(vs ViewString, value string) {}
