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
	MEURXVT
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

	// CRRestore means a snapshot was restored with Terminal.Restore.
	// Frontends must repaint the full screen from the restored state.
	CRRestore
)

// Frontend is a type that can display data from a Terminal.
//
// All Frontend methods are called with the terminal lock held and receive a
// LockedView as their first argument. The view is read-only and valid only
// for the duration of the callback: use it to read terminal state (Size,
// Line, Scrollback, ...) without locking. Do not retain it, and do not call
// Terminal write methods or read methods that take the lock themselves
// (plain Size/Line/Snapshot/... or the Terminal.Scrollback reader) from a
// callback, as that would deadlock. Use v.Snapshot() and
// v.Scrollback().Snapshot() to snapshot screen or scrollback state from a
// callback; Terminal.Snapshot and *Scrollback.Snapshot are for ordinary
// code.
type Frontend interface {
	Bell(v LockedView)
	RegionChanged(v LockedView, r Region, c ChangeReason)

	// ScrollLines is called for every scroll of the main (not alternate)
	// screen with the signed delta. Negative means content moved up (rows
	// left the top); positive means content moved down. Scrollback capture
	// and ScrollbackRowsAppended are separate.
	ScrollLines(v LockedView, delta int)
	CursorMoved(v LockedView, x, y int)
	StyleChanged(v LockedView, s Style)
	ViewFlagChanged(v LockedView, vs ViewFlag, value bool)
	ViewIntChanged(v LockedView, vs ViewInt, value int)
	ViewStringChanged(v LockedView, vs ViewString, value string)
}

// ClipboardSink is an optional Frontend extension for the OSC 52 clipboard
// write path. If the Frontend implements it, the base64-decoded payload of
// an OSC 52 sequence is forwarded via Clipboard with the requested
// selection (usually "c") and the raw bytes.
//
// Clipboard is called with the terminal lock held and receives a LockedView,
// so implementations should read terminal state through the view and must
// not call back into the terminal with locking methods. Clipboard queries
// (data == "?") are never answered or forwarded.
type ClipboardSink interface {
	Clipboard(v LockedView, selection string, data []byte)
}

// ScrollbackListener is an optional Frontend extension. When rows are
// appended to the main-screen scrollback, ScrollbackRowsAppended is called
// once per capture batch with the terminal lock held and the range
// [first, next) of newly assigned ScrollIDs. Use the LockedView (including
// v.Scrollback()) to read the rows; do not retain the view. Alternate-screen
// output never produces rows. Scrollback.Clear() does not call this.
type ScrollbackListener interface {
	ScrollbackRowsAppended(v LockedView, first, next ScrollID)
}

// EmptyFrontend is a simple frontend that does nothing
type EmptyFrontend struct{}

func (d *EmptyFrontend) Bell(v LockedView)                                           {}
func (d *EmptyFrontend) RegionChanged(v LockedView, r Region, c ChangeReason)        {}
func (d *EmptyFrontend) CursorMoved(v LockedView, x, y int)                          {}
func (d *EmptyFrontend) ScrollLines(v LockedView, delta int)                         {}
func (d *EmptyFrontend) StyleChanged(v LockedView, s Style)                          {}
func (d *EmptyFrontend) ViewFlagChanged(v LockedView, vs ViewFlag, value bool)       {}
func (d *EmptyFrontend) ViewIntChanged(v LockedView, vs ViewInt, value int)          {}
func (d *EmptyFrontend) ViewStringChanged(v LockedView, vs ViewString, value string) {}
