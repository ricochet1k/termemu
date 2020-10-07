package termemu

// Region is an non-inclusive rectangle on the screen. X2 and Y2 are not included in the region.
type Region struct {
	X, Y, X2, Y2 int
}

func (r Region) Add(x, y int) Region {
	return Region{
		X:  r.X + x,
		X2: r.X2 + x,
		Y:  r.Y + y,
		Y2: r.Y2 + y,
	}
}

func (r Region) Clamp(rc Region) Region {
	nx := clamp(r.X, rc.X, rc.X2)
	ny := clamp(r.Y, rc.Y, rc.Y2)
	return Region{
		X:  nx,
		X2: clamp(r.X2, nx, rc.X2),
		Y:  ny,
		Y2: clamp(r.Y2, ny, rc.X2),
	}
}

// ViewFlag is an enum of boolean flags on a terminal
type ViewFlag int

const (
	VFBlinkCursor ViewFlag = iota
	VFShowCursor
	VFReportFocus
	VFBracketedPaste
	viewFlagCount
)

// ViewInt is an enum of integer settings on a terminal
type ViewInt int

const (
	VIMouseMode ViewInt = iota
	VIMouseEncoding
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

type MouseMode int

// Mouse modes for VI_MouseMode
const (
	MMNone MouseMode = iota
	MMPress
	MMPressRelease
	MMPressReleaseMove
	MMPressReleaseMoveAll
)

type MouseEncoding int32

// Mouse encodings for VI_MouseEncoding
const (
	MEX10 MouseEncoding = iota
	MEUTF8
	MESGR
	// MEURXVT // Not supported
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
