package termemu

type Region struct {
	X, Y, X2, Y2 int
}

type ViewFlag int

const (
	VF_BlinkCursor ViewFlag = iota
	VF_ShowCursor
	VF_ReportFocus
	VF_BracketedPaste
	viewFlagCount
)

type ViewInt int

const (
	VI_MouseMode ViewInt = iota
	VI_MouseEncoding
	viewIntCount
)

type ViewString int

const (
	VS_WindowTitle ViewString = iota
	VS_CurrentDirectory
	VS_CurrentFile
	viewStringCount
)

// Mouse modes for VI_MouseMode
const (
	MM_None int = iota
	MM_Press
	MM_PressRelease
	MM_PressReleaseMove
	MM_PressReleaseMoveAll
)

// Mouse encodings for VI_MouseEncoding
const (
	ME_X10 int = iota
	ME_UTF8
	ME_SGR
)

type Frontend interface {
	Bell()
	RegionChanged(Region)
	CursorMoved(x, y int)
	ColorsChanged(Color, Color)
	ViewFlagChanged(vs ViewFlag, value bool)
	ViewIntChanged(vs ViewInt, value int)
	ViewStringChanged(vs ViewString, value string)
}

type EmptyFrontend struct{}

func (d *EmptyFrontend) Bell()                                         {}
func (d *EmptyFrontend) RegionChanged(r Region)                        {}
func (d *EmptyFrontend) CursorMoved(x, y int)                          {}
func (d *EmptyFrontend) ColorsChanged(f Color, b Color)                {}
func (d *EmptyFrontend) ViewFlagChanged(vs ViewFlag, value bool)       {}
func (d *EmptyFrontend) ViewIntChanged(vs ViewInt, value int)          {}
func (d *EmptyFrontend) ViewStringChanged(vs ViewString, value string) {}
