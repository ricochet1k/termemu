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

// Intersect returns the overlapping portion of two regions.
func (r Region) Intersect(o Region) Region {
	if r.X < o.X {
		r.X = o.X
	}
	if r.Y < o.Y {
		r.Y = o.Y
	}
	if r.X2 > o.X2 {
		r.X2 = o.X2
	}
	if r.Y2 > o.Y2 {
		r.Y2 = o.Y2
	}
	return r
}

// Empty reports whether the region has no area.
func (r Region) Empty() bool {
	return r.X >= r.X2 || r.Y >= r.Y2
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
