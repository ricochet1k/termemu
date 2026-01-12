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

	// CRRedraw means the application requested a redraw with RedrawAll
	CRRedraw
)

// Frontend is a type that can display data from a Terminal
type Frontend interface {
	Bell()
	RegionChanged(Region, ChangeReason)

	// ScrollLines is called when lines are about to be scrolled off the
	// top of the main (not alternate) screen. If you want to save to a scrollback
	// buffer, do it now.
	ScrollLines(y int)
	CursorMoved(x, y int)
	ColorsChanged(Color, Color)
	ViewFlagChanged(vs ViewFlag, value bool)
	ViewIntChanged(vs ViewInt, value int)
	ViewStringChanged(vs ViewString, value string)
}

// EmptyFrontend is a simple frontend that does nothing
type EmptyFrontend struct{}

func (d *EmptyFrontend) Bell()                                         {}
func (d *EmptyFrontend) RegionChanged(r Region, c ChangeReason)        {}
func (d *EmptyFrontend) CursorMoved(x, y int)                          {}
func (d *EmptyFrontend) ScrollLines(y int)                             {}
func (d *EmptyFrontend) ColorsChanged(f Color, b Color)                {}
func (d *EmptyFrontend) ViewFlagChanged(vs ViewFlag, value bool)       {}
func (d *EmptyFrontend) ViewIntChanged(vs ViewInt, value int)          {}
func (d *EmptyFrontend) ViewStringChanged(vs ViewString, value string) {}
