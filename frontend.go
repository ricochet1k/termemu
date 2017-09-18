package termemu

type Region struct {
	X, Y, X2, Y2 int
}

type Frontend interface {
	Bell()
	RegionChanged(Region)
	CursorMoved(x, y int)
	ColorsChanged(Color, Color)
}

type dummyFrontend struct{}

func (d *dummyFrontend) Bell()                          {}
func (d *dummyFrontend) RegionChanged(r Region)         {}
func (d *dummyFrontend) CursorMoved(x, y int)           {}
func (d *dummyFrontend) ColorsChanged(f Color, b Color) {}
