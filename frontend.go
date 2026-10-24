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
	// VFAltScroll tracks DECSET/DECRST 1007 (alternate scroll mode): on the
	// alternate screen, with no mouse mode active, wheel events are sent to
	// the application as cursor up/down keys instead of mouse reports.
	VFAltScroll
	// VFMousePixel tracks DECSET/DECRST 1016 (SGR-Pixels mouse reporting).
	// While set, the frontend should pass pixel coordinates to SendMouseRaw;
	// the coordinates are encoded verbatim (SGR-Pixels pairs with mode 1006).
	VFMousePixel
	// VFAltScreenAllowed tracks DEC private mode 1046. While clear, requests
	// to switch to the alternate screen (?47/?1047/?1049) are ignored.
	VFAltScreenAllowed
	// VFSynchronizedOutput tracks DECSET/DECRST 2026 (synchronized output).
	// While set, the application is emitting a batch of updates and
	// frontends may defer repaints until it is cleared. Damage batching
	// itself is opt-in through BatchFrontend.
	VFSynchronizedOutput
	viewFlagCount
)

// ViewInt is an enum of integer settings on a terminal
type ViewInt int

const (
	VIMouseMode ViewInt = iota
	VIMouseEncoding
	VIModifyOtherKeys
	// VICursorShape is DECSCUSR (CSI Ps SP q), stored as the raw Ps value.
	VICursorShape
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

// Cursor shapes for VICursorShape (DECSCUSR). The value is the DECSCUSR Ps
// parameter, so it can be forwarded to a terminal frontend unchanged.
const (
	CursorShapeDefault         int = 0 // terminal default (blinking block)
	CursorShapeBlinkBlock      int = 1
	CursorShapeSteadyBlock     int = 2
	CursorShapeBlinkUnderline  int = 3
	CursorShapeSteadyUnderline int = 4
	CursorShapeBlinkBar        int = 5
	CursorShapeSteadyBar       int = 6
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

	// CRPalette means the color palette or a default color changed (OSC
	// 4/10/11/12/104/110/111/112), so every cell rendered with a palette or
	// default color may look different. Frontends that resolve colors
	// themselves must repaint the full screen.
	CRPalette
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

// BatchFrontend is an optional Frontend extension for damage-batched
// rendering. A frontend that implements it still receives every callback per
// change, but may accumulate the damage and render only when the terminal
// calls the batch methods.
//
// PrepareBatch is called with the terminal lock held and receives a
// LockedView. It must render everything accumulated since the previous frame
// into frontend-owned memory and must not perform I/O. WriteBatch is called
// after the terminal lock is released and writes that memory; calls are
// serialized by the frontend and frame order is preserved.
//
// The terminal calls the pair at these boundaries: when the read loop is about
// to block waiting for more input (so a lone write is still painted
// immediately), before the loop parks for Pause, after out-of-band screen
// updates such as Resize and Restore, and whenever the loop otherwise stops.
// After either method returns, no damage may be left unrendered.
type BatchFrontend interface {
	PrepareBatch(v LockedView)
	WriteBatch()
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

// PromptMarkKind identifies one of the OSC 133 semantic prompt marks that
// shells and prompt frameworks emit for shell integration.
type PromptMarkKind int

const (
	// PromptMarkStart is OSC 133;A: the prompt is about to be drawn.
	PromptMarkStart PromptMarkKind = iota
	// PromptMarkInput is OSC 133;B: the prompt ended and user input starts.
	PromptMarkInput
	// PromptMarkCommand is OSC 133;C: the command was accepted and its
	// output starts.
	PromptMarkCommand
	// PromptMarkDone is OSC 133;D: the command finished. ExitCode carries
	// the reported status, or -1 when the sequence did not include one.
	PromptMarkDone
)

// PromptMark is a decoded OSC 133 shell-integration mark.
type PromptMark struct {
	Kind PromptMarkKind
	// ExitCode is the status reported by OSC 133;D, or -1 when absent (it is
	// always -1 for the other kinds).
	ExitCode int
	// Raw is the full parameter string that followed the kind letter, for
	// marks that carry extension data this package does not decode.
	Raw string
}

// ShellIntegration is an optional Frontend extension for OSC 133 semantic
// prompt marks (shell integration). It is called with the terminal lock held;
// read state through the view and do not call locking methods on the terminal.
// Unknown OSC 133 marks (for example 133;P properties) are not forwarded.
type ShellIntegration interface {
	PromptMark(v LockedView, mark PromptMark)
}

// NotificationSink is an optional Frontend extension for OSC 9 and OSC 777
// desktop notifications. OSC 9 carries a body only (title is empty); OSC 777
// carries "notify;title;body". It is called with the terminal lock held.
type NotificationSink interface {
	Notify(v LockedView, title, body string)
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
